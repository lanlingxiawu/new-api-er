package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

// userRateLimitScript 原子执行 LLEN+判断+写入，避免并发下的 TOCTOU 竞态。
// KEYS[1]=key  ARGV: 1=max 2=duration(秒) 3=now(unix秒) 4=expire(秒)
// 返回 1 放行，0 拒绝。语义与原 userRedisRateLimiter 完全一致：
// 列表头为最新、尾为最旧，容量为 max；窗口内最旧一次距今不足 duration 则拒绝。
var userRateLimitScript = redis.NewScript(`
local max = tonumber(ARGV[1])
local duration = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local expire = tonumber(ARGV[4])
local length = redis.call('LLEN', KEYS[1])
if length < max then
    redis.call('LPUSH', KEYS[1], now)
    redis.call('EXPIRE', KEYS[1], expire)
    return 1
end
-- tonumber 包在外层：旧版本可能写入过非数值(时间字符串)，此时视为 nil 放行，
-- 避免对 nil 做算术报错；旧值会被随后的 LTRIM 逐步挤出。
local oldest = tonumber(redis.call('LINDEX', KEYS[1], -1))
if oldest and (now - oldest) < duration then
    redis.call('EXPIRE', KEYS[1], expire)
    return 0
end
redis.call('LPUSH', KEYS[1], now)
redis.call('LTRIM', KEYS[1], 0, max - 1)
redis.call('EXPIRE', KEYS[1], expire)
return 1
`)

var timeFormat = "2006-01-02T15:04:05.000Z"

var inMemoryRateLimiter common.InMemoryRateLimiter

var defNext = func(c *gin.Context) {
	c.Next()
}

func redisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	ctx := context.Background()
	rdb := common.RDB
	key := "rateLimit:" + mark + c.ClientIP()
	listLength, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		fmt.Println(err.Error())
		c.Status(http.StatusInternalServerError)
		c.Abort()
		return
	}
	if listLength < int64(maxRequestNum) {
		rdb.LPush(ctx, key, time.Now().Format(timeFormat))
		rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
	} else {
		oldTimeStr, _ := rdb.LIndex(ctx, key, -1).Result()
		oldTime, err := time.Parse(timeFormat, oldTimeStr)
		if err != nil {
			fmt.Println(err)
			c.Status(http.StatusInternalServerError)
			c.Abort()
			return
		}
		nowTimeStr := time.Now().Format(timeFormat)
		nowTime, err := time.Parse(timeFormat, nowTimeStr)
		if err != nil {
			fmt.Println(err)
			c.Status(http.StatusInternalServerError)
			c.Abort()
			return
		}
		// time.Since will return negative number!
		// See: https://stackoverflow.com/questions/50970900/why-is-time-since-returning-negative-durations-on-windows
		if int64(nowTime.Sub(oldTime).Seconds()) < duration {
			rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		} else {
			rdb.LPush(ctx, key, time.Now().Format(timeFormat))
			rdb.LTrim(ctx, key, 0, int64(maxRequestNum-1))
			rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
		}
	}
}

func memoryRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	key := mark + c.ClientIP()
	if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
		c.Status(http.StatusTooManyRequests)
		c.Abort()
		return
	}
}

func rateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			redisRateLimiter(c, maxRequestNum, duration, mark)
		}
	} else {
		// It's safe to call multi times.
		inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
		return func(c *gin.Context) {
			memoryRateLimiter(c, maxRequestNum, duration, mark)
		}
	}
}

func GlobalWebRateLimit() func(c *gin.Context) {
	if common.GlobalWebRateLimitEnable {
		return rateLimitFactory(common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration, "GW")
	}
	return defNext
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	if common.GlobalApiRateLimitEnable {
		return rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "GA")
	}
	return defNext
}

func CriticalRateLimit() func(c *gin.Context) {
	if common.CriticalRateLimitEnable {
		return rateLimitFactory(common.CriticalRateLimitNum, common.CriticalRateLimitDuration, "CT")
	}
	return defNext
}

func DownloadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.DownloadRateLimitNum, common.DownloadRateLimitDuration, "DW")
}

func UploadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.UploadRateLimitNum, common.UploadRateLimitDuration, "UP")
}

// userRateLimitFactory creates a rate limiter keyed by authenticated user ID
// instead of client IP, making it resistant to proxy rotation attacks.
// Must be used AFTER authentication middleware (UserAuth).
func userRateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			userId := c.GetInt("id")
			if userId == 0 {
				c.Status(http.StatusUnauthorized)
				c.Abort()
				return
			}
			key := fmt.Sprintf("rateLimit:%s:user:%d", mark, userId)
			userRedisRateLimiter(c, maxRequestNum, duration, key)
		}
	}
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		userId := c.GetInt("id")
		if userId == 0 {
			c.Status(http.StatusUnauthorized)
			c.Abort()
			return
		}
		key := fmt.Sprintf("%s:user:%d", mark, userId)
		if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}
	}
}

// userRedisRateLimiter is like redisRateLimiter but accepts a pre-built key
// (to support user-ID-based keys). The check-and-update is executed as a single
// atomic Lua script to avoid TOCTOU races under concurrent requests.
func userRedisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, key string) {
	ctx := context.Background()
	expire := int64(common.RateLimitKeyExpirationDuration.Seconds())
	allowed, err := userRateLimitScript.Run(
		ctx, common.RDB, []string{key},
		maxRequestNum, duration, time.Now().Unix(), expire,
	).Int()
	if err != nil {
		fmt.Println(err.Error())
		c.Status(http.StatusInternalServerError)
		c.Abort()
		return
	}
	if allowed != 1 {
		c.Status(http.StatusTooManyRequests)
		c.Abort()
		return
	}
}

// SearchRateLimit returns a per-user rate limiter for search endpoints.
// Configurable via SEARCH_RATE_LIMIT_ENABLE / SEARCH_RATE_LIMIT / SEARCH_RATE_LIMIT_DURATION.
func SearchRateLimit() func(c *gin.Context) {
	if !common.SearchRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(common.SearchRateLimitNum, common.SearchRateLimitDuration, "SR")
}

// LogExportRateLimit returns a per-user rate limiter for log export endpoints.
// Default: 1 request per 10 minutes per user.
// Must be used AFTER authentication middleware (UserAuth/AdminAuth).
// Configurable via LOG_EXPORT_RATE_LIMIT_ENABLE / LOG_EXPORT_RATE_LIMIT / LOG_EXPORT_RATE_LIMIT_DURATION.
func LogExportRateLimit() func(c *gin.Context) {
	if !common.LogExportRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(common.LogExportRateLimitNum, common.LogExportRateLimitDuration, "LE")
}
