package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// file_service.go — file download / decode / cache service.
// Pure image/HEIF/mime helpers + httptest-backed loadFromURL. No real network.
// ===========================================================================

// allowFileDownload sets a generous max download size for the duration of a test.
func allowFileDownload(t *testing.T) {
	t.Helper()
	orig := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 20
	t.Cleanup(func() { constant.MaxFileDownloadMB = orig })
}

// pngBytes returns a valid encoded PNG of the given dimensions.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// box builds a minimal ISOBMFF box: 4-byte big-endian size, 4-byte type, content.
func box(boxType string, content []byte) []byte {
	out := make([]byte, 8+len(content))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(content)))
	copy(out[4:8], boxType)
	copy(out[8:], content)
	return out
}

// heicBytes builds a synthetic HEIC file: ftyp(heic) + meta->iprp->ipco->ispe(w,h).
func heicBytes(brand string, w, h uint32) []byte {
	ftyp := box("ftyp", append([]byte(brand), 0, 0, 0, 0))

	ispeContent := make([]byte, 12)
	// bytes 0..3 = version/flags (zero)
	binary.BigEndian.PutUint32(ispeContent[4:8], w)
	binary.BigEndian.PutUint32(ispeContent[8:12], h)
	ispe := box("ispe", ispeContent)
	ipco := box("ipco", ispe)
	iprp := box("iprp", ipco)

	// meta is a full box: 4 version/flags bytes then child boxes.
	metaContent := append([]byte{0, 0, 0, 0}, iprp...)
	meta := box("meta", metaContent)

	return append(ftyp, meta...)
}

// --- getContextCacheKey / getBase64ContextCacheKey -------------------------

func TestFileService_CacheKeys(t *testing.T) {
	k1 := getContextCacheKey("https://a/x.png")
	k2 := getContextCacheKey("https://a/y.png")
	assert.NotEqual(t, k1, k2)
	assert.Contains(t, k1, "file_cache_")

	// Short data uses whole string; long data uses first 128 chars.
	short := getBase64ContextCacheKey("abc", "image/png")
	assert.Contains(t, short, "b64_cache_")
	long := getBase64ContextCacheKey(string(make([]byte, 300)), "image/png")
	assert.NotEqual(t, short, long)
	// Same prefix material but different mime => different key.
	assert.NotEqual(t,
		getBase64ContextCacheKey("abc", "image/png"),
		getBase64ContextCacheKey("abc", "image/jpeg"),
	)
}

// --- detectHEIF ------------------------------------------------------------

func TestFileService_DetectHEIF(t *testing.T) {
	assert.Equal(t, "", detectHEIF([]byte("short")))            // < 12 bytes
	assert.Equal(t, "", detectHEIF(make([]byte, 20)))          // no ftyp
	assert.Equal(t, "image/heic", detectHEIF(heicBytes("heic", 4, 4)))
	assert.Equal(t, "image/heic", detectHEIF(heicBytes("hevc", 4, 4)))
	assert.Equal(t, "image/heif", detectHEIF(heicBytes("mif1", 4, 4)))
	assert.Equal(t, "image/heif", detectHEIF(heicBytes("msf1", 4, 4)))
	// ftyp present but unknown brand
	unknown := heicBytes("abcd", 4, 4)
	assert.Equal(t, "", detectHEIF(unknown))
}

// --- parseHEIFDimensions / findISPE ----------------------------------------

func TestFileService_ParseHEIFDimensions(t *testing.T) {
	w, h, ok := parseHEIFDimensions(heicBytes("heic", 640, 480))
	require.True(t, ok)
	assert.Equal(t, 640, w)
	assert.Equal(t, 480, h)

	// Too short.
	_, _, ok = parseHEIFDimensions([]byte("tiny"))
	assert.False(t, ok)

	// ftyp only, no meta box.
	ftypOnly := box("ftyp", append([]byte("heic"), 0, 0, 0, 0))
	_, _, ok = parseHEIFDimensions(ftypOnly)
	assert.False(t, ok)
}

// --- decodeImageConfig -----------------------------------------------------

func TestFileService_DecodeImageConfig(t *testing.T) {
	cfg, format, err := decodeImageConfig(pngBytes(t, 12, 7))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 12, cfg.Width)
	assert.Equal(t, 7, cfg.Height)

	// HEIC path.
	cfg, format, err = decodeImageConfig(heicBytes("heic", 100, 200))
	require.NoError(t, err)
	assert.Equal(t, "heic", format)
	assert.Equal(t, 100, cfg.Width)
	assert.Equal(t, 200, cfg.Height)

	// HEIF (mif1) branch => format "heif".
	_, format, err = decodeImageConfig(heicBytes("mif1", 8, 8))
	require.NoError(t, err)
	assert.Equal(t, "heif", format)

	// Unsupported.
	_, _, err = decodeImageConfig([]byte("not-an-image-at-all"))
	assert.Error(t, err)
}

// --- guessMimeTypeFromURL --------------------------------------------------

func TestFileService_GuessMimeTypeFromURL(t *testing.T) {
	assert.Equal(t, "image/png", guessMimeTypeFromURL("https://x/a.png"))
	assert.Equal(t, "image/png", guessMimeTypeFromURL("https://x/a.PNG?sig=1"))
	assert.Equal(t, "application/octet-stream", guessMimeTypeFromURL("https://x/noext"))
	assert.Equal(t, "application/octet-stream", guessMimeTypeFromURL("https://x/dir/"))
}

// --- DetectFileType --------------------------------------------------------

func TestFileService_DetectFileType(t *testing.T) {
	assert.Equal(t, types.FileTypeImage, DetectFileType("image/png"))
	assert.Equal(t, types.FileTypeAudio, DetectFileType("audio/mpeg"))
	assert.Equal(t, types.FileTypeVideo, DetectFileType("video/mp4"))
	assert.Equal(t, types.FileTypeFile, DetectFileType("application/pdf"))
	assert.Equal(t, types.FileTypeFile, DetectFileType(""))
}

// --- loadFromBase64 (via LoadFileSource / GetBase64Data) -------------------

func TestFileService_LoadFromBase64_DataPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	raw := pngBytes(t, 5, 9)
	b64 := base64.StdEncoding.EncodeToString(raw)
	src := types.NewBase64FileSource("data:image/png;base64,"+b64, "")

	data, mime, err := GetBase64Data(c, src, "test")
	require.NoError(t, err)
	assert.Equal(t, b64, data)
	assert.Equal(t, "image/png", mime, "mime parsed from data: header")

	// Image config decoded and cached on the source.
	cfg, format, err := GetImageConfig(c, src)
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 5, cfg.Width)
	assert.Equal(t, 9, cfg.Height)
}

func TestFileService_LoadFromBase64_ProvidedMimeOverrides(t *testing.T) {
	raw := pngBytes(t, 3, 3)
	b64 := base64.StdEncoding.EncodeToString(raw)
	// providedMimeType wins even against a data: header.
	src := types.NewBase64FileSource("data:image/jpeg;base64,"+b64, "image/png")

	data, mime, err := GetBase64Data(nil, src, "test")
	require.NoError(t, err)
	assert.Equal(t, b64, data)
	assert.Equal(t, "image/png", mime)
}

func TestFileService_LoadFromBase64_InvalidBase64(t *testing.T) {
	src := types.NewBase64FileSource("!!!not-base64!!!", "image/png")
	_, _, err := GetBase64Data(nil, src, "test")
	assert.Error(t, err)
}

func TestFileService_LoadFromBase64_DiskCache(t *testing.T) {
	// Force the disk-cache branch (threshold 0) so writeToDiskCache runs.
	orig := common.GetDiskCacheConfig()
	common.SetDiskCacheConfig(common.DiskCacheConfig{
		Enabled:     true,
		ThresholdMB: 0,
		MaxSizeMB:   1024,
		Path:        t.TempDir(),
	})
	t.Cleanup(func() { common.SetDiskCacheConfig(orig) })

	raw := pngBytes(t, 9, 9)
	b64 := base64.StdEncoding.EncodeToString(raw)
	src := types.NewBase64FileSource(b64, "image/png")

	data, mime, err := GetBase64Data(nil, src)
	require.NoError(t, err)
	assert.Equal(t, b64, data, "disk-cached data round-trips")
	assert.Equal(t, "image/png", mime)
}

func TestFileService_LoadFileSource_NilSource(t *testing.T) {
	_, err := LoadFileSource(nil, nil)
	assert.Error(t, err)
}

func TestFileService_LoadFileSource_URLContextCacheHit(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	raw := pngBytes(t, 6, 6)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	url := srv.URL + "/pic.png"

	// Two distinct source objects, same URL + same gin context => the second load
	// is served from the per-request context cache (no second HTTP fetch).
	_, err := LoadFileSource(c, types.NewURLFileSource(url))
	require.NoError(t, err)
	_, err = LoadFileSource(c, types.NewURLFileSource(url))
	require.NoError(t, err)
	assert.Equal(t, 1, hits, "second load hit the context cache")
}

func TestFileService_GetImageConfig_LoadError(t *testing.T) {
	// Invalid base64 => LoadFileSource fails => GetImageConfig surfaces the error.
	src := types.NewBase64FileSource("!!not-base64!!", "image/png")
	_, _, err := GetImageConfig(nil, src)
	assert.Error(t, err)
}

func TestFileService_LoadFileSource_CacheHit(t *testing.T) {
	src := types.NewBase64FileSource("QUJD", "text/plain") // "ABC"
	// First load populates cache.
	first, err := LoadFileSource(nil, src)
	require.NoError(t, err)
	assert.True(t, src.HasCache())
	// Second load returns the cached pointer.
	second, err := LoadFileSource(nil, src)
	require.NoError(t, err)
	assert.Same(t, first, second)
}

// --- loadFromURL (httptest) ------------------------------------------------

func TestFileService_LoadFromURL_Success(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	raw := pngBytes(t, 16, 24)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	src := types.NewURLFileSource(srv.URL + "/pic.png")
	cfg, format, err := GetImageConfig(c, src)
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 16, cfg.Width)
	assert.Equal(t, 24, cfg.Height)
}

func TestFileService_LoadFromURL_MimeSniffFromContent(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	raw := pngBytes(t, 4, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No/opaque Content-Type, no extension in URL => content sniffing kicks in.
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	src := types.NewURLFileSource(srv.URL + "/blob")
	_, mime, err := GetBase64Data(nil, src)
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
}

func TestFileService_GetImageConfig_DecodesWhenUncached(t *testing.T) {
	// A base64 source with a non-image mime keeps ImageConfig nil after load, so
	// GetImageConfig must decode it on demand.
	raw := pngBytes(t, 13, 21)
	src := types.NewBase64FileSource(base64.StdEncoding.EncodeToString(raw), "application/octet-stream")
	cfg, format, err := GetImageConfig(nil, src)
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 13, cfg.Width)
	assert.Equal(t, 21, cfg.Height)
}

func TestFileService_GetMimeType_URLSourceHead(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes(t, 4, 4))
	}))
	defer srv.Close()

	src := types.NewURLFileSource(srv.URL + "/a.png")
	mt, err := GetMimeType(fdCtx2(t), src)
	require.NoError(t, err)
	assert.Equal(t, "image/png", mt)
}

// fdCtx2 is a non-nil gin context for logger-safe calls in this file.
func fdCtx2(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func TestFileService_LoadFromURL_DiskCache(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	orig := common.GetDiskCacheConfig()
	common.SetDiskCacheConfig(common.DiskCacheConfig{
		Enabled: true, ThresholdMB: 0, MaxSizeMB: 1024, Path: t.TempDir(),
	})
	t.Cleanup(func() { common.SetDiskCacheConfig(orig) })

	raw := pngBytes(t, 18, 12)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	src := types.NewURLFileSource(srv.URL + "/pic.png")
	cfg, format, err := GetImageConfig(fdCtx2(t), src)
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 18, cfg.Width)
	assert.Equal(t, 12, cfg.Height)
}

func TestFileService_LoadFromURL_Non200(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	src := types.NewURLFileSource(srv.URL + "/missing")
	_, err := LoadFileSource(nil, src)
	assert.Error(t, err)
}
