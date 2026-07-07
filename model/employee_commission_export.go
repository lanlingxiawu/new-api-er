package model

import (
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// CommissionMonthlyEmployeeRow 是月度统计导出的单员工一行：名字、等级、业绩、分红。
// ProfitQuota 为该期业绩（利润），CommissionQuota = 业绩 × 该期等级费率（与页面"本期提成"口径一致）。
type CommissionMonthlyEmployeeRow struct {
	EmployeeUserId  int     `json:"employee_user_id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"display_name"`
	Remark          string  `json:"remark"`
	TierLevel       int     `json:"tier_level"`
	TierGroup       string  `json:"tier_group"`
	TierRate        float64 `json:"tier_rate"`
	ProfitQuota     int64   `json:"profit_quota"`
	CommissionQuota int64   `json:"commission_quota"`
	RecordCount     int64   `json:"record_count"`
	ProfitUsd       float64 `json:"profit_usd"`
	CommissionUsd   float64 `json:"commission_usd"`
}

// CommissionMonthlyExport 是月度统计导出的完整载荷：周期信息 + 各员工明细行。
type CommissionMonthlyExport struct {
	PeriodStartAt int64                           `json:"period_start_at"`
	PeriodEndAt   int64                           `json:"period_end_at"`
	PeriodKey     string                          `json:"period_key"`
	Timezone      string                          `json:"timezone"`
	IsHistorical  bool                            `json:"is_historical"`
	Rows          []*CommissionMonthlyEmployeeRow `json:"rows"`
}

// GetCommissionMonthlyEmployeeExport 返回所查月份下【每个在职员工】的业绩/分红/等级/名字，
// 用于月度统计导出。周期口径与日历一致：
//   - 当前本期：按各员工 baseline 锚定的桶聚合，等级取现等级；
//   - 历史周期：按自然月 stat_date 跨所有旧桶聚合，等级由变更日志还原当期结束时刻的等级。
//
// 业绩来自日统计表按员工 SUM，分红 = 业绩 × 该期等级费率（四舍五入，与页面口径一致）。
// 覆盖全部在职员工（无数据者业绩/分红为 0，仍带出名字与等级）。
func GetCommissionMonthlyEmployeeExport(startTime, endTime int64) (*CommissionMonthlyExport, error) {
	period := ResolveCommissionMonthlyPeriod(startTime)
	currentPeriodStart := ResolveCommissionMonthlyPeriod(time.Now().Unix()).PeriodStartAt
	isHistorical := period.PeriodStartAt != currentPeriodStart
	queryEndAt := period.PeriodEndAt
	if endTime > 0 && queryEndAt > endTime {
		queryEndAt = endTime
	}

	export := &CommissionMonthlyExport{
		PeriodStartAt: period.PeriodStartAt,
		PeriodEndAt:   period.PeriodEndAt,
		PeriodKey:     period.PeriodKey,
		Timezone:      period.Timezone,
		IsHistorical:  isHistorical,
		Rows:          make([]*CommissionMonthlyEmployeeRow, 0),
	}

	// 1) 全部在职员工的名字/备注。
	type empRow struct {
		UserId      int
		Username    string
		DisplayName string
		Remark      string
	}
	var emps []empRow
	if err := DB.Model(&EmployeeProfile{}).
		Select("employee_profiles.user_id AS user_id, users.username AS username, users.display_name AS display_name, employee_profiles.remark AS remark").
		Joins("LEFT JOIN users ON users.id = employee_profiles.user_id").
		Where("employee_profiles.status = ?", 1).
		Scan(&emps).Error; err != nil {
		return nil, err
	}
	if len(emps) == 0 {
		return export, nil
	}
	empIds := make([]int, 0, len(emps))
	for _, e := range emps {
		empIds = append(empIds, e.UserId)
	}

	// 2) 各员工该期业绩 + 记录数聚合。
	type aggRow struct {
		EmployeeUserId int
		ProfitQuota    int64
		RecordCount    int64
	}
	var aggs []aggRow
	aggTx := DB.Model(&EmployeeCommissionResetPeriodDailyStat{}).
		Select("employee_commission_reset_period_daily_stats.employee_user_id AS employee_user_id, " +
			"COALESCE(SUM(employee_commission_reset_period_daily_stats.profit_quota),0) AS profit_quota, " +
			"COALESCE(SUM(employee_commission_reset_period_daily_stats.record_count),0) AS record_count")
	if isHistorical {
		// 历史周期：自然月切片，跨所有旧桶（不限定 reset_started_at）。
		aggTx = aggTx.Where(
			"employee_commission_reset_period_daily_stats.stat_date >= ? AND employee_commission_reset_period_daily_stats.stat_date <= ? AND employee_commission_reset_period_daily_stats.employee_user_id IN ?",
			commissionStatDayStart(period.PeriodStartAt),
			commissionStatDayStart(queryEndAt),
			empIds,
		)
	} else {
		// 当前本期：按各员工 baseline 锚定的桶（baseline=0 时回退 periodStart）。
		aggTx = aggTx.Joins(
			"LEFT JOIN employee_tier_levels l ON l.user_id = employee_commission_reset_period_daily_stats.employee_user_id",
		).Where(
			"employee_commission_reset_period_daily_stats.reset_started_at = COALESCE(NULLIF(l.baseline_reset_at,0), ?) "+
				"AND employee_commission_reset_period_daily_stats.stat_date >= ? AND employee_commission_reset_period_daily_stats.stat_date <= ? "+
				"AND employee_commission_reset_period_daily_stats.employee_user_id IN ?",
			period.PeriodStartAt,
			commissionStatDayStart(period.PeriodStartAt),
			commissionStatDayStart(queryEndAt),
			empIds,
		)
	}
	if err := aggTx.Group("employee_commission_reset_period_daily_stats.employee_user_id").Scan(&aggs).Error; err != nil {
		return nil, err
	}
	profitByUser := make(map[int]int64, len(aggs))
	recordByUser := make(map[int]int64, len(aggs))
	for _, a := range aggs {
		profitByUser[a.EmployeeUserId] = a.ProfitQuota
		recordByUser[a.EmployeeUserId] = a.RecordCount
	}

	// 3) 各员工该期等级：历史从变更日志还原，本期取现等级。
	tierIdByUser := make(map[int]int64, len(empIds))
	if isHistorical {
		tierIdByUser = resolveEmployeeTiersAt(empIds, period.PeriodEndAt)
	} else if levels, err := GetTierLevelsByUserIds(empIds); err == nil {
		for uid, lvl := range levels {
			if lvl != nil {
				tierIdByUser[uid] = lvl.TierId
			}
		}
	}
	tiers := GetAllTiersCached()
	tierById := make(map[int64]*EmployeeCommissionTier, len(tiers))
	for _, t := range tiers {
		tierById[t.Id] = t
	}

	// 4) 组装每行：业绩 × 该期费率 = 分红。
	for _, e := range emps {
		profit := profitByUser[e.UserId]
		row := &CommissionMonthlyEmployeeRow{
			EmployeeUserId: e.UserId,
			Username:       e.Username,
			DisplayName:    e.DisplayName,
			Remark:         e.Remark,
			ProfitQuota:    profit,
			RecordCount:    recordByUser[e.UserId],
			ProfitUsd:      common.QuotaToUSD(profit),
		}
		if tid := tierIdByUser[e.UserId]; tid != 0 {
			if t, ok := tierById[tid]; ok {
				row.TierLevel = t.Level
				row.TierGroup = t.Group
				row.TierRate = t.Rate
				row.CommissionQuota = int64(float64(profit) * t.Rate)
			}
		}
		row.CommissionUsd = common.QuotaToUSD(row.CommissionQuota)
		export.Rows = append(export.Rows, row)
	}

	// 业绩从高到低排序，便于查看。
	sort.SliceStable(export.Rows, func(i, j int) bool {
		return export.Rows[i].ProfitQuota > export.Rows[j].ProfitQuota
	})

	return export, nil
}
