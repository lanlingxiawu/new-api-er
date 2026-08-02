package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// JimengRequestConvert
// ---------------------------------------------------------------------------

func TestJimengRequestConvert_MissingAction(t *testing.T) {
	r := gin.New()
	r.POST("/jimeng", JimengRequestConvert(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jimeng", strReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestJimengRequestConvert_TextGenerate(t *testing.T) {
	var gotPath, gotAction, gotModel string
	r := gin.New()
	r.POST("/jimeng", JimengRequestConvert(), func(c *gin.Context) {
		gotPath = c.Request.URL.Path
		gotAction = c.GetString("action")
		gotModel = c.GetString("action")
		_ = gotModel
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jimeng?Action=CVProcess",
		strReader(`{"req_key":"jimeng_v1","prompt":"a cat"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "/v1/video/generations", gotPath)
	// No image -> text-generate action.
	require.Equal(t, string(constant.TaskActionTextGenerate), gotAction)
}

func TestJimengRequestConvert_GetResultRewritesToFetch(t *testing.T) {
	var gotPath, gotMethod string
	var relayMode any
	r := gin.New()
	r.POST("/jimeng", JimengRequestConvert(), func(c *gin.Context) {
		gotPath = c.Request.URL.Path
		gotMethod = c.Request.Method
		relayMode, _ = c.Get("relay_mode")
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jimeng?Action=CVSync2AsyncGetResult",
		strReader(`{"req_key":"jimeng_v1","task_id":"task-99"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "/v1/video/generations/task-99", gotPath)
	require.Equal(t, http.MethodGet, gotMethod)
	require.NotNil(t, relayMode)
}

func TestJimengRequestConvert_GetResultMissingTaskId(t *testing.T) {
	r := gin.New()
	r.POST("/jimeng", JimengRequestConvert(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jimeng?Action=CVSync2AsyncGetResult",
		strReader(`{"req_key":"jimeng_v1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// ---------------------------------------------------------------------------
// KlingRequestConvert
// ---------------------------------------------------------------------------

func TestKlingRequestConvert_RewritesPathAndModel(t *testing.T) {
	var gotPath string
	var gotBody []byte
	r := gin.New()
	r.POST("/kling", KlingRequestConvert(), func(c *gin.Context) {
		gotPath = c.Request.URL.Path
		if v, ok := c.Get(common.KeyRequestBody); ok {
			gotBody, _ = v.([]byte)
		}
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/kling",
		strReader(`{"model_name":"kling-v1","prompt":"dance"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "/v1/video/generations", gotPath)
	require.Contains(t, string(gotBody), "kling-v1")
}

func TestKlingRequestConvert_InvalidBodyPassthrough(t *testing.T) {
	var gotPath string
	r := gin.New()
	r.POST("/kling", KlingRequestConvert(), func(c *gin.Context) {
		gotPath = c.Request.URL.Path
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/kling", strReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "/kling", gotPath) // unchanged on parse failure
}

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
