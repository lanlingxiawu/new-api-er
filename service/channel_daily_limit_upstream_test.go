package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 上游消耗口径：基础消耗（不乘用户分组倍率）× 渠道成本系数。
// 设计见 docs/design/channel-limit-upstream-basis-and-timed-recovery.md §4。

func priceDataWithGroupRatio(groupRatio float64) *hosttypes.PriceData {
	p := &hosttypes.PriceData{}
	p.GroupRatioInfo.GroupRatio = groupRatio
	return p
}

func TestUpstreamBaseQuota(t *testing.T) {
	explicit := int64(321)
	negative := int64(-5)

	cases := []struct {
		name      string
		priceData *hosttypes.PriceData
		quota     int
		explicit  *int64
		want      int64
	}{
		{"explicit value wins over division", priceDataWithGroupRatio(2), 1000, &explicit, 321},
		{"explicit zero is kept (billed nothing upstream)", priceDataWithGroupRatio(2), 1000, ptrInt64(0), 0},
		{"negative explicit clamps to zero", priceDataWithGroupRatio(1), 1000, &negative, 0},
		{"group ratio 1 keeps the quota", priceDataWithGroupRatio(1), 1000, nil, 1000},
		{"group ratio 2 halves the quota", priceDataWithGroupRatio(2), 1000, nil, 500},
		{"group ratio 0.8 scales up", priceDataWithGroupRatio(0.8), 1000, nil, 1250},
		{"division rounds half up", priceDataWithGroupRatio(3), 1000, nil, 333},
		{"zero quota with a paid group", priceDataWithGroupRatio(1.5), 0, nil, 0},
		{"negative quota (refund) is ignored", priceDataWithGroupRatio(1), -100, nil, 0},
		{"nil price data", nil, 1000, nil, 0},
		{"free group per-token cannot be recovered", priceDataWithGroupRatio(0), 0, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, upstreamBaseQuota(tc.priceData, tc.quota, tc.explicit))
		})
	}
}

func TestUpstreamBaseQuota_FreeGroupPerCallUsesModelPrice(t *testing.T) {
	p := priceDataWithGroupRatio(0)
	p.UsePrice = true
	p.ModelPrice = 0.02
	want := int64(0.02*common.QuotaPerUnit + 0.5)
	assert.Equal(t, want, upstreamBaseQuota(p, 0, nil), "per-call price × quota per unit")

	p.AddOtherRatio("seconds", 3)
	assert.Equal(t, int64(0.06*common.QuotaPerUnit+0.5), upstreamBaseQuota(p, 0, nil),
		"other ratios (duration, resolution …) are part of the upstream price")

	p.ModelPrice = 0
	assert.Zero(t, upstreamBaseQuota(p, 0, nil), "a free per-call model costs nothing upstream")
}

func TestUpstreamQuota(t *testing.T) {
	cases := []struct {
		name      string
		base      int64
		costRatio float64
		want      int64
	}{
		{"default cost ratio 1", 1000, 1, 1000},
		{"discounted upstream", 1000, 0.3, 300},
		{"marked-up upstream", 1000, 1.25, 1250},
		{"rounds half up", 3, 0.5, 2},
		{"zero base", 0, 1, 0},
		{"negative base", -10, 1, 0},
		{"zero cost ratio", 1000, 0, 0},
		{"negative cost ratio", 1000, -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, upstreamQuota(tc.base, tc.costRatio))
		})
	}
}

// recordChannelDailyUpstream 对未配置上限的渠道、关闭的总开关、非正数额度一律零副作用。
func TestRecordChannelDailyUpstream_Gates(t *testing.T) {
	resetDailyLimitState(t)
	statDate := todayStatDate()
	limited, unlimited := 900090, 900091
	setDailyLimitConfigs(map[int]channelLimitConfig{limited: {limit: 1_000_000}})

	enableDailyLimitSetting(t, false)
	recordChannelDailyUpstream(limited, 100)
	assert.Zero(t, pendingUsed(limited, statDate), "master switch off")

	enableDailyLimitSetting(t, true)
	recordChannelDailyUpstream(unlimited, 100)
	assert.Zero(t, pendingUsed(unlimited, statDate), "no limit configured")
	recordChannelDailyUpstream(limited, 0)
	recordChannelDailyUpstream(limited, -1)
	recordChannelDailyUpstream(0, 100)
	assert.Zero(t, pendingUsed(limited, statDate), "non-positive amounts and ids are ignored")
}

func ptrInt64(v int64) *int64 { return &v }

// 免费分组（倍率 0）+ 表达式计价的任务：预扣额与结算额都恒为 0，PriceData 里
// ModelPrice 也是 0 且不是按次计费，兜底分支还原不出任何基数。只有提交时显式传入
// 「表达式乘分组倍率之前的额度」才能让渠道每日上限看到真实的上游消耗。
func TestTaskSubmitUpstreamBase_FreeGroupTieredTask(t *testing.T) {
	assert.Nil(t, taskSubmitUpstreamBase(nil, &relaycommon.RelayInfo{}), "非表达式任务沿用既有推导")

	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                0,
			EstimatedQuotaBeforeGroup: 12000.4,
			TaskUsageBilling:          true,
		},
	}
	base := taskSubmitUpstreamBase(nil, info)
	require.NotNil(t, base)
	assert.EqualValues(t, 12000, *base)

	// 提交即完成的结算值走请求上下文，优先于快照里的预估值（快照不被改写）。
	settledCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetTaskSettledUpstreamBase(settledCtx, 20800.6)
	settledBase := taskSubmitUpstreamBase(settledCtx, info)
	require.NotNil(t, settledBase)
	assert.EqualValues(t, 20801, *settledBase)
	assert.EqualValues(t, 12000.4, info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup, "快照的预估口径保持不变")

	freeGroup := priceDataWithGroupRatio(0)
	assert.Zero(t, upstreamBaseQuota(freeGroup, 0, nil), "没有显式基数时止损口径恒为 0")
	assert.EqualValues(t, 12000, upstreamBaseQuota(freeGroup, 0, base))
}

// 差额结算的基数增量：只增不减，四舍五入到 quota。
func TestTaskUpstreamBaseDelta(t *testing.T) {
	var absent *TaskUpstreamBase
	assert.Zero(t, absent.delta(), "非表达式任务不给基数")
	assert.EqualValues(t, 500, (&TaskUpstreamBase{Before: 1000, After: 1500}).delta())
	assert.Zero(t, (&TaskUpstreamBase{Before: 1500, After: 1000}).delta(), "结算下调不回减累计")
	assert.EqualValues(t, 1, (&TaskUpstreamBase{Before: 0.4, After: 1.4}).delta())
	assert.Zero(t, (&TaskUpstreamBase{Before: 800, After: 800}).delta())
}
