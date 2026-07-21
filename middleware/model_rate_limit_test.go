package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// checkRedisRateLimit / recordRedisRequest — direct
// ---------------------------------------------------------------------------

func TestCheckRedisRateLimit_ZeroMaxUnlimited(t *testing.T) {
	enableRedis(t)
	ok, err := checkRedisRateLimit(context.Background(), common.RDB, uniq("MRLzero"), 0, 60)
	require.NoError(t, err)
	require.True(t, ok) // maxCount==0 -> unlimited
}

func TestCheckRedisRateLimit_UnderLimit(t *testing.T) {
	enableRedis(t)
	key := "rateLimit:" + uniq("MRLunder")
	ok, err := checkRedisRateLimit(context.Background(), common.RDB, key, 3, 60)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestCheckRedisRateLimit_OverLimitWithinWindow(t *testing.T) {
	enableRedis(t)
	ctx := context.Background()
	key := "rateLimit:" + uniq("MRLover")
	// Fill to capacity.
	for i := 0; i < 2; i++ {
		recordRedisRequest(ctx, common.RDB, key, 2)
	}
	ok, err := checkRedisRateLimit(ctx, common.RDB, key, 2, 600)
	require.NoError(t, err)
	require.False(t, ok) // oldest entry within window -> rejected
	common.RDB.Del(ctx, key)
}

func TestRecordRedisRequest_ZeroMaxNoOp(t *testing.T) {
	enableRedis(t)
	ctx := context.Background()
	key := "rateLimit:" + uniq("MRLnoop")
	recordRedisRequest(ctx, common.RDB, key, 0)
	n, err := common.RDB.LLen(ctx, key).Result()
	require.NoError(t, err)
	require.Zero(t, n)
}

// ---------------------------------------------------------------------------
// ModelRequestRateLimit — full middleware (memory + redis backends)
// ---------------------------------------------------------------------------

func runModelRateLimit(t *testing.T, n int, uid int) []int {
	t.Helper()
	r := gin.New()
	r.GET("/mrl", func(c *gin.Context) { c.Set("id", uid) }, ModelRequestRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	codes := make([]int, 0, n)
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/mrl", nil)
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	return codes
}

func TestModelRequestRateLimit_DisabledPassthrough(t *testing.T) {
	prev := setting.ModelRequestRateLimitEnabled
	setting.ModelRequestRateLimitEnabled = false
	t.Cleanup(func() { setting.ModelRequestRateLimitEnabled = prev })

	codes := runModelRateLimit(t, 3, nextTestID())
	for _, c := range codes {
		require.Equal(t, http.StatusOK, c)
	}
}

func TestModelRequestRateLimit_MemorySuccessLimit(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	prevEn := setting.ModelRequestRateLimitEnabled
	prevDur := setting.ModelRequestRateLimitDurationMinutes
	prevCount := setting.ModelRequestRateLimitCount
	prevSucc := setting.ModelRequestRateLimitSuccessCount
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = 0 // total unlimited -> exercise success path
	setting.ModelRequestRateLimitSuccessCount = 1
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		setting.ModelRequestRateLimitEnabled = prevEn
		setting.ModelRequestRateLimitDurationMinutes = prevDur
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSucc
	})

	// successMaxCount=1: the memory handler uses a "_check" pre-check key; the
	// 2nd request within the window is rejected.
	codes := runModelRateLimit(t, 2, nextTestID())
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusTooManyRequests, codes[1])
}

func TestModelRequestRateLimit_MemoryTotalLimit(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	prevEn := setting.ModelRequestRateLimitEnabled
	prevDur := setting.ModelRequestRateLimitDurationMinutes
	prevCount := setting.ModelRequestRateLimitCount
	prevSucc := setting.ModelRequestRateLimitSuccessCount
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = 1 // total limit hit on 2nd request
	setting.ModelRequestRateLimitSuccessCount = 1000
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		setting.ModelRequestRateLimitEnabled = prevEn
		setting.ModelRequestRateLimitDurationMinutes = prevDur
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSucc
	})

	codes := runModelRateLimit(t, 2, nextTestID())
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusTooManyRequests, codes[1])
}

func TestModelRequestRateLimit_RedisSuccessLimit(t *testing.T) {
	enableRedis(t)
	prevEn := setting.ModelRequestRateLimitEnabled
	prevDur := setting.ModelRequestRateLimitDurationMinutes
	prevCount := setting.ModelRequestRateLimitCount
	prevSucc := setting.ModelRequestRateLimitSuccessCount
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 10
	setting.ModelRequestRateLimitCount = 0
	setting.ModelRequestRateLimitSuccessCount = 1
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = prevEn
		setting.ModelRequestRateLimitDurationMinutes = prevDur
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSucc
	})

	uid := nextTestID()
	// 1st request succeeds and records a success; 2nd sees success limit reached.
	codes := runModelRateLimit(t, 2, uid)
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusTooManyRequests, codes[1])
	// cleanup redis keys
	common.RDB.Del(context.Background(),
		"rateLimit:"+ModelRequestRateLimitSuccessCountMark+":"+strconv.Itoa(uid))
}
