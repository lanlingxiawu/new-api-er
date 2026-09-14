package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamDiagnosticTimeoutCapture 验证超时拦截的业务正文不采集，临时放行的终止错误仍采集；t 为测试上下文。
func TestStreamDiagnosticTimeoutCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, control := newRelayTimeoutControl(context.Background(), 0, 0, "")
	defer control.Close()
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	writer := &relayTimeoutResponseWriter{ResponseWriter: c.Writer}
	writer.state.Store(&relayTimeoutWriterState{control: control, isStream: true})
	c.Writer = writer
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	service.BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	_, err := c.Writer.WriteString("sent")
	require.NoError(t, err)
	control.expireTotal()
	_, err = c.Writer.WriteString("not-sent")
	require.Error(t, err)
	control.WriteTerminalError(func() {
		_, writeErr := info.StreamWriter.ResponseWriter.Write([]byte("terminal-error"))
		require.NoError(t, writeErr)
	})
	require.Equal(t, "sentterminal-error", rec.Body.String())
	require.Equal(t, base64.StdEncoding.EncodeToString(rec.Body.Bytes()), *info.StreamDiagnostic.DownstreamBody())
}

// privateStreamFlushRecorder 模拟底层刷新失败，验证日志包装不会吞掉错误。
type privateStreamFlushRecorder struct{ *httptest.ResponseRecorder }

// FlushError 无参数，固定返回刷新错误夹具，验证多层响应包装仍能取得底层错误。
func (w privateStreamFlushRecorder) FlushError() error { return errors.New("flush fixture") }

// TestUnifiedStreamLoggerWriter 验证私有流不缓存下游副本，FlushError 穿透日志与 Gin 包装。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamLoggerWriter(t *testing.T) {
	c, _ := gin.CreateTestContext(privateStreamFlushRecorder{httptest.NewRecorder()})
	c.Set(common.StreamPrivateContextKey, true)
	logWriter := &responseBodyWriter{ResponseWriter: c.Writer, body: &bytes.Buffer{}, limit: 1024, ctx: c}
	s := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
	capture := relaycommon.NewStreamResponseCapture(1)
	writer := relaycommon.NewStreamWriter(relaycommon.NewDownstreamCaptureWriter(logWriter, capture), s, nil)
	writer.Header().Set("Content-Type", "text/event-stream")
	_, err := writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	require.NoError(t, err)
	require.Zero(t, logWriter.body.Len())
	require.Error(t, writer.FlushError())
	require.Error(t, s.Snapshot().ClientErr)
	require.False(t, s.Snapshot().Effective)
	require.NotEmpty(t, *capture.DownstreamBody(), "Root 诊断与请求日志缓冲完全独立")
}

// TestUnifiedStreamRequestLogIsolation 验证流式请求不投递请求日志，非流式保持原有投递，不启动数据库工作者。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamRequestLogIsolation(t *testing.T) {
	oldQueue := requestLogQueue.Load()
	oldEnabled, oldName := common.RequestLogEnabled, common.RequestLogUsername
	defer func() {
		requestLogQueue.Store(oldQueue)
		common.RequestLogEnabled, common.RequestLogUsername = oldEnabled, oldName
	}()
	common.RequestLogEnabled = true
	common.RequestLogUsername = ""
	queue := &requestLogQueueHandle{ch: make(chan requestLogTask, 2), done: make(chan struct{})}
	requestLogQueue.Store(queue)
	r := gin.New()
	r.Use(RequestResponseLogger())
	r.POST("/:mode", func(c *gin.Context) {
		if c.Param("mode") == "stream" {
			c.Set(common.StreamPrivateContextKey, true)
		}
		c.String(200, "response")
	})
	for _, mode := range []string{"stream", "normal"} {
		req := httptest.NewRequest("POST", "/"+mode, strings.NewReader(`{"private":"request"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(httptest.NewRecorder(), req)
		if mode == "stream" {
			require.Zero(t, len(queue.ch))
		} else {
			require.Equal(t, 1, len(queue.ch))
		}
	}
}
