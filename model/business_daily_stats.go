package model

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
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
	ChannelName   string  `json:"channel_name" gorm:"type:varchar(255);default:''"`
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

type EmployeeCustomerCommissionDailyStat struct {
	Id              int   `json:"id"`
	StatDate        int64 `json:"stat_date" gorm:"uniqueIndex:idx_employee_customer_commission_daily,priority:1;index"`
	EmployeeUserId  int   `json:"employee_user_id" gorm:"uniqueIndex:idx_employee_customer_commission_daily,priority:2;index"`
	CustomerUserId  int   `json:"customer_user_id" gorm:"uniqueIndex:idx_employee_customer_commission_daily,priority:3;index"`
	RevenueQuota    int64 `json:"revenue_quota" gorm:"default:0"`
	CostQuota       int64 `json:"cost_quota" gorm:"default:0"`
	ProfitQuota     int64 `json:"profit_quota" gorm:"default:0"`
	CommissionQuota int64 `json:"commission_quota" gorm:"default:0"`
	RecordCount     int64 `json:"record_count" gorm:"default:0"`
	LastCreatedAt   int64 `json:"last_created_at" gorm:"default:0"`
}

type EmployeeCommissionMonthlyStat struct {
	Id              int    `json:"id"`
	PeriodStartAt   int64  `json:"period_start_at" gorm:"uniqueIndex:idx_employee_commission_monthly,priority:1;index"`
	PeriodEndAt     int64  `json:"period_end_at" gorm:"index;default:0"`
	PeriodKey       string `json:"period_key" gorm:"type:varchar(32);default:''"`
	Timezone        string `json:"timezone" gorm:"type:varchar(64);default:''"`
	EmployeeUserId  int    `json:"employee_user_id" gorm:"uniqueIndex:idx_employee_commission_monthly,priority:2;index"`
	RevenueQuota    int64  `json:"revenue_quota" gorm:"default:0"`
	CostQuota       int64  `json:"cost_quota" gorm:"default:0"`
	ProfitQuota     int64  `json:"profit_quota" gorm:"default:0"`
	CommissionQuota int64  `json:"commission_quota" gorm:"default:0"`
	RecordCount     int64  `json:"record_count" gorm:"default:0"`
	LastCreatedAt   int64  `json:"last_created_at" gorm:"default:0"`
}

type BusinessDailyStatsCoverage struct {
	Id          int   `json:"id"`
	StatDate    int64 `json:"stat_date" gorm:"uniqueIndex:idx_business_daily_stats_coverage_date"`
	CompletedAt int64 `json:"completed_at" gorm:"default:0"`
}

type BusinessStatsAppliedBatch struct {
	Id        int    `json:"id"`
	BatchKey  string `json:"batch_key" gorm:"uniqueIndex;type:varchar(255)"`
	AppliedAt int64  `json:"applied_at" gorm:"index;default:0"`
}

type CommissionMonthlyPeriod struct {
	PeriodStartAt int64  `json:"period_start_at"`
	PeriodEndAt   int64  `json:"period_end_at"`
	PeriodKey     string `json:"period_key"`
	Timezone      string `json:"timezone"`
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

func commissionResetDaysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func commissionMonthlyStatLocation(timezone string) (*time.Location, string) {
	return operation_setting.ResolveCommissionTierResetLocation(timezone)
}

func commissionMonthlyScheduledTime(year int, month time.Month, cfg *operation_setting.CommissionTierResetSetting, loc *time.Location) time.Time {
	day := cfg.ResetDay
	if day < 1 {
		day = 1
	}
	if maxDay := commissionResetDaysInMonth(year, month); day > maxDay {
		day = maxDay
	}
	return time.Date(year, month, day, cfg.ResetHour, cfg.ResetMinute, cfg.ResetSecond, 0, loc)
}

func previousCommissionMonth(year int, month time.Month) (int, time.Month) {
	if month == time.January {
		return year - 1, time.December
	}
	return year, month - 1
}

func nextCommissionMonth(year int, month time.Month) (int, time.Month) {
	if month == time.December {
		return year + 1, time.January
	}
	return year, month + 1
}

func ResolveCommissionMonthlyPeriod(createdAt int64) CommissionMonthlyPeriod {
	cfg := operation_setting.GetCommissionTierResetSetting()
	loc, timezone := commissionMonthlyStatLocation(cfg.Timezone)
	t := time.Unix(createdAt, 0).In(loc)
	start := commissionMonthlyScheduledTime(t.Year(), t.Month(), cfg, loc)
	if t.Before(start) {
		year, month := previousCommissionMonth(t.Year(), t.Month())
		start = commissionMonthlyScheduledTime(year, month, cfg, loc)
	}
	nextYear, nextMonth := nextCommissionMonth(start.In(loc).Year(), start.In(loc).Month())
	end := commissionMonthlyScheduledTime(nextYear, nextMonth, cfg, loc).Add(-time.Second)
	return CommissionMonthlyPeriod{
		PeriodStartAt: start.Unix(),
		PeriodEndAt:   end.Unix(),
		PeriodKey:     start.In(loc).Format("2006-01-02"),
		Timezone:      timezone,
	}
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
	// 日聚合表自上线起持续写入（回填功能已移除，历史数据已确认完整），
	// 直接以日表边界为准，无需再查明细台账边界。
	return getBusinessStatsQueryBoundsWithContext(context.Background())
}

func getBusinessStatsQueryBoundsWithContext(ctx context.Context) (int64, int64, error) {
	return getBusinessStatsDailyBoundsWithContext(ctx)
}

func getBusinessStatsDailyBounds() (int64, int64, error) {
	return getBusinessStatsDailyBoundsWithContext(context.Background())
}

func getBusinessStatsDailyBoundsWithContext(ctx context.Context) (int64, int64, error) {
	platformMin, platformMax, err := getStatDateBoundsWithContext(ctx, &PlatformChannelDailyStat{})
	if err != nil {
		return 0, 0, err
	}
	employeeMin, employeeMax, err := getStatDateBoundsWithContext(ctx, &EmployeeCommissionDailyStat{})
	if err != nil {
		return 0, 0, err
	}
	customerMin, customerMax, err := getStatDateBoundsWithContext(ctx, &EmployeeCustomerCommissionDailyStat{})
	if err != nil {
		return 0, 0, err
	}
	minDate := platformMin
	if minDate == 0 || (employeeMin != 0 && employeeMin < minDate) {
		minDate = employeeMin
	}
	if minDate == 0 || (customerMin != 0 && customerMin < minDate) {
		minDate = customerMin
	}
	maxDate := platformMax
	if employeeMax > maxDate {
		maxDate = employeeMax
	}
	if customerMax > maxDate {
		maxDate = customerMax
	}
	return minDate, maxDate, nil
}

func getStatDateBounds(table any) (int64, int64, error) {
	return getStatDateBoundsWithContext(context.Background(), table)
}

func getStatDateBoundsWithContext(ctx context.Context, table any) (int64, int64, error) {
	var result struct {
		MinDate int64
		MaxDate int64
	}
	if err := DB.WithContext(safeDBContext(ctx)).Model(table).
		Select("COALESCE(MIN(stat_date),0) as min_date, COALESCE(MAX(stat_date),0) as max_date").
		Scan(&result).Error; err != nil {
		return 0, 0, err
	}
	return result.MinDate, result.MaxDate, nil
}

func normalizeAggregateDateRange(plan dailyAggregatePlan, startTime, endTime int64) (int64, int64, bool, error) {
	return normalizeAggregateDateRangeWithContext(context.Background(), plan, startTime, endTime)
}

func normalizeAggregateDateRangeWithContext(ctx context.Context, plan dailyAggregatePlan, startTime, endTime int64) (int64, int64, bool, error) {
	if !plan.HasAggregate {
		return 0, 0, false, nil
	}
	startDate := plan.AggregateStartDate
	endDate := plan.AggregateEndDate
	minDate, maxDate, err := getBusinessStatsQueryBoundsWithContext(ctx)
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
	return splitCoveredDailyRangesWithContext(context.Background(), startDate, endDate)
}

func splitCoveredDailyRangesWithContext(ctx context.Context, startDate, endDate int64) ([]statDateRange, []statDateRange, error) {
	if endDate < startDate {
		return nil, nil, nil
	}
	var coveredDates []int64
	if err := DB.WithContext(safeDBContext(ctx)).Model(&BusinessDailyStatsCoverage{}).
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
		ChannelName:   rec.ChannelName,
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
			"channel_name":    gorm.Expr("COALESCE(NULLIF(channel_name, ''), ?)", rec.ChannelName),
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
	Context         context.Context
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
	return ResolveBusinessStatsQueryPlanWithContext(context.Background(), startTime, endTime)
}

func ResolveBusinessStatsQueryPlanWithContext(ctx context.Context, startTime, endTime int64) (*ResolvedQueryPlan, error) {
	plan := splitDailyAggregatePlan(startTime, endTime)
	resolved := &ResolvedQueryPlan{
		Context:      ctx,
		DetailRanges: plan.DetailRanges,
	}
	if plan.HasAggregate {
		startDate, endDate, hasAggregate, err := normalizeAggregateDateRangeWithContext(ctx, plan, startTime, endTime)
		if err != nil {
			return nil, err
		}
		if hasAggregate {
			covered, uncovered, err := splitCoveredDailyRangesWithContext(ctx, startDate, endDate)
			if err != nil {
				return nil, err
			}
			resolved.CoveredRanges = covered
			resolved.UncoveredRanges = uncovered
		}
	}
	return resolved, nil
}

// ResolveBusinessStatsDailyOnlyQueryPlan maps the requested range to whole-day
// aggregate rows and never falls back to ledger/detail aggregation.
func ResolveBusinessStatsDailyOnlyQueryPlan(startTime, endTime int64) (*ResolvedQueryPlan, error) {
	return ResolveBusinessStatsDailyOnlyQueryPlanWithContext(context.Background(), startTime, endTime)
}

func ResolveBusinessStatsDailyOnlyQueryPlanWithContext(ctx context.Context, startTime, endTime int64) (*ResolvedQueryPlan, error) {
	minDate, maxDate, err := getBusinessStatsDailyBoundsWithContext(ctx)
	if err != nil {
		return nil, err
	}
	if maxDate == 0 {
		return &ResolvedQueryPlan{Context: ctx}, nil
	}
	startDate := minDate
	endDate := maxDate
	if startTime != 0 {
		startDate = unixDayStart(startTime)
	}
	if endTime != 0 {
		endDate = unixDayStart(endTime)
	}
	if startDate < minDate {
		startDate = minDate
	}
	if endDate > maxDate {
		endDate = maxDate
	}
	if endDate < startDate {
		return &ResolvedQueryPlan{Context: ctx}, nil
	}
	return &ResolvedQueryPlan{
		Context:       ctx,
		CoveredRanges: []statDateRange{{StartDate: startDate, EndDate: endDate}},
	}, nil
}

func dbWithPlanContext(plan *ResolvedQueryPlan) *gorm.DB {
	if plan != nil && plan.Context != nil {
		return DB.WithContext(safeDBContext(plan.Context))
	}
	return DB
}

func safeDBContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
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
		return tx.Where("(LOWER(channels.name) LIKE ? OR LOWER(platform_channel_daily_stats.channel_name) LIKE ? OR platform_channel_daily_stats.channel_id = ?)", like, like, channelId)
	}
	return tx.Where("(LOWER(channels.name) LIKE ? OR LOWER(platform_channel_daily_stats.channel_name) LIKE ?)", like, like)
}

func getConsumptionCostByChannelFromDaily(startDate, endDate int64, hasEndDate bool) ([]*ConsumptionCostChannelStat, error) {
	return getConsumptionCostByChannelFromDailyWithContext(nil, startDate, endDate, hasEndDate)
}

func getConsumptionCostByChannelFromDailyWithContext(ctx context.Context, startDate, endDate int64, hasEndDate bool) ([]*ConsumptionCostChannelStat, error) {
	type row struct {
		ChannelId    int
		ChannelName  string
		TotalRevenue int64
		TotalCost    int64
		RecordCount  int64
		CostRatioSum float64
	}
	var rows []row
	tx := DB.WithContext(safeDBContext(ctx)).Model(&PlatformChannelDailyStat{}).
		Select("channel_id, "+
			"COALESCE(NULLIF(MAX(channel_name), ''), '') as channel_name, "+
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
			ChannelName:  r.ChannelName,
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

func getConsumptionCostByChannelFromLedgerRange(ctx context.Context, startTime, endTime int64) ([]*ConsumptionCostChannelStat, error) {
	type row struct {
		ChannelId    int
		ChannelName  string
		TotalRevenue int64
		TotalCost    int64
		RecordCount  int64
		CostRatioSum float64
	}
	var rows []row
	tx := DB.WithContext(safeDBContext(ctx)).Model(&ConsumptionCost{}).
		Select("channel_id, "+
			"COALESCE(NULLIF(MAX(channel_name), ''), '') as channel_name, "+
			"COALESCE(SUM(revenue_quota),0) as total_revenue, "+
			"COALESCE(SUM(cost_quota),0) as total_cost, "+
			"COUNT(*) as record_count, "+
			"COALESCE(SUM(cost_ratio),0) as cost_ratio_sum").
		Where("created_at >= ? AND created_at <= ?", startTime, endTime).
		Group("channel_id")
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]*ConsumptionCostChannelStat, 0, len(rows))
	for _, r := range rows {
		items = append(items, &ConsumptionCostChannelStat{
			ChannelId:    r.ChannelId,
			ChannelName:  r.ChannelName,
			TotalRevenue: r.TotalRevenue,
			TotalCost:    r.TotalCost,
			RecordCount:  r.RecordCount,
			CostRatioSum: r.CostRatioSum,
		})
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

// GetConsumptionCostByChannelWithPlan 使用预计算的 daily-only 查询计划按渠道汇总。
func GetConsumptionCostByChannelWithPlan(plan *ResolvedQueryPlan) ([]*ConsumptionCostChannelStat, error) {
	byChannel := make(map[int]*ConsumptionCostChannelStat)
	for _, r := range plan.CoveredRanges {
		rows, err := getConsumptionCostByChannelFromDailyWithContext(plan.Context, r.StartDate, r.EndDate, true)
		if err != nil {
			return nil, err
		}
		mergeConsumptionCostChannelStats(byChannel, rows)
	}
	for _, r := range plan.UncoveredRanges {
		rows, err := getConsumptionCostByChannelFromLedgerRange(plan.Context, r.StartDate, unixDayEnd(r.EndDate))
		if err != nil {
			return nil, err
		}
		mergeConsumptionCostChannelStats(byChannel, rows)
	}
	for _, r := range plan.DetailRanges {
		rows, err := getConsumptionCostByChannelFromLedgerRange(plan.Context, r.StartTime, r.EndTime)
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
	db := dbWithPlanContext(plan)
	if isDailyOnlyPlan(plan) {
		type summaryRow struct {
			TotalRevenue           int64
			TotalCost              int64
			RecordCount            int64
			ProfitableChannelCount int
			LossChannelCount       int
		}
		base := db.Model(&PlatformChannelDailyStat{}).
			Select("channel_id, " +
				"COALESCE(SUM(revenue_quota),0) as total_revenue, " +
				"COALESCE(SUM(cost_quota),0) as total_cost, " +
				"COALESCE(SUM(record_count),0) as record_count").
			Group("channel_id")
		base = applyStatDateRanges(base, plan.CoveredRanges)

		var row summaryRow
		err := db.Table("(?) as grouped", base).
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
	db := dbWithPlanContext(plan)
	if isDailyOnlyPlan(plan) {
		base := db.Model(&PlatformChannelDailyStat{}).
			Joins("LEFT JOIN channels ON channels.id = platform_channel_daily_stats.channel_id")
		base = applyStatDateRanges(base, plan.CoveredRanges)
		base = applyChannelKeyword(base, keyword)

		countBase := base.Session(&gorm.Session{}).
			Select("platform_channel_daily_stats.channel_id").
			Group("platform_channel_daily_stats.channel_id")
		var total int64
		if err := db.Table("(?) as grouped", countBase).Count(&total).Error; err != nil {
			return nil, 0, err
		}

		var rows []*ConsumptionCostChannelStat
		sortExpr := channelSortExpression(sortBy)
		orderDirection := "DESC"
		if strings.EqualFold(sortOrder, "asc") {
			orderDirection = "ASC"
		}
		err := base.Select("platform_channel_daily_stats.channel_id, " +
			"COALESCE(NULLIF(MAX(platform_channel_daily_stats.channel_name), ''), MAX(channels.name), '') as channel_name, " +
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
		channelNames, err := GetChannelNamesByIdsWithContext(plan.Context, channelIds)
		if err != nil {
			return nil, 0, err
		}
		logChannelNames := GetChannelNameSnapshotsFromLogsWithContext(plan.Context, missingChannelNameIds(rows, channelNames))
		for _, row := range rows {
			finalizeConsumptionCostChannelStat(row)
			if row.ChannelName == "" {
				row.ChannelName = channelNames[row.ChannelId]
			}
			if row.ChannelName == "" {
				row.ChannelName = logChannelNames[row.ChannelId]
			}
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
	channelNames, err := GetChannelNamesByIdsWithContext(plan.Context, channelIds)
	if err != nil {
		return nil, 0, err
	}
	logChannelNames := GetChannelNameSnapshotsFromLogsWithContext(plan.Context, missingChannelNameIds(items, channelNames))
	for _, item := range items {
		if item.ChannelName == "" {
			item.ChannelName = channelNames[item.ChannelId]
		}
		if item.ChannelName == "" {
			item.ChannelName = logChannelNames[item.ChannelId]
		}
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

func missingChannelNameIds(items []*ConsumptionCostChannelStat, channelNames map[int]string) []int {
	seen := make(map[int]bool)
	ids := make([]int, 0)
	for _, item := range items {
		if item == nil || item.ChannelName != "" || channelNames[item.ChannelId] != "" {
			continue
		}
		if !seen[item.ChannelId] {
			seen[item.ChannelId] = true
			ids = append(ids, item.ChannelId)
		}
	}
	return ids
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
		// 保留日聚合表里的名称快照，渠道删除后仍可显示
		if item.ChannelName == "" {
			item.ChannelName = row.ChannelName
		}
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
	return getCommissionStatsByEmployeeFromDailyWithContext(nil, startDate, endDate, hasEndDate)
}

func getCommissionStatsByEmployeeFromDailyWithContext(ctx context.Context, startDate, endDate int64, hasEndDate bool) ([]*CommissionEmployeeStat, error) {
	var rows []*CommissionEmployeeStat
	tx := DB.WithContext(safeDBContext(ctx)).Model(&EmployeeCommissionDailyStat{}).
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

func getCommissionStatsByEmployeeFromLedgerRange(ctx context.Context, startTime, endTime int64) ([]*CommissionEmployeeStat, error) {
	var rows []*CommissionEmployeeStat
	err := DB.WithContext(safeDBContext(ctx)).Model(&EmployeeCommissionLog{}).
		Select("employee_user_id, "+
			"COALESCE(SUM(revenue_quota),0) as total_revenue, "+
			"COALESCE(SUM(cost_quota),0) as total_cost, "+
			"COALESCE(SUM(profit_quota),0) as total_profit, "+
			"COALESCE(SUM(commission_quota),0) as total_commission, "+
			"COUNT(*) as record_count").
		Where("created_at >= ? AND created_at <= ?", startTime, endTime).
		Group("employee_user_id").
		Scan(&rows).Error
	return rows, err
}

func getCommissionStatsByEmployeeFromStats(startTime, endTime int64, limit int) ([]*CommissionEmployeeStat, error) {
	resolved, err := ResolveBusinessStatsQueryPlan(startTime, endTime)
	if err != nil {
		return nil, err
	}
	return GetCommissionStatsByEmployeeWithPlan(resolved, limit)
}

// GetCommissionStatsByEmployeeWithPlan 使用预计算的 daily-only 查询计划按员工汇总。
func GetCommissionStatsByEmployeeWithPlan(plan *ResolvedQueryPlan, limit int) ([]*CommissionEmployeeStat, error) {
	byEmployee := make(map[int]*CommissionEmployeeStat)
	for _, r := range plan.CoveredRanges {
		rows, err := getCommissionStatsByEmployeeFromDailyWithContext(plan.Context, r.StartDate, r.EndDate, true)
		if err != nil {
			return nil, err
		}
		mergeCommissionEmployeeStats(byEmployee, rows)
	}
	for _, r := range plan.UncoveredRanges {
		rows, err := getCommissionStatsByEmployeeFromLedgerRange(plan.Context, r.StartDate, unixDayEnd(r.EndDate))
		if err != nil {
			return nil, err
		}
		mergeCommissionEmployeeStats(byEmployee, rows)
	}
	for _, r := range plan.DetailRanges {
		rows, err := getCommissionStatsByEmployeeFromLedgerRange(plan.Context, r.StartTime, r.EndTime)
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
	db := dbWithPlanContext(plan)
	if isDailyOnlyPlan(plan) {
		base := db.Model(&EmployeeCommissionDailyStat{})
		base = applyStatDateRanges(base, plan.CoveredRanges)

		countBase := base.Session(&gorm.Session{}).
			Select("employee_user_id").
			Group("employee_user_id")
		var total int64
		if err := db.Table("(?) as grouped", countBase).Count(&total).Error; err != nil {
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
