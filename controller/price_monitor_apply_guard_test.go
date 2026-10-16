package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 改价接口的护栏：补全倍率被系统锁定的模型、字段必须与计费模式一致、不接受删除（null）。
// 设计见 docs/design/price-monitor-inline-repair-floor.md。

const lockedCompletionModel = "claude-sonnet-4-20250514"

// keepPricingRatios 保存并恢复进程内的倍率表：apply_price 成功后会经 updateOptionMap
// 改写全局 modelRatioMap / completionRatioMap。
func keepPricingRatios(t *testing.T) {
	t.Helper()
	prevModel := ratio_setting.ModelRatio2JSONString()
	prevCompletion := ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(prevModel))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(prevCompletion))
	})
}

// seedPricingRatios 同时写 option 表与进程内倍率表，让保本复算读到的「当前值」与库里一致。
func seedPricingRatios(t *testing.T, modelName, modelRatio, completionRatio string) {
	t.Helper()
	seedApplyPriceOption(t, "ModelRatio", `{"`+modelName+`":`+modelRatio+`}`)
	seedApplyPriceOption(t, "CompletionRatio", `{"`+modelName+`":`+completionRatio+`}`)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"`+modelName+`":`+modelRatio+`}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"`+modelName+`":`+completionRatio+`}`))
}

func applyPriceSnapshotWithFloor(modelName string, floor *PriceMonitorRepairFloor) PriceMonitorSnapshot {
	return PriceMonitorSnapshot{
		CheckedAt: 100,
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "channel-a", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{{
			Model: modelName,
			Prices: map[string]PriceMonitorPriceCell{
				priceMonitorPlatformKey: tokenCell(3, 15),
				"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(3), Output: floatPointer(24), Different: true},
			},
			RepairFloor: floor,
		}},
	}
}

func completionFloor(fieldFloor, displayFloor float64) *PriceMonitorRepairFloor {
	return &PriceMonitorRepairFloor{
		Mode:    priceMonitorModeToken,
		Fields:  map[string]float64{"completion_ratio": fieldFloor},
		Display: map[string]float64{"completion_ratio": displayFloor},
		Binding: map[string]string{"completion_ratio": "channel-a"},
	}
}

// 锁定模型的保本价：输出侧的要求折算进 input，不产出改了也不生效的 completion_ratio；按它改完就不再亏。
func TestRepairFloor_LockedCompletionRaisesInputAndClearsVerdict(t *testing.T) {
	platform := tokenCell(1, 5) // 锁定倍率 5：输出 = 输入 × 5
	source := tokenCell(1, 10)  // 上游输出成本 10 → 需要输入 2

	floor := repairFloorFor(lockedCompletionModel, platform, source, 1.0)
	require.NotNil(t, floor)
	_, hasCompletion := floor.Fields["completion_ratio"]
	assert.False(t, hasCompletion)
	assert.InDelta(t, 1, floor.Fields["model_ratio"], 1e-9, "input 2 → model_ratio 1")

	input := floor.Display["model_ratio"]
	factor, ok := priceMonitorMeasuredFactor(tokenCell(input, input*5), source)
	require.True(t, ok)
	assert.LessOrEqual(t, factor, 1.0+1e-9, "the floor must clear the verdict")
}

// 输入侧本身更紧时，锁定不应把 input 压低。
func TestRepairFloor_LockedCompletionKeepsHigherInput(t *testing.T) {
	floor := repairFloorFor(lockedCompletionModel, tokenCell(1, 5), tokenCell(4, 10), 1.0)
	require.NotNil(t, floor)
	assert.InDelta(t, 4, floor.Display["model_ratio"], 1e-9)
}

// 平台单元格的输出价取计费实际用的补全倍率：锁定模型即使映射表里写了值也按锁定倍率计；
// 改价用的 expected 仍是映射表里的原始值。
func TestPriceMonitorPlatformCellUsesBilledCompletionRatio(t *testing.T) {
	data := map[string]any{
		"model_ratio":      map[string]any{lockedCompletionModel: 1.5, "unlocked-probe-model": 1.0},
		"completion_ratio": map[string]any{lockedCompletionModel: 1.0, "unlocked-probe-model": 2.0},
	}
	locked, ok := priceMonitorCell(data, lockedCompletionModel)
	require.True(t, ok)
	locked = priceMonitorBilledPlatformCell(locked, data, lockedCompletionModel)
	require.NotNil(t, locked.Output)
	assert.InDelta(t, 3*5, *locked.Output, 1e-9, "a locked completion ratio is what billing uses")
	assert.Equal(t, map[string]float64{"model_ratio": 1.5, "completion_ratio": 1}, locked.optionFields)

	unlocked, ok := priceMonitorCell(data, "unlocked-probe-model")
	require.True(t, ok)
	unlocked = priceMonitorBilledPlatformCell(unlocked, data, "unlocked-probe-model")
	assert.InDelta(t, 4, *unlocked.Output, 1e-9, "a configured, unlocked ratio is used as is")
}

// 锁定模型的保本下限：输出下限折算进 model_ratio，由决定它的渠道署名；不单列 completion_ratio。
func TestRepairFloor_LockedCompletionFoldsIntoModelRatio(t *testing.T) {
	info := ratio_setting.GetCompletionRatioInfo(lockedCompletionModel)
	require.True(t, info.Locked, "precondition")
	require.InDelta(t, 5, info.Ratio, 1e-9, "precondition")

	headers := floorHeaders("channel-a", "channel-b")
	contexts := map[string]priceMonitorLossContext{
		"channel-a": {ChannelId: 1, SellFactor: 1.0, Valid: true},
		"channel-b": {ChannelId: 2, SellFactor: 1.0, Valid: true},
	}
	items := []PriceMonitorMatrixItem{
		{
			// 输出侧更紧：40/5 = 8 > 输入下限 3。
			Model: lockedCompletionModel,
			Prices: map[string]PriceMonitorPriceCell{
				priceMonitorPlatformKey: tokenCell(3, 15),
				"channel-a":             tokenCell(3, 40),
			},
		},
		{
			// 输入侧更紧：输入下限 9 > 40/5 = 8。
			Model: lockedCompletionModel,
			Prices: map[string]PriceMonitorPriceCell{
				priceMonitorPlatformKey: tokenCell(3, 15),
				"channel-a":             tokenCell(3, 40),
				"channel-b":             tokenCell(9, 10),
			},
		},
	}

	applyPriceMonitorRepairFloors(headers, items, contexts)

	outputBound := items[0].RepairFloor
	require.NotNil(t, outputBound)
	_, hasCompletion := outputBound.Fields["completion_ratio"]
	assert.False(t, hasCompletion, "a locked completion ratio cannot be repaired, so it must not be offered")
	assert.InDelta(t, 4, outputBound.Fields["model_ratio"], 1e-9, "input must reach 40/5 = 8")
	assert.InDelta(t, 8, outputBound.Display["model_ratio"], 1e-9)
	assert.Equal(t, "channel-a", outputBound.Binding["model_ratio"])

	inputBound := items[1].RepairFloor
	require.NotNil(t, inputBound)
	assert.InDelta(t, 4.5, inputBound.Fields["model_ratio"], 1e-9)
	assert.Equal(t, "channel-b", inputBound.Binding["model_ratio"])
}
