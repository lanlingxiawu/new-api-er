package auditsecurity

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func successfulSensitiveOperation(e model.SecurityEvent, f Facts) bool {
	if !e.Success || e.ActorID <= 0 || f.EvidenceIncomplete || f.Changed != nil && !*f.Changed {
		return false
	}
	if successfulAccountKeyAccess(e, f) {
		return true
	}
	switch e.Action {
	case "token.create":
		return f.ID > 0
	case "access_token.generate":
		return f.TokenID > 0 || f.ID > 0
	case "access_token.update":
		return f.PermissionIncrease
	case "channel.update", "channel.token_update", "channel.tag_edit":
		if f.ID <= 0 && len(f.IDs) == 0 {
			return false
		}
		for _, field := range f.ChangedFields {
			if slices.Contains([]string{"key", "base_url", "proxy", "header_override", "param_override", "setting", "settings"}, field) {
				return true
			}
		}
		return false
	case "channel.key_view", "channel.delete", "channel.multi_key_manage":
		return (f.ID > 0 || len(f.IDs) > 0) && highRisk(e, f) > 0
	case "user.update":
		return (f.PermissionsUpdated || f.PasswordReset || len(f.ChangedFields) > 0) && highRisk(e, f) > 0
	case "user.create":
		return f.TargetUserID > 0 && f.Role >= common.RoleAdminUser
	case "channel.delete_batch":
		return f.Count != nil && *f.Count > 0
	case "user.quota_add", "user.quota_subtract":
		return f.Quota != nil && *f.Quota > 0
	case "user.delete":
		return f.TargetUserID > 0
	case "user.manage":
		return f.TargetUserID > 0 && f.Action != ""
	}
	if strings.HasPrefix(e.Action, "user.quota_") {
		return f.From != nil && f.To != nil && *f.From != *f.To
	}
	return slices.Contains([]string{"user.password_change", "user.2fa_enable", "user.2fa_disable_self", "user.passkey_register", "user.passkey_delete", "user.binding_bind", "user.binding_unbind", "user.oauth_unbind", "user.binding_clear", "user.2fa_disable", "user.reset_passkey", "user.2fa_backup_codes"}, e.Action)
}

func currentAbuseEvidence(ip string, intel *IPIntelligenceSnapshot, now int64) bool {
	address, err := netip.ParseAddr(ip)
	if err != nil || !publicIPForIntelligence(ip) || intel == nil || intel.IsAbuser == nil || !*intel.IsAbuser || intel.ObservedAt <= 0 || intel.ObservedAt > now || intel.ExpiresAt <= now {
		return false
	}
	address = address.Unmap()
	provider := intel.FieldProviders["abuse"]
	if provider == "" || !slices.Contains(intel.Providers, provider) {
		return false
	}
	for _, evidence := range intel.Evidence {
		if evidence.ProviderID != provider || evidence.Capability != "abuse" || evidence.DataUpdatedAt <= 0 || evidence.DataUpdatedAt > now || evidence.ExpiresAt <= now {
			continue
		}
		matched, parseErr := netip.ParseAddr(evidence.MatchedNetwork)
		if parseErr != nil {
			prefix, prefixErr := netip.ParsePrefix(evidence.MatchedNetwork)
			if prefixErr != nil || prefix.Bits() != prefix.Addr().BitLen() {
				continue
			}
			matched = prefix.Addr()
		}
		if matched.Unmap() != address {
			continue
		}
		for _, source := range intel.Sources {
			if source.ProviderID == provider && source.Source == evidence.Source && source.Capability == "abuse" && source.DataUpdatedAt == evidence.DataUpdatedAt && source.ExpiresAt == evidence.ExpiresAt {
				return true
			}
		}
	}
	return false
}

func evaluateSensitiveFindings(e model.SecurityEvent, cfg operation_setting.AuditSecuritySetting, newSource bool, now int64) []model.SecurityFinding {
	var f Facts
	if e.ActorID <= 0 || !e.Success || !publicIPForIntelligence(e.IP) || common.UnmarshalJsonStr(e.Facts, &f) != nil || f.EvidenceIncomplete {
		return nil
	}
	sensitive := successfulSensitiveOperation(e, f)
	var rules []string
	if cfg.IPIntelligenceEnabled && currentAbuseEvidence(e.IP, f.IPIntelligence, now) {
		if e.Action == "login" {
			rules = append(rules, "risky_ip_login")
		}
		if sensitive {
			rules = append(rules, "risky_ip_sensitive_operation")
		}
	}
	if newSource && sensitive {
		rules = append(rules, "new_source_sensitive_operation")
	}
	findings := make([]model.SecurityFinding, 0, len(rules))
	for _, rule := range rules {
		dimensions := model.SecurityDimensions{"detected": 1}
		if rule == "new_source_sensitive_operation" {
			// observeManagementSource already required a source newer than one day
			// and another management source active across the last 30 days.
			dimensions["new_source"] = 1
			dimensions["baseline"] = 1
			dimensions["new_window_seconds"] = 86400
			dimensions["baseline_window_seconds"] = 30 * 86400
		}
		findings = append(findings, model.SecurityFinding{Rule: rule, SubjectRef: e.SubjectRef, Severity: correlatedSensitiveSeverity(rule, e, f), Count: 1, Threshold: 1, Evidence: []model.SecurityEvent{e}, Dimensions: dimensions})
	}
	return findings
}

func correlatedSensitiveSeverity(rule string, e model.SecurityEvent, f Facts) int {
	switch rule {
	case "risky_ip_login":
		return 2
	case "risky_ip_sensitive_operation":
		if credentialOrPrivilege(e, f) {
			return 3
		}
		return 2
	case "new_source_sensitive_operation":
		if e.ActorRole < common.RoleAdminUser && (e.Action == "access_token.generate" || e.Action == "token.create") && !credentialOrPrivilege(e, f) {
			return 1
		}
		return 2
	default:
		return 2
	}
}
