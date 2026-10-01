package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for request-log defects found by the branch audit
// (docs/design/branch-audit-vs-main.md M11, M12, M13). The logger sits in
// front of TokenAuth on every relay route, so none of its failures may change
// what the relay or the client sees.

// M11: an over-limit body must still reach the relay as ErrRequestBodyTooLarge
// (mapped to 413), not as "http: invalid Read on closed Body". The logger used
// to call common.GetBodyStorage itself; when that failed the body was already
// drained and closed and the relay got the closed-body error instead.
func TestRLAuditRegression_TooLargeBodyErrorMaskedForRelay(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	prevMB := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMB })

	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) {
		// Stand-in for Distribute / GetAndValidateRequest.
		_, err := common.GetBodyStorage(c)
		if common.IsRequestBodyTooLargeError(err) {
			c.String(http.StatusRequestEntityTooLarge, "too large")
			return
		}
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Status(http.StatusOK)
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := strings.Repeat("a", 2<<20)
	resp, err := http.Post(srv.URL+"/r", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	msg, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode,
		"relay must still see ErrRequestBodyTooLarge; got %d %q", resp.StatusCode, string(msg))

	// The rejected request is still logged: truncated prefix, declared size.
	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	assert.Equal(t, http.StatusRequestEntityTooLarge, logs[0].StatusCode)
	assert.EqualValues(t, len(body), logs[0].RequestBodySize)
	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	maxBytes := common.RequestLogMaxBodyKB * 1024
	assert.Equal(t, body[:maxBytes]+"\n...[truncated]", detail.RequestBody)
}

type rlAuditCountingBody struct {
	r      io.Reader
	read   atomic.Int64
	closed atomic.Bool
}

func (b *rlAuditCountingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read.Add(int64(n))
	return n, err
}

func (b *rlAuditCountingBody) Close() error {
	b.closed.Store(true)
	return nil
}

// M12: the logger runs before TokenAuth on /v1, /pg, /mj, /suno, /v1beta and
// video routes. It must read at most its capture budget (RequestLogMaxBodyKB+1
// byte) of a request that auth rejects without touching the body, yet still
// log that request with the truncated body and its declared size.
func TestRLAuditRegression_ReadsWholeBodyBeforeAuthRejects(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	prevKB := common.RequestLogMaxBodyKB
	common.RequestLogMaxBodyKB = 64
	t.Cleanup(func() { common.RequestLogMaxBodyKB = prevKB })

	r := gin.New()
	r.POST("/v1/chat/completions", RequestResponseLogger(), func(c *gin.Context) {
		// Stand-in for TokenAuth rejecting a request without reading the body.
		c.AbortWithStatus(http.StatusUnauthorized)
	})
	const size = 8 << 20
	body := &rlAuditCountingBody{r: bytes.NewReader(bytes.Repeat([]byte("x"), size))}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	captureBudget := int64(common.RequestLogMaxBodyKB*1024 + 1)
	assert.LessOrEqual(t, body.read.Load(), captureBudget,
		"logger read %d bytes of a rejected request; its capture budget is %d", body.read.Load(), captureBudget)

	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1, "requests rejected by auth must still be logged")
	assert.Equal(t, http.StatusUnauthorized, logs[0].StatusCode)
	assert.EqualValues(t, size, logs[0].RequestBodySize, "size falls back to the declared Content-Length")
	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("x", 64*1024)+"\n...[truncated]", detail.RequestBody)
}

// Without a declared length (chunked upload) an unread body can only be sized
// by what was read: the recorded size is a lower bound, never a full read.
func TestRLAudit_ChunkedRejectedBodySizeIsBytesRead(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	rlAuditWithBodyKB(t, 1)

	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	body := &rlAuditCountingBody{r: bytes.NewReader(bytes.Repeat([]byte("y"), 10_000))}
	req := httptest.NewRequest(http.MethodPost, "/r", body)
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	assert.EqualValues(t, 1025, body.read.Load())
	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	assert.EqualValues(t, 1025, logs[0].RequestBodySize)
}

// Downstream still sees the complete body (prefix replayed + rest streamed)
// over a real net/http connection, and the recorded size is the exact size
// once the body was fully read — also for chunked uploads.
func TestRLAudit_DownstreamSeesFullBodyThroughBoundedCapture(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	rlAuditWithBodyKB(t, 1)

	var downstream []byte
	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) {
		// Runs on the server goroutine: report failures via the status code.
		storage, err := common.GetBodyStorage(c)
		if err == nil {
			downstream, err = storage.Bytes()
		}
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusOK)
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	payload := `{"p":"` + strings.Repeat("z", 300_000) + `"}`
	for _, chunked := range []bool{false, true} {
		downstream = nil
		var reader io.Reader = strings.NewReader(payload)
		if chunked {
			reader = io.MultiReader(reader) // hides the length: Transport sends chunked
		}
		resp, err := http.Post(srv.URL+"/r", "application/json", reader)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, payload, string(downstream), "chunked=%v", chunked)
	}

	logs := recordedRequestLogs(t)
	require.Len(t, logs, 2)
	for _, l := range logs {
		assert.EqualValues(t, len(payload), l.RequestBodySize)
	}
}

// A client read error hit while the logger reads its prefix is handed to the
// relay unchanged (not swallowed, not turned into a closed-body error).
func TestRLAudit_ClientReadErrorReachesRelay(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	rlAuditWithBodyKB(t, 1)

	boom := errors.New("client connection reset")
	for name, prefix := range map[string]string{
		"error inside capture prefix": "abc",
		"error after capture prefix":  strings.Repeat("q", 4096),
	} {
		t.Run(name, func(t *testing.T) {
			var relayErr error
			r := gin.New()
			r.POST("/r", RequestResponseLogger(), func(c *gin.Context) {
				_, relayErr = common.GetBodyStorage(c)
				c.Status(http.StatusBadRequest)
			})
			req := httptest.NewRequest(http.MethodPost, "/r", nil)
			req.Body = io.NopCloser(io.MultiReader(strings.NewReader(prefix), iotest.ErrReader(boom)))
			req.ContentLength = -1
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(httptest.NewRecorder(), req)
			assert.ErrorIs(t, relayErr, boom)
		})
	}
}

// Closing the replacement body closes the original request body.
func TestRLAudit_TapCloseClosesOriginalBody(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := &rlAuditCountingBody{r: strings.NewReader(`{"a":1}`)}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", body)
	captureRequestBody(ctx, 1024)
	require.NoError(t, ctx.Request.Body.Close())
	assert.True(t, body.closed.Load())
}

// M13: the list endpoint (open to non-root admins) returns Url verbatim, and
// Gemini-style clients authenticate with ?key=<token>. The credential must be
// redacted at record time, so neither the list nor the stored body carries it.
func TestRLAuditRegression_ListUrlLeaksQueryStringApiKey(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")

	r := gin.New()
	r.POST("/v1beta/models/*action", RequestResponseLogger(), func(c *gin.Context) {
		c.Set("username", "alice")
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent?alt=sse&key=sk-live-secret-token", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	logs := recordedRequestLogs(t) // what controller.GetAllRequestLogs returns
	require.Len(t, logs, 1)
	assert.NotContains(t, logs[0].Url, "sk-live-secret-token",
		"list-level metadata (visible to non-root admins) must not carry the query-string API key")
	assert.Equal(t, "/v1beta/models/gemini-pro:generateContent?alt=sse&key=***", logs[0].Url,
		"non-credential params stay readable for troubleshooting")
	assert.Empty(t, logs[0].RequestHeaders, "the list projection carries no headers")

	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	assert.NotContains(t, detail.Url, "sk-live-secret-token")
}
