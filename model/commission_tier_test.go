package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ============================================================================
// Shared fixtures for the employee-commission accounting test batch
// (commission_tier / commission_period_switch / employee / employee_performance
//  / employee_commission_export). Defined ONCE here; reused across files.
// All money values are quota (int64). USD = quota / common.QuotaPerUnit
// (default 500000). usdToQuota inverts common.QuotaToUSD so tier boundaries can
// be pinned exactly.
// ============================================================================

// usdToQuota converts a USD figure to the integer quota that QuotaToUSD maps
// back to it exactly (given the current common.QuotaPerUnit).
func usdToQuota(usd float64) int64 { return int64(usd * common.QuotaPerUnit) }

// deleteByID64 hard-deletes a row with an int64 primary key on cleanup.
func deleteByID64(t *testing.T, model interface{}, id int64) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Delete(model, id)
		}
	})
}

// ctMkTier creates a global tier via CreateTier (invalidating the tier cache),
// registers hard-delete + cache-invalidate cleanup, and returns it. Group names
// must be unique per test so the (level, tier_group) unique index never collides
// with real data or sibling tests.
func ctMkTier(t *testing.T, group string, level int, threshold, rate float64) *EmployeeCommissionTier {
	t.Helper()
	requireDB(t)
	tier := &EmployeeCommissionTier{Level: level, Group: group, ThresholdUsd: threshold, Rate: rate}
	require.NoError(t, CreateTier(tier))
	deleteByID64(t, &EmployeeCommissionTier{}, tier.Id)
	t.Cleanup(func() { InvalidateTierCache() })
	return tier
}

// ctMkTierLevel inserts an EmployeeTierLevel row directly (bypassing lazy
// creation) so the test controls tier_id + baseline_reset_at exactly. It clears
// the in-process mem cache so the next read reflects the freshly written row.
func ctMkTierLevel(t *testing.T, userId int, tierId, baselineResetAt int64) *EmployeeTierLevel {
	t.Helper()
	requireDB(t)
	lvl := &EmployeeTierLevel{
		UserId:          userId,
		TierId:          tierId,
		Source:          "manual",
		EffectiveAt:     time.Now().Unix(),
		BaselineResetAt: baselineResetAt,
	}
	require.NoError(t, DB.Create(lvl).Error)
	deleteByID64(t, &EmployeeTierLevel{}, lvl.Id)
	deleteTierLevelFromMem(userId)
	t.Cleanup(func() { deleteTierLevelFromMem(userId) })
	return lvl
}

// ctMkEmployeeProfile inserts a raw EmployeeProfile (does NOT mutate the user
// row like CreateEmployee does), registers cleanup + cache invalidation.
func ctMkEmployeeProfile(t *testing.T, userId, status int) *EmployeeProfile {
	t.Helper()
	requireDB(t)
	emp := &EmployeeProfile{UserId: userId, Status: status}
	require.NoError(t, DB.Create(emp).Error)
	deleteByID(t, &EmployeeProfile{}, emp.Id)
	InvalidateEmployeeCache(userId)
	t.Cleanup(func() { InvalidateEmployeeCache(userId) })
	return emp
}

// ctMkResetStat inserts a reset-period daily-stat row for a bucket.
func ctMkResetStat(t *testing.T, resetStartedAt, statDate int64, userId int, rev, cost, profit, comm, cnt int64) *EmployeeCommissionResetPeriodDailyStat {
	t.Helper()
	requireDB(t)
	row := &EmployeeCommissionResetPeriodDailyStat{
		ResetStartedAt:  resetStartedAt,
		StatDate:        statDate,
		EmployeeUserId:  userId,
		RevenueQuota:    rev,
		CostQuota:       cost,
		ProfitQuota:     profit,
		CommissionQuota: comm,
		RecordCount:     cnt,
	}
	require.NoError(t, DB.Create(row).Error)
	deleteByID(t, &EmployeeCommissionResetPeriodDailyStat{}, row.Id)
	return row
}

// empCleanupStats removes every stat/log/tier-log row keyed to a test user id.
func empCleanupStats(t *testing.T, userId int) {
	t.Helper()
	t.Cleanup(func() {
		if DB == nil {
			return
		}
		DB.Where("employee_user_id = ?", userId).Delete(&EmployeeCommissionResetPeriodDailyStat{})
		DB.Where("employee_user_id = ?", userId).Delete(&EmployeeCommissionDailyStat{})
		DB.Where("employee_user_id = ?", userId).Delete(&EmployeeCommissionLog{})
		DB.Where("user_id = ?", userId).Delete(&EmployeeTierLog{})
		DB.Where("user_id = ?", userId).Delete(&EmployeeTierLevel{})
	})
}

// reloadTierLevel reads the current DB tier level for a user, bypassing caches.
func reloadTierLevel(t *testing.T, userId int) *EmployeeTierLevel {
	t.Helper()
	var lvl EmployeeTierLevel
	require.NoError(t, DB.Where("user_id = ?", userId).First(&lvl).Error)
	return &lvl
}

// ---------------------------------------------------------------------------
// Pure helper: getTierThresholdUsd
// ---------------------------------------------------------------------------

func TestGetTierThresholdUsd(t *testing.T) {
	tiers := []*EmployeeCommissionTier{
		{Id: 11, ThresholdUsd: 5},
		{Id: 22, ThresholdUsd: 10},
	}
	assert.Equal(t, 5.0, getTierThresholdUsd(tiers, 11))
	assert.Equal(t, 10.0, getTierThresholdUsd(tiers, 22))
	// not found -> -1 sentinel
	assert.Equal(t, -1.0, getTierThresholdUsd(tiers, 999))
	assert.Equal(t, -1.0, getTierThresholdUsd(nil, 11))
}

// ---------------------------------------------------------------------------
// Tier CRUD + cache
// ---------------------------------------------------------------------------

func TestTierCRUDAndCache(t *testing.T) {
	requireDB(t)
	grp := uniq("tierg")

	t1 := ctMkTier(t, grp, 1, 0, 0.05)
	t2 := ctMkTier(t, grp, 2, 10, 0.10)
	require.NotZero(t, t1.Id)
	require.NotZero(t, t2.Id)

	// TierExists
	ok, err := TierExists(t1.Id)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = TierExists(t1.Id + 8_000_000_000)
	require.NoError(t, err)
	assert.False(t, ok)

	// GetAllTiers ordered by tier_group ASC, level ASC — my group is a subset.
	all, err := GetAllTiers()
	require.NoError(t, err)
	var mine []*EmployeeCommissionTier
	for _, tt := range all {
		if tt.Group == grp {
			mine = append(mine, tt)
		}
	}
	require.Len(t, mine, 2)
	assert.Equal(t, 1, mine[0].Level)
	assert.Equal(t, 2, mine[1].Level)

	// GetAllTiersCached returns the same rows (contains my group).
	cached := GetAllTiersCached()
	var cachedMine int
	for _, tt := range cached {
		if tt.Group == grp {
			cachedMine++
		}
	}
	assert.Equal(t, 2, cachedMine)

	// UpdateTier changes rate + threshold.
	t2.Rate = 0.20
	t2.ThresholdUsd = 25
	require.NoError(t, UpdateTier(t2))
	reloaded := loadTiersFromDB()
	for _, tt := range reloaded {
		if tt.Id == t2.Id {
			assert.Equal(t, 0.20, tt.Rate)
			assert.Equal(t, 25.0, tt.ThresholdUsd)
		}
	}

	// UpdateTier on a missing id -> ErrRecordNotFound.
	err = UpdateTier(&EmployeeCommissionTier{Id: t2.Id + 9_000_000_000, Level: 9, Group: grp})
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// DeleteTier when referenced by a tier level -> refuse with descriptive error.
	u := mkUser(t, nil)
	ctMkTierLevel(t, u.Id, t1.Id, 0)
	err = DeleteTier(t1.Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "名员工使用")

	// Deleting an unreferenced tier succeeds.
	t3 := ctMkTier(t, grp, 3, 40, 0.30)
	require.NoError(t, DeleteTier(t3.Id))
	ok, _ = TierExists(t3.Id)
	assert.False(t, ok)
}

func TestGetTiersPagination(t *testing.T) {
	requireDB(t)
	grp := uniq("pg")
	for i := 1; i <= 5; i++ {
		ctMkTier(t, grp, i, float64(i*10), 0.1)
	}
	// page<1 & pageSize<1 are normalised (page=1, pageSize=20).
	rows, total, err := GetTiers(0, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(5))
	assert.NotEmpty(t, rows)

	// explicit small page size
	rows, _, err = GetTiers(1, 2)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

// ---------------------------------------------------------------------------
// EmployeeTierLevel read/write
// ---------------------------------------------------------------------------

func TestGetOrCreateTierLevel_LazyCreateAndCache(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)

	// First call lazily creates a tier_id=0 row.
	lvl, err := GetOrCreateTierLevel(u.Id, false)
	require.NoError(t, err)
	assert.EqualValues(t, 0, lvl.TierId)
	require.NotZero(t, lvl.Id)

	// Second call (readCache=true) returns the same row; mem cache warmed.
	lvl2, err := GetOrCreateTierLevel(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, lvl.Id, lvl2.Id)
	// mem cache now holds the entry.
	assert.NotNil(t, getTierLevelFromMem(u.Id))
	deleteTierLevelFromMem(u.Id)
	assert.Nil(t, getTierLevelFromMem(u.Id))
}

func TestSetTierLevel(t *testing.T) {
	requireDB(t)
	grp := uniq("setlv")
	tier := ctMkTier(t, grp, 1, 0, 0.07)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)

	// invalid (non-existent) tier id -> ErrRecordNotFound, nothing written.
	err := SetTierLevel(u.Id, tier.Id+7_000_000_000, "manual", 42, "x", 0)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// valid: creates level + writes a tier log.
	require.NoError(t, SetTierLevel(u.Id, tier.Id, "manual", 42, "promo", 3.5))
	lvl := reloadTierLevel(t, u.Id)
	assert.Equal(t, tier.Id, lvl.TierId)
	assert.Equal(t, "manual", lvl.Source)
	assert.Equal(t, 42, lvl.UpdatedBy)
	assert.Equal(t, "promo", lvl.Remark)

	logs, total, err := GetTierLogs(TierLogFilter{UserId: u.Id, Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, logs, 1)
	assert.EqualValues(t, 0, logs[0].FromTierId)
	assert.Equal(t, tier.Id, logs[0].ToTierId)
	assert.Equal(t, 3.5, logs[0].ProfitSnapshotUsd)

	// tier_id=0 is allowed without existence check (unbind).
	require.NoError(t, SetTierLevel(u.Id, 0, "manual", 1, "unbind", 0))
	lvl = reloadTierLevel(t, u.Id)
	assert.EqualValues(t, 0, lvl.TierId)
}

func TestGetEffectiveCommissionRate(t *testing.T) {
	requireDB(t)
	grp := uniq("rate")
	tier := ctMkTier(t, grp, 1, 0, 0.123)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)

	// unbound (tier_id=0) -> rate 0.
	ctMkTierLevel(t, u.Id, 0, 0)
	assert.Equal(t, 0.0, GetEffectiveCommissionRate(u.Id))

	// bound -> tier rate.
	require.NoError(t, DB.Model(&EmployeeTierLevel{}).Where("user_id = ?", u.Id).Update("tier_id", tier.Id).Error)
	deleteTierLevelFromMem(u.Id)
	assert.Equal(t, 0.123, GetEffectiveCommissionRate(u.Id))
}

func TestGetTierLevelByUserIdAndBatch(t *testing.T) {
	requireDB(t)
	grp := uniq("byid")
	tier := ctMkTier(t, grp, 1, 0, 0.09)
	u1 := mkUser(t, nil)
	u2 := mkUser(t, nil)
	empCleanupStats(t, u1.Id)
	empCleanupStats(t, u2.Id)
	ctMkTierLevel(t, u1.Id, tier.Id, 0)
	ctMkTierLevel(t, u2.Id, 0, 0)

	lvl, tr := GetTierLevelByUserId(u1.Id)
	require.NotNil(t, lvl)
	require.NotNil(t, tr)
	assert.Equal(t, tier.Id, tr.Id)

	// u2 bound to tier 0 -> nil tier.
	lvl2, tr2 := GetTierLevelByUserId(u2.Id)
	require.NotNil(t, lvl2)
	assert.Nil(t, tr2)

	// GetTierLevelsByUserIds batch.
	m, err := GetTierLevelsByUserIds([]int{u1.Id, u2.Id})
	require.NoError(t, err)
	assert.Equal(t, tier.Id, m[u1.Id].TierId)
	assert.EqualValues(t, 0, m[u2.Id].TierId)
	// empty input short-circuits.
	empt, err := GetTierLevelsByUserIds(nil)
	require.NoError(t, err)
	assert.Empty(t, empt)

	// GetTierLevelsByUserIdsCached: mem-hit for u1 (warm it), DB miss for u2.
	deleteTierLevelFromMem(u1.Id)
	deleteTierLevelFromMem(u2.Id)
	setTierLevelToMem(m[u1.Id])
	cached := GetTierLevelsByUserIdsCached([]int{u1.Id, u2.Id, -1, u1.Id})
	require.Contains(t, cached, u1.Id)
	require.Contains(t, cached, u2.Id)
	assert.Equal(t, tier.Id, cached[u1.Id].TierId)
}

func TestEnsureTierLevelsForUserIds(t *testing.T) {
	requireDB(t)
	u1 := mkUser(t, nil)
	u2 := mkUser(t, nil)
	empCleanupStats(t, u1.Id)
	empCleanupStats(t, u2.Id)

	// includes duplicates and an invalid id; idempotent (ON CONFLICT DO NOTHING).
	require.NoError(t, ensureTierLevelsForUserIds([]int{u1.Id, u1.Id, u2.Id, 0, -5}))
	require.NoError(t, ensureTierLevelsForUserIds([]int{u1.Id, u2.Id}))

	m, err := GetTierLevelsByUserIds([]int{u1.Id, u2.Id})
	require.NoError(t, err)
	assert.Len(t, m, 2)

	// empty input -> no-op.
	require.NoError(t, ensureTierLevelsForUserIds(nil))
}

func TestGetTierLogsFilter(t *testing.T) {
	requireDB(t)
	grp := uniq("logf")
	tier := ctMkTier(t, grp, 1, 0, 0.05)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)

	require.NoError(t, SetTierLevel(u.Id, tier.Id, "manual", 1, "a", 0))
	require.NoError(t, SetTierLevel(u.Id, 0, "manual", 1, "b", 0))

	logs, total, err := GetTierLogs(TierLogFilter{UserId: u.Id, Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, logs, 2)
	// ordered operated_at DESC, id DESC -> newest first.
	assert.GreaterOrEqual(t, logs[0].Id, logs[1].Id)
}

// ---------------------------------------------------------------------------
// Auto-upgrade tier: boundary analysis on rate thresholds (only-up).
// ---------------------------------------------------------------------------

func TestTryAutoUpgradeTier_Boundaries(t *testing.T) {
	requireDB(t)
	grp := uniq("auto")
	l1 := ctMkTier(t, grp, 1, 0, 0.05)  // >= $0
	l2 := ctMkTier(t, grp, 2, 10, 0.10) // >= $10
	l3 := ctMkTier(t, grp, 3, 20, 0.15) // >= $20

	// R is the bucket the employee's period profit lives in.
	const R = int64(1_600_000_000)

	run := func(name string, profitQuota int64, wantTierId int64) {
		t.Run(name, func(t *testing.T) {
			u := mkUser(t, nil)
			ctMkEmployeeProfile(t, u.Id, 1)
			empCleanupStats(t, u.Id)
			// start bound to L1 with baseline anchored at R
			ctMkTierLevel(t, u.Id, l1.Id, R)
			if profitQuota != 0 {
				ctMkResetStat(t, R, R, u.Id, profitQuota, 0, profitQuota, 0, 1)
			}
			TryAutoUpgradeTier(u.Id)
			assert.Equal(t, wantTierId, reloadTierLevel(t, u.Id).TierId)
		})
	}

	// just below $10 -> stays L1
	run("below_L2_boundary", usdToQuota(10)-1, l1.Id)
	// exactly $10 -> reaches L2 (>= threshold)
	run("at_L2_boundary", usdToQuota(10), l2.Id)
	// just below $20 -> L2
	run("below_L3_boundary", usdToQuota(20)-1, l2.Id)
	// exactly $20 -> jumps straight to L3 (highest reachable)
	run("at_L3_boundary", usdToQuota(20), l3.Id)
	// zero profit -> stays L1
	run("zero_profit", 0, l1.Id)
}

func TestTryAutoUpgradeTier_OnlyUpNotDown(t *testing.T) {
	requireDB(t)
	grp := uniq("noup")
	_ = ctMkTier(t, grp, 1, 0, 0.05)
	_ = ctMkTier(t, grp, 2, 10, 0.10)
	l3 := ctMkTier(t, grp, 3, 20, 0.15)

	const R = int64(1_600_000_100)
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	// employee already at L3 but only $5 of period profit -> must NOT downgrade.
	ctMkTierLevel(t, u.Id, l3.Id, R)
	ctMkResetStat(t, R, R, u.Id, usdToQuota(5), 0, usdToQuota(5), 0, 1)

	TryAutoUpgradeTier(u.Id)
	assert.Equal(t, l3.Id, reloadTierLevel(t, u.Id).TierId, "auto upgrade must never downgrade")
}

func TestTryAutoUpgradeTier_OrphanedTierSkipped(t *testing.T) {
	requireDB(t)
	grp := uniq("orph")
	_ = ctMkTier(t, grp, 1, 0, 0.05)

	const R = int64(1_600_000_200)
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	// tier_id points to a tier that does not exist -> upgrade is skipped.
	orphanTierId := int64(8_500_000_001)
	ctMkTierLevel(t, u.Id, orphanTierId, R)
	ctMkResetStat(t, R, R, u.Id, usdToQuota(999), 0, usdToQuota(999), 0, 1)

	TryAutoUpgradeTier(u.Id)
	assert.Equal(t, orphanTierId, reloadTierLevel(t, u.Id).TierId, "orphaned tier must not silently switch group")
}

func TestTryAutoUpgradeTierBatch(t *testing.T) {
	requireDB(t)
	grp := uniq("batch")
	l1 := ctMkTier(t, grp, 1, 0, 0.05)
	l2 := ctMkTier(t, grp, 2, 10, 0.10)

	const R = int64(1_600_000_300)
	u1 := mkUser(t, nil)
	u2 := mkUser(t, nil)
	ctMkEmployeeProfile(t, u1.Id, 1)
	ctMkEmployeeProfile(t, u2.Id, 1)
	empCleanupStats(t, u1.Id)
	empCleanupStats(t, u2.Id)
	ctMkTierLevel(t, u1.Id, l1.Id, R)
	ctMkTierLevel(t, u2.Id, l1.Id, R)
	// u1 reaches L2, u2 stays L1.
	ctMkResetStat(t, R, R, u1.Id, usdToQuota(15), 0, usdToQuota(15), 0, 1)
	ctMkResetStat(t, R, R, u2.Id, usdToQuota(3), 0, usdToQuota(3), 0, 1)

	tryAutoUpgradeTierBatch([]int{u1.Id, u2.Id})
	assert.Equal(t, l2.Id, reloadTierLevel(t, u1.Id).TierId)
	assert.Equal(t, l1.Id, reloadTierLevel(t, u2.Id).TierId)

	// empty input -> no-op (no panic).
	tryAutoUpgradeTierBatch(nil)
}

// ---------------------------------------------------------------------------
// Monthly period reset
// ---------------------------------------------------------------------------

func TestResetEmployeeTierLevelsForPeriod(t *testing.T) {
	requireDB(t)
	grp := uniq("reset")
	l1 := ctMkTier(t, grp, 1, 0, 0.05) // group min
	l2 := ctMkTier(t, grp, 2, 10, 0.10)

	// small resetAt to minimise blast radius: only rows with
	// baseline_reset_at < resetAt are touched. This function is inherently
	// global (scans all status=1 employees); assertions are scoped to my rows.
	const resetAt = int64(300_000)
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	// employee currently at L2 with an older baseline -> should reset to L1 (min).
	ctMkTierLevel(t, u.Id, l2.Id, resetAt-1)

	// an employee already at resetAt must NOT be selected (idempotency guard).
	uGuard := mkUser(t, nil)
	ctMkEmployeeProfile(t, uGuard.Id, 1)
	empCleanupStats(t, uGuard.Id)
	ctMkTierLevel(t, uGuard.Id, l2.Id, resetAt)

	processed, _, err := ResetEmployeeTierLevelsForPeriod(resetAt, 300, 77)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, processed, 1)

	// my employee reset to group-min tier + baseline advanced.
	lvl := reloadTierLevel(t, u.Id)
	assert.Equal(t, l1.Id, lvl.TierId, "reset must drop to group minimum tier")
	assert.EqualValues(t, resetAt, lvl.BaselineResetAt)
	assert.Equal(t, "reset", lvl.Source)
	assert.Equal(t, 77, lvl.UpdatedBy)

	// guard employee untouched.
	guard := reloadTierLevel(t, uGuard.Id)
	assert.Equal(t, l2.Id, guard.TierId)
	assert.EqualValues(t, resetAt, guard.BaselineResetAt)

	// a reset tier log was written for my employee.
	var logCount int64
	require.NoError(t, DB.Model(&EmployeeTierLog{}).
		Where("user_id = ? AND source = ?", u.Id, "reset").Count(&logCount).Error)
	assert.EqualValues(t, 1, logCount)

	// second call is idempotent for my employee (baseline no longer < resetAt).
	before := reloadTierLevel(t, u.Id).BaselineResetAt
	_, _, err = ResetEmployeeTierLevelsForPeriod(resetAt, 300, 77)
	require.NoError(t, err)
	assert.EqualValues(t, before, reloadTierLevel(t, u.Id).BaselineResetAt)
}

// ---------------------------------------------------------------------------
// Redis-backed tier-level cache (skips when Redis unreachable)
// ---------------------------------------------------------------------------

func TestTierLevel_RedisCache(t *testing.T) {
	enableRedis(t)
	requireDB(t)
	grp := uniq("redis")
	tier := ctMkTier(t, grp, 1, 0, 0.11)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	t.Cleanup(func() { deleteTierLevelFromRedis(u.Id) })

	lvl := ctMkTierLevel(t, u.Id, tier.Id, 0)

	// key + direct set/get round-trip through Redis.
	assert.Contains(t, tierLevelRedisKey(u.Id), "employee_tier_level:")
	setTierLevelToRedis(lvl)
	got := getTierLevelFromRedis(u.Id)
	require.NotNil(t, got)
	assert.Equal(t, tier.Id, got.TierId)

	// GetOrCreateTierLevel(readCache) hits Redis after the mem cache is cleared.
	deleteTierLevelFromMem(u.Id)
	fromCache, err := GetOrCreateTierLevel(u.Id, true)
	require.NoError(t, err)
	assert.Equal(t, tier.Id, fromCache.TierId)

	// NX write only fills an empty key; delete then refresh from DB repopulates.
	deleteTierLevelFromRedis(u.Id)
	setTierLevelToRedisNX(lvl)
	assert.NotNil(t, getTierLevelFromRedis(u.Id))
	deleteTierLevelFromRedis(u.Id)
	deleteTierLevelFromMem(u.Id)
	refreshTierLevelCacheFromDB(u.Id)
	assert.NotNil(t, getTierLevelFromRedis(u.Id))

	// effective rate via context path resolves the bound tier's rate.
	deleteTierLevelFromMem(u.Id)
	rate, err := GetEffectiveCommissionRateWithContext(nil, u.Id)
	require.NoError(t, err)
	assert.Equal(t, 0.11, rate)

	// refresh for a user with no DB row evicts stale cache entries.
	orphanUser := mkUser(t, nil)
	setTierLevelToRedis(&EmployeeTierLevel{UserId: orphanUser.Id, TierId: 5})
	refreshTierLevelCacheFromDBByUserIds([]int{orphanUser.Id})
	assert.Nil(t, getTierLevelFromRedis(orphanUser.Id), "missing DB row -> cache evicted")
}

func TestResetEmployeeTierLevelsForPeriod_DefaultBatchSize(t *testing.T) {
	requireDB(t)
	// batchSize<=0 normalises to 300; with no matching rows below a tiny resetAt
	// this exercises the default path and returns without error.
	_, _, err := ResetEmployeeTierLevelsForPeriod(1, 0, 0)
	require.NoError(t, err)
}
