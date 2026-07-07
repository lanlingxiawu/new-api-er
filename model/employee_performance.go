package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ============================================================================
// 员工业绩手工调整（加/减业绩）
//
// 设计见 docs/employee-performance-adjustment-design.md。核心：不新增表，把手工
// 调整建模成一笔与真实结算同构的加法——往 employee_commission_logs 插一条 sentinel
// 流水行，并同步上盘到「本期」「累计」两张日聚合表，从而所有既有列表/日历/导出/等级
// 逻辑零改动即可显示。谁/为何写入既有系统日志（RecordLogWithAdminInfo）。
//
//   - sentinel：model_name = ManualPerformanceModelName，channel_id/customer_user_id=0，
//     log_id=nil（三库均允许多个 NULL 唯一值）。
//   - 撤销复用 settle_status 语义（2=已撤销）标记原行，并插一条相反数补偿行使聚合净为 0。
//   - 业绩=profit；revenue=profit、cost=0（保 profit=revenue-cost 不变式，决策 B）。
//   - 当前周期调整后触发等级双向重估（决策 A）；历史周期用还原的历史费率、且不改等级。
// ============================================================================

// ManualPerformanceModelName 手工业绩调整流水行的 model_name sentinel。
const ManualPerformanceModelName = "管理员添加业绩"

// ManualPerformanceRevertModelName 撤销补偿行的 sentinel：与原调整区分，
// 使调整记录列表只展示原始调整、不展示内部反向补偿行（补偿行仍进聚合表使净额为 0）。
const ManualPerformanceRevertModelName = "管理员减少业绩"

// settleStatusReverted 复用 EmployeeCommissionLog.SettleStatus 的「2=已撤销」语义。
const settleStatusReverted = 2

var employeePerfNow = func() int64 { return time.Now().Unix() }

// PerformanceAdjustmentResult 手工业绩调整结果（用于回执与日志）。
type PerformanceAdjustmentResult struct {
	LogId           int     `json:"log_id"`
	EmployeeUserId  int     `json:"employee_user_id"`
	ResetStartedAt  int64   `json:"reset_started_at"`
	StatDate        int64   `json:"stat_date"`
	ProfitQuota     int64   `json:"profit_quota"`
	CommissionQuota int64   `json:"commission_quota"`
	CommissionRate  float64 `json:"commission_rate"`
	IsHistorical    bool    `json:"is_historical"`
}

// calcManualPerfCommissionQuota 计算手工业绩对应的提成额度（decimal 精度）。
// 与 service.calcCommissionQuota 保持一致：正/负利润各有 ±1 保底，避免精度截断丢失极小值。
// 因 model 不能反向依赖 service，此处内联同逻辑。
func calcManualPerfCommissionQuota(profitQuota int64, rate float64) int64 {
	if profitQuota == 0 || rate <= 0 {
		return 0
	}
	result := decimal.NewFromInt(profitQuota).Mul(decimal.NewFromFloat(rate)).Round(0)
	if result.IsZero() {
		if profitQuota > 0 {
			return 1
		}
		return -1
	}
	return result.IntPart()
}

// resolveManualPerfBucket 还原一笔手工调整应归属的 (reset_started_at, stat_date)。
// 逻辑与结算路径一致：createdAt 落在「当前本期」→ 用 effectiveResetStartedAt（含 baseline
// 锚定）；落在历史周期 → 用该历史周期的自然起点。add 与 revert 复用同一函数，保证补偿行与
// 原行落在同一桶、同一天，聚合可精确净为 0。
func resolveManualPerfBucket(userId int, createdAt int64) (resetStartedAt int64, statDate int64) {
	period := ResolveCommissionMonthlyPeriod(createdAt)
	currentPeriodStart := ResolveCommissionMonthlyPeriod(employeePerfNow()).PeriodStartAt
	if period.PeriodStartAt == currentPeriodStart {
		level, _ := GetOrCreateTierLevel(userId, false)
		resetStartedAt = effectiveResetStartedAt(level, createdAt)
	} else {
		resetStartedAt = period.PeriodStartAt
	}
	return resetStartedAt, localDayStart(createdAt)
}

// AddEmployeePerformance 给员工手工追加业绩（profitQuota 可负）。
// targetPeriodStartAt<=0 表示当前周期；>0 表示补录到该历史周期（取其自然起点所在周期）。
func AddEmployeePerformance(employeeUserId int, profitQuota int64, reason string, operatedBy int, targetPeriodStartAt int64) (*PerformanceAdjustmentResult, error) {
	if profitQuota == 0 {
		return nil, errors.New("profit amount cannot be zero")
	}
	var emp EmployeeProfile
	if err := DB.Where("user_id = ?", employeeUserId).First(&emp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("employee profile not found")
		}
		return nil, err
	}

	now := employeePerfNow()
	currentPeriodStart := ResolveCommissionMonthlyPeriod(now).PeriodStartAt

	var createdAt int64
	if targetPeriodStartAt <= 0 {
		createdAt = now
	} else {
		p := ResolveCommissionMonthlyPeriod(targetPeriodStartAt)
		// 落在该历史周期内，用周期结束时刻（不超过当前时间），使 stat_date 归入该周期最后一天。
		createdAt = p.PeriodEndAt
		if createdAt > now {
			createdAt = now
		}
		if createdAt < p.PeriodStartAt {
			createdAt = p.PeriodStartAt
		}
	}

	period := ResolveCommissionMonthlyPeriod(createdAt)
	isHistorical := period.PeriodStartAt != currentPeriodStart

	// 费率：历史周期取该期结束时刻还原的历史等级费率；当前周期取现有效费率。
	var rate float64
	if isHistorical {
		tiers := GetAllTiersCached()
		tierById := make(map[int64]*EmployeeCommissionTier, len(tiers))
		for _, t := range tiers {
			tierById[t.Id] = t
		}
		if t := tierById[resolveEmployeeTierAt(employeeUserId, period.PeriodEndAt)]; t != nil {
			rate = t.Rate
		}
	} else {
		rate = GetEffectiveCommissionRate(employeeUserId)
	}

	revenueQuota := profitQuota // 决策 B：revenue=profit, cost=0
	costQuota := int64(0)
	commissionQuota := calcManualPerfCommissionQuota(profitQuota, rate)
	resetStartedAt, statDate := resolveManualPerfBucket(employeeUserId, createdAt)

	commLog := &EmployeeCommissionLog{
		EmployeeId:      emp.Id,
		EmployeeUserId:  employeeUserId,
		CustomerUserId:  0,
		LogId:           nil,
		ModelName:       ManualPerformanceModelName,
		ChannelId:       0,
		RevenueQuota:    revenueQuota,
		CostQuota:       costQuota,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		CommissionRate:  rate,
		SettleStatus:    0,
		CreatedAt:       createdAt,
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(commLog).Error; err != nil {
			return err
		}
		if err := upsertCommissionResetPeriodDailyStatTx(tx, resetStartedAt, statDate, employeeUserId,
			revenueQuota, costQuota, profitQuota, commissionQuota, 1, createdAt); err != nil {
			return err
		}
		if err := upsertCommissionDailyStatTx(tx, statDate, employeeUserId,
			revenueQuota, costQuota, profitQuota, commissionQuota, 1, createdAt); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	RecordLogWithAdminInfo(employeeUserId, LogTypeManage, "manual performance adjustment", map[string]interface{}{
		"operated_by":      operatedBy,
		"reason":           reason,
		"profit_quota":     profitQuota,
		"commission_quota": commissionQuota,
		"commission_rate":  rate,
		"reset_started_at": resetStartedAt,
		"is_historical":    isHistorical,
		"log_id":           commLog.Id,
	})

	// 决策 A：仅当前周期触发等级双向重估；历史周期封存不动。
	if !isHistorical {
		reevaluateTierByPeriodProfit(employeeUserId)
	}

	return &PerformanceAdjustmentResult{
		LogId:           commLog.Id,
		EmployeeUserId:  employeeUserId,
		ResetStartedAt:  resetStartedAt,
		StatDate:        statDate,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		CommissionRate:  rate,
		IsHistorical:    isHistorical,
	}, nil
}

// RevertEmployeePerformance 撤销一笔手工业绩调整：标记原行 settle_status=2，并插一条相反数
// 补偿行、对两张日聚合表上盘负增量（含 record_count=-1），使聚合精确净为 0。
func RevertEmployeePerformance(logId int, operatedBy int) error {
	var orig EmployeeCommissionLog
	if err := DB.Where("id = ?", logId).First(&orig).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("adjustment not found")
		}
		return err
	}
	if orig.ModelName != ManualPerformanceModelName {
		return errors.New("not a manual performance adjustment")
	}
	if orig.SettleStatus == settleStatusReverted {
		return errors.New("adjustment already reverted")
	}

	now := employeePerfNow()
	resetStartedAt, statDate := resolveManualPerfBucket(orig.EmployeeUserId, orig.CreatedAt)

	comp := &EmployeeCommissionLog{
		EmployeeId:      orig.EmployeeId,
		EmployeeUserId:  orig.EmployeeUserId,
		CustomerUserId:  0,
		LogId:           nil,
		ModelName:       ManualPerformanceRevertModelName,
		ChannelId:       0,
		RevenueQuota:    -orig.RevenueQuota,
		CostQuota:       -orig.CostQuota,
		ProfitQuota:     -orig.ProfitQuota,
		CommissionQuota: -orig.CommissionQuota,
		CommissionRate:  orig.CommissionRate,
		SettleStatus:    settleStatusReverted, // 补偿行本身即撤销条目，不再计为「有效」
		SettledAt:       now,
		CreatedAt:       orig.CreatedAt, // 与原行同桶同天，保证聚合净为 0
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		// 并发保护：仅当原行仍未撤销时才翻转，避免重复撤销。
		res := tx.Model(&EmployeeCommissionLog{}).
			Where("id = ? AND settle_status <> ?", logId, settleStatusReverted).
			Updates(map[string]interface{}{"settle_status": settleStatusReverted, "settled_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("adjustment already reverted")
		}
		if err := tx.Create(comp).Error; err != nil {
			return err
		}
		if err := upsertCommissionResetPeriodDailyStatTx(tx, resetStartedAt, statDate, orig.EmployeeUserId,
			-orig.RevenueQuota, -orig.CostQuota, -orig.ProfitQuota, -orig.CommissionQuota, -1, now); err != nil {
			return err
		}
		if err := upsertCommissionDailyStatTx(tx, statDate, orig.EmployeeUserId,
			-orig.RevenueQuota, -orig.CostQuota, -orig.ProfitQuota, -orig.CommissionQuota, -1, now); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	RecordLogWithAdminInfo(orig.EmployeeUserId, LogTypeManage, "revert manual performance adjustment", map[string]interface{}{
		"operated_by":      operatedBy,
		"reverted_log_id":  logId,
		"compensation_log": comp.Id,
		"profit_quota":     -orig.ProfitQuota,
		"commission_quota": -orig.CommissionQuota,
	})

	// 撤销的是当前周期的调整时，触发一次双向重估，使等级回落到与净业绩匹配的档位。
	if ResolveCommissionMonthlyPeriod(orig.CreatedAt).PeriodStartAt == ResolveCommissionMonthlyPeriod(now).PeriodStartAt {
		reevaluateTierByPeriodProfit(orig.EmployeeUserId)
	}
	return nil
}

// GetManualPerformanceAdjustments 分页查询某员工的手工业绩调整流水（精确匹配 sentinel）。
func GetManualPerformanceAdjustments(employeeUserId, page, pageSize int) ([]*EmployeeCommissionLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	tx := DB.Model(&EmployeeCommissionLog{}).Where("model_name = ?", ManualPerformanceModelName)
	if employeeUserId != 0 {
		tx = tx.Where("employee_user_id = ?", employeeUserId)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []*EmployeeCommissionLog
	if err := tx.Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// reevaluateTierByPeriodProfit 依当前周期业绩对员工等级做双向重估（升/降）。
// 仅用于手工调整路径——组织化结算仍沿用「只升不降」的 TryAutoUpgradeTier。规则：
//   - 在员工当前分组内，取阈值 <= 本期业绩(USD) 的最高等级为目标；若都不满足则回落到该分组
//     最低等级（不解绑分组）；
//   - 目标与现等级不同则以 source=auto 变更并留痕（tier log）。
//   - 未绑定等级的员工只做升级（沿用 TryAutoUpgradeTier，默认「通用」分组），不主动绑定。
func reevaluateTierByPeriodProfit(userId int) {
	tiers := GetAllTiersCached()
	if len(tiers) == 0 {
		return
	}
	level, err := GetOrCreateTierLevel(userId, false)
	if err != nil {
		return
	}
	if level.TierId == 0 {
		TryAutoUpgradeTier(userId)
		return
	}

	// 现等级 → 分组
	var currentGroup string
	found := false
	for _, t := range tiers {
		if t.Id == level.TierId {
			currentGroup = t.Group
			found = true
			break
		}
	}
	if !found {
		// 绑定的等级已被删除，避免静默切组，跳过。
		return
	}

	resetStartedAt := level.BaselineResetAt
	if resetStartedAt == 0 {
		resetStartedAt = ResolveCommissionMonthlyPeriod(employeePerfNow()).PeriodStartAt
	}
	var periodProfit int64
	if err := DB.Model(&EmployeeCommissionResetPeriodDailyStat{}).
		Select("COALESCE(SUM(profit_quota), 0)").
		Where("employee_user_id = ? AND reset_started_at = ?", userId, resetStartedAt).
		Scan(&periodProfit).Error; err != nil {
		common.SysError("reevaluateTierByPeriodProfit: query period profit failed: " + err.Error())
		return
	}
	profitUsd := common.QuotaToUSD(periodProfit)

	// 目标：分组内阈值达标的最高等级；均不达标则回落到分组最低等级。
	var best, floor *EmployeeCommissionTier
	for _, t := range tiers {
		if t.Group != currentGroup {
			continue
		}
		if floor == nil || t.Level < floor.Level {
			floor = t
		}
		if profitUsd >= t.ThresholdUsd {
			if best == nil || t.Level > best.Level {
				best = t
			}
		}
	}
	target := best
	if target == nil {
		target = floor
	}
	if target == nil || target.Id == level.TierId {
		return
	}
	if err := SetTierLevel(userId, target.Id, "auto", 0, "", profitUsd); err != nil {
		common.SysError("reevaluateTierByPeriodProfit: set tier level failed: " + err.Error())
	}
}
