package helper

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requestParseTestContext(t *testing.T, path, body, acceptLanguage string) *gin.Context {
	t.Helper()
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if acceptLanguage != "" {
		c.Request.Header.Set("Accept-Language", acceptLanguage)
	}
	return c
}

// goInternals are fragments of Go decoder errors that must never reach a client.
var goInternals = []string{"json:", "Go struct", "cannot unmarshal", "Request.", "of type uint", "invalid character"}

// Every request that fails GetAndValidateRequest is a client error: 400, not
// retried on another channel, and the message names the problem without Go
// internals.
func TestNewInvalidRequestError_FromGetAndValidateRequest(t *testing.T) {
	require.NoError(t, i18n.Init())
	bodyInvalid := i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestBodyInvalid)
	cases := []struct {
		name   string
		format types.RelayFormat
		path   string
		body   string
		want   string
	}{
		// decoding failures: translated message
		{"claude max_tokens string", types.RelayFormatClaude, "/v1/messages",
			`{"model":"m","max_tokens":"abc","messages":[{"role":"user","content":"hi"}]}`,
			"Invalid value for field 'max_tokens': expected integer >= 0."},
		{"openai max_tokens -1", types.RelayFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","max_tokens":-1,"messages":[{"role":"user","content":"hi"}]}`,
			"Invalid value for field 'max_tokens': expected integer >= 0."},
		{"openai temperature string", types.RelayFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","temperature":"hot","messages":[{"role":"user","content":"hi"}]}`,
			"Invalid value for field 'temperature': expected number."},
		{"openai stream not bool", types.RelayFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","stream":"yes","messages":[{"role":"user","content":"hi"}]}`,
			"Invalid value for field 'stream': expected boolean."},
		{"gemini nested field", types.RelayFormatGemini, "/v1beta/models/m:generateContent",
			`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":"x"}}`,
			"Invalid value for field 'generationConfig.maxOutputTokens': expected integer >= 0."},
		{"gemini malformed json", types.RelayFormatGemini, "/v1beta/models/m:generateContent", `{"contents":`, bodyInvalid},
		{"claude malformed json", types.RelayFormatClaude, "/v1/messages", `{"model":"m",}`, bodyInvalid},
		{"empty body", types.RelayFormatOpenAI, "/v1/chat/completions", ``, bodyInvalid},
		{"body is an array", types.RelayFormatOpenAI, "/v1/chat/completions", `[]`, bodyInvalid},
		{"image n wrong type", types.RelayFormatOpenAIImage, "/v1/images/generations",
			`{"model":"gpt-image-1","prompt":"p","n":"two"}`,
			"Invalid value for field 'n': expected integer >= 0."},
		{"rerank wraps the decode error", types.RelayFormatRerank, "/v1/rerank", `{"query":1}`,
			"Invalid value for field 'query': expected string."},

		// validation messages written for clients: passed through
		{"openai empty messages", types.RelayFormatOpenAI, "/v1/chat/completions", `{"model":"m","messages":[]}`, "field messages is required"},
		{"openai missing messages", types.RelayFormatOpenAI, "/v1/chat/completions", `{"model":"m"}`, "field messages is required"},
		{"claude empty messages", types.RelayFormatClaude, "/v1/messages", `{"model":"m","messages":[]}`, "field messages is required"},
		{"gemini empty contents", types.RelayFormatGemini, "/v1beta/models/m:generateContent", `{"contents":[]}`, "contents is required"},
		{"rerank empty query", types.RelayFormatRerank, "/v1/rerank", `{"model":"m","documents":["d"]}`, "query is empty"},
		{"max_tokens too large", types.RelayFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","max_tokens":4294967295,"messages":[{"role":"user","content":"hi"}]}`, "max_tokens is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := requestParseTestContext(t, tc.path, tc.body, "")
			_, err := GetAndValidateRequest(c, tc.format)
			require.Error(t, err)

			apiErr := NewInvalidRequestError(c, err)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr), "a bad request fails on every channel")
			assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
			assert.Equal(t, tc.want, apiErr.Error())
			for _, frag := range []string{apiErr.ToOpenAIError().Message, apiErr.ToClaudeError().Message} {
				for _, internal := range goInternals {
					assert.NotContains(t, frag, internal)
				}
			}
		})
	}
}

// The message follows the client's language like the other relay errors.
func TestNewInvalidRequestError_Translated(t *testing.T) {
	body := `{"model":"m","max_tokens":"abc","messages":[{"role":"user","content":"hi"}]}`
	c := requestParseTestContext(t, "/v1/messages", body, "zh-CN")
	_, err := GetAndValidateRequest(c, types.RelayFormatClaude)
	require.Error(t, err)
	assert.Equal(t, "字段 'max_tokens' 的值无效，应为 integer >= 0。", NewInvalidRequestError(c, err).Error())

	c = requestParseTestContext(t, "/v1/messages", `{`, "zh-TW")
	_, err = GetAndValidateRequest(c, types.RelayFormatClaude)
	require.Error(t, err)
	assert.Equal(t, i18n.Translate(i18n.LangZhTW, i18n.MsgRelayRequestBodyInvalid), NewInvalidRequestError(c, err).Error())
}

type failingBody struct{ err error }

func (b failingBody) Read([]byte) (int, error) { return 0, b.err }

// Form bodies and body-read failures: the client never sees multipart, strconv
// or file-system text. A body that cannot be stored is a server fault (500).
func TestNewInvalidRequestError_FormsAndReadFailures(t *testing.T) {
	require.NoError(t, i18n.Init())
	formInvalid := i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestFormInvalid)
	newEditContext := func(t *testing.T, body io.Reader, contentType string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", body)
		c.Request.Header.Set("Content-Type", contentType)
		return c
	}
	multipartBody := func(fields map[string]string) (io.Reader, string) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for k, v := range fields {
			require.NoError(t, w.WriteField(k, v))
		}
		require.NoError(t, w.Close())
		return &buf, w.FormDataContentType()
	}

	t.Run("broken multipart body", func(t *testing.T) {
		c := newEditContext(t, strings.NewReader("--x\r\nnot a part"), "multipart/form-data; boundary=x")
		_, err := GetAndValidateRequest(c, types.RelayFormatOpenAIImage)
		require.Error(t, err)
		apiErr := NewInvalidRequestError(c, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Equal(t, formInvalid, apiErr.Error())
		assert.NotContains(t, apiErr.Error(), "multipart:")
	})
	t.Run("stream is not a boolean", func(t *testing.T) {
		body, ct := multipartBody(map[string]string{"model": "gpt-image-1", "prompt": "p", "stream": "maybe"})
		c := newEditContext(t, body, ct)
		_, err := GetAndValidateRequest(c, types.RelayFormatOpenAIImage)
		require.Error(t, err)
		apiErr := NewInvalidRequestError(c, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Equal(t, "stream must be true or false", apiErr.Error())
	})
	t.Run("url-encoded form that does not parse", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader("model=%zz"))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		_, err := GetAndValidateRequest(c, types.RelayFormatOpenAIAudio)
		require.Error(t, err)
		apiErr := NewInvalidRequestError(c, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Equal(t, formInvalid, apiErr.Error())
	})
	t.Run("body cannot be read", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", failingBody{errors.New("write /tmp/body-123: no space left on device")})
		c.Request.Header.Set("Content-Type", "application/json")
		_, err := GetAndValidateRequest(c, types.RelayFormatOpenAI)
		require.Error(t, err)
		apiErr := NewInvalidRequestError(c, err)
		assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
		assert.Equal(t, types.ErrorCodeReadRequestBodyFailed, apiErr.GetErrorCode())
		assert.True(t, types.IsSkipRetryError(apiErr))
		assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestBodyReadFailed), apiErr.Error())
		assert.NotContains(t, apiErr.ToOpenAIError().Message, "/tmp")
	})
	t.Run("body cut short while reading", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", failingBody{io.ErrUnexpectedEOF})
		c.Request.Header.Set("Content-Type", "application/json")
		_, err := GetAndValidateRequest(c, types.RelayFormatOpenAI)
		require.Error(t, err)
		apiErr := NewInvalidRequestError(c, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayRequestBodyInvalid), apiErr.Error())
	})
}

// Nested JSON field paths such as generationConfig.maxOutputTokens look like
// domain names to the sensitive-info masker; the rendered client message must
// keep them readable in both error formats.
func TestNewInvalidRequestError_NestedFieldPathIsNotMasked(t *testing.T) {
	c := requestParseTestContext(t, "/v1beta/models/gemini-2.5-flash:generateContent",
		`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":"abc"}}`, "")
	_, err := GetAndValidateRequest(c, types.RelayFormatGemini)
	require.Error(t, err)
	apiErr := NewInvalidRequestError(c, err)

	for _, message := range []string{apiErr.ToOpenAIError().Message, apiErr.ToClaudeError().Message} {
		assert.Contains(t, message, "generationConfig.maxOutputTokens")
		assert.NotContains(t, message, "***")
	}
}
