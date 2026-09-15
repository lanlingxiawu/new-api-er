package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 只有一档、不带条件的阶梯表达式与同价的按量计费等价，不能仅凭计费模式不同判为不一致。
// 设计见 docs/design/price-monitor-normalized-tier-comparison.md §14。

func singleTierCell(t *testing.T, expression string) PriceMonitorPriceCell {
	t.Helper()
	tiers, dynamic := parsePriceMonitorExpression(expression)
	return PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: expression, Tiers: tiers, Dynamic: dynamic}
}

func tokenCellWithCache(input, output, cacheRead float64) PriceMonitorPriceCell {
	return PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(input),
		Output: floatPointer(output),
		Lanes:  []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(cacheRead)}},
	}
}

// 截图里的误报：官方给单档表达式、平台按量计费、三项价格完全相同，却整格标红、计入「平台对比官方」。
func TestPriceMonitorSingleTierEqualToTokenIsNotADifference(t *testing.T) {
	platform := tokenCellWithCache(2, 8, 0.5)
	official := singleTierCell(t, `tier("base", p * 2 + c * 8 + cr * 0.5)`)
	require.Len(t, official.Tiers, 1, "precondition: parses into one unconditional tier")

	markPriceMonitorDifferences(platform, &official)

	assert.False(t, official.Different)
	assert.False(t, official.ModeDifferent)
	assert.Equal(t, priceMonitorModeToken, official.Mode, "the source is shown as a per-token cell")
	assert.Nil(t, official.Tiers)
	assert.InDelta(t, 2, *official.Input, 1e-9)
	assert.InDelta(t, 8, *official.Output, 1e-9)

	headers := priceMonitorLossHeaders()
	items := []PriceMonitorMatrixItem{{Model: "gpt-4.1", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: platform,
		"official":              official,
	}}}
	assert.Zero(t, countPriceMonitorComparisonModels(headers, items).PlatformOfficial,
		"an equivalent single-tier price must not count as a platform/official mismatch")
}

// 价格确实不同时，要能指出是哪一项不同，而不是笼统的「计费模式不同」。
func TestPriceMonitorSingleTierReportsFieldLevelDifferences(t *testing.T) {
	platform := tokenCellWithCache(2, 8, 0.5)
	source := singleTierCell(t, `tier("base", p * 2 + c * 9 + cr * 0.5)`)

	markPriceMonitorDifferences(platform, &source)

	assert.True(t, source.Different)
	assert.False(t, source.ModeDifferent)
	assert.False(t, source.InputDifferent)
	assert.True(t, source.OutputDifferent)
	require.Len(t, source.Lanes, 1)
	assert.False(t, source.Lanes[0].Different)
}

// 不等价的表达式保持原有的保守判定。
func TestPriceMonitorSingleTierStaysModeDifferentWhenNotEquivalent(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(2), Output: floatPointer(8)}
	cases := map[string]string{
		"two conditional tiers":         `len <= 200000 ? tier("short", p * 2 + c * 8) : tier("long", p * 4 + c * 16)`,
		"lane per-token cannot express": `tier("base", p * 2 + c * 8 + img_o * 3)`,
		"dynamic expression":            `tier("base", p * 2 + c * 8)|||when(header("x-fast") has "1") * 2`,
	}
	for name, expression := range cases {
		t.Run(name, func(t *testing.T) {
			source := singleTierCell(t, expression)
			markPriceMonitorDifferences(platform, &source)
			assert.True(t, source.Different)
			assert.True(t, source.ModeDifferent)
			assert.Equal(t, priceMonitorModeExpression, source.Mode, "a non-equivalent expression is left as it is")
		})
	}
}

// 平台自己按单档表达式计费时，只在比较时借用换算结果：平台单元格保持表达式模式，
// 改价接口据此继续拒绝行内改价（写 ModelRatio 不会生效）。
func TestPriceMonitorSingleTierPlatformIsNeverRewritten(t *testing.T) {
	platform := singleTierCell(t, `tier("base", p * 2 + c * 8)`)

	same := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(2), Output: floatPointer(8)}
	markPriceMonitorDifferences(platform, &same)
	assert.False(t, same.Different)
	assert.False(t, same.ModeDifferent)

	pricier := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(2), Output: floatPointer(9)}
	markPriceMonitorDifferences(platform, &pricier)
	assert.True(t, pricier.OutputDifferent)

	assert.Equal(t, priceMonitorModeExpression, platform.Mode)
	assert.Len(t, platform.Tiers, 1)
}

// 渠道按单档表达式报价时，此前因计费模式不同被亏损判定、保本下限与「高于平台」整体跳过。
func TestPriceMonitorSingleTierChannelJoinsLossDetectionAndFloors(t *testing.T) {
	headers := priceMonitorLossHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2)}
	// 比平台便宜，但售价系数 0.7 下仍亏：0.85 / 0.7 > 1。
	channel := singleTierCell(t, `tier("base", p * 0.85 + c * 1.6)`)
	markPriceMonitorDifferences(platform, &channel)

	items := []PriceMonitorMatrixItem{{Model: "m", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: platform,
		"channel-a":             channel,
	}}}
	contexts := priceMonitorLossContexts(0.7, 0.5)
	applyPriceMonitorLossVerdicts(headers, items, contexts)
	applyPriceMonitorRepairFloors(headers, items, contexts)

	flagged := items[0].Prices["channel-a"]
	assert.Equal(t, []string{priceMonitorLossKindMeasured}, flagged.LossKinds)
	require.NotNil(t, items[0].RepairFloor)
	assert.InDelta(t, 0.85/0.7/2, items[0].RepairFloor.Fields["model_ratio"], 1e-9)

	pricier := singleTierCell(t, `tier("base", p * 3 + c * 2)`)
	markPriceMonitorDifferences(platform, &pricier)
	assert.True(t, priceMonitorSourceAbovePlatform(platform, pricier))
}

// Claude 价格带 1 小时缓存写入（cc1h）。按量计费的平台没有这一项的配置，但计费按
// 5 分钟缓存写入价 × 6/3.75 计价，所以可比：1.25 × 1.6 = 2。
func claudeLikePlatform() PriceMonitorPriceCell {
	return PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(1),
		Output: floatPointer(5),
		Lanes: []PriceMonitorPriceLane{
			{Key: priceMonitorLaneCacheRead, Price: floatPointer(0.1)},
			{Key: priceMonitorLaneCacheWrite, Price: floatPointer(1.25)},
		},
	}
}

func laneByKey(t *testing.T, cell PriceMonitorPriceCell, key string) PriceMonitorPriceLane {
	t.Helper()
	for _, lane := range cell.Lanes {
		if lane.Key == key {
			return lane
		}
	}
	t.Fatalf("lane %s not found in %+v", key, cell.Lanes)
	return PriceMonitorPriceLane{}
}

func TestPriceMonitorSingleTierCompares1hCacheWriteAgainstBilledPrice(t *testing.T) {
	platform := claudeLikePlatform()

	same := singleTierCell(t, `tier("base", p * 1 + c * 5 + cr * 0.1 + cc * 1.25 + cc1h * 2)`)
	markPriceMonitorDifferences(platform, &same)
	assert.Equal(t, priceMonitorModeToken, same.Mode, "a 1h cache write lane no longer blocks the comparison")
	assert.False(t, same.Different)
	assert.False(t, laneByKey(t, same, priceMonitorLaneCacheWrite1h).Different)

	dearer := singleTierCell(t, `tier("base", p * 1 + c * 5 + cr * 0.1 + cc * 1.25 + cc1h * 3)`)
	markPriceMonitorDifferences(platform, &dearer)
	assert.True(t, dearer.Different)
	assert.False(t, dearer.ModeDifferent)
	assert.True(t, laneByKey(t, dearer, priceMonitorLaneCacheWrite1h).Different)
	assert.False(t, laneByKey(t, dearer, priceMonitorLaneCacheWrite).Different)

	// 平台没配 5 分钟缓存写入，就推不出 1 小时价格：与其它单侧才有的分项一样算作差异。
	bare := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(5)}
	only1h := singleTierCell(t, `tier("base", p * 1 + c * 5 + cc1h * 2)`)
	markPriceMonitorDifferences(bare, &only1h)
	assert.True(t, laneByKey(t, only1h, priceMonitorLaneCacheWrite1h).Different)
}

// 平台推导出来的 1 小时价格只用于比较：来源没报这一项时不插空行，平台单元格也不被改写。
func TestPriceMonitorDerived1hLaneIsComparisonOnly(t *testing.T) {
	platform := claudeLikePlatform()
	source := tokenCellWithCache(1, 5, 0.1)

	markPriceMonitorDifferences(platform, &source)

	for _, lane := range source.Lanes {
		assert.NotEqual(t, priceMonitorLaneCacheWrite1h, lane.Key)
	}
	assert.Len(t, platform.Lanes, 2)
}

// 1 小时缓存写入上的亏损：判定能看到，保本下限把要求折算进 create_cache_ratio，
// 并且按保本价改完确实能清掉这次判定。
func TestPriceMonitor1hCacheWriteLossFoldsIntoCreateCacheRatio(t *testing.T) {
	headers := priceMonitorLossHeaders()
	platform := claudeLikePlatform()
	// 1 小时写入成本 3，平台按 1.25 × 1.6 = 2 计费：亏在 1 小时这一项（3 / 2 = 1.5 倍）。
	channel := singleTierCell(t, `tier("base", p * 1 + c * 5 + cr * 0.1 + cc * 1.25 + cc1h * 3)`)
	markPriceMonitorDifferences(platform, &channel)

	items := []PriceMonitorMatrixItem{{Model: "claude-haiku-like", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: platform,
		"channel-a":             channel,
	}}}
	contexts := priceMonitorLossContexts(1.0, 0.5)
	applyPriceMonitorLossVerdicts(headers, items, contexts)
	applyPriceMonitorRepairFloors(headers, items, contexts)

	flagged := items[0].Prices["channel-a"]
	require.Equal(t, []string{priceMonitorLossKindMeasured}, flagged.LossKinds)
	require.NotNil(t, flagged.MeasuredFactor)
	assert.InDelta(t, 1.5, *flagged.MeasuredFactor, 1e-9)

	// 5 分钟写入价要抬到 3 / 1.6 = 1.875，input 仍是 1 → create_cache_ratio = 1.875。
	floor := items[0].RepairFloor
	require.NotNil(t, floor)
	assert.InDelta(t, 1.875, floor.Fields["create_cache_ratio"], 1e-9)
	assert.InDelta(t, 1.875, floor.Display["create_cache_ratio"], 1e-9)
	assert.Equal(t, "channel-a", floor.Binding["create_cache_ratio"])

	factor, ok := priceMonitorMeasuredFactor(repairedCell(floor), channel)
	require.True(t, ok)
	assert.LessOrEqual(t, factor, 1.0+1e-9, "the breakeven floor must clear the 1h cache write loss")
}
