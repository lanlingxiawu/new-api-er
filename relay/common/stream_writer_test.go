package common

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamWriterDeliveryAndStop 验证写出前不计内容、刷新后计入、异常不补成功尾帧。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamWriterDeliveryAndStop(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	s := NewStreamSession(types.RelayFormatOpenAI)
	w := NewStreamWriter(c.Writer, s, nil)
	w.Header().Set("Content-Type", "text/event-stream")
	_, err := w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
	require.NoError(t, err)
	require.False(t, s.Snapshot().Effective)
	require.NoError(t, w.FlushError())
	require.True(t, s.Snapshot().Effective)
	s.EndRead(io.EOF)
	_, err = w.Write([]byte("data: [DONE]\n\n"))
	require.NoError(t, err)
	require.NoError(t, w.FlushError())
	require.NotContains(t, rec.Body.String(), "[DONE]")
}

// streamShortWriter 注入短写或写入 panic，确认没有将半个事件记成有效交付。
type streamShortWriter struct {
	gin.ResponseWriter
	panicWrite bool
}

// Write 对输入 p 注入短写或 panic，避免依赖真实断网时机。
func (w streamShortWriter) Write(p []byte) (int, error) {
	if w.panicWrite {
		panic("write fixture")
	}
	return len(p) - 1, nil
}

// TestStreamWriterShortWrite 覆盖短写与 panic，均终止上游且不保留有效交付标记。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamWriterShortWrite(t *testing.T) {
	for _, panicWrite := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		s := NewStreamSession(types.RelayFormatOpenAI)
		w := NewStreamWriter(streamShortWriter{c.Writer, panicWrite}, s, nil)
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		require.Error(t, err)
		require.False(t, s.Snapshot().Effective)
		require.Error(t, s.Snapshot().ClientErr)
	}
}

// streamFlushFailure 是只覆盖 FlushError 的测试写入器，不模拟协议判断。
type streamFlushFailure struct{ gin.ResponseWriter }

// FlushError 固定返回刷新故障，验证 Write 成功不等于有效交付。
func (w streamFlushFailure) FlushError() error { return errors.New("flush failed") }

// TestStreamWriterFlushFailure 刷新失败时正文不算有效交付，即使 Write 已成功返回。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamWriterFlushFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	s := NewStreamSession(types.RelayFormatOpenAI)
	w := NewStreamWriter(streamFlushFailure{c.Writer}, s, nil)
	w.Header().Set("Content-Type", "text/event-stream")
	_, err := w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
	require.NoError(t, err)
	require.Error(t, w.FlushError())
	require.False(t, s.Snapshot().Effective)
	require.Error(t, s.Snapshot().ClientErr)
	require.Implements(t, (*http.ResponseWriter)(nil), w)
}

// TestStreamWriterCandidateEndFrames 验证 HTTP 分批观察及分片写出时，候选结束不等待全流完成，也不丢同帧正文。
// 参数 t：测试上下文；上游仅使用内存 SSE 夹具，估算器按字节计数以独立验证交付统计。
func TestStreamWriterCandidateEndFrames(t *testing.T) {
	for _, tc := range []struct {
		name      string   // 协议及结束帧形态。
		frames    []string // 依次读取/写出的原始 JSON，每项模拟独立 SSE 帧。
		text      string   // 最终成功交付且应进入估算的正文/工具内容。
		effective bool     // 单独结束帧没有正文时仍不算有效交付。
	}{
		{"text", []string{
			`{"choices":[{"index":0,"delta":{"content":"A"}},{"index":1,"delta":{"content":"B"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"TAIL"},"finish_reason":"stop"}]}`,
			`{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`,
		}, "ABTAIL", true},
		{"mixed choices and indexes", []string{
			`{"choices":[{"index":7,"delta":{}},{"index":2,"delta":{}}]}`,
			`{"choices":[{"index":7,"delta":{"content":"TAIL"},"finish_reason":"length"},{"index":2,"delta":{"content":"MORE"}}],"extension":"preserve"}`,
			`{"choices":[{"index":2,"delta":{"content":"LAST"},"finish_reason":"stop"}]}`,
		}, "TAILMORELAST", true},
		{"empty delta", []string{
			`{"choices":[{"index":0,"delta":{}},{"index":1,"delta":{}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`,
		}, "", false},
		{"tool arguments", []string{
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"{\"x\":"}}]}},{"index":1,"delta":{}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`,
		}, `lookup{"x":1}`, true},
		{"gemini mixed candidates", []string{
			`{"candidates":[{"index":0,"content":{"parts":[]}},{"index":1,"content":{"parts":[]}}]}`,
			`{"candidates":[{"index":0,"content":{"parts":[{"text":"TAIL"}]},"finishReason":"STOP"},{"index":1,"content":{"parts":[{"text":"MORE"}]}}]}`,
			`{"candidates":[{"index":1,"content":{"parts":[{"text":"LAST"}]},"finishReason":"STOP"}]}`,
		}, "TAILMORELAST", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fragmented := range []bool{false, true} {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				s := NewStreamSession(types.RelayFormatOpenAI)
				s.ResponseGate = &StreamResponseGate{}
				w := NewStreamWriter(c.Writer, s, func(text string, _ int) int { return len(text) })
				w.Header().Set("Content-Type", "text/event-stream")
				body := "data: " + strings.Join(tc.frames, "\n\ndata: ") + "\n\n"
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				s.ObserveTransport(resp, nil)
				s.ObserveHTTP(resp)
				defer resp.Body.Close()
				var expected strings.Builder
				for i, data := range tc.frames {
					frame := []byte("data: " + data + "\n\n")
					raw := make([]byte, len(frame))
					_, err := io.ReadFull(resp.Body, raw)
					require.NoError(t, err)
					if i < len(tc.frames)-1 {
						require.False(t, s.Snapshot().Complete, "候选结束帧写出前上游整流仍未完成")
					}
					if fragmented {
						_, err = w.Write(raw[:len(raw)-1])
						require.NoError(t, err)
						require.Equal(t, expected.String(), rec.Body.String(), "帧分隔符完整前不写出")
						_, err = w.Write(raw[len(raw)-1:])
					} else {
						_, err = w.Write(raw)
					}
					require.NoError(t, err)
					require.NoError(t, w.FlushError())
					expected.Write(frame)
					require.Equal(t, expected.String(), rec.Body.String(), "每个候选结束帧应立即原样交付")
				}
				require.True(t, s.Snapshot().Complete)
				require.Equal(t, tc.effective, s.Snapshot().Effective)
				require.Equal(t, len(tc.text), s.Snapshot().EstimatedOutput)
				require.Empty(t, s.Snapshot().Reason)
			}
		})
	}
}

// TestStreamWriterSuccessEndGuards 验证候选结束放行不会放松整流终止校验，已有异常后仍过滤假成功。
// 参数 t：测试上下文，覆盖正常进行、正常完成及读取/协议/本地错误三类边界。
func TestStreamWriterSuccessEndGuards(t *testing.T) {
	frames := []struct {
		name      string // 下游帧类型。
		frame     string // 包含原始分隔符的 SSE。
		candidate bool   // 是否只表示候选完成。
		error     bool   // 原始错误始终交给下游，不被成功过滤器拦截。
	}{
		{"choice", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"tail\"},\"finish_reason\":\"stop\"}]}\n\n", true, false},
		{"candidate", "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n", true, false},
		{"done", "data: [DONE]\n\n", false, false},
		{"claude stop", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", false, false},
		{"claude reason", "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n", false, false},
		{"response completed", "data: {\"type\":\"response.completed\"}\n\n", false, false},
		{"response before choice", "data: {\"type\":\"response.completed\",\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n", false, false},
		{"error before choice", "event: error\ndata: {\"error\":{\"message\":\"original\"},\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n", false, true},
	}
	for _, state := range []string{"ongoing", "complete", "upstream_incomplete", "upstream_read_error", "upstream_json_error", "response_conversion_error"} {
		for _, tc := range frames {
			t.Run(state+"/"+tc.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				s := NewStreamSession(types.RelayFormatOpenAI)
				if state == "complete" {
					s.Complete()
				} else if state != "ongoing" {
					s.Fail(StreamEndReason(state), errors.New("fixture failure"))
				}
				before := s.Snapshot()
				w := NewStreamWriter(c.Writer, s, nil)
				w.Header().Set("Content-Type", "text/event-stream")
				n, err := w.Write([]byte(tc.frame))
				require.NoError(t, err)
				require.Equal(t, len(tc.frame), n)
				require.NoError(t, w.FlushError())
				if tc.error || state == "complete" || state == "ongoing" && tc.candidate {
					require.Equal(t, tc.frame, rec.Body.String())
				} else {
					require.Empty(t, rec.Body.String())
				}
				require.Equal(t, before.Complete, s.Snapshot().Complete, "下游终止标记不修改上游完成状态")
				require.Equal(t, before.Reason, s.Snapshot().Reason, "写出过滤不清除终止原因")
			})
		}
	}
}

// TestStreamWriterCandidateThenFailure 验证已交付候选不会因其他候选随后失败而丢失，异常后的成功终止仍被过滤。
// 参数 t：测试上下文；仅观察当前公共会话，不执行资金操作。
func TestStreamWriterCandidateThenFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	s := NewStreamSession(types.RelayFormatOpenAI)
	w := NewStreamWriter(c.Writer, s, func(text string, _ int) int { return len(text) })
	w.Header().Set("Content-Type", "text/event-stream")
	require.NoError(t, s.ObserveEvent("", []byte(`{"choices":[{"index":0,"delta":{}},{"index":1,"delta":{}}]}`)))
	end := `{"choices":[{"index":0,"delta":{"content":"tail"},"finish_reason":"stop"}]}`
	require.NoError(t, s.ObserveEvent("", []byte(end)))
	_, err := w.Write([]byte("data: " + end + "\n\n"))
	require.NoError(t, err)
	require.NoError(t, w.FlushError())
	require.True(t, s.Snapshot().Effective)
	require.Equal(t, 4, s.Snapshot().EstimatedOutput)
	s.EndRead(io.EOF)
	_, err = w.Write([]byte("data: [DONE]\n\n"))
	require.NoError(t, err)
	require.NoError(t, w.FlushError())
	require.Equal(t, "data: "+end+"\n\n", rec.Body.String())
	require.Equal(t, StreamEndReason("upstream_incomplete"), s.Snapshot().Reason)
}

// TestStreamWriterInactiveEndPassthrough 验证旧流程及成功响应门控尚未开放时不执行新结束帧过滤。
// 参数 t：测试上下文，分别模拟 nil 会话、专用处理器接管、等待成功响应三种状态。
func TestStreamWriterInactiveEndPassthrough(t *testing.T) {
	for _, state := range []string{"nil", "disabled", "pending response"} {
		t.Run(state, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			var s *StreamSession
			if state != "nil" {
				s = NewStreamSession(types.RelayFormatOpenAI)
				if state == "disabled" {
					s.Disable()
				} else {
					s.ResponseGate = &StreamResponseGate{}
				}
			}
			w := NewStreamWriter(c.Writer, s, nil)
			w.Header().Set("Content-Type", "text/event-stream")
			frame := "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			_, err := w.Write([]byte(frame))
			require.NoError(t, err)
			require.Equal(t, frame, rec.Body.String())
		})
	}
}
