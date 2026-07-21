package common

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type redisTestObj struct {
	Name   string
	Count  int
	Active bool
	Opt    *string
}

func TestRedisKeyCacheSeconds(t *testing.T) {
	orig := SyncFrequency
	t.Cleanup(func() { SyncFrequency = orig })
	SyncFrequency = 123
	assert.Equal(t, 123, RedisKeyCacheSeconds())
}

func TestParseRedisOption(t *testing.T) {
	requireRedis(t)
	opt := ParseRedisOption()
	require.NotNil(t, opt)
	assert.NotEmpty(t, opt.Addr)
}

func TestRedisSetGetDel(t *testing.T) {
	requireRedis(t)
	key := "test:common:setget:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisSet(key, "hello", time.Minute))
	v, err := RedisGet(key)
	require.NoError(t, err)
	assert.Equal(t, "hello", v)

	require.NoError(t, RedisDel(key))
	_, err = RedisGet(key)
	assert.Error(t, err) // redis.Nil for a missing key

	// RedisDelKey is an alias for RedisDel
	require.NoError(t, RedisSet(key, "x", time.Minute))
	require.NoError(t, RedisDelKey(key))
}

func TestRedisHSetGetObj(t *testing.T) {
	requireRedis(t)
	key := "test:common:hobj:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	opt := "optional"
	in := &redisTestObj{Name: "alice", Count: 10, Active: true, Opt: &opt}
	require.NoError(t, RedisHSetObj(key, in, time.Minute))

	var out redisTestObj
	require.NoError(t, RedisHGetObj(key, &out))
	assert.Equal(t, "alice", out.Name)
	assert.Equal(t, 10, out.Count)
	assert.True(t, out.Active)
	require.NotNil(t, out.Opt)
	assert.Equal(t, "optional", *out.Opt)
}

func TestRedisHSetObj_NilPointerStoredEmpty(t *testing.T) {
	requireRedis(t)
	key := "test:common:hobjnil:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	in := &redisTestObj{Name: "bob", Count: 1, Opt: nil}
	require.NoError(t, RedisHSetObj(key, in, 0)) // expiration 0 => no TTL set

	var out redisTestObj
	require.NoError(t, RedisHGetObj(key, &out))
	assert.Equal(t, "bob", out.Name)
	assert.Nil(t, out.Opt) // empty stored value leaves pointer nil
}

func TestRedisHGetObj_Errors(t *testing.T) {
	requireRedis(t)

	// missing key -> not-found error (HGetAll returns empty)
	var out redisTestObj
	err := RedisHGetObj("test:common:absent:"+GetRandomString(8), &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// The pointer-shape checks run only after a non-empty hash is loaded, so
	// seed a real key first.
	key := "test:common:hgeterr:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })
	require.NoError(t, RedisHSetObj(key, &redisTestObj{Name: "x", Count: 1}, time.Minute))

	// non-pointer target
	err = RedisHGetObj(key, redisTestObj{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a pointer")

	// pointer to non-struct
	s := ""
	err = RedisHGetObj(key, &s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pointer to a struct")
}

func TestRedisIncr(t *testing.T) {
	requireRedis(t)
	key := "test:common:incr:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	// key without TTL => Incr is a no-op (guards billing keys that must expire)
	require.NoError(t, RedisSet(key, "10", 0))
	require.NoError(t, RedisIncr(key, 5))
	v, _ := RedisGet(key)
	assert.Equal(t, "10", v, "no-TTL key must not be incremented")

	// key with TTL => incremented and TTL preserved
	require.NoError(t, RedisSet(key, "10", time.Minute))
	require.NoError(t, RedisIncr(key, 5))
	v, _ = RedisGet(key)
	assert.Equal(t, "15", v)
	ttl, err := RDB.TTL(context.Background(), key).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
}

func TestRedisHIncrByAndHSetField(t *testing.T) {
	requireRedis(t)
	key := "test:common:hincr:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisHSetObj(key, &redisTestObj{Name: "x", Count: 10}, time.Minute))

	require.NoError(t, RedisHIncrBy(key, "Count", 5))
	cnt, err := RDB.HGet(context.Background(), key, "Count").Result()
	require.NoError(t, err)
	assert.Equal(t, "15", cnt)

	require.NoError(t, RedisHSetField(key, "Name", "y"))
	name, err := RDB.HGet(context.Background(), key, "Name").Result()
	require.NoError(t, err)
	assert.Equal(t, "y", name)
}

func TestRedisHIncrBy_NoTTLNoOp(t *testing.T) {
	requireRedis(t)
	key := "test:common:hincrnottl:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	// hash without TTL => HIncrBy is a no-op
	require.NoError(t, RDB.HSet(context.Background(), key, "Count", "10").Err())
	require.NoError(t, RedisHIncrBy(key, "Count", 5))
	cnt, _ := RDB.HGet(context.Background(), key, "Count").Result()
	assert.Equal(t, "10", cnt)
}

// ---- infra error branches via a client dialed at a closed port ----

func TestRedisErrorBranches(t *testing.T) {
	withBrokenRDB(t, func() {
		assert.Error(t, RedisSet("k", "v", time.Minute))
		_, err := RedisGet("k")
		assert.Error(t, err)
		assert.Error(t, RedisDel("k"))
		assert.Error(t, RedisIncr("k", 1))
		assert.Error(t, RedisHIncrBy("k", "f", 1))
		assert.Error(t, RedisHSetField("k", "f", "v"))
		assert.Error(t, RedisHSetObj("k", &redisTestObj{}, time.Minute))
		assert.Error(t, RedisHGetObj("k", &redisTestObj{}))
	})
}
