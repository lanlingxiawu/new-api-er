package system_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The "simple" settings in this package (discord, fetch_setting, legal, oidc,
// theme) all follow the Rule 12 pattern: a package-level struct var registered
// with config.GlobalConfig, and a getter that returns the live pointer. These
// tests assert the registration, the default values, and the pointer-identity
// contract of each getter.

func TestGetDiscordSettings_ReturnsLivePointerAndDefaults(t *testing.T) {
	got := GetDiscordSettings()
	require.NotNil(t, got)
	// Same instance is returned every call (Rule 12: never a copy).
	assert.Same(t, &defaultDiscordSettings, got)
	assert.Same(t, got, GetDiscordSettings())

	// Defaults: zero value struct.
	assert.False(t, got.Enabled)
	assert.Equal(t, "", got.ClientId)
	assert.Equal(t, "", got.ClientSecret)
}

func TestGetOIDCSettings_ReturnsLivePointerAndDefaults(t *testing.T) {
	got := GetOIDCSettings()
	require.NotNil(t, got)
	assert.Same(t, &defaultOIDCSettings, got)
	assert.Same(t, got, GetOIDCSettings())

	// Defaults: zero value struct.
	assert.False(t, got.Enabled)
	assert.Equal(t, "", got.ClientId)
	assert.Equal(t, "", got.ClientSecret)
	assert.Equal(t, "", got.WellKnown)
	assert.Equal(t, "", got.AuthorizationEndpoint)
	assert.Equal(t, "", got.TokenEndpoint)
	assert.Equal(t, "", got.UserInfoEndpoint)
}

func TestGetLegalSettings_ReturnsLivePointerAndDefaults(t *testing.T) {
	got := GetLegalSettings()
	require.NotNil(t, got)
	assert.Same(t, &defaultLegalSettings, got)
	assert.Same(t, got, GetLegalSettings())

	assert.Equal(t, "", got.UserAgreement)
	assert.Equal(t, "", got.PrivacyPolicy)
}

func TestGetFetchSetting_ReturnsLivePointerAndDefaults(t *testing.T) {
	got := GetFetchSetting()
	require.NotNil(t, got)
	assert.Same(t, &defaultFetchSetting, got)
	assert.Same(t, got, GetFetchSetting())

	// Defaults per fetch_setting.go: SSRF protection on, private IP off,
	// blacklist modes, empty domain/ip lists, a fixed allowed-ports set,
	// and IP filter applied to domains.
	assert.True(t, got.EnableSSRFProtection)
	assert.False(t, got.AllowPrivateIp)
	assert.False(t, got.DomainFilterMode)
	assert.False(t, got.IpFilterMode)
	assert.Equal(t, []string{}, got.DomainList)
	assert.Equal(t, []string{}, got.IpList)
	assert.Equal(t, []string{"80", "443", "8080", "8443"}, got.AllowedPorts)
	assert.True(t, got.ApplyIPFilterForDomain)
}

// TestSimpleSettings_MutationVisibleThroughGetter verifies the live-pointer
// contract has observable effect: mutating through the returned pointer is seen
// by the next getter call (they alias the same struct).
func TestSimpleSettings_MutationVisibleThroughGetter(t *testing.T) {
	d := GetDiscordSettings()
	orig := *d
	t.Cleanup(func() { *d = orig })

	d.Enabled = true
	d.ClientId = "abc"
	assert.True(t, GetDiscordSettings().Enabled)
	assert.Equal(t, "abc", GetDiscordSettings().ClientId)
}

// TestConfig_RegistrationBindsLivePointers confirms each simple setting was
// registered under its documented key by the package init() functions
// (Rule 12: ConfigManager persistence keyed by module name), and that the
// registered instance is the exact package var the getter exposes — so a
// LoadFromDB write lands on the same struct the getter reads.
func TestConfig_RegistrationBindsLivePointers(t *testing.T) {
	cases := map[string]interface{}{
		"discord":       &defaultDiscordSettings,
		"oidc":          &defaultOIDCSettings,
		"legal":         &defaultLegalSettings,
		"fetch_setting": &defaultFetchSetting,
		"passkey":       &defaultPasskeySettings,
	}
	for key, want := range cases {
		got := config.GlobalConfig.Get(key)
		require.NotNilf(t, got, "expected config key %q to be registered", key)
		assert.Samef(t, want, got, "config key %q should bind the live package var", key)
	}
}
