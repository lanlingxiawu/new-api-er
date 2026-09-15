package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func priceMonitorLossHeaders() []PriceMonitorSourceHeader {
	return []PriceMonitorSourceHeader{
		{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
		{Key: "official", Type: priceSourceOfficial},
		{Key: "models-dev", Type: priceSourceModelsDev},
		{Key: "channel-a", Type: priceSourceChannel},
		{Key: "channel-b", Type: priceSourceChannel},
	}
}

func priceMonitorLossContexts(sellFactor, configured float64) map[string]priceMonitorLossContext {
	return map[string]priceMonitorLossContext{
		"channel-a": {ChannelId: 1, SellFactor: sellFactor, Configured: configured, Valid: true},
		"channel-b": {ChannelId: 2, SellFactor: sellFactor, Configured: configured, Valid: true},
	}
}

func TestPriceMonitorMeasuredFactorPerToken(t *testing.T) {
	platform := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(10),
		Output: floatPointer(20),
		Lanes:  []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(5)}},
	}
	tests := []struct {
		name     string
		source   PriceMonitorPriceCell
		expected float64
		ok       bool
	}{
		{"input drives the max", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(13), Output: floatPointer(20)}, 1.3, true},
		{"output drives the max", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(30)}, 1.5, true},
		{"lane drives the max", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(10)}}}, 2, true},
		{"all equal", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)}, 1, true},
		{"cheaper", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(5), Output: floatPointer(10)}, 0.5, true},
		{"source output missing skips that dimension", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(11)}, 1.1, true},
		{"lane absent on platform is skipped", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneAudioInput, Price: floatPointer(999)}}}, 1, true},
		{"different billing mode is not comparable", PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(9)}, 0, false},
		{"unavailable source is not comparable", PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableMissing}, 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factor, ok := priceMonitorMeasuredFactor(platform, test.source)
			require.Equal(t, test.ok, ok)
			if test.ok {
				require.InDelta(t, test.expected, factor, 1e-9)
			}
		})
	}

	_, ok := priceMonitorMeasuredFactor(PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableSourceFailed}, PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1)})
	require.False(t, ok, "an unavailable platform cell is not comparable")

	_, ok = priceMonitorMeasuredFactor(
		PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0), Output: floatPointer(0)},
		PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(5), Output: floatPointer(5)},
	)
	require.False(t, ok, "a platform value of 0 cannot be a divisor, so no dimension is comparable")
}

func TestPriceMonitorMeasuredFactorRequestAndTiered(t *testing.T) {
	requestPlatform := PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.5)}
	factor, ok := priceMonitorMeasuredFactor(requestPlatform, PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.75)})
	require.True(t, ok)
	require.InDelta(t, 1.5, factor, 1e-9)

	tieredPlatform := PriceMonitorPriceCell{
		Mode: priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{
			{Range: "low", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(200000), Input: 1, Output: 5},
			{Range: "high", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(200000), Input: 2, Output: 10},
		},
	}
	tieredSource := PriceMonitorPriceCell{
		Mode: priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{
			{Range: "low", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(200000), Input: 1, Output: 5},
			{Range: "high", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(200000), Input: 2, Output: 14},
		},
	}
	factor, ok = priceMonitorMeasuredFactor(tieredPlatform, tieredSource)
	require.True(t, ok)
	require.InDelta(t, 1.4, factor, 1e-9, "the most expensive tier dimension wins")

	_, ok = priceMonitorMeasuredFactor(tieredPlatform, PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Dynamic: true, Tiers: tieredSource.Tiers})
	require.False(t, ok, "dynamic expressions are not comparable")

	_, ok = priceMonitorMeasuredFactor(tieredPlatform, PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Tiers: tieredSource.Tiers[:1]})
	require.False(t, ok, "a different tier count is not comparable")

	mismatched := []PriceMonitorPriceTier{
		{Range: "low", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(128000), Input: 9, Output: 9},
		{Range: "high", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(128000), Input: 9, Output: 9},
	}
	_, ok = priceMonitorMeasuredFactor(tieredPlatform, PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Tiers: mismatched})
	require.False(t, ok, "different tier conditions are not comparable")
}

func TestPriceMonitorLossKinds(t *testing.T) {
	tests := []struct {
		name       string
		measured   float64
		configured float64
		sellFactor float64
		expected   []string
	}{
		{"both below sell factor", 0.9, 0.9, 1.0, nil},
		{"only measured above", 1.2, 0.9, 1.0, []string{priceMonitorLossKindMeasured}},
		{"only configured above", 0.9, 1.2, 1.0, []string{priceMonitorLossKindConfigured}},
		{"both above", 1.2, 1.3, 1.0, []string{priceMonitorLossKindMeasured, priceMonitorLossKindConfigured}},
		{"group ratio below one exposes a measured loss", 0.85, 1.0, 0.7, []string{priceMonitorLossKindMeasured, priceMonitorLossKindConfigured}},
		{"measured equal to sell factor is not a loss", 1.0, 1.0, 1.0, nil},
		{"within tolerance is not a loss", 1.0 + 1e-12, 0.5, 1.0, nil},
		{"outside tolerance is a loss", 1.0 + 1e-6, 0.5, 1.0, []string{priceMonitorLossKindMeasured}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, priceMonitorLossKinds(test.measured, test.configured, test.sellFactor))
		})
	}
}

// repairFloorFor 走真实的保本下限流程（一行、单个渠道 channel-a）。
func repairFloorFor(modelName string, platform, source PriceMonitorPriceCell, sellFactor float64) *PriceMonitorRepairFloor {
	items := []PriceMonitorMatrixItem{{Model: modelName, Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: platform,
		"channel-a":             source,
	}}}
	applyPriceMonitorRepairFloors(priceMonitorLossHeaders(), items, map[string]priceMonitorLossContext{
		"channel-a": {ChannelId: 1, SellFactor: sellFactor, Valid: true},
	})
	return items[0].RepairFloor
}

// repairedCell 把保本下限的展示价还原成平台单元格，用来验证「按保本价改完就不再亏」。
func repairedCell(floor *PriceMonitorRepairFloor) PriceMonitorPriceCell {
	cell := PriceMonitorPriceCell{Mode: floor.Mode}
	if value, ok := floor.Display["model_price"]; ok {
		cell.Price = floatPointer(value)
	}
	if value, ok := floor.Display["model_ratio"]; ok {
		cell.Input = floatPointer(value)
	}
	if value, ok := floor.Display["completion_ratio"]; ok {
		cell.Output = floatPointer(value)
	}
	for _, lane := range priceMonitorRatioLanes {
		if value, ok := floor.Display[lane.field]; ok {
			cell.Lanes = append(cell.Lanes, PriceMonitorPriceLane{Key: lane.key, Price: floatPointer(value)})
		}
	}
	return cell
}

// 保本价（= 保本下限）必须真的清掉它针对的那次判定。
func TestRepairFloorClearsTheVerdict(t *testing.T) {
	const sellFactor = 0.7
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2)}
	source := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0.85), Output: floatPointer(1.6)}

	measured, ok := priceMonitorMeasuredFactor(platform, source)
	require.True(t, ok)
	require.Equal(t, []string{priceMonitorLossKindMeasured}, priceMonitorLossKinds(measured, 0.5, sellFactor))

	floor := repairFloorFor("loss-model", platform, source, sellFactor)
	require.NotNil(t, floor)
	require.InDelta(t, 0.85/0.7, floor.Display["model_ratio"], 1e-9)
	require.InDelta(t, 1.6/0.7, floor.Display["completion_ratio"], 1e-9)

	repairedFactor, ok := priceMonitorMeasuredFactor(repairedCell(floor), source)
	require.True(t, ok)
	require.LessOrEqual(t, repairedFactor, sellFactor+1e-9, "the breakeven floor must clear the verdict")
	require.Empty(t, priceMonitorLossKinds(repairedFactor, 0.5, sellFactor))
}

func TestRepairFloorIsPerDimension(t *testing.T) {
	platform := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(1),
		Output: floatPointer(10),
		Lanes:  []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(0.5)}},
	}
	// 输入贵一倍、输出便宜一半：统一缩放会把输出也抬一倍，逐维度计算不会。
	source := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(2),
		Output: floatPointer(5),
		Lanes:  []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(0.25)}},
	}

	floor := repairFloorFor("per-dimension-model", platform, source, 1.0)
	require.NotNil(t, floor)
	require.InDelta(t, 2, floor.Display["model_ratio"], 1e-9)
	require.InDelta(t, 5, floor.Display["completion_ratio"], 1e-9, "a cheaper output must not be scaled up by the input ratio")
	require.InDelta(t, 0.25, floor.Display["cache_ratio"], 1e-9)

	// Fields 是 priceMonitorCell 的逆运算，前端直接提交，不重复实现换算。
	require.InDelta(t, 1, floor.Fields["model_ratio"], 1e-9)
	require.InDelta(t, 2.5, floor.Fields["completion_ratio"], 1e-9)
	require.InDelta(t, 0.125, floor.Fields["cache_ratio"], 1e-9)
}

// 「最高价」取各渠道原始报价里每一项的最高值，不除售价系数；保本下限才除。1 小时缓存写入
// 折算进缓存写入价；锁定的补全倍率一并带出，前端据此把输出价跟随输入价。
func TestRepairFloorCarriesHighestChannelQuotes(t *testing.T) {
	platform := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(1),
		Output: floatPointer(2),
		Lanes:  []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheWrite, Price: floatPointer(1.25)}},
	}
	source := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(0.85),
		Output: floatPointer(1.6),
		Lanes: []PriceMonitorPriceLane{
			{Key: priceMonitorLaneCacheWrite, Price: floatPointer(1)},
			{Key: priceMonitorLaneCacheWrite1h, Price: floatPointer(3.2)},
		},
	}

	floor := repairFloorFor("highest-model", platform, source, 0.7)
	require.NotNil(t, floor)
	require.InDelta(t, 0.85, floor.Highest["model_ratio"], 1e-9, "the highest price is the raw quote")
	require.InDelta(t, 1.6, floor.Highest["completion_ratio"], 1e-9)
	require.InDelta(t, 2, floor.Highest["create_cache_ratio"], 1e-9, "1h write 3.2 / 1.6 beats the 5m quote of 1")
	require.InDelta(t, 0.85/0.7, floor.Display["model_ratio"], 1e-9, "the break-even floor still divides by the sell factor")
	require.Zero(t, floor.LockedCompletionRatio)

	locked := repairFloorFor(lockedCompletionModel, tokenCell(1, 5), tokenCell(1, 10), 1.0)
	require.NotNil(t, locked)
	require.InDelta(t, 5, locked.LockedCompletionRatio, 1e-9)
	require.InDelta(t, 10, locked.Highest["completion_ratio"], 1e-9, "the output quote is still reported for comparison")
}

func TestRepairFloorSkipsTieredPricing(t *testing.T) {
	tiered := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Tiers: []PriceMonitorPriceTier{{Range: "all", Input: 1, Output: 2}}}
	require.Nil(t, repairFloorFor("tiered-model", tiered, tiered, 1.0), "tiered pricing has no inline editor, so it gets no floor")

	perRequest := PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(1)}
	floor := repairFloorFor("per-request-model", perRequest, PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(2)}, 1.0)
	require.NotNil(t, floor)
	require.InDelta(t, 2, floor.Fields["model_price"], 1e-9)
}

func TestApplyPriceMonitorLossVerdicts(t *testing.T) {
	headers := priceMonitorLossHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2)}
	pricier := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0.85), Output: floatPointer(0.5), Different: true, InputDifferent: true, OutputDifferent: true}
	cheaper := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0.1), Output: floatPointer(0.2), Different: true, InputDifferent: true, OutputDifferent: true}

	items := []PriceMonitorMatrixItem{
		{Model: "loss", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              pricier,
			"models-dev":            pricier,
			"channel-a":             pricier,
			"channel-b":             cheaper,
		}},
		{Model: "safe", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"channel-a":             cheaper,
		}},
	}
	// configured 0.5 低于售价系数，隔离出纯 measured 命中。
	applyPriceMonitorLossVerdicts(headers, items, priceMonitorLossContexts(0.7, 0.5))

	flagged := items[0].Prices["channel-a"]
	require.Equal(t, []string{priceMonitorLossKindMeasured}, flagged.LossKinds)
	require.InDelta(t, 0.7, *flagged.SellFactor, 1e-9)
	require.InDelta(t, 0.85, *flagged.MeasuredFactor, 1e-9)
	require.InDelta(t, 0.5, *flagged.ConfiguredFactor, 1e-9)

	require.Empty(t, items[0].Prices["channel-b"].LossKinds, "a cheaper channel is not a loss")
	require.Empty(t, items[0].Prices["official"].LossKinds, "official prices are not our procurement cost")
	require.Empty(t, items[0].Prices["models-dev"].LossKinds)
	require.Empty(t, items[0].Prices[priceMonitorPlatformKey].LossKinds)
	require.Empty(t, items[1].Prices["channel-a"].LossKinds)
}

func TestApplyPriceMonitorLossVerdictsConfiguredOnly(t *testing.T) {
	headers := priceMonitorLossHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1)}
	source := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0.5), Different: true, InputDifferent: true}
	items := []PriceMonitorMatrixItem{{Model: "configured-only", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: platform,
		"channel-a":             source,
	}}}

	applyPriceMonitorLossVerdicts(headers, items, priceMonitorLossContexts(0.7, 1.2))

	cell := items[0].Prices["channel-a"]
	// 只有成本系数口径的亏损：改价不改变毛利率，前端不提供改价入口（入口只看 measured）。
	require.Equal(t, []string{priceMonitorLossKindConfigured}, cell.LossKinds)
}

func TestApplyPriceMonitorLossVerdictsSkipsSourcesWithoutContext(t *testing.T) {
	headers := priceMonitorLossHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1)}
	pricier := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(5), Different: true, InputDifferent: true}
	items := []PriceMonitorMatrixItem{{Model: "model", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: platform,
		"channel-a":             pricier,
		"channel-b":             pricier,
	}}}

	contexts := map[string]priceMonitorLossContext{
		"channel-a": {ChannelId: 1, SellFactor: 0, Configured: 1, Valid: true},
		"channel-b": {ChannelId: 2, SellFactor: 1, Configured: 1, Valid: false},
	}
	applyPriceMonitorLossVerdicts(headers, items, contexts)

	require.Empty(t, items[0].Prices["channel-a"].LossKinds, "a zero sell factor cannot be a divisor")
	require.Empty(t, items[0].Prices["channel-b"].LossKinds, "an invalid context is skipped")
}

func TestQueryPriceMonitorMatrixLossRisk(t *testing.T) {
	headers := priceMonitorLossHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2)}
	pricier := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0.85), Output: floatPointer(0.5), Different: true, InputDifferent: true, OutputDifferent: true}
	cheaper := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0.1), Output: floatPointer(0.2), Different: true, InputDifferent: true, OutputDifferent: true}

	items := []PriceMonitorMatrixItem{
		{Model: "channel-a-loses", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              pricier,
			"channel-a":             pricier,
			"channel-b":             cheaper,
		}},
		{Model: "no-loss", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"channel-a":             cheaper,
		}},
	}
	applyPriceMonitorLossVerdicts(headers, items, priceMonitorLossContexts(0.7, 0.5))
	snapshot := PriceMonitorSnapshot{SourceHeaders: headers, MatrixItems: items}

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: "loss_risk", Page: 1, PageSize: 20})
	require.Len(t, result.Items, 1)
	require.Equal(t, "channel-a-loses", result.Items[0].Model)
	require.Equal(t, "loss_risk", result.AppliedFilters.Comparison)
	require.Equal(t, []string{priceMonitorPlatformKey, "channel-a"}, priceMonitorHeaderKeys(result.SourceHeaders))
	require.Equal(t, []string{priceMonitorPlatformKey, "channel-a"}, priceMonitorSortedPriceKeys(result.Items[0]))

	require.Equal(t, 1, countPriceMonitorComparisonModels(headers, items).LossRisk)
}

func TestPriceMonitorLossRiskDoesNotDisturbAbovePlatform(t *testing.T) {
	headers := priceMonitorLossHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2)}
	// 比平台便宜但仍亏损（分组倍率 0.7）——above_platform 不该命中，loss_risk 该命中。
	cheaperButLosing := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0.85), Output: floatPointer(2), Different: true, InputDifferent: true}
	// 比平台贵但仍有利润（分组倍率 1.0 时 measured 1.1 > 1.0 会亏，这里用 0.7 的上下文使其亏损；
	// 用 above_platform 独立断言其命中）。
	pricier := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1.1), Output: floatPointer(2), Different: true, InputDifferent: true}

	build := func() []PriceMonitorMatrixItem {
		return []PriceMonitorMatrixItem{
			{Model: "cheaper-but-losing", Prices: map[string]PriceMonitorPriceCell{
				priceMonitorPlatformKey: platform,
				"channel-a":             cheaperButLosing,
			}},
			{Model: "pricier", Prices: map[string]PriceMonitorPriceCell{
				priceMonitorPlatformKey: platform,
				"channel-a":             pricier,
			}},
		}
	}

	withoutLoss := build()
	baseline := queryPriceMonitorMatrix(PriceMonitorSnapshot{SourceHeaders: headers, MatrixItems: withoutLoss}, priceMonitorQuery{Comparison: "above_platform", Page: 1, PageSize: 20})
	baselineCounts := countPriceMonitorComparisonModels(headers, withoutLoss)

	withLoss := build()
	applyPriceMonitorLossVerdicts(headers, withLoss, priceMonitorLossContexts(0.7, 0.5))
	annotated := queryPriceMonitorMatrix(PriceMonitorSnapshot{SourceHeaders: headers, MatrixItems: withLoss}, priceMonitorQuery{Comparison: "above_platform", Page: 1, PageSize: 20})
	annotatedCounts := countPriceMonitorComparisonModels(headers, withLoss)

	require.Len(t, baseline.Items, 1)
	require.Equal(t, "pricier", baseline.Items[0].Model, "only the pricier channel is above platform")
	require.Equal(t, len(baseline.Items), len(annotated.Items))
	require.Equal(t, baseline.Items[0].Model, annotated.Items[0].Model)
	require.Equal(t, baselineCounts.AbovePlatform, annotatedCounts.AbovePlatform, "loss detection must not move the above_platform count")
	require.Equal(t, 1, baselineCounts.AbovePlatform)
	require.Equal(t, 2, annotatedCounts.LossRisk, "both models lose money once the 0.7 group ratio is applied")
}

func TestPriceMonitorSellFactorUsesResolveGroupRatioPriority(t *testing.T) {
	restoreGroupRatio := ratio_setting.GroupRatio2JSONString()
	restoreGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(restoreGroupRatio))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(restoreGroupGroupRatio))
	})

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":0.9,"free":0,"unrelated":1}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"bulk":{"vip":0.6},"other":{"unrelated":0.1}}`))
	userGroups := priceMonitorConfiguredUserGroups()

	factor, ok := priceMonitorSellFactor([]string{"default"}, userGroups)
	require.True(t, ok)
	require.InDelta(t, 1, factor, 1e-9, "an unconfigured group pair falls back to the global group ratio")

	factor, ok = priceMonitorSellFactor([]string{"vip"}, userGroups)
	require.True(t, ok)
	require.InDelta(t, 0.6, factor, 1e-9, "the group-group ratio wins over the global group ratio")

	factor, ok = priceMonitorSellFactor([]string{"default", "vip"}, userGroups)
	require.True(t, ok)
	require.InDelta(t, 0.6, factor, 1e-9, "the lowest reachable ratio across the channel's groups wins")

	_, ok = priceMonitorSellFactor([]string{"free"}, userGroups)
	require.False(t, ok, "a free group is skipped, matching calcCostQuota treating groupRatio 0 as a giveaway")

	_, ok = priceMonitorSellFactor(nil, userGroups)
	require.False(t, ok)

	factor, ok = priceMonitorSellFactor([]string{"unrelated"}, userGroups)
	require.True(t, ok)
	require.InDelta(t, 0.1, factor, 1e-9, "a configured pair still applies when the channel serves that group")
}

func TestPriceMonitorSellFactorIgnoresUnreachableGroupPairs(t *testing.T) {
	restoreGroupRatio := ratio_setting.GroupRatio2JSONString()
	restoreGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(restoreGroupRatio))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(restoreGroupGroupRatio))
	})

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"served":1,"not-served":1}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"bulk":{"not-served":0.2}}`))

	factor, ok := priceMonitorSellFactor([]string{"served"}, priceMonitorConfiguredUserGroups())
	require.True(t, ok)
	require.InDelta(t, 1, factor, 1e-9, "a discount on a group the channel does not serve must not lower the sell factor")
}

// 保本下限带上各字段当前的原始 option 值，作为改价请求的 expected：只覆盖下限里的字段，
// 平台没配置的字段不出现（expected 传 null）。
func TestRepairFloorCarriesCurrentOptionValues(t *testing.T) {
	platform := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(4),
		Output: floatPointer(12),
		Lanes:  []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(1)}},
		// image_ratio 不是这一行的下限字段，不该带出去；completion_ratio 由内置规则决定时映射表里没有。
		optionFields: map[string]float64{"model_ratio": 2, "cache_ratio": 0.25, "image_ratio": 9},
	}
	source := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(6),
		Output: floatPointer(12),
		Lanes:  []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(1)}},
	}

	floor := repairFloorFor("current-model", platform, source, 1.0)
	require.NotNil(t, floor)
	require.Contains(t, floor.Fields, "completion_ratio")
	require.Equal(t, map[string]float64{"model_ratio": 2, "cache_ratio": 0.25}, floor.Current)
}
