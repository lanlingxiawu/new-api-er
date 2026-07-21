package suno

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	service.InitHttpClient()
	os.Exit(m.Run())
}

func newTestContext(method, target, body string, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, rec
}

func newRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction + actionValidate
// ---------------------------------------------------------------------------

func TestValidateRequestAndSetAction_MusicDefaultsMv(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/suno/submit/music", `{"prompt":"a song"}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "music"}}
	info := newRelayInfo()

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "MUSIC", info.Action)

	raw, ok := c.Get("task_request")
	require.True(t, ok)
	req := raw.(*dto.SunoSubmitReq)
	assert.Equal(t, "chirp-v3-0", req.Mv, "empty mv should default")
}

func TestValidateRequestAndSetAction_MusicKeepsProvidedMv(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/suno/submit/music", `{"prompt":"x","mv":"chirp-v4"}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "MUSIC"}}
	info := newRelayInfo()

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
	require.Nil(t, taskErr)
	req, _ := c.Get("task_request")
	assert.Equal(t, "chirp-v4", req.(*dto.SunoSubmitReq).Mv)
}

func TestValidateRequestAndSetAction_LyricsRequiresPrompt(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/suno/submit/lyrics", `{}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "lyrics"}}

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newRelayInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Contains(t, taskErr.Message, "prompt_empty")
}

func TestValidateRequestAndSetAction_LyricsWithPromptOK(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/suno/submit/lyrics", `{"prompt":"lyrics here"}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "lyrics"}}
	info := newRelayInfo()

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "LYRICS", info.Action)
}

func TestValidateRequestAndSetAction_InvalidAction(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/suno/submit/dance", `{"prompt":"x"}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "dance"}}

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newRelayInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Contains(t, taskErr.Message, "invalid_action")
}

func TestValidateRequestAndSetAction_MalformedBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/suno/submit/music", `{not-json`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "music"}}

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newRelayInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "invalid_request", taskErr.Code)
}

// ---------------------------------------------------------------------------
// Init / BuildRequestURL / BuildRequestHeader / BuildRequestBody
// ---------------------------------------------------------------------------

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 7}})
	assert.Equal(t, 7, a.ChannelType)
}

func TestBuildRequestURL(t *testing.T) {
	info := newRelayInfo()
	info.ChannelBaseUrl = "https://up.example.com"
	info.Action = "MUSIC"
	url, err := (&TaskAdaptor{}).BuildRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://up.example.com/suno/submit/MUSIC", url)
}

func TestBuildRequestHeader(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/x", "", "")
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "application/json")
	info := newRelayInfo()
	info.ApiKey = "sk-secret"

	req := httptest.NewRequest(http.MethodPost, "https://up.example.com/suno/submit/MUSIC", nil)
	err := (&TaskAdaptor{}).BuildRequestHeader(c, req, info)
	require.NoError(t, err)
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.Equal(t, "Bearer sk-secret", req.Header.Get("Authorization"))
}

func TestBuildRequestBody_UsesContextRequest(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/x", "", "")
	sunoReq := &dto.SunoSubmitReq{Prompt: "a", Mv: "chirp-v3-0"}
	c.Set("task_request", sunoReq)

	reader, err := (&TaskAdaptor{}).BuildRequestBody(c, newRelayInfo())
	require.NoError(t, err)
	data, _ := io.ReadAll(reader)
	assert.Contains(t, string(data), `"prompt":"a"`)
}

func TestBuildRequestBody_MissingContext(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/x", "", "")
	_, err := (&TaskAdaptor{}).BuildRequestBody(c, newRelayInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_request not found")
}

// ---------------------------------------------------------------------------
// DoResponse — submit-response parsing
// ---------------------------------------------------------------------------

func doResponse(t *testing.T, upstreamBody string, statusCode int) (*gin.Context, *httptest.ResponseRecorder, string, []byte, *dto.TaskError) {
	t.Helper()
	c, rec := newTestContext(http.MethodPost, "/suno/submit/music", "", "")
	info := newRelayInfo()
	info.PublicTaskID = "task_public_123"

	resp := &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
		Header:     make(http.Header),
	}
	id, data, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	return c, rec, id, data, taskErr
}

func TestDoResponse_Success(t *testing.T) {
	_, rec, id, data, taskErr := doResponse(t, `{"code":"success","message":"ok","data":"upstream_task_id"}`, http.StatusOK)
	require.Nil(t, taskErr)
	assert.Equal(t, "upstream_task_id", id)
	assert.Nil(t, data)
	// Client must see the public task ID, never the upstream ID.
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "task_public_123")
	assert.NotContains(t, rec.Body.String(), "upstream_task_id")
}

func TestDoResponse_UpstreamFailureCode(t *testing.T) {
	_, _, id, _, taskErr := doResponse(t, `{"code":"failure","message":"quota exceeded"}`, http.StatusOK)
	require.NotNil(t, taskErr)
	assert.Empty(t, id)
	assert.Equal(t, "failure", taskErr.Code)
	assert.Contains(t, taskErr.Message, "quota exceeded")
}

func TestDoResponse_MalformedJSON(t *testing.T) {
	_, _, _, _, taskErr := doResponse(t, `{not json`, http.StatusOK)
	require.NotNil(t, taskErr)
	assert.Equal(t, "unmarshal_response_body_failed", taskErr.Code)
	assert.Equal(t, http.StatusInternalServerError, taskErr.StatusCode)
}

// ---------------------------------------------------------------------------
// FetchTask — batch poll endpoint
// ---------------------------------------------------------------------------

func TestFetchTask_PostsToFetchEndpoint(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"success","data":[]}`))
	}))
	defer srv.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(srv.URL, "key-xyz", map[string]any{"ids": []string{"a", "b"}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/suno/fetch", gotPath)
	assert.Equal(t, "Bearer key-xyz", gotAuth)
	assert.Contains(t, gotBody, `"ids"`)
}

func TestFetchTask_InvalidProxyReturnsError(t *testing.T) {
	_, err := (&TaskAdaptor{}).FetchTask("http://127.0.0.1:0", "key", map[string]any{"ids": []string{}}, "://bad-proxy")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Misc surface
// ---------------------------------------------------------------------------

func TestParseTaskResult_NotApplicable(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{}`))
	require.Error(t, err)
	assert.Nil(t, info)
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	assert.Equal(t, ModelList, a.GetModelList())
	assert.Equal(t, "suno", a.GetChannelName())
	assert.Contains(t, a.GetModelList(), "suno_music")
}
