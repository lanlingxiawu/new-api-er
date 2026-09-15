package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

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
func TestRepairFloor_NoComparableChannelsProducesNothing(t *testing.T) {
	headers := floorHeaders("only")
	items := []PriceMonitorMatrixItem{{
		Model: "m",
		Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: tokenCell(2, 2),
			"only":                  {Mode: priceMonitorModeToken, UnavailableReason: "missing"},
		},
	}}
	applyPriceMonitorRepairFloors(headers, items, map[string]priceMonitorLossContext{
		"only": {ChannelId: 1, SellFactor: 1, Valid: true},
	})
	assert.Nil(t, items[0].RepairFloor)
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

// withCurrentPricing 设置某模型**当前生效**的 model_ratio / completion_ratio 并在
// 用例结束后还原。校验读的是 ratio_setting 的实时值（不是巡检快照），所以必须
// 从这里驱动，不能只造快照。
func withCurrentPricing(t *testing.T, modelName string, modelRatio, completionRatio float64) {
	t.Helper()
	originModel := ratio_setting.ModelRatio2JSONString()
	originCompletion := ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(originModel)
		_ = ratio_setting.UpdateCompletionRatioByJSONString(originCompletion)
	})

	models := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(originModel, &models))
	models[modelName] = modelRatio
	encoded, err := common.Marshal(models)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encoded)))

	completions := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(originCompletion, &completions))
	completions[modelName] = completionRatio
	encoded, err = common.Marshal(completions)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(string(encoded)))
}

func floorFixture() *PriceMonitorRepairFloor {
	// 平台 input=20 output=20（model_ratio 10, completion_ratio 1）。
	// 上游要求 output 展示价不低于 10。
	return &PriceMonitorRepairFloor{
		Mode:    priceMonitorModeToken,
		Fields:  map[string]float64{"model_ratio": 1, "completion_ratio": 0.5},
		Display: map[string]float64{"model_ratio": 2, "completion_ratio": 10},
		Binding: map[string]string{"model_ratio": "chan", "completion_ratio": "chan"},
	}
}

// #3：只提交 model_ratio、把 input 从 20 砍到 2，completion_ratio 保持 1，
// 输出实际售价也掉到 2 —— 远低于 output 保本线 10。旧实现只看 item.Fields，
// completion_ratio 没被提交就整个跳过，接口会返回成功。
func TestFloorViolations_CatchesUnsubmittedKnockOn(t *testing.T) {
	withCurrentPricing(t, "m", 10, 1)
	ratio := 1.0 // input 20 -> 2
	got := priceMonitorFloorViolations(floorFixture(), "m", map[string]*float64{"model_ratio": &ratio})

	var hit *priceMonitorFloorViolation
	for i := range got {
		if got[i].Field == "completion_ratio" {
			hit = &got[i]
		}
	}
	require.NotNil(t, hit, "lowering input must flag completion_ratio even though it was not submitted")
	assert.InDelta(t, 5, hit.Floor, 1e-9, "output floor 10 / new input 2 = 5")
	assert.InDelta(t, 1, hit.Value, 1e-9, "its effective value is the unchanged current ratio")
	assert.False(t, hit.Submitted, "it must be reported as a knock-on, not as a value the admin typed")
}

// 同样的提交，如果 completion_ratio 一并抬到 5，就不该报违规。
func TestFloorViolations_AcceptsCompensatedSubmit(t *testing.T) {
	withCurrentPricing(t, "m", 10, 1)
	ratio, comp := 1.0, 5.0
	got := priceMonitorFloorViolations(floorFixture(), "m",
		map[string]*float64{"model_ratio": &ratio, "completion_ratio": &comp})
	for _, v := range got {
		assert.NotEqual(t, "completion_ratio", v.Field,
			"raising completion_ratio alongside input must clear the knock-on")
	}
}

// #4：没提交 model_ratio 时，基准必须是**当前生效**倍率，不是巡检快照里的旧值。
// 平台价已被改小（10 -> 1，即 input 20 -> 2）之后，completion_ratio=1 应当被判违规。
func TestFloorViolations_UsesCurrentPricingNotSnapshot(t *testing.T) {
	withCurrentPricing(t, "m", 1, 1) // input 已经变成 2
	got := priceMonitorFloorViolations(floorFixture(), "m", map[string]*float64{})

	var hit *priceMonitorFloorViolation
	for i := range got {
		if got[i].Field == "completion_ratio" {
			hit = &got[i]
		}
	}
	require.NotNil(t, hit,
		"the floor must be recomputed from the CURRENT platform price, not the stale snapshot")
	assert.InDelta(t, 5, hit.Floor, 1e-9, "10 / current input 2 = 5")
}

func TestFloorViolations_NoFloorMeansNoJudgement(t *testing.T) {
	withCurrentPricing(t, "m", 10, 1)
	assert.Empty(t, priceMonitorFloorViolations(nil, "m", map[string]*float64{}))
	assert.Empty(t, priceMonitorFloorViolations(&PriceMonitorRepairFloor{Mode: priceMonitorModeToken}, "m", nil))
}

func TestFloorViolations_AtExactlyTheFloorIsAccepted(t *testing.T) {
	withCurrentPricing(t, "m", 10, 1)
	exact := 1.0 // model_ratio floor is exactly 1
	got := priceMonitorFloorViolations(floorFixture(), "m", map[string]*float64{"model_ratio": &exact})
	for _, v := range got {
		assert.NotEqual(t, "model_ratio", v.Field, "exactly at the floor is break-even, not a loss")
	}
}
