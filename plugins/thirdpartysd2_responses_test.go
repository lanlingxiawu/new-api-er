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
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sd2Model     = "dreamina-seedance-2-0-260128"
	sd2FastModel = "dreamina-seedance-2-0-fast-260128"
)

func TestThirdPartySD2ResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "thirdpartysd2",
		model:     sd2Model,
		requestBody: map[string]any{
			"model": sd2Model,
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "a running fox"},
				map[string]any{"type": "input_image", "image_url": "https://cdn.example/frame.png"},
			}}},
			"seconds": 6,
			"size":    "1920x1080",
		},
		wantAction: "image_to_video",
		wantRequest: map[string]any{
			"model":   sd2Model,
			"prompt":  "a running fox",
			"images":  []any{"https://cdn.example/frame.png"},
			"seconds": float64(6),
			"size":    "1920x1080",
		},
		wantUsageKeys:  []string{"output_resolution", "tokens", "video_input"},
		wantVendorName: "thirdpartysd2",
	})
}

func loadSD2Plugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("thirdpartysd2")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "thirdpartysd2"})
	require.NoError(t, err)
	return plugin
}

func sd2Submit(t *testing.T, originModel, upstreamModel string, request map[string]any) (*taskplugin.TaskAdaptor, *relaycommon.RelayInfo, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{
		OriginModelName: originModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeThirdPartySD2,
			ChannelBaseUrl:    "https://sd2.example",
			ApiKey:            "sd2-key",
			UpstreamModelName: upstreamModel,
			IsModelMapped:     originModel != upstreamModel,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	adaptor := taskplugin.New(loadSD2Plugin(t))
	adaptor.Init(info)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	c.Set("task_request", request)
	return adaptor, info, c
}

func sd2SubmitBody(t *testing.T, request map[string]any) string {
	t.Helper()
	adaptor, info, c := sd2Submit(t, sd2Model, sd2Model, request)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(encoded)
}

func TestThirdPartySD2SubmitRequest(t *testing.T) {
	adaptor, info, c := sd2Submit(t, sd2Model, sd2Model, map[string]any{"prompt": "p", "size": "1280x720"})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	url, err := adaptor.BuildRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://sd2.example/v1/video/generate", url)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	require.NoError(t, adaptor.BuildRequestHeader(c, req, info))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.Equal(t, "application/json", req.Header.Get("Accept"))
	assert.Equal(t, "Bearer sd2-key", req.Header.Get("Authorization"))
	assert.Equal(t, "text_to_video", info.Action)
}

func TestThirdPartySD2SubmitBodyTransform(t *testing.T) {
	testCases := []struct {
		name     string
		request  map[string]any
		wantBody string
	}{
		{
			name:     "duration fallback",
			request:  map[string]any{"prompt": "test", "duration": 6, "size": "1280x720"},
			wantBody: `{"model":"` + sd2Model + `","content":[{"type":"text","text":"test"}],"resolution":"720p","duration":6}`,
		},
		{
			name:     "seconds preferred over duration",
			request:  map[string]any{"prompt": "test", "duration": 4, "seconds": "8", "size": "1280x720"},
			wantBody: `{"model":"` + sd2Model + `","content":[{"type":"text","text":"test"}],"resolution":"720p","duration":8}`,
		},
		{
			name:     "size wins over a lower metadata resolution",
			request:  map[string]any{"prompt": "test", "size": "1920x1080", "metadata": map[string]any{"resolution": "480p"}},
			wantBody: `{"model":"` + sd2Model + `","content":[{"type":"text","text":"test"}],"resolution":"1080p"}`,
		},
		{
			name:     "images become image_url content before the prompt",
			request:  map[string]any{"prompt": "fox", "images": []any{"https://cdn.example/a.png"}, "metadata": map[string]any{"resolution": "2160P"}},
			wantBody: `{"model":"` + sd2Model + `","content":[{"type":"image_url","image_url":{"url":"https://cdn.example/a.png"}},{"type":"text","text":"fox"}],"resolution":"4k"}`,
		},
		{
			name: "metadata content replaces images, drops text items and whitelisted fields are coerced",
			request: map[string]any{"prompt": "final", "images": []any{"https://cdn.example/ignored.png"}, "metadata": map[string]any{
				"model":          "must-not-override",
				"resolution":     "720p",
				"ratio":          "16:9",
				"seed":           "42",
				"generate_audio": "true",
				"watermark":      false,
				"unknown_field":  "dropped",
				"content": []any{
					map[string]any{"type": "text", "text": "old"},
					map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://cdn.example/ref.mp4"}, "role": "reference_video", "extra": 1},
				},
			}},
			wantBody: `{"model":"` + sd2Model + `","content":[{"type":"video_url","role":"reference_video","video_url":{"url":"https://cdn.example/ref.mp4"}},{"type":"text","text":"final"}],"resolution":"720p","ratio":"16:9","seed":42,"generate_audio":true,"watermark":false}`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.JSONEq(t, testCase.wantBody, sd2SubmitBody(t, testCase.request))
		})
	}
}

func TestThirdPartySD2SubmitUsesMappedUpstreamModel(t *testing.T) {
	adaptor, info, c := sd2Submit(t, "my-sd2-alias", sd2FastModel, map[string]any{"prompt": "p", "size": "854x480"})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"model":"`+sd2FastModel+`"`)
}

func TestThirdPartySD2SubmitRejectsInvalidRequests(t *testing.T) {
	testCases := []struct {
		name    string
		model   string
		request map[string]any
		message string
	}{
		{"missing resolution", sd2Model, map[string]any{"prompt": "p"}, "a recognizable resolution is required"},
		{"missing prompt", sd2Model, map[string]any{"prompt": "", "size": "1280x720"}, "prompt is required"},
		{"invalid integer metadata", sd2Model, map[string]any{"prompt": "p", "size": "1280x720", "metadata": map[string]any{"frames": "many"}}, "metadata.frames must be an integer"},
		{"content must be an array", sd2Model, map[string]any{"prompt": "p", "size": "1280x720", "metadata": map[string]any{"content": "x"}}, "metadata.content must be an array"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			adaptor, info, c := sd2Submit(t, testCase.model, testCase.model, testCase.request)
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Contains(t, taskErr.Message, testCase.message)
		})
	}
}

// The plugin recognizes every resolution tier; the pricing matrix decides which
// tiers a model accepts, so a tier the administrator adds to the matrix is
// accepted and billed at its own price instead of the expression's last tier.
func TestThirdPartySD2AcceptedResolutionsFollowPricingMatrix(t *testing.T) {
	saved, err := config.ConfigToMap(config.GlobalConfig.Get("thirdpartysd2_pricing"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("thirdpartysd2_pricing", saved))
		model_setting.RebuildThirdPartySD2PricingIndex()
	})

	facts1080p := func(t *testing.T, modelName string) map[string]any {
		t.Helper()
		adaptor, info, c := sd2Submit(t, modelName, modelName, map[string]any{"prompt": "p", "metadata": map[string]any{"resolution": "1080p"}})
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
		facts, err := adaptor.ExtractUsageFactsValidated(c, info)
		require.NoError(t, err)
		assert.Equal(t, "1080p", facts["output_resolution"])
		return facts
	}

	facts := facts1080p(t, sd2FastModel)
	err = billing_setting.ValidateForkTaskUsageFacts("thirdpartysd2", sd2FastModel, sd2FastModel, facts)
	require.EqualError(t, err, sd2FastModel+" does not support 1080p resolution (supported: 480p, 720p)")
	require.NoError(t, billing_setting.ValidateForkTaskUsageFacts("thirdpartysd2", sd2Model, sd2Model, facts))

	require.NoError(t, applySD2Matrix(`{"`+sd2FastModel+`":{"1080p":{"no_video":6.6,"with_video":3.9}}}`))
	facts = facts1080p(t, sd2FastModel)
	require.NoError(t, billing_setting.ValidateForkTaskUsageFacts("thirdpartysd2", sd2FastModel, sd2FastModel, facts))
	expression, ok := billing_setting.ResolveTaskBillingExpr("thirdpartysd2", sd2FastModel, "")
	require.True(t, ok)
	cost, trace, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
	require.NoError(t, err)
	assert.InDelta(t, 6.6, cost, 1e-9)
	assert.Equal(t, "1080p", trace.MatchedTier)
}

func TestThirdPartySD2UsageFacts(t *testing.T) {
	testCases := []struct {
		name    string
		model   string
		request map[string]any
		want    map[string]any
	}{
		{"1080p with reference video", sd2Model, map[string]any{"prompt": "p", "size": "1920x1080", "metadata": map[string]any{
			"content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://cdn.example/v.mp4"}}},
		}}, map[string]any{"tokens": float64(1000000), "output_resolution": "1080p", "video_input": "video"}},
		{"720p without video", sd2Model, map[string]any{"prompt": "p", "size": "1280x720"}, map[string]any{"tokens": float64(1000000), "output_resolution": "720p", "video_input": "none"}},
		{"fast 480p video_url key without type", sd2FastModel, map[string]any{"prompt": "p", "metadata": map[string]any{
			"resolution": "480p", "content": []any{map[string]any{"video_url": map[string]any{"url": "https://cdn.example/v.mp4"}}},
		}}, map[string]any{"tokens": float64(1000000), "output_resolution": "480p", "video_input": "video"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			adaptor, info, c := sd2Submit(t, testCase.model, testCase.model, testCase.request)
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			facts, err := adaptor.ExtractUsageFactsValidated(c, info)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, facts)
		})
	}
}

// The admin pricing matrix is served to the plugin as a task usage expression:
// submission reserves the matrix price for 1M tokens (the former Go adaptor's
// pre-consume) and completion settles on the upstream token usage.
func TestThirdPartySD2MatrixBillingExpression(t *testing.T) {
	plugin := loadSD2Plugin(t)
	for _, modelName := range []string{sd2Model, sd2FastModel} {
		expression, ok := billing_setting.ResolveTaskBillingExpr("thirdpartysd2", modelName, "")
		require.True(t, ok, modelName)
		schema, _ := plugin.Meta.UsageForModel(modelName)
		assert.True(t, billing_setting.TaskExprCompatible(expression, schema), modelName)
	}
	_, ok := billing_setting.ResolveTaskBillingExpr("doubao", sd2Model, "")
	assert.False(t, ok, "the matrix only prices the thirdpartysd2 plugin")

	aliasExpression, ok := billing_setting.ResolveTaskBillingExpr("thirdpartysd2", "my-sd2-alias", sd2Model)
	require.True(t, ok)
	expression, _ := model_setting.GetThirdPartySD2BillingExpr(sd2Model)
	assert.Equal(t, expression, aliasExpression)

	testCases := []struct {
		model      string
		resolution string
		videoInput string
		tokens     float64
		wantCost   float64
		wantTier   string
	}{
		{sd2Model, "480p", "none", 1000000, 7.0, "480p"},
		{sd2Model, "720p", "video", 1000000, 4.3, "720p_video"},
		{sd2Model, "1080p", "video", 1000000, 4.7, "1080p_video"},
		{sd2Model, "1080p", "none", 1000000, 7.7, "1080p"},
		{sd2Model, "4k", "none", 1000000, 4.0, "4k"},
		{sd2Model, "4k", "video", 1000000, 2.4, "4k_video"},
		{sd2FastModel, "720p", "none", 243000, 5.6 * 0.243, "720p"},
		{sd2FastModel, "480p", "video", 1000000, 3.3, "480p_video"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.model+"/"+testCase.resolution+"/"+testCase.videoInput, func(t *testing.T) {
			expression, ok := billing_setting.ResolveTaskBillingExpr("thirdpartysd2", testCase.model, "")
			require.True(t, ok)
			cost, trace, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: map[string]any{
				"tokens": testCase.tokens, "output_resolution": testCase.resolution, "video_input": testCase.videoInput,
			}})
			require.NoError(t, err)
			assert.InDelta(t, testCase.wantCost, cost, 1e-9)
			assert.Equal(t, testCase.wantTier, trace.MatchedTier)
		})
	}

	t.Run("completion overlays upstream tokens on the frozen facts", func(t *testing.T) {
		expression, _ := billing_setting.ResolveTaskBillingExpr("thirdpartysd2", sd2Model, "")
		snapshot := &billingexpr.BillingSnapshot{
			BillingMode: billing_setting.BillingModeTieredExpr, ModelName: sd2Model, ExprString: expression,
			ExprHash: billingexpr.ExprHashString(expression), GroupRatio: 1, QuotaPerUnit: common.QuotaPerUnit,
			ExprVersion: billingexpr.ExprVersion(expression), TaskUsageBilling: true,
			UsageFacts: map[string]any{"tokens": float64(1000000), "output_resolution": "1080p", "video_input": "none"},
		}
		result, facts, err := service.EvaluateTaskCompletionUsage(snapshot, map[string]any{"tokens": float64(250000)})
		require.NoError(t, err)
		assert.Equal(t, float64(250000), facts["tokens"])
		assert.Equal(t, "1080p", facts["output_resolution"])
		assert.Equal(t, int(7.7*0.25*common.QuotaPerUnit+0.5), result.ActualQuotaAfterGroup)
	})
}

func TestThirdPartySD2MatrixOverrideRebuildsExpression(t *testing.T) {
	original, ok := model_setting.GetThirdPartySD2BillingExpr(sd2Model)
	require.True(t, ok)
	saved, err := config.ConfigToMap(config.GlobalConfig.Get("thirdpartysd2_pricing"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("thirdpartysd2_pricing", saved))
		model_setting.RebuildThirdPartySD2PricingIndex()
	})
	require.NoError(t, applySD2Matrix(`{"`+sd2Model+`":{"1080P":{"no_video":9.9,"with_video":5.5}}}`))
	updated, ok := model_setting.GetThirdPartySD2BillingExpr(sd2Model)
	require.True(t, ok)
	assert.NotEqual(t, original, updated)
	cost, _, err := billingexpr.RunExprWithRequest(updated, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: map[string]any{
		"tokens": float64(1000000), "output_resolution": "1080p", "video_input": "none",
	}})
	require.NoError(t, err)
	assert.InDelta(t, 9.9, cost, 1e-9)
}

func applySD2Matrix(matrix string) error {
	if err := model_setting.ValidateThirdPartySD2PricingMatrixJSON(matrix); err != nil {
		return err
	}
	if err := config.GlobalConfig.UpdateFromMap("thirdpartysd2_pricing", map[string]string{"matrix": matrix}); err != nil {
		return err
	}
	model_setting.RebuildThirdPartySD2PricingIndex()
	return nil
}

func TestThirdPartySD2ParseSubmitResponse(t *testing.T) {
	adaptor, info, c := sd2Submit(t, sd2Model, sd2Model, map[string]any{"prompt": "p", "size": "1280x720"})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"task":{"id":"mvt-1","status":"queued"}}`))}
	parsed, taskErr := adaptor.ParseResponse(c, response, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "mvt-1", parsed.UpstreamTaskID)
	assert.JSONEq(t, `{"task":{"id":"mvt-1","status":"queued"}}`, string(parsed.TaskData))

	missing := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"task":{}}`))}
	_, taskErr = adaptor.ParseResponse(c, missing, info)
	require.NotNil(t, taskErr)
	assert.Contains(t, taskErr.Message, "task_id is empty")
}

func TestThirdPartySD2PollAndSettlementUsage(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"task":{"id":"mvt-1","status":"succeeded","outputs":["","https://sd2.example/files/out.mp4"],"usage":{"completion_tokens":200000,"total_tokens":243000}}}`))
	}))
	defer server.Close()

	adaptor := taskplugin.New(loadSD2Plugin(t))
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}})
	task := &model.Task{TaskID: "task_public", Properties: model.Properties{OriginModelName: sd2Model}}
	task.PrivateData.UpstreamTaskID = "mvt-1"

	resp, err := adaptor.FetchTask(server.URL, "channel-key", task, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "/v1/video/tasks/mvt-1", gotPath)
	assert.Equal(t, "Bearer channel-key", gotAuth)

	result, err := adaptor.ParseTaskResult(task, resp, body)
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", result.Status)
	assert.Equal(t, "100%", result.Progress)
	assert.Equal(t, "https://sd2.example/files/out.mp4", result.Url)
	assert.Equal(t, 200000, result.CompletionTokens)
	assert.Equal(t, 243000, result.TotalTokens)
	assert.Equal(t, map[string]any{"tokens": float64(243000)}, result.UsageFacts)
}

func TestThirdPartySD2ParseTaskResultStatuses(t *testing.T) {
	plugin := loadSD2Plugin(t)
	testCases := []struct {
		body         string
		wantStatus   string
		wantProgress string
		wantReason   string
	}{
		{`{"task":{"status":"pending"}}`, "QUEUED", "10%", ""},
		{`{"task":{"status":"queued"}}`, "QUEUED", "10%", ""},
		{`{"task":{"status":"processing"}}`, "IN_PROGRESS", "50%", ""},
		{`{"task":{"status":"running"}}`, "IN_PROGRESS", "50%", ""},
		{`{"task":{"status":"in_progress"}}`, "IN_PROGRESS", "50%", ""},
		{`{"task":{"status":"completed","outputs":[]}}`, "SUCCESS", "100%", ""},
		{`{"task":{"status":"cancelled"}}`, "FAILURE", "100%", "task failed"},
		{`{"task":{"id":"mvt-6cd7ccae39014a3a","status":"failed","error":"The request failed because the output audio may contain sensitive information."}}`, "FAILURE", "100%", "The request failed because the output audio may contain sensitive information."},
		{`{"task":{"status":"error","error":{"code":"content_blocked","message":"The request was blocked."}}}`, "FAILURE", "100%", "The request was blocked."},
		{`{"task":{"status":"failed","error":{"code":"content_blocked"}}}`, "FAILURE", "100%", "content_blocked"},
		{`{"task":{"status":"warming_up"}}`, "IN_PROGRESS", "30%", ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.body, func(t *testing.T) {
			var body any
			require.NoError(t, common.Unmarshal([]byte(testCase.body), &body))
			value, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, body)
			require.NoError(t, err)
			result := pluginResultObject(t, value)
			assert.Equal(t, testCase.wantStatus, result["status"])
			assert.Equal(t, testCase.wantProgress, result["progress"])
			reason, _ := result["reason"].(string)
			assert.Equal(t, testCase.wantReason, reason)
			assert.NotContains(t, result, "url")
		})
	}

	_, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, map[string]any{"code": 500})
	require.ErrorContains(t, err, "task is empty")
}

func TestThirdPartySD2ArtifactContentRequest(t *testing.T) {
	adaptor := taskplugin.New(loadSD2Plugin(t))
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sd2-key", ChannelBaseUrl: "https://sd2.example"}})

	sameHost := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess}
	sameHost.SetData(map[string]any{"task": map[string]any{"status": "succeeded", "outputs": []any{"https://SD2.example/files/out.mp4"}}})
	artifacts, err := adaptor.ListArtifacts(sameHost)
	require.NoError(t, err)
	assert.Equal(t, []relaychannel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
	descriptor, err := adaptor.BuildContentRequest(sameHost, "video", relaychannel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, "https://SD2.example/files/out.mp4", descriptor.URL)
	assert.Equal(t, map[string]string{"Authorization": "Bearer sd2-key"}, descriptor.Headers)
	assert.False(t, descriptor.Credentialless)

	otherHost := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess}
	otherHost.SetData(map[string]any{"task": map[string]any{"status": "succeeded", "outputs": []any{"https://cdn.example/out.mp4?sig=1"}}})
	descriptor, err = adaptor.BuildContentRequest(otherHost, "video", sd2HeadRequest())
	require.NoError(t, err)
	assert.True(t, descriptor.Credentialless)
	assert.Empty(t, descriptor.Headers)
	assert.Equal(t, http.MethodHead, descriptor.Method)

	sameHostDefaultPort := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess}
	sameHostDefaultPort.SetData(map[string]any{"task": map[string]any{"status": "succeeded", "outputs": []any{"https://sd2.example:443/files/out.mp4"}}})
	descriptor, err = adaptor.BuildContentRequest(sameHostDefaultPort, "video", relaychannel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Authorization": "Bearer sd2-key"}, descriptor.Headers)

	userinfoHost := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess}
	userinfoHost.SetData(map[string]any{"task": map[string]any{"status": "succeeded", "outputs": []any{"https://sd2.example@cdn.example/out.mp4"}}})
	descriptor, err = adaptor.BuildContentRequest(userinfoHost, "video", relaychannel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	assert.True(t, descriptor.Credentialless)
	assert.Empty(t, descriptor.Headers)

	empty := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess}
	empty.SetData(map[string]any{"task": map[string]any{"status": "succeeded", "outputs": []any{}}})
	artifacts, err = adaptor.ListArtifacts(empty)
	require.NoError(t, err)
	assert.Empty(t, artifacts)
}

// Credential hosts are declared once in the plugin: the same list is the
// manifest allowedHosts the host validates credentialed requests against.
func TestThirdPartySD2ArtifactContentRequestDeclaredCredentialHosts(t *testing.T) {
	source, err := builtinplugins.Source("thirdpartysd2")
	require.NoError(t, err)
	const declaration = "const CREDENTIAL_HOSTS = [];"
	require.Contains(t, source, declaration)
	source = strings.Replace(source, declaration, `const CREDENTIAL_HOSTS = ["files.sd2cdn.example", "media.sd2cdn.example:8443"];`, 1)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "thirdpartysd2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"files.sd2cdn.example", "media.sd2cdn.example:8443"}, plugin.Meta.AllowedHosts)

	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sd2-key", ChannelBaseUrl: "https://sd2.example"}})
	testCases := []struct {
		output         string
		wantCredential bool
	}{
		{"https://files.sd2cdn.example/out.mp4", true},
		{"https://FILES.sd2cdn.example:443/out.mp4", true},
		{"https://media.sd2cdn.example:8443/out.mp4", true},
		{"https://media.sd2cdn.example/out.mp4", false},
		{"https://other.sd2cdn.example/out.mp4", false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.output, func(t *testing.T) {
			task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess}
			task.SetData(map[string]any{"task": map[string]any{"status": "succeeded", "outputs": []any{testCase.output}}})
			descriptor, err := adaptor.BuildContentRequest(task, "video", relaychannel.TaskArtifactClientRequest{Method: http.MethodGet})
			require.NoError(t, err)
			assert.Equal(t, testCase.output, descriptor.URL)
			if testCase.wantCredential {
				assert.Equal(t, map[string]string{"Authorization": "Bearer sd2-key"}, descriptor.Headers)
				assert.False(t, descriptor.Credentialless)
				return
			}
			assert.True(t, descriptor.Credentialless)
			assert.Empty(t, descriptor.Headers)
		})
	}
}

func sd2HeadRequest() relaychannel.TaskArtifactClientRequest {
	return relaychannel.TaskArtifactClientRequest{Method: http.MethodHead}
}

func TestThirdPartySD2OpenAIVideoRenderHidesUpstreamOutputs(t *testing.T) {
	adaptor := taskplugin.New(loadSD2Plugin(t))
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})

	stringError := &model.Task{TaskID: "task_public", Status: model.TaskStatusFailure, Progress: "100%", FailReason: "fallback message",
		Properties: model.Properties{OriginModelName: sd2Model},
		Data:       []byte(`{"task":{"id":"mvt-6cd7ccae39014a3a","status":"failed","error":"The request failed because the output audio may contain sensitive information."}}`)}
	rendered, err := adaptor.ConvertToOpenAIVideo(stringError)
	require.NoError(t, err)
	var video map[string]any
	require.NoError(t, common.Unmarshal(rendered, &video))
	assert.Equal(t, map[string]any{"message": "The request failed because the output audio may contain sensitive information.", "code": "task_failed"}, video["error"])

	objectError := &model.Task{TaskID: "task_public", Status: model.TaskStatusFailure, Progress: "100%", FailReason: "fallback message",
		Properties: model.Properties{OriginModelName: sd2Model},
		Data:       []byte(`{"task":{"status":"failed","error":{"code":"content_blocked","message":"blocked"}}}`)}
	rendered, err = adaptor.ConvertToOpenAIVideo(objectError)
	require.NoError(t, err)
	video = map[string]any{}
	require.NoError(t, common.Unmarshal(rendered, &video))
	assert.Equal(t, map[string]any{"message": "blocked", "code": "content_blocked"}, video["error"])

	success := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, Progress: "100%", CreatedAt: 1, FinishTime: 2,
		Properties: model.Properties{OriginModelName: sd2Model},
		Data:       []byte(`{"task":{"status":"succeeded","outputs":["https://sd2.example/files/secret.mp4"]}}`)}
	rendered, err = adaptor.ConvertToOpenAIVideo(success)
	require.NoError(t, err)
	assert.NotContains(t, string(rendered), "secret.mp4")
	video = map[string]any{}
	require.NoError(t, common.Unmarshal(rendered, &video))
	assert.Equal(t, "completed", video["status"])
	assert.NotContains(t, video, "error")
}

// Tasks created by the former Go adaptor keep platform "58" and the same
// {"task":{...}} data shape. They resolve to the plugin, and every SD2 task
// exposes only the gateway content proxy instead of the credential-gated URL.
func TestThirdPartySD2LegacyTasksAndResultURL(t *testing.T) {
	plugin, ok := relay.ResolveTaskPluginForPlatform(jsplugin.DefaultRegistry.Generation(), constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeThirdPartySD2)))
	require.True(t, ok)
	assert.Equal(t, "thirdpartysd2", plugin.Meta.Key)
	require.NotNil(t, relay.GetTaskAdaptor(constant.TaskPlatform("58")))

	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k", ChannelBaseUrl: "https://sd2.example"}})
	legacy := &model.Task{TaskID: "task_legacy", Platform: "58", Action: "generate", Status: model.TaskStatusInProgress,
		Data: []byte(`{"task":{"id":"mvt-legacy","status":"queued"}}`), Properties: model.Properties{OriginModelName: sd2Model}}
	legacy.PrivateData.UpstreamTaskID = "mvt-legacy"
	result, err := adaptor.ParseTaskResult(legacy, &http.Response{StatusCode: http.StatusOK}, []byte(`{"task":{"id":"mvt-legacy","status":"completed","outputs":["https://sd2.example/files/legacy.mp4"],"usage":{"total_tokens":100}}}`))
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", result.Status)
	assert.Equal(t, "https://sd2.example/files/legacy.mp4", result.Url)
	// Legacy billing contexts settle through RecalculateTaskQuotaByTokens.
	assert.Equal(t, 100, result.TotalTokens)

	for _, platform := range []constant.TaskPlatform{"58", "thirdpartysd2"} {
		task := &model.Task{TaskID: "task_x", Platform: platform, Status: model.TaskStatusSuccess}
		task.PrivateData.ResultURL = "https://sd2.example/files/secret.mp4"
		dto := relay.TaskModel2Dto(task)
		assert.True(t, strings.HasSuffix(dto.ResultURL, "/v1/videos/task_x/content"), string(platform))
		assert.NotContains(t, dto.ResultURL, "secret.mp4")
	}
}
