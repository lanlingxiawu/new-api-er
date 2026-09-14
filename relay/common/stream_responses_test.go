package common

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamResponsesLimitEvents 验证限制终态、真实错误和未知原因的分类；t 只操作本地协议会话。
func TestStreamResponsesLimitEvents(t *testing.T) {
	for _, tc := range []struct {
		name, event, body string // 用例名、SSE 名称和原始 JSON。
		complete          bool   // 是否应按正常限制终态处理。
	}{
		{"limit", "", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, true},
		{"filter", "", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"error":null}}`, true},
		{"compatible done", "", `{"type":"response.done","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, true},
		{"unknown reason", "", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"backend_failure"}}}`, false},
		{"missing reason", "", `{"type":"response.incomplete","response":{"status":"incomplete"}}`, false},
		{"missing status", "", `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`, false},
		{"nested error wins", "", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"error":{"message":"fixture"}}}`, false},
		{"outer error wins", "", `{"type":"response.incomplete","error":{"message":"fixture"},"response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, false},
		{"event error wins", "error", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, false},
		{"failed stays failed", "", `{"type":"response.failed","response":{"status":"failed","incomplete_details":{"reason":"max_output_tokens"}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAIResponses)
			require.Equal(t, !tc.complete, IsStreamErrorEvent(tc.event, []byte(tc.body)))
			require.NoError(t, s.ObserveEvent(tc.event, []byte(tc.body)))
			s.EndRead(io.EOF)
			require.Equal(t, tc.complete, s.ProtocolComplete())
			require.Equal(t, !tc.complete, s.Snapshot().DiagnosticAvailable(false))
			if tc.complete {
				require.Equal(t, streamSuccessResponse, classifyStreamSuccessEvent(tc.event, []byte(tc.body)))
			}
		})
	}
}

// TestStreamResponsesLimitJSON 校验完整 JSON 的正常限制必须同时具备结果结构；t 不连接上游。
func TestStreamResponsesLimitJSON(t *testing.T) {
	for _, tc := range []struct {
		body string // 原始完整响应，含合法或不完整的输出结构。
		ok   bool   // 是否可交给原响应转换器。
	}{
		{`{"object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":5,"output_tokens":2}}`, true},
		{`{"object":"response","status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}`, true},
		{`{"object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}`, false},
		{`{"object":"response","status":"incomplete","incomplete_details":{"reason":"other"},"output":[]}`, false},
		{`{"object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":-1}}`, false},
	} {
		s := NewStreamSession(types.RelayFormatOpenAIResponses)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
		s.ObserveHTTP(resp)
		_, err := io.ReadAll(resp.Body)
		require.Equal(t, tc.ok, err == nil, tc.body)
		require.Equal(t, tc.ok, s.ProtocolComplete(), tc.body)
	}
}

// TestStreamResponsesLimitErrorPriority 验证正常限制不掩盖非法用量或先后到达的真实错误；t 使用本地事件序列。
func TestStreamResponsesLimitErrorPriority(t *testing.T) {
	limit := `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":5,"output_tokens":2}}}`
	invalid := strings.Replace(limit, `"input_tokens":5`, `"input_tokens":-1`, 1)
	failure := `{"type":"error","error":{"type":"server_error","message":"fixture"}}`
	for _, tc := range []struct {
		name   string   // 用例名称，标识错误与终态的先后关系。
		events []string // 按实际接收顺序提供事件。
		reason string   // 首个终止错误必须保持的分类。
	}{
		{"invalid usage", []string{invalid}, "upstream_json_error"},
		{"error before limit", []string{failure, limit}, "upstream_error"},
		{"error after limit", []string{limit, failure}, "upstream_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAIResponses)
			for _, event := range tc.events {
				_ = s.ObserveEvent("", []byte(event))
			}
			s.EndRead(io.EOF)
			snapshot := s.Snapshot()
			require.Equal(t, tc.reason, string(snapshot.Reason))
			require.False(t, snapshot.Complete)
			require.True(t, snapshot.DiagnosticAvailable(false))
		})
	}
}
