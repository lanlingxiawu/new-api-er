package system_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// savePasskeyState snapshots the stored passkey settings and ServerAddress,
// and restores them on cleanup.
func savePasskeyState(t *testing.T) {
	t.Helper()
	origSettings := *GetPasskeySettings()
	origServer := ServerAddress
	t.Cleanup(func() {
		ServerAddress = origServer
		setPasskeySettingsForTest(origSettings)
	})
}

// setPasskeySettingsForTest 替换存储值并重新发布快照。
// 直接给 defaultPasskeySettings 赋值不会更新 GetPasskeySettings 读的快照。
func setPasskeySettingsForTest(s PasskeySettings) {
	ReplacePasskeySettings(s)
}

func TestGetPasskeySettings_Defaults(t *testing.T) {
	savePasskeyState(t)
	setPasskeySettingsForTest(PasskeySettings{
		Enabled:              false,
		RPDisplayName:        common.SystemName,
		RPID:                 "example.rp",
		Origins:              "https://set.example",
		AllowInsecureOrigin:  false,
		UserVerification:     "preferred",
		AttachmentPreference: "",
	})
	ServerAddress = "https://server.example"

	got := GetPasskeySettings()
	require.NotNil(t, got)
	// 返回不可变快照，不再是可变全局的指针。
	assert.NotSame(t, &defaultPasskeySettings, got)
	assert.Equal(t, "example.rp", got.RPID)
	assert.Equal(t, "https://set.example", got.Origins)
	assert.Equal(t, "preferred", got.UserVerification)
}

// GetPasskeySettings returns stored values only; RPID / Origins defaults are
// applied by WithDefaults / PasskeySettingsSnapshot and never written back.
func TestGetPasskeySettings_ReturnsStoredValuesWithoutDefaults(t *testing.T) {
	savePasskeyState(t)
	setPasskeySettingsForTest(PasskeySettings{RPID: "", Origins: ""})
	ServerAddress = "https://combined.example"

	got := GetPasskeySettings()
	assert.Equal(t, "", got.RPID)
	assert.Equal(t, "", got.Origins)
}

// RPID derivation — decision/condition coverage of:
//
//	if RPID == "" && serverAddress != "" { ...url.Parse... }
func TestPasskeySettingsWithDefaults_RPIDDerivation(t *testing.T) {
	tests := []struct {
		name          string
		rpid          string
		serverAddress string
		wantRPID      string
	}{
		{
			name:          "url_with_scheme_uses_host",
			rpid:          "",
			serverAddress: "https://newapi.pro",
			wantRPID:      "newapi.pro",
		},
		{
			name:          "url_with_scheme_and_port_keeps_port",
			rpid:          "",
			serverAddress: "https://newapi.pro:8443",
			wantRPID:      "newapi.pro:8443",
		},
		{
			// Bare host, no scheme: url.Parse succeeds but Host is empty -> raw addr.
			name:          "bare_host_no_scheme_falls_back_to_addr",
			rpid:          "",
			serverAddress: "newapi.pro",
			wantRPID:      "newapi.pro",
		},
		{
			name:          "whitespace_is_trimmed",
			rpid:          "",
			serverAddress: "   https://trimmed.example   ",
			wantRPID:      "trimmed.example",
		},
		{
			// url.Parse error (control byte) -> trimmed raw address.
			name:          "parse_error_falls_back_to_addr",
			rpid:          "",
			serverAddress: "http://ab",
			wantRPID:      "http://ab",
		},
		{
			name:          "existing_rpid_is_preserved",
			rpid:          "preset.rp",
			serverAddress: "https://ignored.example",
			wantRPID:      "preset.rp",
		},
		{
			// RPID empty but no server address -> nothing derived.
			name:          "empty_server_address_derives_nothing",
			rpid:          "",
			serverAddress: "",
			wantRPID:      "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PasskeySettings{RPID: tc.rpid, Origins: "https://origins.set"}.WithDefaults(tc.serverAddress)
			assert.Equal(t, tc.wantRPID, got.RPID)
		})
	}
}

// Origins fallback — decision/condition coverage of:
//
//	if Origins == "" || Origins == "[]" { Origins = serverAddress }
func TestPasskeySettingsWithDefaults_OriginsFallback(t *testing.T) {
	tests := []struct {
		name          string
		origins       string
		serverAddress string
		wantOrigins   string
	}{
		{"empty_origins_uses_server_address", "", "https://srv.example", "https://srv.example"},
		{"empty_json_array_string_uses_server_address", "[]", "https://srv.example", "https://srv.example"},
		{"non_empty_origins_preserved", "https://already.example", "https://srv.example", "https://already.example"},
		{"empty_origins_with_empty_server_address_stays_empty", "", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PasskeySettings{RPID: "preset.rp", Origins: tc.origins}.WithDefaults(tc.serverAddress)
			assert.Equal(t, tc.wantOrigins, got.Origins)
		})
	}
}

// PasskeySettingsSnapshot applies both defaults from the current ServerAddress
// on every call, so a ServerAddress change is reflected immediately.
func TestPasskeySettingsSnapshot_AppliesCurrentServerAddress(t *testing.T) {
	savePasskeyState(t)
	setPasskeySettingsForTest(PasskeySettings{RPID: "", Origins: ""})

	ServerAddress = "https://first.example"
	got := PasskeySettingsSnapshot()
	assert.Equal(t, "first.example", got.RPID)
	assert.Equal(t, "https://first.example", got.Origins)

	ServerAddress = "https://second.example"
	got = PasskeySettingsSnapshot()
	assert.Equal(t, "second.example", got.RPID)
	assert.Equal(t, "https://second.example", got.Origins)

	// stored values untouched
	assert.Equal(t, "", GetPasskeySettings().RPID)
}
