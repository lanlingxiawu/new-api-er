package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRedirectURL(t *testing.T) {
	orig := constant.TrustedRedirectDomains
	t.Cleanup(func() { constant.TrustedRedirectDomains = orig })
	constant.TrustedRedirectDomains = []string{"example.com", "trusted.org"}

	tests := []struct {
		name    string
		url     string
		wantErr string // substring; "" means no error
	}{
		{"exact trusted host", "https://example.com/path", ""},
		{"subdomain of trusted host", "https://app.example.com/cb", ""},
		{"http scheme allowed", "http://example.com", ""},
		{"second trusted domain", "https://trusted.org/x", ""},
		{"case-insensitive host", "https://EXAMPLE.com", ""},
		{"untrusted domain", "https://evil.com", "not in the trusted domains list"},
		{"lookalike suffix not subdomain", "https://notexample.com", "not in the trusted domains list"},
		{"disallowed scheme ftp", "ftp://example.com", "invalid URL scheme"},
		{"disallowed scheme empty", "example.com", "invalid URL scheme"},
		{"malformed url", "http://[::1", "invalid URL format"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRedirectURL(tc.url)
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateRedirectURL_EmptyTrustedList(t *testing.T) {
	orig := constant.TrustedRedirectDomains
	t.Cleanup(func() { constant.TrustedRedirectDomains = orig })
	constant.TrustedRedirectDomains = nil

	err := ValidateRedirectURL("https://example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in the trusted domains list")
}
