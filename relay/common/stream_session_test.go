package common

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamSuccessEventScopes 验证候选/响应结束分类和既有布尔接口一致，未知事件和错误不冒充成功结束。
// 参数 t：测试上下文；夹具覆盖空值、事件名回退、全流标记优先级及原始错误优先级。
func TestStreamSuccessEventScopes(t *testing.T) {
	for _, tc := range []struct {
		name, event, data string             // 用例名、SSE 事件名及 JSON 载荷。
		want              streamSuccessScope // 期望结束粒度。
	}{
		{"empty", "", "", streamSuccessNone},
		{"ping", "ping", `{"type":"ping"}`, streamSuccessNone},
		{"null finish", "", `{"choices":[{"finish_reason":null}]}`, streamSuccessNone},
		{"empty finish", "", `{"choices":[{"finish_reason":""}]}`, streamSuccessNone},
		{"choice", "", `{"choices":[{"finish_reason":"tool_calls"}]}`, streamSuccessCandidate},
		{"candidate", "", `{"candidates":[{"finishReason":"STOP"}]}`, streamSuccessCandidate},
		{"candidate ongoing", "", `{"candidates":[{"content":{"parts":[]}}]}`, streamSuccessNone},
		{"unknown", "extension", `{"extension":true}`, streamSuccessNone},
		{"done", "", " [DONE] ", streamSuccessResponse},
		{"message stop by event", "message_stop", `{}`, streamSuccessResponse},
		{"message delta", "", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, streamSuccessClaudeDelta},
		{"message delta usage", "", `{"type":"message_delta","usage":{"output_tokens":1}}`, streamSuccessNone},
		{"response done", "", `{"type":"response.done","response":{"status":"completed"}}`, streamSuccessResponse},
		{"response completed", "", `{"type":"response.completed","choices":[{"finish_reason":"stop"}]}`, streamSuccessResponse},
		{"speech done", "", `{"type":"speech.audio.done"}`, streamSuccessResponse},
		{"transcript done", "", `{"type":"transcript.text.done"}`, streamSuccessResponse},
		{"error by event", "error", `{"choices":[{"finish_reason":"stop"}]}`, streamSuccessNone},
		{"error by payload", "", `{"error":{"message":"original"},"choices":[{"finish_reason":"stop"}]}`, streamSuccessNone},
		{"failed response", "", `{"type":"response.done","response":{"status":"failed"}}`, streamSuccessNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyStreamSuccessEvent(tc.event, []byte(tc.data)))
			require.Equal(t, tc.want != streamSuccessNone, IsStreamSuccessEvent(tc.event, []byte(tc.data)))
		})
	}
}

// TestStreamSessionProtocols 覆盖各协议结束、截断、解析失败与显式零；不连接真实上游。
// 参数 t：测试上下文；使用局部事件夹具观察终止状态和原始用量证据。
func TestStreamSessionProtocols(t *testing.T) {
	cases := []struct {
		name   string
		frames []string
		done   bool
		reason string
	}{
		{"chat", []string{`{"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, "[DONE]"}, true, ""},
		{"chat truncated", []string{`{"choices":[{"delta":{"content":"hi"}}]}`}, false, "upstream_incomplete"},
		{"responses", []string{`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":20,"output_tokens":0}}}`}, true, ""},
		{"responses failed", []string{`{"type":"response.failed","response":{"error":{"message":"secret"}}}`}, false, "upstream_error"},
		{"gemini", []string{`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":0}}`}, true, ""},
		{"ollama", []string{`{"done":true,"prompt_eval_count":20,"eval_count":0}`}, true, ""},
		{"cohere", []string{`{"event_type":"stream-end","response":{"meta":{"billed_units":{"input_tokens":20,"output_tokens":0}}}}`}, true, ""},
		{"ping only", []string{`{"type":"ping"}`}, false, "upstream_incomplete"},
		{"claude wrong terminator", []string{`{"type":"message_start","message":{}}`, "[DONE]"}, false, "upstream_protocol_error"},
		{"claude orphan block", []string{`{"type":"content_block_stop","index":0}`}, false, "upstream_protocol_error"},
		{"bad json", []string{`{"choices":`}, false, "upstream_json_error"},
		{"bad usage", []string{`{"usage":{"prompt_tokens":-1}}`}, false, "upstream_json_error"},
		{"upstream error", []string{`{"error":{"message":"private cause"}}`}, false, "upstream_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			for _, f := range tc.frames {
				_ = s.ObserveEvent("", []byte(f))
			}
			s.EndRead(io.EOF)
			v := s.Snapshot()
			require.Equal(t, tc.done, v.Complete)
			require.Equal(t, tc.reason, string(v.Reason))
			if tc.name == "responses" || tc.name == "gemini" || tc.name == "ollama" || tc.name == "cohere" {
				require.Equal(t, 20, v.Evidence["input_tokens"])
				require.Contains(t, v.Evidence, "output_tokens")
				require.Zero(t, v.Evidence["output_tokens"])
			}
		})
	}
}

// TestStreamSessionHTTPFragments 覆盖原始 SSE 帧跨 Read、多 data 行、读取错误及超限保护。
// 参数 t：测试上下文；响应体仅使用内存，不产生额外网络请求。
func TestStreamSessionHTTPFragments(t *testing.T) {
	for _, body := range []string{"data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\r\n\r\ndata: [DONE]\r\n\r\n", "data: {\"choices\":\ndata: [{\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
		s.ObserveHTTP(resp)
		var got strings.Builder
		b := make([]byte, 1)
		for {
			n, err := resp.Body.Read(b)
			got.Write(b[:n])
			if err != nil {
				require.ErrorIs(t, err, io.EOF)
				break
			}
		}
		require.Equal(t, body, got.String())
		require.True(t, s.Snapshot().Complete)
	}
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.EndRead(errors.New("read failure"))
	require.Equal(t, StreamEndReason("upstream_read_error"), s.Snapshot().Reason)
	s = NewStreamSession(types.RelayFormatOpenAI)
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[]}"))}
	s.ObserveHTTP(resp)
	_, _ = io.ReadAll(resp.Body)
	require.Equal(t, StreamEndReason("upstream_incomplete"), s.Snapshot().Reason)
}

// TestStreamSessionDelivery 只把成功交付内容计入有效返回，签名、保活和空白不算正文。
// 参数 t：测试上下文；CommitDelivery 必须由成功写出并刷新后的调用者执行。
func TestStreamSessionDelivery(t *testing.T) {
	for _, data := range []string{`{"type":"ping"}`, `{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"abc"}}`, `{"choices":[{"delta":{"content":"  "}}]}`} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		s.CommitDelivery([]byte(data))
		require.False(t, s.Snapshot().Effective)
	}
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.CommitDelivery([]byte(`{"choices":[{"delta":{"content":"hello"}}]}`))
	require.True(t, s.Snapshot().Effective)
	require.Equal(t, "hello", s.TakeDeliveredText())
	require.Empty(t, s.TakeDeliveredText())
}

// TestStreamSessionRegression 覆盖混合换行、原始失败帧、写失败后的读错误与 null error。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamSessionRegression(t *testing.T) {
	first := "data: {\"type\":\"ping\"}\r\n\r\n"
	require.Equal(t, len(first), StreamFrameEnd([]byte(first+"data: [DONE]\n\n")))
	s := NewStreamSession(types.RelayFormatOpenAIResponses)
	body := "event: response.failed\r\ndata: {\"type\":\"response.failed\",\"response\":{\"usage\":{\"input_tokens\":9}}}\r\n\r\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	s.ObserveHTTP(resp)
	_, _ = io.ReadAll(resp.Body)
	require.Equal(t, body, string(s.Snapshot().ErrorFrame))
	require.Equal(t, 9, s.Snapshot().Evidence["input_tokens"])
	s = NewStreamSession(types.RelayFormatOpenAI)
	s.CommitDelivery([]byte(`{"error":null,"choices":[{"delta":{"content":"hi"}}]}`))
	require.True(t, s.Snapshot().Effective)
	require.False(t, s.Snapshot().ErrorDelivered)
	s.ClientFailed(io.ErrClosedPipe)
	s.EndRead(errors.New("read interrupted by close"))
	s.Fail("upstream_json_error", errors.New("callback failed after write"))
	require.Empty(t, s.Snapshot().Reason)
}

// TestStreamSessionMultipleImages 单张完成属于有效媒体而非全局结束，截断时保留已生成张数证据。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamSessionMultipleImages(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.ExpectedImages = 2
	frame := []byte(`{"type":"image_generation.completed","b64_json":"aGk="}`)
	require.NoError(t, s.ObserveEvent("", frame))
	s.CommitDelivery(frame)
	require.False(t, s.Snapshot().Complete)
	require.True(t, s.Snapshot().Effective)
	require.False(t, IsStreamSuccessEvent("", frame))
	s.EndRead(io.EOF)
	require.Equal(t, 1, s.Snapshot().Evidence["image_count"])
	require.Equal(t, StreamEndReason("upstream_incomplete"), s.Snapshot().Reason)
}

// TestStreamSessionJSONFraming 区分缺失 Content-Type 的完整 JSON 与带协议终态的 NDJSON，避免把排版换行或单行截断误判。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamSessionJSONFraming(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		complete                bool
	}{
		{"pretty JSON", "", "{\n  \"choices\": [{\"message\": {\"content\": \"hi\"}, \"finish_reason\": \"stop\"}]\n}", true},
		{"NDJSON complete", "", "{\"done\":false,\"response\":\"hi\"}\n{\"done\":true}", true},
		{"NDJSON incomplete", "", `{"done":false,"response":"hi"}`, false},
		{"mislabelled incomplete", "application/json", `{"done":false,"response":"hi"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			s.ObserveHTTP(resp)
			_, _ = io.ReadAll(resp.Body)
			require.Equal(t, tc.complete, s.Snapshot().Complete)
			if !tc.complete {
				require.Equal(t, StreamEndReason("upstream_incomplete"), s.Snapshot().Reason)
			}
		})
	}
}
