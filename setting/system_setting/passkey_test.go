package system_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// savePasskeyState snapshots every global GetPasskeySettings() reads or
// mutates, and restores them on cleanup. GetPasskeySettings lazily writes back
// into defaultPasskeySettings, so tests must reset it to stay independent.
func savePasskeyState(t *testing.T) {
	t.Helper()
	origSettings := defaultPasskeySettings
	origServer := ServerAddress
	t.Cleanup(func() {
		defaultPasskeySettings = origSettings
		ServerAddress = origServer
	})
}

func TestGetPasskeySettings_Defaults(t *testing.T) {
	savePasskeyState(t)
	// Force a clean baseline independent of package init side effects.
	defaultPasskeySettings = PasskeySettings{
		Enabled:              false,
		RPDisplayName:        common.SystemName,
		RPID:                 "example.rp", // non-empty so derivation is skipped
		Origins:              "https://set.example",
		AllowInsecureOrigin:  false,
		UserVerification:     "preferred",
		AttachmentPreference: "",
	}
	ServerAddress = "https://server.example"

	got := GetPasskeySettings()
	require.NotNil(t, got)
	assert.Same(t, &defaultPasskeySettings, got)
	// Neither RPID nor Origins was empty, so nothing is derived.
	assert.Equal(t, "example.rp", got.RPID)
	assert.Equal(t, "https://set.example", got.Origins)
	assert.Equal(t, "preferred", got.UserVerification)
}

// RPID derivation — decision/condition coverage of:
//
//	if RPID == "" && ServerAddress != "" { ...url.Parse... }
func TestGetPasskeySettings_RPIDDerivation(t *testing.T) {
	tests := []struct {
		name          string
		rpid          string
		serverAddress string
		wantRPID      string
	}{
		{
			// RPID empty + ServerAddress a full URL -> parsed.Host wins.
			name:          "url_with_scheme_uses_host",
			rpid:          "",
			serverAddress: "https://newapi.pro",
			wantRPID:      "newapi.pro",
		},
		{
			// URL with scheme + port: Host retains the port.
			name:          "url_with_scheme_and_port_keeps_port",
			rpid:          "",
			serverAddress: "https://newapi.pro:8443",
			wantRPID:      "newapi.pro:8443",
		},
		{
			// Bare host, no scheme: url.Parse succeeds but Host is empty
			// (the value lands in Path) -> else branch, RPID = trimmed addr.
			name:          "bare_host_no_scheme_falls_back_to_addr",
			rpid:          "",
			serverAddress: "newapi.pro",
			wantRPID:      "newapi.pro",
		},
		{
			// Surrounding whitespace is trimmed before parsing/fallback.
			name:          "whitespace_is_trimmed",
			rpid:          "",
			serverAddress: "   https://trimmed.example   ",
			wantRPID:      "trimmed.example",
		},
		{
			// url.Parse returns an error (control byte) -> else branch uses
			// the trimmed raw address.
			name:          "parse_error_falls_back_to_addr",
			rpid:          "",
			serverAddress: "http://a\x7fb",
			wantRPID:      "http://a\x7fb",
		},
		{
			// RPID already set -> derivation skipped entirely (first
			// sub-condition false).
			name:          "existing_rpid_is_preserved",
			rpid:          "preset.rp",
			serverAddress: "https://ignored.example",
			wantRPID:      "preset.rp",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			savePasskeyState(t)
			defaultPasskeySettings = PasskeySettings{
				RPID:    tc.rpid,
				Origins: "https://origins.set", // non-empty: isolate RPID logic
			}
			ServerAddress = tc.serverAddress

			got := GetPasskeySettings()
			assert.Equal(t, tc.wantRPID, got.RPID)
		})
	}
}

// The second sub-condition of the RPID guard: ServerAddress == "" means no
// derivation even though RPID is empty (condition coverage: RPID=="" true,
// ServerAddress!="" false).
func TestGetPasskeySettings_RPIDNotDerivedWhenServerAddressEmpty(t *testing.T) {
	savePasskeyState(t)
	defaultPasskeySettings = PasskeySettings{
		RPID:    "",
		Origins: "https://origins.set",
	}
	ServerAddress = ""

	got := GetPasskeySettings()
	assert.Equal(t, "", got.RPID)
}

// Origins fallback — decision/condition coverage of:
//
//	if Origins == "" || Origins == "[]" { Origins = ServerAddress }
func TestGetPasskeySettings_OriginsFallback(t *testing.T) {
	tests := []struct {
		name          string
		origins       string
		serverAddress string
		wantOrigins   string
	}{
		{
			name:          "empty_origins_uses_server_address",
			origins:       "",
			serverAddress: "https://srv.example",
			wantOrigins:   "https://srv.example",
		},
		{
			name:          "empty_json_array_string_uses_server_address",
			origins:       "[]",
			serverAddress: "https://srv.example",
			wantOrigins:   "https://srv.example",
		},
		{
			name:          "non_empty_origins_preserved",
			origins:       "https://already.example",
			serverAddress: "https://srv.example",
			wantOrigins:   "https://already.example",
		},
		{
			name:          "empty_origins_with_empty_server_address_stays_empty",
			origins:       "",
			serverAddress: "",
			wantOrigins:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			savePasskeyState(t)
			defaultPasskeySettings = PasskeySettings{
				RPID:    "preset.rp", // non-empty: isolate Origins logic
				Origins: tc.origins,
			}
			ServerAddress = tc.serverAddress

			got := GetPasskeySettings()
			assert.Equal(t, tc.wantOrigins, got.Origins)
		})
	}
}

// Both derivations fire together on a fresh (empty) config — path coverage of
// the full function with both if-blocks taken.
func TestGetPasskeySettings_BothDerivationsTogether(t *testing.T) {
	savePasskeyState(t)
	defaultPasskeySettings = PasskeySettings{RPID: "", Origins: ""}
	ServerAddress = "https://combined.example"

	got := GetPasskeySettings()
	assert.Equal(t, "combined.example", got.RPID)
	assert.Equal(t, "https://combined.example", got.Origins)
}

// The lazy write-back persists: a second call sees the value the first derived
// and no longer re-derives (RPID now non-empty).
func TestGetPasskeySettings_DerivationPersistsAcrossCalls(t *testing.T) {
	savePasskeyState(t)
	defaultPasskeySettings = PasskeySettings{RPID: "", Origins: ""}
	ServerAddress = "https://first.example"
	assert.Equal(t, "first.example", GetPasskeySettings().RPID)

	// Change ServerAddress; RPID is now set, so it must NOT be re-derived.
	ServerAddress = "https://second.example"
	assert.Equal(t, "first.example", GetPasskeySettings().RPID)
}
