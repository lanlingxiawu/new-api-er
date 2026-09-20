package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestUnifiedStreamBillingMatrix 验证六行异常合同、显式零、非流式隔离，不访问资金数据库。
// 参数 t：测试上下文；仅调用费用选择，不执行 PostConsume 资金操作。
func TestUnifiedStreamBillingMatrix(t *testing.T) {
	for _, client := range []bool{false, true} {
		for _, confirmed := range []bool{false, true} {
			for _, delivered := range []bool{false, true} {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				reqCtx, cancel := context.WithCancel(context.Background())
				defer cancel()
				c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(reqCtx)
				info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				info.SetEstimatePromptTokens(10)
				BeginStreamAttempt(c, info)
				info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
				if confirmed {
					require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":20,"completion_tokens":3}}`)))
				}
				if delivered {
					require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[{"delta":{"content":"hello"}}]}`)))
					info.StreamSession.CommitDelivery([]byte(`{"choices":[{"delta":{"content":"hello"}}]}`))
					info.StreamSession.AddEstimatedOutput(2)
				}
				if client {
					cancel()
				} else {
					info.StreamSession.EndRead(io.EOF)
				}
				usage := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 999, CompletionTokens: 999})
				expected := "none"
				if confirmed && (client || delivered) {
					expected = "upstream"
				} else if delivered {
					expected = "estimated"
				}
				require.Equal(t, expected, info.StreamResult.UsageSource)
				switch expected {
				case "none":
					require.Zero(t, usage.TotalTokens)
				case "upstream":
					require.Equal(t, 20, usage.PromptTokens)
					require.Equal(t, 3, usage.CompletionTokens)
				case "estimated":
					require.Equal(t, 10, usage.PromptTokens)
					require.Greater(t, usage.CompletionTokens, 0)
				}
			}
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}
	BeginStreamAttempt(c, info)
	require.Nil(t, info.StreamSession)
	info.IsStream = true // 模拟上游 Content-Type 改写，不应把入口非流式升级为新结算。
	BeginStreamAttempt(c, info)
	require.Nil(t, info.StreamSession)
	usage := &dto.Usage{PromptTokens: 5}
	require.Same(t, usage, FinalizeStreamUsage(c, info, usage))
	require.Nil(t, info.StreamResult)
}

// TestUnifiedStreamExpectedCandidateRequest 验证入口 DTO 的 n/candidateCount 在透传和重试中继续约束完成。
// 参数 t 为测试上下文；不读取或采集请求原文，也不访问数据库。
func TestUnifiedStreamExpectedCandidateRequest(t *testing.T) {
	for _, gemini := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		n := 2
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, Request: &dto.GeneralOpenAIRequest{N: &n}, ChannelMeta: &relaycommon.ChannelMeta{}}
		info.ChannelSetting.PassThroughBodyEnabled = true
		if gemini {
			info.Request = &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{CandidateCount: &n}}
		}
		for attempt := 0; attempt < 2; attempt++ {
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			require.Equal(t, 2, info.StreamSession.ExpectedChoices)
			require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[{"index":0,"finish_reason":"stop"}]}`)))
			require.False(t, info.StreamSession.Snapshot().Complete)
			info.StreamSession.EndRead(io.EOF)
			require.Equal(t, relaycommon.StreamEndReason("upstream_incomplete"), info.StreamSession.Snapshot().Reason)
		}
	}
}

// TestUnifiedStreamTerminalOnce 验证本地错误只输出一次、错误中不泄漏底层原因。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamTerminalOnce(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	info.StreamSession.Fail("upstream_read_error", errors.New("private secret"))
	FinalizeStreamUsage(c, info, nil)
	FinalizeStreamUsage(c, info, nil)
	require.NotContains(t, recorder.Body.String(), "private secret")
	require.Contains(t, recorder.Body.String(), `"error"`)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
	require.Contains(t, info.StreamResult.Diagnostic.Error, "private secret")
}

// TestUnifiedStreamNativeErrorShape 原生供应商错误经 OpenAI 转换时使用兼容错误；已经符合下游协议的原错误逐字保留。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamNativeErrorShape(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		preserve  bool
	}{
		{"openai", "event: error\ndata: {\"error\":{\"type\":\"server_error\",\"message\":\"upstream-detail\"}}\n\n", true},
		{"xunfei", "event: error\ndata: {\"header\":{\"code\":100,\"message\":\"upstream-detail\"}}\n\n", false},
		{"ollama", "event: error\ndata: {\"error\":\"upstream-detail\"}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			WriteStreamTerminalError(c, info, relaycommon.StreamSnapshot{ErrorFrame: []byte(tc.raw)})
			if tc.preserve {
				require.Equal(t, tc.raw, rec.Body.String())
			} else {
				require.Contains(t, rec.Body.String(), `"code":"upstream_stream_error"`)
				require.NotContains(t, rec.Body.String(), "upstream-detail")
			}
		})
	}
}

// TestUnifiedStreamAttemptIsolation 透传开关不影响接入；每次重试清空用量、交付与诊断。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamAttemptIsolation(t *testing.T) {
	for channel := 1; channel <= 80; channel++ {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: channel, ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: true}}}
		BeginStreamAttempt(c, info)
		require.False(t, info.StreamSession.Active())
		info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
		require.True(t, info.StreamSession.Active())
		require.True(t, common.IsPrivateStream(c.Request.Context()))
		require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":33,"completion_tokens":0}}`)))
		info.StreamSession.CommitDelivery([]byte(`{"choices":[{"delta":{"content":"old"}}]}`))
		old := info.StreamSession
		BeginStreamAttempt(c, info)
		require.NotSame(t, old, info.StreamSession)
		require.Empty(t, info.StreamSession.Snapshot().Evidence)
		require.False(t, info.StreamSession.Snapshot().Effective)
		require.False(t, info.StreamSession.Active())
		info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
		require.Equal(t, 2, info.StreamDiagnostic.Snapshot().Attempt)
	}
}

// TestUnifiedStreamLogMode 上游退化为完整 JSON 不改变入口流式请求的结算日志标记。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamLogMode(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	info.IsStream = false // 模拟渠道按上游 application/json 选择完整响应解析器。
	info.StreamSession.Complete()
	FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 3})
	require.True(t, info.IsStream)
}

// TestUnifiedStreamUsageSemantics 验证透传 Claude 缓存、Gemini 思考及显式零不会被旧估算覆盖。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamUsageSemantics(t *testing.T) {
	for _, tc := range []struct {
		raw                   string
		prompt, output, cache int
		semantic              string
	}{
		{`{"type":"message_start","message":{"usage":{"input_tokens":3,"cache_read_input_tokens":7,"output_tokens":0}}}`, 3, 0, 7, "anthropic"},
		{`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":4,"cachedContentTokenCount":3}}`, 10, 6, 3, ""},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
		BeginStreamAttempt(c, info)
		info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
		require.NoError(t, info.StreamSession.ObserveEvent("", []byte(tc.raw)))
		info.StreamSession.CommitDelivery([]byte(`{"choices":[{"delta":{"content":"hi"}}]}`))
		info.StreamSession.EndRead(io.EOF)
		u := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 999, CompletionTokens: 999})
		require.Equal(t, tc.prompt, u.PromptTokens)
		require.Equal(t, tc.output, u.CompletionTokens)
		require.Equal(t, tc.cache, u.PromptTokensDetails.CachedTokens)
		require.Equal(t, tc.semantic, u.UsageSemantic)
	}
}

// TestUnifiedStreamUsageAliases 验证重复别名按实际协议确定优先级，显式零和多模态/缓存细分不被丢弃。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamUsageAliases(t *testing.T) {
	for _, tc := range []struct {
		raw           string
		input, output int
	}{
		{`{"usage":{"prompt_tokens":9,"input_tokens":0,"completion_tokens":2,"output_tokens":0}}`, 9, 2},
		{`{"type":"response.failed","response":{"usage":{"prompt_tokens":0,"input_tokens":9,"completion_tokens":0,"output_tokens":2}}}`, 9, 2},
		{`{"type":"response.failed","response":{"usage":{"prompt_tokens":9,"input_tokens":0,"completion_tokens":2,"output_tokens":0}}}`, 0, 0},
	} {
		for i := 0; i < 32; i++ {
			s := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
			require.NoError(t, s.ObserveEvent("", []byte(tc.raw)))
			e := s.Snapshot().Evidence
			require.Equal(t, tc.input, e["input_tokens"])
			require.Equal(t, tc.output, e["output_tokens"])
		}
	}
	s := relaycommon.NewStreamSession(types.RelayFormatOpenAIResponses)
	require.NoError(t, s.ObserveEvent("", []byte(`{"type":"response.failed","response":{"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"audio_tokens":10,"image_tokens":5,"cached_tokens":3,"cache_write_tokens":2},"output_tokens_details":{"audio_tokens":4,"image_tokens":2,"reasoning_tokens":6}}}}`)))
	u := BuildConfirmedStreamUsage(&relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses}, s.Snapshot().Evidence)
	require.Equal(t, 100, u.PromptTokens)
	require.Equal(t, 20, u.CompletionTokens) // OpenAI reasoning 已包含在总输出内，不再相加。
	require.Equal(t, 10, u.PromptTokensDetails.AudioTokens)
	require.Equal(t, 5, u.PromptTokensDetails.ImageTokens)
	require.Equal(t, 3, u.PromptTokensDetails.CachedTokens)
	require.Equal(t, 2, u.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 4, u.CompletionTokenDetails.AudioTokens)
	require.Equal(t, 2, u.CompletionTokenDetails.ImageTokens)
	require.Equal(t, 6, u.CompletionTokenDetails.ReasoningTokens)
}

// streamReserveFixture 记录逐轮目标额度，所有资金操作均为本地接口夹具。
type streamReserveFixture struct {
	claudeSettlementFixture
	target, reserves int
	reserveErr       error
}

// Reserve 记录目标累计预留；参数 quota 不表示新增实际扣款。
func (f *streamReserveFixture) Reserve(quota int) error {
	f.target = quota
	f.reserves++
	return f.reserveErr
}

// TestUnifiedStreamRealtimeReserve 验证逐轮只追加预留目标，零用量不收费，预留错误交给所有者。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedStreamRealtimeReserve(t *testing.T) {
	f := &streamReserveFixture{}
	info := &relaycommon.RelayInfo{Billing: f, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	info.PriceData.UsePrice = true
	info.PriceData.ModelPrice = 1
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	u := &dto.RealtimeUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}
	require.NoError(t, ReserveRealtimeStreamUsage(info, u))
	require.Positive(t, f.target)
	require.Equal(t, 1, f.reserves)
	require.Zero(t, f.calls)
	require.NoError(t, ReserveRealtimeStreamUsage(info, &dto.RealtimeUsage{}))
	require.Zero(t, f.target)
	f.reserveErr = errors.New("reservation error")
	require.ErrorIs(t, ReserveRealtimeStreamUsage(info, u), f.reserveErr)
	require.Zero(t, f.calls)
}

// TestEstimatedStreamOutputNeverZeroWhenDelivered 断言接收侧读不出上游方言时不按 0 结算：
// 已成功交付过有效内容就回落到交付侧估算，避免"有产出却零收费"的漏收。
func TestEstimatedStreamOutputNeverZeroWhenDelivered(t *testing.T) {
	for _, tc := range []struct {
		name       string
		snapshot   relaycommon.StreamSnapshot
		clientGone bool
		want       int
	}{
		{
			name:     "正常终止取交付侧",
			snapshot: relaycommon.StreamSnapshot{EstimatedOutput: 1415, ReceivedOutput: 1500, Effective: true},
			want:     1415,
		},
		{
			name:       "客户端断开取接收侧",
			snapshot:   relaycommon.StreamSnapshot{EstimatedOutput: 1415, ReceivedOutput: 1500, Effective: true},
			clientGone: true,
			want:       1500,
		},
		{
			name:       "接收侧为 0 但交付过内容时回落交付侧",
			snapshot:   relaycommon.StreamSnapshot{EstimatedOutput: 1415, ReceivedOutput: 0, Effective: true},
			clientGone: true,
			want:       1415,
		},
		{
			name:       "没有有效交付仍为 0",
			snapshot:   relaycommon.StreamSnapshot{EstimatedOutput: 1415, ReceivedOutput: 0, Effective: false},
			clientGone: true,
			want:       0,
		},
		{
			name:       "两侧都读不出仍为 0",
			snapshot:   relaycommon.StreamSnapshot{EstimatedOutput: 0, ReceivedOutput: 0, Effective: true},
			clientGone: true,
			want:       0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, estimatedStreamOutput(tc.snapshot, tc.clientGone))
		})
	}
}

// TestFinalizeStreamUsageBillsInlineMediaOnClientGone 端到端验证"客户端断开"口径会把原生内联媒体
// 按张计入：接收侧是该口径的唯一来源，修复前 inlineData 在这里不产生任何量，纯图片响应按 0 结算。
func TestFinalizeStreamUsageBillsInlineMediaOnClientGone(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini-2.5-flash-image:streamGenerateContent", nil).WithContext(reqCtx)

	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatGemini,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash-image"}}
	info.SetEstimatePromptTokens(7)
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)

	// 上游只回一张内联图片，没有文本、也没有 usageMetadata（断开发生在确认用量到达之前）。
	imageEvent := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"QUJDRA=="}}]}}]}`)
	require.NoError(t, info.StreamSession.ObserveEvent("", imageEvent))
	info.StreamSession.CommitDelivery(imageEvent)
	cancel()

	usage := FinalizeStreamUsage(c, info, nil)
	require.Equal(t, "estimated", info.StreamResult.UsageSource)
	require.Equal(t, DataURLMediaTokens, usage.CompletionTokens, "一张内联图片按张折算，不再按 0 结算")
	require.Equal(t, 7, usage.PromptTokens)
}

// TestFinalizeStreamUsageFallsBackWhenDialectUnreadable 端到端验证兜底：接收侧读不出该上游方言时，
// 只要确实交付过有效内容就用交付侧估算，不按 0 结算。
func TestFinalizeStreamUsageFallsBackWhenDialectUnreadable(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(reqCtx)

	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "some-native-model"}}
	info.SetEstimatePromptTokens(5)
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)

	// 上游方言用通用键和 fallback 表都读不出内容：接收侧计不出量。
	require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"unknown_dialect":{"answer":"opaque payload"}}`)))
	// 但转换后确实成功交付给了客户端，交付侧有估算量。
	info.StreamSession.CommitDelivery([]byte(`{"choices":[{"delta":{"content":"converted answer"}}]}`))
	info.StreamSession.AddEstimatedOutput(777)
	cancel()

	usage := FinalizeStreamUsage(c, info, nil)
	require.Equal(t, "estimated", info.StreamResult.UsageSource)
	require.Zero(t, info.StreamSession.Snapshot().ReceivedOutput, "接收侧确实读不出该方言")
	require.Equal(t, 777, usage.CompletionTokens, "回落到交付侧估算而不是 0")
}

// TestFinalizeStreamUsageSupplementsZeroCompletionWithMedia 验证上游给了确认用量但 completion=0
// （不少图片模型就这么回）时，零输出补估会把已交付的内联媒体按张计入，而不是结算 0 输出。
func TestFinalizeStreamUsageSupplementsZeroCompletionWithMedia(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini-2.5-flash-image:streamGenerateContent", nil)

	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatGemini,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash-image"}}
	info.SetEstimatePromptTokens(7)
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)

	imageEvent := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"QUJDRA=="}}]}}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":0,"totalTokenCount":9}}`)
	require.NoError(t, info.StreamSession.ObserveEvent("", imageEvent))
	info.StreamSession.CommitDelivery(imageEvent)
	info.StreamSession.AddEstimatedOutput(DataURLMediaTokens) // 写入器刷新后的交付侧估算
	info.StreamSession.Complete()

	usage := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 9, CompletionTokens: 0})
	require.Equal(t, 9, usage.PromptTokens, "确认的输入量保持原样")
	require.Equal(t, DataURLMediaTokens, usage.CompletionTokens, "输出为 0 时按已交付张数补估")
}
