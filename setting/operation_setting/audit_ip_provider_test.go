package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestIPProviderEmptyAndSecretLifecycle(t *testing.T) {
	defaults := DefaultAuditSecuritySetting()
	require.NoError(t, ValidateAuditSecuritySetting(defaults))
	providers, err := ParseIPIntelligenceProviders("")
	require.NoError(t, err)
	require.Empty(t, providers)
	s := defaults
	s.IPIntelligenceEnabled = true
	s.IPIntelligenceProvidersJSON = "[]"
	require.Error(t, ValidateAuditSecuritySetting(s))

	initial := []IPIntelligenceProvider{{ID: "selfhost", Name: "Selfhost", Type: IPProviderCustomIPAPI, Endpoint: "https://ip.example.com", Enabled: true, Priority: 100, APIKey: "first-secret", Capabilities: []string{"geo", "vpn"}}}
	raw, _ := common.Marshal(initial)
	previous := string(raw)
	initial[0].APIKey = ""
	raw, _ = common.Marshal(initial)
	merged, err := MergeIPIntelligenceProviders(string(raw), previous)
	require.NoError(t, err)
	parsed, err := ParseIPIntelligenceProviders(merged)
	require.NoError(t, err)
	require.Equal(t, "first-secret", parsed[0].APIKey)
	sanitized := SanitizedIPIntelligenceProvidersJSON(merged)
	require.NotContains(t, sanitized, "first-secret")
	require.Contains(t, sanitized, `"key_configured":true`)
	initial[0].ClearSecret = true
	raw, _ = common.Marshal(initial)
	merged, err = MergeIPIntelligenceProviders(string(raw), previous)
	require.NoError(t, err)
	parsed, err = ParseIPIntelligenceProviders(merged)
	require.NoError(t, err)
	require.Empty(t, parsed[0].APIKey)
	initial = append(initial, initial[0])
	initial[1].ID = "other"
	raw, _ = common.Marshal(initial)
	_, err = MergeIPIntelligenceProviders(string(raw), previous)
	require.Error(t, err)
	initial = initial[:1]
	initial[0].Type = IPProviderProxycheck
	raw, _ = common.Marshal(initial)
	_, err = MergeIPIntelligenceProviders(string(raw), previous)
	require.Error(t, err)

}

func TestIPProviderValidationBoundaries(t *testing.T) {
	base := IPIntelligenceProvider{ID: "self-host", Name: "Self host", Type: IPProviderCustomIPAPI, Endpoint: "https://ipapi.internal", Enabled: true, Priority: 1, DailyLimit: 0, Capabilities: []string{"geo", "tor"}, AllowedPrivateHosts: []string{"ipapi.internal"}}
	raw, _ := common.Marshal([]IPIntelligenceProvider{base})
	_, err := ParseIPIntelligenceProviders(string(raw))
	require.NoError(t, err)
	base.AllowedPrivateHosts = []string{"*.internal"}
	raw, _ = common.Marshal([]IPIntelligenceProvider{base})
	_, err = ParseIPIntelligenceProviders(string(raw))
	require.Error(t, err)
	base.AllowedPrivateHosts = nil
	base.Endpoint = "http://ipapi.internal"
	raw, _ = common.Marshal([]IPIntelligenceProvider{base})
	_, err = ParseIPIntelligenceProviders(string(raw))
	require.Error(t, err)
}

func TestIPProviderEnabledSettingRequiresCallableProvider(t *testing.T) {
	selfhost := IPIntelligenceProvider{ID: "selfhost", Name: "Selfhost", Type: IPProviderCustomIPAPI, Endpoint: "https://ip.example.com", Enabled: true, Priority: 1, Capabilities: []string{"geo"}}
	disabled := selfhost
	disabled.Enabled = false
	second := selfhost
	second.ID = "second"
	legacy := selfhost
	legacy.ID, legacy.Type = "legacy", IPProviderIPAPI
	proxycheck, ipinfo := legacy, legacy
	proxycheck.Type, ipinfo.Type = IPProviderProxycheck, IPProviderIPInfoLite
	for _, tc := range []struct {
		name      string
		providers []IPIntelligenceProvider
		wantError bool
	}{
		{"empty", nil, true},
		{"legacy ipapi", []IPIntelligenceProvider{legacy}, true},
		{"legacy proxycheck", []IPIntelligenceProvider{proxycheck}, true},
		{"legacy ipinfo", []IPIntelligenceProvider{ipinfo}, true},
		{"disabled selfhost", []IPIntelligenceProvider{disabled}, true},
		{"legacy and disabled selfhost", []IPIntelligenceProvider{legacy, disabled}, true},
		{"one selfhost", []IPIntelligenceProvider{selfhost}, false},
		{"legacy and one selfhost", []IPIntelligenceProvider{legacy, selfhost}, false},
		{"multiple selfhosts", []IPIntelligenceProvider{selfhost, second}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := DefaultAuditSecuritySetting()
			raw, err := common.Marshal(tc.providers)
			require.NoError(t, err)
			s.IPIntelligenceProvidersJSON = string(raw)
			require.NoError(t, ValidateAuditSecuritySetting(s), "inactive legacy settings remain readable")
			s.IPIntelligenceEnabled = true
			require.Equal(t, tc.wantError, ValidateAuditSecuritySetting(s) != nil)

			previous, err := config.ConfigToMap(GetAuditSecuritySetting())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, config.GlobalConfig.UpdateFromMap(AuditSecurityModule, previous)) })
			require.NoError(t, config.GlobalConfig.UpdateFromMap(AuditSecurityModule, map[string]string{"ip_intelligence_providers_json": string(raw)}))
			_, err = ParseAuditSecurityUpdate(map[string]string{"ip_intelligence_enabled": "true"})
			require.Equal(t, tc.wantError, err != nil, "enabling a persisted configuration must use the same validation")
			_, err = ParseAuditSecurityUpdate(map[string]string{"ip_intelligence_enabled": "false"})
			require.NoError(t, err)
		})
	}
}
