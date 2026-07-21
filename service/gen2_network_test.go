package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withFetchSetting temporarily mutates the global fetch setting and restores it.
func withFetchSetting(t *testing.T, mutate func(fs *system_setting.FetchSetting)) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	orig := *fs
	mutate(fs)
	t.Cleanup(func() { *fs = orig })
}

// ---------------------------------------------------------------------------
// http.go
// ---------------------------------------------------------------------------

func TestHTTP_ShouldCopyUpstreamHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// Content-Length is filtered
	assert.False(t, ShouldCopyUpstreamHeader(c, "Content-Length", []string{"5"}))
	// RequestId header captured and filtered
	assert.False(t, ShouldCopyUpstreamHeader(c, common.RequestIdKey, []string{"rid-123"}))
	assert.Equal(t, "rid-123", c.GetString(common.UpstreamRequestIdKey))
	// RequestId with nil context / empty value still filtered without panic
	assert.False(t, ShouldCopyUpstreamHeader(nil, common.RequestIdKey, nil))
	// arbitrary header copied
	assert.True(t, ShouldCopyUpstreamHeader(c, "X-Custom", []string{"v"}))
}

func TestHTTP_CloseResponseBodyGracefully(t *testing.T) {
	// nil safe
	CloseResponseBodyGracefully(nil)
	CloseResponseBodyGracefully(&http.Response{})
	// real body
	resp := httptest.NewRecorder().Result()
	CloseResponseBodyGracefully(resp)
}

func TestHTTP_IOCopyBytesGracefully(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// with source response: headers + status copied
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	src := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"X-Foo": []string{"bar"}, "Content-Length": []string{"999"}},
	}
	IOCopyBytesGracefully(c, src, []byte("hello"))
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "bar", rec.Header().Get("X-Foo"))
	assert.Equal(t, "5", rec.Header().Get("Content-Length"))
	assert.Equal(t, "hello", rec.Body.String())

	// without source: defaults to 200
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	IOCopyBytesGracefully(c2, nil, []byte("x"))
	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "x", rec2.Body.String())
}

// ---------------------------------------------------------------------------
// http_client.go
// ---------------------------------------------------------------------------

func TestHTTPClient_InitAndGetters(t *testing.T) {
	InitHttpClient()
	require.NotNil(t, GetHttpClient())

	// proxy empty returns default client
	cl, err := GetHttpClientWithProxy("")
	require.NoError(t, err)
	assert.Equal(t, GetHttpClient(), cl)

	// SSRF disabled => protected client is the plain client
	withFetchSetting(t, func(fs *system_setting.FetchSetting) { fs.EnableSSRFProtection = false })
	assert.Equal(t, GetHttpClient(), GetSSRFProtectedHTTPClient())
}

func TestHTTPClient_GetSSRFProtectedWhenEnabled(t *testing.T) {
	InitHttpClient()
	withFetchSetting(t, func(fs *system_setting.FetchSetting) { fs.EnableSSRFProtection = true })
	cl := GetSSRFProtectedHTTPClient()
	require.NotNil(t, cl)
	assert.NotEqual(t, GetHttpClient(), cl)
}

func TestHTTPClient_NewProxyHttpClient(t *testing.T) {
	ResetProxyClientCache()

	// empty proxy => default/base client
	cl, err := NewProxyHttpClient("")
	require.NoError(t, err)
	require.NotNil(t, cl)

	// http proxy
	hc, err := NewProxyHttpClient("http://127.0.0.1:8080")
	require.NoError(t, err)
	require.NotNil(t, hc)
	// cache reuse returns same instance
	hc2, err := NewProxyHttpClient("http://127.0.0.1:8080")
	require.NoError(t, err)
	assert.Same(t, hc, hc2)

	// socks5 with auth
	sc, err := NewProxyHttpClient("socks5://user:pass@127.0.0.1:1080")
	require.NoError(t, err)
	require.NotNil(t, sc)

	// socks5h without auth
	sc2, err := NewProxyHttpClient("socks5h://127.0.0.1:1080")
	require.NoError(t, err)
	require.NotNil(t, sc2)

	// unsupported scheme
	_, err = NewProxyHttpClient("ftp://127.0.0.1")
	require.Error(t, err)

	// invalid URL
	_, err = NewProxyHttpClient("://bad url with spaces")
	require.Error(t, err)

	ResetProxyClientCache()
}

func TestHTTPClient_CheckRedirect(t *testing.T) {
	// too many redirects
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	via := make([]*http.Request, 10)
	withFetchSetting(t, func(fs *system_setting.FetchSetting) { fs.EnableSSRFProtection = false })
	assert.Error(t, checkRedirect(req, via))
	assert.Error(t, checkProtectedFetchRedirect(req, via))

	// blocked private redirect when SSRF on
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	priv, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/", nil)
	assert.Error(t, checkRedirect(priv, nil))
	assert.Error(t, checkProtectedFetchRedirect(priv, nil))
}

// ---------------------------------------------------------------------------
// protected_fetch_client.go — extras
// ---------------------------------------------------------------------------

func TestProtectedFetch_CurrentProtection(t *testing.T) {
	// disabled
	withFetchSetting(t, func(fs *system_setting.FetchSetting) { fs.EnableSSRFProtection = false })
	prot, enabled, err := currentFetchProtection()
	require.NoError(t, err)
	assert.False(t, enabled)
	assert.Nil(t, prot)

	// enabled
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = true
	})
	prot2, enabled2, err2 := currentFetchProtection()
	require.NoError(t, err2)
	assert.True(t, enabled2)
	require.NotNil(t, prot2)
}

func TestProtectedFetch_CloseIdleConnections(t *testing.T) {
	client := newProtectedFetchHTTPClient()
	require.NotNil(t, client)
	rt, ok := client.Transport.(*ssrfProtectedRoundTripper)
	require.True(t, ok)
	// prime a transport then close
	rt.transportFor(nil)
	rt.CloseIdleConnections()
}

// ---------------------------------------------------------------------------
// download.go
// ---------------------------------------------------------------------------

func TestDownload_DoWorkerRequest_Disabled(t *testing.T) {
	// WorkerUrl empty => worker disabled
	orig := system_setting.WorkerUrl
	system_setting.WorkerUrl = ""
	t.Cleanup(func() { system_setting.WorkerUrl = orig })

	_, err := DoWorkerRequest(&WorkerRequest{URL: "https://example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "worker not enabled")
}

func TestDownload_DoDownloadRequest_SSRFReject(t *testing.T) {
	orig := system_setting.WorkerUrl
	system_setting.WorkerUrl = ""
	t.Cleanup(func() { system_setting.WorkerUrl = orig })

	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	_, err := DoDownloadRequest("http://127.0.0.1:9/file", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request reject")
}

// ---------------------------------------------------------------------------
// exchange_rate.go — cache + fetch error branches (no external network)
// ---------------------------------------------------------------------------

func TestExchangeRate_MemoryCacheHit(t *testing.T) {
	// ensure Redis disabled (TestMain sets it) so memory path is used
	require.False(t, common.RedisEnabled)

	memExchangeRateLock.Lock()
	memExchangeRate = 6.99
	memExchangeRateTime = time.Now()
	memExchangeRateLock.Unlock()
	t.Cleanup(func() {
		memExchangeRateLock.Lock()
		memExchangeRate = 0
		memExchangeRateTime = time.Time{}
		memExchangeRateLock.Unlock()
	})

	rate, ok := resolveUSDCNYRate()
	assert.True(t, ok)
	assert.Equal(t, 6.99, rate)

	assert.Equal(t, 6.99, GetUSDCNYRate())

	r2, ok2 := GetUSDCNYRateWithOK()
	assert.True(t, ok2)
	assert.Equal(t, 6.99, r2)
}

func TestExchangeRate_FetchViaDeadProxyErrors(t *testing.T) {
	// Route the Binance call through a dead local proxy so the request fails
	// fast (connection refused) without ever reaching the real network.
	t.Setenv("BINANCE_PROXY_URL", "http://127.0.0.1:1")

	_, err := fetchRateFromBinance()
	require.Error(t, err)

	_, err = RefreshUSDCNYRate()
	require.Error(t, err)
}
