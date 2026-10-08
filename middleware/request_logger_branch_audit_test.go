package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Branch audit of the request-log middleware on the relay path: it must never
// block or alter what the client and the downstream handler see.

func rlAuditWithBodyKB(t *testing.T, kb int) {
	t.Helper()
	prev := common.RequestLogMaxBodyKB
	common.RequestLogMaxBodyKB = kb
	t.Cleanup(func() { common.RequestLogMaxBodyKB = prev })
}

// Queue full: the request still completes promptly with an intact response,
// and the log entry is dropped (counted), never blocking the handler chain.
func TestRLAudit_FullQueueDoesNotBlockRequest(t *testing.T) {
	enableRequestLog(t, "")
	handle := withFullQueue(t, 1)

	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/r", strings.NewReader(`{"x":1}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		done <- rec
	}()
	select {
	case rec := <-done:
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "pong", rec.Body.String())
	case <-time.After(2 * time.Second):
		t.Fatal("request blocked on a full request-log queue")
	}
	assert.EqualValues(t, 1, requestLogDropped.Load())
	assert.EqualValues(t, 0, handle.pending.Load(), "a dropped enqueue must not leave pending accounting behind")
}

// Request body above the capture cap: the log keeps only the cap plus a
// truncation mark, the recorded size is the real size, and the downstream
// handler still reads the complete body.
func TestRLAudit_LargeRequestBodyTruncatedInLogButRelayReadsAll(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	rlAuditWithBodyKB(t, 1)

	payload := `{"p":"` + strings.Repeat("a", 5000) + `"}`
	var downstream []byte
	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) {
		downstream, _ = io.ReadAll(c.Request.Body)
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/r", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, payload, string(downstream), "relay must still see the full body")
	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	assert.EqualValues(t, len(payload), logs[0].RequestBodySize)
	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	assert.Equal(t, payload[:1024]+"\n...[truncated]", detail.RequestBody)
}

// Request body exactly at the cap is not marked truncated; one byte over is.
func TestRLAudit_RequestBodyExactlyAtCap(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	rlAuditWithBodyKB(t, 1)

	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, n := range []int{1024, 1025} {
		body := strings.Repeat("b", n)
		req := httptest.NewRequest(http.MethodPost, "/r?n="+strconv.Itoa(n), strings.NewReader(body))
		req.Header.Set("Content-Type", "text/plain")
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	logs := recordedRequestLogs(t)
	require.Len(t, logs, 2)
	for _, l := range logs {
		detail, err := model.GetRequestLogById(l.Id)
		require.NoError(t, err)
		if l.RequestBodySize == 1024 {
			assert.Equal(t, strings.Repeat("b", 1024), detail.RequestBody)
		} else {
			assert.Equal(t, strings.Repeat("b", 1024)+"\n...[truncated]", detail.RequestBody)
		}
	}
}

// Streaming: every chunk reaches the client and is flushed through the
// wrapper; the capture stops at the cap while the size keeps counting.
func TestRLAudit_StreamingResponsePassesThroughAndCapsCapture(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	rlAuditWithBodyKB(t, 1)

	chunk := "data: " + strings.Repeat("x", 294) + "\n\n" // 302 bytes
	const chunks = 10
	r := gin.New()
	r.GET("/s", RequestResponseLogger(), func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Set("is_stream", true)
		for i := 0; i < chunks; i++ {
			_, _ = c.Writer.WriteString(chunk)
			c.Writer.Flush()
		}
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s", nil))

	assert.True(t, rec.Flushed, "Flush must pass through the capture wrapper")
	assert.Equal(t, strings.Repeat(chunk, chunks), rec.Body.String())
	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	assert.True(t, logs[0].IsStream)
	assert.EqualValues(t, len(chunk)*chunks, logs[0].ResponseBodySize)
	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat(chunk, chunks)[:1024]+"\n...[truncated]", detail.ResponseBody)
}

// Oversized request headers are capped at the same limit as bodies, so a huge
// Cookie cannot inflate a single entry without bound.
func TestRLAudit_OversizedHeadersCapped(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")
	rlAuditWithBodyKB(t, 1)

	r := gin.New()
	r.GET("/r", RequestResponseLogger(), func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/r", nil)
	req.Header.Set("Cookie", strings.Repeat("c", 10_000))
	r.ServeHTTP(httptest.NewRecorder(), req)

	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	assert.Equal(t, 1024+len("\n...[truncated]"), len(detail.RequestHeaders))
	assert.True(t, strings.HasSuffix(detail.RequestHeaders, "\n...[truncated]"))
}

// Username resolved in the worker (no username in context, only user id):
// an unknown user id resolves to "" and is dropped when a filter is set,
// instead of being recorded under an empty name.
func TestRLAudit_WorkerSideUsernameFilterDropsUnresolved(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "alice")

	r := gin.New()
	r.GET("/r", RequestResponseLogger(), func(c *gin.Context) {
		c.Set("id", 987654321) // no such user; GetUserCache fails -> ""
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/r", nil))
	assert.Empty(t, recordedRequestLogs(t))
}

// Writer shutdown with entries still queued: Stop drains through the workers
// so nothing is left pending, and a later enqueue is a silent no-op.
func TestRLAudit_StopAfterEnqueueLeavesNoPending(t *testing.T) {
	requestLogTestEnv(t)
	for i := 0; i < 50; i++ {
		enqueueRequestLog(requestLogTask{entry: &model.RequestLog{Username: "u", RequestId: "q" + strconv.Itoa(i), RequestBody: string(bytes.Repeat([]byte("z"), 100))}})
	}
	handle := requestLogQueue.Load()
	require.NotNil(t, handle)
	StopRequestLogWriters()
	assert.EqualValues(t, 0, handle.pending.Load())
	_, total, _ := model.GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 1)
	assert.EqualValues(t, 50, total, "workers finish queued entries before exiting")

	enqueueRequestLog(requestLogTask{entry: &model.RequestLog{Username: "u"}})
	assert.Zero(t, DrainRequestLogQueue(10*time.Millisecond))
}
