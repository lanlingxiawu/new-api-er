package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TurnstileCheck — non-network branches only (real HTTP call to Cloudflare is
// not exercised: there is no URL injection point, so the verify path would hit
// the live challenges.cloudflare.com endpoint, which tests must never do).
// ---------------------------------------------------------------------------

func TestTurnstileCheck_DisabledPassthrough(t *testing.T) {
	prev := common.TurnstileCheckEnabled
	common.TurnstileCheckEnabled = false
	t.Cleanup(func() { common.TurnstileCheckEnabled = prev })

	r := newSessionRouter()
	r.GET("/t", TurnstileCheck(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTurnstileCheck_AlreadyCheckedSessionPassthrough(t *testing.T) {
	prev := common.TurnstileCheckEnabled
	common.TurnstileCheckEnabled = true
	t.Cleanup(func() { common.TurnstileCheckEnabled = prev })

	r := newSessionRouter()
	cookies := loginSession(t, r, map[string]interface{}{"turnstile": true})
	r.GET("/t", TurnstileCheck(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTurnstileCheck_EmptyTokenRejected(t *testing.T) {
	prev := common.TurnstileCheckEnabled
	common.TurnstileCheckEnabled = true
	t.Cleanup(func() { common.TurnstileCheckEnabled = prev })

	r := newSessionRouter()
	r.GET("/t", TurnstileCheck(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	// No session turnstile flag, no ?turnstile= query -> empty token -> rejected.
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t", nil))
	require.Equal(t, http.StatusOK, rec.Code) // returns 200 {success:false}
	require.Contains(t, rec.Body.String(), "false")
}

// ---------------------------------------------------------------------------
// EmailVerificationRateLimit
// ---------------------------------------------------------------------------

func runEmailVerify(t *testing.T, n int) []int {
	t.Helper()
	r := gin.New()
	r.GET("/ev", EmailVerificationRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })
	codes := make([]int, 0, n)
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ev", nil)
		req.RemoteAddr = "198.51.100.9:1111"
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	return codes
}

func TestEmailVerificationRateLimit_MemoryOverLimit(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })

	// EmailVerificationMaxRequests = 2 within the window; 3rd is rejected.
	codes := runEmailVerify(t, 3)
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusOK, codes[1])
	require.Equal(t, http.StatusTooManyRequests, codes[2])
}

func TestEmailVerificationRateLimit_RedisOverLimit(t *testing.T) {
	enableRedis(t)
	r := gin.New()
	r.GET("/ev", EmailVerificationRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })
	key := redisIPRateLimitKey(EmailVerificationRateLimitMark, "203.0.113.44")
	common.RDB.Del(context.Background(), key) // clear stale counter
	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ev", nil)
		req.RemoteAddr = "203.0.113.44:2222"
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	require.Equal(t, http.StatusOK, codes[0])
	require.Equal(t, http.StatusOK, codes[1])
	require.Equal(t, http.StatusTooManyRequests, codes[2])
	common.RDB.Del(context.Background(), key)
}
