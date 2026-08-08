package authz

import "strings"

const (
	SystemSettingsResourcePrefix = "system_settings."
	ActionView                   = "view"
	ActionEdit                   = "edit"
)

type systemSettingsScopeDefinition struct {
	Scope         string
	LabelKey      string
	Group         string
	GroupLabelKey string
	Sort          int
}

var systemSettingsScopeDefinitions = []systemSettingsScopeDefinition{
	{Scope: "site.system-info", LabelKey: "System Information", Group: "site", GroupLabelKey: "Site & Branding", Sort: 101},
	{Scope: "site.notice", LabelKey: "System Notice", Group: "site", GroupLabelKey: "Site & Branding", Sort: 102},
	{Scope: "site.header-navigation", LabelKey: "Header navigation", Group: "site", GroupLabelKey: "Site & Branding", Sort: 103},
	{Scope: "site.sidebar-modules", LabelKey: "Sidebar modules", Group: "site", GroupLabelKey: "Site & Branding", Sort: 104},
	{Scope: "auth.basic-auth", LabelKey: "Basic Authentication", Group: "auth", GroupLabelKey: "Authentication", Sort: 201},
	{Scope: "auth.oauth", LabelKey: "OAuth Integrations", Group: "auth", GroupLabelKey: "Authentication", Sort: 202},
	{Scope: "auth.passkey", LabelKey: "Passkey Authentication", Group: "auth", GroupLabelKey: "Authentication", Sort: 203},
	{Scope: "auth.bot-protection", LabelKey: "Bot Protection", Group: "auth", GroupLabelKey: "Authentication", Sort: 204},
	{Scope: "auth.custom-oauth", LabelKey: "Custom OAuth", Group: "auth", GroupLabelKey: "Authentication", Sort: 205},
	{Scope: "billing.quota", LabelKey: "Quota Settings", Group: "billing", GroupLabelKey: "Billing & Payment", Sort: 301},
	{Scope: "billing.currency", LabelKey: "Currency & Display", Group: "billing", GroupLabelKey: "Billing & Payment", Sort: 302},
	{Scope: "billing.model-pricing", LabelKey: "Model Pricing", Group: "billing", GroupLabelKey: "Billing & Payment", Sort: 303},
	{Scope: "billing.group-pricing", LabelKey: "Group Pricing", Group: "billing", GroupLabelKey: "Billing & Payment", Sort: 304},
	{Scope: "billing.payment", LabelKey: "Payment Gateway", Group: "billing", GroupLabelKey: "Billing & Payment", Sort: 305},
	{Scope: "billing.checkin", LabelKey: "Check-in Rewards", Group: "billing", GroupLabelKey: "Billing & Payment", Sort: 306},
	{Scope: "models.global", LabelKey: "Global Model Configuration", Group: "models", GroupLabelKey: "Models & Routing", Sort: 401},
	{Scope: "models.routing-reliability", LabelKey: "Routing Reliability", Group: "models", GroupLabelKey: "Models & Routing", Sort: 402},
	{Scope: "models.gemini", LabelKey: "Gemini", Group: "models", GroupLabelKey: "Models & Routing", Sort: 403},
	{Scope: "models.claude", LabelKey: "Claude", Group: "models", GroupLabelKey: "Models & Routing", Sort: 404},
	{Scope: "models.grok", LabelKey: "Grok", Group: "models", GroupLabelKey: "Models & Routing", Sort: 405},
	{Scope: "models.channel-affinity", LabelKey: "Channel Affinity", Group: "models", GroupLabelKey: "Models & Routing", Sort: 406},
	{Scope: "models.model-deployment", LabelKey: "Model Deployment", Group: "models", GroupLabelKey: "Models & Routing", Sort: 407},
	{Scope: "security.rate-limit", LabelKey: "Rate Limiting", Group: "security", GroupLabelKey: "Security & Limits", Sort: 501},
	{Scope: "security.sensitive-words", LabelKey: "Sensitive Words", Group: "security", GroupLabelKey: "Security & Limits", Sort: 502},
	{Scope: "security.ssrf", LabelKey: "SSRF Protection", Group: "security", GroupLabelKey: "Security & Limits", Sort: 503},
	{Scope: "security.token-limits", LabelKey: "Token Limits", Group: "security", GroupLabelKey: "Security & Limits", Sort: 504},
	{Scope: "content.dashboard", LabelKey: "Data Dashboard", Group: "content", GroupLabelKey: "Console Content", Sort: 601},
	{Scope: "content.announcements", LabelKey: "Announcements", Group: "content", GroupLabelKey: "Console Content", Sort: 602},
	{Scope: "content.api-info", LabelKey: "API Addresses", Group: "content", GroupLabelKey: "Console Content", Sort: 603},
	{Scope: "content.faq", LabelKey: "FAQ", Group: "content", GroupLabelKey: "Console Content", Sort: 604},
	{Scope: "content.uptime-kuma", LabelKey: "Uptime Kuma", Group: "content", GroupLabelKey: "Console Content", Sort: 605},
	{Scope: "content.chat", LabelKey: "Chat Presets", Group: "content", GroupLabelKey: "Console Content", Sort: 606},
	{Scope: "content.drawing", LabelKey: "Drawing", Group: "content", GroupLabelKey: "Console Content", Sort: 607},
	{Scope: "operations.node-control", LabelKey: "Node Control Service", Group: "operations", GroupLabelKey: "Operations", Sort: 701},
	{Scope: "operations.behavior", LabelKey: "System Behavior", Group: "operations", GroupLabelKey: "Operations", Sort: 702},
	{Scope: "operations.alerts", LabelKey: "Monitoring & Alerts", Group: "operations", GroupLabelKey: "Operations", Sort: 703},
	{Scope: "operations.email", LabelKey: "SMTP Email", Group: "operations", GroupLabelKey: "Operations", Sort: 704},
	{Scope: "operations.worker", LabelKey: "Worker Proxy", Group: "operations", GroupLabelKey: "Operations", Sort: 705},
	{Scope: "operations.logs", LabelKey: "Log Maintenance", Group: "operations", GroupLabelKey: "Operations", Sort: 706},
	{Scope: "operations.request-log", LabelKey: "Request Log", Group: "operations", GroupLabelKey: "Operations", Sort: 707},
	{Scope: "operations.performance", LabelKey: "Performance", Group: "operations", GroupLabelKey: "Operations", Sort: 708},
	{Scope: "operations.update-checker", LabelKey: "System maintenance", Group: "operations", GroupLabelKey: "Operations", Sort: 709},
	{Scope: "system-tuning.gateway-rate-limit", LabelKey: "Gateway Rate Limiting", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 801},
	{Scope: "system-tuning.database-pool", LabelKey: "Database Connection Pool", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 802},
	{Scope: "system-tuning.login-session-policy", LabelKey: "Login Session Policy", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 803},
	{Scope: "system-tuning.settlement-guard", LabelKey: "Settlement Guard", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 804},
	{Scope: "system-tuning.ledger-pipeline", LabelKey: "Ledger Pipeline", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 805},
	{Scope: "system-tuning.relay-log-pipeline", LabelKey: "Relay Log Pipeline", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 806},
	{Scope: "system-tuning.export-settings", LabelKey: "Billing Export Settings", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 807},
	{Scope: "system-tuning.log-query", LabelKey: "Usage Log Query Acceleration", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 808},
	{Scope: "system-tuning.log-export", LabelKey: "Usage Log Export Settings", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 809},
	{Scope: "system-tuning.ledger-detail", LabelKey: "Ledger Detail Settings", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 810},
	{Scope: "system-tuning.fallback-backfill", LabelKey: "Fallback Backfill", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 811},
	{Scope: "system-tuning.relay-timeout", LabelKey: "AI Request Timeout", Group: "system-tuning", GroupLabelKey: "Runtime Parameters", Sort: 812},
}

func init() {
	for _, scope := range systemSettingsScopeDefinitions {
		RegisterResource(ResourceDefinition{
			Resource:      SystemSettingsResource(scope.Scope),
			LabelKey:      scope.LabelKey,
			Group:         scope.Group,
			GroupLabelKey: scope.GroupLabelKey,
			Sort:          scope.Sort,
			Actions: []ActionDefinition{
				{Action: ActionView, LabelKey: "Visible", DescriptionKey: "Show this system settings section."},
				{Action: ActionEdit, LabelKey: "Allow editing", DescriptionKey: "Allow changing settings and running actions in this section."},
			},
		})
	}
}

func SystemSettingsScopes() []string {
	result := make([]string, 0, len(systemSettingsScopeDefinitions))
	for _, definition := range systemSettingsScopeDefinitions {
		result = append(result, definition.Scope)
	}
	return result
}

func SystemSettingsResource(scope string) string {
	return SystemSettingsResourcePrefix + scope
}

func SystemSettingsView(scope string) Permission {
	return Permission{Resource: SystemSettingsResource(scope), Action: ActionView}
}

func SystemSettingsEdit(scope string) Permission {
	return Permission{Resource: SystemSettingsResource(scope), Action: ActionEdit}
}

func IsSystemSettingsResource(resource string) bool {
	return strings.HasPrefix(resource, SystemSettingsResourcePrefix)
}

func normalizePermissionActions(resource string, actions map[string]bool) map[string]bool {
	if !IsSystemSettingsResource(resource) {
		return actions
	}

	normalized := make(map[string]bool, len(actions)+1)
	for action, allowed := range actions {
		normalized[action] = allowed
	}
	if visible, explicitlySet := normalized[ActionView]; explicitlySet && !visible {
		normalized[ActionEdit] = false
		return normalized
	}
	if normalized[ActionEdit] {
		normalized[ActionView] = true
	}
	return normalized
}
