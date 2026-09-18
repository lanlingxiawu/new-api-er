package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 价格巡检把计费表达式当静态价与上游比对之前，必须做到两件事：
//
//  1. 认出「随时间或请求浮动」的表达式并判为动态价。漏掉一个时间函数会让按月/按日变价的
//     模型被当成固定价与上游对比，误报成亏损。
//  2. 认全 pkg/billingexpr 的 token 计费变量（见 pkg/billingexpr/expr.md）。漏掉一个变量会让
//     tier 主体解析失败，含该变量的模型整行退出价格比对、亏损判定与保本下限，且不产生任何告警。

func parsedPriceMonitorExpressionCell(expression string) PriceMonitorPriceCell {
	tiers, dynamic := parsePriceMonitorExpression(expression)
	return PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: expression, Tiers: tiers, Dynamic: dynamic}
}

func TestParsePriceMonitorExpressionDetectsTimeBasedPricing(t *testing.T) {
	cases := []struct {
		name       string
		expression string
		dynamic    bool
	}{
		{"plain single tier", `tier("standard", p * 2 + c * 8)`, false},
		{"hourly discount", `tier("standard", p * 2 + c * 8) * (hour("Asia/Shanghai") < 8 ? 0.5 : 1)`, true},
		{"minute based", `tier("standard", p * 2 + c * 8) * (minute("UTC") < 30 ? 0.5 : 1)`, true},
		{"weekday based", `tier("standard", p * 2 + c * 8) * (weekday("UTC") == 0 ? 0.5 : 1)`, true},
		{"monthly promotion", `tier("standard", p * 2 + c * 8) * (month("UTC") == 11 ? 0.5 : 1)`, true},
		{"day of month promotion", `tier("standard", p * 2 + c * 8) * (day("UTC") == 11 ? 0.5 : 1)`, true},
		{"request header based", `tier("standard", p * 2 + c * 8) * (header("x-tier") == "vip" ? 0.5 : 1)`, true},
		{"request param based", `tier("standard", p * 2 + c * 8) * (param("quality") == "low" ? 0.5 : 1)`, true},
		{"versioned expression", `tier("standard", p * 2 + c * 8)|||tier("standard", p * 3 + c * 9)`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, dynamic := parsePriceMonitorExpression(tc.expression)
			assert.Equal(t, tc.dynamic, dynamic)
		})
	}
}

// 按月/按日变价的表达式被判为动态后，就不再进入「高于平台」的比对——这正是误报的来源。
func TestPriceMonitorMonthlyPromotionIsNotComparedAsStaticPrice(t *testing.T) {
	platform := parsedPriceMonitorExpressionCell(`tier("standard", p * 2 + c * 8) * (month("UTC") == 11 ? 0.5 : 1)`)
	source := parsedPriceMonitorExpressionCell(`tier("standard", p * 3 + c * 9)`)
	require.True(t, platform.Dynamic, "按月变价的平台表达式必须判为动态价")

	assert.False(t, priceMonitorSourceAbovePlatform(platform, source),
		"动态价没有可比的静态价，不能判为来源高于平台")
}

// gpt-image-2 这类内置价格就带 img_cr（见 setting/billing_setting/builtin_billing.go）。
func TestParsePriceMonitorExpressionParsesImageCacheReadLane(t *testing.T) {
	tiers, dynamic := parsePriceMonitorExpression(`tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`)

	require.False(t, dynamic, "含 img_cr 的静态表达式不能被当作动态价丢弃")
	require.Len(t, tiers, 1)
	assert.InDelta(t, 5, tiers[0].Input, 1e-9)
	assert.InDelta(t, 30, tiers[0].Output, 1e-9)

	lanes := make(map[string]float64, len(tiers[0].Lanes))
	for _, lane := range tiers[0].Lanes {
		require.NotNil(t, lane.Price, "分项 %s 必须带价格", lane.Key)
		lanes[lane.Key] = *lane.Price
	}
	assert.Equal(t, map[string]float64{
		priceMonitorLaneCacheRead:      1.25,
		priceMonitorLaneImageInput:     8,
		priceMonitorLaneImageCacheRead: 2,
	}, lanes)
}

// 图片缓存读取价单独变贵时，必须像其它分项一样被标红并计入「高于平台」。
func TestPriceMonitorImageCacheReadLaneParticipatesInComparison(t *testing.T) {
	platform := parsedPriceMonitorExpressionCell(`tier("standard", p * 5 + img * 8 + img_cr * 2 + c * 30)`)
	source := parsedPriceMonitorExpressionCell(`tier("standard", p * 5 + img * 8 + img_cr * 3 + c * 30)`)
	require.False(t, platform.Dynamic)
	require.False(t, source.Dynamic)

	markPriceMonitorDifferences(platform, &source)

	assert.True(t, source.Different, "只有图片缓存读取价不同也要判为不一致")
	assert.True(t, priceMonitorSourceAbovePlatform(platform, source),
		"来源的图片缓存读取价更贵，必须计入「高于平台」")
}

// 同价时不能因为多认了一个变量而变成「不一致」。
func TestPriceMonitorImageCacheReadLaneEqualIsNotADifference(t *testing.T) {
	expression := `tier("standard", p * 5 + img * 8 + img_cr * 2 + c * 30)`
	platform := parsedPriceMonitorExpressionCell(expression)
	source := parsedPriceMonitorExpressionCell(expression)

	markPriceMonitorDifferences(platform, &source)

	assert.False(t, source.Different)
	assert.False(t, source.ModeDifferent)
}
