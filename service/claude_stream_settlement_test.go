package service

import (
	"errors"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// claudeSettlementFixture 替代外部资金/令牌接口，仅记录调用及模拟提交结果。
type claudeSettlementFixture struct {
	calls, actual int       // calls 为结算调用次数；actual 为最近收到的最终内部额度。
	err           error     // Settle 返回的模拟错误，nil 表示成功。
	committed     bool      // 是否模拟资金已提交，用于判断 partial 或 failed。
	onSettle      func(int) // 可选提交回调，参数为最终内部额度；用于同步模拟 SubscriptionPostDelta。
}

// Settle 记录结算调用次数和额度，并按需要执行模拟的资金提交回调。
// 接收者 f：计费会话夹具；参数 quota：本次期望收费的内部额度；返回夹具配置的 err。
func (f *claudeSettlementFixture) Settle(quota int) error {
	f.calls++
	f.actual = quota
	if f.onSettle != nil {
		f.onSettle(quota)
	}
	return f.err
}

// Refund 检测终止结算后误走第二条退款路径，调用即触发测试失败。
// 接收者 f：计费夹具；未命名 *gin.Context 参数为接口要求的请求上下文，不使用其数据。
func (f *claudeSettlementFixture) Refund(*gin.Context) { panic("unexpected refund") }

// NeedsRefund 模拟已由终止结算负责资金处理的会话。
// 接收者 f：计费夹具；无参数；固定返回 false，避免测试进入异步退款。
func (f *claudeSettlementFixture) NeedsRefund() bool { return false }

// GetPreConsumedQuota 提供固定的预扣基准，便于核对终止结算的差额。
// 接收者 f：计费夹具；无参数；返回 100 个内部额度单位。
func (f *claudeSettlementFixture) GetPreConsumedQuota() int { return 100 }

// Reserve 满足计费接口的补充预扣方法，本组终止结算测试不执行实际预扣。
// 接收者 f：计费夹具；未命名 int 参数为目标内部额度，本夹具忽略；返回 nil。
func (f *claudeSettlementFixture) Reserve(int) error { return nil }

// FundingCommitted 向结算逻辑暴露夹具配置的资金提交状态。
// 接收者 f：计费夹具；无参数；返回 committed，区分资金失败和令牌失败。
func (f *claudeSettlementFixture) FundingCommitted() bool { return f.committed }

// TestClaudeTerminalSettlementOnce 覆盖收费、释放预扣、资金失败及令牌失败，验证每请求仅结算一次与结果状态。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeTerminalSettlementOnce(t *testing.T) {
	for _, tc := range []struct {
		name      string // 场景名称。
		quota     int    // 策略要求的最终额度。
		err       error  // 注入的结算错误。
		committed bool   // 模拟资金是否已提交。
		state     string // 预期终止结算状态。
		logged    int    // 预期最终记录的收费额度。
	}{
		{"charged", 100, nil, true, "settled", 100},
		{"released", 0, nil, true, "released", 0},
		{"funding failure", 100, errors.New("fixture failure"), false, "failed", 0},
		{"token failure", 100, errors.New("token fixture failure"), true, "partial", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			fixture := &claudeSettlementFixture{err: tc.err, committed: tc.committed}
			info := &relaycommon.RelayInfo{Billing: fixture, UserQuota: 1_000_000_000, ClaudeStream: &relaycommon.ClaudeStreamOutcome{}}
			params := ConsumptionSettlementParams{Quota: tc.quota, CountUsage: true, LedgerQuota: tc.quota}
			require.True(t, settleClaudeStreamQuota(c, info, &params))
			require.False(t, settleClaudeStreamQuota(c, info, &params))
			require.Equal(t, 1, fixture.calls)
			require.Equal(t, tc.quota, fixture.actual)
			require.Equal(t, tc.logged, params.Quota)
			require.Equal(t, tc.state, info.ClaudeStream.SettlementState)
			require.Equal(t, tc.quota, info.ClaudeStream.IntendedQuota)
			require.Equal(t, 100, info.ClaudeStream.ReservedQuota)
		})
	}
}

// TestClaudeCacheOnlyUsageIsBillable 验证仅有缓存读取或任一缓存写入用量仍属于可计费项目，全空摘要则不属于。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeCacheOnlyUsageIsBillable(t *testing.T) {
	for _, summary := range []textQuotaSummary{{CacheTokens: 1}, {CacheCreationTokens: 1}, {CacheCreationTokens5m: 1}, {CacheCreationTokens1h: 1}} {
		require.True(t, summary.hasBillableUsage())
	}
	empty := textQuotaSummary{}
	require.False(t, empty.hasBillableUsage())
}

// claudeSubscriptionFundingFixture 为真实 BillingSession 提供纯内存订阅资金接口，避免测试操作实际余额。
type claudeSubscriptionFundingFixture struct {
	calls, delta int   // calls 为资金调整次数；delta 为最后一次内部额度差额，负数表示退款。
	err          error // 资金接口返回的模拟错误，nil 表示成功提交。
}

// Source 标识模拟资金来源为订阅，驱动真实 BillingSession 的订阅差额更新分支。
// 接收者 f：订阅资金夹具；无参数；返回 BillingSourceSubscription。
func (f *claudeSubscriptionFundingFixture) Source() string { return BillingSourceSubscription }

// PreConsume 检测结算测试中意外重新预扣的行为，调用即触发测试失败。
// 接收者 f：订阅资金夹具；未命名 int 参数为预扣额度，夹具不执行扣款。
func (f *claudeSubscriptionFundingFixture) PreConsume(int) error {
	panic("unexpected pre-consume")
}

// Refund 检测结算流程误用完整退款入口的行为，调用即触发测试失败。
// 接收者 f：订阅资金夹具；无参数；测试预期永远通过 Settle 的负差额退回预扣。
func (f *claudeSubscriptionFundingFixture) Refund() error { panic("unexpected refund") }

// Settle 记录真实 BillingSession 请求的订阅差额而不操作数据库。
// 接收者 f：订阅资金夹具；参数 delta：内部额度差，正数补扣、负数退回；返回配置的资金错误。
func (f *claudeSubscriptionFundingFixture) Settle(delta int) error {
	f.calls++
	f.delta = delta
	return f.err
}

// TestClaudeSubscriptionSettlementRefreshesLogSnapshot 覆盖订阅全退、部分退、补扣、零差额和失败，核对结算后日志金额、余额下限及重复调用保护。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeSubscriptionSettlementRefreshesLogSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name                  string // 场景名称。
		reserved, actual      int    // reserved 为已预扣额度；actual 为最终收费目标。
		err                   error  // 注入的资金提交错误。
		state                 string // 预期结算状态。
		delta, consumed, used int64  // 预期已提交差额、当前请求最终消耗、订阅累计已用额度。
		remaining             int64  // 预期剩余额度，低于 0 时显示 0。
	}{
		{"full refund", 100, 0, nil, "released", -100, 0, 200, 800},
		{"partial refund", 100, 40, nil, "settled", -60, 40, 240, 760},
		{"supplementary charge", 100, 160, nil, "settled", 60, 160, 360, 640},
		{"unchanged clears stale delta", 100, 100, nil, "settled", 0, 100, 300, 700},
		{"zero reservation clears stale consumption", 0, 0, nil, "released", 0, 0, 200, 800},
		{"failed refund", 100, 0, errors.New("funding fixture failure"), "failed", 0, 100, 300, 700},
		{"failed partial refund", 100, 40, errors.New("funding fixture failure"), "failed", 0, 100, 300, 700},
		{"remaining floors at zero", 100, 1000, nil, "settled", 900, 1000, 1200, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				ChannelMeta:                           &relaycommon.ChannelMeta{},
				IsPlayground:                          true, // 跳过令牌数据库，仍执行真实资金结算差额逻辑。
				BillingSource:                         BillingSourceSubscription,
				SubscriptionPreConsumed:               int64(tc.reserved),
				SubscriptionAmountUsedAfterPreConsume: 200 + int64(tc.reserved),
				SubscriptionAmountTotal:               1000,
				ClaudeStream:                          &relaycommon.ClaudeStreamOutcome{Failed: true},
			}
			// SubscriptionId 保持 0，让既有额度通知直接返回，测试不触发外部 I/O。
			funding := &claudeSubscriptionFundingFixture{err: tc.err}
			info.Billing = &BillingSession{relayInfo: info, funding: funding, preConsumedQuota: tc.reserved}
			other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 1, 0, 1)
			if tc.reserved > 0 {
				require.Equal(t, int64(tc.reserved), other["subscription_consumed"])
				require.Equal(t, int64(tc.reserved), other["subscription_pre_consumed"])
			} else {
				other["subscription_consumed"] = int64(17)
			}
			other["subscription_post_delta"] = int64(19)
			other["unrelated_metadata"] = "preserved"
			params := ConsumptionSettlementParams{Quota: tc.actual, Other: other, CountUsage: true, LedgerQuota: tc.actual}

			require.True(t, settleClaudeStreamQuota(c, info, &params))
			require.Equal(t, tc.state, info.ClaudeStream.SettlementState)
			require.Equal(t, tc.delta, info.SubscriptionPostDelta)
			require.Equal(t, tc.delta, other["subscription_post_delta"])
			require.Equal(t, tc.consumed, other["subscription_consumed"])
			require.Equal(t, tc.used, other["subscription_used"])
			require.Equal(t, tc.remaining, other["subscription_remain"])
			require.Equal(t, int64(1000), other["subscription_total"])
			require.Equal(t, 0, other["wallet_quota_deducted"])
			require.Equal(t, "preserved", other["unrelated_metadata"])
			if tc.reserved > 0 {
				require.Equal(t, int64(tc.reserved), other["subscription_pre_consumed"])
			}
			if tc.err != nil {
				require.Zero(t, params.Quota)
				require.False(t, params.CountUsage)
			} else {
				require.Equal(t, tc.actual, params.Quota)
			}
			require.False(t, settleClaudeStreamQuota(c, info, &params))
			require.Equal(t, tc.delta, info.SubscriptionPostDelta)
			require.Equal(t, tc.consumed, other["subscription_consumed"])
			expectedCalls := 1
			if tc.actual == tc.reserved {
				expectedCalls = 0
			}
			require.Equal(t, expectedCalls, funding.calls)
			require.Equal(t, tc.actual-tc.reserved, funding.delta)
		})
	}
}

// TestClaudeSubscriptionLogReflectsCommittedFundingOnTokenFailure 验证资金已提交但令牌失败时，日志仍反映真实订阅差额并标记 partial。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeSubscriptionLogReflectsCommittedFundingOnTokenFailure(t *testing.T) {
	for _, actual := range []int{0, 40} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		info := &relaycommon.RelayInfo{
			ChannelMeta:                           &relaycommon.ChannelMeta{},
			BillingSource:                         BillingSourceSubscription,
			SubscriptionPreConsumed:               100,
			SubscriptionAmountUsedAfterPreConsume: 300,
			SubscriptionAmountTotal:               1000,
			ClaudeStream:                          &relaycommon.ClaudeStreamOutcome{},
		}
		fixture := &claudeSettlementFixture{
			err:       errors.New("token fixture failure"),
			committed: true,
			onSettle: func(quota int) {
				info.SubscriptionPostDelta += int64(quota - 100)
			},
		}
		info.Billing = fixture
		other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 1, 0, 1)
		params := ConsumptionSettlementParams{Quota: actual, Other: other}
		require.True(t, settleClaudeStreamQuota(c, info, &params))
		require.Equal(t, "partial", info.ClaudeStream.SettlementState)
		require.Equal(t, actual, params.Quota)
		require.Equal(t, int64(actual-100), other["subscription_post_delta"])
		require.Equal(t, int64(actual), other["subscription_consumed"])
		require.Equal(t, int64(200+actual), other["subscription_used"])
		require.Equal(t, int64(800-actual), other["subscription_remain"])
		require.False(t, settleClaudeStreamQuota(c, info, &params))
		require.Equal(t, 1, fixture.calls)
	}
}

// TestClaudeSubscriptionLogRefreshPreservesWalletMetadata 验证订阅日志刷新分支不改变钱包日志和无关元数据。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeSubscriptionLogRefreshPreservesWalletMetadata(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		BillingSource: BillingSourceWallet,
		Billing:       &claudeSettlementFixture{},
		ClaudeStream:  &relaycommon.ClaudeStreamOutcome{},
	}
	other := map[string]interface{}{"wallet_quota_deducted": 13, "unrelated_metadata": "preserved"}
	params := ConsumptionSettlementParams{Other: other}
	require.True(t, settleClaudeStreamQuota(c, info, &params))
	require.Equal(t, map[string]interface{}{"wallet_quota_deducted": 13, "unrelated_metadata": "preserved"}, other)
}
