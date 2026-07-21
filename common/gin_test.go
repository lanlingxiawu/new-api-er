package common

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGinCtx(method, contentType, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, rec
}

func TestIsRequestBodyTooLargeError(t *testing.T) {
	assert.False(t, IsRequestBodyTooLargeError(nil))
	assert.True(t, IsRequestBodyTooLargeError(ErrRequestBodyTooLarge))
	assert.True(t, IsRequestBodyTooLargeError(&http.MaxBytesError{Limit: 10}))
	assert.False(t, IsRequestBodyTooLargeError(io.EOF))
}

func TestGetRequestBody_FromRequest(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	c, _ := newGinCtx("POST", "application/json", `{"a":1}`)

	seeker, err := GetRequestBody(c)
	require.NoError(t, err)
	bs, ok := seeker.(BodyStorage)
	require.True(t, ok)
	b, _ := bs.Bytes()
	assert.Equal(t, `{"a":1}`, string(b))

	// second call returns the cached storage (seeked to start)
	seeker2, err := GetRequestBody(c)
	require.NoError(t, err)
	assert.Same(t, seeker, seeker2)
	CleanupBodyStorage(c)
}

func TestGetRequestBody_FromLegacyByteCache(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	c, _ := newGinCtx("POST", "application/json", "")
	c.Set(KeyRequestBody, []byte(`{"cached":true}`))

	seeker, err := GetRequestBody(c)
	require.NoError(t, err)
	bs := seeker.(BodyStorage)
	b, _ := bs.Bytes()
	assert.Equal(t, `{"cached":true}`, string(b))
	CleanupBodyStorage(c)
}

func TestGetBodyStorageAndCleanup(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	c, _ := newGinCtx("POST", "application/json", `{"a":1}`)

	bs, err := GetBodyStorage(c)
	require.NoError(t, err)
	require.NotNil(t, bs)

	CleanupBodyStorage(c)
	// storage was closed; the context slot is cleared
	v, _ := c.Get(KeyBodyStorage)
	assert.Nil(t, v)
}

func TestUnmarshalBodyReusable_JSON(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	c, _ := newGinCtx("POST", "application/json", `{"name":"x","n":5}`)

	var out struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	require.NoError(t, UnmarshalBodyReusable(c, &out))
	assert.Equal(t, "x", out.Name)
	assert.Equal(t, 5, out.N)

	// body is left re-readable
	body, _ := io.ReadAll(c.Request.Body)
	assert.Equal(t, `{"name":"x","n":5}`, string(body))
	CleanupBodyStorage(c)
}

func TestUnmarshalBodyReusable_DiskJSON(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 100})
	ResetDiskCacheUsage()
	c, _ := newGinCtx("POST", "application/json", `{"name":"disk"}`)

	var out struct {
		Name string `json:"name"`
	}
	require.NoError(t, UnmarshalBodyReusable(c, &out))
	assert.Equal(t, "disk", out.Name)
	CleanupBodyStorage(c)
}

func TestUnmarshalBodyReusable_FormData(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	c, _ := newGinCtx("POST", "application/x-www-form-urlencoded", "name=alice&n=7")

	var out struct {
		Name string `json:"name"`
		N    string `json:"n"`
	}
	require.NoError(t, UnmarshalBodyReusable(c, &out))
	assert.Equal(t, "alice", out.Name)
	assert.Equal(t, "7", out.N)
	CleanupBodyStorage(c)
}

func TestUnmarshalBodyReusable_UnknownContentTypeSkips(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	c, _ := newGinCtx("POST", "text/plain", "raw text")

	var out map[string]any
	// unknown content type is skipped (no error, no population)
	require.NoError(t, UnmarshalBodyReusable(c, &out))
	assert.Nil(t, out)
	CleanupBodyStorage(c)
}

func buildMultipart(t *testing.T) (string, string) {
	t.Helper()
	var sb strings.Builder
	w := multipart.NewWriter(&sb)
	require.NoError(t, w.WriteField("field1", "value1"))
	require.NoError(t, w.WriteField("field2", "value2"))
	require.NoError(t, w.Close())
	return w.FormDataContentType(), sb.String()
}

func TestUnmarshalBodyReusable_Multipart(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	ct, body := buildMultipart(t)
	c, _ := newGinCtx("POST", ct, body)

	var out struct {
		F1 string `json:"field1"`
		F2 string `json:"field2"`
	}
	require.NoError(t, UnmarshalBodyReusable(c, &out))
	assert.Equal(t, "value1", out.F1)
	assert.Equal(t, "value2", out.F2)
	CleanupBodyStorage(c)
}

func TestParseMultipartFormReusable(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	ct, body := buildMultipart(t)
	c, _ := newGinCtx("POST", ct, body)

	form, err := ParseMultipartFormReusable(c)
	require.NoError(t, err)
	require.NotNil(t, form)
	assert.Equal(t, "value1", form.Value["field1"][0])
	CleanupBodyStorage(c)
}

func TestParseBoundary(t *testing.T) {
	_, err := parseBoundary("")
	assert.ErrorIs(t, err, errBoundaryNotFound)

	_, err = parseBoundary("application/json")
	assert.ErrorIs(t, err, errBoundaryNotFound)

	b, err := parseBoundary("multipart/form-data; boundary=abc123")
	require.NoError(t, err)
	assert.Equal(t, "abc123", b)

	_, err = parseBoundary("multipart/form-data; charset=x; boundary=")
	assert.Error(t, err)
}

func TestParseFormData(t *testing.T) {
	var out struct {
		A string   `json:"a"`
		B []string `json:"b"`
	}
	require.NoError(t, parseFormData([]byte("a=1&b=x&b=y"), &out))
	assert.Equal(t, "1", out.A)
	assert.Equal(t, []string{"x", "y"}, out.B)

	// invalid query encoding surfaces an error
	err := parseFormData([]byte("%zz=1"), &out)
	assert.Error(t, err)
}

func TestParseMultipartFormData_FallbackToJSON(t *testing.T) {
	c, _ := newGinCtx("POST", "application/json", "")
	var out struct {
		A int `json:"a"`
	}
	// no boundary -> falls back to JSON unmarshal of the data
	require.NoError(t, parseMultipartFormData(c, []byte(`{"a":9}`), &out))
	assert.Equal(t, 9, out.A)
}

func TestMultipartMemoryLimit(t *testing.T) {
	orig := constant.MaxFileDownloadMB
	t.Cleanup(func() { constant.MaxFileDownloadMB = orig })

	constant.MaxFileDownloadMB = 8
	assert.Equal(t, int64(8)<<20, multipartMemoryLimit())

	constant.MaxFileDownloadMB = 0
	assert.Equal(t, int64(32)<<20, multipartMemoryLimit()) // default
}

func TestContextKeyHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	const k constant.ContextKey = "mykey"
	SetContextKey(c, k, "hello")
	v, ok := GetContextKey(c, k)
	assert.True(t, ok)
	assert.Equal(t, "hello", v)
	assert.Equal(t, "hello", GetContextKeyString(c, k))

	SetContextKey(c, "intk", 42)
	assert.Equal(t, 42, GetContextKeyInt(c, "intk"))

	SetContextKey(c, "boolk", true)
	assert.True(t, GetContextKeyBool(c, "boolk"))

	SetContextKey(c, "slicek", []string{"a", "b"})
	assert.Equal(t, []string{"a", "b"}, GetContextKeyStringSlice(c, "slicek"))

	SetContextKey(c, "mapk", map[string]any{"x": 1})
	assert.Equal(t, map[string]any{"x": 1}, GetContextKeyStringMap(c, "mapk"))

	now := time.Now()
	SetContextKey(c, "timek", now)
	assert.Equal(t, now, GetContextKeyTime(c, "timek"))

	// typed getter: hit and miss
	SetContextKey(c, "typedk", 3.14)
	f, ok := GetContextKeyType[float64](c, "typedk")
	assert.True(t, ok)
	assert.Equal(t, 3.14, f)

	_, ok = GetContextKeyType[int](c, "typedk") // wrong type
	assert.False(t, ok)
	_, ok = GetContextKeyType[int](c, "absent") // missing key
	assert.False(t, ok)
}

func TestApiResponses(t *testing.T) {
	t.Run("ApiError", func(t *testing.T) {
		c, rec := newGinCtx("GET", "", "")
		ApiError(c, io.EOF)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"success":false`)
		assert.Contains(t, rec.Body.String(), "EOF")
	})

	t.Run("ApiErrorMsg", func(t *testing.T) {
		c, rec := newGinCtx("GET", "", "")
		ApiErrorMsg(c, "boom")
		assert.Contains(t, rec.Body.String(), `"message":"boom"`)
	})

	t.Run("ApiSuccess", func(t *testing.T) {
		c, rec := newGinCtx("GET", "", "")
		ApiSuccess(c, map[string]int{"n": 1})
		assert.Contains(t, rec.Body.String(), `"success":true`)
		assert.Contains(t, rec.Body.String(), `"n":1`)
	})

	t.Run("ApiErrorI18n", func(t *testing.T) {
		c, rec := newGinCtx("GET", "", "")
		ApiErrorI18n(c, "some.key")
		assert.Contains(t, rec.Body.String(), `"success":false`)
		assert.Contains(t, rec.Body.String(), "some.key") // default translator echoes key
	})

	t.Run("ApiSuccessI18n", func(t *testing.T) {
		c, rec := newGinCtx("GET", "", "")
		ApiSuccessI18n(c, "ok.key", map[string]int{"a": 2})
		assert.Contains(t, rec.Body.String(), `"success":true`)
		assert.Contains(t, rec.Body.String(), "ok.key")
	})
}
