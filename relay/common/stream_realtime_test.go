package common

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamRealtimeEventScope 验证豁免限于 Realtime，恢复错误不污染用量与终止去重；t 为测试上下文。
func TestStreamRealtimeEventScope(t *testing.T) {
	for _, tc := range []struct {
		name, raw             string // 已解析事件及期望语义。
		complete, recoverable bool   // 是否正常结束本轮、或仅恢复事件。
	}{
		{"cancel", `{"type":"response.done","response":{"status":"cancelled","usage":{"input_tokens":10,"output_tokens":0}}}`, true, false},
		{"limited", `{"type":"response.done","response":{"status":"incomplete","status_details":{"reason":"max_output_tokens"}}}`, true, false},
		{"filtered", `{"type":"response.done","response":{"status":"incomplete","status_details":{"reason":"content_filter"}}}`, true, false},
		{"unknown incomplete", `{"type":"response.done","response":{"status":"incomplete","status_details":{"reason":"unknown"}}}`, false, false},
		{"contradictory error", `{"type":"response.done","response":{"status":"cancelled","status_details":{"error":{"code":"failed"}}}}`, false, false},
		{"failed", `{"type":"response.done","response":{"status":"failed"}}`, false, false},
		{"recoverable", `{"type":"error","error":{"type":"invalid_request_error"},"usage":{"input_tokens":999}}`, false, true},
		{"fatal", `{"type":"error","error":{"type":"server_error"}}`, false, false},
		{"unknown error", `{"type":"error","error":{"type":"unknown"}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAIRealtime)
			_ = s.ObserveEvent("", []byte(tc.raw))
			s.CommitDelivery([]byte(tc.raw))
			require.Equal(t, tc.complete, s.ProtocolComplete())
			require.Equal(t, !tc.complete && !tc.recoverable, s.Snapshot().UpstreamFailure)
			require.Equal(t, !tc.complete && !tc.recoverable, s.Snapshot().ErrorDelivered)
			if tc.recoverable {
				require.Empty(t, s.Snapshot().Evidence)
				s.EndRead(io.ErrUnexpectedEOF)
				require.Equal(t, StreamEndReason("upstream_read_error"), s.Snapshot().Reason)
				require.False(t, s.Snapshot().ErrorDelivered, "后续真实故障仍应补发错误")
			}
			if tc.complete || tc.recoverable {
				http := NewStreamSession(types.RelayFormatOpenAIResponses)
				_ = http.ObserveEvent("", []byte(tc.raw))
				require.Equal(t, StreamEndReason("upstream_error"), http.Snapshot().Reason, "HTTP Responses 不采用 WS 恢复语义")
			}
		})
	}
}
