package operation_setting

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestAutomaticResponseLegacyRulesDoNotEnableNewActions(t *testing.T) {
	rules, err := ParseAutoBanRules(defaultAutoBanRulesJSON)
	require.NoError(t, err)
	require.Len(t, rules, 7)
	for _, rule := range rules[4:] {
		require.False(t, rule.HasActions())
	}
	raw, err := common.Marshal(rules[:4])
	require.NoError(t, err)
	rules, err = ParseAutoBanRules(string(raw))
	require.NoError(t, err)
	require.Len(t, rules, 7)
	for _, rule := range rules[4:] {
		require.False(t, rule.HasActions())
	}
	rules = append(rules, rules[4])
	raw, err = common.Marshal(rules)
	require.NoError(t, err)
	_, err = ParseAutoBanRules(string(raw))
	require.Error(t, err)
}

func TestRiskyIPActionsRequireAbuseProviderAndPreserveMaskedSecretEpoch(t *testing.T) {
	current := GetAuditSecuritySetting()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(AuditSecurityModule, map[string]string{"ip_intelligence_providers_json": current.IPIntelligenceProvidersJSON}))
	})
	providers := []IPIntelligenceProvider{{ID: "fixture", Name: "Fixture", Type: IPProviderCustomIPAPI, Endpoint: "https://ip.example.com", Priority: 1, Enabled: true, Capabilities: []string{"abuse"}, APIKey: "fixture-not-a-real-secret"}}
	raw, err := common.Marshal(providers)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(AuditSecurityModule, map[string]string{"ip_intelligence_providers_json": string(raw)}))
	masked := SanitizedIPIntelligenceProvidersJSON(string(raw))
	unchanged, err := NormalizeAuditSecurityUpdateValues(map[string]string{"ip_intelligence_providers_json": masked})
	require.NoError(t, err)
	require.NotContains(t, unchanged, "auto_ban_effective_at")
	changed, err := NormalizeAuditSecurityUpdateValues(map[string]string{"ip_intelligence_providers_json": strings.ReplaceAll(masked, "ip.example.com", "new.example.com")})
	require.NoError(t, err)
	require.Equal(t, fmt.Sprint(time.Now().Unix()), changed["auto_ban_effective_at"])
	s := DefaultAuditSecuritySetting()
	s.AutoBanEnabled = true
	s.CaptureEnabled = true
	rules, err := ParseAutoBanRules(s.AutoBanRulesJSON)
	require.NoError(t, err)
	for i := range rules {
		rules[i].Selected = false
		rules[i].ClearAPIKeys = rules[i].ID == "risky_ip_sensitive_operation"
	}
	encoded, err := common.Marshal(rules)
	require.NoError(t, err)
	s.AutoBanRulesJSON = string(encoded)
	require.ErrorIs(t, ValidateAuditSecuritySetting(s), ErrSecurityAbuseLookupRequired)
	s.IPIntelligenceEnabled = true
	s.IPIntelligenceProvidersJSON = string(raw)
	require.NoError(t, ValidateAuditSecuritySetting(s))
	s.IPIntelligenceProvidersJSON = strings.ReplaceAll(string(raw), "abuse", "geo")
	require.ErrorIs(t, ValidateAuditSecuritySetting(s), ErrSecurityAbuseLookupRequired)
}
