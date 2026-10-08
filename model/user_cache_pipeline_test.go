package model

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cacheGetUserBase 每个 relay 请求至少走一次（TokenAuth），读哈希与读版本下限
// 必须在一次往返内完成，且"先哈希、后下限、下限更高即拒绝"的判定不变。

type userCacheRoundTrips struct{ trips atomic.Int64 }

func (c *userCacheRoundTrips) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	c.trips.Add(1)
	return ctx, nil
}
func (c *userCacheRoundTrips) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (c *userCacheRoundTrips) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	c.trips.Add(1)
	return ctx, nil
}
func (c *userCacheRoundTrips) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// countUserCacheRoundTrips 把 common.RDB 换成带计数钩子的同配置客户端。
func countUserCacheRoundTrips(t *testing.T) *userCacheRoundTrips {
	t.Helper()
	orig := common.RDB
	counted := redis.NewClient(orig.Options())
	counter := &userCacheRoundTrips{}
	counted.AddHook(counter)
	common.RDB = counted
	t.Cleanup(func() {
		common.RDB = orig
		_ = counted.Close()
	})
	return counter
}

func cachedTestUser(t *testing.T) *User {
	t.Helper()
	enableRedis(t)
	u := newTestUser(t, func(u *User) { u.Quota = 500 })
	t.Cleanup(func() {
		_ = common.RDB.Del(context.Background(),
			getUserCacheKey(u.Id), getUserAuthFenceKey(u.Id), getUserAuthVersionKey(u.Id)).Err()
	})
	require.NoError(t, populateUserCache(*u))
	return u
}

func TestCacheGetUserBase_HitIsOneRoundTrip(t *testing.T) {
	u := cachedTestUser(t)
	counter := countUserCacheRoundTrips(t)

	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	assert.Equal(t, u.Id, base.Id)
	assert.Equal(t, 500, base.Quota)
	assert.Equal(t, int64(1), counter.trips.Load())
}

func TestCacheGetUserBase_PendingFenceRejects(t *testing.T) {
	u := cachedTestUser(t)
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)

	require.NoError(t, SetUserAuthVersionFence(u.Id, base.AuthVersion+1))
	_, err = cacheGetUserBase(u.Id)
	assert.ErrorIs(t, err, ErrUserAuthCachePending)
}

func TestCacheGetUserBase_CommittedVersionRejects(t *testing.T) {
	u := cachedTestUser(t)
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)

	require.NoError(t, common.RDB.Set(context.Background(), getUserAuthVersionKey(u.Id), base.AuthVersion+1, 0).Err())
	_, err = cacheGetUserBase(u.Id)
	assert.ErrorIs(t, err, ErrUserAuthCachePending)
}

func TestCacheGetUserBase_FloorEqualToCacheIsHit(t *testing.T) {
	u := cachedTestUser(t)
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)

	require.NoError(t, common.RDB.Set(context.Background(), getUserAuthVersionKey(u.Id), base.AuthVersion, 0).Err())
	_, err = cacheGetUserBase(u.Id)
	assert.NoError(t, err)
}

func TestCacheGetUserBase_MalformedFloorIsError(t *testing.T) {
	u := cachedTestUser(t)
	require.NoError(t, common.RDB.Set(context.Background(), getUserAuthFenceKey(u.Id), "not-a-number", 0).Err())

	_, err := cacheGetUserBase(u.Id)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrUserAuthCachePending)
}

func TestCacheGetUserBase_MissingHashIsError(t *testing.T) {
	u := cachedTestUser(t)
	require.NoError(t, invalidateUserCache(u.Id))

	_, err := cacheGetUserBase(u.Id)
	assert.Error(t, err)
}

func TestCacheGetUserBase_StaleSchemaIsError(t *testing.T) {
	u := cachedTestUser(t)
	require.NoError(t, common.RDB.HSet(context.Background(), getUserCacheKey(u.Id), "CacheSchema", userCacheSchemaVersion-1).Err())

	_, err := cacheGetUserBase(u.Id)
	assert.Error(t, err)
}
