package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// file_decoder.go + image.go — URL file type / base64 / image decoders.
// httptest-backed, no real network.
// ===========================================================================

// fdCtx returns a non-nil gin context so logger.Log* calls don't nil-deref.
func fdCtx(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func TestFileDecoder_GetMimeTypeByExtension(t *testing.T) {
	assert.Equal(t, "text/plain", GetMimeTypeByExtension("JSON"))
	assert.Equal(t, "image/jpeg", GetMimeTypeByExtension("jpg"))
	assert.Equal(t, "image/jpeg", GetMimeTypeByExtension("jfif"))
	assert.Equal(t, "image/png", GetMimeTypeByExtension("png"))
	assert.Equal(t, "image/heic", GetMimeTypeByExtension("heic"))
	assert.Equal(t, "image/gif", GetMimeTypeByExtension("gif"))
	assert.Equal(t, "application/octet-stream", GetMimeTypeByExtension("zzz"))
}

func TestFileDecoder_GetFileTypeFromUrl_HeaderContentType(t *testing.T) {
	disableWorkerAndSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png; charset=binary")
		_, _ = w.Write(pngBytes(t, 4, 4))
	}))
	defer srv.Close()

	mt, err := GetFileTypeFromUrl(fdCtx(t), srv.URL+"/a.png", "test")
	require.NoError(t, err)
	assert.Equal(t, "image/png", mt)
}

func TestFileDecoder_GetFileTypeFromUrl_SniffContent(t *testing.T) {
	disableWorkerAndSSRF(t)
	png := pngBytes(t, 4, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	// URL has no extension => falls through to content sniffing.
	mt, err := GetFileTypeFromUrl(fdCtx(t), srv.URL+"/blob", "test")
	require.NoError(t, err)
	assert.Equal(t, "image/png", mt)
}

func TestFileDecoder_GetFileTypeFromUrl_Non200(t *testing.T) {
	disableWorkerAndSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := GetFileTypeFromUrl(fdCtx(t), srv.URL+"/x", "test")
	assert.Error(t, err)
}

func TestFileDecoder_GetFileBase64FromUrl(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	png := pngBytes(t, 5, 5)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	data, err := GetFileBase64FromUrl(fdCtx(t), srv.URL+"/a.png", "test")
	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "image/png", data.MimeType)
	assert.NotEmpty(t, data.Base64Data)
	assert.Equal(t, srv.URL+"/a.png", data.Url)
}

// --- image.go --------------------------------------------------------------

func TestImage_GetImageFromUrl(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	png := pngBytes(t, 6, 6)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	mime, data, err := GetImageFromUrl(srv.URL + "/a.png")
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
	assert.NotEmpty(t, data)
}

func TestImage_GetImageFromUrl_InvalidContentType(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	}))
	defer srv.Close()

	_, _, err := GetImageFromUrl(srv.URL + "/x")
	assert.Error(t, err)
}

func TestImage_GetImageFromUrl_OctetStreamSniffs(t *testing.T) {
	disableWorkerAndSSRF(t)
	allowFileDownload(t)
	png := pngBytes(t, 7, 7)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	mime, _, err := GetImageFromUrl(srv.URL + "/blob")
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
}

func TestImage_DecodeUrlImageData(t *testing.T) {
	disableWorkerAndSSRF(t)
	png := pngBytes(t, 20, 11)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	cfg, format, err := DecodeUrlImageData(srv.URL + "/a.png")
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 20, cfg.Width)
	assert.Equal(t, 11, cfg.Height)
}

func TestImage_DecodeUrlImageData_BadContentType(t *testing.T) {
	disableWorkerAndSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	_, _, err := DecodeUrlImageData(srv.URL + "/x")
	assert.Error(t, err)
}

var _ = gin.TestMode
var _ = types.FileTypeImage
