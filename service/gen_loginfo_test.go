package service

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loginfoNewCtx returns a bare gin test context safe for the pure log-info
// builders (they only read context keys, never touch DB/Redis/network).
func loginfoNewCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	return c
}

// loginfoBaseRelayInfo builds a RelayInfo with a deterministic 250ms first
// response latency so `frt` is exactly 250.
func loginfoBaseRelayInfo() *relaycommon.RelayInfo {
	start := time.Now()
	return &relaycommon.RelayInfo{
		StartTime:         start,
		FirstResponseTime: start.Add(250 * time.Millisecond),
		// ChannelMeta is an embedded *pointer; promoted fields like IsModelMapped /
		// UpstreamModelName are accessed by the log builders, so it must be non-nil
		// (real relay flow always initializes it).
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
}

// ---------------------------------------------------------------------------
// GenerateTextOtherInfo
// ---------------------------------------------------------------------------

func TestLoginfoGenerateTextOtherInfo_BasicRatios(t *testing.T) {
	c := loginfoNewCtx()
	ri := loginfoBaseRelayInfo()

	other := GenerateTextOtherInfo(c, ri, 2.5, 1.5, 3.0, 42, 0.25, 0.01, 0.9).Snapshot()

	// Core ratio / price fields carried verbatim from the arguments.
	assert.Equal(t, 2.5, other["model_ratio"])
	assert.Equal(t, 1.5, other["group_ratio"])
	assert.Equal(t, 3.0, other["completion_ratio"])
	assert.Equal(t, 42, other["cache_tokens"])
	assert.Equal(t, 0.25, other["cache_ratio"])
	assert.Equal(t, 0.01, other["model_price"])
	assert.Equal(t, 0.9, other["user_group_ratio"])
	assert.Equal(t, float64(250), other["frt"])

	// admin_info always present; use_channel defaults to empty slice.
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok, "admin_info must be a map")
	assert.Empty(t, adminInfo["use_channel"], "use_channel absent -> empty/nil slice")

	// Optional fields absent for the default/zero relay info.
	assert.NotContains(t, other, "reasoning_effort")
	assert.NotContains(t, other, "is_model_mapped")
	assert.NotContains(t, other, "upstream_model_name")
	assert.NotContains(t, other, "is_system_prompt_overwritten")
	assert.NotContains(t, adminInfo, "is_multi_key")
	assert.NotContains(t, adminInfo, "local_count_tokens")
}

// 渠道名快照必须随每条中继日志落库：渠道删除后 channels 表答不出这条日志跑在
// 哪个渠道，佣金明细的「渠道」列就只能靠它。写在 admin_info 下，普通用户读不到。
func TestLoginfoAppendRelayLogAdminInfo_RecordsChannelNameSnapshot(t *testing.T) {
	c := loginfoNewCtx()
	ri := loginfoBaseRelayInfo()
	ri.ChannelName = "azure-east"

	adminInfo, ok := GenerateTextOtherInfo(c, ri, 1, 1, 1, 0, 0, 0, 1).
		Snapshot()["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "azure-east", adminInfo["channel_name"])
}

// relayInfo 尚未构造出来的早期错误日志退回 distributor 写入的上下文键。
func TestLoginfoAppendRelayLogAdminInfo_ChannelNameFromContext(t *testing.T) {
	c := loginfoNewCtx()
	common.SetContextKey(c, constant.ContextKeyChannelName, "ctx-channel")

	other := model.NewLogOther()
	AppendRelayLogAdminInfo(c, nil, other)

	adminInfo, ok := other.Snapshot()["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "ctx-channel", adminInfo["channel_name"])
}

// 取不到渠道名时不写空字段，避免每条日志多出一个无意义的键。
func TestLoginfoAppendRelayLogAdminInfo_OmitsEmptyChannelName(t *testing.T) {
	other := model.NewLogOther()
	AppendRelayLogAdminInfo(loginfoNewCtx(), loginfoBaseRelayInfo(), other)

	adminInfo, ok := other.Snapshot()["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.NotContains(t, adminInfo, "channel_name")
}

func TestLoginfoGenerateTextOtherInfo_DoesNotRecordNegativeFirstResponseTime(t *testing.T) {
	c := loginfoNewCtx()
	start := time.Now()
	ri := loginfoBaseRelayInfo()
	ri.StartTime = start
	ri.FirstResponseTime = start.Add(-time.Second)

	other := GenerateTextOtherInfo(c, ri, 1, 1, 1, 0, 0, 0, 1).Snapshot()

	assert.Equal(t, float64(0), other["frt"])
}

func TestLoginfoGenerateTextOtherInfo_OptionalFields(t *testing.T) {
	c := loginfoNewCtx()
	common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 7)
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)

	ri := loginfoBaseRelayInfo()
	ri.ReasoningEffort = "high"
	// IsModelMapped/UpstreamModelName are promoted from *ChannelMeta; set them on
	// the same ChannelMeta instance so the second assignment doesn't clobber the first.
	ri.ChannelMeta = &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-upstream", IsModelMapped: true}

	other := GenerateTextOtherInfo(c, ri, 1, 1, 1, 0, 0, 0, 1).Snapshot()

	assert.Equal(t, "high", other["reasoning_effort"])
	assert.Equal(t, true, other["is_model_mapped"])
	assert.Equal(t, "gpt-4o-upstream", other["upstream_model_name"])
	assert.Equal(t, true, other["is_system_prompt_overwritten"])

	adminInfo := other["admin_info"].(map[string]interface{})
	assert.Equal(t, true, adminInfo["is_multi_key"])
	assert.Equal(t, 7, adminInfo["multi_key_index"])
	assert.Equal(t, true, adminInfo["local_count_tokens"])
}

// ---------------------------------------------------------------------------
// GenerateClaudeOtherInfo
// ---------------------------------------------------------------------------

func TestLoginfoGenerateClaudeOtherInfo_WithCacheCreationBuckets(t *testing.T) {
	c := loginfoNewCtx()
	ri := loginfoBaseRelayInfo()

	other := GenerateClaudeOtherInfo(c, ri, 2, 1, 3,
		10, 0.5, // cacheTokens, cacheRatio
		20, 1.25, // cacheCreation
		5, 1.1, // 5m
		8, 2.2, // 1h
		0.02, 0.8).Snapshot()

	assert.Equal(t, true, other["claude"])
	// Inherited from the text builder.
	assert.Equal(t, 10, other["cache_tokens"])
	assert.Equal(t, 0.5, other["cache_ratio"])
	// Claude-specific.
	assert.Equal(t, 20, other["cache_creation_tokens"])
	assert.Equal(t, 1.25, other["cache_creation_ratio"])
	assert.Equal(t, 5, other["cache_creation_tokens_5m"])
	assert.Equal(t, 1.1, other["cache_creation_ratio_5m"])
	assert.Equal(t, 8, other["cache_creation_tokens_1h"])
	assert.Equal(t, 2.2, other["cache_creation_ratio_1h"])
}

func TestLoginfoGenerateClaudeOtherInfo_ZeroCacheBucketsOmitted(t *testing.T) {
	c := loginfoNewCtx()
	ri := loginfoBaseRelayInfo()

	other := GenerateClaudeOtherInfo(c, ri, 2, 1, 3,
		0, 0, // cacheTokens, cacheRatio
		20, 1.25, // cacheCreation always present
		0, 9.9, // 5m tokens == 0 -> both 5m keys omitted
		0, 9.9, // 1h tokens == 0 -> both 1h keys omitted
		0.02, 0.8).Snapshot()

	assert.Equal(t, true, other["claude"])
	assert.Equal(t, 20, other["cache_creation_tokens"])
	assert.Equal(t, 1.25, other["cache_creation_ratio"])
	assert.NotContains(t, other, "cache_creation_tokens_5m")
	assert.NotContains(t, other, "cache_creation_ratio_5m")
	assert.NotContains(t, other, "cache_creation_tokens_1h")
	assert.NotContains(t, other, "cache_creation_ratio_1h")
}

// ---------------------------------------------------------------------------
// GenerateAudioOtherInfo
// ---------------------------------------------------------------------------

func TestLoginfoGenerateAudioOtherInfo(t *testing.T) {
	c := loginfoNewCtx()
	ri := loginfoBaseRelayInfo()

	usage := &dto.Usage{}
	usage.PromptTokensDetails.AudioTokens = 11
	usage.PromptTokensDetails.TextTokens = 22
	usage.CompletionTokenDetails.AudioTokens = 33
	usage.CompletionTokenDetails.TextTokens = 44

	other := GenerateAudioOtherInfo(c, ri, usage, 2, 1, 3, 4.0, 5.0, 0.02, 0.8).Snapshot()

	assert.Equal(t, true, other["audio"])
	assert.Equal(t, 11, other["audio_input"])
	assert.Equal(t, 33, other["audio_output"])
	assert.Equal(t, 22, other["text_input"])
	assert.Equal(t, 44, other["text_output"])
	assert.Equal(t, 4.0, other["audio_ratio"])
	assert.Equal(t, 5.0, other["audio_completion_ratio"])
	// cache_tokens forced to 0 by the audio wrapper.
	assert.Equal(t, 0, other["cache_tokens"])
}

// ---------------------------------------------------------------------------
// GenerateWssOtherInfo
// ---------------------------------------------------------------------------

func TestLoginfoGenerateWssOtherInfo(t *testing.T) {
	c := loginfoNewCtx()
	ri := loginfoBaseRelayInfo()

	usage := &dto.RealtimeUsage{}
	usage.InputTokenDetails.AudioTokens = 100
	usage.InputTokenDetails.TextTokens = 200
	usage.OutputTokenDetails.AudioTokens = 300
	usage.OutputTokenDetails.TextTokens = 400

	other := GenerateWssOtherInfo(c, ri, usage, 2, 1, 3, 6.0, 7.0, 0.02, 0.8).Snapshot()

	assert.Equal(t, true, other["ws"])
	assert.Equal(t, 100, other["audio_input"])
	assert.Equal(t, 300, other["audio_output"])
	assert.Equal(t, 200, other["text_input"])
	assert.Equal(t, 400, other["text_output"])
	assert.Equal(t, 6.0, other["audio_ratio"])
	assert.Equal(t, 7.0, other["audio_completion_ratio"])
	assert.Equal(t, 0, other["cache_tokens"])
}

// ---------------------------------------------------------------------------
// GenerateMjOtherInfo
// ---------------------------------------------------------------------------

func TestLoginfoGenerateMjOtherInfo_WithSpecialRatio(t *testing.T) {
	ri := &relaycommon.RelayInfo{RequestURLPath: "/mj/submit/imagine?foo=bar"}
	price := hosttypes.PriceData{
		ModelPrice: 0.5,
		GroupRatioInfo: hosttypes.GroupRatioInfo{
			GroupRatio:        1.5,
			GroupSpecialRatio: 0.7,
			HasSpecialRatio:   true,
		},
	}

	other := GenerateMjOtherInfo(ri, price).Snapshot()

	assert.Equal(t, 0.5, other["model_price"])
	assert.Equal(t, 1.5, other["group_ratio"])
	assert.Equal(t, 0.7, other["user_group_ratio"])
	// request_path taken from relayInfo (query stripped).
	assert.Equal(t, "/mj/submit/imagine", other["request_path"])
}

func TestLoginfoGenerateMjOtherInfo_NoSpecialRatio(t *testing.T) {
	ri := &relaycommon.RelayInfo{}
	price := hosttypes.PriceData{
		ModelPrice:     0.5,
		GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1.5, HasSpecialRatio: false},
	}

	other := GenerateMjOtherInfo(ri, price).Snapshot()

	assert.Equal(t, 1.5, other["group_ratio"])
	assert.NotContains(t, other, "user_group_ratio")
	assert.NotContains(t, other, "request_path")
}

// ---------------------------------------------------------------------------
// attachQuotaSaturationToOther
// ---------------------------------------------------------------------------

func TestLoginfoAttachQuotaSaturationToOther(t *testing.T) {
	clamp := &common.QuotaClamp{
		Op:       "QuotaFromFloat",
		Kind:     common.QuotaClampOverflow,
		Original: 1e12,
		Clamped:  common.MaxQuota,
	}

	t.Run("nil clamp is a no-op", func(t *testing.T) {
		o := model.NewLogOther()
		attachQuotaSaturationToOther(o, nil)
		other := o.Snapshot()
		assert.NotContains(t, other, "admin_info")
	})

	t.Run("nil other does not panic", func(t *testing.T) {
		assert.NotPanics(t, func() { attachQuotaSaturationToOther(nil, clamp) })
	})

	t.Run("creates admin_info when absent", func(t *testing.T) {
		o := model.NewLogOther()
		attachQuotaSaturationToOther(o, clamp)
		other := o.Snapshot()
		adminInfo, ok := other["admin_info"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, clamp.AuditMap(), adminInfo["quota_saturation"])
	})

	t.Run("preserves existing admin_info keys", func(t *testing.T) {
		o := model.NewLogOther()
		o.SetAdmin("existing", 1)
		attachQuotaSaturationToOther(o, clamp)
		other := o.Snapshot()
		adminInfo := other["admin_info"].(map[string]interface{})
		assert.Equal(t, 1, adminInfo["existing"])
		assert.Equal(t, clamp.AuditMap(), adminInfo["quota_saturation"])
	})
}

// ---------------------------------------------------------------------------
// attachQuotaSaturation
// ---------------------------------------------------------------------------

func TestLoginfoAttachQuotaSaturation(t *testing.T) {
	c := loginfoNewCtx()
	clamp := &common.QuotaClamp{Op: "QuotaRound", Kind: common.QuotaClampNaN, Clamped: 0}

	t.Run("nil relayInfo is a no-op", func(t *testing.T) {
		o := model.NewLogOther()
		attachQuotaSaturation(c, nil, o)
		other := o.Snapshot()
		assert.NotContains(t, other, "admin_info")
	})

	t.Run("nil clamp is a no-op", func(t *testing.T) {
		o := model.NewLogOther()
		attachQuotaSaturation(c, &relaycommon.RelayInfo{}, o)
		other := o.Snapshot()
		assert.NotContains(t, other, "admin_info")
	})

	t.Run("attaches clamp when present", func(t *testing.T) {
		o := model.NewLogOther()
		ri := &relaycommon.RelayInfo{QuotaClamp: clamp, UserId: 5, OriginModelName: "m"}
		attachQuotaSaturation(c, ri, o)
		other := o.Snapshot()
		adminInfo := other["admin_info"].(map[string]interface{})
		assert.Equal(t, clamp.AuditMap(), adminInfo["quota_saturation"])
	})
}

// ---------------------------------------------------------------------------
// appendRequestPath
// ---------------------------------------------------------------------------

func TestLoginfoAppendRequestPath(t *testing.T) {
	t.Run("prefers gin request URL path", func(t *testing.T) {
		c := loginfoNewCtx()
		req := loginfoRequestWithPath(t, "/v1/chat/completions")
		c.Request = req
		o := model.NewLogOther()
		appendRequestPath(c, &relaycommon.RelayInfo{RequestURLPath: "/ignored"}, o)
		other := o.Snapshot()
		assert.Equal(t, "/v1/chat/completions", other["request_path"])
	})

	t.Run("falls back to relayInfo path and strips query", func(t *testing.T) {
		o := model.NewLogOther()
		appendRequestPath(nil, &relaycommon.RelayInfo{RequestURLPath: "/v1/messages?beta=true"}, o)
		other := o.Snapshot()
		assert.Equal(t, "/v1/messages", other["request_path"])
	})

	t.Run("nil other is a no-op", func(t *testing.T) {
		assert.NotPanics(t, func() {
			appendRequestPath(nil, &relaycommon.RelayInfo{RequestURLPath: "/x"}, nil)
		})
	})

	t.Run("no path available leaves key absent", func(t *testing.T) {
		o := model.NewLogOther()
		appendRequestPath(nil, &relaycommon.RelayInfo{}, o)
		other := o.Snapshot()
		assert.NotContains(t, other, "request_path")
	})
}

// ---------------------------------------------------------------------------
// appendBillingInfo
// ---------------------------------------------------------------------------

func TestLoginfoAppendBillingInfo_Wallet(t *testing.T) {
	o := model.NewLogOther()
	ri := &relaycommon.RelayInfo{BillingSource: "wallet"}
	appendBillingInfo(ri, o)
	other := o.Snapshot()

	assert.Equal(t, "wallet", other["billing_source"])
	// Subscription-only keys absent.
	assert.NotContains(t, other, "subscription_total")
	assert.NotContains(t, other, "wallet_quota_deducted")
}

func TestLoginfoAppendBillingInfo_Subscription(t *testing.T) {
	o := model.NewLogOther()
	ri := &relaycommon.RelayInfo{
		BillingSource:                         "subscription",
		SubscriptionId:                        9,
		SubscriptionPreConsumed:               30,
		SubscriptionPostDelta:                 -5,
		SubscriptionPlanId:                    2,
		SubscriptionPlanTitle:                 "Pro",
		SubscriptionAmountTotal:               100,
		SubscriptionAmountUsedAfterPreConsume: 40,
	}
	appendBillingInfo(ri, o)
	other := o.Snapshot()

	assert.Equal(t, "subscription", other["billing_source"])
	assert.Equal(t, 9, other["subscription_id"])
	assert.Equal(t, int64(30), other["subscription_pre_consumed"])
	assert.Equal(t, int64(-5), other["subscription_post_delta"])
	assert.Equal(t, 2, other["subscription_plan_id"])
	assert.Equal(t, "Pro", other["subscription_plan_title"])
	// usedFinal = 40 + (-5) = 35 ; remain = 100 - 35 = 65
	assert.Equal(t, int64(100), other["subscription_total"])
	assert.Equal(t, int64(35), other["subscription_used"])
	assert.Equal(t, int64(65), other["subscription_remain"])
	// consumed = 30 + (-5) = 25
	assert.Equal(t, int64(25), other["subscription_consumed"])
	assert.Equal(t, 0, other["wallet_quota_deducted"])
}

func TestLoginfoAppendBillingInfo_SubscriptionClampsNegative(t *testing.T) {
	o := model.NewLogOther()
	ri := &relaycommon.RelayInfo{
		BillingSource:                         "subscription",
		SubscriptionPreConsumed:               1,
		SubscriptionPostDelta:                 -50, // drives consumed & used negative -> clamped to 0
		SubscriptionAmountTotal:               10,
		SubscriptionAmountUsedAfterPreConsume: 5,
	}
	appendBillingInfo(ri, o)
	other := o.Snapshot()

	// usedFinal = 5 + (-50) = -45 -> 0 ; remain = 10 - 0 = 10
	assert.Equal(t, int64(0), other["subscription_used"])
	assert.Equal(t, int64(10), other["subscription_remain"])
	// consumed = 1 + (-50) = -49 -> 0, so subscription_consumed omitted (not > 0)
	assert.NotContains(t, other, "subscription_consumed")
}

// ---------------------------------------------------------------------------
// appendStreamStatus
// ---------------------------------------------------------------------------

func TestLoginfoAppendStreamStatus_OK(t *testing.T) {
	o := model.NewLogOther()
	ss := relaycommon.NewStreamStatus()
	ss.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	ri := &relaycommon.RelayInfo{IsStream: true, StreamStatus: ss}

	appendStreamStatus(ri, o)
	other := o.Snapshot()

	info, ok := other["stream_status"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "ok", info["status"])
	assert.Equal(t, "done", info["end_reason"])
	assert.NotContains(t, info, "error_count")
}

func TestLoginfoAppendStreamStatus_Error(t *testing.T) {
	o := model.NewLogOther()
	ss := relaycommon.NewStreamStatus()
	ss.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
	ss.RecordError("boom")
	ri := &relaycommon.RelayInfo{IsStream: true, StreamStatus: ss}

	appendStreamStatus(ri, o)
	other := o.Snapshot()

	info := other["stream_status"].(map[string]interface{})
	assert.Equal(t, "error", info["status"])
	assert.Equal(t, "timeout", info["end_reason"])
	assert.Equal(t, 1, info["error_count"])
	assert.Equal(t, []string{"boom"}, info["errors"])
}

func TestLoginfoAppendStreamStatus_NotStreamNoop(t *testing.T) {
	o := model.NewLogOther()
	ss := relaycommon.NewStreamStatus()
	ss.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	// IsStream false -> skipped entirely.
	appendStreamStatus(&relaycommon.RelayInfo{IsStream: false, StreamStatus: ss}, o)
	other := o.Snapshot()
	assert.NotContains(t, other, "stream_status")
}

// ---------------------------------------------------------------------------
// appendRequestConversionChain
// ---------------------------------------------------------------------------

func TestLoginfoAppendRequestConversionChain(t *testing.T) {
	o := model.NewLogOther()
	ri := &relaycommon.RelayInfo{
		RequestConversionChain: []types.RelayFormat{
			types.RelayFormatOpenAI,
			types.RelayFormatClaude,
			types.RelayFormatGemini,
			types.RelayFormatOpenAIResponses,
		},
	}
	appendRequestConversionChain(ri, o)
	other := o.Snapshot()

	assert.Equal(t, []string{
		"OpenAI Compatible",
		"Claude Messages",
		"Google Gemini",
		"OpenAI Responses",
	}, other["request_conversion"])
}

func TestLoginfoAppendRequestConversionChain_EmptyOmitted(t *testing.T) {
	o := model.NewLogOther()
	appendRequestConversionChain(&relaycommon.RelayInfo{}, o)
	other := o.Snapshot()
	assert.NotContains(t, other, "request_conversion")
}

// ---------------------------------------------------------------------------
// appendFinalRequestFormat
// ---------------------------------------------------------------------------

func TestLoginfoAppendFinalRequestFormat(t *testing.T) {
	t.Run("claude final format sets claude=true", func(t *testing.T) {
		o := model.NewLogOther()
		ri := &relaycommon.RelayInfo{FinalRequestRelayFormat: types.RelayFormatClaude}
		appendFinalRequestFormat(ri, o)
		other := o.Snapshot()
		assert.Equal(t, true, other["claude"])
	})

	t.Run("non-claude final format leaves key absent", func(t *testing.T) {
		o := model.NewLogOther()
		ri := &relaycommon.RelayInfo{FinalRequestRelayFormat: types.RelayFormatOpenAI}
		appendFinalRequestFormat(ri, o)
		other := o.Snapshot()
		assert.NotContains(t, other, "claude")
	})
}

// ---------------------------------------------------------------------------
// InjectTieredBillingInfo
// ---------------------------------------------------------------------------

func TestLoginfoInjectTieredBillingInfo(t *testing.T) {
	snap := &billingexpr.BillingSnapshot{ExprString: `tier("base", p*3+c*15)`}

	t.Run("nil relayInfo is a no-op", func(t *testing.T) {
		o := model.NewLogOther()
		InjectTieredBillingInfo(o, nil, nil)
		other := o.Snapshot()
		assert.NotContains(t, other, "billing_mode")
	})

	t.Run("nil snapshot is a no-op", func(t *testing.T) {
		o := model.NewLogOther()
		InjectTieredBillingInfo(o, &relaycommon.RelayInfo{}, nil)
		other := o.Snapshot()
		assert.NotContains(t, other, "billing_mode")
	})

	t.Run("snapshot without result omits matched_tier", func(t *testing.T) {
		o := model.NewLogOther()
		ri := &relaycommon.RelayInfo{TieredBillingSnapshot: snap}
		InjectTieredBillingInfo(o, ri, nil)
		other := o.Snapshot()
		assert.Equal(t, "tiered_expr", other["billing_mode"])
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(snap.ExprString)), other["expr_b64"])
		assert.NotContains(t, other, "matched_tier")
	})

	t.Run("snapshot with result includes matched_tier", func(t *testing.T) {
		o := model.NewLogOther()
		ri := &relaycommon.RelayInfo{TieredBillingSnapshot: snap}
		InjectTieredBillingInfo(o, ri, &billingexpr.TieredResult{MatchedTier: "base"})
		other := o.Snapshot()
		assert.Equal(t, "base", other["matched_tier"])
	})
}

// ---------------------------------------------------------------------------
// notify-limit.go — pure / in-memory logic only.
// SKIPPED (touch Redis or background goroutine timing):
//   - checkRedisLimit         : reads/writes Redis (common.RedisGet/Set/Incr)
//   - startCleanupTask        : spawns an hourly background goroutine
//   - CheckNotificationLimit's Redis branch (common.RedisEnabled == true)
// checkMemoryLimit is exercised indirectly through CheckNotificationLimit with
// RedisEnabled forced false.
// ---------------------------------------------------------------------------

func TestLoginfoGetDuration(t *testing.T) {
	orig := constant.NotificationLimitDurationMinute
	defer func() { constant.NotificationLimitDurationMinute = orig }()

	constant.NotificationLimitDurationMinute = 10
	assert.Equal(t, 10*time.Minute, getDuration())

	// Boundary: zero minutes -> zero duration.
	constant.NotificationLimitDurationMinute = 0
	assert.Equal(t, time.Duration(0), getDuration())
}

func TestLoginfoCheckNotificationLimit_MemoryPath(t *testing.T) {
	origRedis := common.RedisEnabled
	origCount := constant.NotifyLimitCount
	origMinute := constant.NotificationLimitDurationMinute
	defer func() {
		common.RedisEnabled = origRedis
		constant.NotifyLimitCount = origCount
		constant.NotificationLimitDurationMinute = origMinute
	}()

	common.RedisEnabled = false
	constant.NotificationLimitDurationMinute = 10

	t.Run("limit=2 allows exactly two then blocks", func(t *testing.T) {
		constant.NotifyLimitCount = 2
		// Unique userId keeps this independent of the package-global store.
		const userId = 910001
		ok, err := CheckNotificationLimit(userId, "loginfo_a")
		require.NoError(t, err)
		assert.True(t, ok, "1st call within limit")

		ok, err = CheckNotificationLimit(userId, "loginfo_a")
		require.NoError(t, err)
		assert.True(t, ok, "2nd call at limit boundary")

		ok, err = CheckNotificationLimit(userId, "loginfo_a")
		require.NoError(t, err)
		assert.False(t, ok, "3rd call exceeds limit")
	})

	t.Run("limit=1 allows one then blocks", func(t *testing.T) {
		constant.NotifyLimitCount = 1
		const userId = 910002
		ok, err := CheckNotificationLimit(userId, "loginfo_b")
		require.NoError(t, err)
		assert.True(t, ok)

		ok, err = CheckNotificationLimit(userId, "loginfo_b")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("limit=0 blocks immediately", func(t *testing.T) {
		constant.NotifyLimitCount = 0
		const userId = 910003
		ok, err := CheckNotificationLimit(userId, "loginfo_c")
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

// loginfoRequestWithPath builds a minimal *http.Request whose URL path is set,
// for exercising appendRequestPath's gin-context branch.
func loginfoRequestWithPath(t *testing.T, path string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodPost, path, nil)
}
