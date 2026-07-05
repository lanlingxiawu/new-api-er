package model

import (
	"fmt"

	"gorm.io/gorm"
)

// SwitchCommissionPeriod 在管理员切换统计口径（统计方式 / 每月重置日 / 时区）后调用，
// 把"本期"安全地对齐到新配置下的本期起点 targetPeriodStart(P)。两个开关分别控制两件事：
//
//   - resetTiers=true：把所有在职员工降到本组最低等级并写重置日志；false：等级保持不变。
//   - includePeriodData=true：把"按新边界属于本期"（stat_date >= P 当天、却仍落在旧桶
//     reset_started_at < P）的日统计行归并进 P 桶，使本期统计完整、无"丢数据"观感；
//     false：不归并，本期从修改时刻起重新累计，旧数据留在上期桶。
//
// 无论开关如何，都会把在职员工的 baseline_reset_at 对齐到 P（让"本期"定义随新口径生效），
// 并在最后按（对齐后的）本期利润做一次"只升不降"的自动升档。
//
// 该函数只做数据/等级迁移，不涉及 last_reset_at（由调用方在切换后单独 arm 到 P，避免定时
// 任务补跑）。执行前会 FlushBusinessStatBuffers，保证归桶/对齐看到的是最新落库数据。
func SwitchCommissionPeriod(targetPeriodStart int64, resetTiers, includePeriodData bool, operatedBy int) (int, error) {
	if targetPeriodStart <= 0 {
		return 0, fmt.Errorf("invalid target period start: %d", targetPeriodStart)
	}

	var employeeUserIds []int
	if err := DB.Model(&EmployeeProfile{}).Select("user_id").Where("status = ?", 1).Scan(&employeeUserIds).Error; err != nil {
		return 0, err
	}
	if len(employeeUserIds) == 0 {
		return 0, nil
	}
	if err := ensureTierLevelsForUserIds(employeeUserIds); err != nil {
		return 0, err
	}

	// 先把缓冲区里尚未落库的统计刷入 DB，保证后续归桶/对齐基于最新数据。
	FlushBusinessStatBuffers()

	// 1) baseline 对齐到 P，先做（新数据从此刻起落入 P 桶），可选同时重置等级。
	processed := 0
	if resetTiers {
		for {
			n, selected, err := ResetEmployeeTierLevelsForPeriod(targetPeriodStart, tierResetSwitchBatchSize, operatedBy)
			if err != nil {
				return processed, err
			}
			processed += n
			if selected < tierResetSwitchBatchSize {
				break
			}
		}
	} else {
		n, err := advanceCommissionBaselineOnly(targetPeriodStart, employeeUserIds)
		if err != nil {
			return processed, err
		}
		processed = n
	}

	// 2) 归桶：把落在旧桶、但按 stat_date 属于本期（>= P 当天）的日统计行并入 P 桶。
	//    在 baseline 对齐之后执行，可顺带把"对齐窗口内仍以旧 baseline 落库"的零星行一并归位。
	if includePeriodData {
		pDayStart := commissionStatDayStart(targetPeriodStart)
		if err := rebucketCurrentPeriodDailyStats(targetPeriodStart, pDayStart, employeeUserIds); err != nil {
			return processed, err
		}
	}

	// 3) 按对齐后的本期利润做一次自动升档（只升不降）：
	//    - resetTiers=true 时把刚降到最低的等级按完整本期利润重排；
	//    - resetTiers=false 且 includePeriodData=true 时，补齐后利润可能触达更高档位。
	tryAutoUpgradeTierBatch(employeeUserIds)

	return processed, nil
}

const tierResetSwitchBatchSize = 300

// advanceCommissionBaselineOnly 仅把 baseline_reset_at 前移到 P（不动等级、不写日志），
// 幂等守卫 baseline_reset_at < P 保证重复调用安全。返回实际推进的行数。
func advanceCommissionBaselineOnly(targetPeriodStart int64, employeeUserIds []int) (int, error) {
	const batch = 300
	total := 0
	for i := 0; i < len(employeeUserIds); i += batch {
		end := i + batch
		if end > len(employeeUserIds) {
			end = len(employeeUserIds)
		}
		ids := employeeUserIds[i:end]
		res := DB.Model(&EmployeeTierLevel{}).
			Where("user_id IN ? AND baseline_reset_at < ?", ids, targetPeriodStart).
			Update("baseline_reset_at", targetPeriodStart)
		if res.Error != nil {
			return total, res.Error
		}
		total += int(res.RowsAffected)
		refreshTierLevelCacheFromDBByUserIds(ids)
	}
	return total, nil
}

const (
	// 单个归桶事务处理的员工上限：限住单事务持锁的行数（≈ chunk × 本期天数），缩短持锁窗口，
	// 降低与后台统计刷盘（同表 upsert）之间的锁等待/死锁概率。
	rebucketEmployeeChunk = 50
	// 批量 UPDATE/DELETE 单条 IN 列表的上限，避免超大 IN。
	rebucketIDChunk = 500
)

// rebucketCurrentPeriodDailyStats 把 employee_commission_reset_period_daily_stats 中
// "reset_started_at < P 且 stat_date >= pDayStart"的行归并进 P 桶：
//   - 目标桶已存在同 (stat_date, employee_user_id) 行 → 金额求和并入、删除源行；
//   - 不存在 → 直接把该行 reset_started_at 改为 P。
//
// 唯一键为 (reset_started_at, stat_date, employee_user_id)，合并在 Go 内完成，
// 不依赖任何数据库方言的 upsert，保证 SQLite/MySQL/PostgreSQL 三库一致（项目 Rule 2）。
// 幂等：归并后源行 reset_started_at 已为 P，重复调用不再匹配 reset_started_at < P。
// 按 rebucketEmployeeChunk 分批、每批一个短事务，避免单个长事务持锁过多行。
func rebucketCurrentPeriodDailyStats(targetPeriodStart, pDayStart int64, employeeUserIds []int) error {
	for i := 0; i < len(employeeUserIds); i += rebucketEmployeeChunk {
		end := i + rebucketEmployeeChunk
		if end > len(employeeUserIds) {
			end = len(employeeUserIds)
		}
		ids := employeeUserIds[i:end]
		if err := rebucketDailyStatsForEmployees(targetPeriodStart, pDayStart, ids); err != nil {
			return err
		}
	}
	return nil
}

type dailyStatKey struct {
	statDate       int64
	employeeUserId int
}

// mergeTarget 记录某 (stat_date, employee) key 的归并落脚行。
type mergeTarget struct {
	row      *EmployeeCommissionResetPeriodDailyStat
	fromP    bool // true=P 桶原有行；false=被改挂到 P 的源行
	absorbed bool // 是否合并过其它源行（决定是否需要单独写金额）
}

// rebucketDailyStatsForEmployees 把一小批员工「本期」旧桶行归并到 P 桶。合并仅在 Go 内完成，
// 落库时把「纯改挂」（绝大多数）折叠成批量 UPDATE、被合并掉的源行折叠成批量 DELETE，
// 只有真正冲突/重复键的少数行才逐行写金额——从而把单事务里的 SQL 条数从 O(行数) 降到 O(分片数)，
// 显著缩短持锁时间。语义与逐行版本完全一致（同样的最终金额与桶归属）。
func rebucketDailyStatsForEmployees(targetPeriodStart, pDayStart int64, ids []int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var src []EmployeeCommissionResetPeriodDailyStat
		if err := tx.Where(
			"reset_started_at < ? AND stat_date >= ? AND employee_user_id IN ?",
			targetPeriodStart, pDayStart, ids,
		).Find(&src).Error; err != nil {
			return err
		}
		if len(src) == 0 {
			return nil
		}

		// 载入目标 P 桶已存在的同 (stat_date, employee) 行，用于合并判定。
		statDateSet := make(map[int64]struct{}, len(src))
		for i := range src {
			statDateSet[src[i].StatDate] = struct{}{}
		}
		statDates := make([]int64, 0, len(statDateSet))
		for d := range statDateSet {
			statDates = append(statDates, d)
		}
		var existing []EmployeeCommissionResetPeriodDailyStat
		if err := tx.Where(
			"reset_started_at = ? AND employee_user_id IN ? AND stat_date IN ?",
			targetPeriodStart, ids, statDates,
		).Find(&existing).Error; err != nil {
			return err
		}
		targetByKey := make(map[dailyStatKey]*mergeTarget, len(existing)+len(src))
		for i := range existing {
			row := &existing[i]
			targetByKey[dailyStatKey{row.StatDate, row.EmployeeUserId}] = &mergeTarget{row: row, fromP: true}
		}

		// 分类：首次出现的 key 以该源行作为落脚点；重复 key（P 桶已有行，或同键的其它旧桶源行）
		// 把金额累加进落脚行的内存副本，并记源行待删除。
		deleteIDs := make([]int, 0)
		for i := range src {
			s := &src[i]
			key := dailyStatKey{s.StatDate, s.EmployeeUserId}
			if t, ok := targetByKey[key]; ok {
				t.row.RevenueQuota += s.RevenueQuota
				t.row.CostQuota += s.CostQuota
				t.row.ProfitQuota += s.ProfitQuota
				t.row.CommissionQuota += s.CommissionQuota
				t.row.RecordCount += s.RecordCount
				if s.LastCreatedAt > t.row.LastCreatedAt {
					t.row.LastCreatedAt = s.LastCreatedAt
				}
				t.absorbed = true
				deleteIDs = append(deleteIDs, s.Id)
			} else {
				targetByKey[key] = &mergeTarget{row: s, fromP: false}
			}
		}

		// 汇总三类落库操作（每个 key 恰好命中其一，互不重叠）：
		//   pureMoveIDs    —— 落脚源行且未合并过任何行 → 批量只改 reset_started_at；
		//   movedMerged    —— 落脚源行且合并过其它行     → 单独写 reset_started_at + 金额；
		//   existingMerged —— P 桶原有行且被合并过        → 单独写金额。
		pureMoveIDs := make([]int, 0, len(src))
		var movedMerged []*EmployeeCommissionResetPeriodDailyStat
		var existingMerged []*EmployeeCommissionResetPeriodDailyStat
		for _, t := range targetByKey {
			switch {
			case t.fromP && t.absorbed:
				existingMerged = append(existingMerged, t.row)
			case !t.fromP && t.absorbed:
				movedMerged = append(movedMerged, t.row)
			case !t.fromP && !t.absorbed:
				pureMoveIDs = append(pureMoveIDs, t.row.Id)
			}
			// t.fromP && !t.absorbed：P 桶原有行未被触碰，无需落库。
		}

		// 1) 批量改挂（绝大多数走这里，一条 UPDATE 处理一片）。
		if err := updateResetStartedAtByIDs(tx, pureMoveIDs, targetPeriodStart); err != nil {
			return err
		}
		// 2) 少数：被合并的落脚源行（改挂 + 金额）。
		for _, row := range movedMerged {
			if err := tx.Model(&EmployeeCommissionResetPeriodDailyStat{}).
				Where("id = ?", row.Id).
				Updates(map[string]interface{}{
					"reset_started_at": targetPeriodStart,
					"revenue_quota":    row.RevenueQuota,
					"cost_quota":       row.CostQuota,
					"profit_quota":     row.ProfitQuota,
					"commission_quota": row.CommissionQuota,
					"record_count":     row.RecordCount,
					"last_created_at":  row.LastCreatedAt,
				}).Error; err != nil {
				return err
			}
		}
		// 3) 少数：被合并的 P 桶原有行（只更金额）。
		for _, row := range existingMerged {
			if err := tx.Model(&EmployeeCommissionResetPeriodDailyStat{}).
				Where("id = ?", row.Id).
				Updates(map[string]interface{}{
					"revenue_quota":    row.RevenueQuota,
					"cost_quota":       row.CostQuota,
					"profit_quota":     row.ProfitQuota,
					"commission_quota": row.CommissionQuota,
					"record_count":     row.RecordCount,
					"last_created_at":  row.LastCreatedAt,
				}).Error; err != nil {
				return err
			}
		}
		// 4) 批量删除被合并掉的源行。
		if err := deleteDailyStatsByIDs(tx, deleteIDs); err != nil {
			return err
		}
		return nil
	})
}

// updateResetStartedAtByIDs 分片批量把给定 id 的行改挂到 P 桶（只改 reset_started_at）。
func updateResetStartedAtByIDs(tx *gorm.DB, ids []int, targetPeriodStart int64) error {
	for i := 0; i < len(ids); i += rebucketIDChunk {
		end := i + rebucketIDChunk
		if end > len(ids) {
			end = len(ids)
		}
		if err := tx.Model(&EmployeeCommissionResetPeriodDailyStat{}).
			Where("id IN ?", ids[i:end]).
			Update("reset_started_at", targetPeriodStart).Error; err != nil {
			return err
		}
	}
	return nil
}

// deleteDailyStatsByIDs 分片批量删除给定 id 的日统计行。
func deleteDailyStatsByIDs(tx *gorm.DB, ids []int) error {
	for i := 0; i < len(ids); i += rebucketIDChunk {
		end := i + rebucketIDChunk
		if end > len(ids) {
			end = len(ids)
		}
		if err := tx.Where("id IN ?", ids[i:end]).
			Delete(&EmployeeCommissionResetPeriodDailyStat{}).Error; err != nil {
			return err
		}
	}
	return nil
}
