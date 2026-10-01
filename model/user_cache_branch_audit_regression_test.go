// Regression test (branch audit L16): editing the non-auth cached fields
// (group_ratios, per-user timeouts, retry) keeps auth_version unchanged, so a
// cache fill that read the row before the edit could land after the edit's
// publish and cache the pre-edit exclusive ratios until the hash expired.
// EditWithTx now bumps users.profile_version and writeUserCache rejects any
// snapshot older than the highest profile version already published.

package model

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBranchAuditRegressionDelayedCacheFillRestoresOldGroupRatios(t *testing.T) {
	enableRedis(t)
	created := newTestUser(t, func(u *User) { u.GroupRatios = `{"vip":2}` })
	ctx := context.Background()
	t.Cleanup(func() {
		_ = common.RDB.Del(ctx, getUserCacheKey(created.Id),
			getUserAuthFenceKey(created.Id), getUserAuthVersionKey(created.Id), getUserProfileVersionKey(created.Id)).Err()
	})
	require.NoError(t, invalidateUserCache(created.Id))

	// 1. A relay request misses the cache and reads the row (pre-edit snapshot).
	stale, err := GetUserById(created.Id, false)
	require.NoError(t, err)

	// 2. The admin edit commits and publishes, exactly as controller.UpdateUser does.
	edited, err := GetUserById(created.Id, false)
	require.NoError(t, err)
	edited.GroupRatios = `{"vip":0.5}`
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return edited.EditWithTx(tx, false) }))
	require.NoError(t, PublishUserAuthCache(created.Id))

	// 3. The delayed fill from step 1 lands.
	_ = populateUserCache(*stale)

	// The rejected fill leaves no hash behind; whatever is cached must not be
	// the pre-edit value, and the next read must serve the edited one.
	cached, err := common.RDB.HGet(ctx, getUserCacheKey(created.Id), "GroupRatios").Result()
	if !errors.Is(err, redis.Nil) {
		require.NoError(t, err)
		assert.Equal(t, `{"vip":0.5}`, cached,
			"a fill that read the row before the edit re-published the old exclusive ratios")
	}
	base, err := GetUserCache(created.Id)
	require.NoError(t, err)
	assert.Equal(t, `{"vip":0.5}`, base.GroupRatios)
	cached, err = common.RDB.HGet(ctx, getUserCacheKey(created.Id), "GroupRatios").Result()
	require.NoError(t, err)
	assert.Equal(t, `{"vip":0.5}`, cached)
}

func profileAuditUser(t *testing.T, groupRatios string) *User {
	t.Helper()
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.GroupRatios = groupRatios })
	t.Cleanup(func() {
		_ = common.RDB.Del(context.Background(), getUserCacheKey(u.Id), getUserAuthFenceKey(u.Id),
			getUserAuthVersionKey(u.Id), getUserProfileVersionKey(u.Id)).Err()
	})
	return u
}

func editGroupRatios(t *testing.T, id int, groupRatios string) *User {
	t.Helper()
	edited, err := GetUserById(id, false)
	require.NoError(t, err)
	edited.GroupRatios = groupRatios
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return edited.EditWithTx(tx, false) }))
	return edited
}

// Two publishes racing: the one that read the older row lands last while the
// hash exists. It must not overwrite the newer values.
func TestUserCacheStalePublishDoesNotOverwriteNewerProfile(t *testing.T) {
	u := profileAuditUser(t, `{"vip":2}`)
	require.NoError(t, populateUserCache(*u))
	first := editGroupRatios(t, u.Id, `{"vip":1}`)
	staleSnapshot := *first
	second := editGroupRatios(t, u.Id, `{"vip":0.5}`)
	assert.Greater(t, second.ProfileVersion, first.ProfileVersion)

	require.NoError(t, updateUserCache(*second))
	require.NoError(t, updateUserCache(staleSnapshot), "a superseded snapshot is not an error")

	cached, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, `{"vip":0.5}`, cached.GroupRatios)
}

// EditWithTx bumps profile_version once per edit, an equal or newer snapshot
// still writes, and the published floor expires like the pending fence.
func TestUserCacheProfileFloorAcceptsCurrentSnapshot(t *testing.T) {
	u := profileAuditUser(t, `{"vip":2}`)
	edited := editGroupRatios(t, u.Id, `{"vip":1}`)
	assert.Equal(t, u.ProfileVersion+1, edited.ProfileVersion)
	require.NoError(t, PublishUserAuthCache(u.Id)) // hash absent: sets the floor only

	ctx := context.Background()
	floorKey := getUserProfileVersionKey(u.Id)
	floor, err := common.RDB.Get(ctx, floorKey).Int64()
	require.NoError(t, err)
	assert.Equal(t, edited.ProfileVersion, floor)
	ttl, err := common.RDB.TTL(ctx, floorKey).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl.Seconds(), 0.0, "the floor must expire so a rolled-back version heals")
	assert.LessOrEqual(t, ttl.Seconds(), float64(userAuthFenceTTLSeconds()))

	// A fill carrying the current version is accepted.
	current, err := GetUserById(u.Id, false)
	require.NoError(t, err)
	require.NoError(t, populateUserCache(*current))
	cached, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, `{"vip":1}`, cached.GroupRatios)
}

// Users never edited keep profile_version 0 and create no floor key.
func TestUserCacheUneditedUserCreatesNoProfileFloor(t *testing.T) {
	u := profileAuditUser(t, `{"vip":2}`)
	require.Zero(t, u.ProfileVersion)
	require.NoError(t, populateUserCache(*u))
	exists, err := common.RDB.Exists(context.Background(), getUserProfileVersionKey(u.Id)).Result()
	require.NoError(t, err)
	assert.Zero(t, exists)
}

// UpdateWithTx writes a caller-supplied struct; a stale profile_version in it
// must not roll the column back (that would lock the user out of the cache
// until the floor expires). Its non-zero fields can include cached ones, so it
// bumps the version like EditWithTx.
func TestUpdateWithTxKeepsProfileVersion(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, nil)
	edited := editGroupRatios(t, u.Id, `{"vip":1}`)
	stale := *u // profile_version from before the edit
	stale.DisplayName = "profile-version-keep"
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return stale.UpdateWithTx(tx, false) }))

	var row User
	require.NoError(t, DB.Select("id", "profile_version", "display_name").First(&row, u.Id).Error)
	assert.Equal(t, edited.ProfileVersion+1, row.ProfileVersion)
	assert.Equal(t, row.ProfileVersion, stale.ProfileVersion, "the caller's struct is reloaded")
	assert.Equal(t, "profile-version-keep", row.DisplayName)
}
