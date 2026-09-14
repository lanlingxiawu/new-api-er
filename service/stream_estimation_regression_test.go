package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamEstimatePartitionInvariant 验证 t 中各模型的实际刷新分片不改变异常输出估算，每次尝试独立。
func TestStreamEstimatePartitionInvariant(t *testing.T) {
	for _, model := range []string{"gpt-4o", "claude-opus-4", "gemini-pro"} {
		for _, full := range []string{"hello", "中文日语かな한글", "abc123xyz @a/b? ∑\n🙂 !"} {
			for _, batch := range []int{1, 2, 100} {
				t.Run(fmt.Sprintf("%s/%s/%d", model, full, batch), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
					info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
					for attempt := 0; attempt < 2; attempt++ {
						BeginStreamAttempt(c, info)
						info.UpstreamModelName = model // 模型映射发生在 Begin 之后。
						info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
						c.Header("Content-Type", "text/event-stream")
						for i, r := range []rune(full) {
							payload, err := common.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(r)}}}})
							require.NoError(t, err)
							_, err = c.Writer.Write(append(append([]byte("data: "), payload...), []byte("\n\n")...))
							require.NoError(t, err)
							if (i+1)%batch == 0 {
								require.NoError(t, info.StreamWriter.FlushError())
							}
						}
						require.NoError(t, info.StreamWriter.FlushError())
						info.StreamSession.EndRead(io.EOF)
						usage := FinalizeStreamUsage(c, info, nil)
						require.Equal(t, "estimated", info.StreamResult.UsageSource)
						require.Equal(t, EstimateTokenByModel(model, full), usage.CompletionTokens)
					}
				})
			}
		}
	}
}

// TestStreamVendorCacheSettlement 验证 t 中缓存原始别名在异常确认结算、透传及客户端取消后仍保留。
func TestStreamVendorCacheSettlement(t *testing.T) {
	for _, tc := range []struct {
		name    string // 渠道兼容字段名称。
		channel int    // 当前尝试渠道，不使用重试遗留的 ChannelMeta。
		extra   string // 在根对象插入的缓存字段。
	}{
		{"deepseek", constant.ChannelTypeDeepSeek, `"usage":{"prompt_tokens":1000,"completion_tokens":3,"prompt_cache_hit_tokens":900}`},
		{"moonshot choice", constant.ChannelTypeMoonshot, `"usage":{"prompt_tokens":1000,"completion_tokens":3},"choices":[{"usage":{"cached_tokens":900}},{"usage":{"cached_tokens":900}}]`},
		{"moonshot root", constant.ChannelTypeMoonshot, `"usage":{"prompt_tokens":1000,"completion_tokens":3,"cached_tokens":900}`},
		{"zhipu", constant.ChannelTypeZhipu_v4, `"usage":{"prompt_tokens":1000,"completion_tokens":3,"prompt_cache_hit_tokens":900}`},
		{"llama", constant.ChannelTypeOpenAI, `"usage":{"prompt_tokens":1000,"completion_tokens":3},"timings":{"cache_n":900}`},
	} {
		for _, passthrough := range []bool{false, true} {
			for _, clientGone := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/pass=%v/client=%v", tc.name, passthrough, clientGone), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
					common.SetContextKey(c, constant.ContextKeyChannelType, tc.channel)
					info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic}}
					info.ChannelSetting.PassThroughBodyEnabled = passthrough
					BeginStreamAttempt(c, info)
					info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
					for repeat := 0; repeat < 2; repeat++ {
						require.NoError(t, info.StreamSession.ObserveEvent("", []byte("{"+tc.extra+"}")))
					}
					if clientGone {
						cancel() // 有确认用量的用户断开不要求已经交付。
					} else {
						info.StreamSession.CommitDelivery([]byte(`{"choices":[{"delta":{"content":"hello"}}]}`))
						info.StreamSession.EndRead(io.EOF)
					}
					selected := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 9999})
					require.Equal(t, "upstream", info.StreamResult.UsageSource)
					require.Equal(t, 1000, selected.PromptTokens)
					require.Equal(t, 900, selected.PromptTokensDetails.CachedTokens)
					require.Equal(t, 900, info.StreamResult.Diagnostic.UsageEvidence["cached_tokens"])
					info.ChannelType = tc.channel // 模拟真实渠道初始化后用于原有费用计算。
					info.PriceData.ModelRatio = 1
					info.PriceData.CompletionRatio = 1
					info.PriceData.CacheRatio = 0.1
					info.PriceData.GroupRatioInfo.GroupRatio = 1
					require.Equal(t, 193, calculateTextQuotaSummary(c, info, selected).Quota, "100 普通输入 + 900×0.1 缓存 + 3 输出")
				})
			}
		}
	}
}
