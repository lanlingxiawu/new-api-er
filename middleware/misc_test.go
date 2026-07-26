package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// CORS / Version
// ---------------------------------------------------------------------------

func TestCORS_AllowsOrigin(t *testing.T) {
	r := gin.New()
	r.Use(CORS())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	// Actual request: allowed through with the wildcard origin header.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://client.test")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCORS_Preflight(t *testing.T) {
	r := gin.New()
	r.Use(CORS())
	r.POST("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://client.test")
	req.Header.Set("Access-Control-Request-Method", "POST")
	r.ServeHTTP(rec, req)
	// Preflight is short-circuited with 204 by gin-contrib/cors.
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestVersion_SetsHeader(t *testing.T) {
	prev := common.Version
	common.Version = "v9.9.9-test"
	t.Cleanup(func() { common.Version = prev })
	ctx, rec := newCtx(http.MethodGet, "/", "")
	Version()(ctx)
	require.Equal(t, "v9.9.9-test", rec.Header().Get("X-New-Api-Version"))
}

// ---------------------------------------------------------------------------
// RequestId
// ---------------------------------------------------------------------------

func TestRequestId_InjectsIdAndHeader(t *testing.T) {
	ctx, rec := newCtx(http.MethodGet, "/", "")
	RequestId()(ctx)
	id := ctx.GetString(common.RequestIdKey)
	require.NotEmpty(t, id)
	require.Equal(t, id, rec.Header().Get(common.RequestIdKey))
	require.Equal(t, id, ctx.Request.Context().Value(common.RequestIdKey))
}

// ---------------------------------------------------------------------------
// DecompressRequestMiddleware (gzip.go)
// ---------------------------------------------------------------------------

func readBodyHandler(out *[]byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		*out = b
		c.Status(http.StatusOK)
	}
}

func TestDecompress_GzipBody(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write([]byte("hello gzip"))
	_ = gw.Close()

	var got []byte
	r := gin.New()
	r.POST("/d", DecompressRequestMiddleware(), readBodyHandler(&got))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/d", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "hello gzip", string(got))
}

func TestDecompress_BrotliBody(t *testing.T) {
	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	_, _ = bw.Write([]byte("hello brotli"))
	_ = bw.Close()

	var got []byte
	r := gin.New()
	r.POST("/d", DecompressRequestMiddleware(), readBodyHandler(&got))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/d", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Encoding", "br")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "hello brotli", string(got))
}

func TestDecompress_PlainBodyPassthrough(t *testing.T) {
	var got []byte
	r := gin.New()
	r.POST("/d", DecompressRequestMiddleware(), readBodyHandler(&got))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/d", bytes.NewReader([]byte("plain")))
	r.ServeHTTP(rec, req)
	require.Equal(t, "plain", string(got))
}

func TestDecompress_InvalidGzipBadRequest(t *testing.T) {
	r := gin.New()
	r.POST("/d", DecompressRequestMiddleware(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/d", bytes.NewReader([]byte("not gzip")))
	req.Header.Set("Content-Encoding", "gzip")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDecompress_GetSkips(t *testing.T) {
	r := gin.New()
	ran := false
	r.GET("/d", DecompressRequestMiddleware(), func(c *gin.Context) { ran = true; c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/d", nil)
	r.ServeHTTP(rec, req)
	require.True(t, ran)
}

// ---------------------------------------------------------------------------
// utils.go — abort helpers
// ---------------------------------------------------------------------------

func TestAbortWithOpenAiMessage(t *testing.T) {
	ctx, rec := newCtx(http.MethodPost, "/v1/chat/completions", "")
	ctx.Set("id", 5)
	abortWithOpenAiMessage(ctx, http.StatusForbidden, "denied")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "new_api_error")
	require.Contains(t, rec.Body.String(), "denied")
	require.True(t, ctx.IsAborted())
}

func TestAbortWithMidjourneyMessage(t *testing.T) {
	ctx, rec := newCtx(http.MethodPost, "/mj/submit/imagine", "")
	abortWithMidjourneyMessage(ctx, http.StatusBadRequest, 4, "mj failed")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "mj failed")
	require.True(t, ctx.IsAborted())
}

// ---------------------------------------------------------------------------
// recover.go
// ---------------------------------------------------------------------------

func TestRelayPanicRecover(t *testing.T) {
	r := gin.New()
	r.GET("/panic", RelayPanicRecover(), func(c *gin.Context) { panic("boom") })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	require.NotPanics(t, func() { r.ServeHTTP(rec, req) })
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "new_api_panic")
}

func TestRelayPanicRecover_NoPanicPassthrough(t *testing.T) {
	r := gin.New()
	r.GET("/ok", RelayPanicRecover(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// ---------------------------------------------------------------------------
// i18n.go
// ---------------------------------------------------------------------------

func TestI18nMiddleware_DefaultLanguage(t *testing.T) {
	ctx, _ := newCtx(http.MethodGet, "/", "")
	I18n()(ctx)
	require.Equal(t, i18n.DefaultLang, GetLanguage(ctx))
}

func TestI18nMiddleware_AcceptLanguageHeader(t *testing.T) {
	ctx, _ := newCtx(http.MethodGet, "/", "")
	ctx.Request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	I18n()(ctx)
	lang := GetLanguage(ctx)
	require.True(t, i18n.IsSupported(lang))
}

func TestGetLanguage_FallbackWhenUnset(t *testing.T) {
	ctx, _ := newCtx(http.MethodGet, "/", "")
	require.Equal(t, i18n.DefaultLang, GetLanguage(ctx))
}

// ---------------------------------------------------------------------------
// request_body_limit.go
// ---------------------------------------------------------------------------

func TestAnonymousRequestBodyLimit_UnderLimit(t *testing.T) {
	prev := constant.AnonymousRequestBodyLimitKB
	constant.AnonymousRequestBodyLimitKB = 1 // 1KB
	t.Cleanup(func() { constant.AnonymousRequestBodyLimitKB = prev })

	var got []byte
	r := gin.New()
	r.POST("/a", AnonymousRequestBodyLimit(), readBodyHandler(&got))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/a", bytes.NewReader([]byte("small")))
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "small", string(got))
}

func TestAnonymousRequestBodyLimit_OverLimit(t *testing.T) {
	prev := constant.AnonymousRequestBodyLimitKB
	constant.AnonymousRequestBodyLimitKB = 1 // 1KB
	t.Cleanup(func() { constant.AnonymousRequestBodyLimitKB = prev })

	big := bytes.Repeat([]byte("a"), 2048) // 2KB > 1KB
	r := gin.New()
	r.POST("/a", AnonymousRequestBodyLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/a", bytes.NewReader(big))
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestAnonymousRequestBodyLimit_DisabledPassthrough(t *testing.T) {
	prev := constant.AnonymousRequestBodyLimitKB
	constant.AnonymousRequestBodyLimitKB = 0 // disabled
	t.Cleanup(func() { constant.AnonymousRequestBodyLimitKB = prev })

	var got []byte
	r := gin.New()
	r.POST("/a", AnonymousRequestBodyLimit(), readBodyHandler(&got))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/a", bytes.NewReader([]byte("x")))
	r.ServeHTTP(rec, req)
	require.Equal(t, "x", string(got))
}

func TestReadAnonymousRequestBody_Boundary(t *testing.T) {
	// exactly at limit is allowed.
	data, err := readAnonymousRequestBody(bytes.NewReader([]byte("abc")), 3)
	require.NoError(t, err)
	require.Equal(t, "abc", string(data))
	// one over limit errors.
	_, err = readAnonymousRequestBody(bytes.NewReader([]byte("abcd")), 3)
	require.Error(t, err)
	require.True(t, common.IsRequestBodyTooLargeError(err))
}

// ---------------------------------------------------------------------------
// performance.go
// ---------------------------------------------------------------------------

func TestSystemPerformanceCheck_DisabledPassthrough(t *testing.T) {
	prev := common.GetPerformanceMonitorConfig()
	common.SetPerformanceMonitorConfig(common.PerformanceMonitorConfig{Enabled: false})
	t.Cleanup(func() { common.SetPerformanceMonitorConfig(prev) })

	r := gin.New()
	r.POST("/v1/chat/completions", SystemPerformanceCheck(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSystemPerformanceCheck_EnabledNotOverloaded(t *testing.T) {
	prev := common.GetPerformanceMonitorConfig()
	common.SetPerformanceMonitorConfig(common.PerformanceMonitorConfig{
		Enabled: true, CPUThreshold: 90, MemoryThreshold: 90, DiskThreshold: 90,
	})
	t.Cleanup(func() { common.SetPerformanceMonitorConfig(prev) })

	r := gin.New()
	r.POST("/v1/messages", SystemPerformanceCheck(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

// ---------------------------------------------------------------------------
// stats.go
// ---------------------------------------------------------------------------

func TestStatsMiddleware_TracksActiveConnections(t *testing.T) {
	before := GetStats().ActiveConnections
	r := gin.New()
	r.GET("/x", StatsMiddleware(), func(c *gin.Context) {
		require.Equal(t, before+1, GetStats().ActiveConnections)
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, before, GetStats().ActiveConnections) // decremented after
}

// ---------------------------------------------------------------------------
// cache.go / disable-cache.go
// ---------------------------------------------------------------------------

func TestCache_RootNoCache(t *testing.T) {
	ctx, rec := newCtx(http.MethodGet, "/", "")
	ctx.Request.RequestURI = "/"
	Cache()(ctx)
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
}

func TestCache_NonRootMaxAge(t *testing.T) {
	ctx, rec := newCtx(http.MethodGet, "/assets/x.js", "")
	ctx.Request.RequestURI = "/assets/x.js"
	Cache()(ctx)
	require.Contains(t, rec.Header().Get("Cache-Control"), "max-age")
}

func TestDisableCache(t *testing.T) {
	ctx, rec := newCtx(http.MethodGet, "/", "")
	DisableCache()(ctx)
	require.Contains(t, rec.Header().Get("Cache-Control"), "no-store")
	require.Equal(t, "no-cache", rec.Header().Get("Pragma"))
}

// ---------------------------------------------------------------------------
// logger.go — RouteTag
// ---------------------------------------------------------------------------

func TestRouteTag(t *testing.T) {
	ctx, _ := newCtx(http.MethodGet, "/", "")
	RouteTag("relay")(ctx)
	require.Equal(t, "relay", ctx.GetString(RouteTagKey))
}

// ---------------------------------------------------------------------------
// body_cleanup.go
// ---------------------------------------------------------------------------

func TestBodyStorageCleanup(t *testing.T) {
	r := gin.New()
	ran := false
	r.POST("/b", BodyStorageCleanup(), func(c *gin.Context) { ran = true; c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/b", bytes.NewReader([]byte(`{"a":1}`)))
	req.Header.Set("Content-Type", "application/json")
	require.NotPanics(t, func() { r.ServeHTTP(rec, req) })
	require.True(t, ran)
}

// ---------------------------------------------------------------------------
// logger.go — SetUpLogger installs a formatter without error
// ---------------------------------------------------------------------------

func TestSetUpLogger(t *testing.T) {
	r := gin.New()
	require.NotPanics(t, func() { SetUpLogger(r) })
	r.GET("/x", RouteTag("api"), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestDecompress_GzipBodyCloseReleasesReaders(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write([]byte("payload"))
	_ = gw.Close()

	r := gin.New()
	r.POST("/d", DecompressRequestMiddleware(), func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		// Explicitly close to exercise readCloser.Close -> gzip+orig close chain.
		require.NoError(t, c.Request.Body.Close())
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/d", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}
