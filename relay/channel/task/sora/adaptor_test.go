package sora

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
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

func newCtx(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
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
// ValidateRequestAndSetAction — direct + remix paths
// ---------------------------------------------------------------------------

func TestValidate_DirectTextGenerate(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","model":"sora-2","size":"720x1280"}`)
	info := newInfo()
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionTextGenerate, info.Action)
}

func TestValidate_DirectMissingModel(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat"}`)
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, "missing_model", taskErr.Code)
}

func TestValidate_DirectInvalidSize(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","model":"sora-2","size":"999x999"}`)
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_size", taskErr.Code)
}

func TestValidate_RemixOK(t *testing.T) {
	c, _ := newCtx(`{"prompt":"make it rain"}`)
	info := newInfo()
	info.Action = constant.TaskActionRemix
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	_, ok := c.Get("task_request")
	assert.True(t, ok)
}

func TestValidate_RemixMissingPrompt(t *testing.T) {
	c, _ := newCtx(`{"prompt":"   "}`)
	info := newInfo()
	info.Action = constant.TaskActionRemix
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
	require.NotNil(t, taskErr)
	assert.Contains(t, taskErr.Message, "prompt is required")
}

func TestValidate_RemixMalformed(t *testing.T) {
	c, _ := newCtx(`{bad`)
	info := newInfo()
	info.Action = constant.TaskActionRemix
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_request", taskErr.Code)
}

// ---------------------------------------------------------------------------
// EstimateBilling
// ---------------------------------------------------------------------------

func TestEstimateBilling_RemixReturnsNil(t *testing.T) {
	c, _ := newCtx("")
	info := newInfo()
	info.Action = constant.TaskActionRemix
	assert.Nil(t, (&TaskAdaptor{}).EstimateBilling(c, info))
}

func TestEstimateBilling_DefaultsWhenNoRequest(t *testing.T) {
	c, _ := newCtx("")
	// no task_request set -> GetTaskRequest errors -> nil
	assert.Nil(t, (&TaskAdaptor{}).EstimateBilling(c, newInfo()))
}

func TestEstimateBilling_SecondsAndSize(t *testing.T) {
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Seconds: "8", Size: "1792x1024"})
	ratios := (&TaskAdaptor{}).EstimateBilling(c, newInfo())
	require.NotNil(t, ratios)
	assert.Equal(t, float64(8), ratios["seconds"])
	assert.InDelta(t, 1.666667, ratios["size"], 0.0001)
}

func TestEstimateBilling_DurationFallbackAndDefaultSize(t *testing.T) {
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Duration: 6})
	ratios := (&TaskAdaptor{}).EstimateBilling(c, newInfo())
	require.NotNil(t, ratios)
	assert.Equal(t, float64(6), ratios["seconds"])
	assert.Equal(t, float64(1), ratios["size"]) // default 720x1280
}

func TestEstimateBilling_ZeroDefaultsToFour(t *testing.T) {
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{})
	ratios := (&TaskAdaptor{}).EstimateBilling(c, newInfo())
	require.NotNil(t, ratios)
	assert.Equal(t, float64(4), ratios["seconds"])
}

// ---------------------------------------------------------------------------
// BuildRequestURL / BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://api.sora"}
	info := newInfo()
	url, err := a.BuildRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.sora/v1/videos", url)

	info.Action = constant.TaskActionRemix
	info.OriginTaskID = "up-77"
	url, _ = a.BuildRequestURL(info)
	assert.Equal(t, "https://api.sora/v1/videos/up-77/remix", url)
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sk-sora"}
	c, _ := newCtx("")
	c.Request.Header.Set("Content-Type", "application/json")
	req := httptest.NewRequest(http.MethodPost, "https://api.sora", nil)
	require.NoError(t, a.BuildRequestHeader(c, req, newInfo()))
	assert.Equal(t, "Bearer sk-sora", req.Header.Get("Authorization"))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
}

// ---------------------------------------------------------------------------
// BuildRequestBody — JSON path injects model
// ---------------------------------------------------------------------------

func TestBuildRequestBody_JSONInjectsModel(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","model":"client-model"}`)
	info := newInfo()
	info.UpstreamModelName = "sora-2-pro"
	reader, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, `"model":"sora-2-pro"`)
	assert.Contains(t, s, `"prompt":"a cat"`)
}

func TestBuildRequestBody_JSONInvalidPassThrough(t *testing.T) {
	c, _ := newCtx(`not-json-body`)
	info := newInfo()
	info.UpstreamModelName = "sora-2"
	reader, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	assert.Equal(t, "not-json-body", string(body))
}

func TestBuildRequestBody_MultipartRewritesModelAndFile(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("model", "client-model")
	_ = writer.WriteField("prompt", "a cat")
	part, err := writer.CreateFormFile("input_reference", "frame.png")
	require.NoError(t, err)
	// minimal PNG signature so DetectContentType has bytes to sniff
	_, _ = part.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01})
	require.NoError(t, writer.Close())

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	info := newInfo()
	info.UpstreamModelName = "sora-2-pro"
	reader, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	out, _ := io.ReadAll(reader)
	s := string(out)
	// upstream model must be injected; client model must be dropped
	assert.Contains(t, s, "sora-2-pro")
	assert.NotContains(t, s, "client-model")
	assert.Contains(t, s, "frame.png")
	// Content-Type header rewritten to the new multipart boundary
	assert.True(t, strings.HasPrefix(c.Request.Header.Get("Content-Type"), "multipart/form-data"))
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func doResp(t *testing.T, body string) (*httptest.ResponseRecorder, string, []byte, *dto.TaskError) {
	t.Helper()
	c, rec := newCtx("")
	info := newInfo()
	info.PublicTaskID = "task_pub"
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	id, data, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	return rec, id, data, taskErr
}

func TestDoResponse_SuccessWithID(t *testing.T) {
	rec, id, data, taskErr := doResp(t, `{"id":"video_up_1","status":"queued"}`)
	require.Nil(t, taskErr)
	assert.Equal(t, "video_up_1", id)
	assert.NotEmpty(t, data)
	assert.Contains(t, rec.Body.String(), "task_pub")
	assert.NotContains(t, rec.Body.String(), "video_up_1")
}

func TestDoResponse_FallsBackToTaskID(t *testing.T) {
	_, id, _, taskErr := doResp(t, `{"task_id":"legacy_id","status":"queued"}`)
	require.Nil(t, taskErr)
	assert.Equal(t, "legacy_id", id)
}

func TestDoResponse_EmptyID(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{"status":"queued"}`)
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_response", taskErr.Code)
}

func TestDoResponse_Malformed(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{bad`)
	require.NotNil(t, taskErr)
	assert.Equal(t, "unmarshal_response_body_failed", taskErr.Code)
}

// ---------------------------------------------------------------------------
// ParseTaskResult — status mapping
// ---------------------------------------------------------------------------

func TestParseTaskResult_Statuses(t *testing.T) {
	a := &TaskAdaptor{}

	for _, s := range []string{"queued", "pending"} {
		info, err := a.ParseTaskResult([]byte(`{"status":"` + s + `"}`))
		require.NoError(t, err)
		assert.Equal(t, model.TaskStatusQueued, info.Status, s)
	}
	for _, s := range []string{"processing", "in_progress"} {
		info, err := a.ParseTaskResult([]byte(`{"status":"` + s + `","progress":40}`))
		require.NoError(t, err)
		assert.Equal(t, model.TaskStatusInProgress, info.Status, s)
		assert.Equal(t, "40%", info.Progress)
	}

	info, err := a.ParseTaskResult([]byte(`{"status":"completed"}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, info.Status)
	assert.Empty(t, info.Url) // caller builds proxy URL

	info, err = a.ParseTaskResult([]byte(`{"status":"failed","error":{"message":"nsfw","code":"blocked"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, info.Status)
	assert.Equal(t, "nsfw", info.Reason)

	info, err = a.ParseTaskResult([]byte(`{"status":"cancelled"}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, info.Status)
	assert.Equal(t, "task failed", info.Reason)
}

func TestParseTaskResult_UnknownStatusNoError(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"status":"weird"}`))
	require.NoError(t, err)
	assert.Empty(t, info.Status)
}

func TestParseTaskResult_ProgressBoundaries(t *testing.T) {
	// progress 100 must NOT render a percent string (only 0<p<100 does)
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"status":"completed","progress":100}`))
	require.NoError(t, err)
	assert.Empty(t, info.Progress)
}

func TestParseTaskResult_Malformed(t *testing.T) {
	_, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{bad`))
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// FetchTask
// ---------------------------------------------------------------------------

func TestFetchTask_OK(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":"processing"}`))
	}))
	defer srv.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(srv.URL, "sk-key", map[string]any{"task_id": "T-3"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/v1/videos/T-3", gotPath)
	assert.Equal(t, "Bearer sk-key", gotAuth)
}

func TestFetchTask_MissingTaskID(t *testing.T) {
	_, err := (&TaskAdaptor{}).FetchTask("http://x", "k", map[string]any{}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_id")
}

// ---------------------------------------------------------------------------
// ConvertToOpenAIVideo — injects public task id
// ---------------------------------------------------------------------------

func TestConvertToOpenAIVideo_SetsID(t *testing.T) {
	task := &model.Task{
		TaskID: "task_pub",
		Data:   []byte(`{"id":"up-1","status":"completed"}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"id":"task_pub"`)
	assert.NotContains(t, string(body), `"id":"up-1"`)
}

// ---------------------------------------------------------------------------
// Misc
// ---------------------------------------------------------------------------

func TestInitAndMeta(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 1, ChannelBaseUrl: "https://b", ApiKey: "k"}})
	assert.Equal(t, "https://b", a.baseURL)
	assert.Equal(t, "k", a.apiKey)
	assert.Equal(t, "sora", a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())
	assert.Contains(t, a.GetModelList(), "sora-2-pro")
}
