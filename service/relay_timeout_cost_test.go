package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 设计 relay-timeout-cost-bearing.md §14 第 2、3 项：我方超时的「只收输入」档与平台承担记录。
// 所有用例只构造「我方时限已到」的请求上下文（超时控制器夹具），不连任何上游。

// timeoutBillingFixture 记录结算与退款调用；err/committed 模拟结算失败与资金已提交。
type timeoutBillingFixture struct {
	settles, refunds int
	actual           int
	pre              int
	err              error
	committed        bool
}

func (f *timeoutBillingFixture) Settle(quota int) error {
	f.settles++
	f.actual = quota
	return f.err
}
func (f *timeoutBillingFixture) Refund(*gin.Context)      { f.refunds++ }
func (f *timeoutBillingFixture) NeedsRefund() bool        { return f.settles == 0 && f.refunds == 0 }
func (f *timeoutBillingFixture) GetPreConsumedQuota() int { return f.pre }
func (f *timeoutBillingFixture) Reserve(int) error        { return nil }
func (f *timeoutBillingFixture) FundingCommitted() bool   { return f.committed }

const (
	timeoutCostPrompt   = 40 // 预扣时的估算输入
	timeoutCostRatio    = 2  // 模型倍率
	timeoutCostSeconds  = 7  // 用户时限，写进日志文案
	timeoutCostPerCall  = 0.01
	timeoutCostModel    = "gpt-timeout-cost"
	timeoutCostUserBase = 91_000
)

// timeoutCostErr 是控制器归一后的我方超时错误。
func timeoutCostErr() *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New("timeout"), types.ErrorCodeRelayTimeout, http.StatusGatewayTimeout, types.ErrOptionWithSkipRetry())
}

// nonStreamTimeoutContext 构造一次我方时限已到、上游尚未返回的非流式请求。
func nonStreamTimeoutContext(t *testing.T, billing string, userID int) (*gin.Context, *relaycommon.RelayInfo, *timeoutBillingFixture) {
	t.Helper()
	require.NoError(t, i18n.Init())
	enableConsumeLogs(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "req-timeout-cost")
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, billing)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &streamTerminalControl{})
	common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, timeoutCostSeconds)
	common.SetContextKey(c, constant.ContextKeyIsStream, false)
	fixture := &timeoutBillingFixture{pre: 1000}
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: timeoutCostModel,
		UserId:          userID,
		TokenId:         userID,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 1, UpstreamModelName: "gpt-4o"},
		Billing:         fixture,
	}
	info.SetEstimatePromptTokens(timeoutCostPrompt)
	// The attempt that ended the request was cut by our deadline (the controller
	// records the normalized error as the last attempt error).
	info.LastError = timeoutCostErr()
	info.PriceData.ModelRatio = timeoutCostRatio
	info.PriceData.CompletionRatio = 4
	info.PriceData.CacheRatio = 0.1
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	return c, info, fixture
}

func usePerCallPrice(info *relaycommon.RelayInfo) {
	info.PriceData.UsePrice = true
	info.PriceData.ModelPrice = timeoutCostPerCall
}

func perCallQuota() int { return int(timeoutCostPerCall * common.QuotaPerUnit) }

// enableConsumeLogs 打开消费日志（测试进程默认值可能关闭），用例结束恢复。
func enableConsumeLogs(t *testing.T) {
	t.Helper()
	prev := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = prev })
}

// logsFor 刷出异步日志管道后读取该用户某类日志。
func logsFor(t *testing.T, userID int, logType int) []model.Log {
	t.Helper()
	drainRelayLogs()
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?", userID, logType).Order("id").Find(&logs).Error)
	return logs
}

func adminTimeoutAbsorbed(t *testing.T, other string) map[string]any {
	t.Helper()
	m, err := common.StrToMap(other)
	require.NoError(t, err)
	adminInfo, _ := m["admin_info"].(map[string]any)
	require.NotNil(t, adminInfo, "admin_info must exist: %s", other)
	absorbed, _ := adminInfo["timeout_absorbed"].(map[string]any)
	return absorbed
}

func TestNonStreamTimeoutBillingAcceptsInput(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"input", NonStreamTimeoutBillingInput},
		{" INPUT ", NonStreamTimeoutBillingInput},
		{"charge", NonStreamTimeoutBillingCharge},
		{"refund", NonStreamTimeoutBillingRefund},
		{"", NonStreamTimeoutBillingRefund},
		{"bogus", NonStreamTimeoutBillingRefund},
	} {
		assert.Equal(t, tc.want, NormalizeNonStreamTimeoutBilling(tc.in), tc.in)
	}
	for _, ok := range []string{"", "refund", "charge", "input", "Input"} {
		assert.NoError(t, ValidateNonStreamTimeoutBilling(ok), ok)
	}
	assert.Error(t, ValidateNonStreamTimeoutBilling("inputs"))
	assert.Error(t, ValidateNonStreamTimeoutBilling("free"))
}

// TestStreamTimeoutSettlementByMode 我方超时且无有效交付的流按档位选择计费来源；按次计费模型在 input 档退款。
func TestStreamTimeoutSettlementByMode(t *testing.T) {
	for _, tc := range []struct {
		name, mode         string
		perCall, confirmed bool
		source             string
		inputOnly          bool
	}{
		{"refund", NonStreamTimeoutBillingRefund, false, true, "", false},
		{"default", "", false, false, "", false},
		{"charge confirmed", NonStreamTimeoutBillingCharge, false, true, "upstream", false},
		{"charge estimated", NonStreamTimeoutBillingCharge, false, false, "estimated", false},
		{"charge per call", NonStreamTimeoutBillingCharge, true, false, "estimated", false},
		{"input confirmed", NonStreamTimeoutBillingInput, false, true, "upstream", true},
		{"input estimated", NonStreamTimeoutBillingInput, false, false, "estimated", true},
		{"input per call refunds", NonStreamTimeoutBillingInput, true, true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info, _ := nonStreamTimeoutContext(t, tc.mode, 0)
			if tc.perCall {
				usePerCallPrice(info)
			}
			source, inputOnly := StreamTimeoutSettlement(c, info, tc.confirmed)
			assert.Equal(t, tc.source, source)
			assert.Equal(t, tc.inputOnly, inputOnly)
		})
	}
}

// TestSettleRelayTimeoutInputNonStream 非流式、未转流、我方超时：input 与 charge 档只按估算输入结算
// （输出 0），消费日志写我方「只收输入」文案并带 timeout_absorbed；refund 档与按次计费的 input 档交给退款。
func TestSettleRelayTimeoutInputNonStream(t *testing.T) {
	for i, tc := range []struct {
		name, mode string
		perCall    bool
		handled    bool
	}{
		{"input", NonStreamTimeoutBillingInput, false, true},
		{"charge unadapted", NonStreamTimeoutBillingCharge, false, true},
		{"refund", NonStreamTimeoutBillingRefund, false, false},
		{"input per call", NonStreamTimeoutBillingInput, true, false},
		{"charge per call", NonStreamTimeoutBillingCharge, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := timeoutCostUserBase + i
			c, info, fixture := nonStreamTimeoutContext(t, tc.mode, userID)
			if tc.perCall {
				usePerCallPrice(info)
			}
			require.Equal(t, tc.handled, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
			logs := logsFor(t, userID, model.LogTypeConsume)
			if !tc.handled {
				assert.Zero(t, fixture.settles, "refund is the controller's job")
				assert.Empty(t, logs)
				return
			}
			want := timeoutCostPrompt * timeoutCostRatio
			assert.Equal(t, 1, fixture.settles)
			assert.Equal(t, want, fixture.actual)
			assert.Zero(t, fixture.refunds)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			assert.Equal(t, timeoutCostPrompt, logs[0].PromptTokens)
			assert.Zero(t, logs[0].CompletionTokens)
			assert.Contains(t, logs[0].Content, "The request exceeded the configured 7 second time limit; charged for input only.")
			assert.Contains(t, logs[0].Content, "req-timeout-cost")
			absorbed := adminTimeoutAbsorbed(t, logs[0].Other)
			require.NotNil(t, absorbed)
			assert.Equal(t, tc.mode, absorbed["mode"])
			assert.Equal(t, "non_stream", absorbed["kind"])
			assert.EqualValues(t, timeoutCostPrompt, absorbed["input_tokens"])
			assert.Equal(t, true, absorbed["input_estimated"])
			assert.EqualValues(t, 0, absorbed["received_output_tokens"])
			assert.EqualValues(t, 0, absorbed["absorbed_quota_min"], "the output of a non-stream request is not observable")
		})
	}
}

// TestSettleRelayTimeoutInputOnlyForOwnTimeout 只有我方超时触发：上游错误、客户端断开、流式、转流都不结算。
func TestSettleRelayTimeoutInputOnlyForOwnTimeout(t *testing.T) {
	upstream := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	for _, tc := range []struct {
		name  string
		setup func(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError
	}{
		{"nil error", func(*gin.Context, *relaycommon.RelayInfo) *types.NewAPIError { return nil }},
		{"upstream 500", func(*gin.Context, *relaycommon.RelayInfo) *types.NewAPIError { return upstream }},
		{"client disconnect (no own timeout)", func(c *gin.Context, _ *relaycommon.RelayInfo) *types.NewAPIError {
			c.Set(string(constant.ContextKeyRelayTimeoutControl), nil)
			return timeoutCostErr()
		}},
		{"stream request", func(c *gin.Context, _ *relaycommon.RelayInfo) *types.NewAPIError {
			common.SetContextKey(c, constant.ContextKeyIsStream, true)
			return timeoutCostErr()
		}},
		{"adapted, settled by its buffered handler", func(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
			info.UpstreamStreamAdapted = true
			markRelaySettlementDone(c)
			return timeoutCostErr()
		}},
		{"response already written", func(c *gin.Context, _ *relaycommon.RelayInfo) *types.NewAPIError {
			c.Writer.WriteHeaderNow()
			return timeoutCostErr()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, 0)
			apiErr := tc.setup(c, info)
			assert.False(t, SettleRelayTimeoutInputIfNeeded(c, info, apiErr))
			assert.Zero(t, fixture.settles)
		})
	}
}

// TestSettleRelayTimeoutInputFallsBackToRefund 结算失败且资金未提交时回退为退款、不写消费日志；
// 资金已提交（令牌调整失败）时按已收取保留消费日志。
func TestSettleRelayTimeoutInputFallsBackToRefund(t *testing.T) {
	for i, tc := range []struct {
		name      string
		committed bool
		refunds   int
		logs      int
	}{
		{"funding failed", false, 1, 0},
		{"funding committed", true, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := timeoutCostUserBase + 20 + i
			c, info, fixture := nonStreamTimeoutContext(t, NonStreamTimeoutBillingInput, userID)
			fixture.err = errors.New("settle failed")
			fixture.committed = tc.committed
			require.True(t, SettleRelayTimeoutInputIfNeeded(c, info, timeoutCostErr()))
			assert.Equal(t, tc.refunds, fixture.refunds)
			assert.Len(t, logsFor(t, userID, model.LogTypeConsume), tc.logs)
		})
	}
}

// TestRelayTimeoutErrorLogRecordsAbsorbed 非流式由平台承担时，渠道错误日志的 admin_info 带 timeout_absorbed：
// refund 档记输入费用，按次计费的 input 档记整次价格；会按输入结算的档位不在错误日志重复记录。
func TestRelayTimeoutErrorLogRecordsAbsorbed(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		perCall    bool
		want       int // -1 表示不记录
	}{
		{"refund", NonStreamTimeoutBillingRefund, false, timeoutCostPrompt * timeoutCostRatio},
		{"refund per call", NonStreamTimeoutBillingRefund, true, perCallQuota()},
		{"input per call", NonStreamTimeoutBillingInput, true, perCallQuota()},
		{"charge per call", NonStreamTimeoutBillingCharge, true, perCallQuota()},
		{"input settles instead", NonStreamTimeoutBillingInput, false, -1},
		{"charge settles instead", NonStreamTimeoutBillingCharge, false, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info, _ := nonStreamTimeoutContext(t, tc.mode, 0)
			if tc.perCall {
				usePerCallPrice(info)
			}
			BindRelayTimeoutInfo(c, info)
			other := model.NewLogOther()
			other.SetAdmin("use_channel", []string{"1"})
			AppendStreamErrorDiagnostic(c, other, timeoutCostErr())
			adminInfo := logOtherAdmin(other)
			if tc.want < 0 {
				assert.NotContains(t, adminInfo, "timeout_absorbed")
				return
			}
			absorbed, ok := adminInfo["timeout_absorbed"].(map[string]interface{})
			require.True(t, ok, "%v", adminInfo)
			assert.Equal(t, tc.mode, absorbed["mode"])
			assert.Equal(t, "non_stream", absorbed["kind"])
			assert.Equal(t, timeoutCostPrompt, absorbed["input_tokens"])
			assert.Equal(t, true, absorbed["input_estimated"])
			assert.Equal(t, 0, absorbed["received_output_tokens"])
			assert.Equal(t, tc.want, absorbed["absorbed_quota_min"])
			assert.Equal(t, tc.perCall, absorbed["per_call"] == true)
		})
	}
}

// TestRelayTimeoutErrorLogSkipsOtherErrors 非超时错误、已写出响应、未绑定请求都不记录。
func TestRelayTimeoutErrorLogSkipsOtherErrors(t *testing.T) {
	upstream := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	for _, tc := range []struct {
		name  string
		setup func(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError
	}{
		{"upstream error", func(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
			BindRelayTimeoutInfo(c, info)
			return upstream
		}},
		{"written response", func(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
			BindRelayTimeoutInfo(c, info)
			c.Writer.WriteHeaderNow()
			return timeoutCostErr()
		}},
		{"not bound", func(*gin.Context, *relaycommon.RelayInfo) *types.NewAPIError { return timeoutCostErr() }},
		{"adapted, settled by its buffered handler", func(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
			BindRelayTimeoutInfo(c, info)
			info.UpstreamStreamAdapted = true
			markRelaySettlementDone(c)
			return timeoutCostErr()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info, _ := nonStreamTimeoutContext(t, NonStreamTimeoutBillingRefund, 0)
			apiErr := tc.setup(c, info)
			other := model.NewLogOther()
			AppendStreamErrorDiagnostic(c, other, apiErr)
			assert.NotContains(t, logOtherAdmin(other), "timeout_absorbed")
		})
	}
}

// openAIUsageFrame 是上游在内容前单独报告的用量帧（含缓存命中）。
const openAIUsageFrame = `{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":60,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":40}}}`

// openAIContentFrame 是一段已接收（未交付）的正文。
const openAIContentFrame = `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"The quick brown fox jumps over the lazy dog again and again and again."}}]}`

// streamTimeoutCase 在已取得上游 200 的受管流上模拟我方时限到期。
func streamTimeoutCase(t *testing.T, mode string, frames ...string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	require.NoError(t, i18n.Init())
	enableConsumeLogs(t)
	c, _, info, cancel := partialBillingInfo(t, types.RelayFormatOpenAI)
	info.OriginModelName = timeoutCostModel
	info.PriceData.ModelRatio = 1
	info.PriceData.CompletionRatio = 1
	info.PriceData.CacheRatio = 0.1
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, mode)
	for _, frame := range frames {
		require.NoError(t, info.StreamSession.ObserveEvent("", []byte(frame)))
	}
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &streamTerminalControl{})
	common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, timeoutCostSeconds)
	cancel()
	return c, info
}

func streamAbsorbed(c *gin.Context) map[string]any {
	cost := relayTimeoutCostFrom(c)
	if cost == nil {
		return nil
	}
	return cost.absorbed
}

// TestStreamOwnTimeoutInputOnly 流式、我方超时、无有效交付：input 档按确认输入（否则估算输入）结算，输出 0；
// 上游确认的缓存按倍率计入，估算不含缓存；平台承担已接收输出的费用。
func TestStreamOwnTimeoutInputOnly(t *testing.T) {
	t.Run("confirmed claude input", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingInput, claudeStartOne)
		u := effectiveBillingUsage(FinalizeStreamUsage(c, info, nil))
		assert.Equal(t, "upstream", info.StreamResult.UsageSource)
		assert.Equal(t, 50, u.PromptTokens)
		assert.Zero(t, u.CompletionTokens, "message_start's output count is not charged")
		absorbed := streamAbsorbed(c)
		require.NotNil(t, absorbed)
		assert.Equal(t, NonStreamTimeoutBillingInput, absorbed["mode"])
		assert.Equal(t, "stream", absorbed["kind"])
		assert.Equal(t, 50, absorbed["input_tokens"])
		assert.Equal(t, false, absorbed["input_estimated"])
		assert.Equal(t, 0, absorbed["absorbed_quota_min"])
		assert.True(t, relayTimeoutCostFrom(c).inputOnly)
	})
	t.Run("confirmed input with cache", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingInput, openAIUsageFrame)
		u := effectiveBillingUsage(FinalizeStreamUsage(c, info, nil))
		assert.Equal(t, 60, u.PromptTokens)
		assert.Equal(t, 40, u.PromptTokensDetails.CachedTokens)
		assert.Zero(t, u.CompletionTokens)
		summary := calculateTextQuotaSummary(c, info, u)
		assert.Equal(t, 24, summary.Quota, "20 uncached + 40 cached × 0.1")
	})
	t.Run("estimated input, received output absorbed", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingInput, openAIContentFrame)
		received := info.StreamSession.Snapshot().ReceivedOutput
		require.Positive(t, received)
		u := effectiveBillingUsage(FinalizeStreamUsage(c, info, nil))
		assert.Equal(t, "estimated", info.StreamResult.UsageSource)
		assert.False(t, info.StreamResult.EffectiveContent)
		assert.Equal(t, 23, u.PromptTokens)
		assert.Zero(t, u.CompletionTokens)
		assert.Zero(t, u.PromptTokensDetails.CachedTokens, "an estimate never includes cache")
		absorbed := streamAbsorbed(c)
		require.NotNil(t, absorbed)
		assert.Equal(t, 23, absorbed["input_tokens"])
		assert.Equal(t, true, absorbed["input_estimated"])
		assert.Equal(t, received, absorbed["received_output_tokens"])
		assert.Equal(t, received, absorbed["absorbed_quota_min"], "ratio 1: each received output token costs 1")
	})
}

// TestStreamOwnTimeoutRefundAndChargeRecords refund 档不收费并记录输入与已接收输出的费用；
// charge 档不变（确认或估算输入＋已接收输出）且不记录；按次计费的 input 档退款并记录整次价格。
func TestStreamOwnTimeoutRefundAndChargeRecords(t *testing.T) {
	t.Run("refund", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingRefund, openAIContentFrame)
		received := info.StreamSession.Snapshot().ReceivedOutput
		u := FinalizeStreamUsage(c, info, nil)
		assert.Equal(t, "none", info.StreamResult.UsageSource)
		assert.Zero(t, u.TotalTokens)
		absorbed := streamAbsorbed(c)
		require.NotNil(t, absorbed)
		assert.Equal(t, NonStreamTimeoutBillingRefund, absorbed["mode"])
		assert.Equal(t, 23+received, absorbed["absorbed_quota_min"])
		assert.False(t, relayTimeoutCostFrom(c).inputOnly)
	})
	t.Run("refund with confirmed cache", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingRefund, openAIUsageFrame)
		FinalizeStreamUsage(c, info, nil)
		absorbed := streamAbsorbed(c)
		require.NotNil(t, absorbed)
		assert.Equal(t, 60, absorbed["input_tokens"])
		assert.Equal(t, 24, absorbed["absorbed_quota_min"])
	})
	t.Run("charge unchanged", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingCharge, openAIContentFrame)
		received := info.StreamSession.Snapshot().ReceivedOutput
		u := effectiveBillingUsage(FinalizeStreamUsage(c, info, nil))
		assert.Equal(t, "estimated", info.StreamResult.UsageSource)
		assert.Equal(t, 23, u.PromptTokens)
		assert.Equal(t, received, u.CompletionTokens)
		assert.Nil(t, streamAbsorbed(c))
	})
	t.Run("input per call refunds", func(t *testing.T) {
		c, info := streamTimeoutCase(t, NonStreamTimeoutBillingInput, openAIContentFrame)
		usePerCallPrice(info)
		u := FinalizeStreamUsage(c, info, nil)
		assert.Equal(t, "none", info.StreamResult.UsageSource)
		assert.Zero(t, u.TotalTokens)
		absorbed := streamAbsorbed(c)
		require.NotNil(t, absorbed)
		assert.Equal(t, true, absorbed["per_call"])
		assert.Equal(t, perCallQuota(), absorbed["absorbed_quota_min"])
	})
}

// TestStreamOwnTimeoutWithDeliveryUnchanged 已有有效交付的超时流在各档位都按交付结算，不写记录。
func TestStreamOwnTimeoutWithDeliveryUnchanged(t *testing.T) {
	for _, mode := range []string{NonStreamTimeoutBillingRefund, NonStreamTimeoutBillingInput, NonStreamTimeoutBillingCharge} {
		t.Run(mode, func(t *testing.T) {
			c, _, info, cancel := partialBillingInfo(t, types.RelayFormatOpenAI)
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, mode)
			deliver(t, info, openAIContentFrame, 17)
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &streamTerminalControl{})
			cancel()
			u := effectiveBillingUsage(FinalizeStreamUsage(c, info, nil))
			assert.True(t, info.StreamResult.EffectiveContent)
			assert.Equal(t, "estimated", info.StreamResult.UsageSource)
			assert.Equal(t, 23, u.PromptTokens)
			assert.Equal(t, 17, u.CompletionTokens)
			assert.Nil(t, streamAbsorbed(c))
		})
	}
}

// TestStreamOwnTimeoutInputTieredOutputZero 阶梯表达式计费：以输入代入、输出为 0 计算。
func TestStreamOwnTimeoutInputTieredOutputZero(t *testing.T) {
	const expr = `tier("base", p * 2 + c * 10)`
	c, info := streamTimeoutCase(t, NonStreamTimeoutBillingInput, openAIContentFrame)
	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{
		BillingMode:  "tiered_expr",
		ExprString:   expr,
		ExprHash:     billingexpr.ExprHashString(expr),
		GroupRatio:   1,
		QuotaPerUnit: testQuotaPerUnit,
	}
	fixture := &timeoutBillingFixture{pre: 1000}
	info.Billing = fixture
	received := info.StreamSession.Snapshot().ReceivedOutput
	require.Positive(t, received)
	usage := FinalizeStreamUsage(c, info, nil)
	absorbed := streamAbsorbed(c) // read before the settlement log consumes it
	require.NotNil(t, absorbed)
	PostTextConsumeQuota(c, info, usage, nil)
	want := int(float64(23*2) / 1_000_000 * testQuotaPerUnit)
	assert.Equal(t, want, fixture.actual, "output is 0 in the expression")
	withOutput := int(float64(23*2+received*10) / 1_000_000 * testQuotaPerUnit)
	assert.Equal(t, withOutput-want, absorbed["absorbed_quota_min"])
}

// TestStreamOwnTimeoutInputSettlementLog input 档流式结算后的消费日志：我方文案、输出 0、带 timeout_absorbed。
func TestStreamOwnTimeoutInputSettlementLog(t *testing.T) {
	userID := timeoutCostUserBase + 40
	c, info := streamTimeoutCase(t, NonStreamTimeoutBillingInput, claudeStartOne)
	c.Set(common.RequestIdKey, "req-stream-input")
	info.UserId = userID
	info.StartTime = time.Now()
	info.Billing = &timeoutBillingFixture{pre: 1000}
	usage := FinalizeStreamUsage(c, info, nil)
	PostTextConsumeQuota(c, info, usage, nil)
	logs := logsFor(t, userID, model.LogTypeConsume)
	require.Len(t, logs, 1)
	assert.Equal(t, 50, logs[0].PromptTokens)
	assert.Zero(t, logs[0].CompletionTokens)
	assert.Contains(t, logs[0].Content, "charged for input only")
	absorbed := adminTimeoutAbsorbed(t, logs[0].Other)
	require.NotNil(t, absorbed)
	assert.Equal(t, "stream", absorbed["kind"])
	assert.Equal(t, NonStreamTimeoutBillingInput, absorbed["mode"])
}

// TestStreamOwnTimeoutRefundErrorLogRecordsAbsorbed refund 档零收费流写错误日志，admin_info 带记录。
func TestStreamOwnTimeoutRefundErrorLogRecordsAbsorbed(t *testing.T) {
	userID := timeoutCostUserBase + 41
	c, info := streamTimeoutCase(t, NonStreamTimeoutBillingRefund)
	info.UserId = userID
	info.StartTime = time.Now()
	info.Billing = &timeoutBillingFixture{pre: 1000}
	usage := FinalizeStreamUsage(c, info, nil)
	PostTextConsumeQuota(c, info, usage, nil)
	logs := logsFor(t, userID, model.LogTypeError)
	require.Len(t, logs, 1)
	absorbed := adminTimeoutAbsorbed(t, logs[0].Other)
	require.NotNil(t, absorbed)
	assert.Equal(t, NonStreamTimeoutBillingRefund, absorbed["mode"])
	assert.EqualValues(t, 23, absorbed["absorbed_quota_min"])
	assert.Empty(t, logsFor(t, userID, model.LogTypeConsume))
}

// TestRealtimeOwnTimeoutNoRecord Realtime 按轮次计费，不参与超时成本记录。
func TestRealtimeOwnTimeoutNoRecord(t *testing.T) {
	c, _, info, cancel := partialBillingInfo(t, types.RelayFormatOpenAIRealtime)
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, NonStreamTimeoutBillingInput)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &streamTerminalControl{})
	cancel()
	FinalizeStreamUsage(c, info, nil)
	assert.Nil(t, streamAbsorbed(c))
}

var _ relaycommon.BillingSettler = (*timeoutBillingFixture)(nil)
var _ = dto.Usage{}
