package common

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RedisHIncrBy 以单条脚本执行"键存在且有 TTL 才增减"。这些用例锁定它与旧的
// TTL + MULTI/HINCRBY/EXPIRE 两步实现逐项等价，外加旧实现做不到的原子性。

func TestRedisHIncrByScript_MissingKeyNotCreated(t *testing.T) {
	requireRedis(t)
	key := "test:common:hincr-missing:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisHIncrBy(key, "Quota", 5))

	n, err := RDB.Exists(context.Background(), key).Result()
	require.NoError(t, err)
	assert.Zero(t, n, "a missing cache key must not be recreated as a partial hash")
}

func TestRedisHIncrByScript_KeepsTTL(t *testing.T) {
	requireRedis(t)
	key := "test:common:hincr-ttl:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisHSetObj(key, &redisTestObj{Name: "x", Count: 10}, 100*time.Second))
	require.NoError(t, RedisHIncrBy(key, "Count", -3))

	cnt, err := RDB.HGet(context.Background(), key, "Count").Result()
	require.NoError(t, err)
	assert.Equal(t, "7", cnt)
	ttl, err := RDB.PTTL(context.Background(), key).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 90*time.Second)
	assert.LessOrEqual(t, ttl, 100*time.Second)
}

func TestRedisHIncrByScript_NewFieldOnExistingHash(t *testing.T) {
	requireRedis(t)
	key := "test:common:hincr-newfield:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisHSetObj(key, &redisTestObj{Name: "x"}, time.Minute))
	require.NoError(t, RedisHIncrBy(key, "Extra", 4))

	v, err := RDB.HGet(context.Background(), key, "Extra").Result()
	require.NoError(t, err)
	assert.Equal(t, "4", v)
}

func TestRedisHIncrByScript_ConcurrentExact(t *testing.T) {
	requireRedis(t)
	key := "test:common:hincr-conc:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisHSetObj(key, &redisTestObj{Count: 1000}, time.Minute))
	const workers, perWorker = 20, 25
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			delta := int64(1)
			if i%2 == 1 {
				delta = -2
			}
			for j := 0; j < perWorker; j++ {
				assert.NoError(t, RedisHIncrBy(key, "Count", delta))
			}
		}(i)
	}
	wg.Wait()

	cnt, err := RDB.HGet(context.Background(), key, "Count").Result()
	require.NoError(t, err)
	// 10 个 +1 worker 与 10 个 -2 worker，各 25 次。
	assert.Equal(t, "750", cnt)
}

// roundTripCounter 统计客户端到 Redis 的往返次数：单条命令与一次 pipeline 各算一次。
type roundTripCounter struct{ trips atomic.Int64 }

func (c *roundTripCounter) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	c.trips.Add(1)
	return ctx, nil
}
func (c *roundTripCounter) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (c *roundTripCounter) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	c.trips.Add(1)
	return ctx, nil
}
func (c *roundTripCounter) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// 每次额度增减是 relay 每请求至少两次的异步操作，往返数就是它的成本；
// 拆回"先查 TTL 再写"会在这里失败。
func TestRedisHIncrByScript_OneRoundTrip(t *testing.T) {
	requireRedis(t)
	key := "test:common:hincr-rtt:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })
	require.NoError(t, RedisHSetObj(key, &redisTestObj{Count: 1}, time.Minute))

	orig := RDB
	counted := redis.NewClient(orig.Options())
	counter := &roundTripCounter{}
	counted.AddHook(counter)
	RDB = counted
	t.Cleanup(func() {
		RDB = orig
		_ = counted.Close()
	})

	require.NoError(t, RedisHIncrBy(key, "Count", 1)) // 首次可能因 NOSCRIPT 回退 EVAL
	counter.trips.Store(0)
	for i := 0; i < 10; i++ {
		require.NoError(t, RedisHIncrBy(key, "Count", 1))
	}
	assert.Equal(t, int64(10), counter.trips.Load())
}
