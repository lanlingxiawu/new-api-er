package xai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "grok-imagine-video-1.5",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeXai,
			ChannelBaseUrl:    "https://api.x.ai",
			ApiKey:            "test-key",
			UpstreamModelName: "grok-imagine-video-1.5",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID: "task_public",
		},
	}
}

func newTaskContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func readJSONMap(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, common.Unmarshal(data, &got))
	return got
}

func TestBuildRequestURLUsesOfficialXaiVideoGenerationsEndpoint(t *testing.T) {
	url, err := (&TaskAdaptor{}).BuildRequestURL(newTestInfo())
	require.NoError(t, err)
	assert.Equal(t, "https://api.x.ai/v1/videos/generations", url)
}

func TestBuildRequestHeaderUsesBearerJSON(t *testing.T) {
	info := newTestInfo()
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", nil)

	require.NoError(t, adaptor.BuildRequestHeader(newTaskContext(), req, info))

	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.Equal(t, "application/json", req.Header.Get("Accept"))
	assert.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
}

func TestBuildRequestBodyMapsOfficialXaiFields(t *testing.T) {
	ctx := newTaskContext()
	info := newTestInfo()
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "a lake at sunrise",
		Duration: 5,
		Size:     "1920x1080",
		Image:    "https://example.com/input.png",
		Metadata: map[string]any{
			"seed":         float64(123),
			"aspect_ratio": "4:3",
			"resolution":   "1080p",
		},
	})

	body, err := (&TaskAdaptor{}).BuildRequestBody(ctx, info)
	require.NoError(t, err)
	got := readJSONMap(t, body)

	assert.Equal(t, "grok-imagine-video-1.5", got["model"])
	assert.Equal(t, "a lake at sunrise", got["prompt"])
	assert.Equal(t, float64(5), got["duration"])
	assert.Equal(t, "4:3", got["aspect_ratio"])
	assert.Equal(t, "1080p", got["resolution"])
	assert.Equal(t, float64(123), got["seed"])
	require.IsType(t, map[string]any{}, got["image"])
	assert.Equal(t, "https://example.com/input.png", got["image"].(map[string]any)["url"])
}

func TestBuildRequestBodyUsesReferenceImagesForMultipleImages(t *testing.T) {
	ctx := newTaskContext()
	info := newTestInfo()
	info.OriginModelName = "grok-imagine-video"
	info.ChannelMeta.UpstreamModelName = "grok-imagine-video"
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "walk down the street",
		Images: []string{"file_subject", "https://example.com/outfit.png"},
	})

	body, err := (&TaskAdaptor{}).BuildRequestBody(ctx, info)
	require.NoError(t, err)
	got := readJSONMap(t, body)

	require.IsType(t, []any{}, got["reference_images"])
	refs := got["reference_images"].([]any)
	require.Len(t, refs, 2)
	assert.Equal(t, "file_subject", refs[0].(map[string]any)["file_id"])
	assert.Equal(t, "https://example.com/outfit.png", refs[1].(map[string]any)["url"])
}

func TestValidateRequestRejectsUnsupportedXaiVideoInputs(t *testing.T) {
	tests := []struct {
		name  string
		model string
		req   relaycommon.TaskSubmitReq
	}{
		{
			name: "duration exceeds xai maximum",
			req:  relaycommon.TaskSubmitReq{Prompt: "too long", Duration: maxXaiVideoDurationSeconds + 1},
		},
		{
			name: "version 1.5 requires an input image",
			req:  relaycommon.TaskSubmitReq{Prompt: "image required"},
		},
		{
			name: "version 1.5 does not support reference images",
			req: relaycommon.TaskSubmitReq{
				Prompt: "unsupported references",
				Images: []string{"file_1", "file_2"},
			},
		},
		{
			name: "more than seven reference images",
			req: relaycommon.TaskSubmitReq{
				Prompt: "too many references",
				Images: []string{"file_1", "file_2", "file_3", "file_4", "file_5", "file_6", "file_7", "file_8"},
			},
			model: "grok-imagine-video",
		},
		{
			name: "reference video duration exceeds ten seconds",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "reference duration",
				Duration: 11,
				Images:   []string{"file_1", "file_2"},
			},
			model: "grok-imagine-video",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newTaskContext()
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			ctx.Set("task_request", tt.req)
			info := newTestInfo()
			if tt.model != "" {
				info.OriginModelName = tt.model
				info.ChannelMeta.UpstreamModelName = tt.model
			}

			taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(ctx, info)

			require.NotNil(t, taskErr)
			assert.Equal(t, "invalid_request", taskErr.Code)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestBuildRequestBodyClampsUnsupportedLegacy1080pTo720p(t *testing.T) {
	ctx := newTaskContext()
	info := newTestInfo()
	info.OriginModelName = "grok-imagine-video"
	info.ChannelMeta.UpstreamModelName = "grok-imagine-video"
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "legacy model",
		Duration: 5,
		Metadata: map[string]any{"resolution": "1080p"},
	})

	body, err := (&TaskAdaptor{}).BuildRequestBody(ctx, info)
	require.NoError(t, err)
	got := readJSONMap(t, body)

	assert.Equal(t, "720p", got["resolution"])
}

func TestEstimateBillingUsesIsolatedXaiVideoUnits(t *testing.T) {
	tests := []struct {
		name  string
		model string
		req   relaycommon.TaskSubmitReq
		want  float64
	}{
		{
			name:  "legacy 480p duration plus input image",
			model: "grok-imagine-video",
			req: relaycommon.TaskSubmitReq{
				Duration: 10,
				Image:    "https://example.com/a.png",
				Metadata: map[string]any{"resolution": "480p"},
			},
			want: 10.04,
		},
		{
			name:  "legacy 720p",
			model: "grok-imagine-video",
			req: relaycommon.TaskSubmitReq{
				Duration: 10,
				Metadata: map[string]any{"resolution": "720p"},
			},
			want: 14,
		},
		{
			name:  "1.5 720p",
			model: "grok-imagine-video-1.5",
			req: relaycommon.TaskSubmitReq{
				Duration: 8,
				Metadata: map[string]any{"resolution": "720p"},
			},
			want: 14,
		},
		{
			name:  "1.5 1080p with two reference images",
			model: "grok-imagine-video-1.5",
			req: relaycommon.TaskSubmitReq{
				Duration: 4,
				Images:   []string{"file_a", "file_b"},
				Metadata: map[string]any{"resolution": "1080p"},
			},
			want: 12.75,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newTaskContext()
			ctx.Set("task_request", tt.req)
			info := newTestInfo()
			info.OriginModelName = tt.model
			info.ChannelMeta.UpstreamModelName = tt.model

			ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

			require.Len(t, ratios, 1)
			assert.InDelta(t, tt.want, ratios["xai_video_units"], 1e-9)
			assert.Equal(t, tt.model, info.PriceData.PricingMetadata["xai_video_model"])
			assert.NotEmpty(t, info.PriceData.PricingMetadata["duration_seconds"])
			assert.NotEmpty(t, info.PriceData.PricingMetadata["resolution"])
		})
	}
}

func TestEstimateBillingClampsLegacy1080pTo720pPricing(t *testing.T) {
	ctx := newTaskContext()
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Duration: 10,
		Metadata: map[string]any{"resolution": "1080p"},
	})
	info := newTestInfo()
	info.OriginModelName = "grok-imagine-video"
	info.ChannelMeta.UpstreamModelName = "grok-imagine-video"

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

	assert.InDelta(t, 14, ratios["xai_video_units"], 1e-9)
	assert.Equal(t, "1080p", info.PriceData.PricingMetadata["requested_resolution"])
	assert.Equal(t, "720p", info.PriceData.PricingMetadata["resolution"])
}

func TestDoResponseReturnsPublicTaskAndStoresRequestID(t *testing.T) {
	ctx := newTaskContext()
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(`{"request_id":"req_123"}`)),
	}

	taskID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(ctx, resp, newTestInfo())

	require.Nil(t, taskErr)
	assert.Equal(t, "req_123", taskID)
	assert.JSONEq(t, `{"request_id":"req_123"}`, string(taskData))
	assert.Equal(t, http.StatusOK, ctx.Writer.Status())
}

func TestDoResponseRejectsMissingRequestID(t *testing.T) {
	ctx := newTaskContext()
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(`{"id":"not-a-request"}`)),
	}

	_, _, taskErr := (&TaskAdaptor{}).DoResponse(ctx, resp, newTestInfo())

	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_response", taskErr.Code)
}

func TestFetchTaskUsesOfficialXaiPollingEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":"done","video":{"url":"https://example.com/out.mp4"}}`))
	}))
	defer server.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(server.URL, "poll-key", map[string]any{"task_id": "req_abc"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "/v1/videos/req_abc", gotPath)
	assert.Equal(t, "Bearer poll-key", gotAuth)
}

func TestParseTaskResultMapsXaiStatuses(t *testing.T) {
	tests := []struct {
		body       string
		wantStatus model.TaskStatus
		wantURL    string
		wantReason string
	}{
		{`{"status":"queued"}`, model.TaskStatusQueued, "", ""},
		{`{"status":"pending"}`, model.TaskStatusQueued, "", ""},
		{`{"status":"processing"}`, model.TaskStatusInProgress, "", ""},
		{`{"status":"in_progress"}`, model.TaskStatusInProgress, "", ""},
		{`{"status":"generating"}`, model.TaskStatusInProgress, "", ""},
		{`{"id":"req_early","request_id":"req_early"}`, model.TaskStatusInProgress, "", ""},
		{`{"status":"done","video":{"url":"https://example.com/out.mp4"}}`, model.TaskStatusSuccess, "https://example.com/out.mp4", ""},
		{`{"status":"completed","video":{"url":"https://example.com/out.mp4"}}`, model.TaskStatusSuccess, "https://example.com/out.mp4", ""},
		{`{"status":"success","video":{"url":"https://example.com/out.mp4"}}`, model.TaskStatusSuccess, "https://example.com/out.mp4", ""},
		{`{"status":"expired"}`, model.TaskStatusFailure, "", "task expired"},
		{`{"status":"failed","error":{"message":"blocked"}}`, model.TaskStatusFailure, "", "blocked"},
		{`{"status":"error"}`, model.TaskStatusFailure, "", "task failed"},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			info, err := (&TaskAdaptor{}).ParseTaskResult([]byte(tt.body))

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, model.TaskStatus(info.Status))
			assert.Equal(t, tt.wantURL, info.Url)
			assert.Equal(t, tt.wantReason, info.Reason)
		})
	}
}

func TestParseTaskResultMalformed(t *testing.T) {
	_, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{bad`))
	require.Error(t, err)
}

func TestConvertToOpenAIVideo(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusFailure,
		Progress:   "100%",
		CreatedAt:  10,
		UpdatedAt:  20,
		FinishTime: 30,
		FailReason: "blocked",
		Properties: model.Properties{OriginModelName: "grok-imagine-video-1.5"},
	}

	data, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, common.Unmarshal(data, &got))
	assert.Equal(t, "task_public", got["id"])
	assert.Equal(t, "task_public", got["task_id"])
	assert.Equal(t, "grok-imagine-video-1.5", got["model"])
	assert.Equal(t, "failed", got["status"])
	assert.Equal(t, float64(100), got["progress"])
	assert.Equal(t, float64(10), got["created_at"])
	assert.Equal(t, float64(30), got["completed_at"])
	require.IsType(t, map[string]any{}, got["error"])
	assert.Equal(t, "blocked", got["error"].(map[string]any)["message"])
}
