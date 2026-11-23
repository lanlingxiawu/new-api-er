package auditsecurity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/logger"
)

func TestPersonalNotificationConsentCoalescingAndResponseRecovery(t *testing.T) {
	db, alert := assignmentFixture(t)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	now := time.Now().Unix()
	require.NoError(t, queueAccountNotification(db, alert, "finding:1", cfg, now, false))
	var count int64
	require.NoError(t, db.Model(&model.SecurityDelivery{}).Count(&count).Error)
	require.Zero(t, count)
	yes := true
	raw, err := common.Marshal(dto.UserSetting{AccountSecurityNotifyEnabled: &yes, NotifyType: "email", NotificationEmail: "self@example.com"})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("setting", string(raw)).Error)
	for _, key := range []string{"finding:1", "finding:1", "finding:2"} {
		require.NoError(t, queueAccountNotification(db, alert, key, cfg, now, false))
	}
	var deliveries []model.SecurityDelivery
	require.NoError(t, db.Find(&deliveries).Error)
	require.Len(t, deliveries, 1)
	require.Equal(t, "account", deliveries[0].Kind)
	require.Equal(t, 3, deliveries[0].RecipientUserID)
	require.NotContains(t, deliveries[0].Destination, "example.com")
	require.NoError(t, db.Model(&deliveries[0]).Updates(map[string]any{"status": "sent", "sent_at": now}).Error)
	require.NoError(t, queueAccountNotification(db, alert, "finding:3", cfg, now+1, false))
	action := model.SecurityAction{AlertID: alert.ID, TargetUserID: 3, Action: "disable_account", Outcome: "succeeded", CreatedAt: now, AccountNotifyPending: true}
	require.NoError(t, db.Create(&action).Error)
	w := worker{db: db}
	for range 2 {
		require.NoError(t, w.queueAccountResponseNotifications(t.Context(), cfg))
	}
	require.NoError(t, db.Order("id").Find(&deliveries).Error)
	require.Len(t, deliveries, 2)
	require.Equal(t, now+int64(cfg.NotificationCooldownSeconds), deliveries[1].NextAttemptAt)
	require.NoError(t, db.First(&action, action.ID).Error)
	require.False(t, action.AccountNotifyPending)
	alert.ActorID, alert.ActorRole, alert.Rule = 4, 1, "user.delete"
	require.NoError(t, queueAccountNotification(db, alert, "management", cfg, now, true))
	require.NoError(t, db.Model(&model.SecurityDelivery{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestPersonalNotificationLinkedResponseGroup(t *testing.T) {
	db, canonical := assignmentFixture(t)
	linked := canonical
	linked.ID = 0
	linked.Rule, linked.RuleVersion = "session_reuse", 2
	require.NoError(t, db.Create(&linked).Error)
	group := model.SecurityResponseGroup{TriggerKey: "notification-linked-response", TargetUserID: 3, State: "active"}
	require.NoError(t, db.Create(&group).Error)
	require.NoError(t, db.Create(&[]model.SecurityResponseLink{{AlertID: canonical.ID, GroupID: group.ID}, {AlertID: linked.ID, GroupID: group.ID}}).Error)
	action := model.SecurityAction{AlertID: canonical.ID, ResponseGroupID: &group.ID, TargetUserID: 3, Action: "clear_api_keys", Outcome: "clearing", AccountNotifyPending: true}
	require.NoError(t, db.Create(&action).Error)
	previous := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": "true", "suspended": "false"}))
	t.Cleanup(func() {
		_ = config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": strconv.FormatBool(previous.Enabled), "suspended": strconv.FormatBool(previous.Suspended)})
	})
	oldFetch := *system_setting.GetFetchSetting()
	system_setting.ReplaceFetchSetting(system_setting.FetchSetting{EnableSSRFProtection: false})
	t.Cleanup(func() { system_setting.ReplaceFetchSetting(oldFetch) })
	oldRedis, oldCount, oldWorker := common.RedisEnabled, constant.NotifyLimitCount, system_setting.WorkerUrl
	common.RedisEnabled, constant.NotifyLimitCount, system_setting.WorkerUrl = false, 10000, ""
	t.Cleanup(func() {
		common.RedisEnabled, constant.NotifyLimitCount, system_setting.WorkerUrl = oldRedis, oldCount, oldWorker
	})
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	yes := true
	raw, err := common.Marshal(dto.UserSetting{AccountSecurityNotifyEnabled: &yes, NotifyType: "webhook", WebhookUrl: server.URL})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("setting", string(raw)).Error)
	cfg := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, queueAccountNotification(db, linked, "linked-finding", cfg, time.Now().Unix(), false))
	w := worker{db: db}
	require.NoError(t, w.deliverAccountNotifications(t.Context()))
	require.Zero(t, calls.Load(), "wait for the response owned by the shared group")

	// A terminal response must schedule every linked alert, including one whose
	// previous finding notification already left the pending state.
	require.NoError(t, db.Model(&model.SecurityDelivery{}).Where("alert_id = ?", linked.ID).Updates(map[string]any{"status": "sent", "sent_at": time.Now().Unix()}).Error)
	require.NoError(t, db.Model(&action).Update("outcome", "succeeded").Error)
	for range 2 {
		require.NoError(t, w.queueAccountResponseNotifications(t.Context(), cfg))
	}
	var pending []model.SecurityDelivery
	require.NoError(t, db.Where("status = ?", "pending").Order("alert_id").Find(&pending).Error)
	require.Len(t, pending, 2)
	require.Equal(t, canonical.ID, pending[0].AlertID)
	require.Equal(t, linked.ID, pending[1].AlertID)
	for _, d := range pending {
		require.Equal(t, 3, d.RecipientUserID)
	}
}

func TestPersonalNotificationBacklogCooldownAndRetryLimit(t *testing.T) {
	db, alert := assignmentFixture(t)
	previous := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": "true", "suspended": "false"}))
	t.Cleanup(func() {
		_ = config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": strconv.FormatBool(previous.Enabled), "suspended": strconv.FormatBool(previous.Suspended)})
	})
	oldLimit, oldDuration, oldRedis := constant.NotifyLimitCount, constant.NotificationLimitDurationMinute, common.RedisEnabled
	constant.NotifyLimitCount, constant.NotificationLimitDurationMinute, common.RedisEnabled = 1000, 60, false
	t.Cleanup(func() {
		constant.NotifyLimitCount, constant.NotificationLimitDurationMinute, common.RedisEnabled = oldLimit, oldDuration, oldRedis
	})
	oldFetch := *system_setting.GetFetchSetting()
	system_setting.ReplaceFetchSetting(system_setting.FetchSetting{EnableSSRFProtection: false})
	t.Cleanup(func() { system_setting.ReplaceFetchSetting(oldFetch) })
	var calls atomic.Int64
	var stall atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		if stall.Load() {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	yes := true
	raw, err := common.Marshal(dto.UserSetting{AccountSecurityNotifyEnabled: &yes, NotifyType: "webhook", WebhookUrl: server.URL})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("setting", string(raw)).Error)
	cfg := operation_setting.GetAuditSecuritySetting()
	now := time.Now().Unix()
	old := model.SecurityDelivery{AlertID: alert.ID, RecipientUserID: 3, Kind: "account", Status: "sent", CreatedAt: now - 3*86400, SentAt: now}
	require.NoError(t, db.Create(&old).Error)
	require.NoError(t, queueAccountNotification(db, alert, "repeat", cfg, now, false))
	var count int64
	require.NoError(t, db.Model(&model.SecurityDelivery{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "actual send time controls cooldown, even for old queued messages")
	require.NoError(t, queueAccountNotification(db, alert, "response", cfg, now, true))
	var pending model.SecurityDelivery
	require.NoError(t, db.Order("id DESC").First(&pending).Error)
	require.Equal(t, now+int64(cfg.NotificationCooldownSeconds), pending.NextAttemptAt)
	require.NoError(t, db.Model(&pending).Update("next_attempt_at", 0).Error)
	w := worker{db: db}
	require.NoError(t, w.deliverAccountNotifications(t.Context()))
	require.Zero(t, calls.Load(), "delivery rechecks cooldown before contacting the endpoint")
	require.NoError(t, db.First(&pending, pending.ID).Error)
	require.Equal(t, "cooldown", pending.WaitReason)
	require.NoError(t, db.Model(&old).Update("sent_at", now-int64(cfg.NotificationCooldownSeconds)-1).Error)
	constant.NotifyLimitCount = 0
	require.NoError(t, db.Model(&pending).Update("next_attempt_at", 0).Error)
	require.NoError(t, w.deliverAccountNotifications(t.Context()))
	require.NoError(t, db.First(&pending, pending.ID).Error)
	require.Equal(t, "rate_limited", pending.WaitReason)
	require.Zero(t, pending.Attempts)
	require.Zero(t, calls.Load())
	constant.NotifyLimitCount = 1000
	for attempt := 1; attempt <= 5; attempt++ {
		require.NoError(t, db.Model(&pending).Update("next_attempt_at", 0).Error)
		require.NoError(t, w.deliverAccountNotifications(t.Context()))
		require.NoError(t, db.First(&pending, pending.ID).Error)
		require.Equal(t, attempt, pending.Attempts)
		require.Equal(t, "delivery_failed", pending.ErrorCode)
		if attempt < 5 {
			require.Equal(t, "pending", pending.Status)
			require.GreaterOrEqual(t, pending.NextAttemptAt, time.Now().Unix()+int64(60*attempt*attempt)-1)
		} else {
			require.Equal(t, "failed", pending.Status)
		}
	}
	require.EqualValues(t, 5, calls.Load())
	stall.Store(true)
	for range 2 {
		require.NoError(t, db.Create(&model.SecurityDelivery{AlertID: alert.ID, RecipientUserID: 3, Kind: "account", Status: "pending", Destination: pending.Destination, CreatedAt: now}).Error)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	require.NoError(t, w.deliverAccountNotifications(ctx))
	require.NoError(t, ctx.Err(), "slow personal destinations must leave time for administrator notifications")
	require.EqualValues(t, 6, calls.Load(), "one slow endpoint consumes the bounded batch budget")
	var untouched int64
	require.NoError(t, db.Model(&model.SecurityDelivery{}).Where("status = ? AND attempts = ?", "pending", 0).Count(&untouched).Error)
	require.EqualValues(t, 1, untouched)
}

func TestPersonalNotificationDeliveryRechecksConsentAndIsIndependent(t *testing.T) {
	db, alert := assignmentFixture(t)
	oldLimit, oldDuration := constant.NotifyLimitCount, constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount, constant.NotificationLimitDurationMinute = 100, 60
	t.Cleanup(func() { constant.NotifyLimitCount, constant.NotificationLimitDurationMinute = oldLimit, oldDuration })
	previous := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": "true", "suspended": "false", "notification_enabled": "false"}))
	t.Cleanup(func() {
		_ = config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": strconv.FormatBool(previous.Enabled), "suspended": strconv.FormatBool(previous.Suspended), "notification_enabled": strconv.FormatBool(previous.NotificationEnabled)})
	})
	oldFetch := *system_setting.GetFetchSetting()
	system_setting.ReplaceFetchSetting(system_setting.FetchSetting{EnableSSRFProtection: false})
	t.Cleanup(func() { system_setting.ReplaceFetchSetting(oldFetch) })
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(204)
	}))
	defer server.Close()
	yes, no := true, false
	settings := dto.UserSetting{AccountSecurityNotifyEnabled: &yes, NotifyType: "webhook", WebhookUrl: server.URL}
	raw, err := common.Marshal(settings)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Updates(map[string]any{"setting": string(raw), "status": 2}).Error)
	cfg := operation_setting.GetAuditSecuritySetting()
	w := worker{db: db}
	for i, change := range []string{"send", "unsubscribe", "address", "delete"} {
		body = ""
		raw, err = common.Marshal(settings)
		require.NoError(t, err)
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("setting", string(raw)).Error)
		require.NoError(t, queueAccountNotification(db, alert, change, cfg, time.Now().Unix()+int64(i)*90000, true))
		var d model.SecurityDelivery
		require.NoError(t, db.Order("id DESC").First(&d).Error)
		require.NoError(t, db.Model(&d).Update("next_attempt_at", 0).Error)
		switch change {
		case "unsubscribe":
			updated := settings
			updated.AccountSecurityNotifyEnabled = &no
			raw, err = common.Marshal(updated)
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("setting", string(raw)).Error)
		case "address":
			updated := settings
			updated.WebhookUrl = server.URL + "/changed"
			raw, err = common.Marshal(updated)
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("setting", string(raw)).Error)
		case "delete":
			require.NoError(t, db.Where("id = ?", 3).Delete(&model.User{}).Error)
		}
		require.NoError(t, w.deliver(t.Context(), cfg))
		require.NoError(t, db.First(&d, d.ID).Error)
		if change == "send" {
			require.Equal(t, "sent", d.Status)
			require.Contains(t, body, "account_security")
			require.NotContains(t, body, "assignee")
			require.NotContains(t, body, "destination")
			require.NotContains(t, body, "conclusion")
		} else {
			require.Equal(t, "cancelled", d.Status)
			require.Empty(t, body)
		}
	}
}

func TestPersonalNotificationTransportProtocolsAndCancellation(t *testing.T) {
	oldFetch := *system_setting.GetFetchSetting()
	system_setting.ReplaceFetchSetting(system_setting.FetchSetting{EnableSSRFProtection: false})
	t.Cleanup(func() { system_setting.ReplaceFetchSetting(oldFetch) })
	oldWorker, oldKey, oldHTTP := system_setting.WorkerUrl, system_setting.WorkerValidKey, system_setting.WorkerAllowHttpImageRequestEnabled
	system_setting.WorkerUrl = ""
	t.Cleanup(func() {
		system_setting.WorkerUrl, system_setting.WorkerValidKey, system_setting.WorkerAllowHttpImageRequestEnabled = oldWorker, oldKey, oldHTTP
	})
	type received struct {
		method, path, query, signature string
		body                           []byte
	}
	requests := make(chan received, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- received{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Webhook-Signature"), body}
		w.WriteHeader(204)
	}))
	defer server.Close()
	data := dto.NewNotify("account_security", "security title", "safe content", nil)
	settings := []dto.UserSetting{{NotifyType: "webhook", WebhookUrl: server.URL, WebhookSecret: "test-signing-secret"}, {NotifyType: "bark", BarkUrl: server.URL + "/push?title={{title}}&body={{content}}"}, {NotifyType: "gotify", GotifyUrl: server.URL, GotifyToken: "test-token", GotifyPriority: 5}}
	for _, s := range settings {
		require.NoError(t, sendAccountNotification(t.Context(), model.User{}, s, data, 1))
		r := <-requests
		switch s.NotifyType {
		case "webhook":
			h := hmac.New(sha256.New, []byte(s.WebhookSecret))
			_, _ = h.Write(r.body)
			require.Equal(t, hex.EncodeToString(h.Sum(nil)), r.signature)
			require.Contains(t, string(r.body), `"type":"account_security"`)
		case "bark":
			require.Equal(t, "GET", r.method)
			require.Contains(t, r.query, "security+title")
			require.Contains(t, r.query, "safe+content")
		case "gotify":
			require.Equal(t, "/message", r.path)
			require.Equal(t, "token=test-token", r.query)
			require.Contains(t, string(r.body), `"priority":5`)
		}
	}
	system_setting.WorkerUrl, system_setting.WorkerValidKey, system_setting.WorkerAllowHttpImageRequestEnabled = server.URL, "worker-test-key", true
	require.NoError(t, sendAccountNotification(t.Context(), model.User{}, settings[0], data, 2))
	r := <-requests
	var worker service.WorkerRequest
	require.NoError(t, common.Unmarshal(r.body, &worker))
	require.Equal(t, server.URL, worker.URL)
	require.Equal(t, "worker-test-key", worker.Key)
	require.Equal(t, "Bearer test-signing-secret", worker.Headers["Authorization"])
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, sendAccountNotification(ctx, model.User{}, settings[0], data, 3))
	require.Empty(t, requests)
	system_setting.WorkerUrl = ""
	blocked := oldFetch
	blocked.EnableSSRFProtection = true
	blocked.AllowPrivateIp = false
	blocked.AllowedPorts = []string{"1-65535"}
	system_setting.ReplaceFetchSetting(blocked)
	require.Error(t, sendAccountNotification(t.Context(), model.User{}, settings[0], data, 4))
	require.Empty(t, requests)
	address, commands := smtpFixture(t, false)
	host, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{"EmailProvider": "smtp", "SMTPServer": host, "SMTPPort": port, "SMTPFrom": "sender@example.com"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = previous; common.OptionMapRWMutex.Unlock() })
	require.NoError(t, sendAccountNotification(t.Context(), model.User{Email: "self@example.com"}, dto.UserSetting{NotifyType: "email"}, data, 5))
	require.Contains(t, strings.Join(<-commands, "\n"), "RCPT TO:<self@example.com>")
}

func TestPersonalNotificationSchemaUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.Migrator().DropIndex(&model.SecurityDelivery{}, "idx_sec_account_delivery"))
	require.NoError(t, db.Migrator().DropIndex(&model.SecurityDelivery{}, "idx_sec_account_cooldown"))
	require.NoError(t, db.Migrator().DropIndex(&model.SecurityDelivery{}, "idx_sec_account_sent"))
	require.NoError(t, db.Migrator().DropColumn(&model.SecurityDelivery{}, "delivery_key"))
	require.NoError(t, db.Migrator().DropColumn(&model.SecurityDelivery{}, "recipient_user_id"))
	require.NoError(t, db.Migrator().DropIndex(&model.SecurityAction{}, "idx_sec_account_notify"))
	require.NoError(t, db.Migrator().DropColumn(&model.SecurityAction{}, "account_notify_pending"))
	require.NoError(t, db.Table("audit_security_deliveries").Create(map[string]any{"alert_id": 1, "kind": "email", "status": "sent"}).Error)
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	var old model.SecurityDelivery
	require.NoError(t, db.First(&old).Error)
	require.Nil(t, old.DeliveryKey)
	require.Zero(t, old.RecipientUserID)
	recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	db.Logger = recorder
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.Empty(t, recorder.statements)
}
