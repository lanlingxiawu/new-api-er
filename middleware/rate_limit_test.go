package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// runLimiter installs the limiter + a terminal handler on a real engine and
// issues n sequential requests from the same client IP, returning the status
// code of each request.
func runLimiter(t *testing.T, limiter gin.HandlerFunc, n int, setup func(c *gin.Context)) []int {
	t.Helper()
	r := gin.New()
	handlers := []gin.HandlerFunc{}
	if setup != nil {
		handlers = append(handlers, setup)
	}
	handlers = append(handlers, limiter, func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/lim", handlers...)

	codes := make([]int, 0, n)
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/lim", nil)
		req.RemoteAddr = "203.0.113.7:12345" // stable client IP
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	return codes
}

// ---------------------------------------------------------------------------
// memory backend — rateLimitFactory / memoryRateLimiter
// ---------------------------------------------------------------------------

func TestMemoryRateLimiter_UnderLimitAllows(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })

	lim := rateLimitFactory(3, 60, uniq("MEMU"))
	codes := runLimiter(t, lim, 3, nil)
	for _, c := range codes {
		require.Equal(t, http.StatusOK, c)
	}
}

func TestMemoryRateLimiter_OverLimitRejects(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })

	lim := rateLimitFactory(2, 60, uniq("MEMO"))
	codes := runLimiter(t, lim, 4, nil)
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusOK, codes[1])
	require.Equal(t, http.StatusTooManyRequests, codes[2])
	require.Equal(t, http.StatusTooManyRequests, codes[3])
}

// ---------------------------------------------------------------------------
// redis backend — rateLimitFactory / redisRateLimiter
// ---------------------------------------------------------------------------

func TestRedisRateLimiter_OverLimitRejects(t *testing.T) {
	enableRedis(t)
	lim := rateLimitFactory(2, 60, uniq("REDO"))
	codes := runLimiter(t, lim, 4, nil)
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusOK, codes[1])
	require.Equal(t, http.StatusTooManyRequests, codes[2])
}

func TestRedisRateLimiter_UnderLimitAllows(t *testing.T) {
	enableRedis(t)
	lim := rateLimitFactory(5, 60, uniq("REDU"))
	codes := runLimiter(t, lim, 3, nil)
	for _, c := range codes {
		require.Equal(t, http.StatusOK, c)
	}
}

// ---------------------------------------------------------------------------
// user-keyed limiter — userRateLimitFactory
// ---------------------------------------------------------------------------

func TestUserRateLimiter_MemoryUnauthenticated(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })

	lim := userRateLimitFactory(5, 60, uniq("URU"))
	// id == 0 -> 401.
	codes := runLimiter(t, lim, 1, nil)
	require.Equal(t, http.StatusUnauthorized, codes[0])
}

func TestUserRateLimiter_MemoryOverLimit(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })

	lim := userRateLimitFactory(2, 60, uniq("URM"))
	codes := runLimiter(t, lim, 3, func(c *gin.Context) { c.Set("id", 5551) })
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusOK, codes[1])
	require.Equal(t, http.StatusTooManyRequests, codes[2])
}

func TestUserRateLimiter_RedisUnauthenticated(t *testing.T) {
	enableRedis(t)
	lim := userRateLimitFactory(5, 60, uniq("URRU"))
	codes := runLimiter(t, lim, 1, nil)
	require.Equal(t, http.StatusUnauthorized, codes[0])
}

func TestUserRateLimiter_RedisOverLimit(t *testing.T) {
	enableRedis(t)
	lim := userRateLimitFactory(2, 60, uniq("URR"))
	uid := nextTestID()
	codes := runLimiter(t, lim, 3, func(c *gin.Context) { c.Set("id", uid) })
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusOK, codes[1])
	require.Equal(t, http.StatusTooManyRequests, codes[2])
}

// ---------------------------------------------------------------------------
// factory selectors (enabled/disabled config)
// ---------------------------------------------------------------------------

func TestGlobalWebRateLimit_DisabledIsPassthrough(t *testing.T) {
	prev := common.GlobalWebRateLimitEnable
	common.GlobalWebRateLimitEnable = false
	t.Cleanup(func() { common.GlobalWebRateLimitEnable = prev })
	// defNext -> always allow.
	codes := runLimiter(t, GlobalWebRateLimit(), 3, nil)
	for _, c := range codes {
		require.Equal(t, http.StatusOK, c)
	}
}

func TestGlobalAPIRateLimit_DisabledIsPassthrough(t *testing.T) {
	prev := common.GlobalApiRateLimitEnable
	common.GlobalApiRateLimitEnable = false
	t.Cleanup(func() { common.GlobalApiRateLimitEnable = prev })
	codes := runLimiter(t, GlobalAPIRateLimit(), 2, nil)
	require.Equal(t, http.StatusOK, codes[0])
}

func TestCriticalRateLimit_DisabledIsPassthrough(t *testing.T) {
	prev := common.CriticalRateLimitEnable
	common.CriticalRateLimitEnable = false
	t.Cleanup(func() { common.CriticalRateLimitEnable = prev })
	codes := runLimiter(t, CriticalRateLimit(), 2, nil)
	require.Equal(t, http.StatusOK, codes[0])
}

func TestSearchRateLimit_DisabledIsPassthrough(t *testing.T) {
	prev := common.SearchRateLimitEnable
	common.SearchRateLimitEnable = false
	t.Cleanup(func() { common.SearchRateLimitEnable = prev })
	codes := runLimiter(t, SearchRateLimit(), 2, nil)
	require.Equal(t, http.StatusOK, codes[0])
}

func TestLogExportRateLimit_DisabledIsPassthrough(t *testing.T) {
	prev := common.LogExportRateLimitEnable
	common.LogExportRateLimitEnable = false
	t.Cleanup(func() { common.LogExportRateLimitEnable = prev })
	codes := runLimiter(t, LogExportRateLimit(), 2, nil)
	require.Equal(t, http.StatusOK, codes[0])
}

func TestSearchRateLimit_EnabledPerUser(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	prevEn := common.SearchRateLimitEnable
	prevNum := common.SearchRateLimitNum
	prevDur := common.SearchRateLimitDuration
	common.SearchRateLimitEnable = true
	common.SearchRateLimitNum = 1
	common.SearchRateLimitDuration = 60
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.SearchRateLimitEnable = prevEn
		common.SearchRateLimitNum = prevNum
		common.SearchRateLimitDuration = prevDur
	})
	codes := runLimiter(t, SearchRateLimit(), 2, func(c *gin.Context) { c.Set("id", 6661) })
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusTooManyRequests, codes[1])
}

func TestDownloadUploadRateLimitFactoriesBuild(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })
	require.NotNil(t, DownloadRateLimit())
	require.NotNil(t, UploadRateLimit())
}

func TestGlobalWebRateLimit_EnabledBuildsLimiter(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	prev := common.GlobalWebRateLimitEnable
	prevNum := common.GlobalWebRateLimitNum
	prevDur := common.GlobalWebRateLimitDuration
	common.GlobalWebRateLimitEnable = true
	common.GlobalWebRateLimitNum = 1
	common.GlobalWebRateLimitDuration = 60
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.GlobalWebRateLimitEnable = prev
		common.GlobalWebRateLimitNum = prevNum
		common.GlobalWebRateLimitDuration = prevDur
	})
	codes := runLimiter(t, GlobalWebRateLimit(), 2, nil)
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusTooManyRequests, codes[1])
}

func TestGlobalAPIRateLimit_EnabledBuildsLimiter(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	prev := common.GlobalApiRateLimitEnable
	prevNum := common.GlobalApiRateLimitNum
	prevDur := common.GlobalApiRateLimitDuration
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum = 1
	common.GlobalApiRateLimitDuration = 60
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.GlobalApiRateLimitEnable = prev
		common.GlobalApiRateLimitNum = prevNum
		common.GlobalApiRateLimitDuration = prevDur
	})
	codes := runLimiter(t, GlobalAPIRateLimit(), 2, nil)
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusTooManyRequests, codes[1])
}

func TestCriticalRateLimit_EnabledBuildsLimiter(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	prev := common.CriticalRateLimitEnable
	prevNum := common.CriticalRateLimitNum
	prevDur := common.CriticalRateLimitDuration
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = 1
	common.CriticalRateLimitDuration = 60
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.CriticalRateLimitEnable = prev
		common.CriticalRateLimitNum = prevNum
		common.CriticalRateLimitDuration = prevDur
	})
	codes := runLimiter(t, CriticalRateLimit(), 2, nil)
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusTooManyRequests, codes[1])
}

func TestLogExportRateLimit_EnabledPerUser(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	prevEn := common.LogExportRateLimitEnable
	prevNum := common.LogExportRateLimitNum
	prevDur := common.LogExportRateLimitDuration
	common.LogExportRateLimitEnable = true
	common.LogExportRateLimitNum = 1
	common.LogExportRateLimitDuration = 600
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.LogExportRateLimitEnable = prevEn
		common.LogExportRateLimitNum = prevNum
		common.LogExportRateLimitDuration = prevDur
	})
	codes := runLimiter(t, LogExportRateLimit(), 2, func(c *gin.Context) { c.Set("id", 7771) })
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusTooManyRequests, codes[1])
}
