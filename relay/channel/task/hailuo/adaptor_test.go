package hailuo

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	service.InitHttpClient()
	os.Exit(m.Run())
}

func newCtx(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/video_generation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, rec
}

func newInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
}

// ---------------------------------------------------------------------------
// GetModelConfig (models.go)
// ---------------------------------------------------------------------------

func TestGetModelConfig_Known(t *testing.T) {
	cfg := GetModelConfig("MiniMax-Hailuo-02")
	assert.Equal(t, Resolution768P, cfg.DefaultResolution)
	assert.Contains(t, cfg.SupportedResolutions, Resolution512P)
}

func TestGetModelConfig_UnknownFallback(t *testing.T) {
	cfg := GetModelConfig("no-such-model")
	assert.Equal(t, "no-such-model", cfg.Name)
	assert.Equal(t, DefaultResolution, cfg.DefaultResolution)
}

// ---------------------------------------------------------------------------
// parseResolutionFromSize
// ---------------------------------------------------------------------------

func TestParseResolutionFromSize(t *testing.T) {
	a := &TaskAdaptor{}
	cfg := GetModelConfig("MiniMax-Hailuo-02")
	assert.Equal(t, Resolution1080P, a.parseResolutionFromSize("1920x1080", cfg))
	assert.Equal(t, Resolution768P, a.parseResolutionFromSize("768x768", cfg))
	assert.Equal(t, Resolution720P, a.parseResolutionFromSize("1280x720", cfg))
	assert.Equal(t, Resolution512P, a.parseResolutionFromSize("512x512", cfg))
	// no match -> model default
	assert.Equal(t, cfg.DefaultResolution, a.parseResolutionFromSize("999x999", cfg))
}

// ---------------------------------------------------------------------------
// convertToRequestPayload / BuildRequestBody
// ---------------------------------------------------------------------------

func TestBuildRequestBody_Defaults(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "a cat"})
	info := newInfo()
	info.UpstreamModelName = "MiniMax-Hailuo-02"
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, `"model":"MiniMax-Hailuo-02"`)
	assert.Contains(t, s, `"duration":6`)
	assert.Contains(t, s, `"resolution":"768P"`)
}

func TestBuildRequestBody_SizeAndDurationOverride(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "a cat", Duration: 10, Size: "1920x1080"})
	info := newInfo()
	info.UpstreamModelName = "MiniMax-Hailuo-2.3"
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, `"duration":10`)
	assert.Contains(t, s, `"resolution":"1080P"`)
}

func TestBuildRequestBody_MetadataMerge(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a cat",
		Metadata: map[string]interface{}{
			"first_frame_image": "https://i/first.png",
		},
	})
	info := newInfo()
	info.UpstreamModelName = "I2V-01"
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	assert.Contains(t, string(body), `"first_frame_image":"https://i/first.png"`)
}

func TestBuildRequestBody_MissingContext(t *testing.T) {
	c, _ := newCtx("")
	_, err := (&TaskAdaptor{}).BuildRequestBody(c, newInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request not found")
}

func TestBuildRequestBody_WrongType(t *testing.T) {
	c, _ := newCtx("")
	c.Set("task_request", "not-a-req")
	_, err := (&TaskAdaptor{}).BuildRequestBody(c, newInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid request type")
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction / URL / Header
// ---------------------------------------------------------------------------

func TestValidate_OK(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat"}`)
	info := newInfo()
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
}

func TestValidate_MissingPrompt(t *testing.T) {
	c, _ := newCtx(`{"prompt":""}`)
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://api.minimax"}
	url, err := a.BuildRequestURL(newInfo())
	require.NoError(t, err)
	assert.Equal(t, "https://api.minimax/v1/video_generation", url)
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sk-hailuo"}
	c, _ := newCtx("")
	req := httptest.NewRequest(http.MethodPost, "https://api.minimax", nil)
	require.NoError(t, a.BuildRequestHeader(c, req, newInfo()))
	assert.Equal(t, "Bearer sk-hailuo", req.Header.Get("Authorization"))
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func doResp(t *testing.T, body string) (*httptest.ResponseRecorder, string, []byte, *taskdto.TaskError) {
	t.Helper()
	c, rec := newCtx("")
	info := newInfo()
	info.PublicTaskID = "task_pub"
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	id, data, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	return rec, id, data, taskErr
}

func TestDoResponse_Success(t *testing.T) {
	rec, id, data, taskErr := doResp(t, `{"task_id":"up-1","base_resp":{"status_code":0,"status_msg":"success"}}`)
	require.Nil(t, taskErr)
	assert.Equal(t, "up-1", id)
	assert.NotEmpty(t, data)
	assert.Contains(t, rec.Body.String(), "task_pub")
	assert.NotContains(t, rec.Body.String(), "up-1")
}

func TestDoResponse_UpstreamError(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{"base_resp":{"status_code":1026,"status_msg":"sensitive content"}}`)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "1026", taskErr.Code)
	assert.Contains(t, taskErr.Message, "sensitive content")
}

func TestDoResponse_Malformed(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{bad`)
	require.NotNil(t, taskErr)
	assert.Equal(t, "unmarshal_response_body_failed", taskErr.Code)
}

// ---------------------------------------------------------------------------
// ParseTaskResult — status mapping
// ---------------------------------------------------------------------------

func TestParseTaskResult_InProgressStates(t *testing.T) {
	a := &TaskAdaptor{}
	for _, s := range []string{TaskStatusPreparing, TaskStatusQueueing} {
		info, err := a.ParseTaskResult([]byte(`{"status":"` + s + `","base_resp":{"status_code":0}}`))
		require.NoError(t, err)
		assert.Equal(t, model.TaskStatusInProgress, info.Status, s)
		assert.Equal(t, "30%", info.Progress)
	}
	info, err := a.ParseTaskResult([]byte(`{"status":"Processing","base_resp":{"status_code":0}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, info.Status)
	assert.Equal(t, "50%", info.Progress)
}

func TestParseTaskResult_SuccessBuildsURL(t *testing.T) {
	// buildVideoURL needs apiKey+baseURL; here empty so URL is "".
	a := &TaskAdaptor{}
	info, err := a.ParseTaskResult([]byte(`{"status":"Success","task_id":"t","file_id":"f","base_resp":{"status_code":0}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, info.Status)
	assert.Equal(t, "100%", info.Progress)
	assert.Empty(t, info.Url)
}

func TestParseTaskResult_SuccessRetrievesFileURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/files/retrieve", r.URL.Path)
		assert.Equal(t, "f-123", r.URL.Query().Get("file_id"))
		_, _ = w.Write([]byte(`{"file":{"download_url":"https://cdn/out.mp4"},"base_resp":{"status_code":0}}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{apiKey: "k", baseURL: srv.URL}
	info, err := a.ParseTaskResult([]byte(`{"status":"Success","task_id":"t","file_id":"f-123","base_resp":{"status_code":0}}`))
	require.NoError(t, err)
	assert.Equal(t, "https://cdn/out.mp4", info.Url)
}

func TestParseTaskResult_Failed(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"status":"Fail","base_resp":{"status_code":0}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, info.Status)
	assert.Equal(t, "task failed", info.Reason)
}

// NOTE (latent bug): when base_resp carries an error but the task `status`
// field is empty/unknown, the base_resp block first sets Status=FAILURE, but
// the subsequent switch default case unconditionally overwrites it back to
// IN_PROGRESS. Code and Reason still reflect the error. This test pins the
// ACTUAL behavior; the FAILURE status is silently lost. See batch doc.
func TestParseTaskResult_BaseRespErrorWithEmptyStatus(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"status":"","base_resp":{"status_code":1004,"status_msg":"auth failed"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, info.Status) // FAILURE overwritten by default case
	assert.Equal(t, 1004, info.Code)
	assert.Equal(t, "auth failed", info.Reason)
}

// When base_resp errors AND status is an explicit failure, FAILURE survives.
func TestParseTaskResult_BaseRespErrorWithFailStatus(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"status":"Fail","base_resp":{"status_code":1004,"status_msg":"auth failed"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, info.Status)
	assert.Equal(t, "auth failed", info.Reason)
}

func TestParseTaskResult_UnknownStatus(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"status":"weird","base_resp":{"status_code":0}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, info.Status)
	assert.Equal(t, "30%", info.Progress)
}

func TestParseTaskResult_Malformed(t *testing.T) {
	_, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{bad`))
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// buildVideoURL — error paths
// ---------------------------------------------------------------------------

func TestBuildVideoURL_MissingCreds(t *testing.T) {
	a := &TaskAdaptor{}
	assert.Empty(t, a.buildVideoURL("t", "f"))
}

func TestBuildVideoURL_UpstreamErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"bad"}}`))
	}))
	defer srv.Close()
	a := &TaskAdaptor{apiKey: "k", baseURL: srv.URL}
	assert.Empty(t, a.buildVideoURL("t", "f"))
}

func TestBuildVideoURL_Malformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{bad`))
	}))
	defer srv.Close()
	a := &TaskAdaptor{apiKey: "k", baseURL: srv.URL}
	assert.Empty(t, a.buildVideoURL("t", "f"))
}

// ---------------------------------------------------------------------------
// FetchTask
// ---------------------------------------------------------------------------

func TestFetchTask_OK(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("task_id")
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":"Processing","base_resp":{"status_code":0}}`))
	}))
	defer srv.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(srv.URL, "sk-k", map[string]any{"task_id": "T-42"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/v1/query/video_generation", gotPath)
	assert.Equal(t, "T-42", gotQuery)
	assert.Equal(t, "Bearer sk-k", gotAuth)
}

func TestFetchTask_MissingTaskID(t *testing.T) {
	_, err := (&TaskAdaptor{}).FetchTask("http://x", "k", map[string]any{}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_id")
}

// ---------------------------------------------------------------------------
// ConvertToOpenAIVideo
// ---------------------------------------------------------------------------

func TestConvertToOpenAIVideo_Success(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_pub",
		Status:   model.TaskStatusSuccess,
		Progress: "100%",
		Data:     []byte(`{"status":"Success","base_resp":{"status_code":0}}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var v dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &v))
	assert.Nil(t, v.Error)
}

func TestConvertToOpenAIVideo_Error(t *testing.T) {
	task := &model.Task{
		TaskID: "task_pub",
		Status: model.TaskStatusFailure,
		Data:   []byte(`{"status":"Fail","base_resp":{"status_code":1026,"status_msg":"blocked"}}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var v dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &v))
	require.NotNil(t, v.Error)
	assert.Equal(t, "1026", v.Error.Code)
	assert.Equal(t, "blocked", v.Error.Message)
}

func TestConvertToOpenAIVideo_Malformed(t *testing.T) {
	_, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(&model.Task{Data: []byte(`{bad`)})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Misc + dead-code helpers
// ---------------------------------------------------------------------------

func TestInitAndMeta(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 2, ChannelBaseUrl: "https://b", ApiKey: "k"}})
	assert.Equal(t, "https://b", a.baseURL)
	assert.Equal(t, "k", a.apiKey)
	assert.Equal(t, "hailuo-video", a.GetChannelName())
	assert.Contains(t, a.GetModelList(), "MiniMax-Hailuo-02")
}

func TestContainsHelpers(t *testing.T) {
	assert.True(t, contains([]string{"a", "b"}, "b"))
	assert.False(t, contains([]string{"a"}, "z"))
	assert.True(t, containsInt([]int{1, 2}, 2))
	assert.False(t, containsInt([]int{1}, 9))
}
