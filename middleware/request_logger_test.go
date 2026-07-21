package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// isTextualContentType — pure logic
// ---------------------------------------------------------------------------

func TestIsTextualContentType(t *testing.T) {
	require.True(t, isTextualContentType(""))
	require.True(t, isTextualContentType("application/json"))
	require.True(t, isTextualContentType("text/plain; charset=utf-8"))
	require.True(t, isTextualContentType("text/event-stream"))
	require.True(t, isTextualContentType("application/x-www-form-urlencoded"))
	require.True(t, isTextualContentType("application/xml"))
	require.False(t, isTextualContentType("application/octet-stream"))
	require.False(t, isTextualContentType("image/png"))
}

// ---------------------------------------------------------------------------
// appendTruncatedMark / truncateString / readLimited / headersToString
// ---------------------------------------------------------------------------

func TestAppendTruncatedMark(t *testing.T) {
	require.Equal(t, "body", appendTruncatedMark("body", false))
	require.Equal(t, "body\n...[truncated]", appendTruncatedMark("body", true))
}

func TestTruncateString(t *testing.T) {
	require.Equal(t, "abc", truncateString("abc", 5))
	require.Equal(t, "ab", truncateString("abcd", 2))
	require.Equal(t, "abcd", truncateString("abcd", 0)) // max<=0 -> unchanged
}

func TestReadLimited(t *testing.T) {
	data, truncated := readLimited(strings.NewReader("abcdef"), 3)
	require.Equal(t, "abc", string(data))
	require.True(t, truncated)

	data, truncated = readLimited(strings.NewReader("ab"), 3)
	require.Equal(t, "ab", string(data))
	require.False(t, truncated)

	data, truncated = readLimited(strings.NewReader("abc"), 0)
	require.Nil(t, data)
	require.False(t, truncated)
}

func TestHeadersToString(t *testing.T) {
	require.Equal(t, "", headersToString(http.Header{}))
	h := http.Header{"X-Test": []string{"v"}}
	require.Contains(t, headersToString(h), "X-Test")
}

// ---------------------------------------------------------------------------
// responseBodyWriter — capture buffer semantics
// ---------------------------------------------------------------------------

func TestResponseBodyWriter_CaptureAndTruncate(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	w := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 4}
	_, _ = w.Write([]byte("abcdef"))
	require.Equal(t, "abcd", w.body.String())
	require.True(t, w.truncated)
	require.EqualValues(t, 6, w.totalSize)
	require.Equal(t, "abcdef", rec.Body.String())
}

func TestResponseBodyWriter_WriteStringUnderLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	w := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 100}
	_, _ = w.WriteString("hello")
	require.Equal(t, "hello", w.body.String())
	require.False(t, w.truncated)
}

func TestResponseBodyWriter_ZeroLimitNoCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	w := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 0}
	_, _ = w.Write([]byte("data"))
	require.Equal(t, "", w.body.String())
	require.EqualValues(t, 4, w.totalSize) // still counts bytes
}

// ---------------------------------------------------------------------------
// matchRequestLogUsername
// ---------------------------------------------------------------------------

func TestMatchRequestLogUsername(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", " Alice ")
	prev := common.RequestLogUsername
	t.Cleanup(func() { common.RequestLogUsername = prev })

	common.RequestLogUsername = "alice" // case-insensitive match
	username, matched := matchRequestLogUsername(ctx)
	require.True(t, matched)
	require.Equal(t, "Alice", username)

	common.RequestLogUsername = "bob"
	_, matched = matchRequestLogUsername(ctx)
	require.False(t, matched)

	common.RequestLogUsername = "" // empty filter -> match all
	_, matched = matchRequestLogUsername(ctx)
	require.True(t, matched)
}

// ---------------------------------------------------------------------------
// captureRequestBody
// ---------------------------------------------------------------------------

func TestCaptureRequestBody_TextualResets(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	t.Cleanup(func() { common.CleanupBodyStorage(ctx) })

	content, truncated, size := captureRequestBody(ctx, 1024)
	require.Equal(t, `{"a":1}`, content)
	require.False(t, truncated)
	require.EqualValues(t, 7, size)

	// Body remains readable for downstream relay.
	data, _ := io.ReadAll(ctx.Request.Body)
	require.Equal(t, `{"a":1}`, string(data))
}

func TestCaptureRequestBody_NonTextualOmitted(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := "binary"
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/octet-stream")
	t.Cleanup(func() { common.CleanupBodyStorage(ctx) })

	content, truncated, size := captureRequestBody(ctx, 4)
	require.Equal(t, "[non-textual request body omitted]", content)
	require.False(t, truncated)
	require.EqualValues(t, len(body), size)
}

func TestCaptureRequestBody_NilBody(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ctx.Request.Body = nil
	content, truncated, size := captureRequestBody(ctx, 1024)
	require.Equal(t, "", content)
	require.False(t, truncated)
	require.Zero(t, size)
}

// ---------------------------------------------------------------------------
// RequestResponseLogger — middleware flow
// ---------------------------------------------------------------------------

func TestRequestResponseLogger_DisabledPassthrough(t *testing.T) {
	prev := common.RequestLogEnabled
	common.RequestLogEnabled = false
	t.Cleanup(func() { common.RequestLogEnabled = prev })

	ran := false
	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) { ran = true; c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/r", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.True(t, ran)
}

func TestRequestResponseLogger_EnabledCapturesTerminalRuns(t *testing.T) {
	requireDB(t)
	prev := common.RequestLogEnabled
	prevUser := common.RequestLogUsername
	common.RequestLogEnabled = true
	common.RequestLogUsername = "nobody-matches-this" // filter out -> no DB write
	t.Cleanup(func() {
		common.RequestLogEnabled = prev
		common.RequestLogUsername = prevUser
	})

	ran := false
	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) {
		ran = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/r", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.True(t, ran)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "ok")
}

// ---------------------------------------------------------------------------
// captureResponseBody — non-textual omitted
// ---------------------------------------------------------------------------

func TestCaptureResponseBody_NonTextual(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	rbw := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 1024}
	rbw.Header().Set("Content-Type", "image/png")
	_, _ = rbw.Write([]byte{0x89, 0x50})
	require.Equal(t, "[non-textual response body omitted]", captureResponseBody(ctx, rbw))
}

func TestCaptureResponseBody_Textual(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	rbw := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 1024}
	rbw.Header().Set("Content-Type", "application/json")
	_, _ = rbw.Write([]byte(`{"ok":true}`))
	require.Equal(t, `{"ok":true}`, captureResponseBody(ctx, rbw))
}

// getRequestLogUsername: context username wins.
func TestGetRequestLogUsername_FromContext(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "  ctxuser ")
	require.Equal(t, "ctxuser", getRequestLogUsername(ctx))
}

func TestGetRequestLogUsername_EmptyWhenNoIdentity(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Equal(t, "", getRequestLogUsername(ctx))
}
