package doubao

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
	req := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
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
// GetVideoInputRatio (constants.go pricing matrix)
// ---------------------------------------------------------------------------

func TestGetVideoInputRatio_UnknownModel(t *testing.T) {
	ratio, ok := GetVideoInputRatio("nonexistent-model", "1080p", false)
	assert.False(t, ok)
	assert.Equal(t, 0.0, ratio)
}

func TestGetVideoInputRatio_BaselineIsOne(t *testing.T) {
	// baseline key {480p/720p, no video} -> price/base = 1.0
	ratio, ok := GetVideoInputRatio("doubao-seedance-2-0-260128", "720p", false)
	assert.True(t, ok)
	assert.Equal(t, 1.0, ratio)
}

func TestGetVideoInputRatio_VideoInputDiscount(t *testing.T) {
	// {hasVideo:true} = 28 / base 46
	ratio, ok := GetVideoInputRatio("doubao-seedance-2-0-260128", "720p", true)
	assert.True(t, ok)
	assert.InDelta(t, 28.0/46.0, ratio, 1e-9)
}

func TestGetVideoInputRatio_1080pPremium(t *testing.T) {
	ratio, ok := GetVideoInputRatio("doubao-seedance-2-0-260128", "1080p", false)
	assert.True(t, ok)
	assert.InDelta(t, 51.0/46.0, ratio, 1e-9)
}

func TestGetVideoInputRatio_4kWithVideo(t *testing.T) {
	ratio, ok := GetVideoInputRatio("doubao-seedance-2-0-260128", "4k", true)
	assert.True(t, ok)
	assert.InDelta(t, 16.0/46.0, ratio, 1e-9)
}

func TestGetVideoInputRatio_UnconfiguredComboFallsBackToOne(t *testing.T) {
	// fast model has no 1080p key -> defaults to baseline 1.0
	ratio, ok := GetVideoInputRatio("doubao-seedance-2-0-fast-260128", "1080p", false)
	assert.True(t, ok)
	assert.Equal(t, 1.0, ratio)
}

// ---------------------------------------------------------------------------
// hasVideoInMetadata
// ---------------------------------------------------------------------------

func TestHasVideoInMetadata(t *testing.T) {
	assert.False(t, hasVideoInMetadata(nil))
	assert.False(t, hasVideoInMetadata(map[string]interface{}{}))
	assert.False(t, hasVideoInMetadata(map[string]interface{}{"content": "not-a-slice"}))

	// type == video_url
	assert.True(t, hasVideoInMetadata(map[string]interface{}{
		"content": []interface{}{map[string]interface{}{"type": "video_url"}},
	}))
	// video_url key present
	assert.True(t, hasVideoInMetadata(map[string]interface{}{
		"content": []interface{}{map[string]interface{}{"video_url": map[string]interface{}{"url": "x"}}},
	}))
	// only image
	assert.False(t, hasVideoInMetadata(map[string]interface{}{
		"content": []interface{}{map[string]interface{}{"type": "image_url"}},
	}))
}

// ---------------------------------------------------------------------------
// EstimateBilling
// ---------------------------------------------------------------------------

func TestEstimateBilling_NoRequestReturnsNil(t *testing.T) {
	c, _ := newCtx("")
	assert.Nil(t, (&TaskAdaptor{}).EstimateBilling(c, newInfo()))
}

func TestEstimateBilling_BaselineReturnsNil(t *testing.T) {
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"resolution": "720p"}})
	info := newInfo()
	info.OriginModelName = "doubao-seedance-2-0-260128"
	// ratio 1.0 -> nil
	assert.Nil(t, (&TaskAdaptor{}).EstimateBilling(c, info))
}

func TestEstimateBilling_VideoInputRatio(t *testing.T) {
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{
		"resolution": "720p",
		"content":    []interface{}{map[string]interface{}{"type": "video_url"}},
	}})
	info := newInfo()
	info.OriginModelName = "doubao-seedance-2-0-260128"
	ratios := (&TaskAdaptor{}).EstimateBilling(c, info)
	require.NotNil(t, ratios)
	assert.InDelta(t, 28.0/46.0, ratios["video_input"], 1e-9)
}

func TestEstimateBilling_UnknownModelReturnsNil(t *testing.T) {
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"resolution": "720p"}})
	info := newInfo()
	info.OriginModelName = "some-other-model"
	assert.Nil(t, (&TaskAdaptor{}).EstimateBilling(c, info))
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction / URL / Header
// ---------------------------------------------------------------------------

func TestValidate_OK(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","model":"doubao-seedance-2-0-260128"}`)
	info := newInfo()
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
}

func TestValidate_MissingPrompt(t *testing.T) {
	c, _ := newCtx(`{"model":"x"}`)
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://ark.doubao"}
	url, err := a.BuildRequestURL(newInfo())
	require.NoError(t, err)
	assert.Equal(t, "https://ark.doubao/api/v3/contents/generations/tasks", url)
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sk-doubao"}
	req := httptest.NewRequest(http.MethodPost, "https://ark.doubao", nil)
	require.NoError(t, a.BuildRequestHeader(nil, req, newInfo()))
	assert.Equal(t, "Bearer sk-doubao", req.Header.Get("Authorization"))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
}

// ---------------------------------------------------------------------------
// BuildRequestBody + convertToRequestPayload
// ---------------------------------------------------------------------------

func TestBuildRequestBody_ImagesAndPrompt(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "dance",
		Model:  "doubao-seedance-1-0-lite-i2v",
		Images: []string{"https://i/a.png"},
	})
	info := newInfo()
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, `"image_url"`)
	assert.Contains(t, s, `"text"`)
	assert.Contains(t, s, "dance")
	// not model-mapped -> upstream model set from body
	assert.Equal(t, "doubao-seedance-1-0-lite-i2v", info.UpstreamModelName)
}

func TestBuildRequestBody_ModelMappedUsesUpstream(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "x", Model: "client-model"})
	info := newInfo()
	info.IsModelMapped = true
	info.UpstreamModelName = "mapped-upstream"
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	assert.Contains(t, string(body), `"model":"mapped-upstream"`)
}

func TestBuildRequestBody_SecondsBecomeDuration(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "x", Model: "m", Seconds: "8"})
	reader, err := a.BuildRequestBody(c, newInfo())
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	assert.Contains(t, string(body), `"duration":8`)
}

func TestBuildRequestBody_MetadataTextRejected(t *testing.T) {
	// metadata content with a text item should be dropped and replaced by prompt text
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "real prompt",
		Model:  "m",
		Metadata: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "junk text"},
			},
		},
	})
	reader, err := a.BuildRequestBody(c, newInfo())
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, "real prompt")
	assert.NotContains(t, s, "junk text")
}

func TestBuildRequestBody_NoRequest(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	_, err := a.BuildRequestBody(c, newInfo())
	require.Error(t, err)
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

func TestDoResponse_Success(t *testing.T) {
	rec, id, data, taskErr := doResp(t, `{"id":"up-1"}`)
	require.Nil(t, taskErr)
	assert.Equal(t, "up-1", id)
	assert.NotEmpty(t, data)
	assert.Contains(t, rec.Body.String(), "task_pub")
	assert.NotContains(t, rec.Body.String(), "up-1")
}

func TestDoResponse_EmptyID(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{"id":""}`)
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

	for _, s := range []string{"pending", "queued"} {
		info, err := a.ParseTaskResult([]byte(`{"status":"` + s + `"}`))
		require.NoError(t, err)
		assert.Equal(t, model.TaskStatusQueued, info.Status, s)
		assert.Equal(t, "10%", info.Progress)
	}
	for _, s := range []string{"processing", "running"} {
		info, err := a.ParseTaskResult([]byte(`{"status":"` + s + `"}`))
		require.NoError(t, err)
		assert.Equal(t, model.TaskStatusInProgress, info.Status, s)
		assert.Equal(t, "50%", info.Progress)
	}

	info, err := a.ParseTaskResult([]byte(`{"status":"succeeded","content":{"video_url":"https://v/o.mp4"},"usage":{"completion_tokens":12,"total_tokens":20}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, info.Status)
	assert.Equal(t, "https://v/o.mp4", info.Url)
	assert.Equal(t, 12, info.CompletionTokens)
	assert.Equal(t, 20, info.TotalTokens)

	info, err = a.ParseTaskResult([]byte(`{"status":"failed","error":{"message":"blocked"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, info.Status)
	assert.Equal(t, "blocked", info.Reason)

	// unknown -> treated as in progress
	info, err = a.ParseTaskResult([]byte(`{"status":"weird"}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, info.Status)
	assert.Equal(t, "30%", info.Progress)
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

	resp, err := (&TaskAdaptor{}).FetchTask(srv.URL, "sk-key", map[string]any{"task_id": "T-9"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/api/v3/contents/generations/tasks/T-9", gotPath)
	assert.Equal(t, "Bearer sk-key", gotAuth)
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
		Data:     []byte(`{"status":"succeeded","content":{"video_url":"https://v/o.mp4"}}`),
		Properties: model.Properties{OriginModelName: "doubao-seedance-2-0-260128"},
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var v dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &v))
	assert.Equal(t, "task_pub", v.ID)
	assert.Nil(t, v.Error)
}

func TestConvertToOpenAIVideo_Failed(t *testing.T) {
	task := &model.Task{
		TaskID: "task_pub",
		Status: model.TaskStatusFailure,
		Data:   []byte(`{"status":"failed","error":{"code":"E1","message":"blocked"}}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var v dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &v))
	require.NotNil(t, v.Error)
	assert.Equal(t, "blocked", v.Error.Message)
	assert.Equal(t, "E1", v.Error.Code)
}

func TestConvertToOpenAIVideo_Malformed(t *testing.T) {
	_, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(&model.Task{Data: []byte(`{bad`)})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Misc
// ---------------------------------------------------------------------------

func TestInitAndMeta(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 5, ChannelBaseUrl: "https://b", ApiKey: "k"}})
	assert.Equal(t, "https://b", a.baseURL)
	assert.Equal(t, "k", a.apiKey)
	assert.Equal(t, "doubao-video", a.GetChannelName())
	assert.Contains(t, a.GetModelList(), "doubao-seedance-2-0-260128")
}
