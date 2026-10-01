package controller

import (
	"fmt"
	"math"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// userQuotaOverrideManage runs ManageUser add_quota as root and requires success.
func userQuotaOverrideManage(t *testing.T, userID int, mode string, value int) {
	t.Helper()
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/user/manage",
		fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":%q,"value":%d}`, userID, mode, value))
	asRoot(ctx, 1)
	ManageUser(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
}

// userQuotaOverrideRelayView returns the two balances the relay wallet
// pre-consume decides on: the cached user quota TokenAuth hands it, and the
// Redis-first GetUserQuota it re-reads when that looks insufficient.
func userQuotaOverrideRelayView(t *testing.T, userID int) (cached int, live int) {
	t.Helper()
	cache, err := model.GetUserCache(userID)
	require.NoError(t, err)
	live, err = model.GetUserQuota(userID, false)
	require.NoError(t, err)
	return cache.Quota, live
}

func userQuotaOverrideDBQuota(t *testing.T, userID int) int {
	t.Helper()
	var quota int
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Select("quota").Scan(&quota).Error)
	return quota
}

// Test report 2026-09-29 #3 / access-billing D1: an admin override only wrote
// the database, so for up to one cache TTL the relay kept deciding on the old
// cached balance — a lowered balance still let requests through, a raised one
// still refused them. The override must reach the cache immediately.
func TestManageUserQuotaOverrideUpdatesRelayCacheImmediately(t *testing.T) {
	enableRedis(t)
	user := mkUser(t, func(u *model.User) { u.Quota = 5_000_000 })
	t.Cleanup(func() { _ = model.InvalidateUserCache(user.Id) })

	// Warm the cache the way the first relay request does.
	cached, _ := userQuotaOverrideRelayView(t, user.Id)
	require.Equal(t, 5_000_000, cached)

	// Lower to 0: the next pre-consume must see 0 and refuse.
	userQuotaOverrideManage(t, user.Id, "override", 0)
	assert.Equal(t, 0, userQuotaOverrideDBQuota(t, user.Id))
	cached, live := userQuotaOverrideRelayView(t, user.Id)
	assert.Equal(t, 0, cached, "cached quota must drop at once, or the relay keeps spending the old balance")
	assert.Equal(t, 0, live)

	// Raise to 20: allowed at once, not after the cache expires.
	userQuotaOverrideManage(t, user.Id, "override", 20)
	cached, live = userQuotaOverrideRelayView(t, user.Id)
	assert.Equal(t, 20, cached)
	assert.Equal(t, 20, live)

	// Same value again: still a success and nothing drifts.
	userQuotaOverrideManage(t, user.Id, "override", 20)
	cached, _ = userQuotaOverrideRelayView(t, user.Id)
	assert.Equal(t, 20, cached)
	assert.Equal(t, 20, userQuotaOverrideDBQuota(t, user.Id))

	// Negative balances are allowed by override (debt write-off / correction).
	userQuotaOverrideManage(t, user.Id, "override", -7)
	cached, _ = userQuotaOverrideRelayView(t, user.Id)
	assert.Equal(t, -7, cached)
	assert.Equal(t, -7, userQuotaOverrideDBQuota(t, user.Id))
}

// A relay deduction that already reached the cache but is still queued for the
// database (batch update) must survive an override: the cache moves by the
// committed difference, and after the flush the two agree.
func TestOverrideUserQuotaKeepsPendingCachedDeduction(t *testing.T) {
	enableRedis(t)
	user := mkUser(t, func(u *model.User) { u.Quota = 1000 })
	t.Cleanup(func() { _ = model.InvalidateUserCache(user.Id) })
	cached, _ := userQuotaOverrideRelayView(t, user.Id)
	require.Equal(t, 1000, cached)

	// Simulate an unflushed batch deduction of 300: cache already 700, DB 1000.
	require.NoError(t, common.RedisHIncrBy(fmt.Sprintf("user:%d", user.Id), "Quota", -300))
	cached, _ = userQuotaOverrideRelayView(t, user.Id)
	require.Equal(t, 700, cached, "fixture: the cache key format must match the user cache")

	before, err := model.OverrideUserQuota(user.Id, common.RoleRootUser, 5000)
	require.NoError(t, err)
	assert.Equal(t, 1000, before, "returns the committed database balance it replaced")
	assert.Equal(t, 5000, userQuotaOverrideDBQuota(t, user.Id))
	cached, _ = userQuotaOverrideRelayView(t, user.Id)
	assert.Equal(t, 4700, cached, "the pending 300 stays deducted; the flush brings the database to 4700 too")
}

// Without a cached hash there is nothing to update; the override still lands
// in the database and the next cache fill reads it.
func TestOverrideUserQuotaWithoutCachedUser(t *testing.T) {
	enableRedis(t)
	user := mkUser(t, func(u *model.User) { u.Quota = 50 })
	require.NoError(t, model.InvalidateUserCache(user.Id))
	t.Cleanup(func() { _ = model.InvalidateUserCache(user.Id) })

	_, err := model.OverrideUserQuota(user.Id, common.RoleRootUser, 9)
	require.NoError(t, err)
	cached, live := userQuotaOverrideRelayView(t, user.Id)
	assert.Equal(t, 9, cached)
	assert.Equal(t, 9, live)
}

func TestOverrideUserQuotaMissingUser(t *testing.T) {
	requireDB(t)
	_, err := model.OverrideUserQuota(-12345, common.RoleRootUser, 10)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// Review finding: the override value was not bounded (users.quota is a 32-bit
// int column on MySQL/PostgreSQL) and the operator's permission was checked
// outside the row lock. Out-of-range values are refused with a translated
// message; the role check runs against the locked row.
func TestOverrideUserQuotaBoundsAndRole(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(u *model.User) { u.Quota = 10 })
	admin := mkUser(t, func(u *model.User) { u.Quota = 10; u.Role = common.RoleAdminUser })

	for _, v := range []int{math.MaxInt32 + 1, -math.MaxInt32 - 1, math.MaxInt64} {
		_, err := model.OverrideUserQuota(user.Id, common.RoleRootUser, v)
		assert.ErrorIs(t, err, model.ErrUserQuotaOutOfRange, "value %d", v)
	}
	for _, v := range []int{math.MaxInt32, -math.MaxInt32} {
		_, err := model.OverrideUserQuota(user.Id, common.RoleRootUser, v)
		require.NoError(t, err, "value %d", v)
		assert.Equal(t, v, userQuotaOverrideDBQuota(t, user.Id))
	}

	// An admin may not override another admin (same level); root may.
	_, err := model.OverrideUserQuota(admin.Id, common.RoleAdminUser, 99)
	assert.ErrorIs(t, err, model.ErrUserQuotaPermission)
	assert.Equal(t, 10, userQuotaOverrideDBQuota(t, admin.Id), "nothing written")
	_, err = model.OverrideUserQuota(admin.Id, common.RoleRootUser, 99)
	require.NoError(t, err)
	assert.Equal(t, 99, userQuotaOverrideDBQuota(t, admin.Id))
	_, err = model.OverrideUserQuota(user.Id, common.RoleAdminUser, 5)
	require.NoError(t, err, "an admin may override a common user")

	// Through the handler: translated message, balance unchanged.
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/user/manage",
		fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":%d}`, user.Id, int64(math.MaxInt32)+1))
	asRoot(ctx, 1)
	ManageUser(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgQuotaExceedMax), resp.Message)
	assert.Equal(t, 5, userQuotaOverrideDBQuota(t, user.Id))
}

// ManageUser finds soft-deleted users too; overriding one is refused with the
// translated "user does not exist", not a raw database error, and nothing moves.
func TestManageUserQuotaOverrideDeletedUser(t *testing.T) {
	user := mkUser(t, func(u *model.User) { u.Quota = 100 })
	require.NoError(t, model.DB.Delete(&model.User{}, user.Id).Error)

	ctx, rec := newRawCtx(t, http.MethodPost, "/api/user/manage",
		fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":5}`, user.Id))
	asRoot(ctx, 1)
	ManageUser(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgUserNotExists), resp.Message)

	var quota int
	require.NoError(t, model.DB.Unscoped().Model(&model.User{}).Where("id = ?", user.Id).Select("quota").Scan(&quota).Error)
	assert.Equal(t, 100, quota)
}
