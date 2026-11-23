package operation_setting

import (
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestAuditSecuritySettingsValidation(t *testing.T) {
	defaults := DefaultAuditSecuritySetting()
	require.False(t, defaults.Enabled)
	require.False(t, defaults.CaptureEnabled)
	require.NoError(t, ValidateAuditSecuritySetting(defaults))
	for _, seconds := range []int{0, 4, 5, 60, 61} {
		s := defaults
		s.ScanIntervalSeconds = seconds
		require.Equal(t, seconds < 5 || seconds > 60, ValidateAuditSecuritySetting(s) != nil)
	}
	s := defaults
	s.NotificationEnabled = true
	require.Error(t, ValidateAuditSecuritySetting(s))
	s.NotificationEmails = "security@example.test"
	require.NoError(t, ValidateAuditSecuritySetting(s))
	s.NotificationEmails = "security@example.test\r\nBcc: attacker@example.test"
	require.Error(t, ValidateAuditSecuritySetting(s))
}

func TestAutoBanConfigurationBoundaries(t *testing.T) {
	s := DefaultAuditSecuritySetting()
	require.False(t, s.AutoBanEnabled)
	require.False(t, s.AutoBanIncludeStaff)
	require.True(t, s.AutoBanExcludeRoot)
	rules, err := ParseAutoBanRules(s.AutoBanRulesJSON)
	require.NoError(t, err)
	require.Len(t, rules, 7)
	for _, count := range []int{2, 3, 100, 101} {
		rule := rules[0]
		rule.LoginAccounts, rule.KeyAccounts = count, 2
		raw, err := common.Marshal([]AutoBanRule{rule, rules[1], rules[2], rules[3]})
		require.NoError(t, err)
		_, err = ParseAutoBanRules(string(raw))
		require.Equal(t, count < 3 || count > 100, err != nil)
	}
	for _, raw := range []string{`null`, `[]`, `[{"id":"unknown","selected":true}]`, `[{"id":"session_reuse","selected":true,"script":"anything"}]`} {
		_, err := ParseAutoBanRules(raw)
		require.Error(t, err)
	}
	for i := range rules {
		rules[i].Selected = false
	}
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	s.AutoBanRulesJSON = string(raw)
	require.NoError(t, ValidateAuditSecuritySetting(s))
	s.AutoBanEnabled = true
	require.Error(t, ValidateAuditSecuritySetting(s))
	for _, raw := range []string{`[-1]`, `[0]`, `[1,1]`, `["1"]`, `null`} {
		_, err := ParseAutoBanExcludedUsers(raw)
		require.Error(t, err)
	}
	ids, err := ParseAutoBanExcludedUsers(`[7,3]`)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{7, 3}, ids)
}

func TestTravelThresholdsCannotBeConfigured(t *testing.T) {
	s, err := ParseAuditSecurityUpdate(map[string]string{
		"ip_intelligence_impossible_travel_kmh": "2000",
		"ip_intelligence_min_distance_km":       "500",
	})
	require.NoError(t, err)
	require.Equal(t, 1000, s.IPIntelligenceImpossibleTravelKMH)
	require.Equal(t, 200, s.IPIntelligenceMinDistanceKM)
	_, err = ParseAuditSecurityUpdate(map[string]string{"auto_ban_effective_at": "1"})
	require.Error(t, err, "activation time is server-owned")
}

func TestSessionReuseAutomaticResponseRequiresCapture(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, selected := range []bool{false, true} {
			for _, capture := range []bool{false, true} {
				s := DefaultAuditSecuritySetting()
				s.AutoBanEnabled, s.CaptureEnabled = automatic, capture
				rules, err := ParseAutoBanRules(s.AutoBanRulesJSON)
				require.NoError(t, err)
				rules[3].Selected = selected
				raw, err := common.Marshal(rules)
				require.NoError(t, err)
				s.AutoBanRulesJSON = string(raw)
				require.Equal(t, automatic && selected && !capture, ValidateAuditSecuritySetting(s) != nil,
					"automatic=%t session_reuse=%t capture=%t", automatic, selected, capture)
			}
		}
	}
}

func TestResponseScopeChangeStartsNewAutomaticResponsePolicy(t *testing.T) {
	current := GetAuditSecuritySetting()
	for field, value := range map[string]bool{"capture_enabled": current.CaptureEnabled, "auto_ban_exclude_root": current.AutoBanExcludeRoot} {
		t.Run(field, func(t *testing.T) {
			unchanged, err := NormalizeAuditSecurityUpdateValues(map[string]string{field: strconv.FormatBool(value)})
			require.NoError(t, err)
			require.NotContains(t, unchanged, "auto_ban_effective_at")
			before := time.Now().Unix()
			changed, err := NormalizeAuditSecurityUpdateValues(map[string]string{field: strconv.FormatBool(!value)})
			require.NoError(t, err)
			epoch, err := strconv.ParseInt(changed["auto_ban_effective_at"], 10, 64)
			require.NoError(t, err)
			require.GreaterOrEqual(t, epoch, before)
		})
	}
}

func TestAutomaticResponseRootExclusionSetting(t *testing.T) {
	for _, value := range []string{"true", "false", "", "1", "TRUE", "null"} {
		t.Run(value, func(t *testing.T) {
			updated, err := ParseAuditSecurityUpdate(map[string]string{"auto_ban_exclude_root": value})
			if value != "true" && value != "false" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, value == "true", updated.AutoBanExcludeRoot)
		})
	}
	unchanged, err := ParseAuditSecurityUpdate(map[string]string{})
	require.NoError(t, err)
	require.Equal(t, GetAuditSecuritySetting().AutoBanExcludeRoot, unchanged.AutoBanExcludeRoot)
}
