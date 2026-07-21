package common

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

// ---------------------------------------------------------------------------
// GetFullRequestURL
// ---------------------------------------------------------------------------

func TestGetFullRequestURL(t *testing.T) {
	t.Run("plain concatenation", func(t *testing.T) {
		got := GetFullRequestURL("https://api.openai.com", "/v1/chat/completions", constant.ChannelTypeOpenAI)
		assert.Equal(t, "https://api.openai.com/v1/chat/completions", got)
	})
	t.Run("cloudflare gateway strips /v1 for openai", func(t *testing.T) {
		base := "https://gateway.ai.cloudflare.com/v1/acct/gw/openai"
		got := GetFullRequestURL(base, "/v1/chat/completions", constant.ChannelTypeOpenAI)
		assert.Equal(t, base+"/chat/completions", got)
	})
	t.Run("cloudflare gateway strips openai deployments for azure", func(t *testing.T) {
		base := "https://gateway.ai.cloudflare.com/v1/acct/gw/azure-openai"
		got := GetFullRequestURL(base, "/openai/deployments/gpt4/chat", constant.ChannelTypeAzure)
		assert.Equal(t, base+"/gpt4/chat", got)
	})
	t.Run("cloudflare gateway other channel unchanged", func(t *testing.T) {
		base := "https://gateway.ai.cloudflare.com/v1/acct/gw/x"
		got := GetFullRequestURL(base, "/v1/messages", constant.ChannelTypeAnthropic)
		assert.Equal(t, base+"/v1/messages", got)
	})
}

// ---------------------------------------------------------------------------
// SanitizeURLForLog
// ---------------------------------------------------------------------------

func TestSanitizeURLForLog(t *testing.T) {
	t.Run("empty stays empty", func(t *testing.T) {
		assert.Equal(t, "", SanitizeURLForLog(""))
	})
	t.Run("unparseable returned as-is", func(t *testing.T) {
		raw := "://bad url with spaces"
		assert.Equal(t, raw, SanitizeURLForLog(raw))
	})
	t.Run("no query returned as-is", func(t *testing.T) {
		raw := "https://example.test/v1/chat"
		assert.Equal(t, raw, SanitizeURLForLog(raw))
	})
	t.Run("no sensitive query returned as-is", func(t *testing.T) {
		raw := "https://example.test/v1/chat?alt=sse&api-version=2024-02-01"
		assert.Equal(t, raw, SanitizeURLForLog(raw))
	})
	t.Run("masks sensitive values", func(t *testing.T) {
		raw := "https://example.test/g?alt=sse&key=sk-secret&access_token=ya29-secret&signature=abc"
		got := SanitizeURLForLog(raw)
		assert.NotContains(t, got, "sk-secret")
		assert.NotContains(t, got, "ya29-secret")
		u, err := url.Parse(got)
		require.NoError(t, err)
		q := u.Query()
		assert.Equal(t, "***masked***", q.Get("key"))
		assert.Equal(t, "***masked***", q.Get("access_token"))
		assert.Equal(t, "***masked***", q.Get("signature"))
		assert.Equal(t, "sse", q.Get("alt"))
	})
	t.Run("masks AWS and secret-like keys", func(t *testing.T) {
		raw := "https://example.test/p?X-Amz-Credential=c&X-Amz-Signature=s&session_token=x&client_secret=y&model=gpt"
		got := SanitizeURLForLog(raw)
		u, err := url.Parse(got)
		require.NoError(t, err)
		q := u.Query()
		assert.Equal(t, "***masked***", q.Get("X-Amz-Credential"))
		assert.Equal(t, "***masked***", q.Get("X-Amz-Signature"))
		assert.Equal(t, "***masked***", q.Get("session_token"))
		assert.Equal(t, "***masked***", q.Get("client_secret"))
		assert.Equal(t, "gpt", q.Get("model"))
	})
}

func TestIsSensitiveURLQueryKey(t *testing.T) {
	sensitive := []string{"key", "api_key", "API-KEY", " token ", "authorization", "password",
		"x-amz-signature", "my_secret", "session_token", "some_signature_thing"}
	for _, k := range sensitive {
		assert.True(t, isSensitiveURLQueryKey(k), "expected sensitive: %q", k)
	}
	nonSensitive := []string{"model", "alt", "api-version", "temperature", ""}
	for _, k := range nonSensitive {
		assert.False(t, isSensitiveURLQueryKey(k), "expected non-sensitive: %q", k)
	}
}

// ---------------------------------------------------------------------------
// GetAPIVersion
// ---------------------------------------------------------------------------

func TestGetAPIVersion(t *testing.T) {
	t.Run("from query", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/chat?api-version=2024-01-01", nil)
		assert.Equal(t, "2024-01-01", GetAPIVersion(c))
	})
	t.Run("from context when query missing", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/chat", nil)
		c.Set("api_version", "ctx-version")
		assert.Equal(t, "ctx-version", GetAPIVersion(c))
	})
}

// ---------------------------------------------------------------------------
// Task request storage / retrieval
// ---------------------------------------------------------------------------

func TestGetTaskRequest(t *testing.T) {
	t.Run("missing returns error", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		_, err := GetTaskRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})
	t.Run("wrong type returns error", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("task_request", "not a TaskSubmitReq")
		_, err := GetTaskRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid task request type")
	})
	t.Run("round trip", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		storeTaskRequest(c, info, "generate", TaskSubmitReq{Prompt: "p", Model: "m"})
		assert.Equal(t, "generate", info.Action)
		got, err := GetTaskRequest(c)
		require.NoError(t, err)
		assert.Equal(t, "p", got.Prompt)
	})
}

// ---------------------------------------------------------------------------
// ValidateMultipartDirect
// ---------------------------------------------------------------------------

func newTaskContext(t *testing.T, body string) (*gin.Context, *RelayInfo) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c, &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
}

func TestValidateMultipartDirect(t *testing.T) {
	t.Run("normalizes single image and sets generate action", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"wan2.7-i2v","prompt":"animate","image":" https://x.com/first.png "}`)
		require.Nil(t, ValidateMultipartDirect(c, info))
		got, err := GetTaskRequest(c)
		require.NoError(t, err)
		assert.Equal(t, []string{"https://x.com/first.png"}, got.Images)
		assert.Equal(t, constant.TaskActionGenerate, info.Action)
	})
	t.Run("text-only sets text-generate action", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"sora-2","prompt":"a cat","seconds":"4"}`)
		require.Nil(t, ValidateMultipartDirect(c, info))
		assert.Equal(t, constant.TaskActionTextGenerate, info.Action)
	})
	t.Run("missing model rejected", func(t *testing.T) {
		c, info := newTaskContext(t, `{"prompt":"a cat"}`)
		te := ValidateMultipartDirect(c, info)
		require.NotNil(t, te)
		assert.Equal(t, "missing_model", te.Code)
	})
	t.Run("empty prompt rejected", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"sora-2","prompt":"   "}`)
		te := ValidateMultipartDirect(c, info)
		require.NotNil(t, te)
		assert.Equal(t, http.StatusBadRequest, te.StatusCode)
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c, info := newTaskContext(t, `not json`)
		te := ValidateMultipartDirect(c, info)
		require.NotNil(t, te)
		assert.Equal(t, "invalid_json", te.Code)
	})
	t.Run("input_reference becomes images", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"sora-2","prompt":"cat","input_reference":"ref.png"}`)
		require.Nil(t, ValidateMultipartDirect(c, info))
		got, _ := GetTaskRequest(c)
		assert.Equal(t, []string{"ref.png"}, got.Images)
		assert.Equal(t, constant.TaskActionGenerate, info.Action)
	})
	t.Run("sora-2 defaults size and seconds", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"sora-2","prompt":"cat"}`)
		require.Nil(t, ValidateMultipartDirect(c, info))
	})
	t.Run("sora-2 invalid size rejected", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"sora-2","prompt":"cat","size":"999x999"}`)
		te := ValidateMultipartDirect(c, info)
		require.NotNil(t, te)
		assert.Equal(t, "invalid_size", te.Code)
	})
	t.Run("sora-2-pro allows larger size", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"sora-2-pro","prompt":"cat","size":"1792x1024"}`)
		require.Nil(t, ValidateMultipartDirect(c, info))
	})
}

// ---------------------------------------------------------------------------
// Task duration bounds (billing invariant)
// ---------------------------------------------------------------------------

func TestTaskDurationBounds(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"huge duration rejected", `{"model":"sora-2","prompt":"a","duration":9999999999}`, true},
		{"huge seconds string rejected", `{"model":"sora-2","prompt":"a","seconds":"9999999999"}`, true},
		{"negative duration rejected", `{"model":"sora-2","prompt":"a","duration":-8}`, true},
		{"boundary at max accepted", `{"model":"sora-2","prompt":"a","seconds":"3600"}`, false},
		{"normal accepted", `{"model":"sora-2","prompt":"a","seconds":"8"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/direct", func(t *testing.T) {
			c, info := newTaskContext(t, tt.body)
			te := ValidateMultipartDirect(c, info)
			if tt.wantErr {
				require.NotNil(t, te)
				assert.Equal(t, "invalid_seconds", te.Code)
			} else {
				require.Nil(t, te)
			}
		})
		t.Run(tt.name+"/basic", func(t *testing.T) {
			c, info := newTaskContext(t, tt.body)
			te := ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
			if tt.wantErr {
				require.NotNil(t, te)
				assert.Equal(t, "invalid_seconds", te.Code)
			} else {
				require.Nil(t, te)
			}
		})
	}
}

func TestValidateBasicTaskRequest(t *testing.T) {
	t.Run("empty prompt rejected", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"sora-2","prompt":""}`)
		te := ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
		require.NotNil(t, te)
	})
	t.Run("single image normalized", func(t *testing.T) {
		c, info := newTaskContext(t, `{"model":"sora-2","prompt":"cat","image":"img.png"}`)
		require.Nil(t, ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate))
		got, _ := GetTaskRequest(c)
		assert.Equal(t, []string{"img.png"}, got.Images)
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c, info := newTaskContext(t, `bad`)
		te := ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
		require.NotNil(t, te)
		assert.Equal(t, "invalid_request", te.Code)
	})
}

func TestValidateTaskDurationBoundsHelper(t *testing.T) {
	assert.Nil(t, validateTaskDurationBounds(TaskSubmitReq{Duration: 0}))
	assert.Nil(t, validateTaskDurationBounds(TaskSubmitReq{Seconds: "10"}))
	assert.Nil(t, validateTaskDurationBounds(TaskSubmitReq{Duration: MaxTaskDurationSeconds}))
	assert.NotNil(t, validateTaskDurationBounds(TaskSubmitReq{Duration: MaxTaskDurationSeconds + 1}))
	assert.NotNil(t, validateTaskDurationBounds(TaskSubmitReq{Duration: -1}))
}
