package passkey

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configurePasskey resets the live passkey settings + ServerAddress to a known
// clean state, applies the mutation, and restores the originals on cleanup.
// GetPasskeySettings returns a live pointer and lazily writes derived RPID /
// Origins back into it, so full snapshot/restore is mandatory to avoid
// cross-test pollution.
func configurePasskey(t *testing.T, serverAddr string, mutate func(s *system_setting.PasskeySettings)) *system_setting.PasskeySettings {
	t.Helper()
	s := system_setting.GetPasskeySettings()
	orig := *s
	origAddr := system_setting.ServerAddress
	t.Cleanup(func() {
		*s = orig
		system_setting.ServerAddress = origAddr
	})
	*s = system_setting.PasskeySettings{}
	system_setting.ServerAddress = serverAddr
	if mutate != nil {
		mutate(s)
	}
	return s
}

func plainRequest(t *testing.T, host string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = host
	r.TLS = nil
	r.URL.Scheme = ""
	return r
}

// ---------------------------------------------------------------------------
// detectScheme
// ---------------------------------------------------------------------------

func TestDetectScheme(t *testing.T) {
	t.Run("nil request returns empty", func(t *testing.T) {
		assert.Equal(t, "", detectScheme(nil))
	})

	t.Run("x-forwarded-proto first value lowercased", func(t *testing.T) {
		r := plainRequest(t, "ex.com")
		r.Header.Set("X-Forwarded-Proto", "HTTPS, http")
		assert.Equal(t, "https", detectScheme(r))
	})

	t.Run("tls connection implies https", func(t *testing.T) {
		r := plainRequest(t, "ex.com")
		r.TLS = &tls.ConnectionState{}
		assert.Equal(t, "https", detectScheme(r))
	})

	t.Run("url scheme used when present", func(t *testing.T) {
		r := plainRequest(t, "ex.com")
		r.URL.Scheme = "HTTPS"
		assert.Equal(t, "https", detectScheme(r))
	})

	t.Run("x-forwarded-protocol fallback", func(t *testing.T) {
		r := plainRequest(t, "ex.com")
		r.Header.Set("X-Forwarded-Protocol", "HTTPS")
		assert.Equal(t, "https", detectScheme(r))
	})

	t.Run("default is http", func(t *testing.T) {
		r := plainRequest(t, "ex.com")
		assert.Equal(t, "http", detectScheme(r))
	})
}

// ---------------------------------------------------------------------------
// hostWithoutPort
// ---------------------------------------------------------------------------

func TestHostWithoutPort(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"no port", "example.com", "example.com"},
		{"with port", "example.com:8443", "example.com"},
		{"ipv6 with port", "[::1]:443", "::1"},
		{"trailing colon empty port", "host:", "host"},
		{"invalid multi-colon falls through to original", "a:b:c", "a:b:c"},
		{"trimmed then processed", "  example.com:9000  ", "example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hostWithoutPort(tc.in))
		})
	}
}

// ---------------------------------------------------------------------------
// resolveOrigins
// ---------------------------------------------------------------------------

func TestResolveOrigins_ConfiguredList(t *testing.T) {
	t.Run("multiple https origins returned trimmed", func(t *testing.T) {
		s := &system_setting.PasskeySettings{Origins: " https://a.com , https://b.com "}
		got, err := resolveOrigins(plainRequest(t, "ignored"), s)
		require.NoError(t, err)
		assert.Equal(t, []string{"https://a.com", "https://b.com"}, got)
	})

	t.Run("http origin rejected when insecure disallowed", func(t *testing.T) {
		s := &system_setting.PasskeySettings{Origins: "http://insecure.com", AllowInsecureOrigin: false}
		_, err := resolveOrigins(plainRequest(t, "ignored"), s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "不安全")
	})

	t.Run("http origin allowed when insecure allowed", func(t *testing.T) {
		s := &system_setting.PasskeySettings{Origins: "http://insecure.com", AllowInsecureOrigin: true}
		got, err := resolveOrigins(plainRequest(t, "ignored"), s)
		require.NoError(t, err)
		assert.Equal(t, []string{"http://insecure.com"}, got)
	})

	t.Run("all-empty entries fall through to auto-detect", func(t *testing.T) {
		s := &system_setting.PasskeySettings{Origins: " , , "}
		r := plainRequest(t, "auto.com")
		r.TLS = &tls.ConnectionState{} // https
		got, err := resolveOrigins(r, s)
		require.NoError(t, err)
		assert.Equal(t, []string{"https://auto.com"}, got)
	})
}

func TestResolveOrigins_AutoDetect(t *testing.T) {
	t.Run("https from tls", func(t *testing.T) {
		s := &system_setting.PasskeySettings{}
		r := plainRequest(t, "ex.com")
		r.TLS = &tls.ConnectionState{}
		got, err := resolveOrigins(r, s)
		require.NoError(t, err)
		assert.Equal(t, []string{"https://ex.com"}, got)
	})

	t.Run("http non-local rejected", func(t *testing.T) {
		s := &system_setting.PasskeySettings{}
		_, err := resolveOrigins(plainRequest(t, "ex.com"), s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "仅支持 HTTPS")
	})

	t.Run("http allowed when insecure allowed", func(t *testing.T) {
		s := &system_setting.PasskeySettings{AllowInsecureOrigin: true}
		got, err := resolveOrigins(plainRequest(t, "ex.com"), s)
		require.NoError(t, err)
		assert.Equal(t, []string{"http://ex.com"}, got)
	})

	// localhost / loopback exemptions (condition coverage of the 4 sub-conditions)
	localhostCases := []struct{ host, want string }{
		{"localhost", "http://localhost"},
		{"127.0.0.1", "http://127.0.0.1"},
		{"localhost:3000", "http://localhost:3000"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
	}
	for _, tc := range localhostCases {
		t.Run("http allowed for "+tc.host, func(t *testing.T) {
			s := &system_setting.PasskeySettings{}
			got, err := resolveOrigins(plainRequest(t, tc.host), s)
			require.NoError(t, err)
			assert.Equal(t, []string{tc.want}, got)
		})
	}

	t.Run("empty host falls back to ServerAddress", func(t *testing.T) {
		origAddr := system_setting.ServerAddress
		system_setting.ServerAddress = "https://fromserver.com:9000"
		t.Cleanup(func() { system_setting.ServerAddress = origAddr })

		s := &system_setting.PasskeySettings{}
		r := plainRequest(t, "")
		r.Header.Set("X-Forwarded-Proto", "https") // avoid http-rejection with empty host
		got, err := resolveOrigins(r, s)
		require.NoError(t, err)
		assert.Equal(t, []string{"https://fromserver.com:9000"}, got)
	})

	t.Run("empty host and empty ServerAddress errors", func(t *testing.T) {
		origAddr := system_setting.ServerAddress
		system_setting.ServerAddress = ""
		t.Cleanup(func() { system_setting.ServerAddress = origAddr })

		s := &system_setting.PasskeySettings{}
		r := plainRequest(t, "")
		r.Header.Set("X-Forwarded-Proto", "https")
		_, err := resolveOrigins(r, s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "无法确定")
	})

	t.Run("ServerAddress without parseable host errors", func(t *testing.T) {
		origAddr := system_setting.ServerAddress
		system_setting.ServerAddress = "notaurl" // url.Parse => empty Host
		t.Cleanup(func() { system_setting.ServerAddress = origAddr })

		s := &system_setting.PasskeySettings{}
		r := plainRequest(t, "")
		r.Header.Set("X-Forwarded-Proto", "https")
		_, err := resolveOrigins(r, s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "无法确定")
	})
}

// ---------------------------------------------------------------------------
// resolveRPID
// ---------------------------------------------------------------------------

func TestResolveRPID(t *testing.T) {
	r := plainRequest(t, "ex.com")

	t.Run("configured rpid with port stripped", func(t *testing.T) {
		s := &system_setting.PasskeySettings{RPID: "example.com:8443"}
		got, err := resolveRPID(r, s, []string{"https://ignored"})
		require.NoError(t, err)
		assert.Equal(t, "example.com", got)
	})

	t.Run("configured rpid without port", func(t *testing.T) {
		s := &system_setting.PasskeySettings{RPID: "example.com"}
		got, err := resolveRPID(r, s, nil)
		require.NoError(t, err)
		assert.Equal(t, "example.com", got)
	})

	t.Run("derived from first origin host", func(t *testing.T) {
		s := &system_setting.PasskeySettings{RPID: "  "} // trimmed empty => derive
		got, err := resolveRPID(r, s, []string{"https://derived.com:8443"})
		require.NoError(t, err)
		assert.Equal(t, "derived.com", got)
	})

	t.Run("empty rpid and no origins errors", func(t *testing.T) {
		s := &system_setting.PasskeySettings{RPID: ""}
		_, err := resolveRPID(r, s, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "无法推导 RPID")
	})

	t.Run("unparseable origin errors", func(t *testing.T) {
		s := &system_setting.PasskeySettings{RPID: ""}
		_, err := resolveRPID(r, s, []string{"http://[::1"}) // malformed => url.Parse error
		require.Error(t, err)
		assert.Contains(t, err.Error(), "无法解析")
	})
}

// ---------------------------------------------------------------------------
// BuildWebAuthn
// ---------------------------------------------------------------------------

func TestBuildWebAuthn_Success(t *testing.T) {
	configurePasskey(t, "", func(s *system_setting.PasskeySettings) {
		s.RPID = "example.com"
		s.RPDisplayName = "My RP"
		s.Origins = "https://example.com"
		s.UserVerification = "required"
		s.AttachmentPreference = "platform"
	})

	wa, err := BuildWebAuthn(plainRequest(t, "example.com"))
	require.NoError(t, err)
	require.NotNil(t, wa)

	cfg := wa.Config
	assert.Equal(t, "example.com", cfg.RPID)
	assert.Equal(t, "My RP", cfg.RPDisplayName)
	assert.Equal(t, []string{"https://example.com"}, cfg.RPOrigins)

	sel := cfg.AuthenticatorSelection
	assert.Equal(t, protocol.ResidentKeyRequirementRequired, sel.ResidentKey)
	require.NotNil(t, sel.RequireResidentKey)
	assert.True(t, *sel.RequireResidentKey)
	assert.Equal(t, protocol.VerificationRequired, sel.UserVerification)
	assert.Equal(t, protocol.AuthenticatorAttachment("platform"), sel.AuthenticatorAttachment)

	assert.True(t, cfg.Timeouts.Login.Enforce)
	assert.Equal(t, 2*time.Minute, cfg.Timeouts.Login.Timeout)
	assert.Equal(t, 2*time.Minute, cfg.Timeouts.Registration.Timeout)
}

func TestBuildWebAuthn_DisplayNameFallsBackToSystemName(t *testing.T) {
	configurePasskey(t, "", func(s *system_setting.PasskeySettings) {
		s.RPID = "example.com"
		s.RPDisplayName = "   " // blank => fallback
		s.Origins = "https://example.com"
	})

	wa, err := BuildWebAuthn(plainRequest(t, "example.com"))
	require.NoError(t, err)
	assert.Equal(t, common.SystemName, wa.Config.RPDisplayName)
}

func TestBuildWebAuthn_UserVerificationDefaultsToPreferred(t *testing.T) {
	configurePasskey(t, "", func(s *system_setting.PasskeySettings) {
		s.RPID = "example.com"
		s.Origins = "https://example.com"
		s.UserVerification = "" // empty => default preferred
	})

	wa, err := BuildWebAuthn(plainRequest(t, "example.com"))
	require.NoError(t, err)
	assert.Equal(t, protocol.VerificationPreferred, wa.Config.AuthenticatorSelection.UserVerification)
}

func TestBuildWebAuthn_DerivesFromServerAddress(t *testing.T) {
	// RPID and Origins empty => GetPasskeySettings derives them from ServerAddress.
	configurePasskey(t, "https://newapi.pro", func(s *system_setting.PasskeySettings) {
		s.RPID = ""
		s.Origins = ""
	})

	wa, err := BuildWebAuthn(plainRequest(t, "newapi.pro"))
	require.NoError(t, err)
	assert.Equal(t, "newapi.pro", wa.Config.RPID)
	assert.Equal(t, []string{"https://newapi.pro"}, wa.Config.RPOrigins)
}

func TestBuildWebAuthn_PropagatesInsecureOriginError(t *testing.T) {
	configurePasskey(t, "", func(s *system_setting.PasskeySettings) {
		s.RPID = "example.com"
		s.Origins = "http://insecure.com"
		s.AllowInsecureOrigin = false
	})

	wa, err := BuildWebAuthn(plainRequest(t, "example.com"))
	require.Error(t, err)
	assert.Nil(t, wa)
	assert.Contains(t, err.Error(), "不安全")
}

func TestBuildWebAuthn_PropagatesRPIDDerivationError(t *testing.T) {
	// RPID empty + a syntactically-valid-but-unparseable origin => resolveRPID
	// fails inside BuildWebAuthn (covers the resolveRPID error branch).
	configurePasskey(t, "", func(s *system_setting.PasskeySettings) {
		s.RPID = ""
		s.Origins = "https://[::1" // passes the http/secure check, fails url.Parse
	})

	wa, err := BuildWebAuthn(plainRequest(t, "example.com"))
	require.Error(t, err)
	assert.Nil(t, wa)
	assert.Contains(t, err.Error(), "无法解析")
}

// ---------------------------------------------------------------------------
// End-to-end wiring: options built by the library carry our settings.
// (No crypto: we only assert the plumbing of RPID / display name / origins /
// credentials into the generated ceremony options.)
// ---------------------------------------------------------------------------

func TestBuildWebAuthn_BeginRegistrationCarriesSettings(t *testing.T) {
	configurePasskey(t, "", func(s *system_setting.PasskeySettings) {
		s.RPID = "example.com"
		s.RPDisplayName = "My RP"
		s.Origins = "https://example.com"
	})
	wa, err := BuildWebAuthn(plainRequest(t, "example.com"))
	require.NoError(t, err)

	wu := NewWebAuthnUser(&model.User{Id: 11, Username: "erin", DisplayName: "Erin"}, nil)
	creation, session, err := wa.BeginRegistration(wu)
	require.NoError(t, err)
	require.NotNil(t, creation)
	require.NotNil(t, session)

	assert.Equal(t, "example.com", creation.Response.RelyingParty.ID)
	assert.Equal(t, "My RP", creation.Response.RelyingParty.Name)
	assert.Equal(t, "Erin", creation.Response.User.DisplayName)
	assert.Equal(t, "erin", creation.Response.User.Name)
	assert.Equal(t, "example.com", session.RelyingPartyID)
}

func TestBuildWebAuthn_BeginLoginUsesCredentials(t *testing.T) {
	configurePasskey(t, "", func(s *system_setting.PasskeySettings) {
		s.RPID = "example.com"
		s.Origins = "https://example.com"
	})
	wa, err := BuildWebAuthn(plainRequest(t, "example.com"))
	require.NoError(t, err)

	cred := makeCredential(t)
	wu := NewWebAuthnUser(&model.User{Id: 12, Username: "frank"}, cred)

	assertion, session, err := wa.BeginLogin(wu)
	require.NoError(t, err)
	require.NotNil(t, assertion)
	require.NotNil(t, session)
	assert.Equal(t, "example.com", assertion.Response.RelyingPartyID)
	require.Len(t, assertion.Response.AllowedCredentials, 1)
	assert.Equal(t, []byte("cred-id-bytes"), []byte(assertion.Response.AllowedCredentials[0].CredentialID))
}

func TestBuildWebAuthn_BeginLoginNoCredentialsErrors(t *testing.T) {
	configurePasskey(t, "", func(s *system_setting.PasskeySettings) {
		s.RPID = "example.com"
		s.Origins = "https://example.com"
	})
	wa, err := BuildWebAuthn(plainRequest(t, "example.com"))
	require.NoError(t, err)

	wu := NewWebAuthnUser(&model.User{Id: 13, Username: "grace"}, nil) // no credential
	_, _, err = wa.BeginLogin(wu)
	require.Error(t, err) // library: "Found no credentials for user"
}
