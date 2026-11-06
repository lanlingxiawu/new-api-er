package service

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Adapter tests separately prove that complete vendor responses produce these
// usages. This boundary exercises real funding, counters and logs, without an
// entire relay-route E2E or a call to a paid model.
func TestForkNormalBillingDatabaseMatrix(t *testing.T) {
	runBillingDatabaseMatrix(t, runForkNormalBillingCases)
}

func runForkNormalBillingCases(t *testing.T, db, logDB *gorm.DB) {
	t.Helper()
	const initialQuota = 2_000_000
	const tokenExpr = `tier("standard", p * 2 + c * 10 + cr * 0.2 + cc * 2.5 + cc1h * 4 + cc12h * 5)`
	const fixedExpr = `tier("request", fixed(0.02))`
	operation_setting.SetToolPriceForTest("fork_normal_priced_tool", 6)
	t.Cleanup(func() { operation_setting.DeleteToolPriceForTest("fork_normal_priced_tool") })
	require.Equal(t, float64(500000), common.QuotaPerUnit)
	plain := dto.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}
	cache := plain
	cache.PromptTokensDetails.CachedTokens = 80
	cache.CompletionTokenDetails.ReasoningTokens = 7 // Included in output; never add twice.
	claude := dto.Usage{UsageSemantic: "anthropic", PromptTokens: 2, CompletionTokens: 8, TotalTokens: 10,
		PromptTokensDetails:         dto.InputTokenDetails{CachedTokens: 1000, CachedCreationTokens: 20},
		ClaudeCacheCreation5mTokens: 12, ClaudeCacheCreation1hTokens: 8}
	write := dto.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 40, CacheWriteTokens: 30,
			CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{TTL30mTokens: 20, TTL12hTokens: 10}}}
	gemini := dto.Usage{PromptTokens: 130, CompletionTokens: 27, TotalTokens: 157,
		PromptTokensDetails:    dto.InputTokenDetails{CachedTokens: 40, AudioTokens: 40},
		CompletionTokenDetails: dto.OutputTokenDetails{AudioTokens: 8, ReasoningTokens: 7}}
	for _, tc := range []struct {
		name, expr         string
		usage              dto.Usage
		want               int
		fixed, tool, image bool
	}{
		// Each expected charge is hand calculated at group 1.5, QPU 500000.
		{"plain_ratio", "", plain, 300, false, false, false}, // (100+20*5)*1.5
		{"plain_token_expr", tokenExpr, plain, 300, false, false, false},
		{"cache_reasoning_ratio", "", cache, 192, false, false, false}, // (20+80*.1+20*5)*1.5
		{"cache_reasoning_token_expr", tokenExpr, cache, 192, false, false, false},
		{"claude_cache_ttl_ratio", "", claude, 260, false, false, false}, // round((2+100+15+16+40)*1.5)
		{"claude_cache_ttl_token_expr", tokenExpr, claude, 260, false, false, false},
		{"openai_write_ttl_ratio", "", write, 276, false, false, false}, // (30+4+25+25+100)*1.5
		{"openai_write_ttl_token_expr", tokenExpr, write, 276, false, false, false},
		{"cache_only_token_expr", tokenExpr, dto.Usage{UsageSemantic: "anthropic", PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 1000}}, 150, false, false, false},
		{"explicit_zero_ratio", "", dto.Usage{}, 0, false, false, false},
		{"explicit_zero_token_expr", tokenExpr, dto.Usage{}, 0, false, false, false},
		{"output_only_ratio", "", dto.Usage{CompletionTokens: 20, TotalTokens: 20}, 150, false, false, false},
		{"output_only_token_expr", tokenExpr, dto.Usage{CompletionTokens: 20, TotalTokens: 20}, 150, false, false, false},
		{"fixed_ratio", "", plain, 15000, true, false, false},
		{"fixed_expr", fixedExpr, plain, 15000, false, false, false},
		{"fixed_zero_usage", fixedExpr, dto.Usage{}, 15000, false, false, false},
		{"missing_usage_ratio", "", dto.Usage{}, 1499, false, false, false}, // round(estimated input 999 * 1.5)
		{"missing_usage_fixed", fixedExpr, dto.Usage{}, 15000, false, false, false},
		{"fixed_free", `tier("free", fixed(0))`, plain, 0, false, false, false},
		{"fixed_free_plus_tools", `tier("free", fixed(0))`, plain, 9000, false, true, false},
		{"fixed_plus_tools", fixedExpr, plain, 24000, false, true, false}, // .02*500000*1.5 + 2*6/1000*500000*1.5
		{"ratio_plus_tools", "", plain, 9300, false, true, false},
		{"gemini_modalities", `tier("multimodal", p*2+c*10+cr*0.2+ai*4+ao*20)`, gemini, 464, false, false, false}, // (50*2+19*10+40*.2+40*4+8*20)*.75
		{"tier_at_boundary", `len <= 1000 ? tier("short", p*2+c*10) : tier("long", p*4+c*20)`, dto.Usage{PromptTokens: 1000, CompletionTokens: 20, TotalTokens: 1020}, 1650, false, false, false},
		{"tier_above_boundary", `len <= 1000 ? tier("short", p*2+c*10) : tier("long", p*4+c*20)`, dto.Usage{PromptTokens: 1001, CompletionTokens: 20, TotalTokens: 1021}, 3303, false, false, false},
		{"two_actual_images", `tier("image", fixed(0.04)) * image_count`, plain, 60000, false, false, true},
	} {
		for _, stream := range []bool{false, true} {

			for _, reserveKind := range []string{"less", "equal", "more"} {
				name := fmt.Sprintf("%s/stream_%t/reserve_%s", tc.name, stream, reserveKind)
				t.Run(name, func(t *testing.T) {
					reservation := tc.want
					if reserveKind == "less" {
						reservation /= 2
					}
					if reserveKind == "more" {
						reservation += 20000
					}
					user := model.User{Username: fmt.Sprintf("normal_%d", time.Now().UnixNano()), Quota: initialQuota, Status: common.UserStatusEnabled}
					require.NoError(t, db.Create(&user).Error)
					token := model.Token{UserId: user.Id, Key: fmt.Sprintf("normal-%d", user.Id), Name: "normal-test", RemainQuota: initialQuota, Status: common.TokenStatusEnabled}
					require.NoError(t, db.Create(&token).Error)
					channel := model.Channel{Name: "normal-test", Key: "unused", Status: common.ChannelStatusEnabled}
					require.NoError(t, db.Create(&channel).Error)
					t.Cleanup(func() {
						require.NoError(t, logDB.Where("user_id = ?", user.Id).Delete(&model.Log{}).Error)
						require.NoError(t, db.Unscoped().Delete(&token).Error)
						require.NoError(t, db.Unscoped().Delete(&user).Error)
						require.NoError(t, db.Unscoped().Delete(&channel).Error)
					})
					info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
						ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}, OriginModelName: "fork-normal-test",
						UsingGroup: "default", UserGroup: "default", UserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
						ForcePreConsume: true, StartTime: time.Now(), IsStream: stream, RelayFormat: types.RelayFormatOpenAI,
						PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 5, CacheRatio: .1,
							CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2, CacheCreation12hRatio: 2.5,
							UsePrice: tc.fixed, ModelPrice: .02, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1.5}}}
					info.SetEstimatePromptTokens(999) // Explicit successful zeros must not charge this estimate.
					if tc.expr != "" {
						info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: tc.expr,
							ExprHash: billingexpr.ExprHashString(tc.expr), QuotaPerUnit: common.QuotaPerUnit, GroupRatio: 1.5,
							EstimatedQuotaAfterGroup: reservation}
					}
					if tc.image {
						info.BillingRequestInput = &billingexpr.RequestInput{ImageCount: common.GetPointer(3)}
						info.TieredBillingSnapshot.EstimatedImageCount = common.GetPointer(3)
					}
					if tc.tool {
						info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{"fork_normal_priced_tool": {CallCount: 2}}}
					}
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
					require.Nil(t, PreConsumeBilling(ctx, reservation, info))
					require.NoError(t, db.First(&user, user.Id).Error)
					require.NoError(t, db.First(&token, token.Id).Error)
					require.Equal(t, initialQuota-reservation, user.Quota)
					require.Equal(t, initialQuota-reservation, token.RemainQuota)
					require.Zero(t, user.UsedQuota)
					require.Equal(t, reservation, token.UsedQuota, "token reservation is included in used quota until settlement")
					if tc.image {
						info.UpdateImageCount(2) // Actual count is learned only after sending the reserved request.
					}
					if stream {
						info.StreamResult = &relaycommon.StreamOutcome{ReceivedResponse: true, EffectiveContent: true, ConfirmedUsage: true, UsageSource: "upstream", SettlementState: "pending"}
					}
					usageCopy := tc.usage
					usage := &usageCopy
					if tc.name == "missing_usage_ratio" || tc.name == "missing_usage_fixed" {
						usage = nil
					}
					PostTextConsumeQuota(ctx, info, usage, nil)
					session, ok := info.Billing.(*BillingSession)
					require.True(t, ok)
					require.True(t, session.settled, "the actual settlement must succeed before exercising idempotency")
					require.NoError(t, db.First(&user, user.Id).Error)
					require.NoError(t, db.First(&token, token.Id).Error)
					require.Equal(t, initialQuota-tc.want, user.Quota)
					require.Equal(t, initialQuota-tc.want, token.RemainQuota)
					if stream {
						require.True(t, info.StreamResult.SettlementAttempted)
						require.False(t, info.StreamResult.Failed)
						require.Equal(t, tc.want, info.StreamResult.IntendedQuota)
						wantState := "settled"
						if tc.want == 0 {
							wantState = "released"
						}
						require.Equal(t, wantState, info.StreamResult.SettlementState)
						PostTextConsumeQuota(ctx, info, usage, nil) // Guard prevents duplicate counters/logs too.
					}
					require.NoError(t, info.Billing.Settle(tc.want+123), "a later amount must not replace the successful settlement")
					info.Billing.Refund(ctx) // A settled success cannot be refunded by a deferred cleanup.
					drainRelayLogs()
					require.NoError(t, db.First(&user, user.Id).Error)
					require.NoError(t, db.First(&token, token.Id).Error)
					require.NoError(t, db.First(&channel, channel.Id).Error)
					require.Equal(t, initialQuota-tc.want, user.Quota)
					require.Equal(t, initialQuota-tc.want, token.RemainQuota)
					require.Equal(t, tc.want, user.UsedQuota)
					require.Equal(t, tc.want, token.UsedQuota)
					require.Equal(t, int64(tc.want), channel.UsedQuota)
					wantRequests := 1
					if tc.name == "explicit_zero_ratio" || tc.name == "explicit_zero_token_expr" {
						wantRequests = 0
					}
					require.Equal(t, wantRequests, user.RequestCount)
					var logs []model.Log
					require.NoError(t, logDB.Where("user_id = ?", user.Id).Find(&logs).Error)
					require.Len(t, logs, 1)
					require.Equal(t, model.LogTypeConsume, logs[0].Type)
					require.Equal(t, user.Id, logs[0].UserId)
					require.Equal(t, tc.want, logs[0].Quota)
					require.Equal(t, stream, logs[0].IsStream)
					require.Equal(t, token.Id, logs[0].TokenId)
					require.Equal(t, channel.Id, logs[0].ChannelId)
					require.Equal(t, tc.usage.CompletionTokens, logs[0].CompletionTokens)
					var other map[string]any
					require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
					require.Equal(t, 1.5, other["group_ratio"])
					if tc.image {
						require.Equal(t, float64(2), other["image_count"])
						require.Equal(t, 3, *info.BillingRequestInput.ImageCount)
					}
				})
			}

		}
	}
}
