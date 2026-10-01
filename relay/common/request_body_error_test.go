package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicRequestError(t *testing.T) {
	require.NoError(t, i18n.Init())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	en := func(key string) string { return i18n.Translate(i18n.LangEn, key) }
	bodyInvalid, formInvalid := en(i18n.MsgRelayRequestBodyInvalid), en(i18n.MsgRelayRequestFormInvalid)
	diskErr := errors.New("write /tmp/body-1: no space left on device")
	cases := []struct {
		name   string
		err    error
		status int
		want   string
		ok     bool
	}{
		{"nil", nil, 0, "", false},
		{"too large", common.ErrRequestBodyTooLarge, http.StatusRequestEntityTooLarge, en(i18n.MsgRelayRequestBodyTooLarge), true},
		{"too large while reading", &RequestBodyError{Err: &http.MaxBytesError{Limit: 1}, read: true}, http.StatusRequestEntityTooLarge, en(i18n.MsgRelayRequestBodyTooLarge), true},
		{"io.EOF (disk-backed decoder, empty body)", io.EOF, http.StatusBadRequest, bodyInvalid, true},
		{"unexpected EOF", fmt.Errorf("read: %w", io.ErrUnexpectedEOF), http.StatusBadRequest, bodyInvalid, true},
		{"syntax error", &json.SyntaxError{Offset: 1}, http.StatusBadRequest, bodyInvalid, true},
		{"invalid unmarshal target", &json.InvalidUnmarshalError{Type: reflect.TypeOf(0)}, http.StatusBadRequest, bodyInvalid, true},
		{"type error without field", &json.UnmarshalTypeError{Value: "array", Type: reflect.TypeOf(struct{}{})}, http.StatusBadRequest, bodyInvalid, true},
		{"type error on a type with no JSON name", &json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(make(chan int)), Field: "x"}, http.StatusBadRequest, bodyInvalid, true},
		{"type error wrapped in NewAPIError", types.NewError(&json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(0), Field: "top_k"}, types.ErrorCodeInvalidRequest),
			http.StatusBadRequest, "Invalid value for field 'top_k': expected integer.", true},
		{"read failure is a server fault", &RequestBodyError{Err: diskErr, read: true}, http.StatusInternalServerError, en(i18n.MsgRelayRequestBodyReadFailed), true},
		{"read cut short is the client's", &RequestBodyError{Err: io.ErrUnexpectedEOF, read: true}, http.StatusBadRequest, bodyInvalid, true},
		{"read cut short, form", &RequestBodyError{Err: io.ErrUnexpectedEOF, read: true, form: true}, http.StatusBadRequest, formInvalid, true},
		{"form parse failure", NewRequestFormError(errors.New("multipart: NextPart: EOF")), http.StatusBadRequest, formInvalid, true},
		{"form type error names the field", NewRequestFormError(&json.UnmarshalTypeError{Type: reflect.TypeOf(0), Field: "n"}), http.StatusBadRequest, "Invalid value for field 'n': expected integer.", true},
		{"unknown decode failure", &RequestBodyError{Err: errors.New("seek: bad file")}, http.StatusBadRequest, bodyInvalid, true},
		{"validation message", errors.New("model is required"), 0, "", false},
	}
	for _, tc := range cases {
		status, got, ok := PublicRequestError(c, tc.err)
		assert.Equal(t, tc.ok, ok, tc.name)
		assert.Equal(t, tc.status, status, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}

	// Translated, and safe on a context without an HTTP request.
	zh, _ := gin.CreateTestContext(httptest.NewRecorder())
	zh.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	zh.Request.Header.Set("Accept-Language", "zh-CN")
	_, got, _ := PublicRequestError(zh, io.EOF)
	assert.Equal(t, i18n.Translate(i18n.LangZhCN, i18n.MsgRelayRequestBodyInvalid), got)
	noRequest, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, got, _ = PublicRequestError(noRequest, io.EOF)
	assert.Equal(t, bodyInvalid, got)
	_, got, _ = PublicRequestError(nil, io.EOF)
	assert.Equal(t, bodyInvalid, got)
}

// UnmarshalRequestBody tells a body that cannot be read from one that cannot be decoded.
func TestUnmarshalRequestBody_MarksStage(t *testing.T) {
	newCtx := func(body io.Reader, ct string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", body)
		c.Request.Header.Set("Content-Type", ct)
		return c
	}
	var v struct {
		Model string `json:"model"`
	}
	err := UnmarshalRequestBody(newCtx(failingReader{errors.New("disk full")}, "application/json"), &v)
	var bodyErr *RequestBodyError
	require.ErrorAs(t, err, &bodyErr)
	assert.True(t, bodyErr.read)

	err = UnmarshalRequestBody(newCtx(strings.NewReader(`{"model":1}`), "application/json"), &v)
	require.ErrorAs(t, err, &bodyErr)
	assert.False(t, bodyErr.read)
	assert.False(t, bodyErr.form)

	err = UnmarshalRequestBody(newCtx(strings.NewReader(`model=%zz`), "application/x-www-form-urlencoded"), &v)
	require.ErrorAs(t, err, &bodyErr)
	assert.True(t, bodyErr.form)

	require.NoError(t, UnmarshalRequestBody(newCtx(strings.NewReader(`{"model":"m"}`), "application/json"), &v))
	assert.Equal(t, "m", v.Model)

	_, err = ParseMultipartRequestForm(newCtx(strings.NewReader("--x\r\nbroken"), "multipart/form-data; boundary=x"))
	require.ErrorAs(t, err, &bodyErr)
	assert.True(t, bodyErr.form)
	assert.False(t, bodyErr.read)
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestJSONTypeName(t *testing.T) {
	n := 0
	cases := []struct {
		t    reflect.Type
		want string
	}{
		{nil, ""},
		{reflect.TypeOf(uint(0)), "integer >= 0"},
		{reflect.TypeOf(uint8(0)), "integer >= 0"},
		{reflect.TypeOf(uint64(0)), "integer >= 0"},
		{reflect.TypeOf(0), "integer"},
		{reflect.TypeOf(int32(0)), "integer"},
		{reflect.TypeOf(0.5), "number"},
		{reflect.TypeOf(float32(0)), "number"},
		{reflect.TypeOf(json.Number("")), "number"},
		{reflect.TypeOf(""), "string"},
		{reflect.TypeOf(false), "boolean"},
		{reflect.TypeOf(struct{}{}), "object"},
		{reflect.TypeOf(map[string]any{}), "object"},
		{reflect.TypeOf([]string{}), "array"},
		{reflect.TypeOf([2]int{}), "array"},
		{reflect.TypeOf([]byte{}), "string"},
		{reflect.TypeOf(&n), "integer"},
		{reflect.TypeOf(new(*uint)), "integer >= 0"},
		{reflect.TypeOf(make(chan int)), ""},
		{reflect.TypeOf(func() {}), ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, jsonTypeName(tc.t), fmt.Sprint(tc.t))
	}
}

func TestJSONFieldPath(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"max_tokens":       "max_tokens",
		"messages.content": "messages.content",
		"Alias.generationConfig.Alias.maxOutputTokens": "generationConfig.maxOutputTokens",
		"Alias":                    "",
		"a..b":                     "a.b",
		"ClaudeRequest.max_tokens": "max_tokens",
	}
	for in, want := range cases {
		assert.Equal(t, want, jsonFieldPath(in), in)
	}
}

// Task submission shows the same messages as the relay, never Go decoder text
// such as "cannot unmarshal … into Go struct field TaskSubmitReq.prompt".
func TestTaskSubmit_BodyErrorsWithoutGoInternals(t *testing.T) {
	require.NoError(t, i18n.Init())
	newCtx := func(body io.Reader, ct string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", body)
		c.Request.Header.Set("Content-Type", ct)
		return c
	}
	assertClean := func(t *testing.T, msg string) {
		for _, internal := range []string{"json:", "Go struct", "TaskSubmitReq", "cannot unmarshal", "multipart:", "/tmp"} {
			assert.NotContains(t, msg, internal)
		}
	}

	taskErr := ValidateBasicTaskRequest(newCtx(strings.NewReader(`{"prompt":1}`), "application/json"), &RelayInfo{}, "generate")
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "invalid_request", taskErr.Code)
	assert.Equal(t, "Invalid value for field 'prompt': expected string.", taskErr.Message)
	assert.True(t, taskErr.SkipRetry)
	assertClean(t, taskErr.Message)

	taskErr = ValidateMultipartDirect(newCtx(strings.NewReader(`{"model":`), "application/json"), &RelayInfo{})
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "invalid_json", taskErr.Code)
	assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestBodyInvalid), taskErr.Message)

	taskErr = ValidateBasicTaskRequest(newCtx(strings.NewReader("--x\r\nbroken"), "multipart/form-data; boundary=x"), &RelayInfo{}, "generate")
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "invalid_multipart_form", taskErr.Code)
	assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestFormInvalid), taskErr.Message)
	assertClean(t, taskErr.Message)

	taskErr = ValidateBasicTaskRequest(newCtx(failingReader{errors.New("write /tmp/x: no space left")}, "application/json"), &RelayInfo{}, "generate")
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusInternalServerError, taskErr.StatusCode)
	assertClean(t, taskErr.Message)

	// validation messages are unchanged
	taskErr = ValidateBasicTaskRequest(newCtx(strings.NewReader(`{"prompt":" "}`), "application/json"), &RelayInfo{}, "generate")
	require.NotNil(t, taskErr)
	assert.Equal(t, "prompt is required", taskErr.Message)
}

// A multipart form whose file part spills to a temp file that cannot be
// created is the gateway's fault (500), unlike a malformed form (400) or one
// over the multipart limits (413). Both form entry points are covered: the
// relay's ParseMultipartRequestForm and the task path's c.MultipartForm().
func TestMultipartForm_DiskFailureIsServerFault(t *testing.T) {
	require.NoError(t, i18n.Init())
	prevLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1 // parts over 1 MiB go to a temp file
	t.Cleanup(func() { constant.MaxFileDownloadMB = prevLimit })
	missing := filepath.Join(t.TempDir(), "gone")
	for _, env := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(env, missing)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("prompt", "p"))
	part, err := w.CreateFormFile("image", "a.png")
	require.NoError(t, err)
	_, err = part.Write(bytes.Repeat([]byte("x"), 3<<20/2))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	body := buf.Bytes()
	newCtx := func() *gin.Context {
		c, engine := gin.CreateTestContext(httptest.NewRecorder())
		engine.MaxMultipartMemory = 1 << 20 // gin's c.MultipartForm() limit, used by the task path
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", w.FormDataContentType())
		// as the distributor middleware does: the body is stored once and replayed
		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		c.Request.Body = io.NopCloser(storage)
		return c
	}

	_, err = ParseMultipartRequestForm(newCtx())
	require.Error(t, err)
	assert.True(t, IsLocalIOError(err), "%v", err)
	status, message, ok := PublicRequestError(newCtx(), err)
	assert.True(t, ok)
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestBodyReadFailed), message)

	taskErr := ValidateBasicTaskRequest(newCtx(), &RelayInfo{}, "generate")
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusInternalServerError, taskErr.StatusCode)
	assert.NotContains(t, taskErr.Message, missing)
}

func TestPublicRequestError_FormFailureKinds(t *testing.T) {
	require.NoError(t, i18n.Init())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"multipart syntax", NewRequestFormError(errors.New("multipart: NextPart: EOF")), http.StatusBadRequest},
		{"malformed header", NewRequestFormError(textproto.ProtocolError("malformed MIME header line: x")), http.StatusBadRequest},
		{"not multipart", NewRequestFormError(http.ErrNotMultipart), http.StatusBadRequest},
		{"client connection reset", NewRequestFormError(&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", errors.New("connection reset"))}), http.StatusBadRequest},
		{"over multipart limits", NewRequestFormError(multipart.ErrMessageTooLarge), http.StatusRequestEntityTooLarge},
		{"temp file", NewRequestFormError(&fs.PathError{Op: "open", Path: "/tmp/multipart-1", Err: errors.New("no space left on device")}), http.StatusInternalServerError},
		{"decode from disk-backed body", &RequestBodyError{Err: &fs.PathError{Op: "read", Path: "/data/body-1", Err: errors.New("input/output error")}}, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		status, message, ok := PublicRequestError(c, tc.err)
		assert.True(t, ok, tc.name)
		assert.Equal(t, tc.status, status, tc.name)
		assert.NotContains(t, message, "/tmp", tc.name)
		assert.NotContains(t, message, "multipart:", tc.name)
	}
}
