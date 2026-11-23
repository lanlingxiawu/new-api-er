package auditsecurity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func CreateManualAccountResponse(ctx context.Context, db *gorm.DB, role int, identity model.AuthSessionIdentity, alertID, version int64, requestID string) (model.SecurityAction, error) {
	var action model.SecurityAction
	key := model.SecurityAccountRef(fmt.Sprintf("manual-response:%d:%d:%s", alertID, identity.UserID, requestID))
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var alert model.SecurityAlert
		if err := model.SecurityVisibleQuery(tx, role).Where("id = ? AND (purge_state = ? OR purge_state IS NULL)", alertID, "").Take(&alert).Error; err != nil {
			return err
		}
		if err := tx.Where("execution_key = ?", key).Take(&action).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var finding model.SecurityFindingSnapshot
		if alert.ActorID <= 0 || alert.ActorRole >= common.RoleRootUser || alert.Version != version {
			return model.ErrSecurityConflict
		}
		if err := tx.Where("id = ? AND alert_id = ? AND actor_id = ?", alert.LatestFindingID, alert.ID, alert.ActorID).Take(&finding).Error; err != nil {
			return err
		}
		if finding.ActorRole == 0 && finding.Rule != "session_reuse" {
			return model.ErrSecurityConflict
		}
		r := tx.Model(&model.SecurityAlert{}).Where("id = ? AND version = ? AND (purge_state = ? OR purge_state IS NULL)", alert.ID, version, "").Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": time.Now().Unix()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return model.ErrSecurityConflict
		}
		raw, err := common.Marshal(model.SecurityResponseResult{OperatorAuthVersion: identity.UserAuthVersion})
		if err != nil {
			return err
		}
		now := time.Now().Unix()
		action = model.SecurityAction{ExecutionKey: &key, AlertID: alertID, ActorID: identity.UserID, ActorRole: role, Action: "disable_account", Source: "human", Outcome: "pending", TargetUserID: alert.ActorID, EventID: finding.TriggerEventID, EventAt: finding.TriggerAt, DetectedAt: now, CreatedAt: now, NextAttemptAt: now, Result: string(raw)}
		action.AccountNotifyPending = true
		if err := tx.Create(&action).Error; err != nil {
			return err
		}
		return model.AdvanceAuditActivityRevision(tx)
	})
	if err != nil {
		return action, err
	}
	if err := processAccountResponse(ctx, db, action); err != nil {
		common.SysError("manual audit account response is awaiting recovery")
	}
	err = db.WithContext(ctx).Where("id = ?", action.ID).Take(&action).Error
	return action, err
}

// A bounded batch of alert IDs uses the action's alert index. No per-row HTTP
// or database requests are needed to display response status in the list.
func loadAlertResponses(ctx context.Context, db *gorm.DB, alerts []model.SecurityAlert) error {
	if len(alerts) == 0 {
		return nil
	}
	db = db.WithContext(ctx)
	ids := make([]int64, 0, len(alerts))
	for _, alert := range alerts {
		ids = append(ids, alert.ID)
	}
	var links []model.SecurityResponseLink
	if err := db.Model(&model.SecurityResponseLink{}).Select("alert_id, MAX(group_id) AS group_id").Where("alert_id IN ?", ids).Group("alert_id").Find(&links).Error; err != nil {
		return err
	}
	groupByAlert := map[int64]int64{}
	groupIDs := []int64{}
	for _, link := range links {
		groupByAlert[link.AlertID] = link.GroupID
		groupIDs = append(groupIDs, link.GroupID)
	}
	var groups []model.SecurityResponseGroup
	var grouped []model.SecurityAction
	if len(groupIDs) > 0 {
		if err := db.Select("id", "state", "stopped_at").Where("id IN ?", groupIDs).Find(&groups).Error; err != nil {
			return err
		}
		if err := db.Select(model.SecurityResponseColumns).Where("response_group_id IN ? AND action IN ?", groupIDs, []string{"disable_account", "clear_access_tokens", "clear_api_keys"}).Order("id").Find(&grouped).Error; err != nil {
			return err
		}
	}
	byGroup := map[int64][]model.SecurityAction{}
	canStop := map[int64]bool{}
	for _, group := range groups {
		canStop[group.ID] = group.State == "active" && group.StoppedAt == 0
	}
	for _, action := range grouped {
		byGroup[*action.ResponseGroupID] = append(byGroup[*action.ResponseGroupID], action)
	}
	var actions []model.SecurityAction
	latest := db.Model(&model.SecurityAction{}).Select("MAX(id)").Where("alert_id IN ? AND response_group_id IS NULL AND action IN ?", ids, []string{"disable_account", "clear_access_tokens", "clear_api_keys"}).Group("alert_id, action")
	if err := db.Select(model.SecurityResponseColumns).Where("id IN (?)", latest).Order("id").Find(&actions).Error; err != nil {
		return err
	}
	byAlert := map[int64][]model.SecurityAction{}
	for _, action := range actions {
		byAlert[action.AlertID] = append(byAlert[action.AlertID], action)
	}
	for i := range alerts {
		alert := &alerts[i]
		alert.Response, alert.Responses, alert.ResponseGroupID, alert.CanStop = nil, nil, nil, false
		rows := byAlert[alert.ID]
		if groupID := groupByAlert[alert.ID]; groupID > 0 {
			rows = append([]model.SecurityAction(nil), byGroup[groupID]...)
			for _, legacy := range byAlert[alert.ID] {
				var latestID int64
				for _, grouped := range rows {
					if grouped.Action == legacy.Action {
						latestID = max(latestID, grouped.ID)
					}
				}
				if legacy.ID > latestID || slices.Contains([]string{"pending", "committed", "sync_pending", "clearing"}, legacy.Outcome) {
					rows = append(rows, legacy)
				}
			}
			slices.SortFunc(rows, func(a, b model.SecurityAction) int {
				if a.ID < b.ID {
					return -1
				}
				if a.ID > b.ID {
					return 1
				}
				return 0
			})
			alert.ResponseGroupID = &groupID
			alert.CanStop = canStop[groupID]
		}
		for _, action := range rows {
			if action.TargetUserID != alert.ActorID {
				continue
			}
			summary := action.ResponseSummary()
			alert.Responses = append(alert.Responses, summary)
			if action.Action == "disable_account" {
				alert.Response = &summary
			}
		}
	}
	return nil
}

func queueAutomaticAccountResponse(tx *gorm.DB, alert model.SecurityAlert, finding model.SecurityFinding, event model.SecurityEvent, state model.SecurityState, cfg operation_setting.AuditSecuritySetting, now int64, truncated bool, groups ...*model.SecurityResponseGroup) error {
	if !cfg.Enabled || cfg.Suspended || !cfg.AutoBanEnabled {
		return nil
	}
	rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
	if err != nil {
		return err
	}
	var selected *operation_setting.AutoBanRule
	for _, rule := range rules {
		if rule.ID == finding.Rule && rule.HasActions() {
			selected = &rule
			break
		}
	}
	if selected == nil {
		return nil
	}
	facts, reason := automaticResponseEligibility(event, finding, cfg, now, truncated)
	var group *model.SecurityResponseGroup
	if len(groups) > 0 {
		group = groups[0]
	}
	if group != nil {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.SecurityResponseLink{AlertID: alert.ID, GroupID: group.ID}).Error; err != nil {
			return err
		}
		if group.State != "active" || group.StoppedAt > 0 {
			return nil
		}
	} else if reason == "" {
		return nil // A previous response owns this event or is still recovering.
	}
	result := model.SecurityResponseResult{PolicyAt: cfg.AutoBanEffectiveAt, Rule: *selected}
	if facts.Identity != nil {
		result.TargetAuthVersion = facts.Identity.Version()
		result.TargetRole = facts.Identity.Role
	}
	if group != nil {
		result.TargetAuthVersion = group.AuthVersion
		result.TargetRole = group.TargetRole
		result.BanActionID = group.BanActionID
	}
	if strings.HasPrefix(finding.Rule, "risky_ip_") && facts.IPIntelligence != nil {
		result.EvidenceExpiresAt = facts.IPIntelligence.ExpiresAt
		for _, proof := range facts.IPIntelligence.Evidence {
			if proof.Capability == "abuse" && proof.ExpiresAt > now {
				result.EvidenceExpiresAt = min(result.EvidenceExpiresAt, proof.ExpiresAt)
			}
		}
	}
	for _, kind := range []string{"disable_account", "clear_access_tokens", "clear_api_keys"} {
		if !selected.AllowsAction(kind) {
			continue
		}
		key := model.SecurityAccountRef(fmt.Sprintf("response-skipped:%d:%s:%s", alert.ID, event.EventID, kind))
		action := model.SecurityAction{AlertID: alert.ID, Action: kind, Source: "automatic", Outcome: "pending", TargetUserID: event.ActorID, EventID: event.EventID, EventAt: event.At, DetectedAt: now, CreatedAt: now, NextAttemptAt: now}
		action.AccountNotifyPending = true
		if reason != "" {
			action.Outcome, action.ReasonCode = "skipped", reason
		}
		if group != nil {
			key = model.SecurityAccountRef(fmt.Sprintf("response-group:%d:%s", group.ID, kind))
			action.ResponseGroupID = &group.ID
			action.EventAt, action.EventID = group.TriggerAt, group.TriggerEventID
		}
		action.ExecutionKey = &key
		raw, err := common.Marshal(result)
		if err != nil {
			return err
		}
		action.Result = string(raw)
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&action)
		if created.Error != nil {
			return created.Error
		}
		if kind == "disable_account" && group != nil && created.RowsAffected == 1 {
			result.BanActionID = action.ID
			group.BanActionID = action.ID
			if err := tx.Model(group).Update("ban_action_id", action.ID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *worker) processAccountResponses(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var actions []model.SecurityAction
	if err := w.db.WithContext(ctx).Select(model.SecurityResponseColumns).Where("outcome IN ? AND next_attempt_at <= ?", []string{"pending", "committed", "sync_pending", "clearing"}, time.Now().Unix()).Order("next_attempt_at ASC").Order("id ASC").Limit(5).Find(&actions).Error; err != nil {
		return err
	}
	for _, action := range actions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		actionCtx, stop := context.WithTimeout(ctx, time.Second)
		var err error
		if action.Action == "clear_api_keys" || action.Action == "clear_access_tokens" {
			err = w.processCredentialCleanup(actionCtx, action)
		} else {
			err = processAccountResponse(actionCtx, w.db, action)
		}
		stop()
		if err != nil {
			common.SysError("audit account response did not complete")
		}
	}
	return nil
}

func processAccountResponse(ctx context.Context, db *gorm.DB, action model.SecurityAction) error {
	if action.Outcome == "pending" {
		updated, err := model.DisableUserForSecurity(ctx, db, action.ID)
		if err != nil {
			// A failed Commit can be ambiguous. Read the receipt, never repeat the
			// user update just because the caller did not receive its acknowledgement.
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
			defer cancel()
			var receipt model.SecurityAction
			if readErr := db.WithContext(readCtx).Where("id = ?", action.ID).Take(&receipt).Error; readErr != nil {
				return err
			}
			if receipt.Outcome == "pending" {
				return model.FinishSecurityAccountResponse(readCtx, db, receipt, "unknown", "unknown")
			}
			updated = receipt
		}
		action = updated
	}
	if action.Outcome != "committed" && action.Outcome != "sync_pending" {
		return nil
	}
	var result model.SecurityResponseResult
	if err := common.UnmarshalJsonStr(action.Result, &result); err != nil {
		return err
	}
	superseded, err := model.PublishSecurityAccountState(ctx, db, action.TargetUserID, result.AuthVersion)
	outcome, reason := "succeeded", ""
	if err != nil {
		outcome, reason = "sync_pending", "sync_pending"
		if action.Attempts >= 2 {
			outcome, reason = "partial", "sync_failed"
		}
		common.SysError("audit account response cache synchronization failed")
	} else if superseded {
		outcome, reason = "superseded", "superseded"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
	defer cancel()
	return model.FinishSecurityAccountResponse(finishCtx, db, action, outcome, reason)
}
