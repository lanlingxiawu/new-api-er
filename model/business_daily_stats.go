package model

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// coveredDaysCache 记录本进程已标记过 coverage 的日期，避免每笔消费都 upsert coverage 行。
// 进程重启后重新填充，无一致性风险（upsert 幂等）。
var coveredDaysCache sync.Map

const businessStatsDaySeconds int64 = 86400

type PlatformChannelDailyStat struct {
	Id            int     `json:"id"`
	StatDate      int64   `json:"stat_date" gorm:"uniqueIndex:idx_platform_channel_daily,priority:1;index"`
	ChannelId     int     `json:"channel_id" gorm:"uniqueIndex:idx_platform_channel_daily,priority:2;index"`
	RevenueQuota  int64   `json:"revenue_quota" gorm:"default:0"`
	CostQuota     int64   `json:"cost_quota" gorm:"default:0"`
	RecordCount   int64   `json:"record_count" gorm:"default:0"`
	CostRatioSum  float64 `json:"cost_ratio_sum" gorm:"default:0"`
	LastCreatedAt int64   `json:"last_created_at" gorm:"default:0"`
}

type EmployeeCommissionDailyStat struct {
	Id              int   `json:"id"`
	StatDate        int64 `json:"stat_date" gorm:"uniqueIndex:idx_employee_commission_daily,priority:1;index"`
	EmployeeUserId  int   `json:"employee_user_id" gorm:"uniqueIndex:idx_employee_commission_daily,priority:2;index"`
	RevenueQuota    int64 `json:"revenue_quota" gorm:"default:0"`
	CostQuota       int64 `json:"cost_quota" gorm:"default:0"`
	ProfitQuota     int64 `json:"profit_quota" gorm:"default:0"`
	CommissionQuota int64 `json:"commission_quota" gorm:"default:0"`
	RecordCount     int64 `json:"record_count" gorm:"default:0"`
	LastCreatedAt   int64 `json:"last_created_at" gorm:"default:0"`
}

type BusinessDailyStatsCoverage struct {
	Id          int   `json:"id"`
	StatDate    int64 `json:"stat_date" gorm:"uniqueIndex:idx_business_daily_stats_coverage_date"`
	CompletedAt int64 `json:"completed_at" gorm:"default:0"`
}

type statsTimeRange struct {
	StartTime int64
	EndTime   int64
}

type statDateRange struct {
	StartDate int64
	EndDate   int64
}

type dailyAggregatePlan struct {
	AggregateStartDate int64
	AggregateEndDate   int64
	HasAggregate       bool
	DetailRanges       []statsTimeRange
}

func unixDayStart(ts int64) int64 {
	return ts / businessStatsDaySeconds * businessStatsDaySeconds
}

func unixDayEnd(ts int64) int64 {
	return unixDayStart(ts) + businessStatsDaySeconds - 1
}

func splitDailyAggregatePlan(startTime, endTime int64) dailyAggregatePlan {
	var plan dailyAggregatePlan

	aggregateStartDate := int64(0)
	if startTime != 0 {
		startDay := unixDayStart(startTime)
		if startTime > startDay {
			detailEnd := unixDayEnd(startTime)
			if endTime != 0 && endTime < detailEnd {
				detailEnd = endTime
			}
			if startTime <= detailEnd {
				plan.DetailRanges = append(plan.DetailRanges, statsTimeRange{
					StartTime: startTime,
					EndTime:   detailEnd,
				})
			}
			if endTime != 0 && endTime <= unixDayEnd(startTime) {
				return plan
			}
			aggregateStartDate = startDay + businessStatsDaySeconds
		} else {
			aggregateStartDate = startDay
		}
	}

	aggregateEndDate := int64(0)
	if endTime != 0 {
		endDay := unixDayStart(endTime)
		if endTime < unixDayEnd(endTime) {
			detailStart := endDay
			if startTime != 0 && startTime > detailStart {
				detailStart = startTime
			}
			if detailStart <= endTime {
				plan.DetailRanges = append(plan.DetailRanges, statsTimeRange{
					StartTime: detailStart,
					EndTime:   endTime,
				})
			}
			aggregateEndDate = endDay - businessStatsDaySeconds
		} else {
			aggregateEndDate = endDay
		}
	}

	plan.AggregateStartDate = aggregateStartDate
	plan.AggregateEndDate = aggregateEndDate
	if endTime == 0 {
		plan.HasAggregate = true
	} else {
		plan.HasAggregate = aggregateEndDate >= aggregateStartDate
	}
	return plan
}

func getBusinessStatsQueryBounds() (int64, int64, error) {
	dailyMin, dailyMax, err := getBusinessStatsDailyBounds()
	if err != nil {
		return 0, 0, err
	}

	minDate := dailyMin
	maxDate := dailyMax

	// 回填完成后日聚合表的范围已覆盖全部台账，无需再查明细表边界（省 2 次 DB）
	common.OptionMapRWMutex.RLock()
	backfillDone := common.OptionMap["BusinessStatsBackfillCompleted"] == "true"
	common.OptionMapRWMutex.RUnlock()

	if !backfillDone {
		ledgerMin, ledgerMax, err := getBusinessStatsLedgerBounds()
		if err != nil {
			return 0, 0, err
		}
		if ledgerMin != 0 {
			ledgerMinDate := unixDayStart(ledgerMin)
			if minDate == 0 || ledgerMinDate < minDate {
				minDate = ledgerMinDate
			}
		}
		if ledgerMax != 0 {
			ledgerMaxDate := unixDayStart(ledgerMax)
			if ledgerMaxDate > maxDate {
				maxDate = ledgerMaxDate
			}
		}
	}
	return minDate, maxDate, nil
}

func getBusinessStatsDailyBounds() (int64, int64, error) {
	platformMin, platformMax, err := getStatDateBounds(&PlatformChannelDailyStat{})
	if err != nil {
		return 0, 0, err
	}
	employeeMin, employeeMax, err := getStatDateBounds(&EmployeeCommissionDailyStat{})
	if err != nil {
		return 0, 0, err
	}
	minDate := platformMin
	if minDate == 0 || (employeeMin != 0 && employeeMin < minDate) {
		minDate = employeeMin
	}
	maxDate := platformMax
	if employeeMax > maxDate {
		maxDate = employeeMax
	}
	return minDate, maxDate, nil
}

func getStatDateBounds(table any) (int64, int64, error) {
	var result struct {
		MinDate int64
		MaxDate int64
	}
	if err := DB.Model(table).
		Select("COALESCE(MIN(stat_date),0) as min_date, COALESCE(MAX(stat_date),0) as max_date").
		Scan(&result).Error; err != nil {
		return 0, 0, err
	}
	return result.MinDate, result.MaxDate, nil
}

func normalizeAggregateDateRange(plan dailyAggregatePlan, startTime, endTime int64) (int64, int64, bool, error) {
	if !plan.HasAggregate {
		return 0, 0, false, nil
	}
	startDate := plan.AggregateStartDate
	endDate := plan.AggregateEndDate
	minDate, maxDate, err := getBusinessStatsQueryBounds()
	if err != nil {
		return 0, 0, false, err
	}
	if maxDate == 0 {
		return 0, 0, false, nil
	}
	if startTime == 0 {
		startDate = minDate
	}
	if endTime == 0 {
		endDate = maxDate
	}
	if startDate == 0 && minDate != 0 {
		startDate = minDate
	}
	if endDate < startDate {
		return 0, 0, false, nil
	}
	return startDate, endDate, true, nil
}

func splitCoveredDailyRanges(startDate, endDate int64) ([]statDateRange, []statDateRange, error) {
	if endDate < startDate {
		return nil, nil, nil
	}
	var coveredDates []int64
	if err := DB.Model(&BusinessDailyStatsCoverage{}).
		Where("stat_date >= ? AND stat_date <= ?", startDate, endDate).
		Pluck("stat_date", &coveredDates).Error; err != nil {
		return nil, nil, err
	}
	coveredSet := make(map[int64]struct{}, len(coveredDates))
	for _, date := range coveredDates {
		coveredSet[date] = struct{}{}
	}

	var covered []statDateRange
	var uncovered []statDateRange
	var current *statDateRange
	currentCovered := false
	flush := func() {
		if current == nil {
			return
		}
		if currentCovered {
			covered = append(covered, *current)
		} else {
			uncovered = append(uncovered, *current)
		}
		current = nil
	}

	for date := startDate; date <= endDate; date += businessStatsDaySeconds {
		_, isCovered := coveredSet[date]
		if current == nil {
			current = &statDateRange{StartDate: date, EndDate: date}
			currentCovered = isCovered
			continue
		}
		if isCovered == currentCovered && date == current.EndDate+businessStatsDaySeconds {
			current.EndDate = date
			continue
		}
		flush()
		current = &statDateRange{StartDate: date, EndDate: date}
		currentCovered = isCovered
	}
	flush()
	return covered, uncovered, nil
}

func addPlatformChannelDailyStatTx(tx *gorm.DB, rec *ConsumptionCost) error {
	statDate := unixDayStart(rec.CreatedAt)
	row := PlatformChannelDailyStat{
		StatDate:      statDate,
		ChannelId:     rec.ChannelId,
		RevenueQuota:  rec.RevenueQuota,
		CostQuota:     rec.CostQuota,
		RecordCount:   1,
		CostRatioSum:  rec.CostRatio,
		LastCreatedAt: rec.CreatedAt,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stat_date"}, {Name: "channel_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"revenue_quota":   gorm.Expr("revenue_quota + ?", rec.RevenueQuota),
			"cost_quota":      gorm.Expr("cost_quota + ?", rec.CostQuota),
			"record_count":    gorm.Expr("record_count + ?", 1),
			"cost_ratio_sum":  gorm.Expr("cost_ratio_sum + ?", rec.CostRatio),
			"last_created_at": gorm.Expr("CASE WHEN last_created_at > ? THEN last_created_at ELSE ? END", rec.CreatedAt, rec.CreatedAt),
		}),
	}).Create(&row).Error; err != nil {
		return err
	}
	// 自动标记当天 coverage，使查询走日聚合表而非扫描明细。
	// 进程内缓存保证每天只 upsert 一次，开销可忽略。
	return ensureDailyCoverageTx(tx, statDate)
}

func addEmployeeCommissionDailyStatTx(tx *gorm.DB, log *EmployeeCommissionLog) error {
	statDate := unixDayStart(log.CreatedAt)
	row := EmployeeCommissionDailyStat{
		StatDate:        statDate,
		EmployeeUserId:  log.EmployeeUserId,
		RevenueQuota:    log.RevenueQuota,
		CostQuota:       log.CostQuota,
		ProfitQuota:     log.ProfitQuota,
		CommissionQuota: log.CommissionQuota,
		RecordCount:     1,
		LastCreatedAt:   log.CreatedAt,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stat_date"}, {Name: "employee_user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"revenue_quota":    gorm.Expr("revenue_quota + ?", log.RevenueQuota),
			"cost_quota":       gorm.Expr("cost_quota + ?", log.CostQuota),
			"profit_quota":     gorm.Expr("profit_quota + ?", log.ProfitQuota),
			"commission_quota": gorm.Expr("commission_quota + ?", log.CommissionQuota),
			"record_count":     gorm.Expr("record_count + ?", 1),
			"last_created_at":  gorm.Expr("CASE WHEN last_created_at > ? THEN last_created_at ELSE ? END", log.CreatedAt, log.CreatedAt),
		}),
	}).Create(&row).Error
}

// ensureDailyCoverageTx 在事务中标记指定日期为已覆盖。
// 使用进程内缓存去重，每个日期只执行一次 DB upsert。
func ensureDailyCoverageTx(tx *gorm.DB, statDate int64) error {
	if _, loaded := coveredDaysCache.LoadOrStore(statDate, struct{}{}); loaded {
		return nil // 本进程已标记过
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "stat_date"}},
		DoUpdates: clause.AssignmentColumns([]string{"completed_at"}),
	}).Create(&BusinessDailyStatsCoverage{
		StatDate:    statDate,
		CompletedAt: time.Now().Unix(),
	}).Error
}

// ResolvedQueryPlan 预计算的查询计划，包含覆盖/未覆盖的日期范围和边界明细范围。
// 由 ResolveBusinessStatsQueryPlan 生成一次，可传给多个查询函数复用，
// 避免重复计算 splitDailyAggregatePlan / normalizeAggregateDateRange / splitCoveredDailyRanges。
type ResolvedQueryPlan struct {
	CoveredRanges   []statDateRange
	UncoveredRanges []statDateRange
	DetailRanges    []statsTimeRange
}

type ConsumptionCostChannelSummary struct {
	Totals                 ConsumptionCostTotals
	ProfitableChannelCount int
	LossChannelCount       int
}

// ResolveBusinessStatsQueryPlan 根据时间范围生成查询计划。
func ResolveBusinessStatsQueryPlan(startTime, endTime int64) (*ResolvedQueryPlan, error) {
	plan := splitDailyAggregatePlan(startTime, endTime)
	resolved := &ResolvedQueryPlan{
		DetailRanges: plan.DetailRanges,
	}
	if plan.HasAggregate {
		startDate, endDate, hasAggregate, err := normalizeAggregateDateRange(plan, startTime, endTime)
		if err != nil {
			return nil, err
		}
		if hasAggregate {
			covered, uncovered, err := splitCoveredDailyRanges(startDate, endDate)
			if err != nil {
				return nil, err
			}
			resolved.CoveredRanges = covered
			resolved.UncoveredRanges = uncovered
		}
	}
	return resolved, nil
}

func normalizeStatsPagination(page, pageSize int) (int, int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize, (page - 1) * pageSize
}

func isDailyOnlyPlan(plan *ResolvedQueryPlan) bool {
	return plan != nil &&
		len(plan.CoveredRanges) > 0 &&
		len(plan.UncoveredRanges) == 0 &&
		len(plan.DetailRanges) == 0
}

func applyStatDateRanges(tx *gorm.DB, ranges []statDateRange) *gorm.DB {
	if len(ranges) == 0 {
		return tx.Where("1 = 0")
	}
	parts := make([]string, 0, len(ranges))
	args := make([]interface{}, 0, len(ranges)*2)
	for _, r := range ranges {
		parts = append(parts, "(stat_date >= ? AND stat_date <= ?)")
		args = append(args, r.StartDate, r.EndDate)
	}
	return tx.Where(strings.Join(parts, " OR "), args...)
}

func applyChannelKeyword(tx *gorm.DB, keyword string) *gorm.DB {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return tx
	}
	like := "%" + keyword + "%"
	if channelId, err := strconv.Atoi(keyword); err == nil {
		return tx.Where("(LOWER(channels.name) LIKE ? OR platform_channel_daily_stats.channel_id = ?)", like, channelId)
	}
	return tx.Where("LOWER(channels.name) LIKE ?", like)
}

func getConsumptionCostByChannelFromDaily(startDate, endDate int64, hasEndDate bool) ([]*ConsumptionCostChannelStat, error) {
	type row struct {
		ChannelId    int
		TotalRevenue int64
		TotalCost    int64
		RecordCount  int64
		CostRatioSum float64
	}
	var rows []row
	tx := DB.Model(&PlatformChannelDailyStat{}).
		Select("channel_id, "+
			"COALESCE(SUM(revenue_quota),0) as total_revenue, "+
			"COALESCE(SUM(cost_quota),0) as total_cost, "+
			"COALESCE(SUM(record_count),0) as record_count, "+
			"COALESCE(SUM(cost_ratio_sum),0) as cost_ratio_sum").
		Where("stat_date >= ?", startDate).
		Group("channel_id")
	if hasEndDate {
		tx = tx.Where("stat_date <= ?", endDate)
	}
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]*ConsumptionCostChannelStat, 0, len(rows))
	for _, r := range rows {
		item := &ConsumptionCostChannelStat{
			ChannelId:    r.ChannelId,
			TotalRevenue: r.TotalRevenue,
			TotalCost:    r.TotalCost,
			RecordCount:  r.RecordCount,
			CostRatioSum: r.CostRatioSum,
		}
		// 不在此处 finalize——由调用方 merge 完所有数据源后统一 finalize，
		// 避免 merge 时 CostRatio 已被覆盖导致潜在的维护隐患。
		items = append(items, item)
	}
	return items, nil
}

func getConsumptionCostByChannelFromStats(startTime, endTime int64) ([]*ConsumptionCostChannelStat, error) {
	resolved, err := ResolveBusinessStatsQueryPlan(startTime, endTime)
	if err != nil {
		return nil, err
	}
	return GetConsumptionCostByChannelWithPlan(resolved)
}

// GetConsumptionCostByChannelWithPlan 使用预计算的查询计划按渠道汇总。
func GetConsumptionCostByChannelWithPlan(plan *ResolvedQueryPlan) ([]*ConsumptionCostChannelStat, error) {
	byChannel := make(map[int]*ConsumptionCostChannelStat)
	for _, r := range plan.CoveredRanges {
		rows, err := getConsumptionCostByChannelFromDaily(r.StartDate, r.EndDate, true)
		if err != nil {
			return nil, err
		}
		mergeConsumptionCostChannelStats(byChannel, rows)
	}
	for _, r := range plan.UncoveredRanges {
		rows, err := GetConsumptionCostByChannelFromLedger(r.StartDate, unixDayEnd(r.EndDate))
		if err != nil {
			return nil, err
		}
		mergeConsumptionCostChannelStats(byChannel, rows)
	}
	for _, r := range plan.DetailRanges {
		rows, err := GetConsumptionCostByChannelFromLedger(r.StartTime, r.EndTime)
		if err != nil {
			return nil, err
		}
		mergeConsumptionCostChannelStats(byChannel, rows)
	}
	items := make([]*ConsumptionCostChannelStat, 0, len(byChannel))
	for _, item := range byChannel {
		finalizeConsumptionCostChannelStat(item)
		items = append(items, item)
	}
	return items, nil
}

func GetConsumptionCostChannelSummaryWithPlan(plan *ResolvedQueryPlan) (ConsumptionCostChannelSummary, error) {
	if isDailyOnlyPlan(plan) {
		type summaryRow struct {
			TotalRevenue           int64
			TotalCost              int64
			RecordCount            int64
			ProfitableChannelCount int
			LossChannelCount       int
		}
		base := DB.Model(&PlatformChannelDailyStat{}).
			Select("channel_id, " +
				"COALESCE(SUM(revenue_quota),0) as total_revenue, " +
				"COALESCE(SUM(cost_quota),0) as total_cost, " +
				"COALESCE(SUM(record_count),0) as record_count").
			Group("channel_id")
		base = applyStatDateRanges(base, plan.CoveredRanges)

		var row summaryRow
		err := DB.Table("(?) as grouped", base).
			Select("COALESCE(SUM(total_revenue),0) as total_revenue, " +
				"COALESCE(SUM(total_cost),0) as total_cost, " +
				"COALESCE(SUM(record_count),0) as record_count, " +
				"COALESCE(SUM(CASE WHEN total_revenue - total_cost > 0 THEN 1 ELSE 0 END),0) as profitable_channel_count, " +
				"COALESCE(SUM(CASE WHEN total_revenue - total_cost < 0 THEN 1 ELSE 0 END),0) as loss_channel_count").
			Scan(&row).Error
		if err != nil {
			return ConsumptionCostChannelSummary{}, err
		}
		return ConsumptionCostChannelSummary{
			Totals: ConsumptionCostTotals{
				TotalRevenue: row.TotalRevenue,
				TotalCost:    row.TotalCost,
				RecordCount:  row.RecordCount,
			},
			ProfitableChannelCount: row.ProfitableChannelCount,
			LossChannelCount:       row.LossChannelCount,
		}, nil
	}

	items, err := GetConsumptionCostByChannelWithPlan(plan)
	if err != nil {
		return ConsumptionCostChannelSummary{}, err
	}
	var summary ConsumptionCostChannelSummary
	for _, item := range items {
		summary.Totals.TotalRevenue += item.TotalRevenue
		summary.Totals.TotalCost += item.TotalCost
		summary.Totals.RecordCount += item.RecordCount
		profit := item.TotalRevenue - item.TotalCost
		if profit > 0 {
			summary.ProfitableChannelCount++
		} else if profit < 0 {
			summary.LossChannelCount++
		}
	}
	return summary, nil
}

func channelSortExpression(sortBy string) string {
	switch sortBy {
	case "channel_name":
		return "channel_name"
	case "cost_ratio":
		return "cost_ratio"
	case "consumption_quota":
		return "total_revenue"
	case "est_cost_quota":
		return "total_cost"
	case "est_gross_margin":
		return "gross_margin"
	default:
		return "est_profit"
	}
}

func sortConsumptionCostChannels(items []*ConsumptionCostChannelStat, sortBy, sortOrder string) {
	desc := !strings.EqualFold(sortOrder, "asc")
	sort.Slice(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		switch sortBy {
		case "channel_name":
			if desc {
				return left.ChannelName > right.ChannelName
			}
			return left.ChannelName < right.ChannelName
		case "cost_ratio":
			if desc {
				return left.CostRatio > right.CostRatio
			}
			return left.CostRatio < right.CostRatio
		case "consumption_quota":
			if desc {
				return left.TotalRevenue > right.TotalRevenue
			}
			return left.TotalRevenue < right.TotalRevenue
		case "est_cost_quota":
			if desc {
				return left.TotalCost > right.TotalCost
			}
			return left.TotalCost < right.TotalCost
		case "est_gross_margin":
			leftMargin := float64(0)
			rightMargin := float64(0)
			if left.TotalRevenue != 0 {
				leftMargin = float64(left.TotalRevenue-left.TotalCost) / float64(left.TotalRevenue)
			}
			if right.TotalRevenue != 0 {
				rightMargin = float64(right.TotalRevenue-right.TotalCost) / float64(right.TotalRevenue)
			}
			if desc {
				return leftMargin > rightMargin
			}
			return leftMargin < rightMargin
		default:
			leftProfit := left.TotalRevenue - left.TotalCost
			rightProfit := right.TotalRevenue - right.TotalCost
			if desc {
				return leftProfit > rightProfit
			}
			return leftProfit < rightProfit
		}
	})
}

func GetConsumptionCostByChannelPageWithPlan(plan *ResolvedQueryPlan, page, pageSize int, keyword, sortBy, sortOrder string) ([]*ConsumptionCostChannelStat, int64, error) {
	_, pageSize, offset := normalizeStatsPagination(page, pageSize)
	if isDailyOnlyPlan(plan) {
		base := DB.Model(&PlatformChannelDailyStat{}).
			Joins("LEFT JOIN channels ON channels.id = platform_channel_daily_stats.channel_id")
		base = applyStatDateRanges(base, plan.CoveredRanges)
		base = applyChannelKeyword(base, keyword)

		countBase := base.Session(&gorm.Session{}).
			Select("platform_channel_daily_stats.channel_id").
			Group("platform_channel_daily_stats.channel_id")
		var total int64
		if err := DB.Table("(?) as grouped", countBase).Count(&total).Error; err != nil {
			return nil, 0, err
		}

		var rows []*ConsumptionCostChannelStat
		sortExpr := channelSortExpression(sortBy)
		orderDirection := "DESC"
		if strings.EqualFold(sortOrder, "asc") {
			orderDirection = "ASC"
		}
		err := base.Select("platform_channel_daily_stats.channel_id, " +
			"MAX(channels.name) as channel_name, " +
			"COALESCE(SUM(platform_channel_daily_stats.revenue_quota),0) as total_revenue, " +
			"COALESCE(SUM(platform_channel_daily_stats.cost_quota),0) as total_cost, " +
			"COALESCE(SUM(platform_channel_daily_stats.record_count),0) as record_count, " +
			"COALESCE(SUM(platform_channel_daily_stats.cost_ratio_sum),0) as cost_ratio_sum, " +
			"CASE WHEN COALESCE(SUM(platform_channel_daily_stats.record_count),0) > 0 THEN COALESCE(SUM(platform_channel_daily_stats.cost_ratio_sum),0) * 1.0 / COALESCE(SUM(platform_channel_daily_stats.record_count),0) ELSE 1 END as cost_ratio, " +
			"COALESCE(SUM(platform_channel_daily_stats.revenue_quota),0) - COALESCE(SUM(platform_channel_daily_stats.cost_quota),0) as est_profit, " +
			"CASE WHEN COALESCE(SUM(platform_channel_daily_stats.revenue_quota),0) <> 0 THEN (COALESCE(SUM(platform_channel_daily_stats.revenue_quota),0) - COALESCE(SUM(platform_channel_daily_stats.cost_quota),0)) * 1.0 / COALESCE(SUM(platform_channel_daily_stats.revenue_quota),0) ELSE 0 END as gross_margin").
			Group("platform_channel_daily_stats.channel_id").
			Order(sortExpr + " " + orderDirection).
			Offset(offset).
			Limit(pageSize).
			Scan(&rows).Error
		if err != nil {
			return nil, 0, err
		}
		channelIds := make([]int, 0, len(rows))
		for _, row := range rows {
			channelIds = append(channelIds, row.ChannelId)
		}
		channelNames, err := GetChannelNamesByIds(channelIds)
		if err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			finalizeConsumptionCostChannelStat(row)
			row.ChannelName = channelNames[row.ChannelId]
		}
		return rows, total, nil
	}

	items, err := GetConsumptionCostByChannelWithPlan(plan)
	if err != nil {
		return nil, 0, err
	}
	channelIds := make([]int, 0, len(items))
	for _, item := range items {
		channelIds = append(channelIds, item.ChannelId)
	}
	channelNames, err := GetChannelNamesByIds(channelIds)
	if err != nil {
		return nil, 0, err
	}
	for _, item := range items {
		item.ChannelName = channelNames[item.ChannelId]
	}
	if keyword != "" {
		filtered := make([]*ConsumptionCostChannelStat, 0, len(items))
		lowerKeyword := strings.ToLower(strings.TrimSpace(keyword))
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.ChannelName), lowerKeyword) || strings.Contains(strconv.Itoa(item.ChannelId), lowerKeyword) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	sortConsumptionCostChannels(items, sortBy, sortOrder)
	total := int64(len(items))
	start := offset
	if start >= len(items) {
		return items[:0], total, nil
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], total, nil
}

func mergeConsumptionCostChannelStats(dst map[int]*ConsumptionCostChannelStat, rows []*ConsumptionCostChannelStat) {
	for _, row := range rows {
		item := dst[row.ChannelId]
		if item == nil {
			item = &ConsumptionCostChannelStat{ChannelId: row.ChannelId}
			dst[row.ChannelId] = item
		}
		item.TotalRevenue += row.TotalRevenue
		item.TotalCost += row.TotalCost
		item.RecordCount += row.RecordCount
		item.CostRatioSum += row.CostRatioSum
	}
}

func finalizeConsumptionCostChannelStat(item *ConsumptionCostChannelStat) {
	if item.RecordCount > 0 {
		item.CostRatio = item.CostRatioSum / float64(item.RecordCount)
	} else {
		item.CostRatio = channelCostDefaultRatio
	}
}

func getCommissionStatsByEmployeeFromDaily(startDate, endDate int64, hasEndDate bool) ([]*CommissionEmployeeStat, error) {
	var rows []*CommissionEmployeeStat
	tx := DB.Model(&EmployeeCommissionDailyStat{}).
		Select("employee_user_id, "+
			"COALESCE(SUM(revenue_quota),0) as total_revenue, "+
			"COALESCE(SUM(cost_quota),0) as total_cost, "+
			"COALESCE(SUM(profit_quota),0) as total_profit, "+
			"COALESCE(SUM(commission_quota),0) as total_commission, "+
			"COALESCE(SUM(record_count),0) as record_count").
		Where("stat_date >= ?", startDate).
		Group("employee_user_id")
	if hasEndDate {
		tx = tx.Where("stat_date <= ?", endDate)
	}
	err := tx.Scan(&rows).Error
	return rows, err
}

func getCommissionStatsByEmployeeFromStats(startTime, endTime int64, limit int) ([]*CommissionEmployeeStat, error) {
	resolved, err := ResolveBusinessStatsQueryPlan(startTime, endTime)
	if err != nil {
		return nil, err
	}
	return GetCommissionStatsByEmployeeWithPlan(resolved, limit)
}

// GetCommissionStatsByEmployeeWithPlan 使用预计算的查询计划按员工汇总。
func GetCommissionStatsByEmployeeWithPlan(plan *ResolvedQueryPlan, limit int) ([]*CommissionEmployeeStat, error) {
	byEmployee := make(map[int]*CommissionEmployeeStat)
	for _, r := range plan.CoveredRanges {
		rows, err := getCommissionStatsByEmployeeFromDaily(r.StartDate, r.EndDate, true)
		if err != nil {
			return nil, err
		}
		mergeCommissionEmployeeStats(byEmployee, rows)
	}
	for _, r := range plan.UncoveredRanges {
		rows, err := GetCommissionStatsByEmployeeFromLedger(r.StartDate, unixDayEnd(r.EndDate), 0)
		if err != nil {
			return nil, err
		}
		mergeCommissionEmployeeStats(byEmployee, rows)
	}
	for _, r := range plan.DetailRanges {
		rows, err := GetCommissionStatsByEmployeeFromLedger(r.StartTime, r.EndTime, 0)
		if err != nil {
			return nil, err
		}
		mergeCommissionEmployeeStats(byEmployee, rows)
	}
	items := make([]*CommissionEmployeeStat, 0, len(byEmployee))
	for _, item := range byEmployee {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].TotalCommission > items[j].TotalCommission
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func GetCommissionStatsByEmployeePageWithPlan(plan *ResolvedQueryPlan, page, pageSize int) ([]*CommissionEmployeeStat, int64, error) {
	_, pageSize, offset := normalizeStatsPagination(page, pageSize)
	if isDailyOnlyPlan(plan) {
		base := DB.Model(&EmployeeCommissionDailyStat{})
		base = applyStatDateRanges(base, plan.CoveredRanges)

		countBase := base.Session(&gorm.Session{}).
			Select("employee_user_id").
			Group("employee_user_id")
		var total int64
		if err := DB.Table("(?) as grouped", countBase).Count(&total).Error; err != nil {
			return nil, 0, err
		}

		var rows []*CommissionEmployeeStat
		err := base.Select("employee_user_id, " +
			"COALESCE(SUM(revenue_quota),0) as total_revenue, " +
			"COALESCE(SUM(cost_quota),0) as total_cost, " +
			"COALESCE(SUM(profit_quota),0) as total_profit, " +
			"COALESCE(SUM(commission_quota),0) as total_commission, " +
			"COALESCE(SUM(record_count),0) as record_count").
			Group("employee_user_id").
			Order("total_commission DESC").
			Offset(offset).
			Limit(pageSize).
			Scan(&rows).Error
		if err != nil {
			return nil, 0, err
		}
		return rows, total, nil
	}

	items, err := GetCommissionStatsByEmployeeWithPlan(plan, 0)
	if err != nil {
		return nil, 0, err
	}
	total := int64(len(items))
	start := offset
	if start >= len(items) {
		return items[:0], total, nil
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], total, nil
}

func mergeCommissionEmployeeStats(dst map[int]*CommissionEmployeeStat, rows []*CommissionEmployeeStat) {
	for _, row := range rows {
		item := dst[row.EmployeeUserId]
		if item == nil {
			item = &CommissionEmployeeStat{EmployeeUserId: row.EmployeeUserId}
			dst[row.EmployeeUserId] = item
		}
		item.TotalRevenue += row.TotalRevenue
		item.TotalCost += row.TotalCost
		item.TotalProfit += row.TotalProfit
		item.TotalCommission += row.TotalCommission
		item.RecordCount += row.RecordCount
	}
}

func getCommissionTotalsFromStats(startTime, endTime int64) (CommissionTotals, error) {
	stats, err := getCommissionStatsByEmployeeFromStats(startTime, endTime, 0)
	if err != nil {
		return CommissionTotals{}, err
	}
	var totals CommissionTotals
	for _, item := range stats {
		totals.TotalRevenue += item.TotalRevenue
		totals.TotalCost += item.TotalCost
		totals.TotalProfit += item.TotalProfit
		totals.TotalCommission += item.TotalCommission
		totals.RecordCount += item.RecordCount
	}
	return totals, nil
}

type BusinessDailyStatsBackfillResult struct {
	StartDate         int64 `json:"start_date"`
	EndDate           int64 `json:"end_date"`
	Days              int64 `json:"days"`
	PlatformRows      int64 `json:"platform_rows"`
	EmployeeRows      int64 `json:"employee_rows"`
	CostRecords       int64 `json:"cost_records"`
	CommissionRecords int64 `json:"commission_records"`
	TotalRecords      int64 `json:"total_records"`
	BatchDays         int   `json:"batch_days"`
	SleepMs           int   `json:"sleep_ms"`
	MaxRetries        int   `json:"max_retries"`
}

type businessStatsBackfillPlan struct {
	StartTime         int64
	EndTime           int64
	StartDate         int64
	EndDate           int64
	Days              int64
	CostRecords       int64
	CommissionRecords int64
	TotalRecords      int64
	AvgRecordsPerDay  int64
	BatchDays         int
	SleepMs           int
	MaxRetries        int
}

type businessStatsBackfillDayResult struct {
	PlatformRows      int64
	EmployeeRows      int64
	CostRecords       int64
	CommissionRecords int64
}

func BackfillBusinessDailyStats(startTime, endTime int64, batchDays int) (BusinessDailyStatsBackfillResult, error) {
	plan, err := resolveBusinessStatsBackfillPlan(startTime, endTime, batchDays)
	if err != nil {
		return BusinessDailyStatsBackfillResult{}, err
	}
	result := BusinessDailyStatsBackfillResult{
		StartDate:         plan.StartDate,
		EndDate:           plan.EndDate,
		CostRecords:       plan.CostRecords,
		CommissionRecords: plan.CommissionRecords,
		TotalRecords:      plan.TotalRecords,
		BatchDays:         plan.BatchDays,
		SleepMs:           plan.SleepMs,
		MaxRetries:        plan.MaxRetries,
	}
	if plan.Days == 0 {
		return result, nil
	}

	for statDate := plan.StartDate; statDate <= plan.EndDate; statDate += businessStatsDaySeconds {
		startedAt := time.Now()
		dayResult, err := backfillBusinessDailyStatsDayWithRetry(statDate, plan.MaxRetries, plan.SleepMs)
		if err != nil {
			return result, err
		}
		result.Days++
		result.PlatformRows += dayResult.PlatformRows
		result.EmployeeRows += dayResult.EmployeeRows

		if statDate < plan.EndDate {
			sleep := businessStatsBackfillSleepDuration(plan.SleepMs, dayResult.CostRecords+dayResult.CommissionRecords, time.Since(startedAt))
			if sleep > 0 {
				time.Sleep(sleep)
			}
		}
	}

	return result, nil
}

func resolveBusinessStatsBackfillPlan(startTime, endTime int64, requestedBatchDays int) (businessStatsBackfillPlan, error) {
	if requestedBatchDays < 0 {
		requestedBatchDays = 0
	}
	if requestedBatchDays > 31 {
		requestedBatchDays = 31
	}

	minCreated, maxCreated, err := getBusinessStatsLedgerBounds()
	if err != nil {
		return businessStatsBackfillPlan{}, err
	}
	if maxCreated == 0 {
		return businessStatsBackfillPlan{BatchDays: normalizeBackfillBatchDays(requestedBatchDays, 0), MaxRetries: 3}, nil
	}
	if startTime == 0 {
		startTime = minCreated
	}
	if endTime == 0 {
		endTime = maxCreated
	}
	if startTime > endTime {
		return businessStatsBackfillPlan{StartTime: startTime, EndTime: endTime, BatchDays: normalizeBackfillBatchDays(requestedBatchDays, 0), MaxRetries: 3}, nil
	}

	startDate := unixDayStart(startTime)
	endDate := unixDayStart(endTime)
	days := (endDate-startDate)/businessStatsDaySeconds + 1

	costRows, err := countCreatedAtRangeRows(&ConsumptionCost{}, startDate, endDate+businessStatsDaySeconds-1)
	if err != nil {
		return businessStatsBackfillPlan{}, err
	}
	commissionRows, err := countCreatedAtRangeRows(&EmployeeCommissionLog{}, startDate, endDate+businessStatsDaySeconds-1)
	if err != nil {
		return businessStatsBackfillPlan{}, err
	}
	totalRows := costRows + commissionRows
	avgRows := int64(0)
	if days > 0 {
		avgRows = totalRows / days
	}
	batchDays, sleepMs, maxRetries := chooseBusinessStatsBackfillPace(avgRows, requestedBatchDays)
	return businessStatsBackfillPlan{
		StartTime:         startTime,
		EndTime:           endTime,
		StartDate:         startDate,
		EndDate:           endDate,
		Days:              days,
		CostRecords:       costRows,
		CommissionRecords: commissionRows,
		TotalRecords:      totalRows,
		AvgRecordsPerDay:  avgRows,
		BatchDays:         batchDays,
		SleepMs:           sleepMs,
		MaxRetries:        maxRetries,
	}, nil
}

func normalizeBackfillBatchDays(requestedBatchDays int, fallback int) int {
	if fallback <= 0 {
		fallback = 1
	}
	if requestedBatchDays <= 0 {
		return fallback
	}
	if requestedBatchDays > 31 {
		return 31
	}
	return requestedBatchDays
}

func chooseBusinessStatsBackfillPace(avgRowsPerDay int64, requestedBatchDays int) (int, int, int) {
	batchDays := 7
	sleepMs := 0
	maxRetries := 3
	switch {
	case avgRowsPerDay >= 1_000_000:
		batchDays = 1
		sleepMs = 3000
		maxRetries = 5
	case avgRowsPerDay >= 500_000:
		batchDays = 1
		sleepMs = 2000
		maxRetries = 5
	case avgRowsPerDay >= 200_000:
		batchDays = 1
		sleepMs = 1000
		maxRetries = 4
	case avgRowsPerDay >= 50_000:
		batchDays = 3
		sleepMs = 500
	default:
		batchDays = 7
		sleepMs = 0
	}
	if requestedBatchDays > 0 && requestedBatchDays < batchDays {
		batchDays = requestedBatchDays
	}
	return batchDays, sleepMs, maxRetries
}

func countCreatedAtRangeRows(table any, startTime, endTime int64) (int64, error) {
	var count int64
	if err := DB.Model(table).Where("created_at >= ? AND created_at <= ?", startTime, endTime).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func backfillBusinessDailyStatsDayWithRetry(statDate int64, maxRetries int, sleepMs int) (businessStatsBackfillDayResult, error) {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := backfillBusinessDailyStatsDay(statDate)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isBusinessStatsBackfillRetryableError(err) || attempt == maxRetries {
			break
		}
		backoffMs := sleepMs
		if backoffMs <= 0 {
			backoffMs = 1000
		}
		backoffMs *= attempt + 1
		if backoffMs > 30000 {
			backoffMs = 30000
		}
		time.Sleep(time.Duration(backoffMs) * time.Millisecond)
	}
	return businessStatsBackfillDayResult{}, lastErr
}

func backfillBusinessDailyStatsDay(statDate int64) (businessStatsBackfillDayResult, error) {
	platformRows, err := aggregatePlatformChannelDailyStatsFromLedger(statDate, statDate)
	if err != nil {
		return businessStatsBackfillDayResult{}, err
	}
	employeeRows, err := aggregateEmployeeCommissionDailyStatsFromLedger(statDate, statDate)
	if err != nil {
		return businessStatsBackfillDayResult{}, err
	}

	result := businessStatsBackfillDayResult{
		PlatformRows: int64(len(platformRows)),
		EmployeeRows: int64(len(employeeRows)),
	}
	for _, row := range platformRows {
		result.CostRecords += row.RecordCount
	}
	for _, row := range employeeRows {
		result.CommissionRecords += row.RecordCount
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("stat_date = ?", statDate).Delete(&PlatformChannelDailyStat{}).Error; err != nil {
			return err
		}
		if err := tx.Where("stat_date = ?", statDate).Delete(&EmployeeCommissionDailyStat{}).Error; err != nil {
			return err
		}
		if len(platformRows) > 0 {
			if err := tx.CreateInBatches(platformRows, 1000).Error; err != nil {
				return err
			}
		}
		if len(employeeRows) > 0 {
			if err := tx.CreateInBatches(employeeRows, 1000).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "stat_date"}},
			DoUpdates: clause.AssignmentColumns([]string{"completed_at"}),
		}).Create(&BusinessDailyStatsCoverage{
			StatDate:    statDate,
			CompletedAt: time.Now().Unix(),
		}).Error
	}); err != nil {
		return businessStatsBackfillDayResult{}, err
	}
	coveredDaysCache.Store(statDate, struct{}{})
	return result, nil
}

func businessStatsBackfillSleepDuration(baseSleepMs int, records int64, elapsed time.Duration) time.Duration {
	sleepMs := baseSleepMs
	switch {
	case records >= 1_000_000 || elapsed >= 10*time.Second:
		if sleepMs < 5000 {
			sleepMs = 5000
		}
	case records >= 500_000 || elapsed >= 5*time.Second:
		if sleepMs < 2000 {
			sleepMs = 2000
		}
	case records >= 200_000 || elapsed >= 2*time.Second:
		if sleepMs < 1000 {
			sleepMs = 1000
		}
	}
	if sleepMs <= 0 {
		return 0
	}
	if sleepMs > 30000 {
		sleepMs = 30000
	}
	return time.Duration(sleepMs) * time.Millisecond
}

func isBusinessStatsBackfillRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "lock wait timeout") ||
		strings.Contains(msg, "deadlock") ||
		strings.Contains(msg, "database is locked")
}

func getBusinessStatsLedgerBounds() (int64, int64, error) {
	costMin, costMax, err := getCreatedAtBounds(&ConsumptionCost{})
	if err != nil {
		return 0, 0, err
	}
	commissionMin, commissionMax, err := getCreatedAtBounds(&EmployeeCommissionLog{})
	if err != nil {
		return 0, 0, err
	}

	minCreated := costMin
	if minCreated == 0 || (commissionMin != 0 && commissionMin < minCreated) {
		minCreated = commissionMin
	}
	maxCreated := costMax
	if commissionMax > maxCreated {
		maxCreated = commissionMax
	}
	return minCreated, maxCreated, nil
}

func getCreatedAtBounds(table any) (int64, int64, error) {
	var result struct {
		MinCreated int64
		MaxCreated int64
	}
	if err := DB.Model(table).
		Select("COALESCE(MIN(created_at),0) as min_created, COALESCE(MAX(created_at),0) as max_created").
		Scan(&result).Error; err != nil {
		return 0, 0, err
	}
	return result.MinCreated, result.MaxCreated, nil
}

func aggregatePlatformChannelDailyStatsFromLedger(startDate, endDate int64) ([]PlatformChannelDailyStat, error) {
	var rows []PlatformChannelDailyStat
	err := DB.Model(&ConsumptionCost{}).
		Select("FLOOR(created_at / 86400) * 86400 as stat_date, "+
			"channel_id, "+
			"COALESCE(SUM(revenue_quota),0) as revenue_quota, "+
			"COALESCE(SUM(cost_quota),0) as cost_quota, "+
			"COUNT(*) as record_count, "+
			"COALESCE(SUM(cost_ratio),0) as cost_ratio_sum, "+
			"COALESCE(MAX(created_at),0) as last_created_at").
		Where("created_at >= ? AND created_at <= ?", startDate, endDate+businessStatsDaySeconds-1).
		Group("FLOOR(created_at / 86400), channel_id").
		Scan(&rows).Error
	return rows, err
}

// NeedsBusinessStatsBackfill 检查是否有台账数据尚未被 coverage 覆盖。
// 如果 option 中已标记回填完成则直接返回 false，避免每次查询都检查。
func NeedsBusinessStatsBackfill() bool {
	common.OptionMapRWMutex.RLock()
	completed := common.OptionMap["BusinessStatsBackfillCompleted"] == "true"
	common.OptionMapRWMutex.RUnlock()
	if completed {
		return false
	}
	minCreated, maxCreated, err := getBusinessStatsLedgerBounds()
	if err != nil || minCreated == 0 || maxCreated == 0 {
		return false
	}
	minDate := unixDayStart(minCreated)
	maxDate := unixDayStart(maxCreated)
	expectedDays := (maxDate-minDate)/businessStatsDaySeconds + 1
	if expectedDays <= 0 {
		return false
	}
	var coveredDays int64
	if err := DB.Model(&BusinessDailyStatsCoverage{}).
		Where("stat_date >= ? AND stat_date <= ?", minDate, maxDate).
		Distinct("stat_date").
		Count(&coveredDays).Error; err != nil {
		return false
	}
	return coveredDays < expectedDays
}

func aggregateEmployeeCommissionDailyStatsFromLedger(startDate, endDate int64) ([]EmployeeCommissionDailyStat, error) {
	var rows []EmployeeCommissionDailyStat
	err := DB.Model(&EmployeeCommissionLog{}).
		Select("FLOOR(created_at / 86400) * 86400 as stat_date, "+
			"employee_user_id, "+
			"COALESCE(SUM(revenue_quota),0) as revenue_quota, "+
			"COALESCE(SUM(cost_quota),0) as cost_quota, "+
			"COALESCE(SUM(profit_quota),0) as profit_quota, "+
			"COALESCE(SUM(commission_quota),0) as commission_quota, "+
			"COUNT(*) as record_count, "+
			"COALESCE(MAX(created_at),0) as last_created_at").
		Where("created_at >= ? AND created_at <= ?", startDate, endDate+businessStatsDaySeconds-1).
		Group("FLOOR(created_at / 86400), employee_user_id").
		Scan(&rows).Error
	return rows, err
}
