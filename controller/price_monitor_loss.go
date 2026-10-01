package controller

import (
	"strconv"

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
	// UpstreamRatio 是渠道密钥在上游的分组倍率（成本系数核对取得）；nil 表示未知。
	// 上游对我们的实际成本 = 上游列表价 × 它，实测亏损与保本下限都按实际成本算。
	UpstreamRatio *float64
}

// upstreamCostFactor 返回上游列表价到实际成本的系数；未知时为 1（按列表价，即原口径）。
func (context priceMonitorLossContext) upstreamCostFactor() float64 {
	if context.UpstreamRatio == nil {
		return 1
	}
	return *context.UpstreamRatio
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
// 映射到矩阵里的来源键，与 buildPriceMonitorMatrix 使用的键保持一致；channelCosts
// 以渠道 ID 为键提供上游分组倍率。
func buildPriceMonitorLossContexts(channels []*model.Channel, sourceNames map[int]string, channelCosts map[string]PriceMonitorChannelCost) map[string]priceMonitorLossContext {
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
		// 成本系数取核对结果里的同一个值，判定与核对不会各读一次、读到不同的值。
		configured := model.GetChannelCostRatio(channel.Id)
		cost, hasCost := channelCosts[strconv.Itoa(channel.Id)]
		if hasCost {
			configured = cost.CostRatio
		}
		contexts[sourceName] = priceMonitorLossContext{
			ChannelId:     channel.Id,
			SellFactor:    sellFactor,
			Configured:    configured,
			Valid:         true,
			UpstreamRatio: cost.UpstreamRatio,
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

// 亏损明细里非分项维度的键；分项维度沿用分项自己的键（cache_read 等）。
const (
	priceMonitorLossLineInput  = "input"
	priceMonitorLossLineOutput = "output"
	priceMonitorLossLinePrice  = "price"
)

// priceMonitorPriceDimension 是一项可比的价格维度：两边都配置了，且平台价 > 0（要作除数）。
// Tier 只在阶梯价时非空，是阶梯下标。
type priceMonitorPriceDimension struct {
	Tier     *int
	Key      string
	Platform float64
	Source   float64
}

// priceMonitorComparableDimensions 列出平台与来源可比的价格维度；第二个返回值为 false 表示
// 两者整体不可比（计费方式不同、动态表达式、阶梯结构不同等）。实测系数与亏损明细都基于它，
// 不可比情形复用 priceMonitorSourceAbovePlatform 的规则，不另立一套。
func priceMonitorComparableDimensions(platform, source PriceMonitorPriceCell) ([]priceMonitorPriceDimension, bool) {
	if priceMonitorComparisonIgnores(platform) || priceMonitorComparisonIgnores(source) || platform.Mode != source.Mode {
		return nil, false
	}
	var dimensions []priceMonitorPriceDimension
	add := func(tier *int, key string, platformPrice, sourcePrice *float64) {
		if platformPrice == nil || sourcePrice == nil || *platformPrice <= 0 {
			return
		}
		dimensions = append(dimensions, priceMonitorPriceDimension{Tier: tier, Key: key, Platform: *platformPrice, Source: *sourcePrice})
	}
	addLanes := func(tier *int, platformLanes, sourceLanes []PriceMonitorPriceLane) {
		platformByKey := make(map[string]*float64, len(platformLanes))
		for _, lane := range platformLanes {
			platformByKey[lane.Key] = lane.Price
		}
		for _, lane := range sourceLanes {
			if platformPrice, exists := platformByKey[lane.Key]; exists {
				add(tier, lane.Key, platformPrice, lane.Price)
			}
		}
	}
	switch platform.Mode {
	case priceMonitorModeToken:
		add(nil, priceMonitorLossLineInput, platform.Input, source.Input)
		add(nil, priceMonitorLossLineOutput, platform.Output, source.Output)
		addLanes(nil, priceMonitorComparablePlatformLanes(platform.Lanes), source.Lanes)
	case priceMonitorModeRequest:
		add(nil, priceMonitorLossLinePrice, platform.Price, source.Price)
	case priceMonitorModeExpression:
		if platform.Dynamic || source.Dynamic || len(platform.Tiers) == 0 || len(platform.Tiers) != len(source.Tiers) {
			return nil, false
		}
		for index, platformTier := range platform.Tiers {
			sourceTier := source.Tiers[index]
			if platformTier.ConditionVariable != sourceTier.ConditionVariable || platformTier.ConditionOperator != sourceTier.ConditionOperator || !priceMonitorOptionalPriceEqual(platformTier.ConditionValue, sourceTier.ConditionValue) {
				return nil, false
			}
			tier := index
			add(&tier, priceMonitorLossLineInput, floatPointer(platformTier.Input), floatPointer(sourceTier.Input))
			add(&tier, priceMonitorLossLineOutput, floatPointer(platformTier.Output), floatPointer(sourceTier.Output))
			addLanes(&tier, platformTier.Lanes, sourceTier.Lanes)
		}
	default:
		return nil, false
	}
	return dimensions, true
}

// priceMonitorMeasuredFactor 求「上游报价相对平台列表价的最高倍数」。
func priceMonitorMeasuredFactor(platform, source PriceMonitorPriceCell) (float64, bool) {
	dimensions, comparable := priceMonitorComparableDimensions(platform, source)
	if !comparable || len(dimensions) == 0 {
		return 0, false
	}
	highest := dimensions[0].Source / dimensions[0].Platform
	for _, dimension := range dimensions[1:] {
		highest = max(highest, dimension.Source/dimension.Platform)
	}
	return highest, true
}

// priceMonitorMeasuredLossLines 列出实测亏损的具体维度：上游实际成本（列表价 × 上游分组倍率）
// 高于我们的最低售价（平台价 × 最低售价倍率）的项，给页面直接显示两个价格。
// 判定条件与 priceMonitorLossKinds 的 measured 一致（按比值比较、同一个 nearlyEqual），
// 所以有实测亏损标记就一定有明细，反之亦然。
func priceMonitorMeasuredLossLines(platform, source PriceMonitorPriceCell, upstreamFactor, sellFactor float64) []PriceMonitorLossLine {
	dimensions, _ := priceMonitorComparableDimensions(platform, source)
	var lines []PriceMonitorLossLine
	for _, dimension := range dimensions {
		measured := dimension.Source / dimension.Platform * upstreamFactor
		if measured <= sellFactor || nearlyEqual(measured, sellFactor) {
			continue
		}
		lines = append(lines, PriceMonitorLossLine{
			Tier: dimension.Tier,
			Key:  dimension.Key,
			Cost: dimension.Source * upstreamFactor,
			Sell: dimension.Platform * sellFactor,
		})
	}
	return lines
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
			listFactor, comparable := priceMonitorMeasuredFactor(platform, source)
			if !comparable {
				continue
			}
			measured := listFactor * context.upstreamCostFactor()
			kinds := priceMonitorLossKinds(measured, context.Configured, context.SellFactor)
			if len(kinds) == 0 {
				continue
			}
			source.LossKinds = kinds
			source.LossLines = priceMonitorMeasuredLossLines(platform, source, context.upstreamCostFactor(), context.SellFactor)
			source.SellFactor = floatPointer(context.SellFactor)
			source.MeasuredFactor = floatPointer(measured)
			source.ConfiguredFactor = floatPointer(context.Configured)
			if context.UpstreamRatio != nil {
				source.UpstreamFactor = floatPointer(*context.UpstreamRatio)
			}
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

// observePriceMonitorFloor 把一个来源单元格在平台可比维度上的报价记进累加器（报价 ÷ sellFactor）。
func observePriceMonitorFloor(target *priceMonitorFloorAccumulator, platform, source PriceMonitorPriceCell, sellFactor float64, sourceKey string) {
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

// priceMonitorReferencePrices 把一个来源单元格按改价字段映射成展示价，供改价弹窗做参考价。
func priceMonitorReferencePrices(platform, source PriceMonitorPriceCell) map[string]float64 {
	acc := newPriceMonitorFloorAccumulator()
	observePriceMonitorFloor(acc, platform, source, 1, "")
	return priceMonitorHighestPrices(acc)
}

// applyPriceMonitorRepairFloors 为每个按量/按次计价的模型行计算改价数据：保本下限、各字段现值，
// 以及改价弹窗的参考价（官方价、渠道最低/最高价）。
//
// 保本下限与亏损判定分开一趟，是因为两者的取值范围不同：判定只关心**命中亏损**的渠道，
// 而下限必须扫过**所有可比渠道**——包括当前没亏的那些。只按命中的渠道算，
// 修完仍会亏在报价更高的另一个渠道上（设计 §2.1）。
//
// 没有可比渠道的行也产出改价数据（Fields 为空）：任何一行都能在巡检页改价（设计 §5.1），
// 而改价请求的并发校验需要 Current。
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
			observePriceMonitorFloor(target, platform, source, sellFactor, sourceKey)
		}
		var lowest, official map[string]float64
		for _, header := range headers {
			if header.Type == priceSourceOfficial {
				if source, hasCell := item.Prices[header.Key]; hasCell && !priceMonitorComparisonIgnores(source) && source.Mode == platform.Mode {
					official = priceMonitorReferencePrices(platform, source)
				}
				continue
			}
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
			// 保本价 = 实际成本 ÷ 售价系数 = 列表价 × 上游倍率 ÷ 售价系数，折成一个等效除数；
			// 上游免费分组（倍率 0）没有成本，不构成下限。「最高价」仍取原始列表价。
			if upstream := context.upstreamCostFactor(); upstream > 0 {
				observe(acc, source, context.SellFactor/upstream, header.Key)
			}
			observe(highest, source, 1, header.Key)
			for field, value := range priceMonitorReferencePrices(platform, source) {
				if lowest == nil {
					lowest = make(map[string]float64)
				}
				if current, seen := lowest[field]; !seen || value < current {
					lowest[field] = value
				}
			}
		}
		locked := priceMonitorLockedCompletionRatio(item.Model)
		floor := buildPriceMonitorRepairFloor(platform, acc, locked)
		if floor == nil {
			floor = &PriceMonitorRepairFloor{Mode: platform.Mode}
		}
		// Current 覆盖平台已配置的所有改价字段，而不只是有下限的字段：按官方价改一个没有下限的
		// 字段时，改价请求同样需要它作为 expected，否则会被当成"原本未配置"而冲突。
		if len(platform.optionFields) > 0 {
			floor.Current = make(map[string]float64, len(platform.optionFields))
			for field, value := range platform.optionFields {
				floor.Current[field] = value
			}
		}
		if highestPrices := priceMonitorHighestPrices(highest); len(highestPrices) > 0 {
			floor.Highest = highestPrices
		}
		floor.Lowest = lowest
		floor.Official = official
		floor.LockedCompletionRatio = locked
		item.RepairFloor = floor
	}
}
