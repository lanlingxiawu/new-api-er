package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamDifyUsageEvidence 验证 Dify 终止用量的字段作用域、数值边界及原子更新；t 提供断言上下文。
func TestStreamDifyUsageEvidence(t *testing.T) {
	valid := `{"event":"message_end","metadata":{"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"total_price":"0.001"}}}`
	for _, tc := range []struct {
		name    string         // 用例名，区分报告格式与渠道边界。
		channel int            // 当前真实上游渠道，非 Dify 不解释其专属字段。
		frames  []string       // 按顺序观察的原始事件，不经过适配器转换。
		want    map[string]int // 期望的完整确认用量证据，空 map 表示没有确认用量。
		bad     bool           // 是否应触发上游用量校验错误。
	}{
		{"valid", constant.ChannelTypeDify, []string{valid}, map[string]int{"input_tokens": 12, "output_tokens": 3}, false},
		{"repeated cumulative", constant.ChannelTypeDify, []string{valid, valid}, map[string]int{"input_tokens": 12, "output_tokens": 3}, false},
		{"explicit zero", constant.ChannelTypeDify, []string{`{"event":"message_end","metadata":{"usage":{"prompt_tokens":0,"completion_tokens":0}}}`}, map[string]int{"input_tokens": 0, "output_tokens": 0}, false},
		{"missing", constant.ChannelTypeDify, []string{`{"event":"message_end","metadata":{}}`}, map[string]int{}, false},
		{"null", constant.ChannelTypeDify, []string{`{"event":"message_end","metadata":{"usage":null}}`}, map[string]int{}, false},
		{"other channel", constant.ChannelTypeOpenAI, []string{valid}, map[string]int{}, false},
		{"node is not request usage", constant.ChannelTypeDify, []string{`{"event":"node_finished","metadata":{"usage":{"prompt_tokens":999}}}`}, map[string]int{}, false},
		{"authoritative metadata", constant.ChannelTypeDify, []string{`{"event":"message_end","usage":{"prompt_tokens":999},"metadata":{"usage":{"prompt_tokens":0,"completion_tokens":3}}}`}, map[string]int{"input_tokens": 0, "output_tokens": 3}, false},
		{"negative atomic update", constant.ChannelTypeDify, []string{valid, `{"event":"message_end","metadata":{"usage":{"prompt_tokens":99,"completion_tokens":-1}}}`}, map[string]int{"input_tokens": 12, "output_tokens": 3}, true},
		{"fraction", constant.ChannelTypeDify, []string{`{"event":"message_end","metadata":{"usage":{"prompt_tokens":1.5}}}`}, map[string]int{}, true},
		{"string", constant.ChannelTypeDify, []string{`{"event":"message_end","metadata":{"usage":{"prompt_tokens":"12"}}}`}, map[string]int{}, true},
		{"limit", constant.ChannelTypeDify, []string{`{"event":"message_end","metadata":{"usage":{"prompt_tokens":1000000000}}}`}, map[string]int{"input_tokens": 1000000000}, false},
		{"over limit", constant.ChannelTypeDify, []string{`{"event":"message_end","metadata":{"usage":{"prompt_tokens":1000000001}}}`}, map[string]int{}, true},
		{"not object", constant.ChannelTypeDify, []string{`{"event":"message_end","metadata":{"usage":[]}}`}, map[string]int{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.ChannelType = tc.channel
			for _, frame := range tc.frames {
				_ = s.ObserveEvent("", []byte(frame))
			}
			v := s.Snapshot()
			require.Equal(t, tc.want, v.Evidence)
			if tc.bad {
				require.Equal(t, StreamEndReason("upstream_json_error"), v.Reason)
				require.False(t, v.Complete)
			} else {
				require.NoError(t, v.Err)
			}
		})
	}
}
