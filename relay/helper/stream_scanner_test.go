package helper

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
	if constant.StreamingTimeout == 0 {
		constant.StreamingTimeout = 30
	}
}

type relayTimeoutStateStub string

func (state relayTimeoutStateStub) RelayTimeoutKind() string { return string(state) }

type deadlineResponseWriter struct {
	header http.Header
}

func (writer *deadlineResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *deadlineResponseWriter) Write([]byte) (int, error) {
	return 0, context.DeadlineExceeded
}

func (writer *deadlineResponseWriter) WriteHeader(int) {}

func setupStreamTest(t *testing.T, body io.Reader) (*gin.Context, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{Body: io.NopCloser(body)}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	return c, resp, info
}

func buildSSEBody(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "data: {\"id\":%d,\"choices\":[{\"delta\":{\"content\":\"token_%d\"}}]}\n", i, i)
	}
	b.WriteString("data: [DONE]\n")
	return b.String()
}

// ---------- Scanner buffer sizing ----------

func TestGetScannerBufferSize(t *testing.T) {
	old := constant.StreamScannerMaxBufferMB
	t.Cleanup(func() { constant.StreamScannerMaxBufferMB = old })

	constant.StreamScannerMaxBufferMB = 0
	require.Equal(t, DefaultMaxScannerBufferSize, getScannerBufferSize())

	constant.StreamScannerMaxBufferMB = 4
	require.Equal(t, 4<<20, getScannerBufferSize())
}

func TestNewStreamScanner_AllowsLargeStreamLine(t *testing.T) {
	old := constant.StreamScannerMaxBufferMB
	constant.StreamScannerMaxBufferMB = 1
	t.Cleanup(func() { constant.StreamScannerMaxBufferMB = old })

	payload := strings.Repeat("x", 128<<10)
	scanner := NewStreamScanner(strings.NewReader("data: " + payload + "\n"))
	scanner.Split(bufio.ScanLines)
	require.True(t, scanner.Scan())
	assert.Equal(t, "data: "+payload, scanner.Text())
	require.NoError(t, scanner.Err())
}

func TestExtendWriteDeadline_NoPanicOnRecorder(t *testing.T) {
	c, _, _ := setupStreamTest(t, strings.NewReader(""))
	assert.NotPanics(t, func() { ExtendWriteDeadline(c) })
	assert.NotPanics(t, func() { ExtendWriteDeadline(nil) })
}

// ---------- Basic correctness ----------

func TestStreamScannerHandler_NilInputs(t *testing.T) {
	c, _, info := setupStreamTest(t, strings.NewReader(""))
	// nil resp and nil handler both return without side effects.
	StreamScannerHandler(c, nil, info, func(data string, sr *StreamResult) {})
	StreamScannerHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(""))}, info, nil)
}

func TestStreamScannerHandler_EmptyBody(t *testing.T) {
	c, resp, info := setupStreamTest(t, strings.NewReader(""))
	var called atomic.Bool
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) { called.Store(true) })
	assert.False(t, called.Load())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonEOF, info.StreamStatus.EndReason)
}

func TestStreamScannerHandler_ManyChunks(t *testing.T) {
	const numChunks = 1000
	c, resp, info := setupStreamTest(t, strings.NewReader(buildSSEBody(numChunks)))
	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) { count.Add(1) })
	assert.Equal(t, int64(numChunks), count.Load())
	assert.Equal(t, numChunks, info.ReceivedResponseCount)
}

func TestStreamScannerHandler_OrderPreserved(t *testing.T) {
	const numChunks = 300
	c, resp, info := setupStreamTest(t, strings.NewReader(buildSSEBody(numChunks)))
	var mu sync.Mutex
	received := make([]string, 0, numChunks)
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		mu.Lock()
		received = append(received, data)
		mu.Unlock()
	})
	require.Len(t, received, numChunks)
	for i := 0; i < numChunks; i++ {
		expected := fmt.Sprintf("{\"id\":%d,\"choices\":[{\"delta\":{\"content\":\"token_%d\"}}]}", i, i)
		assert.Equal(t, expected, received[i])
	}
}

func TestStreamScannerHandler_DoneStopsScanner(t *testing.T) {
	body := buildSSEBody(50) + "data: should_not_appear\n"
	c, resp, info := setupStreamTest(t, strings.NewReader(body))
	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) { count.Add(1) })
	assert.Equal(t, int64(50), count.Load())
}

func TestStreamScannerHandler_SkipsNonDataLines(t *testing.T) {
	var b strings.Builder
	b.WriteString(": comment line\n")
	b.WriteString("event: message\n")
	b.WriteString("id: 12345\n")
	b.WriteString("x\n")     // < 6 chars, skipped
	b.WriteString("data:\n") // becomes empty after trim, skipped
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "data: payload_%d\n", i)
	}
	b.WriteString("data: [DONE]\n")
	c, resp, info := setupStreamTest(t, strings.NewReader(b.String()))
	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) { count.Add(1) })
	assert.Equal(t, int64(100), count.Load())
}

func TestStreamScannerHandler_DataWithExtraSpaces(t *testing.T) {
	body := "data:   {\"trimmed\":true}  \ndata: [DONE]\n"
	c, resp, info := setupStreamTest(t, strings.NewReader(body))
	var got string
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) { got = data })
	assert.Equal(t, "{\"trimmed\":true}", got)
}

// ---------- StreamResult stop / done via handler ----------

func TestStreamScannerHandler_HandlerStop(t *testing.T) {
	const stopAt int64 = 50
	c, resp, info := setupStreamTest(t, strings.NewReader(buildSSEBody(200)))
	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		if count.Add(1) >= stopAt {
			sr.Stop(fmt.Errorf("fatal"))
		}
	})
	assert.Equal(t, stopAt, count.Load())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.HasErrors())
}

func TestStreamScannerHandler_HandlerDone(t *testing.T) {
	c, resp, info := setupStreamTest(t, strings.NewReader(buildSSEBody(20)))
	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		if count.Add(1) >= 5 {
			sr.Done()
		}
	})
	assert.Equal(t, int64(5), count.Load())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.False(t, info.StreamStatus.HasErrors())
}

func TestStreamScannerHandler_SoftErrors(t *testing.T) {
	c, resp, info := setupStreamTest(t, strings.NewReader(buildSSEBody(10)))
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		sr.Error(fmt.Errorf("soft"))
	})
	require.NotNil(t, info.StreamStatus)
	assert.True(t, info.StreamStatus.HasErrors())
	assert.Equal(t, 10, info.StreamStatus.TotalErrorCount())
}

// ---------- StreamStatus lifecycle ----------

func TestStreamScannerHandler_StreamStatus_InitializedIfNil(t *testing.T) {
	c, resp, info := setupStreamTest(t, strings.NewReader(buildSSEBody(1)))
	assert.Nil(t, info.StreamStatus)
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
	assert.NotNil(t, info.StreamStatus)
}

func TestStreamScannerHandler_StreamStatus_ReplacesPreInitialized(t *testing.T) {
	c, resp, info := setupStreamTest(t, strings.NewReader(buildSSEBody(5)))
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.RecordError("pre-existing")
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.Equal(t, 0, info.StreamStatus.TotalErrorCount())
}

func TestStreamScannerHandler_StreamStatus_EOFWithoutDone(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "data: {\"id\":%d}\n", i)
	}
	c, resp, info := setupStreamTest(t, strings.NewReader(b.String()))
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonEOF, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.IsNormalEnd())
}

func TestStreamScannerHandler_StreamStatus_Timeout(t *testing.T) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	t.Cleanup(func() { constant.StreamingTimeout = old })

	pr, pw := io.Pipe()
	go func() {
		fmt.Fprint(pw, "data: {\"id\":1}\n")
		time.Sleep(2 * time.Second)
		pw.Close()
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{Body: pr}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
	assert.False(t, info.StreamStatus.IsNormalEnd())
}

// ---------- Client disconnect ----------

func TestStreamScannerHandler_ClientCancelAbortsUpstreamAndReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	resp := &http.Response{Body: pr}
	info := &relaycommon.RelayInfo{DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{}}

	var count atomic.Int64
	firstHandled := make(chan struct{})
	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
			count.Add(1)
			_ = StringData(c, data)
			if data == "first" {
				close(firstHandled)
			}
		})
		close(done)
	}()

	_, err := fmt.Fprint(pw, "data: first\n")
	require.NoError(t, err)
	select {
	case <-firstHandled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first chunk")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after disconnect")
	}
	_, err = fmt.Fprint(pw, "data: second\n")
	require.ErrorIs(t, err, io.ErrClosedPipe)
	assert.Equal(t, int64(1), count.Load())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	body := rec.Body.String()
	assert.Contains(t, body, "first")
	assert.NotContains(t, body, "second")
}

func TestStreamScannerHandler_OwnedDeadlineUsesTimeoutEndReason(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, relayTimeoutStateStub("response_timeout"))
	resp := &http.Response{Body: pr}
	info := &relaycommon.RelayInfo{DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{}}

	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.Fail(t, "handler did not return after configured deadline")
	}
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
}

func TestStreamScannerHandler_PingWriteFailurePreservesOwnedTimeoutReason(t *testing.T) {
	setting := operation_setting.GetGeneralSetting()
	oldEnabled, oldSeconds := setting.PingIntervalEnabled, setting.PingIntervalSeconds
	setting.PingIntervalEnabled = true
	setting.PingIntervalSeconds = 1
	t.Cleanup(func() {
		setting.PingIntervalEnabled = oldEnabled
		setting.PingIntervalSeconds = oldSeconds
	})

	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	go func() {
		time.Sleep(1200 * time.Millisecond)
		_ = writer.Close()
	}()

	c, _ := gin.CreateTestContext(&deadlineResponseWriter{header: make(http.Header)})
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, relayTimeoutStateStub("response_timeout"))
	resp := &http.Response{Body: reader}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		require.Fail(t, "stream scanner did not stop")
	}
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
}

// ---------- Ping ----------

func TestStreamScannerHandler_PingSentDuringSlowUpstream(t *testing.T) {
	setting := operation_setting.GetGeneralSetting()
	oe, os := setting.PingIntervalEnabled, setting.PingIntervalSeconds
	setting.PingIntervalEnabled = true
	setting.PingIntervalSeconds = 1
	t.Cleanup(func() { setting.PingIntervalEnabled = oe; setting.PingIntervalSeconds = os })

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for i := 0; i < 4; i++ {
			fmt.Fprintf(pw, "data: chunk_%d\n", i)
			time.Sleep(400 * time.Millisecond)
		}
		fmt.Fprint(pw, "data: [DONE]\n")
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{Body: pr}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	var count atomic.Int64
	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) { count.Add(1) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
	assert.Equal(t, int64(4), count.Load())
	assert.GreaterOrEqual(t, strings.Count(rec.Body.String(), ": PING"), 1)
}

func TestStreamScannerHandler_PingDisabledByRelayInfo(t *testing.T) {
	setting := operation_setting.GetGeneralSetting()
	oe, os := setting.PingIntervalEnabled, setting.PingIntervalSeconds
	setting.PingIntervalEnabled = true
	setting.PingIntervalSeconds = 1
	t.Cleanup(func() { setting.PingIntervalEnabled = oe; setting.PingIntervalSeconds = os })

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(buildSSEBody(5)))}
	info := &relaycommon.RelayInfo{DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{}}

	var count atomic.Int64
	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) { count.Add(1) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
	assert.Equal(t, int64(5), count.Load())
	assert.Equal(t, 0, strings.Count(rec.Body.String(), ": PING"))
}
