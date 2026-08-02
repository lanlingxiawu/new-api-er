package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

const redisRateLimitNamespace = "rateLimit:v2"

const rateLimitDegradationLogEvery uint64 = 1000

var rateLimitDegradationEvents atomic.Uint64

func shouldLogRateLimitDegradation() (uint64, bool) {
	count := rateLimitDegradationEvents.Add(1)
	return count, count == 1 || count%rateLimitDegradationLogEvery == 0
}

// Critical actions are split across three buckets so one class of traffic
// cannot starve another. They all used to share the single IP-keyed "CT"
// bucket, which meant the periodic token refreshes of a handful of already
// logged-in users behind one NAT egress consumed the whole allowance and
// nobody at that office could log in until the window expired.
const (
	// criticalRateLimitMark covers the anonymous authentication surface
	// (login, register, password reset, OAuth entry). Keyed by client IP —
	// there is no identity yet, so this is the brute-force backstop.
	criticalRateLimitMark = "CT"
	// criticalUserRateLimitMark covers sensitive actions that run after
	// authentication (payments, credential reveal, profile update, OAuth
	// binding). Keyed by user ID so co-located users stay independent.
	criticalUserRateLimitMark = "CTU"
	// criticalSessionRateLimitMark covers the refresh-cookie endpoints, keyed
	// by login session ID.
	criticalSessionRateLimitMark = "CTS"
	// publicQueryRateLimitMark covers read endpoints that authenticate — if at
	// all — only after this middleware, so they can just be keyed by client IP.
	// The separate mark keeps a polling script from locking its own office out
	// of the dashboard.
	publicQueryRateLimitMark = "PQ"
)

// Marks for the remaining limiters. Every mark in this package must be unique:
// two limiters sharing a mark share a counter, which is exactly how the login
// outage above happened. TestEveryRateLimiterUsesItsOwnCounter enforces that by
// running the constructed middlewares and comparing the counters they touch —
// checking a list of constants would not, since a call site can hardcode a
// string and never reference its constant.
const (
	globalWebRateLimitMark     = "GW"
	globalAPIRateLimitMark     = "GA"
	globalAPIUserRateLimitMark = "GAU"
	downloadRateLimitMark      = "DW"
	uploadRateLimitMark        = "UP"
	searchRateLimitMark        = "SR"
	logExportRateLimitMark     = "LE"
)

// Redis rate limiting intentionally uses a fixed window. The single Lua script
// makes increment, expiry, and the limit decision atomic, while retaining the
// simple fixed-window behavior: traffic at a window boundary can burst up to
// twice the configured limit. Do not replace this with a sliding-window ZSET
// unless that externally visible behavior is intentionally changed.
const redisFixedWindowScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
  ttl = redis.call('TTL', KEYS[1])
end
if count > tonumber(ARGV[1]) then
  return {0, count, ttl}
end
return {1, count, ttl}
`

var inMemoryRateLimiter common.InMemoryRateLimiter

func redisIPRateLimitKey(mark string, clientIP string) string {
	return fmt.Sprintf("%s:ip:%s:%s", redisRateLimitNamespace, mark, clientIP)
}

func redisUserRateLimitKey(mark string, userID int) string {
	return fmt.Sprintf("%s:user:%s:%d", redisRateLimitNamespace, mark, userID)
}

func redisSessionRateLimitKey(mark string, sessionID string) string {
	return fmt.Sprintf("%s:session:%s:%s", redisRateLimitNamespace, mark, sessionID)
}

func redisReplyInteger(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer reply type %T", value)
	}
}

// redisFixedWindowTake 自带超时预算，所以每个调用方都自动受保护，
// 将来新增的调用方也不会漏掉。
//
// 限流是一条可降级的旁路：Redis 不可用时降级到本地内存计数即可，请求不该跟着
// go-redis 的默认超时一起等下去。本项目只覆盖了 PoolSize，其余用 go-redis 默认值
// （DialTimeout 5s、ReadTimeout 3s、MaxRetries 3），而 gin 的请求 context 没有截止
// 时间——实测「Redis 连得上但永不响应」时单次调用要 12.08s，一个已认证的 /api 请求
// 含两次限流调用就是 24s。加上这个预算之后，最坏情况由配置说了算。
func redisFixedWindowTake(ctx context.Context, key string, maxRequestNum int, duration int64) (bool, int64, int64, error) {
	if common.RDB == nil {
		return false, 0, 0, errors.New("Redis client is not initialized")
	}
	if budget := operation_setting.GetRateLimitSnapshot().RedisTimeout; budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}
	if key == "" {
		return false, 0, 0, errors.New("rate limit key is empty")
	}
	if maxRequestNum <= 0 {
		return false, 0, 0, errors.New("rate limit maximum must be positive")
	}
	if duration <= 0 {
		return false, 0, 0, errors.New("rate limit duration must be positive")
	}

	values, err := common.RDB.Eval(
		ctx,
		redisFixedWindowScript,
		[]string{key},
		maxRequestNum,
		duration,
	).Slice()
	if err != nil {
		return false, 0, 0, err
	}
	if len(values) != 3 {
		return false, 0, 0, fmt.Errorf("unexpected Redis rate limit reply length %d", len(values))
	}

	allowedValue, err := redisReplyInteger(values[0])
	if err != nil {
		return false, 0, 0, err
	}
	count, err := redisReplyInteger(values[1])
	if err != nil {
		return false, 0, 0, err
	}
	ttlSeconds, err := redisReplyInteger(values[2])
	if err != nil {
		return false, 0, 0, err
	}

	return allowedValue == 1, count, ttlSeconds, nil
}

// takeRateLimit charges one request against an already-built key. The key
// carries its own namespace, so the same string is safe to use for both
// backends.
//
// When Redis is unreachable the limiter degrades to the per-instance in-memory
// counter instead of failing the request. A rate limiter exists to shed excess
// load; turning a Redis blip into a 500 on every authenticated /api request
// trades a bounded problem for a full dashboard outage. Degrading rather than
// simply allowing keeps brute-force protection alive — at N times the
// configured limit for N instances, which is the right trade during an outage.
func takeRateLimit(c *gin.Context, maxRequestNum int, duration int64, key string) {
	if common.RedisEnabled {
		allowed, _, ttlSeconds, err := redisFixedWindowTake(c.Request.Context(), key, maxRequestNum, duration)
		if err == nil {
			if !allowed {
				writeRateLimited(c, ttlSeconds)
			}
			return
		}
		if degradationCount, shouldLog := shouldLogRateLimitDegradation(); shouldLog {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"rate limit degraded to in-memory (events=%d): %v",
				degradationCount,
				err,
			))
		}
	}
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
		writeRateLimited(c, duration)
	}
}

// writeRateLimited rejects the request with 429 and a Retry-After hint so
// clients can back off instead of treating the rejection as a fatal error.
// The in-memory limiter cannot report the remaining window, so callers
// without a TTL pass the full window duration as a conservative upper bound.
func writeRateLimited(c *gin.Context, retryAfterSeconds int64) {
	if retryAfterSeconds > 0 {
		c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"success": false,
		"message": i18n.T(c, i18n.MsgRateLimitTooManyRequests, map[string]any{
			"Seconds": retryAfterSeconds,
		}),
	})
}

// apiUserRateLimit applies the authenticated-user half of the dashboard API
// limiter. The route tag guard is mandatory: UserAuth/AdminAuth are also used
// outside /api, and rate limiting those paths here could affect relay traffic.
func apiUserRateLimit(c *gin.Context, maxRequestNum int, duration int64) {
	if c.GetString(RouteTagKey) != "api" {
		return
	}
	userID := c.GetInt("id")
	if userID == 0 {
		return
	}
	takeRateLimit(c, maxRequestNum, duration, redisUserRateLimitKey(globalAPIUserRateLimitMark, userID))
}

func applyAPIUserRateLimit(c *gin.Context) {
	bucket := operation_setting.GetRateLimitSnapshot().GlobalAPIUser
	if !bucket.Enabled {
		return
	}
	apiUserRateLimit(c, bucket.Num, bucket.Duration)
}

type bucketProvider func() operation_setting.RateLimitBucket

func rateLimitFactory(provider bucketProvider, mark string) func(c *gin.Context) {
	// It's safe to call multi times. Keep the fallback ready before requests
	// arrive so a Redis outage cannot race its first initialization.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		bucket := provider()
		if !bucket.Enabled {
			return
		}
		takeRateLimit(c, bucket.Num, bucket.Duration, redisIPRateLimitKey(mark, c.ClientIP()))
	}
}

func snapshotBucket(get func(*operation_setting.RateLimitSnapshot) operation_setting.RateLimitBucket) bucketProvider {
	return func() operation_setting.RateLimitBucket { return get(operation_setting.GetRateLimitSnapshot()) }
}
func staticBucket(num int, duration int64) bucketProvider {
	return func() operation_setting.RateLimitBucket {
		return operation_setting.RateLimitBucket{Enabled: true, Num: num, Duration: duration}
	}
}

func GlobalWebRateLimit() func(c *gin.Context) {
	return rateLimitFactory(snapshotBucket(func(s *operation_setting.RateLimitSnapshot) operation_setting.RateLimitBucket { return s.GlobalWeb }), globalWebRateLimitMark)
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	return rateLimitFactory(snapshotBucket(func(s *operation_setting.RateLimitSnapshot) operation_setting.RateLimitBucket { return s.GlobalAPI }), globalAPIRateLimitMark)
}

// CriticalRateLimit guards the anonymous authentication surface. Only routes
// that genuinely have no caller identity belong here — anything mounted after
// UserAuth/AdminAuth must use UserCriticalRateLimit instead, or it will consume
// the login allowance shared by everyone behind the same egress IP.
func CriticalRateLimit() func(c *gin.Context) {
	return rateLimitFactory(snapshotBucket(func(s *operation_setting.RateLimitSnapshot) operation_setting.RateLimitBucket { return s.Critical }), criticalRateLimitMark)
}

// UserCriticalRateLimit guards sensitive actions that run after authentication.
// Must be mounted AFTER UserAuth/AdminAuth/RootAuth. A request that somehow
// arrives without an identity falls back to an IP-keyed bucket under the same
// mark, which still keeps it out of the login bucket.
func UserCriticalRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		bucket := operation_setting.GetRateLimitSnapshot().Critical
		if !bucket.Enabled {
			return
		}
		key := redisIPRateLimitKey(criticalUserRateLimitMark, c.ClientIP())
		if userID := c.GetInt("id"); userID != 0 {
			key = redisUserRateLimitKey(criticalUserRateLimitMark, userID)
		}
		takeRateLimit(c, bucket.Num, bucket.Duration, key)
	}
}

// SessionCriticalRateLimit guards the refresh-cookie endpoints. Those run
// before authentication, so the user ID is unknown, but the refresh cookie
// carries a login session ID that survives token rotation — keying on it
// isolates one browser session from every other session behind the same egress
// IP，这是拆桶要解决的核心问题。
//
// 每个请求都要先付一份**按 IP** 的配额，带可用会话 ID 的再额外付一份按会话的。
//
// 早先的版本只对"没有可用 cookie"的请求计 IP 桶，理由是"带有效会话的请求不该被
// 同一出口 IP 的其他人挤占"。那个推理有个致命前提错误：**这里根本判定不了会话
// 是否有效**。requestLoginSessionID 走的是 splitRefreshToken，它只切分
// `sid.secret` 并检查 sid 能否 uuid.Parse——不验签、不查库。也就是说随手编一个
// 随机 UUID 就能拿到一个私有计数器，每次换一个就永远打不满，专用 IP 桶被完全绕过。
// 剩下的只有全局 API 桶（默认 2000/180s ≈ 11.1 次每秒），反而比合法降级路径的
// 600/1200s ≈ 0.5 次每秒宽 22 倍——保护关系是反的。
//
// 现在无条件计 IP 桶把伪造成本重新压回 AuthRefreshIpRateLimitNum。这不会重演当初
// 的挤兑事故：那次是 20 次/20 分钟被所有关键操作共享，而这里是 600 次/20 分钟且
// 只服务 refresh/logout 两个端点。access token 15 分钟过期，一个常驻页签在 20 分钟
// 窗口内约刷新 1~2 次，600 的余量足够几百人共用一个出口。
//
// 真正的验签在 handler 里（RefreshLoginSession 要比对库里的哈希），限流层不重复做——
// 那需要一次查库，正是限流要挡在前面的开销。
func SessionCriticalRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		bucket := operation_setting.GetRateLimitSnapshot().AuthRefresh
		if !bucket.Enabled {
			return
		}
		// 先收 IP 份额：无论 cookie 真假都要付，伪造 UUID 也绕不过去。
		takeRateLimit(
			c,
			bucket.IPNum,
			bucket.Duration,
			redisIPRateLimitKey(criticalSessionRateLimitMark, c.ClientIP()),
		)
		if c.IsAborted() {
			return
		}
		if sessionID, ok := requestLoginSessionID(c); ok {
			takeRateLimit(
				c,
				bucket.Num,
				bucket.Duration,
				redisSessionRateLimitKey(criticalSessionRateLimitMark, sessionID),
			)
		}
		// 没有可用 cookie 的请求到这里已经付过 IP 份额，无需再收一次。
	}
}

// requestLoginSessionID 从 refresh cookie 里取出登录会话 ID。
//
// 注意：service.RefreshTokenSID 只保证取出来的值是合法 UUID（因此可以安全地拼进
// 缓存键），**不保证这个会话真实存在**——它不验签也不查库。所以这个返回值只能用来
// 做"同一浏览器会话之间互相隔离"，绝不能当成身份凭证；伪造成本由上面无条件收取的
// IP 桶兜底。真正的校验在 RefreshLoginSession 里。
func requestLoginSessionID(c *gin.Context) (string, bool) {
	rawRefreshToken, err := c.Cookie(service.RefreshCookieName)
	if err != nil || rawRefreshToken == "" {
		return "", false
	}
	sessionID, ok := service.RefreshTokenSID(rawRefreshToken)
	if !ok || sessionID == "" {
		return "", false
	}
	return sessionID, true
}

// PublicQueryRateLimit guards public config reads and the API-key-authenticated
// usage/log query endpoints. Authentication happens after this middleware (or
// not at all), so it stays keyed by client IP; the point of the separate mark
// is only to keep automated polling from consuming the login allowance.
func PublicQueryRateLimit() func(c *gin.Context) {
	return rateLimitFactory(snapshotBucket(func(s *operation_setting.RateLimitSnapshot) operation_setting.RateLimitBucket { return s.Critical }), publicQueryRateLimitMark)
}

func DownloadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(staticBucket(common.DownloadRateLimitNum, common.DownloadRateLimitDuration), downloadRateLimitMark)
}

func UploadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(staticBucket(common.UploadRateLimitNum, common.UploadRateLimitDuration), uploadRateLimitMark)
}

// userRateLimitFactory creates a rate limiter keyed by authenticated user ID
// instead of client IP, making it resistant to proxy rotation attacks.
// Must be used AFTER authentication middleware (UserAuth).
func userRateLimitFactory(provider bucketProvider, mark string) func(c *gin.Context) {
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		bucket := provider()
		if !bucket.Enabled {
			return
		}
		userID := c.GetInt("id")
		if userID == 0 {
			c.Status(http.StatusUnauthorized)
			c.Abort()
			return
		}
		takeRateLimit(c, bucket.Num, bucket.Duration, redisUserRateLimitKey(mark, userID))
	}
}

// SearchRateLimit returns a per-user rate limiter for search endpoints.
// Configurable via SEARCH_RATE_LIMIT_ENABLE / SEARCH_RATE_LIMIT / SEARCH_RATE_LIMIT_DURATION.
func SearchRateLimit() func(c *gin.Context) {
	return userRateLimitFactory(snapshotBucket(func(s *operation_setting.RateLimitSnapshot) operation_setting.RateLimitBucket { return s.Search }), searchRateLimitMark)
}

// LogExportRateLimit returns a per-user rate limiter for log export endpoints.
// Default: 1 request per 10 minutes per user.
// Must be used AFTER authentication middleware (UserAuth/AdminAuth).
// Configurable via LOG_EXPORT_RATE_LIMIT_ENABLE / LOG_EXPORT_RATE_LIMIT / LOG_EXPORT_RATE_LIMIT_DURATION.
func LogExportRateLimit() func(c *gin.Context) {
	return userRateLimitFactory(snapshotBucket(func(s *operation_setting.RateLimitSnapshot) operation_setting.RateLimitBucket { return s.LogExport }), logExportRateLimitMark)
}
