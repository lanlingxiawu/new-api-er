package auditsecurity

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Only these single-account rules may expose a failed login to its target.
func loginTargetRuleVersion(rule string) int {
	switch rule {
	case "login_failures", "anonymous_network", "ip_abuse":
		return 2
	default:
		return 0
	}
}

func eventLoginTarget(event model.SecurityEvent) *model.AuditLoginTarget {
	if event.Success || event.ActorID != 0 || event.ActorRole != 0 || event.ResourceType != "account" || (event.Action != "auth.login_failed" && event.Action != "auth.login_locked") {
		return nil
	}
	var facts Facts
	if common.UnmarshalJsonStr(event.Facts, &facts) != nil || facts.LoginTarget == nil || !facts.LoginTarget.Valid() || facts.LoginTarget.UserID != event.TargetID {
		return nil
	}
	return facts.LoginTarget
}

func alertLoginTarget(alert model.SecurityAlert) *model.AuditLoginTarget {
	version := loginTargetRuleVersion(alert.Rule)
	if version == 0 || alert.RuleVersion != version || alert.ActorID != 0 || alert.ActorRole != 0 || alert.ResourceType != "account" || alert.TargetUserID == nil || alert.TargetRole == nil {
		return nil
	}
	target := model.AuditLoginTarget{UserID: *alert.TargetUserID, Role: *alert.TargetRole}
	if !target.Valid() {
		return nil
	}
	return &target
}

// Read/investigation ownership only. Account response and notification paths
// must continue using authenticated ActorID and their existing identity proof.
func alertAccountID(alert model.SecurityAlert) int {
	if target := alertLoginTarget(alert); target != nil {
		return target.UserID
	}
	return alert.ActorID
}

func accountEventAccessible(alert model.SecurityAlert, event model.SecurityEvent, accountID int, selfView bool) bool {
	if alert.Rule == "sensitive_operation" || alert.Rule == "bulk_sensitive_operations" {
		version := customerSensitiveOperationVersion
		if alert.RuleVersion == customerRecoveryOperationVersion {
			version = customerRecoveryOperationVersion
		}
		return !selfView && event.ActorID == accountID && event.ResourceType == alert.ResourceType && customerSensitiveEventVersion(event) == version
	}
	if target := eventLoginTarget(event); target != nil {
		if target.UserID != accountID || !accountRiskRoleAllowed(alert.Rule, target.Role, selfView) {
			return false
		}
		if owner := alertLoginTarget(alert); owner != nil {
			return *owner == *target
		}
		return alert.Rule == "login_after_failures" && alert.ActorID == accountID && alert.ActorRole == target.Role
	}
	return event.ActorID == accountID && accountRiskRoleAllowed(alert.Rule, event.ActorRole, selfView) && (event.ActorRole != 0 || alert.Rule == "session_reuse" && event.Action == "auth.session_refresh_reuse")
}

func sameLoginTarget(a, b model.SecurityEvent) bool {
	left, right := eventLoginTarget(a), eventLoginTarget(b)
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
