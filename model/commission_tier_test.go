package model

import (
	"sync"
	"testing"

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

func cleanTierTables(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		DB.Exec("DELETE FROM employee_commission_tiers")
		DB.Exec("DELETE FROM employee_tier_levels")
		DB.Exec("DELETE FROM employee_tier_logs")
		DB.Exec("DELETE FROM user_extensions")
		DB.Exec("DELETE FROM channel_cost_configs")
		DB.Exec("DELETE FROM users")
		InvalidateTierCache()
		ResetChannelCostCache()
	})
}

func seedUser(t *testing.T, id int) {
	t.Helper()
	u := &User{Id: id, Username: "testuser", Password: "x", Status: 1}
	require.NoError(t, DB.Create(u).Error)
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
	TryAutoUpgradeTier(3001, usd2q(0.5))
	lvl, _ := GetOrCreateTierLevel(3001)
	assert.Equal(t, int64(0), lvl.TierId, "未达门槛，不应升级")

	// profit=$1.0 (500000 quota)，恰好达到等级1
	TryAutoUpgradeTier(3001, usd2q(1.0))
	lvl, _ = GetOrCreateTierLevel(3001)
	assert.Equal(t, t1.Id, lvl.TierId, "应升为等级1")
	assert.Equal(t, "auto", lvl.Source)

	// profit=$6.0 (3000000 quota)，跨越等级1直接到等级2
	TryAutoUpgradeTier(3001, usd2q(6.0))
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
	TryAutoUpgradeTier(3002, usd2q(3.0))
	lvl, _ := GetOrCreateTierLevel(3002)
	assert.Equal(t, tier.Id, lvl.TierId)

	// profit 下降（退款场景），不应降级
	TryAutoUpgradeTier(3002, usd2q(0.4))
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
	TryAutoUpgradeTier(3003, usd2q(20.0))
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
	TryAutoUpgradeTier(3004, usd2q(99999.0))
	lvl, _ := GetOrCreateTierLevel(3004)
	assert.Equal(t, int64(0), lvl.TierId)
}

// ---------------------------------------------------------------------------
// 5. GetEffectiveCommissionRate：员工自定义率优先，未设置时回退等级 rate
// ---------------------------------------------------------------------------

func TestGetEffectiveCommissionRate(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 4001)

	tier := &EmployeeCommissionTier{Level: 3, ThresholdUsd: 1000.0, Rate: 0.12}
	require.NoError(t, CreateTier(tier))
	require.NoError(t, SetTierLevel(4001, tier.Id, "manual", 0, "", 0))
	InvalidateTierCache()

	// 员工有自定义比例 0.08，即使有等级配置（0.12）也应优先使用 0.08
	rate := GetEffectiveCommissionRate(4001, 0.08)
	assert.Equal(t, 0.08, rate, "员工自定义 rate 应优先于等级 rate")

	// 员工自定义比例更高（0.15），同样优先
	rate = GetEffectiveCommissionRate(4001, 0.15)
	assert.Equal(t, 0.15, rate, "员工自定义 rate（更高）应优先于等级 rate")

	// 员工未设置自定义比例（fallbackRate=0）时，使用等级 rate
	rate = GetEffectiveCommissionRate(4001, 0)
	assert.Equal(t, 0.12, rate, "fallbackRate=0 时应回退到等级 rate")

	// 等级被删除后，fallbackRate=0 返回 0
	require.NoError(t, DeleteTier(tier.Id))
	InvalidateTierCache()

	rate = GetEffectiveCommissionRate(4001, 0)
	assert.Equal(t, 0.0, rate, "等级已删除且无自定义比例时返回 0")

	// 重新验证：有自定义比例时永远不依赖等级
	rate = GetEffectiveCommissionRate(4001, 0.05)
	assert.Equal(t, 0.05, rate, "有自定义比例时不受等级删除影响")
}

// ---------------------------------------------------------------------------
// 6. AddProfitStats：返回值正确
// ---------------------------------------------------------------------------

func TestAddProfitStats_ReturnValue(t *testing.T) {
	cleanTierTables(t)
	seedUser(t, 5001)
	require.NoError(t, EnsureUserExtension(5001))

	total, err := AddProfitStats(5001, 300, false)
	require.NoError(t, err)
	assert.Equal(t, int64(300), total)

	total, err = AddProfitStats(5001, 200, false)
	require.NoError(t, err)
	assert.Equal(t, int64(500), total)

	total, err = AddProfitStats(5001, -100, false)
	require.NoError(t, err)
	assert.Equal(t, int64(400), total)
}

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

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			TryAutoUpgradeTier(6001, usd2q(2.0))
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
