package common_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamCacheEvidenceValidation 验证 t 中缓存别名的显式零、优先级、数值边界及同帧原子提交。
func TestStreamCacheEvidenceValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string // 用例名及原始证据帧。
		channel    int    // 仅白名单渠道采用兼容字段。
		want       int    // 规范化缓存 token。
		present    bool   // 区别缺失与确认零。
	}{
		{"standard zero", `{"usage":{"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_hit_tokens":99}}`, constant.ChannelTypeDeepSeek, 0, true},
		{"standard positive", `{"usage":{"prompt_tokens_details":{"cached_tokens":7},"cached_tokens":88},"choices":[{"usage":{"cached_tokens":99}}]}`, constant.ChannelTypeMoonshot, 7, true},
		{"input details", `{"usage":{"input_tokens_details":{"cached_tokens":7},"cached_tokens":88}}`, constant.ChannelTypeZhipu_v4, 7, true},
		{"choice zero", `{"choices":[{"usage":{"cached_tokens":0}},{"usage":{"cached_tokens":99}}],"usage":{"cached_tokens":88}}`, constant.ChannelTypeMoonshot, 0, true},
		{"choice missing", `{"choices":[{"delta":{}},{"usage":{"cached_tokens":7}}],"usage":{"cached_tokens":88}}`, constant.ChannelTypeMoonshot, 7, true},
		{"root zero", `{"usage":{"cached_tokens":0,"prompt_cache_hit_tokens":99}}`, constant.ChannelTypeZhipu_v4, 0, true},
		{"timings zero", `{"timings":{"cache_n":0}}`, constant.ChannelTypeOpenAI, 0, true},
		{"limit", `{"usage":{"prompt_cache_hit_tokens":1000000000}}`, constant.ChannelTypeDeepSeek, 1000000000, true},
		{"unrelated channel", `{"usage":{"cached_tokens":99,"prompt_cache_hit_tokens":99},"timings":{"cache_n":99}}`, constant.ChannelTypeAnthropic, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
			s.ChannelType = tc.channel
			s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			require.NoError(t, s.ObserveEvent("", []byte(tc.body)))
			got, present := s.Snapshot().Evidence["cached_tokens"]
			require.Equal(t, tc.present, present)
			require.Equal(t, tc.want, got)
		})
	}
	for _, path := range []struct {
		channel int    // 待校验字段所属渠道。
		frame   string // 数字格式化位置位于当前缓存字段。
	}{
		{constant.ChannelTypeDeepSeek, `{"usage":{"prompt_tokens":777,"prompt_cache_hit_tokens":%s}}`},
		{constant.ChannelTypeMoonshot, `{"usage":{"prompt_tokens":777},"choices":[{"usage":{"cached_tokens":%s}}]}`},
		{constant.ChannelTypeMoonshot, `{"usage":{"prompt_tokens":777,"cached_tokens":%s}}`},
		{constant.ChannelTypeOpenAI, `{"usage":{"prompt_tokens":777},"timings":{"cache_n":%s}}`},
	} {
		for _, bad := range []string{"null", `"9"`, "-1", "1.5", "1000000001", "true", "{}"} {
			t.Run(fmt.Sprintf("invalid/%d/%s/%s", path.channel, path.frame, bad), func(t *testing.T) {
				s := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
				s.ChannelType = path.channel
				s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
				require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":10,"prompt_tokens_details":{"cached_tokens":2}}}`)))
				before := s.Snapshot().Evidence
				require.Error(t, s.ObserveEvent("", []byte(fmt.Sprintf(path.frame, bad))))
				require.Equal(t, before, s.Snapshot().Evidence)
				require.Equal(t, relaycommon.StreamEndReason("upstream_json_error"), s.Snapshot().Reason)
			})
		}
	}
}
