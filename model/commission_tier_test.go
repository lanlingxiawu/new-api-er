package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 辅助：单位换算（与 common.QuotaPerUnit = 500000 保持一致）
// ---------------------------------------------------------------------------

const testQuotaPerUnit = 500000.0

// usd2q 将 USD 金额转换为 quota，便于测试中构造利润值。
func usd2q(usd float64) int64 {
	return int64(usd * testQuotaPerUnit)
}

// ---------------------------------------------------------------------------
// 辅助：每个测试前清空相关表，避免数据污染
// ---------------------------------------------------------------------------

// setPeriodStats sets userId's current-period accumulated profit/commission by
// writing a single reset-period daily-stat row (replacing any existing rows for
// the period). TryAutoUpgradeTier and the reset logic read these via
// SUM(profit_quota)/SUM(commission_quota). Replaces the removed AddProfitStats /
// AddCommissionQuota seeders after TryAutoUpgradeTier became DB-driven.
func setPeriodStats(t *testing.T, userId int, profitQuota, commissionQuota int64) {
	t.Helper()
	// Ensure the user is an active employee so the period reset (which selects
	// from employee_profiles WHERE status=1) includes them.
	seedEmployeeProfile(t, userId)
	level, err := GetOrCreateTierLevel(userId, true)
	require.NoError(t, err)
	period := ResolveCommissionMonthlyPeriod(time.Now().Unix()).PeriodStartAt
	resetStartedAt := level.BaselineResetAt
	if resetStartedAt == 0 {
		resetStartedAt = period
	}
	require.NoError(t, DB.Where("employee_user_id = ? AND reset_started_at = ?", userId, resetStartedAt).
		Delete(&EmployeeCommissionResetPeriodDailyStat{}).Error)
	require.NoError(t, DB.Create(&EmployeeCommissionResetPeriodDailyStat{
		EmployeeUserId:  userId,
		ResetStartedAt:  resetStartedAt,
		StatDate:        period,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
	}).Error)
}

func cleanTierTables(t *testing.T) {
	t.Helper()
	if allowTestDBCleanup() {
		DB.Exec("DELETE FROM employee_commission_tiers")
		DB.Exec("DELETE FROM employee_tier_levels")
		DB.Exec("DELETE FROM employee_tier_logs")
		DB.Exec("DELETE FROM employee_commission_reset_period_daily_stats")
		DB.Exec("DELETE FROM employee_profiles")
		DB.Exec("DELETE FROM user_extensions")
		DB.Exec("DELETE FROM channel_cost_configs")
		DB.Exec("DELETE FROM users")
		InvalidateTierCache()
		ResetChannelCostCache()
	}
	t.Cleanup(func() {
		if !allowTestDBCleanup() {
			return
		}
		DB.Exec("DELETE FROM employee_commission_tiers")
		DB.Exec("DELETE FROM employee_tier_levels")
		DB.Exec("DELETE FROM employee_tier_logs")
		DB.Exec("DELETE FROM employee_commission_reset_period_daily_stats")
		DB.Exec("DELETE FROM employee_profiles")
		DB.Exec("DELETE FROM user_extensions")
		DB.Exec("DELETE FROM channel_cost_configs")
		DB.Exec("DELETE FROM users")
		InvalidateTierCache()
		ResetChannelCostCache()
	})
}

func seedUser(t *testing.T, id int) {
	t.Helper()
	u := &User{Id: id, Username: fmt.Sprintf("testuser%d", id), Password: "x", Status: 1, AffCode: fmt.Sprintf("aff%d", id)}
	require.NoError(t, DB.Create(u).Error)
}

// seedEmployeeProfile ensures userId has an active (status=1) EmployeeProfile so
// the period reset — which selects employees from employee_profiles WHERE
// status=1 — includes them. Idempotent via FirstOrCreate.
func seedEmployeeProfile(t *testing.T, userId int) {
	t.Helper()
	var emp EmployeeProfile
	require.NoError(t, DB.Where(EmployeeProfile{UserId: userId}).
		Attrs(EmployeeProfile{Status: 1}).
		FirstOrCreate(&emp).Error)
}

// ---------------------------------------------------------------------------
// 1. 等级 CRUD 与缓存失效
// ---------------------------------------------------------------------------

func TestTierCRUD(t *testing.T) {
	cleanTierTables(t)

	// 创建三个等级（USD 门槛）
	tiers := []*EmployeeCommissionTier{
		{Level: 1, ThresholdUsd: 100.0, Rate: 0.05},
		{Level: 2, ThresholdUsd: 500.0, Rate: 0.08},
		{Level: 3, ThresholdUsd: 1000.0, Rate: 0.12},
	}
	for _, tier := range tiers {
		require.NoError(t, CreateTier(tier))
		assert.NotZero(t, tier.Id)
	}

	// 列表按 level ASC
	all, err := GetAllTiers()
	require.NoError(t, err)
	assert.Len(t, all, 3)
	assert.Equal(t, 1, all[0].Level)
	assert.Equal(t, 3, all[2].Level)

	// 缓存：第二次调用应命中缓存（数量不变）
	cached := GetAllTiersCached()
	assert.Len(t, cached, 3)

	// 更新等级 2
	tiers[1].Rate = 0.09
	tiers[1].ThresholdUsd = 600.0
	require.NoError(t, UpdateTier(tiers[1]))

	// 更新后缓存应失效，重新从 DB 读
	fresh, err := GetAllTiers()
	require.NoError(t, err)
	var found *EmployeeCommissionTier
	for _, f := range fresh {
		if f.Id == tiers[1].Id {
			found = f
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, 0.09, found.Rate)
	assert.Equal(t, 600.0, found.ThresholdUsd)

	// 删除等级 1
	require.NoError(t, DeleteTier(tiers[0].Id))
	after, err := GetAllTiers()
	require.NoError(t, err)
	assert.Len(t, after, 2)

	// 更新不存在的 tier 返回 ErrRecordNotFound
	err = UpdateTier(&EmployeeCommissionTier{Id: 99999, Level: 1, Rate: 0.1})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// 2. GetOrCreateTierLevel 幂等性
// ---------------------------------------------------------------------------

func TestGetOrCreateTierLevel_Idempotent(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 1001)

	lvl1, err := GetOrCreateTierLevel(1001)
	require.NoError(t, err)
	assert.Equal(t, 1001, lvl1.UserId)
	assert.Equal(t, int64(0), lvl1.TierId)

	// 第二次调用返回同一条记录
	lvl2, err := GetOrCreateTierLevel(1001)
	require.NoError(t, err)
	assert.Equal(t, lvl1.Id, lvl2.Id)
}

// ---------------------------------------------------------------------------
// 3. SetTierLevel：设置等级并写日志
// ---------------------------------------------------------------------------

func TestSetTierLevel(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 2001)

	tier := &EmployeeCommissionTier{Level: 3, ThresholdUsd: 1000.0, Rate: 0.12}
	require.NoError(t, CreateTier(tier))

	require.NoError(t, SetTierLevel(2001, tier.Id, "manual", 9, "管理员手动调整", 0))

	lvl, err := GetOrCreateTierLevel(2001)
	require.NoError(t, err)
	assert.Equal(t, tier.Id, lvl.TierId)
	assert.Equal(t, "manual", lvl.Source)
	assert.Equal(t, 9, lvl.UpdatedBy)

	// 日志应有一条记录
	logs, total, err := GetTierLogs(TierLogFilter{UserId: 2001, Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, tier.Id, logs[0].ToTierId)
	assert.Equal(t, int64(0), logs[0].FromTierId)
	assert.Equal(t, "manual", logs[0].Source)

	// 再次设置另一个等级，日志应有两条
	tier2 := &EmployeeCommissionTier{Level: 5, ThresholdUsd: 5000.0, Rate: 0.15}
	require.NoError(t, CreateTier(tier2))
	require.NoError(t, SetTierLevel(2001, tier2.Id, "manual", 9, "", 0))

	logs2, total2, _ := GetTierLogs(TierLogFilter{UserId: 2001, Page: 1, PageSize: 10})
	assert.Equal(t, int64(2), total2)
	assert.Equal(t, tier.Id, logs2[0].FromTierId) // 最新一条在前
	assert.Equal(t, tier2.Id, logs2[0].ToTierId)
}

// ---------------------------------------------------------------------------
// 4. TryAutoUpgradeTier：自动升级核心逻辑
// ---------------------------------------------------------------------------

func TestTryAutoUpgradeTier_NormalUpgrade(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 3001)

	// 三个等级，门槛 $1 / $5 / $10 USD
	// 对应 quota: 500000 / 2500000 / 5000000
	t1 := &EmployeeCommissionTier{Level: 1, ThresholdUsd: 1.0, Rate: 0.05}
	t2 := &EmployeeCommissionTier{Level: 2, ThresholdUsd: 5.0, Rate: 0.08}
	t3 := &EmployeeCommissionTier{Level: 3, ThresholdUsd: 10.0, Rate: 0.12}
	require.NoError(t, CreateTier(t1))
	require.NoError(t, CreateTier(t2))
	require.NoError(t, CreateTier(t3))
	InvalidateTierCache()

	// profit=$0.5 (250000 quota)，未达到任何等级
	setPeriodStats(t, 3001, usd2q(0.5), 0)
	TryAutoUpgradeTier(3001)
	lvl, _ := GetOrCreateTierLevel(3001)
	assert.Equal(t, int64(0), lvl.TierId, "未达门槛，不应升级")

	// profit=$1.0 (500000 quota)，恰好达到等级1
	setPeriodStats(t, 3001, usd2q(1.0), 0)
	TryAutoUpgradeTier(3001)
	lvl, _ = GetOrCreateTierLevel(3001)
	assert.Equal(t, t1.Id, lvl.TierId, "应升为等级1")
	assert.Equal(t, "auto", lvl.Source)

	// profit=$6.0 (3000000 quota)，跨越等级1直接到等级2
	setPeriodStats(t, 3001, usd2q(6.0), 0)
	TryAutoUpgradeTier(3001)
	lvl, _ = GetOrCreateTierLevel(3001)
	assert.Equal(t, t2.Id, lvl.TierId, "应升为等级2")

	// 日志应有两条升级记录
	_, total, _ := GetTierLogs(TierLogFilter{UserId: 3001, Page: 1, PageSize: 10})
	assert.Equal(t, int64(2), total)
}

func TestTryAutoUpgradeTier_NoDowngrade(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 3002)

	tier := &EmployeeCommissionTier{Level: 3, ThresholdUsd: 1.0, Rate: 0.12}
	require.NoError(t, CreateTier(tier))
	InvalidateTierCache()

	// 先升到等级3
	setPeriodStats(t, 3002, usd2q(3.0), 0)
	TryAutoUpgradeTier(3002)
	lvl, _ := GetOrCreateTierLevel(3002)
	assert.Equal(t, tier.Id, lvl.TierId)

	// profit 下降（退款场景），不应降级
	setPeriodStats(t, 3002, usd2q(0.4), 0)
	TryAutoUpgradeTier(3002)
	lvl, _ = GetOrCreateTierLevel(3002)
	assert.Equal(t, tier.Id, lvl.TierId, "不应自动降级")

	// 日志仍只有一条
	_, total, _ := GetTierLogs(TierLogFilter{UserId: 3002, Page: 1, PageSize: 10})
	assert.Equal(t, int64(1), total)
}

func TestTryAutoUpgradeTier_MultiLevelJump(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 3003)

	t1 := &EmployeeCommissionTier{Level: 1, ThresholdUsd: 1.0, Rate: 0.05}
	t2 := &EmployeeCommissionTier{Level: 2, ThresholdUsd: 5.0, Rate: 0.08}
	t3 := &EmployeeCommissionTier{Level: 3, ThresholdUsd: 10.0, Rate: 0.12}
	require.NoError(t, CreateTier(t1))
	require.NoError(t, CreateTier(t2))
	require.NoError(t, CreateTier(t3))
	InvalidateTierCache()

	// 一次性 profit=$20（远超所有门槛），应直接到等级3
	setPeriodStats(t, 3003, usd2q(20.0), 0)
	TryAutoUpgradeTier(3003)
	lvl, _ := GetOrCreateTierLevel(3003)
	assert.Equal(t, t3.Id, lvl.TierId, "应直接升到最高等级3")

	// 日志只有一条（直接跳到最终等级）
	_, total, _ := GetTierLogs(TierLogFilter{UserId: 3003, Page: 1, PageSize: 10})
	assert.Equal(t, int64(1), total)
}

func TestTryAutoUpgradeTier_NoTiersConfigured(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 3004)
	InvalidateTierCache()

	// 无任何等级配置，不应产生任何记录
	setPeriodStats(t, 3004, usd2q(99999.0), 0)
	TryAutoUpgradeTier(3004)
	lvl, _ := GetOrCreateTierLevel(3004)
	assert.Equal(t, int64(0), lvl.TierId)
}

// ---------------------------------------------------------------------------
// 5. GetEffectiveCommissionRate：提成率统一由当前等级决定
// ---------------------------------------------------------------------------

func TestGetEffectiveCommissionRate(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 4001)

	// 未绑定等级时返回 0
	rate := GetEffectiveCommissionRate(4001)
	assert.Equal(t, 0.0, rate, "未绑定等级时应返回 0")

	tier := &EmployeeCommissionTier{Level: 3, ThresholdUsd: 1000.0, Rate: 0.12}
	require.NoError(t, CreateTier(tier))
	require.NoError(t, SetTierLevel(4001, tier.Id, "manual", 0, "", 0))
	InvalidateTierCache()

	// 绑定等级后返回该等级对应的 rate
	rate = GetEffectiveCommissionRate(4001)
	assert.Equal(t, 0.12, rate, "应返回当前等级对应的 rate")

	// 解绑等级后返回 0
	require.NoError(t, SetTierLevel(4001, 0, "manual", 0, "", 0))
	rate = GetEffectiveCommissionRate(4001)
	assert.Equal(t, 0.0, rate, "解绑等级后应返回 0")
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// 7. getTierThresholdUsd：内部辅助函数
// ---------------------------------------------------------------------------

func TestGetTierThresholdUsd(t *testing.T) {
	tiers := []*EmployeeCommissionTier{
		{Id: 1, ThresholdUsd: 100.0},
		{Id: 2, ThresholdUsd: 500.0},
		{Id: 3, ThresholdUsd: 1000.0},
	}
	assert.Equal(t, 500.0, getTierThresholdUsd(tiers, 2))
	assert.Equal(t, -1.0, getTierThresholdUsd(tiers, 99), "不存在的 tier 应返回 -1")
}

// ---------------------------------------------------------------------------
// 8. 并发安全：多 goroutine 同时 TryAutoUpgradeTier
// ---------------------------------------------------------------------------

func TestTryAutoUpgradeTier_Concurrent(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 6001)

	tier := &EmployeeCommissionTier{Level: 1, ThresholdUsd: 1.0, Rate: 0.12}
	require.NoError(t, CreateTier(tier))
	InvalidateTierCache()

	setPeriodStats(t, 6001, usd2q(2.0), 0)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			TryAutoUpgradeTier(6001)
		}()
	}
	wg.Wait()

	lvl, err := GetOrCreateTierLevel(6001)
	require.NoError(t, err)
	assert.Equal(t, tier.Id, lvl.TierId)

	_, total, _ := GetTierLogs(TierLogFilter{UserId: 6001, Page: 1, PageSize: 100})
	assert.GreaterOrEqual(t, total, int64(1))
}

// ---------------------------------------------------------------------------
// 9. GetTierLevelsByUserIds：批量查询
// ---------------------------------------------------------------------------

func TestGetTierLevelsByUserIds(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 7001)
	seedUser(t, 7002)
	seedUser(t, 7003)

	tier := &EmployeeCommissionTier{Level: 2, ThresholdUsd: 500.0, Rate: 0.08}
	require.NoError(t, CreateTier(tier))

	require.NoError(t, SetTierLevel(7001, tier.Id, "manual", 0, "", 0))
	require.NoError(t, SetTierLevel(7002, tier.Id, "manual", 0, "", 0))

	result, err := GetTierLevelsByUserIds([]int{7001, 7002, 7003})
	require.NoError(t, err)
	assert.Len(t, result, 2, "7003 无等级记录，不应在结果中")
	assert.Equal(t, tier.Id, result[7001].TierId)
	assert.Equal(t, tier.Id, result[7002].TierId)

	empty, err := GetTierLevelsByUserIds([]int{})
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// ---------------------------------------------------------------------------
// 10. TryAutoUpgradeTier：基于 BaselineProfitQuota 的"周期利润"判定
// ---------------------------------------------------------------------------

func TestTryAutoUpgradeTier_WithBaseline(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 3005)

	t1 := &EmployeeCommissionTier{Level: 1, ThresholdUsd: 1.0, Rate: 0.05}
	t2 := &EmployeeCommissionTier{Level: 2, ThresholdUsd: 5.0, Rate: 0.08}
	require.NoError(t, CreateTier(t1))
	require.NoError(t, CreateTier(t2))
	InvalidateTierCache()

	// 累计利润 $1，升至等级1
	setPeriodStats(t, 3005, usd2q(1.0), 0)
	TryAutoUpgradeTier(3005)
	lvl, _ := GetOrCreateTierLevel(3005)
	require.Equal(t, t1.Id, lvl.TierId)

	// 模拟一次月度重置：基准设为当前累计利润（$1）
	require.NoError(t, DB.Model(&EmployeeTierLevel{}).Where("user_id = ?", 3005).
		Update("baseline_profit_quota", usd2q(1.0)).Error)

	// 累计利润增至 $1.5（周期利润 $0.5），未达等级2门槛（$5），不应升级
	setPeriodStats(t, 3005, usd2q(1.5), 0)
	TryAutoUpgradeTier(3005)
	lvl, _ = GetOrCreateTierLevel(3005)
	assert.Equal(t, t1.Id, lvl.TierId, "周期利润未达门槛，不应升级")

	// 累计利润增至 $6（周期利润 $5），达到等级2门槛，应升级
	setPeriodStats(t, 3005, usd2q(6.0), 0)
	TryAutoUpgradeTier(3005)
	lvl, _ = GetOrCreateTierLevel(3005)
	assert.Equal(t, t2.Id, lvl.TierId, "周期利润达到门槛，应升级到等级2")
}

// ---------------------------------------------------------------------------
// 11. ResetEmployeeTierLevelsForPeriod：月度等级 + 本期业绩/提成基准重置
// ---------------------------------------------------------------------------

func TestResetEmployeeTierLevelsForPeriod(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 8001)
	seedUser(t, 8002)

	// 同一分组"通用"内的三个等级
	g1 := &EmployeeCommissionTier{Level: 1, Group: "通用", ThresholdUsd: 1.0, Rate: 0.05}
	g2 := &EmployeeCommissionTier{Level: 2, Group: "通用", ThresholdUsd: 5.0, Rate: 0.08}
	g3 := &EmployeeCommissionTier{Level: 3, Group: "通用", ThresholdUsd: 10.0, Rate: 0.12}
	require.NoError(t, CreateTier(g1))
	require.NoError(t, CreateTier(g2))
	require.NoError(t, CreateTier(g3))
	InvalidateTierCache()

	// 8001：升到等级3
	setPeriodStats(t, 8001, usd2q(20.0), 0)
	TryAutoUpgradeTier(8001)
	lvl, _ := GetOrCreateTierLevel(8001)
	require.Equal(t, g3.Id, lvl.TierId)

	// 8001 累计业绩/提成
	setPeriodStats(t, 8001, usd2q(20.0), usd2q(2.0))

	// 8002：未定级，但已有累计业绩，确保懒创建 EmployeeTierLevel 行
	_, err := GetOrCreateTierLevel(8002)
	require.NoError(t, err)
	setPeriodStats(t, 8002, usd2q(0.5), 0)

	// 重置前：当期利润/提成从 daily_stats 动态聚合（baseline_reset_at=0 → 当月周期），
	// 应等于累计业绩，作为重置后清零的对照。
	preStats, err := GetCurrentResetPeriodStatsByEmployeeUserIds([]int{8001, 8002})
	require.NoError(t, err)
	assert.Equal(t, usd2q(20.0), preStats[8001].ProfitQuota, "重置前 8001 当期利润应等于累计业绩")
	assert.Equal(t, usd2q(2.0), preStats[8001].CommissionQuota, "重置前 8001 当期提成应等于累计提成")
	assert.Equal(t, usd2q(0.5), preStats[8002].ProfitQuota, "重置前 8002 当期利润应等于累计业绩")

	resetAt := time.Now().Unix() - 100

	processed, selected, err := ResetEmployeeTierLevelsForPeriod(resetAt, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, processed)
	assert.Equal(t, 2, selected)

	// 8001：重置到本组最低等级（g1）
	lvl1, err := GetOrCreateTierLevel(8001)
	require.NoError(t, err)
	assert.Equal(t, g1.Id, lvl1.TierId, "应重置到本组最低等级")
	assert.Equal(t, "reset", lvl1.Source)
	assert.GreaterOrEqual(t, lvl1.EffectiveAt, resetAt)
	assert.Equal(t, "月度自动重置", lvl1.Remark)
	assert.Equal(t, resetAt, lvl1.BaselineResetAt)

	// baseline_profit_quota / baseline_commission_quota 快照字段已废弃（见
	// commission_tier.go 中 ResetEmployeeTierLevelsForPeriod 注释）：重置不再写入。
	// 当期业绩改由 daily_stats 按 baseline_reset_at 动态聚合——重置把 baseline_reset_at
	// 前移到 resetAt 后，旧周期（reset_started_at=当月起点）业绩被排除，当期利润/提成清零。
	postStats, err := GetCurrentResetPeriodStatsByEmployeeUserIds([]int{8001, 8002})
	require.NoError(t, err)
	assert.Equal(t, int64(0), postStats[8001].ProfitQuota, "重置后 8001 当期利润应清零")
	assert.Equal(t, int64(0), postStats[8001].CommissionQuota, "重置后 8001 当期提成应清零")

	// 8002：tier_id=0 保持不变
	lvl2, err := GetOrCreateTierLevel(8002)
	require.NoError(t, err)
	assert.Equal(t, int64(0), lvl2.TierId, "未定级应保持不变")
	assert.Equal(t, "reset", lvl2.Source)
	assert.Equal(t, resetAt, lvl2.BaselineResetAt)
	assert.Equal(t, int64(0), postStats[8002].ProfitQuota, "重置后 8002 当期利润应清零")

	// 应各写入一条 source=reset 的日志
	var resetLogCount int64
	require.NoError(t, DB.Model(&EmployeeTierLog{}).Where("source = ?", "reset").Count(&resetLogCount).Error)
	assert.Equal(t, int64(2), resetLogCount)

	// 幂等性：baseline_reset_at 已被置为 resetAt，baseline_reset_at < resetAt 不再成立，不应重复处理
	processed2, selected2, err := ResetEmployeeTierLevelsForPeriod(resetAt, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, processed2, "已处理的行不应被重复重置")
	assert.Equal(t, 0, selected2, "已处理的行不应被重复选中")
}

func TestResetEmployeeTierLevelsForPeriodRefreshesOrphanBaselineWithoutBlockingBatch(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 8101)
	seedUser(t, 8102)
	// 重置从 employee_profiles WHERE status=1 选取员工，本用例直接构造 tier_level，
	// 需显式补齐员工档案，否则不会被选中。
	seedEmployeeProfile(t, 8101)
	seedEmployeeProfile(t, 8102)

	tier := &EmployeeCommissionTier{Level: 1, Group: "default", ThresholdUsd: 1.0, Rate: 0.05}
	require.NoError(t, CreateTier(tier))
	InvalidateTierCache()

	require.NoError(t, DB.Create(&EmployeeTierLevel{
		UserId:      8101,
		TierId:      999999,
		Source:      "manual",
		EffectiveAt: 1,
	}).Error)
	require.NoError(t, DB.Create(&EmployeeTierLevel{
		UserId:      8102,
		TierId:      tier.Id,
		Source:      "manual",
		EffectiveAt: 1,
	}).Error)

	resetAt := time.Now().Unix() - 100
	processed, selected, err := ResetEmployeeTierLevelsForPeriod(resetAt, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, 1, selected)

	var orphan EmployeeTierLevel
	require.NoError(t, DB.Where("user_id = ?", 8101).First(&orphan).Error)
	assert.Equal(t, int64(999999), orphan.TierId)
	assert.Equal(t, resetAt, orphan.BaselineResetAt)
	assert.Equal(t, "reset", orphan.Source)

	var valid EmployeeTierLevel
	require.NoError(t, DB.Where("user_id = ?", 8102).First(&valid).Error)
	assert.GreaterOrEqual(t, valid.EffectiveAt, resetAt)
	assert.Equal(t, "reset", valid.Source)
}

func TestResetEmployeeTierLevelsForPeriodUsesBaselineResetAtNotEffectiveAt(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 8201)

	tier := &EmployeeCommissionTier{Level: 1, Group: "default", ThresholdUsd: 1.0, Rate: 0.05}
	require.NoError(t, CreateTier(tier))
	InvalidateTierCache()

	resetAt := time.Now().Unix() - 60
	require.NoError(t, DB.Create(&EmployeeTierLevel{
		UserId:          8201,
		TierId:          tier.Id,
		Source:          "auto",
		EffectiveAt:     resetAt + 30,
		BaselineResetAt: 0,
	}).Error)
	setPeriodStats(t, 8201, usd2q(3.0), 0)

	processed, selected, err := ResetEmployeeTierLevelsForPeriod(resetAt, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, 1, selected)

	var level EmployeeTierLevel
	require.NoError(t, DB.Where("user_id = ?", 8201).First(&level).Error)
	assert.Equal(t, resetAt, level.BaselineResetAt)

	// baseline_profit_quota 快照已废弃；重置把 baseline_reset_at 前移到 resetAt 后，
	// 当期业绩改由 daily_stats 动态聚合，旧周期业绩被排除，当期利润应清零。
	postStats, err := GetCurrentResetPeriodStatsByEmployeeUserIds([]int{8201})
	require.NoError(t, err)
	assert.Equal(t, int64(0), postStats[8201].ProfitQuota, "重置后 8201 当期利润应清零")
}
