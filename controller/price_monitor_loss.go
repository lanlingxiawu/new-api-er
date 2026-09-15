package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

const (
	priceMonitorLossKindMeasured   = "measured"
	priceMonitorLossKindConfigured = "configured"
)

// priceMonitorLossContext 是一个渠道来源参与亏损判定所需的、与模型无关的上下文。
// 每轮巡检按渠道计算一次，供该渠道下所有模型复用。
type priceMonitorLossContext struct {
	ChannelId  int
	SellFactor float64
	Configured float64
	Valid      bool
}

// priceMonitorSellFactor 求「该渠道流量可能拿到的最低售价系数」。
//
// 必须走 ratio_setting.ResolveGroupRatio，而不是自行 min(GetGroupRatio, GetGroupGroupRatio)，
// 否则优先级会与实际计费不一致。第一个参数固定传 nil：本判定不含用户专属倍率，
// 覆盖范围由 UI 显式标注（见 docs/design/price-monitor-loss-detection-and-price-repair.md §3.3）。
//
// usingGroups 只取该渠道实际服务的分组——遍历全部分组组合会把用户根本用不到的组合
// 算进最小值，制造误报。ratio 为 0 表示免费分组，与 calcCostQuota 中 groupRatio == 0
// 视为免费赠送的处理一致，跳过。
func priceMonitorSellFactor(usingGroups []string, userGroups []string) (float64, bool) {
	sellFactor, found := 0.0, false
	for _, usingGroup := range usingGroups {
		if usingGroup == "" {
			continue
		}
		candidates := make([]string, 0, len(userGroups)+1)
		candidates = append(candidates, "")
		candidates = append(candidates, userGroups...)
		for _, userGroup := range candidates {
			ratio, _ := ratio_setting.ResolveGroupRatio(nil, userGroup, usingGroup)
			if ratio <= 0 {
				continue
			}
			if !found || ratio < sellFactor {
				sellFactor, found = ratio, true
			}
		}
	}
	return sellFactor, found
}

// buildPriceMonitorLossContexts 为每个渠道来源建立判定上下文。sourceNames 把渠道 ID
// 映射到矩阵里的来源键，与 buildPriceMonitorMatrix 使用的键保持一致。
func buildPriceMonitorLossContexts(channels []*model.Channel, sourceNames map[int]string) map[string]priceMonitorLossContext {
	contexts := make(map[string]priceMonitorLossContext, len(channels))
	userGroups := priceMonitorConfiguredUserGroups()
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		sourceName, ok := sourceNames[channel.Id]
		if !ok || sourceName == "" {
			continue
		}
		sellFactor, found := priceMonitorSellFactor(channel.GetGroups(), userGroups)
		if !found {
			// 渠道没有分组或分组倍率全为 0，退化为现状口径。
			sellFactor = 1
		}
		contexts[sourceName] = priceMonitorLossContext{
			ChannelId:  channel.Id,
			SellFactor: sellFactor,
			Configured: model.GetChannelCostRatio(channel.Id),
			Valid:      true,
		}
	}
	return contexts
}

func priceMonitorConfiguredUserGroups() []string {
	groupGroupRatio := ratio_setting.GetGroupGroupRatioCopy()
	userGroups := make([]string, 0, len(groupGroupRatio))
	for userGroup := range groupGroupRatio {
		if userGroup != "" {
			userGroups = append(userGroups, userGroup)
		}
	}
	return userGroups
}

// priceMonitorRatioMax 逐维度累计「上游价 / 平台价」的最大值，并记住是否出现过可比维度。
type priceMonitorRatioMax struct {
	value float64
	found bool
}

// observe 跳过只有一侧配置的维度，以及平台值为 0 的维度（无法作除数）。
func (accumulator *priceMonitorRatioMax) observe(platform, source *float64) {
	if platform == nil || source == nil || *platform <= 0 {
		return
	}
	ratio := *source / *platform
	if !accumulator.found || ratio > accumulator.value {
		accumulator.value, accumulator.found = ratio, true
	}
}

func (accumulator *priceMonitorRatioMax) observeLanes(platformLanes, sourceLanes []PriceMonitorPriceLane) {
	if len(platformLanes) == 0 || len(sourceLanes) == 0 {
		return
	}
	platformByKey := make(map[string]*float64, len(platformLanes))
	for _, lane := range platformLanes {
		platformByKey[lane.Key] = lane.Price
	}
	for _, lane := range sourceLanes {
		if platformPrice, exists := platformByKey[lane.Key]; exists {
			accumulator.observe(platformPrice, lane.Price)
		}
	}
}

// priceMonitorMeasuredFactor 求「上游报价相对平台列表价的最高倍数」。
// 可比维度与不可比情形完全复用 priceMonitorSourceAbovePlatform 的规则，不另立一套。
func priceMonitorMeasuredFactor(platform, source PriceMonitorPriceCell) (float64, bool) {
	if priceMonitorComparisonIgnores(platform) || priceMonitorComparisonIgnores(source) || platform.Mode != source.Mode {
		return 0, false
	}
	accumulator := priceMonitorRatioMax{}
	switch platform.Mode {
	case priceMonitorModeToken:
		accumulator.observe(platform.Input, source.Input)
		accumulator.observe(platform.Output, source.Output)
		accumulator.observeLanes(priceMonitorComparablePlatformLanes(platform.Lanes), source.Lanes)
	case priceMonitorModeRequest:
		accumulator.observe(platform.Price, source.Price)
	case priceMonitorModeExpression:
		if platform.Dynamic || source.Dynamic || len(platform.Tiers) == 0 || len(platform.Tiers) != len(source.Tiers) {
			return 0, false
		}
		for index, platformTier := range platform.Tiers {
			sourceTier := source.Tiers[index]
			if platformTier.ConditionVariable != sourceTier.ConditionVariable || platformTier.ConditionOperator != sourceTier.ConditionOperator || !priceMonitorOptionalPriceEqual(platformTier.ConditionValue, sourceTier.ConditionValue) {
				return 0, false
			}
			accumulator.observe(floatPointer(platformTier.Input), floatPointer(sourceTier.Input))
			accumulator.observe(floatPointer(platformTier.Output), floatPointer(sourceTier.Output))
			accumulator.observeLanes(platformTier.Lanes, sourceTier.Lanes)
		}
	default:
		return 0, false
	}
	return accumulator.value, accumulator.found
}

// priceMonitorLossKinds 判定命中的风险类型。
//
// 两类风险的修复动作完全相反，因此绝不能合成一个 max()：
//   - measured：上游报价 C 固定，抬高平台价 P 能真实止损。
//   - configured：cost = revenue / g * r，成本正比于收入，P 是 profit = P*(g-r) 的公因子，
//     改价不改变毛利率，只会等比放大绝对亏损额。正确动作是调分组倍率或修正成本系数。
func priceMonitorLossKinds(measured, configured, sellFactor float64) []string {
	kinds := make([]string, 0, 2)
	if measured > sellFactor && !nearlyEqual(measured, sellFactor) {
		kinds = append(kinds, priceMonitorLossKindMeasured)
	}
	if configured > sellFactor && !nearlyEqual(configured, sellFactor) {
		kinds = append(kinds, priceMonitorLossKindConfigured)
	}
	if len(kinds) == 0 {
		return nil
	}
	return kinds
}

// applyPriceMonitorLossVerdicts 在矩阵构建完成后单独走一遍，把亏损判定写进渠道单元格。
// 独立一趟而不是塞进 buildPriceMonitorMatrix，是为了让判定可以单独测试，
// 也确保 above_platform 的既有代码路径一个字节都不用动。
func applyPriceMonitorLossVerdicts(headers []PriceMonitorSourceHeader, items []PriceMonitorMatrixItem, contexts map[string]priceMonitorLossContext) {
	for _, item := range items {
		platform, ok := item.Prices[priceMonitorPlatformKey]
		if !ok {
			continue
		}
		for _, header := range headers {
			if header.Type != priceSourceChannel {
				continue
			}
			context, hasContext := contexts[header.Key]
			if !hasContext || !context.Valid || context.SellFactor <= 0 {
				continue
			}
			source, hasCell := item.Prices[header.Key]
			if !hasCell {
				continue
			}
			measured, comparable := priceMonitorMeasuredFactor(platform, source)
			if !comparable {
				continue
			}
			kinds := priceMonitorLossKinds(measured, context.Configured, context.SellFactor)
			if len(kinds) == 0 {
				continue
			}
			source.LossKinds = kinds
			source.SellFactor = floatPointer(context.SellFactor)
			source.MeasuredFactor = floatPointer(measured)
			source.ConfiguredFactor = floatPointer(context.Configured)
			item.Prices[header.Key] = source
		}
	}
}

// priceMonitorLockedCompletionRatio 返回被系统硬编码锁定的补全倍率；未锁定返回 0。
//
// 锁定的模型计费时不读 CompletionRatio 里的覆盖值，输出价只能随 input 联动。建议价与
// 保本下限都必须把输出侧的要求折算进 model_ratio，而不是给出一个改了也不生效的
// completion_ratio——改价接口也会拒绝这种提交（见 ApplyPriceMonitorPrice）。
func priceMonitorLockedCompletionRatio(modelName string) float64 {
	info := ratio_setting.GetCompletionRatioInfo(modelName)
	if !info.Locked || info.Ratio <= 0 {
		return 0
	}
	return info.Ratio
}

// ── 改价保本下限 ──────────────────────────────────────────────────────────

// priceMonitorFloorAccumulator 逐字段累计「该字段的平台展示价至少要多少」。
//
// 与 priceMonitorRatioMax 的区别是它**不塌缩维度**：判定只需要一个标量（有没有亏），
// 而改价需要知道**具体哪一项**要抬到多少，否则管理员只能看到「整体没清干净」。
type priceMonitorFloorAccumulator struct {
	display map[string]float64
	binding map[string]string
}

func newPriceMonitorFloorAccumulator() *priceMonitorFloorAccumulator {
	return &priceMonitorFloorAccumulator{
		display: map[string]float64{},
		binding: map[string]string{},
	}
}

// observe 记录「渠道 source 在维度 key 上要求平台价不低于upstream/sellFactor」。
//
// 注意除的是**该渠道自己的** sellFactor：不同渠道服务不同分组、系数不同，
// 报价最高的渠道未必是约束最紧的那个，先除再取 max 才正确。
func (a *priceMonitorFloorAccumulator) observe(key string, upstream *float64, sellFactor float64, sourceKey string) {
	if upstream == nil || *upstream <= 0 || sellFactor <= 0 {
		return
	}
	required := *upstream / sellFactor
	if current, ok := a.display[key]; ok && current >= required {
		return
	}
	a.display[key] = required
	a.binding[key] = sourceKey
}

func (a *priceMonitorFloorAccumulator) observeLanes(platformLanes, sourceLanes []PriceMonitorPriceLane, sellFactor float64, sourceKey string) {
	if len(platformLanes) == 0 || len(sourceLanes) == 0 {
		return
	}
	// 只有平台也配置了的 lane 才参与：单侧有值不构成可比维度。
	platformHas := make(map[string]struct{}, len(platformLanes))
	for _, lane := range platformLanes {
		if lane.Price != nil {
			platformHas[lane.Key] = struct{}{}
		}
	}
	for _, lane := range sourceLanes {
		if _, ok := platformHas[lane.Key]; !ok {
			continue
		}
		a.observe(lane.Key, lane.Price, sellFactor, sourceKey)
	}
}

// priceMonitorFloorLaneField 把 lane key 映射回 option 字段名，与 priceMonitorRatioLanes 同源。
func priceMonitorFloorLaneField(key string) (string, bool) {
	for _, definition := range priceMonitorRatioLanes {
		if definition.key == key {
			return definition.field, true
		}
	}
	return "", false
}

// buildPriceMonitorRepairFloor 把逐维度的展示价下限换算成 option 字段值下限。
//
// 换算是 priceMonitorCell 的逆运算（input = model_ratio * 2，其余字段是相对 input 的比值）。
// 下限同时就是改价弹窗的「保本价」：它对这一行所有可比渠道取最严，只按单个渠道算的
// 建议价会在报价更高的另一个渠道上继续亏。
//
// 比值型字段（completion_ratio / lane 类）的下限依赖 input，这里用**下限自身的 input**
// 作为基准；前端在管理员改动 model_ratio 时会按输入框当前值实时重算（设计 §5.2）。
//
// lockedCompletion > 0 表示补全倍率被系统锁定：输出下限只能靠抬 input 满足，折算进
// model_ratio 下限，不再单列 completion_ratio。
func buildPriceMonitorRepairFloor(platform PriceMonitorPriceCell, acc *priceMonitorFloorAccumulator, lockedCompletion float64) *PriceMonitorRepairFloor {
	if len(acc.display) == 0 {
		return nil
	}
	floor := &PriceMonitorRepairFloor{
		Mode:    platform.Mode,
		Fields:  map[string]float64{},
		Display: map[string]float64{},
		Binding: map[string]string{},
	}
	bind := func(field, key string) {
		floor.Display[field] = acc.display[key]
		floor.Binding[field] = acc.binding[key]
	}
	switch platform.Mode {
	case priceMonitorModeRequest:
		if price, ok := acc.display[priceMonitorFloorKeyPrice]; ok {
			floor.Fields["model_price"] = price
			bind("model_price", priceMonitorFloorKeyPrice)
		}
	case priceMonitorModeToken:
		input, hasInput := acc.display[priceMonitorFloorKeyInput]
		inputKey := priceMonitorFloorKeyInput
		output, hasOutput := acc.display[priceMonitorFloorKeyOutput]
		if hasOutput && lockedCompletion > 0 {
			if needed := output / lockedCompletion; !hasInput || needed > input {
				input, hasInput, inputKey = needed, true, priceMonitorFloorKeyOutput
			}
		}
		if hasInput {
			floor.Fields["model_ratio"] = input / 2
			floor.Display["model_ratio"] = input
			floor.Binding["model_ratio"] = acc.binding[inputKey]
		}
		// 比值型字段需要一个 input 基准。没有 input 下限时退回平台当前 input，
		// 仍然算不出就跳过——给 0 会被误读成「随便填都安全」。
		base := input
		if !hasInput && platform.Input != nil {
			base = *platform.Input
		}
		if base > 0 {
			if hasOutput && lockedCompletion <= 0 {
				floor.Fields["completion_ratio"] = output / base
				bind("completion_ratio", priceMonitorFloorKeyOutput)
			}
			for key, value := range acc.display {
				field, ok := priceMonitorFloorLaneField(key)
				if !ok {
					continue
				}
				floor.Fields[field] = value / base
				bind(field, key)
			}
			// 1 小时缓存写入没有独立配置项，计费价 = 5 分钟缓存写入价 × 系数：它的下限只能靠
			// create_cache_ratio 满足，折算过去并与 5 分钟的下限取大。
			if value, ok := acc.display[priceMonitorLaneCacheWrite1h]; ok {
				required := value / priceMonitorCacheWrite1hMultiplier
				if current, exists := floor.Display["create_cache_ratio"]; !exists || required > current {
					floor.Fields["create_cache_ratio"] = required / base
					floor.Display["create_cache_ratio"] = required
					floor.Binding["create_cache_ratio"] = acc.binding[priceMonitorLaneCacheWrite1h]
				}
			}
		}
	}
	if len(floor.Fields) == 0 {
		return nil
	}
	for field := range floor.Fields {
		if value, ok := platform.optionFields[field]; ok {
			if floor.Current == nil {
				floor.Current = make(map[string]float64, len(floor.Fields))
			}
			floor.Current[field] = value
		}
	}
	return floor
}

const (
	priceMonitorFloorKeyInput  = "__input"
	priceMonitorFloorKeyOutput = "__output"
	priceMonitorFloorKeyPrice  = "__price"
)

// priceMonitorHighestPrices 把按原始报价累计的最高值映射到改价字段名（展示价）。
// 1 小时缓存写入没有独立配置项，计费价 = 5 分钟写入价 × 系数，所以折算进 create_cache_ratio 取大。
func priceMonitorHighestPrices(acc *priceMonitorFloorAccumulator) map[string]float64 {
	highest := make(map[string]float64, len(acc.display))
	for key, value := range acc.display {
		switch key {
		case priceMonitorFloorKeyInput:
			highest["model_ratio"] = value
		case priceMonitorFloorKeyOutput:
			highest["completion_ratio"] = value
		case priceMonitorFloorKeyPrice:
			highest["model_price"] = value
		default:
			if field, ok := priceMonitorFloorLaneField(key); ok {
				highest[field] = value
			}
		}
	}
	if value, ok := acc.display[priceMonitorLaneCacheWrite1h]; ok {
		if required := value / priceMonitorCacheWrite1hMultiplier; required > highest["create_cache_ratio"] {
			highest["create_cache_ratio"] = required
		}
	}
	return highest
}

// applyPriceMonitorRepairFloors 为每个模型行计算改价保本下限。
//
// 与亏损判定分开一趟，是因为两者的取值范围不同：判定只关心**命中亏损**的渠道，
// 而下限必须扫过**所有可比渠道**——包括当前没亏的那些。只按命中的渠道算，
// 修完仍会亏在报价更高的另一个渠道上（设计 §2.1）。
func applyPriceMonitorRepairFloors(headers []PriceMonitorSourceHeader, items []PriceMonitorMatrixItem, contexts map[string]priceMonitorLossContext) {
	// 按下标遍历：RepairFloor 是结构体字段，写进 range 的值拷贝会被丢掉。
	// （判定那一趟能用值拷贝，只是因为它改的是 item.Prices —— map 是引用。）
	for i := range items {
		item := &items[i]
		platform, ok := item.Prices[priceMonitorPlatformKey]
		if !ok || priceMonitorComparisonIgnores(platform) {
			continue
		}
		// 阶梯表达式不支持行内改价，也就不产出下限。
		if platform.Mode != priceMonitorModeToken && platform.Mode != priceMonitorModeRequest {
			continue
		}
		// floor 按「报价 ÷ 售价系数」累计（保本），highest 按原始报价累计（改价弹窗的「最高价」）。
		acc := newPriceMonitorFloorAccumulator()
		highest := newPriceMonitorFloorAccumulator()
		observe := func(target *priceMonitorFloorAccumulator, source PriceMonitorPriceCell, sellFactor float64, sourceKey string) {
			switch platform.Mode {
			case priceMonitorModeRequest:
				if platform.Price != nil {
					target.observe(priceMonitorFloorKeyPrice, source.Price, sellFactor, sourceKey)
				}
			case priceMonitorModeToken:
				if platform.Input != nil {
					target.observe(priceMonitorFloorKeyInput, source.Input, sellFactor, sourceKey)
				}
				if platform.Output != nil {
					target.observe(priceMonitorFloorKeyOutput, source.Output, sellFactor, sourceKey)
				}
				target.observeLanes(priceMonitorComparablePlatformLanes(platform.Lanes), source.Lanes, sellFactor, sourceKey)
			}
		}
		for _, header := range headers {
			// 只看渠道：官方价是对比基准而非采购成本，计入会把下限抬到没有成本依据的高度。
			if header.Type != priceSourceChannel {
				continue
			}
			context, hasContext := contexts[header.Key]
			if !hasContext || !context.Valid || context.SellFactor <= 0 {
				continue
			}
			source, hasCell := item.Prices[header.Key]
			if !hasCell {
				continue
			}
			// 可比性完全复用判定侧的规则，不另立一套。
			if priceMonitorComparisonIgnores(source) || source.Mode != platform.Mode {
				continue
			}
			observe(acc, source, context.SellFactor, header.Key)
			observe(highest, source, 1, header.Key)
		}
		locked := priceMonitorLockedCompletionRatio(item.Model)
		floor := buildPriceMonitorRepairFloor(platform, acc, locked)
		if floor != nil {
			floor.Highest = priceMonitorHighestPrices(highest)
			floor.LockedCompletionRatio = locked
		}
		item.RepairFloor = floor
	}
}
