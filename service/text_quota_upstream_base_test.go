package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 文本计费路径上「上游消耗」口径的基础消耗：同一次用量按分组倍率 1 计算的额度。
// 它必须与用户分组倍率无关——免费分组（倍率 0）也要算出真实的上游消耗。

func upstreamBaseRelayInfo(groupRatio float64) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-4o",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: groupRatio},
		},
		StartTime: time.Now(),
	}
}

func TestCalculateTextQuotaSummary_UpstreamBaseIgnoresGroupRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 50}
	// 100 输入 + 50 输出 × 2 = 200
	const base = 200

	cases := []struct {
		groupRatio float64
		quota      int
	}{
		{1, base},
		{2, 2 * base},
		{0.5, base / 2},
		{0, 0}, // 免费分组：用户不扣费，上游照样消耗
	}
	for _, tc := range cases {
		summary := calculateTextQuotaSummary(ctx, upstreamBaseRelayInfo(tc.groupRatio), usage)
		assert.Equal(t, tc.quota, summary.Quota, "group ratio %v", tc.groupRatio)
		assert.EqualValues(t, base, summary.UpstreamBaseQuota, "group ratio %v", tc.groupRatio)
	}

	// 没有可计费用量（上游什么都没返回）：用户不扣费，上游消耗也计 0。
	empty := calculateTextQuotaSummary(ctx, upstreamBaseRelayInfo(2), &dto.Usage{})
	assert.Zero(t, empty.Quota)
	assert.Zero(t, empty.UpstreamBaseQuota)
}

func TestCalculateTextQuotaSummary_UpstreamBasePerCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 50}

	for _, groupRatio := range []float64{0, 1, 3} {
		info := upstreamBaseRelayInfo(groupRatio)
		info.PriceData.UsePrice = true
		info.PriceData.ModelPrice = 0.01
		summary := calculateTextQuotaSummary(ctx, info, usage)
		assert.EqualValues(t, int64(0.01*common.QuotaPerUnit), summary.UpstreamBaseQuota,
			"per-call base is the model price regardless of group ratio %v", groupRatio)
	}
}

func TestTieredUpstreamBaseQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	summary := calculateTextQuotaSummary(ctx, upstreamBaseRelayInfo(2), &dto.Usage{PromptTokens: 100, CompletionTokens: 50})
	require.EqualValues(t, 200, summary.UpstreamBaseQuota)

	assert.EqualValues(t, 200, tieredUpstreamBaseQuota(summary, nil), "no tiered result keeps the ratio base")
	assert.EqualValues(t, 750, tieredUpstreamBaseQuota(summary, &billingexpr.TieredResult{ActualQuotaBeforeGroup: 750}),
		"tiered billing uses the expression amount before the group ratio")

	empty := calculateTextQuotaSummary(ctx, upstreamBaseRelayInfo(2), &dto.Usage{})
	assert.Zero(t, tieredUpstreamBaseQuota(empty, &billingexpr.TieredResult{ActualQuotaBeforeGroup: 750}),
		"nothing billable, nothing counted upstream")
}
