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

// 锁定模型的 completion_ratio 计费时不生效：写进去只会让接口谎报「已应用」。必须整单拒绝、一个字节不写。
func TestApplyPriceMonitorPriceRejectsLockedCompletionRatio(t *testing.T) {
	require.True(t, ratio_setting.GetCompletionRatioInfo(lockedCompletionModel).Locked, "precondition")
	keepPricingRatios(t)
	useApplyPriceEnv(t, applyPriceSnapshotWithFloor(lockedCompletionModel, completionFloor(8, 24)))
	seedPricingRatios(t, lockedCompletionModel, "1.5", "5")

	_, response := performApplyPrice(t, `{"checked_at":100,"pricing_version":0,"items":[{"model":"`+lockedCompletionModel+`",`+
		`"fields":{"model_ratio":4,"completion_ratio":10},"expected":{"model_ratio":1.5,"completion_ratio":5}}]}`)

	require.Equal(t, false, response["success"])
	assert.Contains(t, response["message"], lockedCompletionModel, "the message must name the model so the admin knows which row to fix")
	completion, _ := applyPriceOptionValue(t, "CompletionRatio", lockedCompletionModel)
	assert.InDelta(t, 5, completion, 1e-9)
	ratio, _ := applyPriceOptionValue(t, "ModelRatio", lockedCompletionModel)
	assert.InDelta(t, 1.5, ratio, 1e-9, "the whole request is rejected, including the editable field")
}

// 锁定模型改 model_ratio 是唯一有效的修法，必须照常放行。
func TestApplyPriceMonitorPriceAcceptsModelRatioForLockedModel(t *testing.T) {
	keepPricingRatios(t)
	useApplyPriceEnv(t, applyPriceSnapshotWithFloor(lockedCompletionModel, completionFloor(8, 24)))
	seedPricingRatios(t, lockedCompletionModel, "1.5", "5")

	// model_ratio 2.4 → input 4.8；锁定倍率 5 → 输出 24，恰好达到保本下限。
	_, response := performApplyPrice(t, `{"checked_at":100,"pricing_version":0,"items":[{"model":"`+lockedCompletionModel+`",`+
		`"fields":{"model_ratio":2.4},"expected":{"model_ratio":1.5}}]}`)

	require.Equal(t, true, response["success"], response["message"])
	assert.InDelta(t, 24, ratio_setting.GetCompletionRatio(lockedCompletionModel)*2.4*2, 1e-9)
}

// 不接受删除（null）；字段必须与平台计费模式一致。被拒的请求一个字节都不写。
func TestApplyPriceMonitorPriceRejectsNullAndModeMismatchedFields(t *testing.T) {
	keepPricingRatios(t)
	useApplyPriceEnv(t, applyPriceSnapshotWithFloor("gpt-4o", nil))
	seedPricingRatios(t, "gpt-4o", "1", "8")

	for name, fields := range map[string]string{
		"deleting an override":                   `{"completion_ratio":null}`,
		"per-request price on a per-token model": `{"model_price":0.01}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, response := performApplyPrice(t, `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o","fields":`+fields+`}]}`)
			require.Equal(t, false, response["success"])
		})
	}
	completion, _ := applyPriceOptionValue(t, "CompletionRatio", "gpt-4o")
	assert.InDelta(t, 8, completion, 1e-9)
	_, hasPrice := applyPriceOptionValue(t, "ModelPrice", "gpt-4o")
	assert.False(t, hasPrice, "a per-token model must not be switched to per-request billing")
}

// 按次计费的模型只能改 model_price：比值字段写进去根本不生效。
func TestApplyPriceMonitorPriceRejectsRatioFieldsOnPerRequestModel(t *testing.T) {
	useApplyPriceEnv(t, applyPriceSnapshot(100, priceMonitorModeRequest))
	seedApplyPriceOption(t, "ModelRatio", `{"gpt-4o":1}`)

	_, response := performApplyPrice(t, `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o","fields":{"model_ratio":2}}]}`)
	require.Equal(t, false, response["success"])
	value, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	assert.InDelta(t, 1, value, 1e-9)
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

// 保本复算在合并后的完整价格上判：只把 input 降下来、不动 completion_ratio，输出价同样被拖到
// 线下，这一项必须报出来（并标明不是管理员直接改的）。
func TestFloorViolations_UnsubmittedRatioDraggedDownByInput(t *testing.T) {
	keepPricingRatios(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gpt-4o":8}`))

	// 当前 input 2、completion 8 → 输出 16 ≥ 保本 12，不提交任何字段时不违规。
	assert.Empty(t, priceMonitorFloorViolations(completionFloor(6, 12), "gpt-4o", map[string]*float64{}))

	// model_ratio 0.5 → input 1：completion 8 → 输出 8 < 12，completion 的下限变成 12 / 1 = 12。
	violations := priceMonitorFloorViolations(completionFloor(6, 12), "gpt-4o",
		map[string]*float64{"model_ratio": floatPointer(0.5)})
	require.Len(t, violations, 1)
	assert.Equal(t, "completion_ratio", violations[0].Field)
	assert.InDelta(t, 8, violations[0].Value, 1e-9)
	assert.InDelta(t, 12, violations[0].Floor, 1e-9)
	assert.False(t, violations[0].Submitted, "the admin did not touch it; the lowered input dragged it down")
}
