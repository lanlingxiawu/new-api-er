package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 改价保本下限。设计见 docs/design/price-monitor-inline-repair-floor.md。

func floorHeaders(keys ...string) []PriceMonitorSourceHeader {
	headers := []PriceMonitorSourceHeader{{Key: priceMonitorPlatformKey, Type: "platform"}}
	for _, key := range keys {
		headers = append(headers, PriceMonitorSourceHeader{Key: key, Type: priceSourceChannel})
	}
	return headers
}

func tokenCell(input, output float64) PriceMonitorPriceCell {
	return PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(input), Output: floatPointer(output)}
}

// 核心口径：floor = max over s ( C_s / g_s )。必须**先除各渠道自己的售价系数再取 max**。
//
// 用一组会让 max(C)/min(g) 给出错误答案的数据：
//
//	A: C=6  g=1.0 -> 需要 6
//	B: C=8  g=2.0 -> 需要 4      <- 报价最高，但约束最松
//
// 正确下限 = 6（由 A 决定）。错误写法 max(C)/min(g) = 8/1 = 8。
func TestRepairFloor_DividesEachChannelBySellFactorBeforeMax(t *testing.T) {
	headers := floorHeaders("channel-a", "channel-b")
	items := []PriceMonitorMatrixItem{{
		Model: "m",
		Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: tokenCell(2, 2),
			"channel-a":             tokenCell(6, 1),
			"channel-b":             tokenCell(8, 1),
		},
	}}
	contexts := map[string]priceMonitorLossContext{
		"channel-a": {ChannelId: 1, SellFactor: 1.0, Valid: true},
		"channel-b": {ChannelId: 2, SellFactor: 2.0, Valid: true},
	}

	applyPriceMonitorRepairFloors(headers, items, contexts)

	floor := items[0].RepairFloor
	require.NotNil(t, floor, "floor must be written back to the slice element, not a range copy")
	assert.InDelta(t, 6, floor.Display["model_ratio"], 1e-9, "input floor = max(6/1.0, 8/2.0) = 6")
	assert.InDelta(t, 3, floor.Fields["model_ratio"], 1e-9, "model_ratio = input/2")
	assert.Equal(t, "channel-a", floor.Binding["model_ratio"],
		"the binding must name the channel that actually set the floor, not the priciest one")
}

func TestRepairFloor_SkipsIncomparableSources(t *testing.T) {
	headers := floorHeaders("ok", "failed", "wrong-mode")
	items := []PriceMonitorMatrixItem{{
		Model: "m",
		Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: tokenCell(2, 2),
			"ok":                    tokenCell(4, 1),
			"failed":                {Mode: priceMonitorModeToken, Input: floatPointer(999), UnavailableReason: "source_failed"},
			"wrong-mode":            {Mode: priceMonitorModeRequest, Price: floatPointer(999)},
		},
	}}
	contexts := map[string]priceMonitorLossContext{
		"ok":         {ChannelId: 1, SellFactor: 1, Valid: true},
		"failed":     {ChannelId: 2, SellFactor: 1, Valid: true},
		"wrong-mode": {ChannelId: 3, SellFactor: 1, Valid: true},
	}

	applyPriceMonitorRepairFloors(headers, items, contexts)

	require.NotNil(t, items[0].RepairFloor)
	assert.InDelta(t, 4, items[0].RepairFloor.Display["model_ratio"], 1e-9,
		"unavailable and mode-mismatched sources must not raise the floor")
}

// 没有任何可比渠道时不产出下限——给 0 会被误读成「随便填都安全」。
// Without comparable channels there is no break-even floor, but the row can still
// be repriced (against the official price or by hand), so the pricing data is there.
func TestRepairFloor_NoComparableChannelsHasNoFloorButCanBeRepriced(t *testing.T) {
	headers := floorHeaders("only")
	platform := tokenCell(2, 2)
	platform.optionFields = map[string]float64{"model_ratio": 1}
	items := []PriceMonitorMatrixItem{{
		Model: "m",
		Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"only":                  {Mode: priceMonitorModeToken, UnavailableReason: "missing"},
		},
	}}
	applyPriceMonitorRepairFloors(headers, items, map[string]priceMonitorLossContext{
		"only": {ChannelId: 1, SellFactor: 1, Valid: true},
	})
	require.NotNil(t, items[0].RepairFloor)
	assert.Empty(t, items[0].RepairFloor.Fields, "no floor")
	assert.Equal(t, map[string]float64{"model_ratio": 1}, items[0].RepairFloor.Current)
	assert.Empty(t, items[0].RepairFloor.Highest)
	assert.Empty(t, items[0].RepairFloor.Lowest)
}

// The official price and the channel range are offered as reference prices.
// The official price still never drives the floor (see below).
func TestRepairFloor_ReferencePrices(t *testing.T) {
	headers := []PriceMonitorSourceHeader{
		{Key: priceMonitorPlatformKey, Type: "platform"},
		{Key: "official", Type: priceSourceOfficial},
		{Key: "cheap", Type: priceSourceChannel},
		{Key: "dear", Type: priceSourceChannel},
	}
	items := []PriceMonitorMatrixItem{{
		Model: "m",
		Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: tokenCell(2, 2),
			"official":              tokenCell(3, 2),
			"cheap":                 tokenCell(1, 2),
			"dear":                  tokenCell(5, 2),
		},
	}}
	applyPriceMonitorRepairFloors(headers, items, map[string]priceMonitorLossContext{
		"cheap": {ChannelId: 1, SellFactor: 1, Valid: true},
		"dear":  {ChannelId: 2, SellFactor: 1, Valid: true},
	})
	floor := items[0].RepairFloor
	require.NotNil(t, floor)
	assert.InDelta(t, 3, floor.Official["model_ratio"], 1e-9)
	assert.InDelta(t, 1, floor.Lowest["model_ratio"], 1e-9)
	assert.InDelta(t, 5, floor.Highest["model_ratio"], 1e-9)
	assert.InDelta(t, 5, floor.Display["model_ratio"], 1e-9)
}

// 官方价是对比基准而非采购成本，不得抬高下限。
func TestRepairFloor_IgnoresOfficialSource(t *testing.T) {
	headers := []PriceMonitorSourceHeader{
		{Key: priceMonitorPlatformKey, Type: "platform"},
		{Key: "official", Type: priceSourceOfficial},
		{Key: "chan", Type: priceSourceChannel},
	}
	items := []PriceMonitorMatrixItem{{
		Model: "m",
		Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: tokenCell(2, 2),
			"official":              tokenCell(50, 1),
			"chan":                  tokenCell(4, 1),
		},
	}}
	applyPriceMonitorRepairFloors(headers, items, map[string]priceMonitorLossContext{
		"official": {ChannelId: 0, SellFactor: 1, Valid: true},
		"chan":     {ChannelId: 1, SellFactor: 1, Valid: true},
	})
	require.NotNil(t, items[0].RepairFloor)
	assert.InDelta(t, 4, items[0].RepairFloor.Display["model_ratio"], 1e-9,
		"the official preset must not drive the break-even floor")
}

// ── 合并生效价格的校验（审计 #3 / #4）──────────────────────────────────────
