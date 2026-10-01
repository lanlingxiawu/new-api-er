package common

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Branch audit of the fork's Redis helpers: the guarded HINCRBY Lua script and
// the DecodeRedisHash split that lets the user cache fold HGETALL into a
// pipeline. The script must surface Redis-side failures (the old
// TxPipeline+Exec path returned them too) and must never mutate the hash when
// HINCRBY itself is rejected.

func TestBranchAuditHIncrByScript_NonIntegerFieldErrorsAndLeavesHash(t *testing.T) {
	requireRedis(t)
	key := "test:common:audit-hincr-nonint:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })
	require.NoError(t, RedisHSetObj(key, &redisTestObj{Name: "not-a-number", Count: 3}, time.Minute))

	err := RedisHIncrBy(key, "Name", 1)

	require.Error(t, err, "HINCRBY on a non-integer field must not be swallowed as redis.Nil")
	name, getErr := RDB.HGet(context.Background(), key, "Name").Result()
	require.NoError(t, getErr)
	assert.Equal(t, "not-a-number", name)
}

func TestBranchAuditHIncrByScript_OverflowErrorsAndKeepsValue(t *testing.T) {
	requireRedis(t)
	key := "test:common:audit-hincr-overflow:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })
	require.NoError(t, RedisHSetObj(key, &redisTestObj{Count: 1}, time.Minute))
	require.NoError(t, RDB.HSet(context.Background(), key, "Count", int64(math.MaxInt64)).Err())

	err := RedisHIncrBy(key, "Count", 1)

	require.Error(t, err, "int64 overflow must be reported, not wrapped around")
	v, getErr := RDB.HGet(context.Background(), key, "Count").Int64()
	require.NoError(t, getErr)
	assert.Equal(t, int64(math.MaxInt64), v)
}

func TestBranchAuditHIncrByScript_WrongTypeKeyErrors(t *testing.T) {
	requireRedis(t)
	key := "test:common:audit-hincr-wrongtype:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })
	require.NoError(t, RDB.Set(context.Background(), key, "plain-string", time.Minute).Err())

	assert.Error(t, RedisHIncrBy(key, "Quota", 1))
	v, err := RDB.Get(context.Background(), key).Result()
	require.NoError(t, err)
	assert.Equal(t, "plain-string", v)
}

// Argument order is KEYS[1]=key, ARGV[1]=field, ARGV[2]=delta: a large negative
// delta proves the delta (not the field name) is what reaches HINCRBY.
func TestBranchAuditHIncrByScript_LargeNegativeDeltaAndReturnsNoError(t *testing.T) {
	requireRedis(t)
	key := "test:common:audit-hincr-neg:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })
	require.NoError(t, RedisHSetObj(key, &redisTestObj{Count: 10}, time.Minute))

	require.NoError(t, RedisHIncrBy(key, "Count", -1_000_000_000_000))

	v, err := RDB.HGet(context.Background(), key, "Count").Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(10-1_000_000_000_000), v)
}

func TestBranchAuditDecodeRedisHash_Partitions(t *testing.T) {
	var obj redisTestObj
	assert.Error(t, DecodeRedisHash("k", nil, &obj), "an empty HGETALL result is a cache miss")
	assert.Error(t, DecodeRedisHash("k", map[string]string{}, &obj))
	assert.Error(t, DecodeRedisHash("k", map[string]string{"Name": "x"}, obj), "non-pointer target")
	n := 1
	assert.Error(t, DecodeRedisHash("k", map[string]string{"Name": "x"}, &n), "pointer to non-struct")
	assert.Error(t, DecodeRedisHash("k", map[string]string{"Count": "12x"}, &obj), "malformed int")
	assert.Error(t, DecodeRedisHash("k", map[string]string{"Active": "maybe"}, &obj), "malformed bool")

	opt := "o"
	obj = redisTestObj{Opt: &opt}
	require.NoError(t, DecodeRedisHash("k", map[string]string{
		"Name": "n", "Count": "-7", "Active": "true", "Opt": "", "Unknown": "ignored",
	}, &obj))
	assert.Equal(t, "n", obj.Name)
	assert.Equal(t, -7, obj.Count)
	assert.True(t, obj.Active)
	require.NotNil(t, obj.Opt, "an empty value leaves an existing pointer untouched")
	assert.Equal(t, "o", *obj.Opt)
}
