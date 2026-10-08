package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rule 10: the access log must not carry the ?key=<token> a Gemini-style client
// sends; the rest of the line (path, other params) stays readable.
func TestFormatAccessLog_RedactsQueryCredentials(t *testing.T) {
	line := formatAccessLog(gin.LogFormatterParams{
		Request:    httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:streamGenerateContent?alt=sse&key=sk-live-secret", nil),
		Method:     http.MethodPost,
		Path:       "/v1beta/models/gemini-pro:streamGenerateContent?alt=sse&key=sk-live-secret",
		StatusCode: http.StatusOK,
		Keys:       map[any]any{common.RequestIdKey: "req-1", RouteTagKey: "relay"},
	})
	assert.NotContains(t, line, "sk-live-secret")
	assert.Contains(t, line, "/v1beta/models/gemini-pro:streamGenerateContent?alt=sse&key=***")
	assert.Contains(t, line, "| relay | req-1 |")

	plain := formatAccessLog(gin.LogFormatterParams{
		Request: httptest.NewRequest(http.MethodGet, "/api/status?x=1", nil),
		Method:  http.MethodGet,
		Path:    "/api/status?x=1",
	})
	assert.Contains(t, plain, "/api/status?x=1")
	assert.Contains(t, plain, "| web |", "untagged routes default to web")
}

// End to end through gin: what reaches the log writer has no token.
func TestSetUpLogger_WritesRedactedPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	prev := gin.DefaultWriter
	gin.DefaultWriter = &buf
	t.Cleanup(func() { gin.DefaultWriter = prev })

	r := gin.New()
	SetUpLogger(r)
	r.GET("/v1beta/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1beta/models?key=sk-live-secret", nil))

	require.NotEmpty(t, buf.String())
	assert.NotContains(t, buf.String(), "sk-live-secret")
	assert.Contains(t, buf.String(), "/v1beta/models?key=***")
}

// A percent-encoded "?" in the path decodes into gin's param.Path, so the
// decoded "path?query" string has two "?" and splitting it at the first one
// would leave the real ?key= unredacted. The line is built from the parsed URL.
func TestSetUpLogger_EncodedQuestionMarkInPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	prev := gin.DefaultWriter
	gin.DefaultWriter = &buf
	t.Cleanup(func() { gin.DefaultWriter = prev })

	r := gin.New()
	SetUpLogger(r)
	r.NoRoute(func(c *gin.Context) {
		assert.Equal(t, "sk-secret", c.Query("key"), "the credential still reaches auth")
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost,
		"/v1beta/models/a%3Fb:generateContent?key=sk-secret&alt=sse", nil))

	require.NotEmpty(t, buf.String())
	assert.NotContains(t, buf.String(), "sk-secret")
	assert.Contains(t, buf.String(), "/v1beta/models/a%3Fb:generateContent?key=***&alt=sse")
}

// Other credential names from the shared list are redacted in the access log too.
func TestFormatAccessLog_SharedCredentialNames(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/r?session_token=s1&X-Amz-Signature=s2&sig=s3&passwd=s4&model=m", nil)
	line := formatAccessLog(gin.LogFormatterParams{Request: req, Method: http.MethodGet, Path: "/ignored"})
	for _, secret := range []string{"s1", "s2", "s3", "s4"} {
		assert.NotContains(t, line, "="+secret)
	}
	assert.Contains(t, line, "/r?session_token=***&X-Amz-Signature=***&sig=***&passwd=***&model=m")
}

// Without a request (never the case under gin) the query is dropped rather
// than re-parsed from the decoded path.
func TestFormatAccessLog_NoRequestDropsQuery(t *testing.T) {
	line := formatAccessLog(gin.LogFormatterParams{Method: http.MethodGet, Path: "/v1/models/a?b:x?key=sk-secret"})
	assert.NotContains(t, line, "sk-secret")
	assert.Contains(t, line, " /v1/models/a\n")
}

// A handler that rewrites the path (kling / jimeng adapters) does not change
// what the access log shows: the client's path, with the query still redacted.
func TestSetUpLogger_KeepsRequestedPathWhenRewritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	prev := gin.DefaultWriter
	gin.DefaultWriter = &buf
	t.Cleanup(func() { gin.DefaultWriter = prev })

	r := gin.New()
	SetUpLogger(r)
	r.POST("/kling/v1/videos/text2video", func(c *gin.Context) {
		c.Request.URL.Path = "/v1/video/generations"
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/kling/v1/videos/text2video?key=sk-secret&a=1", nil))

	assert.NotContains(t, buf.String(), "sk-secret")
	assert.Contains(t, buf.String(), " /kling/v1/videos/text2video?key=***&a=1\n")
}

func TestAccessLogRequestedPath(t *testing.T) {
	cases := []struct {
		name, ginPath, rawQuery, want string
		ok                            bool
	}{
		{"no query", "/v1/models", "", "/v1/models", true},
		{"query suffix", "/v1/models?key=sk&a=1", "key=sk&a=1", "/v1/models", true},
		{"decoded %3F in path", "/v1/a?b:gen?key=sk", "key=sk", "", false},
		{"query changed after gin captured it", "/p?key=sk&x=1", "x=1", "", false},
		{"query cleared after gin captured it", "/p?key=sk", "", "", false},
		{"current query is a suffix of the captured one", "/p?key=sk?x=1", "x=1", "", false},
		{"query longer than path", "/p", "key=sk-very-long", "", false},
		{"suffix without separator", "/pkey=sk", "key=sk", "", false},
	}
	for _, tc := range cases {
		got, ok := accessLogRequestedPath(tc.ginPath, tc.rawQuery)
		assert.Equal(t, tc.ok, ok, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
		assert.NotContains(t, got, "sk", tc.name)
	}
}

// The access-log line for an ordinary request adds no allocations for redaction.
func TestAccessLogURI_NoCredentialNoExtraAlloc(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models/m:generateContent?alt=sse", nil)
	param := gin.LogFormatterParams{Request: req, Path: "/v1beta/models/m:generateContent?alt=sse"}
	allocs := testing.AllocsPerRun(100, func() { _ = accessLogURI(param) })
	// RequestURI joins path and query (one allocation); redaction itself adds none.
	assert.LessOrEqual(t, allocs, 1.0)
}

// A client that percent-encodes ?key=<token> into the path (the token then is
// part of the path, not the query) must not get it written to the access log
// or the request log. %3F, %3f and double-encoded %253F are all covered.
func TestRequestLogs_RedactCredentialEncodedIntoPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	var buf bytes.Buffer
	prev := gin.DefaultWriter
	gin.DefaultWriter = &buf
	t.Cleanup(func() { gin.DefaultWriter = prev })

	r := gin.New()
	SetUpLogger(r)
	r.POST("/v1beta/models/*action", RequestResponseLogger(), func(c *gin.Context) {
		c.Set("username", "alice")
		c.Status(http.StatusOK)
	})
	cases := []struct{ target, want string }{
		{"/v1beta/models/gemini-2.0-flash%3Fkey=PATHSECRET1:generateContent?key=QUERYSECRET1",
			"/v1beta/models/gemini-2.0-flash%3Fkey=***?key=***"},
		{"/v1beta/models/gemini-2.0-flash%3fkey=PATHSECRET2:generateContent",
			"/v1beta/models/gemini-2.0-flash%3fkey=***"},
		{"/v1beta/models/gemini-2.0-flash%253Fkey%253DPATHSECRET3:generateContent?alt=sse",
			"/v1beta/models/gemini-2.0-flash%253Fkey%253D***?alt=sse"},
		{"/v1beta/models/gemini.key=PATHSECRET4:generateContent?alt=sse%26key=QUERYSECRET2",
			"/v1beta/models/gemini.key=***?alt=sse%26key=***"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	access := buf.String()
	logs := recordedRequestLogs(t)
	require.Len(t, logs, len(cases))
	urls := make([]string, 0, len(logs))
	for _, l := range logs {
		urls = append(urls, l.Url)
	}
	for _, tc := range cases {
		assert.Contains(t, access, " "+tc.want+"\n")
		assert.Contains(t, urls, tc.want)
	}
	for _, secret := range []string{"PATHSECRET1", "PATHSECRET2", "PATHSECRET3", "PATHSECRET4", "QUERYSECRET1", "QUERYSECRET2"} {
		assert.NotContains(t, access, secret)
		for _, u := range urls {
			assert.NotContains(t, u, secret)
		}
	}
}

// Redacting the path adds no allocations to an ordinary access-log line whose
// path contains escapes or "=".
func TestAccessLogURI_EscapedPathNoExtraAlloc(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models/m%3Fb=c:generateContent?alt=sse", nil)
	param := gin.LogFormatterParams{Request: req, Path: "/v1beta/models/m?b=c:generateContent?alt=sse"}
	assert.Equal(t, "/v1beta/models/m%3Fb=c:generateContent?alt=sse", accessLogURI(param))
	allocs := testing.AllocsPerRun(100, func() { _ = accessLogURI(param) })
	// EscapedPath un-escapes RawPath to validate it, RequestURI joins path and query.
	assert.LessOrEqual(t, allocs, 2.0)
}
