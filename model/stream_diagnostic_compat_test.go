package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestStreamDiagnosticCompatibility 验证双键同时过滤、新键优先、独立原因和旧标记隔离；t 为测试上下文。
func TestStreamDiagnosticCompatibility(t *testing.T) {
	for _, tc := range []struct {
		raw            string
		attempt        float64
		source, reason string
	}{
		{`{"claude_diagnostic":{"reject_reason":"old","downstream_body_base64":"private"},"claude_stream":{"usage_source":"upstream"},"claude_diagnostic_attempt":1}`, 1, "upstream", "old"},
		{`{"stream_diagnostic":{"reject_reason":"new","downstream_body_base64":"private"},"stream_result":{"usage_source":"none"},"stream_diagnostic_attempt":2}`, 2, "none", "new"},
		{`{"claude_diagnostic":{"reject_reason":"old","error":"private"},"stream_diagnostic":{"reject_reason":"new","downstream_body_base64":"private"},"claude_stream":{"usage_source":"upstream"},"stream_result":{"usage_source":"none"},"claude_diagnostic_attempt":1,"stream_diagnostic_attempt":2,"reject_reason":"top","claude_diagnostic_available":true}`, 2, "none", "top"},
	} {
		log := Log{Other: tc.raw}
		require.NoError(t, log.AfterFind(nil))
		var fields map[string]any
		require.NoError(t, common.UnmarshalJsonStr(log.Other, &fields))
		require.NotContains(t, log.Other, "private")
		require.NotContains(t, log.Other, "claude_")
		require.NotContains(t, fields, "stream_diagnostic")
		require.NotContains(t, fields, "stream_diagnostic_available")
		require.Equal(t, tc.attempt, fields["stream_diagnostic_attempt"])
		require.Equal(t, tc.source, fields["stream_result"].(map[string]any)["usage_source"])
		require.Equal(t, tc.reason, fields["reject_reason"])
	}
	log := Log{Other: `{"stream_diagnostic":broken`}
	require.NoError(t, log.AfterFind(nil))
	require.Equal(t, "{}", log.Other)
}
