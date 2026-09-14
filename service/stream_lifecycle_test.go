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
				} else if !client && delivered {
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
