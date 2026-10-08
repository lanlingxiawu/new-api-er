package common

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var redactURICases = []struct {
	name string
	uri  string
	want string
}{
	{"no query", "/v1/chat/completions", "/v1/chat/completions"},
	{"no credential", "/r?a=1&b=2", "/r?a=1&b=2"},
	{"gemini key", "/v1beta/models/m:generateContent?key=sk-1", "/v1beta/models/m:generateContent?key=***"},
	{"key in the middle keeps order", "/r?alt=sse&key=sk-1&x=y", "/r?alt=sse&key=***&x=y"},
	{"case insensitive", "/r?KEY=sk-1", "/r?KEY=***"},
	{"percent-encoded name", "/r?%6Bey=sk-1", "/r?%6Bey=***"},
	{"repeated", "/r?key=a&key=b", "/r?key=***&key=***"},
	{"other credential names", "/r?api_key=a&access_token=b&token=c&x-goog-api-key=d",
		"/r?api_key=***&access_token=***&token=***&x-goog-api-key=***"},
	{"prefix of a name is not a match", "/r?keyword=abc&monkey=1", "/r?keyword=abc&monkey=1"},
	{"bare name without value", "/r?key&a=1", "/r?key&a=1"},
	{"empty value", "/r?key=", "/r?key=***"},
	{"empty segments preserved", "/r?&key=a&&b=1", "/r?&key=***&&b=1"},
	{"value containing =", "/r?token=a=b=c", "/r?token=***"},
	{"aws presigned", "/r?X-Amz-Credential=c&X-Amz-Security-Token=t&X-Amz-Signature=s&AWSAccessKeyId=a&X-Amz-Date=20260101",
		"/r?X-Amz-Credential=***&X-Amz-Security-Token=***&X-Amz-Signature=***&AWSAccessKeyId=***&X-Amz-Date=20260101"},
	{"signature names", "/r?signature=a&sig=b&passwd=c", "/r?signature=***&sig=***&passwd=***"},
	{"substring names", "/r?session_token=a&My_Secret=b&db_password=c&client_credential=d",
		"/r?session_token=***&My_Secret=***&db_password=***&client_credential=***"},

	// A client that percent-encodes ?key=<token> into the path (Gemini style)
	// must not get the token logged as part of the path.
	{"path: %3F credential", "/v1beta/models/m%3Fkey=sk-1:generateContent", "/v1beta/models/m%3Fkey=***"},
	{"path: lower-case %3f", "/v1beta/models/m%3fkey=sk-1:generateContent", "/v1beta/models/m%3fkey=***"},
	{"path and real query both redacted", "/v1beta/models/m%3Fkey=sk-1:generateContent?key=sk-2&alt=sse",
		"/v1beta/models/m%3Fkey=***?key=***&alt=sse"},
	{"path: encoded =", "/m%3Fkey%3Dsk-1", "/m%3Fkey%3D***"},
	{"path: lower-case encoded =", "/m%3fkey%3dsk-1", "/m%3fkey%3d***"},
	{"path: double-encoded, value stops at encoded &", "/m%253Fkey%253Dsk-1%2526alt%253Dsse",
		"/m%253Fkey%253D***%2526alt%253Dsse"},
	{"path: triple-encoded", "/m%25253Fkey%25253Dsk-1", "/m%25253Fkey%25253D***"},
	{"path: encoded name", "/m%3F%6Bey=sk-1", "/m%3F%6Bey=***"},
	{"path: several names and separators", "/m%3Falt=sse%26access_token=t1;api_key=t2",
		"/m%3Falt=sse%26access_token=***;api_key=***"},
	{"path: value is not cut at / or :", "/m%3Fkey=ab/cd:generateContent", "/m%3Fkey=***"},
	{"path: empty value", "/m%3Fkey=", "/m%3Fkey=***"},
	{"path: empty value before next pair", "/m%3Fkey=%26x=1", "/m%3Fkey=***%26x=1"},
	{"path: name right after /", "/key=sk-1", "/key=***"},
	{"path: prefix of a name is not a match", "/m%3Fkeyword=abc%26monkey=1", "/m%3Fkeyword=abc%26monkey=1"},
	{"path: = without credential name", "/files/a=b/c%3Fd", "/files/a=b/c%3Fd"},
	{"path: name without =", "/v1beta/models/m%3Fkey:generateContent", "/v1beta/models/m%3Fkey:generateContent"},
	{"path: dotted credential name", "/models/gemini.key=sk-1", "/models/gemini.key=***"},
	{"path: dotted name after %3F", "/models/m%3Fx.api_key=sk-1", "/models/m%3Fx.api_key=***"},
	{"path: dotted non-credential names", "/models/gemini.keyword=1/key.v2=2", "/models/gemini.keyword=1/key.v2=2"},

	// A credential behind an encoded "&" is inside another parameter's value.
	{"query: credential hidden in a value", "/r?alt=sse%26key=sk-1", "/r?alt=sse%26key=***"},
	{"query: lower-case and double-encoded in a value", "/r?alt=sse%2526key%253Dsk-1&x=1", "/r?alt=sse%2526key%253D***&x=1"},
	{"query: hidden credential and real credential", "/r?alt=sse%26access_token=t1&key=sk-2", "/r?alt=sse%26access_token=***&key=***"},
	{"query: value with = but no credential", "/r?q=a%3Db&filter=x=y", "/r?q=a%3Db&filter=x=y"},
	{"query: dotted credential name", "/r?gemini.key=sk-1&a.b=c", "/r?gemini.key=***&a.b=c"},
	{"query: dotted non-credential name", "/r?gemini.keyword=1", "/r?gemini.keyword=1"},
}

// Every name either list used to cover is a credential; ordinary parameters are not.
func TestIsCredentialQueryName(t *testing.T) {
	sensitive := []string{
		// exact names
		"key", "api_key", "apikey", "api-key", "x-api-key", "x-goog-api-key", "auth", "authorization",
		"passwd", "sig", "awsaccesskeyid",
		// covered by the token/secret/signature/password/credential substrings
		"token", "access_token", "id_token", "refresh_token", "session_token", "x-amz-security-token",
		"secret", "client_secret", "my_secret", "signature", "x-amz-signature", "some_signature_thing",
		"password", "db_password", "x-amz-credential", "credential",
		// case and surrounding whitespace do not matter
		"KEY", " Token ", "X-Goog-Api-Key", "X-AMZ-SIGNATURE",
	}
	for _, name := range sensitive {
		assert.True(t, IsCredentialQueryName(name), name)
	}
	for _, name := range []string{"", "  ", "model", "alt", "api-version", "keyword", "monkey", "signal", "passport", "X-Amz-Date", "tok"} {
		assert.False(t, IsCredentialQueryName(name), name)
	}
}

func TestRedactRequestURI(t *testing.T) {
	for _, tc := range redactURICases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.ParseRequestURI(tc.uri)
			require.NoError(t, err)
			assert.Equal(t, tc.want, RedactRequestURI(u))
		})
	}
	assert.Equal(t, "", RedactRequestURI(nil))
}

// The string form must agree with the URL form, and applying it again must not
// change the result (request-log entries may be scrubbed more than once).
func TestRedactURIString(t *testing.T) {
	for _, tc := range redactURICases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactURIString(tc.uri)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, got, RedactURIString(got), "idempotent")
		})
	}
	assert.Equal(t, "", RedactURIString(""))
	assert.Equal(t, "/r?", RedactURIString("/r?"), "empty query")
	assert.Equal(t, "?key=***", RedactURIString("?key=sk"), "query without path")
	// Only the first ? starts the query; a later ? is part of a value.
	assert.Equal(t, "/r?q=a?b&key=***", RedactURIString("/r?q=a?b&key=sk"))
}

// The common case (no credential in the query) must not allocate: the access
// log runs this on every request, relay included.
func TestRedactQueryCredentials_NoMatchNoAlloc(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = RedactQueryCredentials("alt=sse&model=gemini-pro")
		_ = RedactURIString("/v1beta/models/m:generateContent?alt=sse")
		_ = RedactURIString("/v1/chat/completions")
		_, _ = RedactQueryCredentials("Alt=SSE&Model=Gemini-Pro&X-Amz-Date=1")
		// paths with escapes or "=" but no credential
		_ = RedactURIString("/v1beta/models/m%3Fb:generateContent?alt=sse")
		_ = RedactURIString("/m%3Fkeyword=abc%26a=b/%6Beyword%3D1")
		_, _ = RedactPathCredentials("/m%253F%2561lt%253Dsse")
		// query values and dotted names that are not credentials
		_, _ = RedactQueryCredentials("alt=sse&q=a%3Db&filter=x=y&gemini.keyword=1")
		_ = RedactURIString("/models/gemini.keyword=1?a.b=c")
	})
	assert.Zero(t, allocs)
}

// Inputs a parsed URL never produces but a stored request-log URL might.
func TestRedactPathCredentials_Edges(t *testing.T) {
	cases := []struct{ name, path, want string }{
		{"literal % before a name", "/r/100%/key=sk", "/r/100%/key=***"},
		{"truncated escape at the end", "/m%3Fkey=sk%3", "/m%3Fkey=***"},
		{"lone % at the end", "/m%", "/m%"},
		{"= at the start", "=sk", "=sk"},
		{"value stops at encoded ?", "/m%3Fkey=a%3Fb=c", "/m%3Fkey=***%3Fb=c"},
		{"already redacted", "/m%3Fkey=***%26x=1", "/m%3Fkey=***%26x=1"},
	}
	for _, tc := range cases {
		got, changed := RedactPathCredentials(tc.path)
		assert.Equal(t, tc.want, got, tc.name)
		assert.Equal(t, tc.want != tc.path, changed, tc.name)
	}

	// An encoded name longer than the decode buffer cannot be checked, so it is
	// treated as a credential; an unencoded one of any length is checked as is.
	longEncoded := "/m%3F" + strings.Repeat("%61", maxPathCredentialName+1) + "=v"
	got, changed := RedactPathCredentials(longEncoded)
	assert.True(t, changed)
	assert.True(t, strings.HasSuffix(got, "=***"))
	longPlain := "/m%3F" + strings.Repeat("a", 100) + "=v"
	got, changed = RedactPathCredentials(longPlain)
	assert.False(t, changed)
	assert.Equal(t, longPlain, got)
	longToken := "/m%3F" + strings.Repeat("a", 100) + "_token=v"
	got, _ = RedactPathCredentials(longToken)
	assert.True(t, strings.HasSuffix(got, "_token=***"))
}

func TestPathUnit(t *testing.T) {
	cases := []struct {
		in    string
		c     byte
		width int
	}{
		{"a", 'a', 1},
		{"%3F", '?', 3},
		{"%3f", '?', 3},
		{"%253F", '?', 5},
		{"%25253d", '=', 7},
		{"%25", '%', 3},
		{"%25zz", '%', 3},
		{"%zz", '%', 1},
		{"%3", '%', 1},
		{"%", '%', 1},
	}
	for _, tc := range cases {
		c, w := pathUnit(tc.in, 0)
		assert.Equal(t, tc.c, c, tc.in)
		assert.Equal(t, tc.width, w, tc.in)
	}
}
