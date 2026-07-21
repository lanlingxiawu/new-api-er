package cachex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"github.com/samber/hot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Redis harness (skips when unreachable, like the other Tier-1 packages).
// ---------------------------------------------------------------------------

func findRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func dialRedis(t *testing.T) *redis.Client {
	t.Helper()
	if root := findRoot(); root != "" {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
	conn := os.Getenv("REDIS_CONN_STRING")
	if conn == "" {
		t.Skip("REDIS_CONN_STRING not set; skipping cachex Redis tests")
	}
	opt, err := redis.ParseURL(conn)
	require.NoError(t, err)
	client := redis.NewClient(opt)
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis unreachable (%v); skipping", err)
	}
	return client
}

// memCacheOf builds a memory-only HybridCache (Redis nil => redisOn false) with
// a roomy backing cache so multi-key assertions are not evicted.
func memCacheOf[V any](ns Namespace) *HybridCache[V] {
	return NewHybridCache[V](HybridCacheConfig[V]{
		Namespace: ns,
		Memory:    func() *hot.HotCache[string, V] { return hot.NewHotCache[string, V](hot.LRU, 100).Build() },
	})
}

// redisCacheOf builds a Redis-backed string cache under a unique namespace and
// purges it on cleanup.
func redisCacheOf(t *testing.T, ns Namespace) *HybridCache[string] {
	t.Helper()
	client := dialRedis(t)
	c := NewHybridCache[string](HybridCacheConfig[string]{
		Namespace:    ns,
		Redis:        client,
		RedisCodec:   StringCodec{},
		RedisEnabled: func() bool { return true },
	})
	t.Cleanup(func() { _ = c.Purge() })
	return c
}

// ---------------------------------------------------------------------------
// redisOn() decision matrix (through observable behavior).
// ---------------------------------------------------------------------------

func TestRedisOn_NilRedisUsesMemory(t *testing.T) {
	c := memCacheOf[string]("ns")
	main, missing := c.Algorithm()
	assert.NotEqual(t, "redis", main, "no redis client => memory algorithm")
	_ = missing
}

func TestRedisOn_NilCodecFallsBackToMemory(t *testing.T) {
	client := dialRedis(t)
	// Redis client present but codec nil => redisOn() is false => memory path.
	c := NewHybridCache[string](HybridCacheConfig[string]{
		Namespace:    "ns-nocodec",
		Redis:        client,
		RedisCodec:   nil,
		RedisEnabled: func() bool { return true },
		Memory:       func() *hot.HotCache[string, string] { return hot.NewHotCache[string, string](hot.LRU, 10).Build() },
	})
	main, _ := c.Algorithm()
	assert.NotEqual(t, "redis", main, "nil codec forces memory path")
}

// ---------------------------------------------------------------------------
// Memory path.
// ---------------------------------------------------------------------------

func TestMemory_SetGet(t *testing.T) {
	c := memCacheOf[string]("app")
	require.NoError(t, c.SetWithTTL("k", "v", time.Minute))

	v, found, err := c.Get("k")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "v", v)
}

func TestMemory_GetMiss(t *testing.T) {
	c := memCacheOf[string]("app")
	_, found, err := c.Get("absent")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestMemory_EmptyKeyIsNoOp(t *testing.T) {
	c := memCacheOf[string]("app")
	// Empty full key => Get returns zero/false/nil and Set is a no-op.
	require.NoError(t, c.SetWithTTL("", "v", time.Minute))
	v, found, err := c.Get("")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, "", v)
}

func TestMemory_FullKey(t *testing.T) {
	c := memCacheOf[string]("app:v1")
	assert.Equal(t, "app:v1:k", c.FullKey("k"))
}

func TestMemory_KeysAndPurge(t *testing.T) {
	c := memCacheOf[string]("app")
	require.NoError(t, c.SetWithTTL("a", "1", time.Minute))
	require.NoError(t, c.SetWithTTL("b", "2", time.Minute))

	keys, err := c.Keys()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"app:a", "app:b"}, keys)

	require.NoError(t, c.Purge())
	keys, err = c.Keys()
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestMemory_DeleteByPrefix(t *testing.T) {
	c := memCacheOf[string]("app")
	require.NoError(t, c.SetWithTTL("user:1", "a", time.Minute))
	require.NoError(t, c.SetWithTTL("user:2", "b", time.Minute))
	require.NoError(t, c.SetWithTTL("order:1", "c", time.Minute))

	n, err := c.DeleteByPrefix("user")
	require.NoError(t, err)
	assert.Equal(t, 2, n, "only the two user:* keys deleted")

	_, found, _ := c.Get("order:1")
	assert.True(t, found, "unrelated prefix untouched")
}

func TestMemory_DeleteByPrefix_EmptyPrefix(t *testing.T) {
	c := memCacheOf[string]("app")
	n, err := c.DeleteByPrefix("")
	require.NoError(t, err)
	assert.Equal(t, 0, n, "empty prefix resolves to empty full key => no deletion")
}

func TestMemory_DeleteMany(t *testing.T) {
	c := memCacheOf[string]("app")
	require.NoError(t, c.SetWithTTL("a", "1", time.Minute))
	require.NoError(t, c.SetWithTTL("b", "2", time.Minute))

	res, err := c.DeleteMany([]string{"a", "b", "never-set"})
	require.NoError(t, err)
	assert.True(t, res["app:a"])
	assert.True(t, res["app:b"])
}

func TestMemory_DeleteMany_Empty(t *testing.T) {
	c := memCacheOf[string]("app")
	res, err := c.DeleteMany(nil)
	require.NoError(t, err)
	assert.Empty(t, res)
}

func TestMemory_DeleteMany_AllKeysResolveEmpty(t *testing.T) {
	c := memCacheOf[string]("app")
	// Blank/whitespace keys all resolve to an empty full key => skipped, leaving
	// no keys to delete (covers the k=="" continue and empty-fullKeys guard).
	res, err := c.DeleteMany([]string{"", "   "})
	require.NoError(t, err)
	assert.Empty(t, res)
}

func TestMemory_CapacityAndAlgorithm(t *testing.T) {
	c := memCacheOf[string]("app")
	main, _ := c.Capacity()
	assert.Equal(t, 100, main, "memory capacity reflects the backing cache")
	algo, _ := c.Algorithm()
	assert.NotEqual(t, "redis", algo)
}

func TestMemory_DefaultBackingCacheWhenNoMemoryFunc(t *testing.T) {
	// memInit nil => memCache() builds a default LRU cache of capacity 1.
	c := NewHybridCache[string](HybridCacheConfig[string]{Namespace: "app"})
	require.NoError(t, c.SetWithTTL("k", "v", time.Minute))
	v, found, err := c.Get("k")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "v", v)
	main, _ := c.Capacity()
	assert.Equal(t, 1, main, "default backing cache capacity is 1")
}

// ---------------------------------------------------------------------------
// Redis path (real Redis).
// ---------------------------------------------------------------------------

func TestRedis_SetGetRoundTrip(t *testing.T) {
	c := redisCacheOf(t, "test:cachex:rt")
	require.NoError(t, c.SetWithTTL("k", "v", time.Minute))
	v, found, err := c.Get("k")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "v", v)
}

func TestRedis_GetMissReturnsNotFound(t *testing.T) {
	c := redisCacheOf(t, "test:cachex:miss")
	_, found, err := c.Get("absent")
	require.NoError(t, err, "redis.Nil is not an error at this layer")
	assert.False(t, found)
}

func TestRedis_DecodeErrorSurfaces(t *testing.T) {
	client := dialRedis(t)
	ns := Namespace("test:cachex:decerr")
	c := NewHybridCache[int](HybridCacheConfig[int]{
		Namespace: ns, Redis: client, RedisCodec: IntCodec{}, RedisEnabled: func() bool { return true },
	})
	t.Cleanup(func() { _ = c.Purge() })
	// Store a value that IntCodec.Decode cannot parse.
	require.NoError(t, client.Set(context.Background(), ns.FullKey("k"), "not-an-int", time.Minute).Err())

	_, found, err := c.Get("k")
	assert.Error(t, err, "decode failure must surface")
	assert.False(t, found)
}

func TestRedis_EncodeErrorSurfaces(t *testing.T) {
	client := dialRedis(t)
	c := NewHybridCache[map[string]any](HybridCacheConfig[map[string]any]{
		Namespace: "test:cachex:encerr", Redis: client,
		RedisCodec: JSONCodec[map[string]any]{}, RedisEnabled: func() bool { return true },
	})
	err := c.SetWithTTL("k", map[string]any{"bad": make(chan int)}, time.Minute)
	assert.Error(t, err, "unmarshalable value must fail on encode")
}

func TestRedis_KeysScan(t *testing.T) {
	c := redisCacheOf(t, "test:cachex:keys")
	require.NoError(t, c.SetWithTTL("a", "1", time.Minute))
	require.NoError(t, c.SetWithTTL("b", "2", time.Minute))
	keys, err := c.Keys()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"test:cachex:keys:a", "test:cachex:keys:b"}, keys)
}

func TestRedis_DeleteByPrefix(t *testing.T) {
	c := redisCacheOf(t, "test:cachex:delprefix")
	require.NoError(t, c.SetWithTTL("user:1", "a", time.Minute))
	require.NoError(t, c.SetWithTTL("user:2", "b", time.Minute))
	require.NoError(t, c.SetWithTTL("order:1", "c", time.Minute))

	n, err := c.DeleteByPrefix("user")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	_, found, _ := c.Get("order:1")
	assert.True(t, found)
}

func TestRedis_DeleteMany(t *testing.T) {
	c := redisCacheOf(t, "test:cachex:delmany")
	require.NoError(t, c.SetWithTTL("a", "1", time.Minute))
	require.NoError(t, c.SetWithTTL("b", "2", time.Minute))

	res, err := c.DeleteMany([]string{"a", "b", "absent"})
	require.NoError(t, err)
	assert.True(t, res["test:cachex:delmany:a"])
	assert.True(t, res["test:cachex:delmany:b"])
	assert.False(t, res["test:cachex:delmany:absent"], "deleting a missing key reports false")
}

func TestRedis_Purge(t *testing.T) {
	c := redisCacheOf(t, "test:cachex:purge")
	require.NoError(t, c.SetWithTTL("a", "1", time.Minute))
	require.NoError(t, c.Purge())
	keys, err := c.Keys()
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestRedis_PurgeEmptyIsNoOp(t *testing.T) {
	c := redisCacheOf(t, "test:cachex:purge-empty")
	require.NoError(t, c.Purge(), "purging an empty namespace must not error")
}

func TestRedis_CapacityAndAlgorithm(t *testing.T) {
	c := redisCacheOf(t, "test:cachex:cap")
	main, missing := c.Capacity()
	assert.Equal(t, 0, main)
	assert.Equal(t, 0, missing)
	algo, _ := c.Algorithm()
	assert.Equal(t, "redis", algo)
}

func TestRedisOn_NilEnabledDefaultsToRedis(t *testing.T) {
	client := dialRedis(t)
	// redisEnabled == nil, with a client + codec present, means "always on".
	c := NewHybridCache[string](HybridCacheConfig[string]{
		Namespace: "test:cachex:nilenabled", Redis: client, RedisCodec: StringCodec{},
	})
	algo, _ := c.Algorithm()
	assert.Equal(t, "redis", algo, "nil RedisEnabled => redis path")
}

// ---------------------------------------------------------------------------
// Redis error branches — a client aimed at a closed port fails every op fast,
// deterministically covering the error returns without a live Redis.
// ---------------------------------------------------------------------------

func brokenRedisCache() *HybridCache[string] {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 500 * time.Millisecond})
	return NewHybridCache[string](HybridCacheConfig[string]{
		Namespace: "test:cachex:broken", Redis: client, RedisCodec: StringCodec{},
		RedisEnabled: func() bool { return true },
	})
}

func TestRedis_BrokenClient_GetErrors(t *testing.T) {
	c := brokenRedisCache()
	_, found, err := c.Get("k")
	assert.Error(t, err, "connection failure (not redis.Nil) surfaces")
	assert.False(t, found)
}

func TestRedis_BrokenClient_KeysErrors(t *testing.T) {
	c := brokenRedisCache()
	_, err := c.Keys()
	assert.Error(t, err)
}

func TestRedis_BrokenClient_PurgeErrors(t *testing.T) {
	c := brokenRedisCache()
	assert.Error(t, c.Purge())
}

func TestRedis_BrokenClient_DeleteByPrefixErrors(t *testing.T) {
	c := brokenRedisCache()
	_, err := c.DeleteByPrefix("x")
	assert.Error(t, err)
}

func TestRedis_BrokenClient_DeleteManyErrors(t *testing.T) {
	c := brokenRedisCache()
	_, err := c.DeleteMany([]string{"a"})
	assert.Error(t, err)
}
