package common

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsCredentialHeaderName(t *testing.T) {
	for _, name := range []string{
		"Authorization", "authorization", "Proxy-Authorization", "X-Auth-Token",
		"Cookie", "Set-Cookie", "X-Api-Key", "Api-Key", "x-goog-api-key",
		"Anthropic-Api-Key", "Mj-Api-Secret", "X-Amz-Security-Token",
		"Sec-WebSocket-Protocol", "X-Session-Token", "X-Password",
	} {
		assert.True(t, IsCredentialHeaderName(name), name)
	}
	for _, name := range []string{
		"Content-Type", "Content-Length", "User-Agent", "Accept", "X-Request-Id",
		"X-Forwarded-For", "Openai-Organization", "Anthropic-Version", "",
	} {
		assert.False(t, IsCredentialHeaderName(name), name)
	}
}

func TestRedactCredentialHeadersJSON(t *testing.T) {
	raw, err := Marshal(http.Header{
		"Authorization":          {"Bearer sk-secret"},
		"X-Goog-Api-Key":         {"AIza-secret"},
		"Cookie":                 {"a=1", "b=2"},
		"Sec-Websocket-Protocol": {"realtime, openai-insecure-api-key.sk-secret"},
		"Content-Type":           {"application/json"},
	})
	require.NoError(t, err)

	got, ok := RedactCredentialHeadersJSON(string(raw))
	require.True(t, ok)
	assert.NotContains(t, got, "secret")
	var headers http.Header
	require.NoError(t, UnmarshalJsonStr(got, &headers))
	assert.Equal(t, []string{"***"}, headers["Authorization"])
	assert.Equal(t, []string{"***", "***"}, headers["Cookie"], "every value is masked, the count is kept")
	assert.Equal(t, []string{"application/json"}, headers["Content-Type"])
}

func TestRedactCredentialHeadersJSONEdgeCases(t *testing.T) {
	got, ok := RedactCredentialHeadersJSON("")
	assert.True(t, ok)
	assert.Empty(t, got)

	plain := `{"Content-Type":["application/json"]}`
	got, ok = RedactCredentialHeadersJSON(plain)
	assert.True(t, ok)
	assert.Equal(t, plain, got, "nothing to mask keeps the stored bytes")

	// Headers cut at the size limit end with the truncation mark and are not
	// valid JSON; a half-kept credential cannot be masked, so it is withheld.
	for _, broken := range []string{
		`{"Authorization":["Bearer sk-sec` + "\n...[truncated]",
		`not json`,
		`["Authorization"]`,
	} {
		got, ok = RedactCredentialHeadersJSON(broken)
		assert.False(t, ok, broken)
		assert.Empty(t, got)
	}
}
