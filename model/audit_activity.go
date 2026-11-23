package model

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const AuditActivityProjectionVersion = 1

// AuditActivity is the public account activity contract. Never embed AuditLog:
// adding a storage field must not silently expose it to users or employees.
type AuditActivity struct {
	EventID       string               `json:"event_id"`
	CreatedAt     int64                `json:"created_at"`
	Category      string               `json:"category"`
	Action        string               `json:"action"`
	Success       bool                 `json:"success"`
	IP            string               `json:"ip"`
	Client        string               `json:"client"`
	Source        string               `json:"source"`
	LoginMethod   string               `json:"login_method,omitempty"`
	RequestID     string               `json:"request_id"`
	Target        *AuditActivityTarget `json:"target,omitempty"`
	ChangeSummary *AuditChangeSummary  `json:"change_summary,omitempty"`
}

type AuditActivityTarget struct {
	Resource string `json:"resource"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
}

type AuditChangeSummary struct {
	Fields  []string `json:"fields"`
	Total   int      `json:"total"`
	Outcome string   `json:"outcome"`
}

// Only names from account-owned resources can become public change labels.
// Grouping nested settings under their parent avoids disclosing internal paths.
var auditActivityFields = map[string]string{
	"token":        "name status expired_time remain_quota unlimited_quota model_limits_enabled model_limits allow_ips allow_user_quota_query group cross_group_retry auto_groups",
	"access_token": "name scopes expires_at revoked",
	"user":         "username display_name email language sidebar_modules setting password bound deleted",
}

func ProjectAuditActivities(records []*AuditLog) []AuditActivity {
	items := make([]AuditActivity, 0, len(records))
	for _, record := range records {
		if record != nil {
			items = append(items, ProjectAuditActivity(record))
		}
	}
	return items
}

func ProjectAuditActivity(entry *AuditLog) AuditActivity {
	a := AuditActivity{EventID: entry.EventId, CreatedAt: entry.CreatedAt, Category: entry.Category, Success: entry.Success, IP: entry.Ip, RequestID: entry.RequestId, Client: auditActivityClient(entry.UserAgent), Source: "unknown", Action: "operation"}
	if !ValidAuditCategory(a.Category) || a.Category == "" {
		a.Category = AuditCategoryOperation
	}
	if entry.AuthMethod == "session" || entry.AuthMethod == "access_token" {
		a.Source = entry.AuthMethod
	}
	action := entry.Action
	if action == "" && entry.Other.Op != nil {
		action = entry.Other.Op.Action
	}
	if action == "" && entry.Category == AuditCategoryAccessToken {
		// Request audit records predate explicit action names. Failure describes
		// the request result, not necessarily an authorization denial.
		action = "access_token.request"
	}
	// Rows are already authorized before projection. Keep known management
	// action names for administrators viewing their own activity, while leaving
	// parameters and management targets private. Never allow an entire prefix:
	// unknown actions have no raw-content or raw-route fallback.
	switch action {
	case "login", "logout", "auth.login_locked", "auth.login_failed", "auth.access_denied", "auth.recovery_requested", "auth.recovery_rejected", "auth.session_refresh_reuse", "auth.password_reset_committed", "auth.password_reset_cleanup_failed",
		"user.profile_update", "user.settings_update", "user.password_change", "user.2fa_setup", "user.2fa_enable", "user.2fa_disable_self", "user.2fa_backup_codes", "user.passkey_register", "user.passkey_delete", "user.binding_start", "user.binding_bind", "user.binding_unbind", "user.email_binding_resend", "user.security_verify", "user.account_delete", "user.session_revoke", "user.sessions_revoke",
		"token.create", "token.update", "token.status_update", "token.delete", "token.delete_batch", "token.key_view", "token.key_view_batch",
		"access_token.generate", "access_token.rename", "access_token.update", "access_token.revoke", "access_token.request", "access_token.denied", "log_export.job_create", "log_export.job_delete", "log_export.job_cancel",
		"user.create", "user.update", "user.delete", "user.manage", "user.quota_add", "user.quota_subtract", "user.quota_override", "user.binding_clear", "user.2fa_disable", "user.reset_passkey", "user.oauth_unbind", "user.topup_complete", "user.access_tokens.delete_all", "user.api_keys.delete_all",
		"channel.create", "channel.update", "channel.token_update", "channel.delete", "channel.copy", "channel.key_view", "channel.multi_key_manage", "channel.batch_daily_limit", "channel.priority_reorder", "channel.status_update", "channel.status_update_batch", "channel.delete_batch", "channel.delete_disabled", "channel.tag_disable", "channel.tag_enable", "channel.tag_edit", "channel.tag_batch_set", "channel.upstream_apply", "channel.upstream_apply_all", "channel.upstream_detect_all", "channel.veridrop_cleanup", "channel.veridrop_detect_manual",
		"channel.veridrop_detect", "channel.veridrop_detect_batch", "channel.veridrop_detect_enabled",
		"option.update", "option.group.update", "option.passkey_domains", "option.passkey_domains_confirmed", "option.passkey_domains_blocked", "option.passkey_domains_failed", "option.payment_compliance", "option.reset_ratio", "option.clear_affinity_cache",
		"custom_oauth.create", "custom_oauth.update", "custom_oauth.delete", "performance.clear_disk_cache", "performance.gc", "performance.clear_logs",
		"redemption.create", "redemption.update", "redemption.delete", "redemption.delete_batch", "redemption.delete_invalid", "prefill_group.create", "prefill_group.update", "prefill_group.delete",
		"vendor.create", "vendor.update", "vendor.delete", "vendor.metadata.delete", "vendor.metadata.save", "vendor.merge", "vendor.assign",
		"model.create", "model.update", "model.delete", "model.sync_upstream", "model.delete_batch", "model.metadata.sync", "model.pricing.update", "deployment.create", "deployment.update", "deployment.delete",
		"subscription.plan_create", "subscription.plan_update", "subscription.bind", "subscription.plan_reset", "subscription.user_plan_reset", "subscription.user_quota_reset",
		"employee.performance_adjust", "employee.performance_revert", "customer.bind_blocked", "employee_export.template_save", "employee_export.template_delete",
		"audit_security.disable_account", "audit_security.status", "audit_security.archive", "audit_security.export", "audit_security.delete", "audit_security.cleanup",
		"price_monitor.cost_ratio.update", "price_monitor.price.apply", "price_monitor.settings.update", "log.clear", "log.cleanup_start", "log_export.template_create", "log_export.template_update", "log_export.template_delete":
		a.Action = action
	}
	var params AuditFields
	if entry.Other.Op != nil {
		params = entry.Other.Op.Params
	}
	if a.Action == "login" || a.Action == "auth.login_failed" {
		method := auditActivityText(params["method"])
		if method == "" {
			method = entry.Other.LoginMethod
		}
		if strings.HasPrefix(method, "oauth:") {
			method = "oauth"
		}
		if slices.Contains([]string{"password", "2fa", "passkey", "wechat", "telegram", "oauth"}, method) {
			a.LoginMethod = method
		}
	}
	changes := entry.Other.Changes.project(true, common.RoleCommonUser, entry.UserId)
	if changes != nil {
		fields := []string{}
		targetCount := 0
		updating := slices.Contains([]string{"token.update", "token.status_update", "access_token.update", "access_token.rename", "user.profile_update", "user.settings_update"}, a.Action)
		for _, target := range changes.Targets {
			allowed := auditActivityFields[target.Resource]
			if allowed == "" {
				continue
			}
			targetCount++
			if a.Target == nil {
				a.Target = &AuditActivityTarget{Resource: target.Resource, ID: auditActivityID(target.ID), Name: auditActivityText(target.Name)}
			}
			if target.Operation != "update" {
				continue
			}
			updating = true
			for _, field := range target.Fields {
				if len(field.Path) > 0 && auditWordContains(allowed, field.Path[0]) && !slices.Contains(fields, field.Path[0]) {
					fields = append(fields, field.Path[0])
				}
			}
		}
		if targetCount > 1 {
			a.Target = nil // A batch must not be presented as a single-target operation.
		}
		if updating {
			slices.Sort(fields)
			outcome := changes.Outcome
			if !slices.Contains([]string{"committed", "partial", "not_committed", "unchanged", "unavailable"}, outcome) {
				outcome = "unavailable"
			}
			a.ChangeSummary = &AuditChangeSummary{Fields: fields[:min(3, len(fields))], Total: len(fields), Outcome: outcome}
		}
	}
	// Targets require an owned receipt. Missing/filtered legacy receipts must not
	// be reconstructed from operation parameters, which can describe other users.
	return a
}

func auditActivityText(value any) string {
	data, err := common.Marshal(value)
	if err != nil {
		return ""
	}
	var text string
	if common.Unmarshal(data, &text) != nil || utf8.RuneCountInString(text) > 128 || strings.IndexFunc(text, unicode.IsControl) >= 0 {
		return ""
	}
	return text
}

func auditActivityID(value string) string {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

var auditClientPatterns = []struct {
	pattern *regexp.Regexp
	name    string
}{
	{regexp.MustCompile(`(?i)Electron/`), "Electron"}, {regexp.MustCompile(`(?i)PostmanRuntime/`), "Postman"},
	{regexp.MustCompile(`(?i)curl/`), "curl"}, {regexp.MustCompile(`(?i)python-requests/`), "Python requests"},
	{regexp.MustCompile(`(?i)python-httpx/`), "Python httpx"}, {regexp.MustCompile(`(?i)\bnode(?:\.js)?\b`), "Node.js"},
	{regexp.MustCompile(`(?i)Go-http-client/`), "Go HTTP Client"},
}

func auditActivityClient(ua string) string {
	if ua == "" {
		return "Unknown client"
	}
	if len(ua) > 2048 {
		ua = ua[:2048]
	}
	for _, client := range auditClientPatterns {
		if client.pattern.MatchString(ua) {
			return client.name
		}
	}
	browser := ""
	for _, client := range []struct{ fragments, name string }{{"Edg/ Edge/ EdgA/ EdgiOS/", "Edge"}, {"OPR/ Opera/", "Opera"}, {"Firefox/ FxiOS/", "Firefox"}, {"Chrome/ CriOS/", "Chrome"}, {"Safari/", "Safari"}} {
		for _, fragment := range strings.Fields(client.fragments) {
			if strings.Contains(ua, fragment) {
				browser = client.name
				break
			}
		}
		if browser != "" {
			break
		}
	}
	if browser == "" {
		return "Other client"
	}
	for _, system := range []struct{ fragments, name string }{{"iPhone|iPad|iPod", "iOS"}, {"Android", "Android"}, {"Windows", "Windows"}, {"Macintosh|Mac OS X", "macOS"}, {"Linux", "Linux"}} {
		for _, fragment := range strings.Split(system.fragments, "|") {
			if strings.Contains(ua, fragment) {
				return browser + " · " + system.name
			}
		}
	}
	return browser
}
