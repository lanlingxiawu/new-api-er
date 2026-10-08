package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Branch audit of the fork's user-cache extensions: eight fields were appended
// to the Lua writer as ARGV[13..20] and the schema moved from 2 to 4. Every
// field gets a distinct value so an off-by-one in the ARGV list would land a
// value in the wrong field and fail here.

func auditCacheUser(t *testing.T) *User {
	t.Helper()
	enableRedis(t)
	u := newTestUser(t, func(u *User) {
		u.Quota = 500
		u.GroupRatios = `{"vip-审计":0.25,"q\"uote":3}`
		u.StreamResponseTimeout = 11
		u.StreamResponseTimeoutMode = "idle"
		u.StreamTotalTimeout = 22
		u.NonStreamResponseTimeout = 33
		u.NonStreamTotalTimeout = 44
		u.NonStreamTimeoutBilling = "charge"
		u.RetryTimes = -1
	})
	t.Cleanup(func() {
		_ = common.RDB.Del(context.Background(),
			getUserCacheKey(u.Id), getUserAuthFenceKey(u.Id), getUserAuthVersionKey(u.Id)).Err()
	})
	return u
}

func assertForkFields(t *testing.T, want *User, got *UserBase) {
	t.Helper()
	assert.Equal(t, want.GroupRatios, got.GroupRatios)
	assert.Equal(t, want.StreamResponseTimeout, got.StreamResponseTimeout)
	assert.Equal(t, want.StreamResponseTimeoutMode, got.StreamResponseTimeoutMode)
	assert.Equal(t, want.StreamTotalTimeout, got.StreamTotalTimeout)
	assert.Equal(t, want.NonStreamResponseTimeout, got.NonStreamResponseTimeout)
	assert.Equal(t, want.NonStreamTotalTimeout, got.NonStreamTotalTimeout)
	assert.Equal(t, want.NonStreamTimeoutBilling, got.NonStreamTimeoutBilling)
	assert.Equal(t, want.RetryTimes, got.RetryTimes)
	assert.Equal(t, userCacheSchemaVersion, got.CacheSchema)
}

func TestBranchAuditUserCache_ForkFieldsRoundTripThroughLuaWriter(t *testing.T) {
	u := auditCacheUser(t)
	require.NoError(t, populateUserCache(*u))

	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assertForkFields(t, u, base)
	assert.Equal(t, 500, base.Quota)
	assert.Equal(t, map[string]float64{"vip-审计": 0.25, `q"uote`: 3}, base.GetGroupRatios())

	// A profile refresh (includeQuota=false) rewrites every fork field but must
	// leave the cached quota — which may carry in-flight deductions — alone.
	require.NoError(t, cacheDecrUserQuota(u.Id, 120))
	u.Quota = 999_999
	u.GroupRatios = `{"other":1.5}`
	u.StreamResponseTimeout = 101
	u.StreamResponseTimeoutMode = "first_output"
	u.StreamTotalTimeout = 202
	u.NonStreamResponseTimeout = 303
	u.NonStreamTotalTimeout = 404
	u.NonStreamTimeoutBilling = "refund"
	u.RetryTimes = 7
	require.NoError(t, updateUserCache(*u))

	base, err = cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assertForkFields(t, u, base)
	assert.Equal(t, 380, base.Quota, "updateUserCache must not overwrite the cached quota")
}

// Rolling upgrade from schema 2: an old hash is treated as a miss, rebuilt at
// schema 4 with the new fields, and its cached quota is kept rather than being
// reset to the (possibly lagging) database value.
func TestBranchAuditUserCache_LegacySchemaRebuiltKeepsCachedQuota(t *testing.T) {
	u := auditCacheUser(t)
	require.NoError(t, populateUserCache(*u))
	ctx := context.Background()
	key := getUserCacheKey(u.Id)
	require.NoError(t, common.RDB.HSet(ctx, key, "CacheSchema", 2, "Quota", 321).Err())
	require.NoError(t, common.RDB.HDel(ctx, key, "GroupRatios", "RetryTimes", "NonStreamTimeoutBilling").Err())

	_, err := cacheGetUserBase(u.Id)
	require.Error(t, err, "schema-2 hash must not be served")

	got, err := GetUserCache(u.Id)
	require.NoError(t, err)
	assertForkFields(t, u, got)

	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assertForkFields(t, u, base)
	assert.Equal(t, 321, base.Quota)
	ttl, err := common.RDB.TTL(ctx, key).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0), "rebuilt hash must keep an expiry")
}

func brokenUserCacheRedis(t *testing.T) {
	t.Helper()
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	broken := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
		ReadTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
	common.RDB = broken
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = prevRDB, prevEnabled
		_ = broken.Close()
	})
}

// Redis outage: every reader on the auth hot path must degrade to the database
// instead of failing the request, and write helpers must report (not panic).
func TestBranchAuditUserCache_RedisUnavailableFallsBackToDatabase(t *testing.T) {
	requireDB(t)
	u := newTestUser(t, func(u *User) {
		u.Quota = 4321
		u.GroupRatios = `{"audit-outage":0.5}`
		u.RetryTimes = 3
		u.NonStreamTimeoutBilling = "charge"
	})
	brokenUserCacheRedis(t)

	_, err := cacheGetUserBase(u.Id)
	require.Error(t, err)

	got, err := GetUserCache(u.Id)
	require.NoError(t, err, "a Redis outage must not fail authentication")
	assert.Equal(t, u.Id, got.Id)
	assert.Equal(t, 4321, got.Quota)
	assert.Equal(t, 3, got.RetryTimes)
	assert.Equal(t, "charge", got.NonStreamTimeoutBilling)
	assert.Equal(t, map[string]float64{"audit-outage": 0.5}, GetUserGroupRatios(u.Id))

	assert.Error(t, cacheIncrUserQuota(u.Id, 1))
	assert.True(t, advanceProfileFloorUnlessCached(u.Id, 1), "on a Redis error bulk refreshers must attempt the refresh")
	assert.Error(t, updateUserCache(*u))
}

// A pending fence published before a restrictive update must also block the
// fork's extra fields: a stale snapshot cannot re-publish old group ratios.
func TestBranchAuditUserCache_PendingFenceRejectsStaleGroupRatioSnapshot(t *testing.T) {
	u := auditCacheUser(t)
	require.NoError(t, populateUserCache(*u))
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)

	require.NoError(t, SetUserAuthVersionFence(u.Id, base.AuthVersion+1))
	stale := *u
	stale.GroupRatios = `{"stale":0}`
	assert.ErrorIs(t, updateUserCache(stale), ErrUserAuthCachePending)

	raw, err := common.RDB.HGet(context.Background(), getUserCacheKey(u.Id), "GroupRatios").Result()
	require.NoError(t, err)
	assert.Equal(t, u.GroupRatios, raw)
}
