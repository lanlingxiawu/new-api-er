package service

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// token_counter_test.go covers the media fallback in EstimateRequestToken: an
// image / file the gateway cannot read must not fail the request.

func setMediaCountFlags(t *testing.T, media, mediaNotStream bool) {
	t.Helper()
	o0, o1, o2 := constant.CountToken, constant.GetMediaToken, constant.GetMediaTokenNotStream
	constant.CountToken = true
	constant.GetMediaToken = media
	constant.GetMediaTokenNotStream = mediaNotStream
	t.Cleanup(func() {
		constant.CountToken = o0
		constant.GetMediaToken = o1
		constant.GetMediaTokenNotStream = o2
	})
}

// allowLoopbackFetch turns SSRF protection off so downloads really reach the
// httptest server (the default settings reject its loopback address / port).
func allowLoopbackFetch(t *testing.T) {
	t.Helper()
	InitHttpClient()
	orig := *system_setting.GetFetchSetting()
	s := orig
	s.EnableSSRFProtection = false
	system_setting.ReplaceFetchSetting(s)
	t.Cleanup(func() { system_setting.ReplaceFetchSetting(orig) })
}

// countingServer serves handler and counts requests.
func countingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// forbiddenImageServer answers 403 to everything (the wikimedia case from the
// test report).
func forbiddenImageServer(t *testing.T) (*httptest.Server, *int32) {
	return countingServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))))
	return buf.Bytes()
}

func estimateCtx(model string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
	return c
}

// "hi" counted as 2 runes + 3 OpenAI framing tokens.
const estimateTextTokens = 2 + 3

func estimateMeta(files ...*types.FileMeta) *types.TokenCountMeta {
	return &types.TokenCountMeta{TokenType: types.TokenTypeTextNumber, CombineText: "hi", Files: files}
}

func streamOpenAI() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, IsStream: true}
}

func TestEstimateRequestToken_ImageURLDownloadFails_UsesFallback(t *testing.T) {
	setMediaCountFlags(t, true, true)
	allowLoopbackFetch(t)
	srv, hits := forbiddenImageServer(t)

	for _, model := range []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1-mini", "o3"} {
		t.Run(model, func(t *testing.T) {
			before := atomic.LoadInt32(hits)
			img := &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewURLFileSource(srv.URL + "/cat.png?sig=SECRET"), Detail: "high"}

			got, err := EstimateRequestToken(estimateCtx(model), estimateMeta(img), streamOpenAI())

			require.NoError(t, err, "an unreadable image must not fail the request")
			assert.Equal(t, estimateTextTokens+imageTokenFallback(model, "high"), got)
			assert.Equal(t, int32(1), atomic.LoadInt32(hits)-before, "downloaded once, not retried by getImageToken")
		})
	}
}

func TestEstimateRequestToken_ImageURLDownloadFails_LowDetailFallback(t *testing.T) {
	setMediaCountFlags(t, true, true)
	allowLoopbackFetch(t)
	srv, _ := forbiddenImageServer(t)
	img := &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewURLFileSource(srv.URL + "/x.png"), Detail: "low"}

	got, err := EstimateRequestToken(estimateCtx("gpt-4o"), estimateMeta(img), streamOpenAI())

	require.NoError(t, err)
	assert.Equal(t, estimateTextTokens+85, got, "low detail keeps the base-token estimate")
}

// With the default SSRF settings the loopback URL is refused before any
// request is sent; that refusal must not fail the request either.
func TestEstimateRequestToken_ImageURLRefusedBySSRFGuard_UsesFallback(t *testing.T) {
	setMediaCountFlags(t, true, true)
	orig := *system_setting.GetFetchSetting()
	s := orig
	s.EnableSSRFProtection = true
	s.AllowPrivateIp = false
	system_setting.ReplaceFetchSetting(s)
	t.Cleanup(func() { system_setting.ReplaceFetchSetting(orig) })
	srv, hits := forbiddenImageServer(t)
	img := &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewURLFileSource(srv.URL + "/x.png"), Detail: "high"}

	got, err := EstimateRequestToken(estimateCtx("gpt-4o"), estimateMeta(img), streamOpenAI())

	require.NoError(t, err)
	assert.Equal(t, estimateTextTokens+3*85, got)
	assert.Equal(t, int32(0), atomic.LoadInt32(hits))
}

// A readable image is still measured (parity with the pre-fix success path).
func TestEstimateRequestToken_ImageURLReadable_MeasuresImage(t *testing.T) {
	setMediaCountFlags(t, true, true)
	allowLoopbackFetch(t)
	origMax := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 20
	t.Cleanup(func() { constant.MaxFileDownloadMB = origMax })
	body := testPNG(t, 512, 512)
	srv, hits := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	})
	img := &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewURLFileSource(srv.URL + "/ok.png"), Detail: "high"}

	got, err := EstimateRequestToken(estimateCtx("gpt-4o"), estimateMeta(img), streamOpenAI())

	require.NoError(t, err)
	// 512x512 high: scaled to 768x768 -> 2x2 tiles -> 4*170 + 85.
	assert.Equal(t, estimateTextTokens+4*170+85, got)
	assert.Equal(t, int32(1), atomic.LoadInt32(hits), "the cached download is reused for the image size")
}

func TestEstimateRequestToken_UnknownTypeURLDownloadFails_UsesDefaultFileEstimate(t *testing.T) {
	setMediaCountFlags(t, true, true)
	allowLoopbackFetch(t)
	srv, hits := forbiddenImageServer(t)
	file := &types.FileMeta{Source: types.NewURLFileSource(srv.URL + "/doc")}

	got, err := EstimateRequestToken(estimateCtx("gpt-4o"), estimateMeta(file), streamOpenAI())

	require.NoError(t, err)
	assert.Equal(t, estimateTextTokens+4096, got, "unknown type keeps the generic 4096 estimate")
	assert.Equal(t, int32(1), atomic.LoadInt32(hits))
}

func TestEstimateRequestToken_NonOpenAIModelURLDownloadFails_UsesFlatImageEstimate(t *testing.T) {
	setMediaCountFlags(t, true, true)
	allowLoopbackFetch(t)
	srv, hits := forbiddenImageServer(t)
	img := &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewURLFileSource(srv.URL + "/x.png")}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, IsStream: true}

	got, err := EstimateRequestToken(estimateCtx("claude-sonnet-4"), estimateMeta(img), info)

	require.NoError(t, err)
	assert.Equal(t, 2+520, got)
	assert.Equal(t, int32(1), atomic.LoadInt32(hits))
}

func TestEstimateRequestToken_UndecodableImage_UsesFallback(t *testing.T) {
	setMediaCountFlags(t, true, true)
	// Valid base64, but not an image the decoder understands.
	img := &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewBase64FileSource(base64.StdEncoding.EncodeToString([]byte("not an image")), "image/png"), Detail: "high"}

	got, err := EstimateRequestToken(estimateCtx("gpt-4o"), estimateMeta(img), streamOpenAI())

	require.NoError(t, err)
	assert.Equal(t, estimateTextTokens+imageTokenFallback("gpt-4o", "high"), got)
}

// Non-stream with GET_MEDIA_TOKEN_NOT_STREAM off never downloads (unchanged).
func TestEstimateRequestToken_NonStreamDoesNotFetch(t *testing.T) {
	setMediaCountFlags(t, true, false)
	allowLoopbackFetch(t)
	srv, hits := forbiddenImageServer(t)
	img := &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewURLFileSource(srv.URL + "/x.png")}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, IsStream: false}

	got, err := EstimateRequestToken(estimateCtx("gpt-4o"), estimateMeta(img), info)

	require.NoError(t, err)
	assert.Equal(t, estimateTextTokens+3*85, got)
	assert.Equal(t, int32(0), atomic.LoadInt32(hits))
}

func TestImageTokenFallback_MatchesMediaCountingDisabled(t *testing.T) {
	cases := map[string]int{
		"gpt-4o":               3 * 85,
		"GPT-4o-mini":          3 * 2833,
		"gpt-5":                3 * 70,
		"o1-pro":               3 * 75,
		"o3":                   3 * 75,
		"computer-use-preview": 3 * 65,
		"gpt-4.1-mini":         3 * 85, // patch-based: default base
		"gpt-5-nano":           3 * 85,
		"glm-4v":               1047,
		"some-unknown":         3 * 85,
	}
	for model, want := range cases {
		assert.Equal(t, want, imageTokenFallback(model, "high"), model)
	}

	// Parity with getImageToken when local media counting is off, for every
	// detail level (low detail returns the base tokens on tile-based models).
	setMediaCountFlags(t, false, false)
	c := estimateCtx("")
	for model := range cases {
		for _, detail := range []string{"", "auto", "low", "high"} {
			meta := &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewURLFileSource("https://example.com/a.png"), Detail: detail}
			got, err := getImageToken(c, meta, model, true)
			require.NoError(t, err)
			assert.Equal(t, got, imageTokenFallback(model, detail), "%s detail=%q", model, detail)
		}
	}
}

// Real download errors quote the URL in re-serialized forms (userinfo masked,
// path percent-encoded, redirect target), so the redaction must not depend on
// the original string.
func TestFileSourceLogHelpers_DoNotLeakURL(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example.com/next?token=SECRET2", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	hangup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(hangup.Close)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	raws := []struct {
		raw    string
		client *http.Client
	}{
		{"http://user:pass@" + hangup.Listener.Addr().String() + "/图 片.png?X-Amz-Signature=SECRET", http.DefaultClient},
		{redirect.URL + "/a.png?sig=SECRET", noRedirect},
		// Quotes / angle brackets stay unescaped in the re-serialized query.
		{"http://" + hangup.Listener.Addr().String() + "/my image.png?a='x'&b=<c>&q=\"d\"&X-Amz-Signature=SECRET", http.DefaultClient},
	}
	for _, tc := range raws {
		src := types.NewURLFileSource(tc.raw)
		resp, reqErr := tc.client.Get(tc.raw)
		if resp != nil {
			_ = resp.Body.Close()
		}
		require.Error(t, reqErr)
		err := errors.New("failed to download file from " + tc.raw + ": " + reqErr.Error() + "; fail to decode image config: " + src.GetIdentifier())

		msg := redactFileSourceError(src, err)
		assert.NotContains(t, msg, "SECRET")
		assert.NotContains(t, msg, "pass")
		assert.NotContains(t, msg, "127.0.0.1")
		assert.Contains(t, msg, "<url>")

		desc := describeFileSourceForLog(src)
		assert.NotContains(t, desc, "SECRET")
		assert.NotContains(t, desc, "pass")
		assert.Contains(t, desc, "url host=")
	}

	assert.Equal(t, "base64", describeFileSourceForLog(types.NewBase64FileSource("AAAA", "image/png")))
	assert.Equal(t, "unknown", describeFileSourceForLog(nil))
	assert.Equal(t, "url", describeFileSourceForLog(types.NewURLFileSource("::bad")))
	assert.Equal(t, "", redactFileSourceError(nil, nil))
	assert.Equal(t, "boom", redactFileSourceError(types.NewBase64FileSource("AAAA", "image/png"), errors.New("boom")))
}
