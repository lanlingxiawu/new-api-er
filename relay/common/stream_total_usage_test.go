package common

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamProviderTotalUsage 验证同帧总量归一化、显式零与渠道隔离；t 为测试上下文。
func TestStreamProviderTotalUsage(t *testing.T) {
	for _, channel := range []int{constant.ChannelTypeBaidu, constant.ChannelTypeXai} {
		for _, tc := range []struct {
			name, usage string         // 用例名及原始顶层 usage。
			want        map[string]int // 期望规范证据，不从上一帧借缺失字段。
		}{
			{"derived", `{"prompt_tokens":10,"total_tokens":14}`, map[string]int{"input_tokens": 10, "output_tokens": 4, "total_tokens": 14}},
			{"authoritative total", `{"prompt_tokens":10,"completion_tokens":1,"total_tokens":14}`, map[string]int{"input_tokens": 10, "output_tokens": 4, "total_tokens": 14}},
			{"zero output", `{"prompt_tokens":10,"completion_tokens":4,"total_tokens":10}`, map[string]int{"input_tokens": 10, "output_tokens": 0, "total_tokens": 10}},
			{"zero all", `{"prompt_tokens":0,"total_tokens":0}`, map[string]int{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}},
			{"absent total", `{"prompt_tokens":10,"completion_tokens":2}`, map[string]int{"input_tokens": 10, "output_tokens": 2}},
			{"total alone", `{"total_tokens":14}`, map[string]int{}},
			{"input alias not chat tuple", `{"input_tokens":10,"output_tokens":2,"total_tokens":14}`, map[string]int{"input_tokens": 10, "output_tokens": 2}},
		} {
			t.Run(tc.name+strconv.Itoa(channel), func(t *testing.T) {
				s := NewStreamSession(types.RelayFormatOpenAI)
				s.ChannelType = channel
				require.NoError(t, s.ObserveEvent("", []byte(`{"usage":`+tc.usage+`}`)))
				require.Equal(t, tc.want, s.Snapshot().Evidence)
			})
		}
	}
	for _, channel := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeBaiduV2, constant.ChannelTypeAnthropic, constant.ChannelTypeGemini} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		s.ChannelType = channel
		require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":14}}`)))
		require.Equal(t, map[string]int{"input_tokens": 10, "output_tokens": 2}, s.Snapshot().Evidence)
	}
}

// TestStreamProviderTotalUsageAtomicity 验证坏总量整批回滚和前帧保留；t 为测试上下文。
func TestStreamProviderTotalUsageAtomicity(t *testing.T) {
	for _, channel := range []int{constant.ChannelTypeBaidu, constant.ChannelTypeXai} {
		for _, total := range []string{`-1`, `1.5`, `"14"`, `null`, `1000000001`, `9`} {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.ChannelType = channel
			require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":5,"total_tokens":7}}`)))
			require.Error(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":`+total+`}}`)))
			require.Equal(t, map[string]int{"input_tokens": 5, "output_tokens": 2, "total_tokens": 7}, s.Snapshot().Evidence)
			require.Equal(t, StreamEndReason("upstream_json_error"), s.Snapshot().Reason)
		}
	}
}
