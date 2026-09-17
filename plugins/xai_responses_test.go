package plugins_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXaiResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "xai",
		model:     "grok-imagine-video",
		requestBody: map[string]any{
			"model": "grok-imagine-video",
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "a lake at sunrise"},
				map[string]any{"type": "input_image", "image_url": "https://cdn.example/frame.png"},
			}}},
			"seconds": 8,
			"size":    "1280x720",
		},
		wantAction: "image_to_video",
		wantRequest: map[string]any{
			"model":   "grok-imagine-video",
			"prompt":  "a lake at sunrise",
			"images":  []any{"https://cdn.example/frame.png"},
			"seconds": float64(8),
			"size":    "1280x720",
		},
		wantUsageKeys:  []string{"input_images", "output_resolution", "seconds"},
		wantVendorName: "xai",
	})
}

func loadXaiPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("xai")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "xai"})
	require.NoError(t, err)
	return plugin
}

// xaiSubmit runs the production host adaptor over a legacy-route task request.
func xaiSubmit(t *testing.T, modelName string, request map[string]any) (*taskplugin.TaskAdaptor, *relaycommon.RelayInfo, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeXai,
			ChannelBaseUrl:    "https://api.x.ai",
			ApiKey:            "test-key",
			UpstreamModelName: modelName,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	adaptor := taskplugin.New(loadXaiPlugin(t))
	adaptor.Init(info)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	c.Set("task_request", request)
	return adaptor, info, c
}

func xaiSubmitBody(t *testing.T, modelName string, request map[string]any) map[string]any {
	t.Helper()
	adaptor, info, c := xaiSubmit(t, modelName, request)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, common.Unmarshal(encoded, &body))
	return body
}

func TestXaiSubmitUsesOfficialEndpointAndBearerJSON(t *testing.T) {
	adaptor, info, c := xaiSubmit(t, "grok-imagine-video", map[string]any{"prompt": "waves"})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

	url, err := adaptor.BuildRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.x.ai/v1/videos/generations", url)

	req := httptest.NewRequest(http.MethodPost, url, nil)
	require.NoError(t, adaptor.BuildRequestHeader(c, req, info))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.Equal(t, "application/json", req.Header.Get("Accept"))
	assert.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
	assert.Equal(t, "text_to_video", info.Action)
}

func TestXaiSubmitMapsOfficialFields(t *testing.T) {
	body := xaiSubmitBody(t, "grok-imagine-video-1.5", map[string]any{
		"prompt":   "a lake at sunrise",
		"duration": 5,
		"size":     "1920x1080",
		"image":    "https://example.com/input.png",
		"images":   []any{"https://example.com/input.png"},
		"metadata": map[string]any{"seed": 123, "aspect_ratio": "4:3", "resolution": "1080p"},
	})
	assert.Equal(t, map[string]any{
		"model":        "grok-imagine-video-1.5",
		"prompt":       "a lake at sunrise",
		"duration":     float64(5),
		"aspect_ratio": "4:3",
		"resolution":   "1080p",
		"seed":         float64(123),
		"image":        map[string]any{"url": "https://example.com/input.png"},
	}, body)
}

func TestXaiSubmitDerivesDefaultsFromSize(t *testing.T) {
	testCases := []struct {
		name           string
		request        map[string]any
		wantDuration   float64
		wantRatio      string
		wantResolution string
	}{
		{"defaults without size", map[string]any{"prompt": "p"}, 5, "16:9", "720p"},
		{"portrait 480p from size and string seconds", map[string]any{"prompt": "p", "size": "480x854", "seconds": "6"}, 6, "9:16", "480p"},
		{"square size", map[string]any{"prompt": "p", "size": "720x720"}, 5, "1:1", "720p"},
		{"metadata durationSeconds wins", map[string]any{"prompt": "p", "duration": 4, "metadata": map[string]any{"durationSeconds": 7}}, 7, "16:9", "720p"},
		{"numeric resolution normalized", map[string]any{"prompt": "p", "metadata": map[string]any{"resolution": "480"}}, 5, "16:9", "480p"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			body := xaiSubmitBody(t, "grok-imagine-video", testCase.request)
			assert.Equal(t, testCase.wantDuration, body["duration"])
			assert.Equal(t, testCase.wantRatio, body["aspect_ratio"])
			assert.Equal(t, testCase.wantResolution, body["resolution"])
			assert.NotContains(t, body, "image")
			assert.NotContains(t, body, "reference_images")
		})
	}
}

func TestXaiSubmitUsesReferenceImagesForMultipleImages(t *testing.T) {
	adaptor, info, c := xaiSubmit(t, "grok-imagine-video", map[string]any{
		"prompt": "walk down the street",
		"images": []any{"file_subject", "https://example.com/outfit.png"},
	})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, common.Unmarshal(encoded, &body))
	assert.Equal(t, []any{
		map[string]any{"file_id": "file_subject"},
		map[string]any{"url": "https://example.com/outfit.png"},
	}, body["reference_images"])
	assert.NotContains(t, body, "image")
	assert.Equal(t, "image_to_video", info.Action)
}

func TestXaiSubmitInlinesRawBase64Image(t *testing.T) {
	body := xaiSubmitBody(t, "grok-imagine-video-1.5", map[string]any{"prompt": "p", "image": "iVBORw0KGgoAAAANSUhEUg=="})
	assert.Equal(t, map[string]any{"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="}, body["image"])
}

func TestXaiSubmitClampsLegacyModel1080pTo720p(t *testing.T) {
	body := xaiSubmitBody(t, "grok-imagine-video", map[string]any{"prompt": "legacy", "duration": 5, "metadata": map[string]any{"resolution": "1080p"}})
	assert.Equal(t, "720p", body["resolution"])
}

func TestXaiSubmitRejectsUnsupportedInputs(t *testing.T) {
	testCases := []struct {
		name    string
		model   string
		request map[string]any
		message string
	}{
		{"duration exceeds xai maximum", "grok-imagine-video", map[string]any{"prompt": "too long", "duration": 16}, "duration must not exceed 15 seconds"},
		{"version 1.5 requires an input image", "grok-imagine-video-1.5", map[string]any{"prompt": "image required"}, "requires an input image"},
		{"version 1.5 rejects reference images", "grok-imagine-video-1.5", map[string]any{"prompt": "refs", "images": []any{"file_1", "file_2"}}, "does not support reference_images"},
		{"more than seven reference images", "grok-imagine-video", map[string]any{"prompt": "refs", "images": []any{"file_1", "file_2", "file_3", "file_4", "file_5", "file_6", "file_7", "file_8"}}, "must not exceed 7"},
		{"reference duration exceeds ten seconds", "grok-imagine-video", map[string]any{"prompt": "refs", "duration": 11, "images": []any{"file_1", "file_2"}}, "no greater than 10 seconds"},
		{"unknown resolution", "grok-imagine-video-1.5", map[string]any{"prompt": "p", "image": "file_1", "metadata": map[string]any{"resolution": "4k"}}, `resolution "4k" is not supported; use 480p, 720p or 1080p`},
		{"missing prompt", "grok-imagine-video", map[string]any{"prompt": " "}, "prompt is required"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			adaptor, info, c := xaiSubmit(t, testCase.model, testCase.request)
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Contains(t, taskErr.Message, testCase.message)
		})
	}
	t.Run("ten-second reference video is accepted", func(t *testing.T) {
		adaptor, info, c := xaiSubmit(t, "grok-imagine-video", map[string]any{"prompt": "refs", "duration": 10, "images": []any{"file_1", "file_2"}})
		assert.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	})
}

func TestXaiBillingRatiosKeepLegacyVideoUnits(t *testing.T) {
	testCases := []struct {
		name    string
		model   string
		request map[string]any
		want    float64
	}{
		{"legacy 480p duration plus input image", "grok-imagine-video", map[string]any{"prompt": "p", "duration": 10, "image": "https://example.com/a.png", "images": []any{"https://example.com/a.png"}, "metadata": map[string]any{"resolution": "480p"}}, 10.04},
		{"legacy 720p", "grok-imagine-video", map[string]any{"prompt": "p", "duration": 10, "metadata": map[string]any{"resolution": "720p"}}, 14},
		{"legacy 1080p priced as 720p", "grok-imagine-video", map[string]any{"prompt": "p", "duration": 10, "metadata": map[string]any{"resolution": "1080p"}}, 14},
		{"1.5 720p with image", "grok-imagine-video-1.5", map[string]any{"prompt": "p", "duration": 8, "image": "file_a", "metadata": map[string]any{"resolution": "720p"}}, 14.125},
		{"1.5 1080p with image", "grok-imagine-video-1.5", map[string]any{"prompt": "p", "duration": 4, "image": "file_a", "metadata": map[string]any{"resolution": "1080p"}}, 12.625},
		{"legacy two reference images", "grok-imagine-video", map[string]any{"prompt": "p", "duration": 4, "images": []any{"file_a", "file_b"}, "metadata": map[string]any{"resolution": "1080p"}}, 5.68},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			adaptor, info, c := xaiSubmit(t, testCase.model, testCase.request)
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			ratios, err := adaptor.EstimateBillingValidated(c, info)
			require.NoError(t, err)
			require.Len(t, ratios, 1)
			assert.InDelta(t, testCase.want, ratios["xai_video_units"], 1e-9)
		})
	}
}

func TestXaiUsageFactsForExpressions(t *testing.T) {
	adaptor, info, c := xaiSubmit(t, "grok-imagine-video", map[string]any{
		"prompt":   "p",
		"seconds":  "8",
		"images":   []any{"file_a", "https://example.com/b.png", "file_a"},
		"metadata": map[string]any{"resolution": "1080p"},
	})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	facts, err := adaptor.ExtractUsageFactsValidated(c, info)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"seconds": float64(8), "output_resolution": "720p", "input_images": float64(2)}, facts)
}

func TestXaiParseSubmitResponse(t *testing.T) {
	adaptor, info, c := xaiSubmit(t, "grok-imagine-video", map[string]any{"prompt": "p"})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"request_id":"req_123"}`))}
	parsed, taskErr := adaptor.ParseResponse(c, response, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "req_123", parsed.UpstreamTaskID)
	assert.JSONEq(t, `{"request_id":"req_123"}`, string(parsed.TaskData))

	missing := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"not-a-request"}`))}
	_, taskErr = adaptor.ParseResponse(c, missing, info)
	require.NotNil(t, taskErr)
	assert.Contains(t, taskErr.Message, "missing request_id")
}

func TestXaiPollsOfficialEndpointWithTaskKey(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":"done","video":{"url":"https://vidgen.x.ai/out.mp4","duration":5}}`))
	}))
	defer server.Close()

	adaptor := taskplugin.New(loadXaiPlugin(t))
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}})
	task := &model.Task{TaskID: "task_public", Properties: model.Properties{OriginModelName: "grok-imagine-video"}}
	task.PrivateData.UpstreamTaskID = "req_abc"
	task.PrivateData.Key = "task-key"

	resp, err := adaptor.FetchTask(server.URL, "channel-key", task, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "/v1/videos/req_abc", gotPath)
	assert.Equal(t, "Bearer task-key", gotAuth)

	result, err := adaptor.ParseTaskResult(task, resp, body)
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", result.Status)
	assert.Equal(t, "https://vidgen.x.ai/out.mp4", result.Url)
}

func TestXaiParseTaskResultMapsStatuses(t *testing.T) {
	plugin := loadXaiPlugin(t)
	testCases := []struct {
		body       string
		wantStatus string
		wantURL    string
		wantReason string
	}{
		{`{"status":"queued"}`, "QUEUED", "", ""},
		{`{"status":"pending"}`, "QUEUED", "", ""},
		{`{"status":"processing"}`, "IN_PROGRESS", "", ""},
		{`{"status":"in_progress"}`, "IN_PROGRESS", "", ""},
		{`{"status":"generating"}`, "IN_PROGRESS", "", ""},
		{`{"id":"req_early","request_id":"req_early"}`, "IN_PROGRESS", "", ""},
		{`{"status":"done","video":{"url":"https://example.com/out.mp4"}}`, "SUCCESS", "https://example.com/out.mp4", ""},
		{`{"status":"completed","video":{"url":"https://example.com/out.mp4"}}`, "SUCCESS", "https://example.com/out.mp4", ""},
		{`{"status":"success","video":{"url":"https://example.com/out.mp4"}}`, "SUCCESS", "https://example.com/out.mp4", ""},
		{`{"status":"expired"}`, "FAILURE", "", "task expired"},
		{`{"status":"failed","error":{"message":"blocked"}}`, "FAILURE", "", "blocked"},
		{`{"status":"error"}`, "FAILURE", "", "task failed"},
		{`{"error":{"message":"invalid request","code":"bad"}}`, "FAILURE", "", "invalid request"},
		{`{"status":"mystery"}`, "UNKNOWN", "", "unknown xai video task status: mystery"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.body, func(t *testing.T) {
			var body any
			require.NoError(t, common.Unmarshal([]byte(testCase.body), &body))
			value, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, body)
			require.NoError(t, err)
			result := pluginResultObject(t, value)
			assert.Equal(t, testCase.wantStatus, result["status"])
			url, _ := result["url"].(string)
			reason, _ := result["reason"].(string)
			assert.Equal(t, testCase.wantURL, url)
			assert.Equal(t, testCase.wantReason, reason)
		})
	}
}

func TestXaiArtifactsAndOpenAIVideoRender(t *testing.T) {
	adaptor := taskplugin.New(loadXaiPlugin(t))
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k", ChannelBaseUrl: "https://api.x.ai"}})

	success := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, Progress: "100%", CreatedAt: 10, UpdatedAt: 20, FinishTime: 30,
		Properties: model.Properties{OriginModelName: "grok-imagine-video-1.5"}}
	success.SetData(map[string]any{"status": "done", "video": map[string]any{"url": "https://vidgen.x.ai/out.mp4"}})
	artifacts, err := adaptor.ListArtifacts(success)
	require.NoError(t, err)
	assert.Equal(t, []relaychannel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
	descriptor, err := adaptor.BuildContentRequest(success, "video", relaychannel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, "https://vidgen.x.ai/out.mp4", descriptor.URL)
	assert.True(t, descriptor.Credentialless)
	assert.Empty(t, descriptor.Headers)

	rendered, err := adaptor.ConvertToOpenAIVideo(success)
	require.NoError(t, err)
	var video map[string]any
	require.NoError(t, common.Unmarshal(rendered, &video))
	assert.Equal(t, "task_public", video["id"])
	assert.Equal(t, "completed", video["status"])
	assert.Equal(t, map[string]any{"url": "https://vidgen.x.ai/out.mp4"}, video["metadata"])

	failure := &model.Task{TaskID: "task_failed", Status: model.TaskStatusFailure, Progress: "100%", CreatedAt: 10, FailReason: "blocked",
		Properties: model.Properties{OriginModelName: "grok-imagine-video-1.5"}}
	failure.SetData(map[string]any{"status": "failed", "error": map[string]any{"message": "blocked"}})
	rendered, err = adaptor.ConvertToOpenAIVideo(failure)
	require.NoError(t, err)
	video = map[string]any{}
	require.NoError(t, common.Unmarshal(rendered, &video))
	assert.Equal(t, "failed", video["status"])
	assert.Equal(t, map[string]any{"message": "blocked", "code": "task_failed"}, video["error"])
	assert.NotContains(t, video, "metadata")
}

// Tasks persisted by the former Go adaptor keep platform "48" and the raw
// xAI submit body; they must resolve to this plugin and stay pollable.
func TestXaiLegacyTasksResolveToPlugin(t *testing.T) {
	plugin, ok := relay.ResolveTaskPluginForPlatform(jsplugin.DefaultRegistry.Generation(), constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeXai)))
	require.True(t, ok)
	assert.Equal(t, "xai", plugin.Meta.Key)
	require.NotNil(t, relay.GetTaskAdaptor(constant.TaskPlatform("48")))

	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k", ChannelBaseUrl: "https://api.x.ai"}})
	legacy := &model.Task{TaskID: "task_legacy", Platform: "48", Action: "textGenerate", Status: model.TaskStatusInProgress,
		Data: []byte(`{"request_id":"req_legacy"}`), Properties: model.Properties{OriginModelName: "grok-imagine-video"}}
	legacy.PrivateData.UpstreamTaskID = "req_legacy"
	result, err := adaptor.ParseTaskResult(legacy, &http.Response{StatusCode: http.StatusOK}, []byte(`{"status":"done","video":{"url":"https://vidgen.x.ai/legacy.mp4"}}`))
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", result.Status)
	assert.Equal(t, "https://vidgen.x.ai/legacy.mp4", result.Url)
}

func pluginResultObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(encoded, &result))
	return result
}

// Multipart uploads reach the hook only as opaque references; the host inlines
// the bytes into the JSON body as a data URL and the upload counts as an input image.
func TestXaiUploadedImageUsesFilePlaceholder(t *testing.T) {
	plugin := loadXaiPlugin(t)
	ctx := map[string]any{
		"requestBody":   map[string]any{"prompt": "animate", "model": "grok-imagine-video-1.5"},
		"model":         "grok-imagine-video-1.5",
		"upstreamModel": "grok-imagine-video-1.5",
		"baseUrl":       "https://api.x.ai/",
		"apiKey":        "k",
		"files":         []any{map[string]any{"ref": "request_file:input_reference", "field": "input_reference", "filename": "a.png", "mimeType": "image/png", "size": 10}},
	}
	value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	descriptor := pluginResultObject(t, value)
	assert.Equal(t, "https://api.x.ai/v1/videos/generations", descriptor["url"])
	assert.Equal(t, "image_to_video", descriptor["action"])
	body, ok := descriptor["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"url": map[string]any{"__fileRef": "request_file:input_reference", "encoding": "dataUrl", "maxBytes": float64(20971520)}}, body["image"])

	ctx["usagePurpose"] = "facts"
	value, err = plugin.Engine.Call(t.Context(), "extractUsage", ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"seconds": float64(5), "output_resolution": "720p", "input_images": float64(1)}, pluginResultObject(t, value))
}
