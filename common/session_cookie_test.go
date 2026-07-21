package common

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveSessionCookieEnv(t *testing.T) {
	t.Helper()
	secureOrig, hadSecure := os.LookupEnv("SESSION_COOKIE_SECURE")
	urlOrig, hadURL := os.LookupEnv("SESSION_COOKIE_TRUSTED_URL")
	secVal, urls := SessionCookieSecure, SessionCookieTrustedURLs
	t.Cleanup(func() {
		restoreEnv("SESSION_COOKIE_SECURE", secureOrig, hadSecure)
		restoreEnv("SESSION_COOKIE_TRUSTED_URL", urlOrig, hadURL)
		SessionCookieSecure, SessionCookieTrustedURLs = secVal, urls
	})
}

func restoreEnv(key, val string, had bool) {
	if had {
		_ = os.Setenv(key, val)
	} else {
		_ = os.Unsetenv(key)
	}
}

func TestInitSessionCookieSettings(t *testing.T) {
	tests := []struct {
		name        string
		secure      string
		trustedURLs string
		wantErr     string
		wantSecure  bool
		wantURLs    []string
	}{
		{
			name:       "secure unset defaults to false",
			secure:     "",
			wantSecure: false,
		},
		{
			name:   "secure false with trusted url is an error",
			secure: "false", trustedURLs: "https://a.com",
			wantErr: "requires SESSION_COOKIE_SECURE=true",
		},
		{
			name:   "secure invalid value",
			secure: "yes",
			wantErr: "must be true or false",
		},
		{
			name:    "secure true without trusted url",
			secure:  "true",
			wantErr: "requires SESSION_COOKIE_TRUSTED_URL",
		},
		{
			name:   "secure true with empty entry",
			secure: "true", trustedURLs: "https://a.com,,https://b.com",
			wantErr: "contains an empty URL",
		},
		{
			name:   "secure true with non-https url",
			secure: "true", trustedURLs: "http://a.com",
			wantErr: "must contain only https URLs",
		},
		{
			name:   "secure true with host-less url",
			secure: "true", trustedURLs: "https://",
			wantErr: "must contain only https URLs",
		},
		{
			name:   "valid https trusted urls",
			secure: "true", trustedURLs: "https://a.com, https://b.com",
			wantSecure: true,
			wantURLs:   []string{"https://a.com", "https://b.com"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			saveSessionCookieEnv(t)
			if tc.secure == "" {
				_ = os.Unsetenv("SESSION_COOKIE_SECURE")
			} else {
				require.NoError(t, os.Setenv("SESSION_COOKIE_SECURE", tc.secure))
			}
			if tc.trustedURLs == "" {
				_ = os.Unsetenv("SESSION_COOKIE_TRUSTED_URL")
			} else {
				require.NoError(t, os.Setenv("SESSION_COOKIE_TRUSTED_URL", tc.trustedURLs))
			}

			err := InitSessionCookieSettings()
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSecure, SessionCookieSecure)
			if tc.wantURLs != nil {
				assert.Equal(t, tc.wantURLs, SessionCookieTrustedURLs)
			}
		})
	}
}

func TestInitSessionCookieSettings_InvalidURL(t *testing.T) {
	saveSessionCookieEnv(t)
	require.NoError(t, os.Setenv("SESSION_COOKIE_SECURE", "true"))
	require.NoError(t, os.Setenv("SESSION_COOKIE_TRUSTED_URL", "https://a.com,ht tp://bad"))
	err := InitSessionCookieSettings()
	require.Error(t, err)
	// either parse error or scheme error depending on how url.Parse treats it
	assert.Error(t, err)
}
