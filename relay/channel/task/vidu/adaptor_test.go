package vidu

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
	req := httptest.NewRequest(http.MethodPost, "/v1/video", strings.NewReader(body))
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
// ValidateRequestAndSetAction — action derivation
// ---------------------------------------------------------------------------

func TestValidate_TextGenerateDefault(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat"}`)
	info := newInfo()
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionTextGenerate, info.Action)
}

func TestValidate_SingleImageGenerate(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","image":"https://i/1.png"}`)
	info := newInfo()
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
}

func TestValidate_ViduTwoImagesFirstTail(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","images":["a","b"]}`)
	info := newInfo()
	info.ChannelType = constant.ChannelTypeVidu
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionFirstTailGenerate, info.Action)
}

func TestValidate_ViduThreeImagesReference(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","images":["a","b","c"]}`)
	info := newInfo()
	info.ChannelType = constant.ChannelTypeVidu
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionReferenceGenerate, info.Action)
}

func TestValidate_NonViduTwoImagesStaysGenerate(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","images":["a","b"]}`)
	info := newInfo()
	info.ChannelType = 999 // not vidu
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
}

func TestValidate_MetadataActionOverride(t *testing.T) {
	c, _ := newCtx(`{"prompt":"a cat","image":"i","metadata":{"action":"customAction"}}`)
	info := newInfo()
	info.ChannelType = constant.ChannelTypeVidu
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	assert.Equal(t, "customAction", info.Action)
}

func TestValidate_MissingPrompt(t *testing.T) {
	c, _ := newCtx(`{"prompt":""}`)
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

// ---------------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://api.vidu"}
	cases := map[string]string{
		constant.TaskActionGenerate:          "https://api.vidu/ent/v2/img2video",
		constant.TaskActionFirstTailGenerate: "https://api.vidu/ent/v2/start-end2video",
		constant.TaskActionReferenceGenerate: "https://api.vidu/ent/v2/reference2video",
		constant.TaskActionTextGenerate:      "https://api.vidu/ent/v2/text2video",
	}
	for action, want := range cases {
		info := newInfo()
		info.Action = action
		url, err := a.BuildRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, want, url, "action=%s", action)
	}
}

// ---------------------------------------------------------------------------
// BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestHeader(t *testing.T) {
	c, _ := newCtx("")
	info := newInfo()
	info.ApiKey = "tok-123"
	req := httptest.NewRequest(http.MethodPost, "https://api.vidu", nil)
	require.NoError(t, (&TaskAdaptor{}).BuildRequestHeader(c, req, info))
	assert.Equal(t, "Token tok-123", req.Header.Get("Authorization"))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
}

// ---------------------------------------------------------------------------
// BuildRequestBody + convertToRequestPayload
// ---------------------------------------------------------------------------

func TestBuildRequestBody_Defaults(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "hi", Images: []string{"a"}})
	info := newInfo()
	info.Action = constant.TaskActionGenerate
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, `"model":"viduq1"`)
	assert.Contains(t, s, `"duration":5`)
	assert.Contains(t, s, `"resolution":"1080p"`)
	assert.Contains(t, s, `"movement_amplitude":"auto"`)
}

func TestBuildRequestBody_ReferenceForcesViduq2(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "hi", Images: []string{"a", "b", "c"}})
	info := newInfo()
	info.UpstreamModelName = "viduq2-pro"
	info.Action = constant.TaskActionReferenceGenerate
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	assert.Contains(t, string(body), `"model":"viduq2"`)
	assert.NotContains(t, string(body), "viduq2-pro")
}

func TestBuildRequestBody_ReferenceNonQ2Untouched(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "hi", Images: []string{"a", "b", "c"}})
	info := newInfo()
	info.UpstreamModelName = "vidu1.5"
	info.Action = constant.TaskActionReferenceGenerate
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	assert.Contains(t, string(body), `"model":"vidu1.5"`)
}

func TestBuildRequestBody_MetadataOverride(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx("")
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "hi", Images: []string{"a"},
		Metadata: map[string]interface{}{"seed": 42, "bgm": true},
	})
	info := newInfo()
	info.Action = constant.TaskActionGenerate
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, `"seed":42`)
	assert.Contains(t, s, `"bgm":true`)
}

func TestBuildRequestBody_MissingContext(t *testing.T) {
	c, _ := newCtx("")
	_, err := (&TaskAdaptor{}).BuildRequestBody(c, newInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request not found")
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
	rec, id, data, taskErr := doResp(t, `{"task_id":"vt-1","state":"created"}`)
	require.Nil(t, taskErr)
	assert.Equal(t, "vt-1", id)
	assert.NotEmpty(t, data)
	assert.Contains(t, rec.Body.String(), "task_pub")
	assert.NotContains(t, rec.Body.String(), "vt-1")
}

func TestDoResponse_Failed(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{"task_id":"vt-1","state":"failed"}`)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "task_failed", taskErr.Code)
}

func TestDoResponse_Malformed(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{bad`)
	require.NotNil(t, taskErr)
	assert.Equal(t, "unmarshal_response_failed", taskErr.Code)
}

// ---------------------------------------------------------------------------
// ParseTaskResult — state mapping
// ---------------------------------------------------------------------------

func TestParseTaskResult_States(t *testing.T) {
	a := &TaskAdaptor{}

	for _, state := range []string{"created", "queueing"} {
		info, err := a.ParseTaskResult([]byte(`{"state":"` + state + `"}`))
		require.NoError(t, err)
		assert.Equal(t, model.TaskStatusSubmitted, info.Status, state)
	}

	info, err := a.ParseTaskResult([]byte(`{"state":"processing"}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, info.Status)

	info, err = a.ParseTaskResult([]byte(`{"state":"success","creations":[{"url":"https://v/o.mp4"}]}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, info.Status)
	assert.Equal(t, "https://v/o.mp4", info.Url)

	info, err = a.ParseTaskResult([]byte(`{"state":"failed","err_code":"E_NSFW"}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, info.Status)
	assert.Equal(t, "E_NSFW", info.Reason)
}

func TestParseTaskResult_Unknown(t *testing.T) {
	_, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"state":"weird"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown task state")
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
		_, _ = w.Write([]byte(`{"state":"processing"}`))
	}))
	defer srv.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(srv.URL, "tok-9", map[string]any{"task_id": "T-1"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/ent/v2/tasks/T-1/creations", gotPath)
	assert.Equal(t, "Token tok-9", gotAuth)
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
		Data:     []byte(`{"state":"success","creations":[{"url":"https://v/o.mp4"}]}`),
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
		Data:   []byte(`{"state":"failed","err_code":"E_BLOCK"}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var v dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &v))
	require.NotNil(t, v.Error)
	assert.Equal(t, "E_BLOCK", v.Error.Code)
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
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 52, ChannelBaseUrl: "https://b"}})
	assert.Equal(t, 52, a.ChannelType)
	assert.Equal(t, "https://b", a.baseURL)
	assert.Equal(t, "vidu", a.GetChannelName())
	assert.Contains(t, a.GetModelList(), "viduq2")
}
