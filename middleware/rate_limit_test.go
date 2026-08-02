package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useRateLimitMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()

	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	require.NoError(t, redisClient.Ping(context.Background()).Err())

	common.RedisEnabled = true
	common.RDB = redisClient

	// 把 Redis 超时预算放到最大。
	//
	// 生产默认是 100ms：Redis 响应超过这个预算就降级到本节点内存计数，用有界延迟
	// 换精确计数，这是刻意的取舍。但下面那些断言"精确放行几次"的用例一旦踩到降级，
	// 计数就换了一套计数器，结果不再确定——在 go test ./... 满载时 miniredis 确实
	// 会偶发超过 100ms。用例要验的是限流逻辑本身，不是降级时机，所以这里把预算调大，
	// 让降级路径只在专门测它的用例里发生。
	previousSetting := operation_setting.GetRateLimitSetting()
	pinned := previousSetting
	pinned.RedisTimeoutMs = 5_000
	operation_setting.ReplaceRateLimitSetting(pinned)

	t.Cleanup(func() {
		operation_setting.ReplaceRateLimitSetting(previousSetting)
		_ = redisClient.Close()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	return redisServer, redisClient
}

func performRateLimitRequest(router http.Handler, path string, remoteAddr string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = remoteAddr
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestRedisIPRateLimiterThresholdTTLAndNamespace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/limited", rateLimitFactory(staticBucket(2, 37), "TEST"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	remoteAddr := "192.0.2.10:12345"
	legacyKey := "rateLimit:TEST192.0.2.10"
	_, err := redisServer.Push(legacyKey, "legacy-list-entry")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", remoteAddr).Code)
	limitedResponse := performRateLimitRequest(router, "/limited", remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, limitedResponse.Code)
	assert.Equal(t, "37", limitedResponse.Header().Get("Retry-After"))

	key := redisIPRateLimitKey("TEST", "192.0.2.10")
	count, err := redisServer.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "3", count)
	assert.Equal(t, 37*time.Second, redisServer.TTL(key))
	assert.True(t, redisServer.Exists(legacyKey), "the v2 counter must not touch an old list key")
}

func TestRedisUserRateLimiterUsesSharedFixedWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	router.GET(
		"/limited",
		func(c *gin.Context) { c.Set("id", 42) },
		userRateLimitFactory(staticBucket(1, 23), "USER"),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", "192.0.2.20:12345").Code)
	assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/limited", "198.51.100.20:12345").Code)

	key := redisUserRateLimitKey("USER", 42)
	assert.True(t, redisServer.Exists(key))
	assert.Equal(t, 23*time.Second, redisServer.TTL(key))
}

func TestWriteRateLimitedReturnsActionableJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Accept-Language", "en")

	writeRateLimited(c, 17)

	assert.Equal(t, http.StatusTooManyRequests, recorder.Code)
	assert.Equal(t, "17", recorder.Header().Get("Retry-After"))
	assert.JSONEq(t, `{"success":false,"message":"Too many requests. Please retry in 17 seconds."}`, recorder.Body.String())
}

func TestAPIUserRateLimiterIsolatesUsersBehindSameIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	router.GET("/limited", func(c *gin.Context) {
		c.Set(RouteTagKey, "api")
		userID, err := strconv.Atoi(c.Query("user"))
		require.NoError(t, err)
		c.Set("id", userID)
		apiUserRateLimit(c, 1, 31)
		if !c.IsAborted() {
			c.Status(http.StatusNoContent)
		}
	})

	remoteAddr := "192.0.2.25:12345"
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited?user=1", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited?user=2", remoteAddr).Code)
	assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/limited?user=1", remoteAddr).Code)
	assert.True(t, redisServer.Exists(redisUserRateLimitKey("GAU", 1)))
	assert.True(t, redisServer.Exists(redisUserRateLimitKey("GAU", 2)))
}

func TestAPIUserRateLimiterSkipsNonAPIRoutesAndAnonymousRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	for _, testCase := range []struct {
		name     string
		routeTag string
		userID   int
	}{
		{name: "non api route", routeTag: "relay", userID: 7},
		{name: "anonymous api route", routeTag: "api", userID: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			c.Set(RouteTagKey, testCase.routeTag)
			c.Set("id", testCase.userID)

			apiUserRateLimit(c, 1, 31)

			assert.False(t, c.IsAborted())
		})
	}
	assert.Empty(t, redisServer.Keys())
}

// useCriticalRateLimitConfig pins the critical-bucket quotas so the assertions
// below stay independent of whatever InitEnv read from the environment.
func useCriticalRateLimitConfig(t *testing.T, criticalNum int, sessionNum int, sessionIPNum int) {
	t.Helper()

	previous := operation_setting.GetRateLimitSetting()
	setting := previous
	setting.CriticalEnabled, setting.CriticalNum, setting.CriticalDurationSec = true, criticalNum, 60
	setting.AuthRefreshEnabled, setting.AuthRefreshNum, setting.AuthRefreshIPNum, setting.AuthRefreshDurationSec = true, sessionNum, sessionIPNum, 60
	operation_setting.ReplaceRateLimitSetting(setting)

	t.Cleanup(func() { operation_setting.ReplaceRateLimitSetting(previous) })
}

// useGlobalRateLimitConfig enables the global buckets with a fixed allowance so
// the assertions do not depend on whatever InitEnv read from the environment.
func useGlobalRateLimitConfig(t *testing.T, num int) {
	t.Helper()

	previous := operation_setting.GetRateLimitSetting()
	setting := previous
	setting.GlobalWebEnabled, setting.GlobalWebNum, setting.GlobalWebDurationSec = true, num, 60
	setting.GlobalAPIEnabled, setting.GlobalAPINum, setting.GlobalAPIDurationSec = true, num, 60
	setting.GlobalAPIUserEnabled, setting.GlobalAPIUserNum, setting.GlobalAPIUserDurationSec = true, num, 60
	operation_setting.ReplaceRateLimitSetting(setting)

	t.Cleanup(func() {
		operation_setting.ReplaceRateLimitSetting(previous)
	})
}

func performCookieRequest(router http.Handler, path string, remoteAddr string, cookieValue string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, nil)
	request.RemoteAddr = remoteAddr
	if cookieValue != "" {
		request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: cookieValue})
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestUserCriticalRateLimitIsolatesUsersBehindSameIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)
	useCriticalRateLimitConfig(t, 1, 1, 100)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/pay",
		func(c *gin.Context) {
			userID, err := strconv.Atoi(c.Query("user"))
			require.NoError(t, err)
			c.Set("id", userID)
		},
		UserCriticalRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	remoteAddr := "192.0.2.70:12345"
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/pay?user=11", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/pay?user=12", remoteAddr).Code)
	assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/pay?user=11", remoteAddr).Code)

	assert.True(t, redisServer.Exists(redisUserRateLimitKey(criticalUserRateLimitMark, 11)))
	assert.True(t, redisServer.Exists(redisUserRateLimitKey(criticalUserRateLimitMark, 12)))
	assert.False(t, redisServer.Exists(redisIPRateLimitKey(criticalRateLimitMark, "192.0.2.70")),
		"an authenticated action must not draw from the anonymous login bucket")
}

func TestUserCriticalRateLimitFallsBackToItsOwnIPBucketWhenAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)
	useCriticalRateLimitConfig(t, 1, 1, 100)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/pay", UserCriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	remoteAddr := "192.0.2.71:12345"
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/pay", remoteAddr).Code)
	assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/pay", remoteAddr).Code)

	assert.True(t, redisServer.Exists(redisIPRateLimitKey(criticalUserRateLimitMark, "192.0.2.71")))
	assert.False(t, redisServer.Exists(redisIPRateLimitKey(criticalRateLimitMark, "192.0.2.71")),
		"the anonymous fallback must stay out of the login bucket")
}

// TestSessionCriticalRateLimitIsolatesSessionsBehindSameIP is the regression
// test for the incident where employees behind one NAT egress could not log in:
// their periodic token refreshes all landed in the shared IP-keyed login bucket
// and drained it. Refreshes must now be counted per login session.
func TestSessionCriticalRateLimitIsolatesSessionsBehindSameIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)
	useCriticalRateLimitConfig(t, 1, 1, 100)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/refresh", SessionCriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const (
		remoteAddr = "192.0.2.72:12345"
		firstSID   = "6f1b1f9c-6a1a-4f4a-9d3f-1a2b3c4d5e6f"
		secondSID  = "7e2c2a0d-7b2b-4a5b-8e4f-2b3c4d5e6f70"
	)
	assert.Equal(t, http.StatusNoContent, performCookieRequest(router, "/refresh", remoteAddr, firstSID+".secret-a").Code)
	assert.Equal(t, http.StatusNoContent, performCookieRequest(router, "/refresh", remoteAddr, secondSID+".secret-b").Code)
	assert.Equal(t, http.StatusTooManyRequests, performCookieRequest(router, "/refresh", remoteAddr, firstSID+".secret-a").Code)
	// Rotation replaces the secret but keeps the SID, so the counter follows the session.
	assert.Equal(t, http.StatusTooManyRequests, performCookieRequest(router, "/refresh", remoteAddr, firstSID+".rotated-secret").Code)

	assert.True(t, redisServer.Exists(redisSessionRateLimitKey(criticalSessionRateLimitMark, firstSID)))
	assert.True(t, redisServer.Exists(redisSessionRateLimitKey(criticalSessionRateLimitMark, secondSID)))
	// 会话之间互不挤占，同时也不能消耗同一办公室的**登录**配额——
	// 那是当初事故的根因，两个桶必须分开。
	assert.False(t, redisServer.Exists(redisIPRateLimitKey(criticalRateLimitMark, "192.0.2.72")),
		"refreshing a session must not consume the login allowance of the same office")
	// 刷新端点自己的 IP 桶则**必须**被计数：会话 ID 无法验真，
	// 它是伪造 cookie 的唯一成本来源。
	assert.True(t, redisServer.Exists(redisIPRateLimitKey(criticalSessionRateLimitMark, "192.0.2.72")),
		"刷新请求必须计入本端点的 IP 桶，否则伪造 SID 就能完全绕过限流")
}

// A per-IP allowance is drained by whoever else shares the egress, so charging a
// valid session to one would reintroduce the outage this split removes: a deploy
// that makes every open tab refresh at once must not lock an office out.
// TestSessionCriticalRateLimitCannotBeBypassedByForgedSessionIDs 是伪造 SID 绕过
// 限流的回归用例。
//
// requestLoginSessionID 只做 splitRefreshToken：切分 `sid.secret` 并检查 sid 能否
// uuid.Parse——**不验签、不查库**。早先的实现只对"没有可用 cookie"的请求计 IP 桶，
// 于是随手编一个随机 UUID 就能拿到私有计数器，每次换一个就永远打不满，
// 专用 IP 桶被完全绕过，只剩全局 API 桶（2000/180s）兜底，
// 反而比合法降级路径（600/1200s）宽 22 倍——保护关系是反的。
func TestSessionCriticalRateLimitCannotBeBypassedByForgedSessionIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useRateLimitMiniRedis(t)
	// 每 IP 只允许 2 次，会话配额给足——证明拦住伪造的是 IP 桶而不是会话桶
	useCriticalRateLimitConfig(t, 100, 100, 2)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/refresh", SessionCriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const remoteAddr = "192.0.2.77:12345"
	// 每次都用一个全新的、格式合法但完全伪造的 UUID
	forged := []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
		"44444444-4444-4444-8444-444444444444",
	}
	codes := make([]int, 0, len(forged))
	for _, sessionID := range forged {
		codes = append(codes, performCookieRequest(router, "/refresh", remoteAddr, sessionID+".s").Code)
	}

	assert.Equal(t, http.StatusNoContent, codes[0])
	assert.Equal(t, http.StatusNoContent, codes[1])
	assert.Equal(t, http.StatusTooManyRequests, codes[2],
		"换一个伪造 UUID 就绕过了 IP 配额")
	assert.Equal(t, http.StatusTooManyRequests, codes[3])
}

// 合法会话之间仍然互相隔离：IP 配额给足时，一个会话打满不影响另一个。
func TestSessionCriticalRateLimitStillIsolatesSessionsUnderGenerousIPBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useRateLimitMiniRedis(t)
	// 每会话 1 次，每 IP 100 次：会话桶先打满，IP 桶远未触及
	useCriticalRateLimitConfig(t, 100, 1, 100)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/refresh", SessionCriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const remoteAddr = "192.0.2.78:12345"
	const sessionA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const sessionB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	assert.Equal(t, http.StatusNoContent, performCookieRequest(router, "/refresh", remoteAddr, sessionA+".s").Code)
	assert.Equal(t, http.StatusTooManyRequests, performCookieRequest(router, "/refresh", remoteAddr, sessionA+".s").Code,
		"会话 A 应当打满自己的配额")
	assert.Equal(t, http.StatusNoContent, performCookieRequest(router, "/refresh", remoteAddr, sessionB+".s").Code,
		"会话 B 被会话 A 的配额挤占了，隔离失效")
}

// 无 cookie 的请求同样受 IP 桶约束。
func TestSessionCriticalRateLimitAppliesIPBudgetWithoutCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useRateLimitMiniRedis(t)
	useCriticalRateLimitConfig(t, 100, 100, 1)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/refresh", SessionCriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const remoteAddr = "192.0.2.79:12345"
	assert.Equal(t, http.StatusNoContent, performCookieRequest(router, "/refresh", remoteAddr, "").Code)
	assert.Equal(t, http.StatusTooManyRequests, performCookieRequest(router, "/refresh", remoteAddr, "").Code)
}

func TestSessionCriticalRateLimitFallsBackToIPWithoutUsableCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)
	useCriticalRateLimitConfig(t, 1, 100, 1)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/refresh", SessionCriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, testCase := range []struct {
		name        string
		remoteAddr  string
		cookieValue string
	}{
		{name: "no cookie", remoteAddr: "192.0.2.73:12345", cookieValue: ""},
		{name: "no secret separator", remoteAddr: "192.0.2.74:12345", cookieValue: "6f1b1f9c-6a1a-4f4a-9d3f-1a2b3c4d5e6f"},
		{name: "session id is not a uuid", remoteAddr: "192.0.2.75:12345", cookieValue: "not-a-uuid.secret"},
		{name: "empty secret", remoteAddr: "192.0.2.76:12345", cookieValue: "6f1b1f9c-6a1a-4f4a-9d3f-1a2b3c4d5e6f."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			host := testCase.remoteAddr[:len(testCase.remoteAddr)-6]
			assert.Equal(t, http.StatusNoContent, performCookieRequest(router, "/refresh", testCase.remoteAddr, testCase.cookieValue).Code)
			assert.Equal(t, http.StatusTooManyRequests, performCookieRequest(router, "/refresh", testCase.remoteAddr, testCase.cookieValue).Code)
			assert.True(t, redisServer.Exists(redisIPRateLimitKey(criticalSessionRateLimitMark, host)))
			assert.False(t, redisServer.Exists(redisIPRateLimitKey(criticalRateLimitMark, host)))
		})
	}
}

// TestCriticalBucketsDoNotShareAllowance locks in the split itself: draining
// every other critical bucket from one IP must leave the login bucket for that
// same IP untouched.
func TestCriticalBucketsDoNotShareAllowance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, _ = useRateLimitMiniRedis(t)
	useCriticalRateLimitConfig(t, 1, 1, 1)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/refresh", SessionCriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/pay",
		func(c *gin.Context) { c.Set("id", 91) },
		UserCriticalRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	router.POST("/usage", PublicQueryRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/login", CriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const remoteAddr = "192.0.2.78:12345"
	for _, path := range []string{"/refresh", "/pay", "/usage"} {
		assert.Equal(t, http.StatusNoContent, performCookieRequest(router, path, remoteAddr, "").Code, path)
		assert.Equal(t, http.StatusTooManyRequests, performCookieRequest(router, path, remoteAddr, "").Code, path)
	}

	assert.Equal(t, http.StatusNoContent, performCookieRequest(router, "/login", remoteAddr, "").Code,
		"login must still be available after every other critical bucket is drained")
	assert.Equal(t, http.StatusTooManyRequests, performCookieRequest(router, "/login", remoteAddr, "").Code)
}

// TestEveryRateLimiterUsesItsOwnCounter drives the constructed middlewares and
// compares the counters they actually touch. Checking a list of mark constants
// would not catch the real defect: a call site can hardcode a string and never
// reference its constant, so the list stays unique while two limiters share a
// counter — which is exactly how the login outage happened.
func TestEveryRateLimiterUsesItsOwnCounter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)
	useCriticalRateLimitConfig(t, 10, 10, 10)
	useGlobalRateLimitConfig(t, 10)

	const (
		remoteAddr  = "192.0.2.90:12345"
		sessionID   = "9a9a9a9a-9a9a-4a9a-8a9a-9a9a9a9a9a9a"
		cookieValue = sessionID + ".secret"
	)

	limiters := []struct {
		name    string
		handler func(c *gin.Context)
	}{
		{"GlobalWebRateLimit", GlobalWebRateLimit()},
		{"GlobalAPIRateLimit", GlobalAPIRateLimit()},
		{"CriticalRateLimit", CriticalRateLimit()},
		{"UserCriticalRateLimit", UserCriticalRateLimit()},
		{"SessionCriticalRateLimit", SessionCriticalRateLimit()},
		{"PublicQueryRateLimit", PublicQueryRateLimit()},
		{"DownloadRateLimit", DownloadRateLimit()},
		{"UploadRateLimit", UploadRateLimit()},
		{"SearchRateLimit", SearchRateLimit()},
		{"LogExportRateLimit", LogExportRateLimit()},
		{"EmailVerificationRateLimit", EmailVerificationRateLimit()},
		{"applyAPIUserRateLimit", applyAPIUserRateLimit},
	}

	claimedBy := make(map[string]string)
	for _, limiter := range limiters {
		before := make(map[string]bool)
		for _, key := range redisServer.Keys() {
			before[key] = true
		}

		router := gin.New()
		require.NoError(t, router.SetTrustedProxies(nil))
		router.POST("/limited",
			func(c *gin.Context) {
				// Every identity dimension a limiter might key on is the same
				// across limiters, so a shared mark produces a shared key.
				c.Set("id", 4242)
				c.Set(RouteTagKey, "api")
			},
			limiter.handler,
			func(c *gin.Context) { c.Status(http.StatusNoContent) },
		)
		response := performCookieRequest(router, "/limited", remoteAddr, cookieValue)
		require.Equal(t, http.StatusNoContent, response.Code, "%s rejected its first request", limiter.name)

		fresh := make([]string, 0, 1)
		for _, key := range redisServer.Keys() {
			if !before[key] {
				fresh = append(fresh, key)
			}
		}
		require.NotEmpty(t, fresh, "%s charged no counter, so it shares one with an earlier limiter", limiter.name)
		for _, key := range fresh {
			owner, taken := claimedBy[key]
			assert.False(t, taken, "%s and %s share counter %q", limiter.name, owner, key)
			claimedBy[key] = limiter.name
		}
	}
}

func TestRedisEmailVerificationRateLimiterPreservesResponseAndTTL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/verify", EmailVerificationRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	remoteAddr := "192.0.2.30:12345"
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/verify", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/verify", remoteAddr).Code)
	response := performRateLimitRequest(router, "/verify", remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.JSONEq(t, `{"success":false,"message":"发送过于频繁，请等待 30 秒后再试"}`, response.Body.String())

	key := redisIPRateLimitKey(EmailVerificationRateLimitMark, "192.0.2.30")
	assert.True(t, redisServer.Exists(key))
	assert.Equal(t, time.Duration(EmailVerificationDuration)*time.Second, redisServer.TTL(key))
}

func TestRedisFixedWindowIsAtomicUnderConcurrency(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const (
		requestCount = 20
		maximumCount = 7
		duration     = int64(41)
	)
	key := redisIPRateLimitKey("CONCURRENT", "192.0.2.40")

	var allowedCount atomic.Int64
	errorsFound := make(chan error, requestCount)
	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)
	for range requestCount {
		go func() {
			defer waitGroup.Done()
			allowed, _, _, err := redisFixedWindowTake(context.Background(), key, maximumCount, duration)
			if err != nil {
				errorsFound <- err
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}

	assert.Equal(t, int64(maximumCount), allowedCount.Load())
	count, err := redisServer.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "20", count)
	assert.Equal(t, time.Duration(duration)*time.Second, redisServer.TTL(key))
}

func TestRedisFixedWindowResetsAtBoundary(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const duration = int64(10)
	key := redisIPRateLimitKey("BOUNDARY", "192.0.2.50")

	for range 2 {
		allowed, _, _, err := redisFixedWindowTake(context.Background(), key, 2, duration)
		require.NoError(t, err)
		assert.True(t, allowed)
	}
	allowed, _, _, err := redisFixedWindowTake(context.Background(), key, 2, duration)
	require.NoError(t, err)
	assert.False(t, allowed)

	// This reset is intentional fixed-window behavior. A client can consume one
	// full allowance immediately before and another immediately after a boundary.
	redisServer.FastForward(time.Duration(duration) * time.Second)
	for range 2 {
		allowed, _, _, err = redisFixedWindowTake(context.Background(), key, 2, duration)
		require.NoError(t, err)
		assert.True(t, allowed)
	}
}

func TestRedisFixedWindowRepairsCounterWithoutTTL(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const duration = int64(29)
	key := redisIPRateLimitKey("MISSING-TTL", "192.0.2.51")
	redisServer.Set(key, "5")

	allowed, count, ttl, err := redisFixedWindowTake(context.Background(), key, 3, duration)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, int64(6), count)
	assert.Equal(t, duration, ttl)
	assert.Equal(t, time.Duration(duration)*time.Second, redisServer.TTL(key))

	redisServer.FastForward(time.Duration(duration) * time.Second)
	assert.False(t, redisServer.Exists(key), "a recovered counter must not remain permanently rate-limited")
}

// A rate limiter exists to shed excess load. Failing the request when Redis is
// unreachable turned a bounded problem into a dashboard-wide outage, because
// GlobalAPIRateLimit sits on the whole /api group and applyAPIUserRateLimit runs
// inside authHelper — one Redis blip 500'd every authenticated request. Each
// limiter must instead degrade to the per-instance in-memory counter, which
// keeps enforcement alive rather than failing open.
func TestRedisFailureDegradesToInMemoryInsteadOfFailingRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, redisClient := useRateLimitMiniRedis(t)
	require.NoError(t, redisClient.Close())

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/ip", rateLimitFactory(staticBucket(1, 30), "FAIL-IP"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET(
		"/user",
		func(c *gin.Context) { c.Set("id", 7) },
		userRateLimitFactory(staticBucket(1, 30), "FAIL-USER"),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	router.GET("/email", EmailVerificationRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, testCase := range []struct{ path, remoteAddr string }{
		{"/ip", "192.0.2.60:12345"},
		{"/user", "192.0.2.61:12345"},
	} {
		// Served, not 500'd...
		assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, testCase.path, testCase.remoteAddr).Code,
			"%s should be served from the in-memory fallback", testCase.path)
		// ...and still counted, so the outage does not disable enforcement.
		limited := performRateLimitRequest(router, testCase.path, testCase.remoteAddr)
		assert.Equal(t, http.StatusTooManyRequests, limited.Code,
			"%s must still enforce its allowance while Redis is down", testCase.path)
		assert.Contains(t, limited.Body.String(), `"success":false`)
	}

	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/email", "192.0.2.62:12345").Code)
}

func TestShouldLogRateLimitDegradationSamplesWithoutPerKeyState(t *testing.T) {
	rateLimitDegradationEvents.Store(0)
	t.Cleanup(func() { rateLimitDegradationEvents.Store(0) })

	count, logEvent := shouldLogRateLimitDegradation()
	assert.Equal(t, uint64(1), count)
	assert.True(t, logEvent)

	for range rateLimitDegradationLogEvery - 2 {
		_, logEvent = shouldLogRateLimitDegradation()
		assert.False(t, logEvent)
	}

	count, logEvent = shouldLogRateLimitDegradation()
	assert.Equal(t, uint64(rateLimitDegradationLogEvery), count)
	assert.True(t, logEvent)
}
