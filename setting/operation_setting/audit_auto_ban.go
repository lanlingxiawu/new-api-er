package operation_setting

import (
	"errors"
	"slices"

	"github.com/QuantumNous/new-api/common"
)

// AutoBanRule is a fixed, validated policy, never an executable expression.
type AutoBanRule struct {
	ID                 string `json:"id"`
	Selected           bool   `json:"selected"`
	ClearAPIKeys       bool   `json:"clear_api_keys"`
	ClearAccessTokens  bool   `json:"clear_access_tokens"`
	LoginWindowSeconds int    `json:"login_window_seconds,omitempty"`
	WindowSeconds      int    `json:"window_seconds,omitempty"`
	LoginAccounts      int    `json:"login_accounts,omitempty"`
	KeyAccounts        int    `json:"key_accounts,omitempty"`
	LoginIPs           int    `json:"login_ips,omitempty"`
	KeyIPs             int    `json:"key_ips,omitempty"`
	Channels           int    `json:"channels,omitempty"`
}

const defaultAutoBanRulesJSON = `[{"id":"multi_account_key_access","selected":true,"login_window_seconds":60,"window_seconds":300,"login_accounts":5,"key_accounts":3},{"id":"multi_source_key_access","selected":true,"window_seconds":300,"login_ips":5,"key_ips":3},{"id":"new_source_channel_rewrite","selected":true,"window_seconds":300,"channels":10},{"id":"session_reuse","selected":true}]`

func ParseAutoBanRules(raw string) ([]AutoBanRule, error) {
	invalid := errors.New("invalid automatic account response rules")
	if len(raw) > 8192 {
		return nil, invalid
	}
	var rules []AutoBanRule
	var fields []map[string]any
	if common.UnmarshalJsonStr(raw, &rules) != nil || common.UnmarshalJsonStr(raw, &fields) != nil || len(rules) < 4 || len(rules) > 7 {
		return nil, invalid
	}
	seen := make(map[string]bool, 4)
	for i, r := range rules {
		if seen[r.ID] {
			return nil, invalid
		}
		seen[r.ID] = true
		allowed := []string{"id", "selected", "clear_api_keys", "clear_access_tokens"}
		for _, name := range []string{"clear_api_keys", "clear_access_tokens"} {
			if value, exists := fields[i][name]; exists {
				if _, ok := value.(bool); !ok {
					return nil, invalid
				}
			}
		}
		if _, ok := fields[i]["selected"].(bool); !ok {
			return nil, invalid
		}
		switch r.ID {
		case "multi_account_key_access":
			allowed = append(allowed, "login_window_seconds", "window_seconds", "login_accounts", "key_accounts")
			if r.LoginWindowSeconds < 10 || r.LoginWindowSeconds > 120 || r.WindowSeconds < r.LoginWindowSeconds || r.LoginAccounts < 3 || r.LoginAccounts > 100 || r.KeyAccounts < 2 || r.KeyAccounts > r.LoginAccounts {
				return nil, invalid
			}
		case "multi_source_key_access":
			allowed = append(allowed, "window_seconds", "login_ips", "key_ips")
			if r.LoginIPs < 3 || r.LoginIPs > 100 || r.KeyIPs < 2 || r.KeyIPs > r.LoginIPs {
				return nil, invalid
			}
		case "new_source_channel_rewrite":
			allowed = append(allowed, "window_seconds", "channels")
			if r.Channels < 5 || r.Channels > 100 {
				return nil, invalid
			}
		case "session_reuse", "risky_ip_login", "risky_ip_sensitive_operation", "new_source_sensitive_operation":
		default:
			return nil, invalid
		}
		if slices.Contains([]string{"multi_account_key_access", "multi_source_key_access", "new_source_channel_rewrite"}, r.ID) && (r.WindowSeconds < 60 || r.WindowSeconds > 600) {
			return nil, invalid
		}
		for field := range fields[i] {
			if !slices.Contains(allowed, field) {
				return nil, invalid
			}
		}
	}
	for _, id := range []string{"multi_account_key_access", "multi_source_key_access", "new_source_channel_rewrite", "session_reuse"} {
		if !seen[id] {
			return nil, invalid
		}
	}
	for _, id := range []string{"risky_ip_login", "risky_ip_sensitive_operation", "new_source_sensitive_operation"} {
		if !seen[id] {
			rules = append(rules, AutoBanRule{ID: id})
		}
	}
	return rules, nil
}

func (r AutoBanRule) HasActions() bool {
	return r.Selected || r.ClearAPIKeys || r.ClearAccessTokens
}

func (r AutoBanRule) AllowsAction(action string) bool {
	switch action {
	case "disable_account":
		return r.Selected
	case "clear_api_keys":
		return r.ClearAPIKeys
	case "clear_access_tokens":
		return r.ClearAccessTokens
	default:
		return false
	}
}

func ParseAutoBanExcludedUsers(raw string) ([]int, error) {
	var ids []int
	if len(raw) > 4096 || common.UnmarshalJsonStr(raw, &ids) != nil || ids == nil || len(ids) > 100 {
		return nil, errors.New("invalid automatic response exclusions")
	}
	slices.Sort(ids)
	for i, id := range ids {
		if id <= 0 || i > 0 && ids[i-1] == id {
			return nil, errors.New("invalid automatic response exclusions")
		}
	}
	return ids, nil
}
