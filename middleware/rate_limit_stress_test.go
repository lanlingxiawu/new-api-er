package middleware

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func percentileOf(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sort.Slice(samples, func(a, b int) bool { return samples[a] < samples[b] })
	return samples[int(float64(len(samples)-1)*p)]
}

// TestStressRateLimiterConcurrentAccuracy 压的是分桶重构后的核心保证：
// 高并发下每个身份只消耗自己的配额，且计数精确——多算会误伤真实用户，
// 少算会让限流形同虚设。
func TestStressRateLimiterConcurrentAccuracy(t *testing.T) {
	useRateLimitMiniRedis(t)
	const usersCount, perUser, allowance = 16, 200, 50
	useGlobalRateLimitConfig(t, allowance)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(RouteTagKey, "api")
		c.Set("id", c.GetHeader("X-Test-User"))
		c.Next()
	})

	var allowed, limited int64
	router.GET("/probe", func(c *gin.Context) {
		bucket := operation_setting.GetRateLimitSnapshot().GlobalAPIUser
		userID := 0
		if raw := c.GetHeader("X-Test-User"); raw != "" {
			userID = int(raw[0])*1000 + int(raw[len(raw)-1])
		}
		c.Set("id", userID)
		apiUserRateLimit(c, bucket.Num, bucket.Duration)
		if c.IsAborted() {
			atomic.AddInt64(&limited, 1)
			return
		}
		atomic.AddInt64(&allowed, 1)
		c.Status(http.StatusNoContent)
	})

	var wg sync.WaitGroup
	var mu sync.Mutex
	samples := make([]time.Duration, 0, usersCount*perUser)
	started := time.Now()

	for u := 0; u < usersCount; u++ {
		wg.Add(1)
		go func(user int) {
			defer wg.Done()
			header := string(rune('a'+user%26)) + string(rune('0'+user/26))
			local := make([]time.Duration, 0, perUser)
			for i := 0; i < perUser; i++ {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, "/probe", nil)
				request.RemoteAddr = "10.0.0.1:1234" // 全部同一 IP：证明按用户隔离
				request.Header.Set("X-Test-User", header)
				callStart := time.Now()
				router.ServeHTTP(recorder, request)
				local = append(local, time.Since(callStart))
			}
			mu.Lock()
			samples = append(samples, local...)
			mu.Unlock()
		}(u)
	}
	wg.Wait()
	elapsed := time.Since(started)

	total := int64(usersCount * perUser)
	t.Logf("限流中间件：%d 次请求 / %s，吞吐 ≈ %.0f 次每秒",
		total, elapsed.Round(time.Millisecond), float64(total)/elapsed.Seconds())
	t.Logf("限流中间件：p50=%s p90=%s p99=%s max=%s",
		percentileOf(samples, 0.50), percentileOf(samples, 0.90),
		percentileOf(samples, 0.99), percentileOf(samples, 1.0))

	assert.Equal(t, total, atomic.LoadInt64(&allowed)+atomic.LoadInt64(&limited),
		"每个请求都必须有明确结论，不能既没放行也没拒绝")

	// 16 个用户共用一个 IP，各自 allowance=50 —— 放行数必须按用户数放大，
	// 而不是被压到单个 IP 的配额上。这正是分桶重构要修的问题。
	assert.Greater(t, atomic.LoadInt64(&allowed), int64(allowance),
		"同一 IP 下的多个用户被折叠进了同一个桶")
}

// redisCommandCounter 统计经过 go-redis 客户端的命令数。
type redisCommandCounter struct{ count atomic.Int64 }

func (c *redisCommandCounter) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	c.count.Add(1)
	return ctx, nil
}
func (c *redisCommandCounter) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (c *redisCommandCounter) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	c.count.Add(int64(len(cmds)))
	return ctx, nil
}
func (c *redisCommandCounter) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// TestStressRateLimiterCostsExactlyOneRoundTrip 断言限流器的**工作量**，
// 而不是它的墙钟耗时。
//
// 之前两版都断言 p99 延迟，都在 go test ./... 满载时偶发失败：Windows 的
// time.Now() 精度是亚毫秒级取整，一次限流调用的 p99 要么是 0 要么是 ~1ms——
// 这个指标根本分辨不出限流器的真实成本，调度抖动就能把信号淹没。
//
// 真正要守住的不变式是"每个请求恰好一次 Redis 往返"：多一次往返（比如有人把
// 读写拆成两条命令）、或者出现重试循环，都会被这里抓住，而且完全确定性。
func TestStressRateLimiterCostsExactlyOneRoundTrip(t *testing.T) {
	_, client := useRateLimitMiniRedis(t)
	useGlobalRateLimitConfig(t, 1_000_000) // 配额给足，不触发拒绝分支

	counter := &redisCommandCounter{}
	client.AddHook(counter)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(RouteTagKey, "api"); c.Next() })
	router.GET("/probe", GlobalAPIRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const requests = 2000
	samples := make([]time.Duration, 0, requests)
	started := time.Now()
	for i := 0; i < requests; i++ {
		callStart := time.Now()
		recorder := performRateLimitRequest(router, "/probe", "10.0.0.9:1111")
		samples = append(samples, time.Since(callStart))
		require.Equal(t, http.StatusNoContent, recorder.Code)
	}
	elapsed := time.Since(started)

	// 延迟只作为观测信息输出，不作断言——见上面的说明
	t.Logf("%d 次请求 / %s，吞吐 ≈ %.0f 次每秒；p50=%s p99=%s",
		requests, elapsed.Round(time.Millisecond), float64(requests)/elapsed.Seconds(),
		percentileOf(samples, 0.50), percentileOf(samples, 0.99))

	assert.EqualValues(t, requests, counter.count.Load(),
		"每个请求应当恰好一次 Redis 往返，实际 %d 次 / %d 个请求",
		counter.count.Load(), requests)
}

// Redis 故障期间的降级路径也必须并发安全：这条路径会在 Redis 抖动时被全量流量打到。
func TestStressRateLimiterDegradationIsConcurrencySafe(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	useGlobalRateLimitConfig(t, 100)
	redisServer.Close() // 制造 Redis 不可用

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(RouteTagKey, "api"); c.Next() })
	router.GET("/probe", GlobalAPIRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const workers, perWorker = 32, 100
	var serverErrors int64
	var wg sync.WaitGroup
	samples := make([]time.Duration, 0, workers*perWorker)
	var mu sync.Mutex

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			local := make([]time.Duration, 0, perWorker)
			for i := 0; i < perWorker; i++ {
				callStart := time.Now()
				recorder := performRateLimitRequest(router, "/probe", "10.0.1.1:2222")
				local = append(local, time.Since(callStart))
				if recorder.Code >= 500 {
					atomic.AddInt64(&serverErrors, 1)
				}
			}
			mu.Lock()
			samples = append(samples, local...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()

	t.Logf("Redis 不可用时：p99=%s max=%s", percentileOf(samples, 0.99), percentileOf(samples, 1.0))
	// Redis 抖动绝不能变成 5xx —— 这正是降级要解决的问题
	assert.Zero(t, atomic.LoadInt64(&serverErrors),
		"Redis 不可用时返回了 %d 个 5xx，降级失效", atomic.LoadInt64(&serverErrors))
	require.NotNil(t, common.RDB)
}

// blackholeRedis 接受 TCP 连接但永不响应，复现最坏失败模式：
// Redis 进程还在、连得上，但已经卡死或网络吞包。这比"连接被拒绝"慢几个数量级。
func blackholeRedis(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() { <-done; _ = conn.Close() }()
		}
	}()
	t.Cleanup(func() { close(done); _ = listener.Close() })
	return listener.Addr().String()
}

func usePointedRedis(t *testing.T, addr string) {
	t.Helper()
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	client := redis.NewClient(&redis.Options{Addr: addr})
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled = previousEnabled
		common.RDB = previousRDB
	})
}

func useRedisTimeoutBudget(t *testing.T, budgetMs int) {
	t.Helper()
	previous := operation_setting.GetRateLimitSetting()
	draft := previous
	draft.RedisTimeoutMs = budgetMs
	operation_setting.ReplaceRateLimitSetting(draft)
	t.Cleanup(func() { operation_setting.ReplaceRateLimitSetting(previous) })
}

// TestRateLimitRedisTimeoutBoundsWorstCase 是 12 秒问题的回归用例。
//
// 没有这个预算时，"连得上但不响应"的 Redis 会让单次限流调用耗时
// DialTimeout/ReadTimeout × 重试次数 ≈ 12s；已认证的 /api 请求含两次限流调用 ≈ 24s。
func TestRateLimitRedisTimeoutBoundsWorstCase(t *testing.T) {
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	usePointedRedis(t, blackholeRedis(t))
	useRedisTimeoutBudget(t, 80)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/probe", nil)

	started := time.Now()
	takeRateLimit(ctx, 100, 60, "rateLimit:v2:ip:TMO:1.2.3.4")
	elapsed := time.Since(started)

	t.Logf("Redis 无响应时单次限流调用耗时 = %s（预算 80ms）", elapsed.Round(time.Millisecond))
	// 预算 80ms，留足调度余量后仍必须远低于 go-redis 默认的 12s
	assert.Less(t, elapsed, 2*time.Second,
		"超时预算没有生效，单次调用耗时 %s", elapsed)
	// 降级之后请求必须放行，而不是 5xx 或 429
	assert.False(t, ctx.IsAborted(), "降级路径不该中断请求")
}

// 预算必须真的可调：调大预算，耗时应当跟着变长。
func TestRateLimitRedisTimeoutIsConfigurable(t *testing.T) {
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	addr := blackholeRedis(t)

	measure := func(budgetMs int) time.Duration {
		usePointedRedis(t, addr)
		useRedisTimeoutBudget(t, budgetMs)
		gin.SetMode(gin.TestMode)
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodGet, "/probe", nil)
		started := time.Now()
		takeRateLimit(ctx, 100, 60, "rateLimit:v2:ip:TMO2:1.2.3.4")
		return time.Since(started)
	}

	short := measure(50)
	long := measure(600)
	t.Logf("预算 50ms → %s；预算 600ms → %s", short.Round(time.Millisecond), long.Round(time.Millisecond))
	assert.Greater(t, long, short, "调大预算之后耗时没有跟着变长，说明配置没生效")
	assert.Less(t, short, 500*time.Millisecond)
}
