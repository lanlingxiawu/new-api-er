package kling

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
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	service.InitHttpClient()
	os.Exit(m.Run())
}

func newCtx(method, target, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
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

func newInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType:    3,
		ChannelBaseUrl: "https://api.kling",
		ApiKey:         "ak|sk",
	}})
	assert.Equal(t, 3, a.ChannelType)
	assert.Equal(t, "https://api.kling", a.baseURL)
	assert.Equal(t, "ak|sk", a.apiKey)
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestValidate_OK(t *testing.T) {
	c, _ := newCtx(http.MethodPost, "/v1/video", `{"prompt":"a cat","duration":5}`, "application/json")
	info := newInfo()
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
	require.Nil(t, taskErr)
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
	_, ok := c.Get("task_request")
	assert.True(t, ok)
}

func TestValidate_MissingPrompt(t *testing.T) {
	c, _ := newCtx(http.MethodPost, "/v1/video", `{"prompt":"  "}`, "application/json")
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestValidate_Malformed(t *testing.T) {
	c, _ := newCtx(http.MethodPost, "/v1/video", `{bad`, "application/json")
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, newInfo())
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_request", taskErr.Code)
}

// ---------------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://host"}

	info := newInfo()
	info.Action = constant.TaskActionGenerate
	info.ApiKey = "ak|sk"
	url, err := a.BuildRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://host/v1/videos/image2video", url)

	info.Action = constant.TaskActionTextGenerate
	url, _ = a.BuildRequestURL(info)
	assert.Equal(t, "https://host/v1/videos/text2video", url)

	// NewAPI relay (sk- prefix) prepends /kling
	info.Action = constant.TaskActionGenerate
	info.ApiKey = "sk-relaykey"
	url, _ = a.BuildRequestURL(info)
	assert.Equal(t, "https://host/kling/v1/videos/image2video", url)
}

// ---------------------------------------------------------------------------
// BuildRequestHeader (JWT)
// ---------------------------------------------------------------------------

func TestBuildRequestHeader_JWT(t *testing.T) {
	a := &TaskAdaptor{apiKey: "myaccess|mysecret"}
	c, _ := newCtx(http.MethodPost, "/x", "", "")
	req := httptest.NewRequest(http.MethodPost, "https://host/v1/videos/image2video", nil)
	err := a.BuildRequestHeader(c, req, newInfo())
	require.NoError(t, err)
	auth := req.Header.Get("Authorization")
	require.True(t, strings.HasPrefix(auth, "Bearer "))
	tokenStr := strings.TrimPrefix(auth, "Bearer ")
	// Verify signed with secret and carries issuer claim.
	tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) { return []byte("mysecret"), nil })
	require.NoError(t, err)
	claims := tok.Claims.(jwt.MapClaims)
	assert.Equal(t, "myaccess", claims["iss"])
}

func TestBuildRequestHeader_InvalidKeyFormat(t *testing.T) {
	a := &TaskAdaptor{apiKey: "no-delimiter"}
	c, _ := newCtx(http.MethodPost, "/x", "", "")
	req := httptest.NewRequest(http.MethodPost, "https://host", nil)
	err := a.BuildRequestHeader(c, req, newInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT token")
}

func TestBuildRequestHeader_RelayKeyPassthrough(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sk-relay"}
	c, _ := newCtx(http.MethodPost, "/x", "", "")
	req := httptest.NewRequest(http.MethodPost, "https://host", nil)
	err := a.BuildRequestHeader(c, req, newInfo())
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-relay", req.Header.Get("Authorization"))
}

// ---------------------------------------------------------------------------
// BuildRequestBody + convertToRequestPayload
// ---------------------------------------------------------------------------

func TestBuildRequestBody_ImageToVideo(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx(http.MethodPost, "/x", "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "dance",
		Image:    "https://img/first.png",
		Size:     "1280x720",
		Duration: 10,
	})
	info := newInfo()
	info.UpstreamModelName = "kling-v1-6"

	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, `"image":"https://img/first.png"`)
	assert.Contains(t, s, `"aspect_ratio":"16:9"`)
	assert.Contains(t, s, `"duration":"10"`)
	assert.Contains(t, s, `"model_name":"kling-v1-6"`)
	// with an image, action stays image2video (not switched to text)
	assert.Empty(t, c.GetString("action"))
}

func TestBuildRequestBody_TextToVideoSwitchesAction(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx(http.MethodPost, "/x", "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "a story"})
	reader, err := a.BuildRequestBody(c, newInfo())
	require.NoError(t, err)
	_, _ = io.ReadAll(reader)
	assert.Equal(t, constant.TaskActionTextGenerate, c.GetString("action"))
}

func TestBuildRequestBody_DefaultsModelAndMode(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx(http.MethodPost, "/x", "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "hi", Image: "x"})
	reader, err := a.BuildRequestBody(c, newInfo())
	require.NoError(t, err)
	body, _ := io.ReadAll(reader)
	s := string(body)
	assert.Contains(t, s, `"model_name":"kling-v1"`)
	assert.Contains(t, s, `"mode":"std"`)
	assert.Contains(t, s, `"duration":"5"`)
}

func TestBuildRequestBody_MissingContext(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newCtx(http.MethodPost, "/x", "", "")
	_, err := a.BuildRequestBody(c, newInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request not found")
}

func TestConvertToRequestPayload_MetadataOverride(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt: "x",
		Image:  "i",
		Metadata: map[string]interface{}{
			"cfg_scale":       0.9,
			"negative_prompt": "blurry",
		},
	}
	info := newInfo()
	info.UpstreamModelName = "kling-v2-master"
	payload, err := a.convertToRequestPayload(req, info)
	require.NoError(t, err)
	assert.Equal(t, 0.9, payload.CfgScale)
	assert.Equal(t, "blurry", payload.NegativePrompt)
}

func TestGetAspectRatio(t *testing.T) {
	a := &TaskAdaptor{}
	assert.Equal(t, "1:1", a.getAspectRatio("1024x1024"))
	assert.Equal(t, "16:9", a.getAspectRatio("1920x1080"))
	assert.Equal(t, "9:16", a.getAspectRatio("720x1280"))
	assert.Equal(t, "1:1", a.getAspectRatio("weird"))
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func doResp(t *testing.T, body string) (*httptest.ResponseRecorder, string, []byte, *dto.TaskError) {
	t.Helper()
	c, rec := newCtx(http.MethodPost, "/x", "", "")
	info := newInfo()
	info.PublicTaskID = "task_pub"
	info.OriginModelName = "kling-v1"
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	id, data, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	return rec, id, data, taskErr
}

func TestDoResponse_Success(t *testing.T) {
	rec, id, data, taskErr := doResp(t, `{"code":0,"message":"ok","data":{"task_id":"up-123","task_status":"submitted"}}`)
	require.Nil(t, taskErr)
	assert.Equal(t, "up-123", id)
	assert.NotEmpty(t, data)
	assert.Contains(t, rec.Body.String(), "task_pub")
	assert.NotContains(t, rec.Body.String(), "up-123")
}

func TestDoResponse_UpstreamError(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{"code":1002,"message":"invalid params"}`)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Contains(t, taskErr.Message, "invalid params")
}

func TestDoResponse_Malformed(t *testing.T) {
	_, _, _, taskErr := doResp(t, `{bad`)
	require.NotNil(t, taskErr)
	assert.Equal(t, "unmarshal_response_failed", taskErr.Code)
}

// ---------------------------------------------------------------------------
// ParseTaskResult — status mapping
// ---------------------------------------------------------------------------

func TestParseTaskResult_Submitted(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"code":0,"data":{"task_id":"t","task_status":"submitted"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSubmitted, info.Status)
	assert.Equal(t, "t", info.TaskID)
}

func TestParseTaskResult_Processing(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"code":0,"data":{"task_status":"processing"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, info.Status)
}

func TestParseTaskResult_SucceedWithVideoAndDeduction(t *testing.T) {
	body := `{"code":0,"data":{"task_status":"succeed","final_unit_deduction":"3.2","task_result":{"videos":[{"url":"https://v/out.mp4"}]}}}`
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, info.Status)
	assert.Equal(t, "https://v/out.mp4", info.Url)
	// ceil(3.2)=4 tokens
	assert.EqualValues(t, common.QuotaFromFloat(4), info.CompletionTokens)
	assert.EqualValues(t, common.QuotaFromFloat(4), info.TotalTokens)
}

func TestParseTaskResult_SucceedNoDeduction(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"code":0,"data":{"task_status":"succeed"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, info.Status)
	assert.EqualValues(t, 0, info.CompletionTokens)
}

func TestParseTaskResult_Failed(t *testing.T) {
	info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"code":0,"data":{"task_status":"failed","task_status_msg":"nsfw"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, info.Status)
	assert.Equal(t, "nsfw", info.Reason)
}

func TestParseTaskResult_UnknownStatus(t *testing.T) {
	_, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"code":0,"data":{"task_status":"weird"}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown task status")
}

func TestParseTaskResult_Malformed(t *testing.T) {
	_, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{bad`))
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// FetchTask
// ---------------------------------------------------------------------------

func TestFetchTask_BuildsGetURL(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_status":"processing"}}`))
	}))
	defer srv.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(srv.URL, "ak|sk", map[string]any{
		"task_id": "T-42",
		"action":  constant.TaskActionGenerate,
	}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/v1/videos/image2video/T-42", gotPath)
	assert.True(t, strings.HasPrefix(gotAuth, "Bearer "))
}

func TestFetchTask_RelayKeyPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(srv.URL, "sk-relay", map[string]any{
		"task_id": "T-9",
		"action":  constant.TaskActionTextGenerate,
	}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/kling/v1/videos/text2video/T-9", gotPath)
}

func TestFetchTask_MissingTaskID(t *testing.T) {
	_, err := (&TaskAdaptor{}).FetchTask("http://x", "k", map[string]any{"action": "generate"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_id")
}

func TestFetchTask_MissingAction(t *testing.T) {
	_, err := (&TaskAdaptor{}).FetchTask("http://x", "k", map[string]any{"task_id": "t"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action")
}

// ---------------------------------------------------------------------------
// ConvertToOpenAIVideo
// ---------------------------------------------------------------------------

func TestConvertToOpenAIVideo_Success(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_pub",
		Status:   model.TaskStatusSuccess,
		Progress: "100%",
		Data: []byte(`{"code":0,"data":{"created_at":100,"updated_at":200,"task_result":{"videos":[{"url":"https://v/o.mp4","duration":"5"}]}}}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var v dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &v))
	assert.Equal(t, "task_pub", v.ID)
	assert.Equal(t, "5", v.Seconds)
	assert.Nil(t, v.Error)
}

func TestConvertToOpenAIVideo_FailedStatusSetsError(t *testing.T) {
	task := &model.Task{
		TaskID: "task_pub",
		Status: model.TaskStatusFailure,
		Data:   []byte(`{"code":0,"data":{"task_status":"failed","task_status_msg":"content blocked"}}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var v dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &v))
	require.NotNil(t, v.Error)
	assert.Equal(t, "content blocked", v.Error.Message)
}

func TestConvertToOpenAIVideo_TopLevelErrorCode(t *testing.T) {
	task := &model.Task{
		TaskID: "task_pub",
		Status: model.TaskStatusFailure,
		Data:   []byte(`{"code":500,"message":"server err","data":{}}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var v dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &v))
	require.NotNil(t, v.Error)
	assert.Equal(t, "500", v.Error.Code)
}

func TestConvertToOpenAIVideo_Malformed(t *testing.T) {
	task := &model.Task{Data: []byte(`{bad`)}
	_, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Misc
// ---------------------------------------------------------------------------

func TestGetModelListAndName(t *testing.T) {
	a := &TaskAdaptor{}
	assert.Equal(t, "kling", a.GetChannelName())
	assert.Contains(t, a.GetModelList(), "kling-v2-master")
}

func TestIsNewAPIRelay(t *testing.T) {
	assert.True(t, isNewAPIRelay("sk-abc"))
	assert.False(t, isNewAPIRelay("ak|sk"))
}
