package auditsecurity

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

func alertRuleVersion(rule string, event model.SecurityEvent) int {
	if rule == "sensitive_operation" || rule == "bulk_sensitive_operations" {
		if version := customerSensitiveEventVersion(event); version != 0 {
			return version
		}
	}
	if version := customerAccountRuleVersion(rule); version > 0 {
		return version
	}
	switch rule {
	case "login_failures", "bulk_key_access":
		return 2
	case "sensitive_operation", "bulk_sensitive_operations":
		return 3
	case "recovery_requests":
		return 2
	default:
		return 1
	}
}

func completeFindingMetadata(findings []model.SecurityFinding, e model.SecurityEvent, history []model.SecurityEvent, cfg operation_setting.AuditSecuritySetting) {
	for i := range findings {
		f := &findings[i]
		if f.Dimensions == nil {
			f.Dimensions = model.SecurityDimensions{}
		}
		switch f.Rule {
		case "sensitive_operation":
			f.Dimensions["affected_count"] = f.Count
		case "bulk_sensitive_operations":
			f.Threshold, f.WindowSeconds = cfg.BatchThreshold, 300
			f.Dimensions["affected_count"] = f.Count
		case "bulk_key_access":
			f.Threshold, f.WindowSeconds = cfg.KeyViewThreshold, 300
			f.Dimensions["distinct_channels"] = f.Count
		case "login_failures":
			f.Threshold, f.WindowSeconds = cfg.FailureThreshold, 300
			f.Dimensions["failures"] = f.Count
		case "password_spray":
			f.Threshold, f.WindowSeconds = 20, 300
			accounts := map[string]bool{e.SubjectRef: true}
			for _, item := range uniqueAuthenticationHistory(e, history) {
				if item.IP == e.IP && item.Action == "auth.login_failed" && item.At >= e.At-300 && item.At <= e.At && (item.Reason == "invalid_credentials" || item.Reason == "invalid_code") {
					accounts[item.SubjectRef] = true
				}
			}
			f.Dimensions["failures"] = f.Count
			f.Dimensions["accounts"] = len(accounts)
			f.Dimensions["account_threshold"] = 5
		case "login_after_failures":
			f.Threshold, f.WindowSeconds = cfg.FailureThreshold, 900
			f.Dimensions["failures"] = f.Count
		case "recovery_requests":
			f.Threshold, f.WindowSeconds = 3, 600
			f.Dimensions["events"] = f.Count
		case "permission_failures":
			f.Threshold, f.WindowSeconds = 10, 300
			f.Dimensions["events"] = f.Count
		case "session_reuse":
			f.Threshold = 1
			f.Dimensions["reuse"] = 1
		case "new_login_source":
			f.WindowSeconds = 30 * 86400
			f.Dimensions["new_source"] = 1
			f.Dimensions["baseline"] = 1
			f.Dimensions["new_window_seconds"] = 86400
		case "new_source_sensitive_operation", "new_source_channel_rewrite":
			f.Dimensions["new_source"] = 1
			f.Dimensions["baseline"] = 1
			f.Dimensions["new_window_seconds"] = 86400
			f.Dimensions["baseline_window_seconds"] = 30 * 86400
			var facts Facts
			if common.UnmarshalJsonStr(e.Facts, &facts) == nil && facts.SourceBaseline != nil {
				f.Dimensions["source_first_seen"] = int(facts.SourceBaseline.SourceFirstSeen)
				f.Dimensions["baseline_first_seen"] = int(facts.SourceBaseline.BaselineFirstSeen)
				f.Dimensions["baseline_last_seen"] = int(facts.SourceBaseline.BaselineLastSeen)
			}
		}
	}
}

func persistFindingSnapshot(tx *gorm.DB, alert *model.SecurityAlert, finding model.SecurityFinding, event model.SecurityEvent, state model.SecurityState, detectedAt int64, truncated bool) error {
	source := "live"
	if state.Name == "ip_retry" {
		source = "repair"
	}
	key := findingDetectionKey(finding, event)
	snapshot := model.SecurityFindingSnapshot{
		AttemptedAccount: alert.AttemptedAccount,
		DetectionKey:     &key,
		AlertID:          alert.ID, Rule: finding.Rule, RuleVersion: alert.RuleVersion,
		TriggerEventID: event.EventID, TriggerAt: event.At, DetectedAt: detectedAt, Source: source,
		SubjectRef: event.SubjectRef, ActorID: event.ActorID, ActorRole: event.ActorRole,
		ResourceType: event.ResourceType, TargetID: event.TargetID, IP: event.IP, RequestID: event.RequestID,
		Severity: finding.Severity, Threshold: finding.Threshold, WindowSeconds: finding.WindowSeconds,
		MatchedCount: finding.Count, Dimensions: finding.Dimensions, Truncated: truncated || finding.Truncated,
	}
	if snapshot.Dimensions == nil {
		snapshot.Dimensions = model.SecurityDimensions{}
	}
	if target := eventLoginTarget(event); target != nil {
		snapshot.TargetRole = &target.Role
	}
	if err := tx.Create(&snapshot).Error; err != nil {
		return err
	}
	alert.LatestFindingID = snapshot.ID
	return tx.Model(&model.SecurityAlert{}).Where("id = ?", alert.ID).UpdateColumn("latest_finding_id", snapshot.ID).Error
}

func recordNotificationSkip(tx *gorm.DB, alert *model.SecurityAlert, detectedAt int64, reason string) error {
	return tx.Create(&model.SecurityDelivery{
		AlertID: alert.ID, FindingID: alert.LatestFindingID, Version: alert.Version,
		Kind: "none", Status: "skipped", WaitReason: reason, CreatedAt: detectedAt, UpdatedAt: detectedAt,
	}).Error
}

func notificationSkipReason(cfg operation_setting.AuditSecuritySetting, alert model.SecurityAlert, event model.SecurityEvent, fresh bool, previousSeverity int, now time.Time) string {
	if !cfg.NotificationEnabled {
		return "disabled"
	}
	if event.At < now.Unix()-300 {
		return "backfill"
	}
	if !fresh && previousSeverity >= alert.Severity && event.At-alert.NotifiedAt < int64(cfg.NotificationCooldownSeconds) {
		return "cooldown"
	}
	if cfg.NotificationEmails == "" && cfg.WebhookURL == "" {
		return "no_destination"
	}
	return ""
}
