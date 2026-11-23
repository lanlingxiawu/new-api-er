package operation_setting

import (
	"errors"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

const AuditSecurityModule = "audit_security_setting"

const DefaultSecurityTravelKMH = 1000
const DefaultSecurityTravelDistanceKM = 200

var ErrSecurityAbuseLookupRequired = errors.New("high-risk IP automatic response requires abuse lookup")

type AuditSecuritySetting struct {
	Enabled                           bool   `json:"enabled"`
	CaptureEnabled                    bool   `json:"capture_enabled"`
	RelayIPEnabled                    bool   `json:"relay_ip_enabled"`
	Suspended                         bool   `json:"suspended"`
	AutoBanEnabled                    bool   `json:"auto_ban_enabled"`
	AutoBanIncludeStaff               bool   `json:"auto_ban_include_staff"`
	AutoBanExcludeRoot                bool   `json:"auto_ban_exclude_root"`
	AutoBanExcludedUserIDsJSON        string `json:"auto_ban_excluded_user_ids_json"`
	AutoBanRulesJSON                  string `json:"auto_ban_rules_json"`
	AutoBanEffectiveAt                int64  `json:"auto_ban_effective_at"`
	ScanIntervalSeconds               int    `json:"scan_interval_seconds"`
	FailureThreshold                  int    `json:"failure_threshold"`
	BatchThreshold                    int    `json:"batch_threshold"`
	KeyViewThreshold                  int    `json:"key_view_threshold"`
	NotificationEnabled               bool   `json:"notification_enabled"`
	NotificationEmails                string `json:"notification_emails"`
	WebhookURL                        string `json:"webhook_url"`
	NotificationCooldownSeconds       int    `json:"notification_cooldown_seconds"`
	MaxNotificationsPerHour           int    `json:"max_notifications_per_hour"`
	AlertRetentionDays                int    `json:"alert_retention_days"`
	ArchiveRetentionDays              int    `json:"archive_retention_days"`
	IPIntelligenceEnabled             bool   `json:"ip_intelligence_enabled"`
	IPIntelligenceProvidersJSON       string `json:"ip_intelligence_providers_json"`
	IPIntelligenceCacheTTLMinutes     int    `json:"ip_intelligence_cache_ttl_minutes"`
	IPIntelligenceTimeoutSeconds      int    `json:"ip_intelligence_timeout_seconds"`
	IPIntelligenceMaxLookupsPerCycle  int    `json:"ip_intelligence_max_lookups_per_cycle"`
	IPIntelligenceImpossibleTravelKMH int    `json:"ip_intelligence_impossible_travel_kmh"`
	IPIntelligenceMinDistanceKM       int    `json:"ip_intelligence_min_distance_km"`
}

var AuditSecurityFields = []string{
	"auto_ban_enabled", "auto_ban_include_staff", "auto_ban_exclude_root", "auto_ban_excluded_user_ids_json", "auto_ban_rules_json",
	"enabled", "capture_enabled", "relay_ip_enabled", "suspended", "scan_interval_seconds", "failure_threshold", "batch_threshold", "key_view_threshold",
	"notification_enabled", "notification_emails", "webhook_url", "notification_cooldown_seconds", "max_notifications_per_hour",
	"alert_retention_days", "archive_retention_days", "ip_intelligence_enabled", "ip_intelligence_providers_json",
	"ip_intelligence_cache_ttl_minutes", "ip_intelligence_timeout_seconds", "ip_intelligence_max_lookups_per_cycle",
	"ip_intelligence_impossible_travel_kmh", "ip_intelligence_min_distance_km",
}

func DefaultAuditSecuritySetting() AuditSecuritySetting {
	return AuditSecuritySetting{
		AutoBanRulesJSON: defaultAutoBanRulesJSON, AutoBanExcludedUserIDsJSON: "[]", AutoBanExcludeRoot: true,
		ScanIntervalSeconds: 10, FailureThreshold: 5, BatchThreshold: 5, KeyViewThreshold: 10,
		NotificationCooldownSeconds: 1800, MaxNotificationsPerHour: 20, AlertRetentionDays: 180, ArchiveRetentionDays: 365,
		IPIntelligenceProvidersJSON: defaultIPIntelligenceProvidersJSON(), IPIntelligenceCacheTTLMinutes: 1440,
		IPIntelligenceTimeoutSeconds: 5, IPIntelligenceMaxLookupsPerCycle: 5,
		IPIntelligenceImpossibleTravelKMH: 1000, IPIntelligenceMinDistanceKM: 200,
	}
}

var auditSecuritySetting = DefaultAuditSecuritySetting()
var auditSecuritySnapshot config.Snapshot[AuditSecuritySetting]

func init() {
	config.GlobalConfig.RegisterSnapshot(AuditSecurityModule, &auditSecuritySetting, func() { auditSecuritySnapshot.Publish(auditSecuritySetting) })
}

func GetAuditSecuritySetting() AuditSecuritySetting {
	s := *auditSecuritySnapshot.Load()
	s.IPIntelligenceImpossibleTravelKMH = DefaultSecurityTravelKMH
	s.IPIntelligenceMinDistanceKM = DefaultSecurityTravelDistanceKM
	return s
}

func ValidateAuditSecuritySetting(s AuditSecuritySetting) error {
	rules, err := ParseAutoBanRules(s.AutoBanRulesJSON)
	if err != nil {
		return err
	}
	selected := false
	for _, r := range rules {
		selected = selected || r.HasActions()
		if s.AutoBanEnabled && r.ID == "session_reuse" && r.HasActions() && !s.CaptureEnabled {
			return errors.New("session replay automatic response requires security event capture")
		}
	}
	if s.AutoBanEnabled && !selected {
		return errors.New("automatic response rule required")
	}
	if _, err = ParseAutoBanExcludedUsers(s.AutoBanExcludedUserIDsJSON); err != nil {
		return err
	}
	if s.ScanIntervalSeconds < 5 || s.ScanIntervalSeconds > 60 || s.FailureThreshold < 1 || s.FailureThreshold > 1000 || s.BatchThreshold < 1 || s.BatchThreshold > 1000 || s.KeyViewThreshold < 1 || s.KeyViewThreshold > 1000 || s.NotificationCooldownSeconds < 60 || s.NotificationCooldownSeconds > 86400 || s.MaxNotificationsPerHour < 1 || s.MaxNotificationsPerHour > 100 || s.AlertRetentionDays < 30 || s.AlertRetentionDays > 365 || s.ArchiveRetentionDays < 30 || s.ArchiveRetentionDays > 3650 {
		return errors.New("audit security setting outside allowed range")
	}
	if s.IPIntelligenceCacheTTLMinutes < 10 || s.IPIntelligenceCacheTTLMinutes > 10080 || s.IPIntelligenceTimeoutSeconds < 1 || s.IPIntelligenceTimeoutSeconds > 8 || s.IPIntelligenceMaxLookupsPerCycle < 1 || s.IPIntelligenceMaxLookupsPerCycle > 20 || s.IPIntelligenceImpossibleTravelKMH < 100 || s.IPIntelligenceImpossibleTravelKMH > 10000 || s.IPIntelligenceMinDistanceKM < 50 || s.IPIntelligenceMinDistanceKM > 5000 || len(s.IPIntelligenceProvidersJSON) > 65536 {
		return errors.New("IP intelligence setting outside allowed range")
	}
	providers, err := ParseIPIntelligenceProviders(s.IPIntelligenceProvidersJSON)
	if err != nil {
		return err
	}
	if s.IPIntelligenceEnabled && ActiveIPIntelligenceProvider(providers) == nil {
		return errors.New("exactly one enabled self-hosted IP intelligence provider required")
	}
	for _, r := range rules {
		if s.AutoBanEnabled && r.HasActions() && (r.ID == "risky_ip_login" || r.ID == "risky_ip_sensitive_operation") {
			provider := ActiveIPIntelligenceProvider(providers)
			if !s.IPIntelligenceEnabled || provider == nil || !slices.Contains(provider.Capabilities, "abuse") {
				return ErrSecurityAbuseLookupRequired
			}
		}
	}
	if len(s.NotificationEmails) > 2048 || strings.ContainsAny(s.NotificationEmails, "\r\n") || len(s.WebhookURL) > 2048 {
		return errors.New("invalid audit security destination")
	}
	if s.NotificationEmails != "" {
		emails := strings.Split(s.NotificationEmails, ";")
		if len(emails) > 10 {
			return errors.New("too many audit security destinations")
		}
		for _, v := range emails {
			a, e := mail.ParseAddress(strings.TrimSpace(v))
			if e != nil || a.Address != strings.TrimSpace(v) {
				return errors.New("invalid audit security email")
			}
		}
	}
	if s.WebhookURL != "" {
		u, err := url.Parse(s.WebhookURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return errors.New("invalid audit security webhook")
		}
	}
	if s.NotificationEnabled && s.NotificationEmails == "" && s.WebhookURL == "" {
		return errors.New("audit security destination required")
	}
	return nil
}

func ParseAuditSecurityUpdate(values map[string]string) (AuditSecuritySetting, error) {
	s := GetAuditSecuritySetting()
	for k, v := range values {
		found := false
		for _, key := range AuditSecurityFields {
			if key == k {
				found = true
				break
			}
		}
		if !found {
			return s, errors.New("unknown audit security setting")
		}
		if k == "enabled" || k == "capture_enabled" || k == "relay_ip_enabled" || k == "suspended" || k == "notification_enabled" || k == "ip_intelligence_enabled" || k == "auto_ban_enabled" || k == "auto_ban_include_staff" || k == "auto_ban_exclude_root" {
			if v != "true" && v != "false" {
				return s, errors.New("invalid audit security boolean")
			}
		}
	}
	normalized, err := NormalizeAuditSecurityUpdateValues(values)
	if err != nil {
		return s, err
	}
	if err = config.UpdateConfigFromMap(&s, normalized); err != nil {
		return s, err
	}
	return s, ValidateAuditSecuritySetting(s)
}

func NormalizeAuditSecurityUpdateValues(values map[string]string) (map[string]string, error) {
	normalized := make(map[string]string, len(values))
	for key, value := range values {
		normalized[key] = value
	}
	// Legacy clients may still send these fields, but their values cannot alter detection.
	if _, ok := normalized["ip_intelligence_impossible_travel_kmh"]; ok {
		normalized["ip_intelligence_impossible_travel_kmh"] = strconv.Itoa(DefaultSecurityTravelKMH)
	}
	if _, ok := normalized["ip_intelligence_min_distance_km"]; ok {
		normalized["ip_intelligence_min_distance_km"] = strconv.Itoa(DefaultSecurityTravelDistanceKM)
	}
	if raw, ok := normalized["ip_intelligence_providers_json"]; ok {
		merged, err := MergeIPIntelligenceProviders(raw, GetAuditSecuritySetting().IPIntelligenceProvidersJSON)
		if err != nil {
			return nil, err
		}
		normalized["ip_intelligence_providers_json"] = merged
	}
	current := GetAuditSecuritySetting()
	for _, field := range []string{"auto_ban_enabled", "auto_ban_include_staff", "auto_ban_exclude_root", "auto_ban_rules_json", "auto_ban_excluded_user_ids_json", "enabled", "suspended", "capture_enabled", "ip_intelligence_enabled", "ip_intelligence_providers_json"} {
		value, present := normalized[field]
		if !present {
			continue
		}
		previous := map[string]string{"auto_ban_enabled": strconv.FormatBool(current.AutoBanEnabled), "auto_ban_include_staff": strconv.FormatBool(current.AutoBanIncludeStaff), "auto_ban_exclude_root": strconv.FormatBool(current.AutoBanExcludeRoot), "auto_ban_rules_json": current.AutoBanRulesJSON, "auto_ban_excluded_user_ids_json": current.AutoBanExcludedUserIDsJSON, "enabled": strconv.FormatBool(current.Enabled), "suspended": strconv.FormatBool(current.Suspended), "capture_enabled": strconv.FormatBool(current.CaptureEnabled)}[field]
		if field == "ip_intelligence_enabled" {
			previous = strconv.FormatBool(current.IPIntelligenceEnabled)
		}
		if field == "ip_intelligence_providers_json" {
			previous = current.IPIntelligenceProvidersJSON
		}
		if value != previous {
			normalized["auto_ban_effective_at"] = strconv.FormatInt(time.Now().Unix(), 10)
			break
		}
	}
	return normalized, nil
}
