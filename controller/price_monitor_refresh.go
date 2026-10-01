package controller

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 改价、改成本系数之后就地重算快照，不等下一轮巡检（设计 §5.3、§4）。

var (
	// priceMonitorRefreshMu 串行化就地重算：两个管理员同时改动时，后一个基于前一个的结果重算，
	// 而不是各自基于同一份旧快照、后写的覆盖先写的。
	priceMonitorRefreshMu sync.Mutex

	// priceMonitorRefreshPending 表示巡检进行中又有改动：那一轮读的是改动前的定价或成本系数，
	// 它保存后会覆盖这里的重算结果，所以巡检保存后再补算一次（runPriceMonitorCheck）。
	priceMonitorRefreshPending atomic.Bool

	errPriceMonitorSnapshotSuperseded = errors.New("price monitor snapshot superseded by a newer check")

	// 以下是接缝，测试替换为固定数据；生产读当前定价与渠道。
	priceMonitorRefreshPricingData = getLocalPricingSyncData
	priceMonitorRefreshChannels    = func() ([]*model.Channel, error) { return model.GetAllChannels(0, 0, true, false) }
	priceMonitorRefreshCostRatio   = model.GetChannelCostRatio
	priceMonitorRefreshConfigured  = priceMonitorCostRatioConfigured
)

// refreshPriceMonitorSnapshotInPlace 用当前平台定价与快照里已存的来源价格重算整份快照：
// 平台单元格、差异标记、亏损判定、保本下限、成本系数核对与统计。不发任何上游请求。
//
// 平台单元格必须整体重建：它的原始 option 值（保本下限的 Current，改价的并发校验依据）
// 不写进快照文件，从磁盘加载的快照里没有；而且其他页面改过的价格也应一并反映。
//
// 只在主节点执行：快照由主节点的巡检任务维护、存在主节点本地。其他节点返回 refreshed=false，
// 结果在下一轮巡检后刷新。
func refreshPriceMonitorSnapshotInPlace() (bool, error) {
	if !common.IsMasterNode {
		return false, nil
	}
	if priceMonitorRunning.Load() {
		priceMonitorRefreshPending.Store(true)
	}
	return recomputePriceMonitorSnapshotInPlace()
}

// recomputePriceMonitorSnapshotInPlace 执行重算与写回，不登记"待补算"。巡检保存后的补算调用它：
// 那时运行标志仍为真，走 refreshPriceMonitorSnapshotInPlace 会又把自己登记成待补算。
func recomputePriceMonitorSnapshotInPlace() (bool, error) {
	priceMonitorRefreshMu.Lock()
	defer priceMonitorRefreshMu.Unlock()

	store := getPriceMonitorStore()
	base := store.Get()
	// 升级前的快照缺少表头渠道 ID 等字段，按它重算会清掉所有渠道判定；等升级后的第一轮巡检。
	if base.CheckedAt == 0 || len(base.SourceHeaders) == 0 || base.MatrixVersion < priceMonitorMatrixVersion {
		return false, nil
	}
	next, err := recomputePriceMonitorSnapshot(base)
	if err != nil {
		return false, err
	}
	if err := store.SaveIfCheckedAt(base.CheckedAt, next); err != nil {
		if errors.Is(err, errPriceMonitorSnapshotSuperseded) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// recomputePriceMonitorSnapshot 返回重算后的新快照，不修改 base（base 的切片与 map 仍被读者共享）。
func recomputePriceMonitorSnapshot(base PriceMonitorSnapshot) (PriceMonitorSnapshot, error) {
	localData := priceMonitorRefreshPricingData()

	next := base
	next.MatrixItems = make([]PriceMonitorMatrixItem, len(base.MatrixItems))
	for i, item := range base.MatrixItems {
		next.MatrixItems[i] = recomputePriceMonitorRow(item, localData)
	}

	next.ChannelCosts = make(map[string]PriceMonitorChannelCost, len(base.ChannelCosts))
	for key, cost := range base.ChannelCosts {
		next.ChannelCosts[key] = cost
	}
	applyPriceMonitorChannelCostStatus(next.ChannelCosts, priceMonitorRefreshCostRatio, priceMonitorRefreshConfigured())

	channels, err := priceMonitorRefreshChannels()
	if err != nil {
		return PriceMonitorSnapshot{}, err
	}
	sourceNames := make(map[int]string, len(base.SourceHeaders))
	for _, header := range base.SourceHeaders {
		if header.Type == priceSourceChannel && header.ChannelId > 0 {
			sourceNames[header.ChannelId] = header.Key
		}
	}
	contexts := buildPriceMonitorLossContexts(channels, sourceNames, next.ChannelCosts)
	applyPriceMonitorLossVerdicts(next.SourceHeaders, next.MatrixItems, contexts)
	applyPriceMonitorRepairFloors(next.SourceHeaders, next.MatrixItems, contexts)

	next.ComparisonModelCounts = countPriceMonitorComparisonModels(next.SourceHeaders, next.MatrixItems)
	next.ComparisonModelCounts.CostRatioMismatch = countPriceMonitorCostRatioMismatch(next.ChannelCosts)
	return next, nil
}

// recomputePriceMonitorRow 按当前平台定价重建一行：平台单元格重建，其余来源单元格清掉比对结果后重新标记。
// 取不到平台价格（模型已下架）时保留原行，等下一轮巡检处理。
func recomputePriceMonitorRow(item PriceMonitorMatrixItem, localData map[string]any) PriceMonitorMatrixItem {
	prices := make(map[string]PriceMonitorPriceCell, len(item.Prices))
	platform, ok := priceMonitorCell(localData, item.Model)
	if !ok {
		for key, cell := range item.Prices {
			prices[key] = resetPriceMonitorCellVerdict(cell)
		}
		return PriceMonitorMatrixItem{Model: item.Model, Prices: prices}
	}
	platform = priceMonitorBilledPlatformCell(platform, localData, item.Model)
	for key, cell := range item.Prices {
		if key == priceMonitorPlatformKey {
			continue
		}
		cell = resetPriceMonitorCellVerdict(cell)
		// 缺失、占位价、来源失败这三类不是价格比对结果，原样保留。动态表达式只能比表达式原文，
		// 而原文不写进快照文件（Expr 是 json:"-"），从磁盘加载的快照没有它，沿用巡检时的比对结果。
		if cell.UnavailableReason == "" && !priceMonitorDynamicExprUnknown(platform, cell) {
			cell = resetPriceMonitorCellComparison(cell)
			markPriceMonitorDifferences(platform, &cell)
		}
		prices[key] = cell
	}
	prices[priceMonitorPlatformKey] = platform
	return PriceMonitorMatrixItem{Model: item.Model, Prices: prices}
}

// priceMonitorDynamicExprUnknown 报告这个单元格的比对依赖一段不在手上的表达式原文。
func priceMonitorDynamicExprUnknown(platform, source PriceMonitorPriceCell) bool {
	if platform.Mode != priceMonitorModeExpression || source.Mode != priceMonitorModeExpression {
		return false
	}
	return (platform.Dynamic || source.Dynamic) && source.Expr == ""
}

// resetPriceMonitorCellVerdict 清掉亏损判定字段，由重算重新写入。
func resetPriceMonitorCellVerdict(cell PriceMonitorPriceCell) PriceMonitorPriceCell {
	cell.LossKinds = nil
	cell.SellFactor = nil
	cell.MeasuredFactor = nil
	cell.ConfiguredFactor = nil
	cell.UpstreamFactor = nil
	cell.LossLines = nil
	cell.Highest = false
	return cell
}

// resetPriceMonitorCellComparison 把来源单元格还原成标记前的样子：
//   - 清掉差异标记。markPriceMonitorDifferences 对「模式不同」只置位不清除，不先清掉，
//     改价后已经一致的单元格会一直显示为不一致；
//   - 去掉没有价格的分项。那是上一次标记为「平台有、来源没有」补的占位行，再标记一次
//     会被当成「来源有这一项但没价格」而判为不一致。
//
// 分项切片新建，不动快照里共享的原切片。
func resetPriceMonitorCellComparison(cell PriceMonitorPriceCell) PriceMonitorPriceCell {
	cell.Different = false
	cell.InputDifferent = false
	cell.OutputDifferent = false
	cell.PriceDifferent = false
	cell.ModeDifferent = false
	if len(cell.Lanes) > 0 {
		lanes := make([]PriceMonitorPriceLane, 0, len(cell.Lanes))
		for _, lane := range cell.Lanes {
			if lane.Price == nil {
				continue
			}
			lane.Different = false
			lanes = append(lanes, lane)
		}
		cell.Lanes = lanes
	}
	return cell
}

// priceMonitorChannelCostList 把快照里的核对结果按"需要处理的在前"排序，并用当前成本系数重算状态，
// 这样在非主节点上改了成本系数，列表也立刻反映。
func priceMonitorChannelCostList(snapshot PriceMonitorSnapshot) []PriceMonitorChannelCost {
	costs := make(map[string]PriceMonitorChannelCost, len(snapshot.ChannelCosts))
	for key, cost := range snapshot.ChannelCosts {
		costs[key] = cost
	}
	applyPriceMonitorChannelCostStatus(costs, priceMonitorRefreshCostRatio, priceMonitorRefreshConfigured())
	list := make([]PriceMonitorChannelCost, 0, len(costs))
	for _, cost := range costs {
		// 摘要只供巡检判断渠道配置是否变过，页面用不到，不外发。
		cost.SourceFingerprint = ""
		list = append(list, cost)
	}
	rank := map[string]int{
		priceMonitorCostStatusLow:     0,
		priceMonitorCostStatusHigh:    1,
		priceMonitorCostStatusUnknown: 2,
		priceMonitorCostStatusMatch:   3,
		priceMonitorCostStatusFree:    4,
	}
	sort.SliceStable(list, func(i, j int) bool {
		if rank[list[i].Status] != rank[list[j].Status] {
			return rank[list[i].Status] < rank[list[j].Status]
		}
		return list[i].ChannelId < list[j].ChannelId
	})
	return list
}
