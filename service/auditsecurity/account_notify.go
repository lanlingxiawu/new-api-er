package auditsecurity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// No address or credential is persisted in personal delivery records.
func accountNotificationDestination(user model.User) (dto.UserSetting, string) {
	s := user.GetSetting()
	if s.AccountSecurityNotifyEnabled == nil || !*s.AccountSecurityNotifyEnabled {
		return s, ""
	}
	if s.NotifyType == "" {
		s.NotifyType = dto.NotifyTypeEmail
	}
	var destination string
	switch s.NotifyType {
	case dto.NotifyTypeEmail:
		destination = s.NotificationEmail
		if destination == "" {
			destination = user.Email
		}
	case dto.NotifyTypeWebhook:
		if s.WebhookUrl != "" {
			destination = s.WebhookUrl + "\x00" + s.WebhookSecret
		}
	case dto.NotifyTypeBark:
		destination = s.BarkUrl
	case dto.NotifyTypeGotify:
		if s.GotifyUrl != "" && s.GotifyToken != "" {
			destination = s.GotifyUrl + "\x00" + s.GotifyToken
		}
	}
	if destination == "" {
		return s, ""
	}
	return s, model.SecurityAccountRef("account-notification-destination-v1:" + s.NotifyType + ":" + destination)
}

func accountNotificationNextAttempt(db *gorm.DB, alertID int64, userID int, cooldown int, now int64) (int64, error) {
	var latest model.SecurityDelivery
	err := db.Select("sent_at").Where("alert_id = ? AND recipient_user_id = ? AND sent_at > ?", alertID, userID, now-int64(cooldown)).Order("sent_at DESC").Limit(1).Find(&latest).Error
	return max(now, latest.SentAt+int64(cooldown)), err
}

func queueAccountNotification(tx *gorm.DB, alert model.SecurityAlert, identity string, cfg operation_setting.AuditSecuritySetting, now int64, response bool) error {
	allowed, err := accountAlertAccessible(tx, alert, alert.ActorID, true)
	if err != nil || !allowed || alert.PurgeState != "" {
		return err
	}
	var user model.User
	err = tx.Select("id,email,setting").Where("id = ?", alert.ActorID).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, destination := accountNotificationDestination(user)
	if destination == "" {
		return nil
	}
	// Coalesce changes only while a delivery is pending. A delivery already
	// sending owns its snapshot, so a later result needs a separate receipt.
	var recent []model.SecurityDelivery
	if err := tx.Select("id,status,created_at,sent_at,destination").Where("alert_id = ? AND recipient_user_id = ? AND created_at >= ?", alert.ID, user.Id, now-86400).Order("created_at DESC").Limit(100).Find(&recent).Error; err != nil {
		return err
	}
	next, err := accountNotificationNextAttempt(tx, alert.ID, user.Id, cfg.NotificationCooldownSeconds, now)
	if err != nil {
		return err
	}
	for _, row := range recent {
		if row.Status == "pending" && row.Destination == destination {
			return nil
		}
		if row.Status == "sending" {
			next = max(next, max(row.SentAt, row.CreatedAt)+int64(cfg.NotificationCooldownSeconds))
		}
	}
	if !response && next > now {
		return nil
	}
	key := model.SecurityAccountRef(fmt.Sprintf("account-notification-v1:%d:%d:%s", alert.ID, user.Id, identity))
	d := model.SecurityDelivery{AlertID: alert.ID, FindingID: alert.LatestFindingID, Version: alert.Version, RecipientUserID: user.Id, DeliveryKey: &key, Kind: "account", Destination: destination, Status: "pending", CreatedAt: now, UpdatedAt: now, NextAttemptAt: next}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&d).Error
}

func (w *worker) queueAccountResponseNotifications(ctx context.Context, cfg operation_setting.AuditSecuritySetting) error {
	var actions []model.SecurityAction
	if err := w.db.WithContext(ctx).Select("id,alert_id,target_user_id,response_group_id").Where("account_notify_pending = ? AND outcome NOT IN ?", true, []string{"pending", "committed", "sync_pending", "clearing"}).Order("id").Limit(20).Find(&actions).Error; err != nil {
		return err
	}
	for _, action := range actions {
		if err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			r := tx.Model(&model.SecurityAction{}).Where("id = ? AND account_notify_pending = ?", action.ID, true).Update("account_notify_pending", false)
			if r.Error != nil || r.RowsAffected == 0 {
				return r.Error
			}
			now := time.Now().Unix()
			var after int64
			for {
				query := tx.Omit("evidence").Where("actor_id = ? AND id > ?", action.TargetUserID, after)
				if action.ResponseGroupID == nil {
					query = query.Where("id = ?", action.AlertID)
				} else {
					linked := tx.Model(&model.SecurityResponseLink{}).Select("alert_id").Where("group_id = ?", *action.ResponseGroupID)
					query = query.Where("id = ? OR id IN (?)", action.AlertID, linked)
				}
				var alerts []model.SecurityAlert
				if err := query.Order("id").Limit(100).Find(&alerts).Error; err != nil {
					return err
				}
				for _, alert := range alerts {
					if err := queueAccountNotification(tx, alert, fmt.Sprintf("response:%d", action.ID), cfg, now, true); err != nil {
						return err
					}
					if err := touchAlertActivity(tx, alert.ID, now); err != nil {
						return err
					}
				}
				if len(alerts) < 100 {
					return nil
				}
				after = alerts[len(alerts)-1].ID
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *worker) deliverAccountNotifications(ctx context.Context) error {
	// Bound the entire personal batch so slow user endpoints leave time for
	// administrator notifications in the shared worker cycle.
	deliveryDeadline := time.Now().Add(8 * time.Second)
	var deliveries []model.SecurityDelivery
	if err := w.db.WithContext(ctx).Where("status = ? AND next_attempt_at <= ? AND kind = ?", "pending", time.Now().Unix(), "account").Order("next_attempt_at,id").Limit(3).Find(&deliveries).Error; err != nil {
		return err
	}
	for _, d := range deliveries {
		if time.Now().After(deliveryDeadline) {
			return nil
		}
		cfg := operation_setting.GetAuditSecuritySetting()
		if ctx.Err() != nil || !cfg.Enabled || cfg.Suspended {
			return ctx.Err()
		}
		var user model.User
		err := w.db.WithContext(ctx).Select("id,email,setting").Where("id = ?", d.RecipientUserID).Take(&user).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		settings, destination := accountNotificationDestination(user)
		if err != nil || destination == "" || destination != d.Destination {
			if err := updateDeliveryLifecycle(ctx, w.db, d, map[string]any{"status": "cancelled", "wait_reason": "destination_removed"}); err != nil {
				return err
			}
			continue
		}
		detail, err := GetSelfSecurityAlert(ctx, w.db, user.Id, d.AlertID)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := updateDeliveryLifecycle(ctx, w.db, d, map[string]any{"status": "cancelled", "wait_reason": "alert_unavailable"}); err != nil {
				return err
			}
			continue
		}
		waiting := false
		for _, response := range detail.Responses {
			waiting = waiting || slices.Contains([]string{"pending", "committed", "sync_pending", "clearing"}, response.Outcome)
		}
		if waiting {
			if err := updateDeliveryLifecycle(ctx, w.db, d, map[string]any{"next_attempt_at": time.Now().Unix() + 10, "wait_reason": "account_response"}); err != nil {
				return err
			}
			continue
		}
		// An old queued delivery may have just succeeded. Check actual send time
		// again so a recovered backlog cannot bypass the cooldown.
		next, err := accountNotificationNextAttempt(w.db.WithContext(ctx), d.AlertID, user.Id, cfg.NotificationCooldownSeconds, time.Now().Unix())
		if err != nil {
			return err
		}
		if next > time.Now().Unix() {
			if err := updateDeliveryLifecycle(ctx, w.db, d, map[string]any{"next_attempt_at": next, "wait_reason": "cooldown"}); err != nil {
				return err
			}
			continue
		}
		claimed, err := claimDelivery(ctx, w.db, d)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		payload, err := common.Marshal(struct {
			Alert     SelfSecurityAlert      `json:"alert"`
			Responses []SelfSecurityResponse `json:"responses"`
		}{detail.Alert, detail.Responses})
		if err != nil {
			return err
		}
		data := dto.NewNotify("account_security", i18n.Translate(settings.Language, i18n.MsgAuditSecuritySubject, map[string]any{"ID": d.AlertID}), i18n.Translate(settings.Language, i18n.MsgAuditSecurityAccountBody)+"\n"+string(payload), nil)
		sendCtx, cancel := context.WithDeadline(ctx, deliveryDeadline)
		if common.RedisEnabled && w.credentialCache == nil && common.RDB != nil {
			options := *common.RDB.Options()
			options.PoolSize, options.MinIdleConns, options.MaxRetries = 1, 0, -1
			options.PoolTimeout, options.DialTimeout = 200*time.Millisecond, 200*time.Millisecond
			options.ReadTimeout, options.WriteTimeout = 200*time.Millisecond, 200*time.Millisecond
			w.credentialCache = redis.NewClient(&options)
		}
		allowed, err := service.CheckNotificationLimitContext(sendCtx, w.credentialCache, user.Id, "account_security")
		if err == nil && allowed {
			err = sendAccountNotification(sendCtx, user, settings, data, d.ID)
		}
		cancel()
		updates := map[string]any{"wait_reason": "", "error_code": "", "next_attempt_at": 0}
		if err == nil && !allowed {
			updates["status"], updates["wait_reason"], updates["next_attempt_at"] = "pending", "rate_limited", time.Now().Unix()+3600
		} else if err == nil {
			updates["status"], updates["sent_at"], updates["attempts"] = "sent", time.Now().Unix(), d.Attempts+1
		} else {
			common.SysError("account security notification delivery failed")
			d.Attempts++
			updates["status"], updates["error_code"], updates["attempts"], updates["next_attempt_at"] = "pending", "delivery_failed", d.Attempts, time.Now().Unix()+int64(60*d.Attempts*d.Attempts)
			if d.Attempts >= 5 {
				updates["status"] = "failed"
			}
		}
		if err := updateDeliveryLifecycle(ctx, w.db, d, updates); err != nil {
			return err
		}
	}
	return nil
}

// Keep destination material out of log messages, even when a transport error
// contains a Bark path or Gotify token.
func accountNotificationContent(data dto.Notify) string {
	content := data.Content
	for _, value := range data.Values {
		content = strings.Replace(content, dto.ContentValueParam, fmt.Sprint(value), 1)
	}
	return content
}
