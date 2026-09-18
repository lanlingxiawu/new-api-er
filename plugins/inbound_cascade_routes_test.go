package plugins_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A downstream gateway cascades these models to this gateway through a Task
// Plugin channel keyed with the same plugin and a base URL carrying the route
// prefix. The inbound routes therefore speak each provider's own request and
// response shapes: the request forwarded upstream repeats the inbound request,
// and the plugin parses the responses it renders.

func TestThirdPartySD2InboundCascadeRoutes(t *testing.T) {
	registry := jsplugin.NewRegistry()
	source, err := builtinplugins.Source("thirdpartysd2")
	require.NoError(t, err)
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "thirdpartysd2"})
	require.NoError(t, err)

	submitRoute, found := registry.Generation().LookupDeclaredRoute(http.MethodPost, "/sd2/v1/video/generate")
	require.True(t, found)
	assert.Equal(t, "submit", string(submitRoute.Route.Type))
	assert.Equal(t, "createTask", submitRoute.Route.Decode)
	assert.Equal(t, "taskCreated", submitRoute.Route.Render)
	queryRoute, found := registry.Generation().LookupDeclaredRoute(http.MethodGet, "/sd2/v1/video/tasks/:task_id")
	require.True(t, found)
	assert.Equal(t, "query", string(queryRoute.Route.Type))
	assert.Equal(t, "taskStatus", queryRoute.Route.Render)
	assert.Equal(t, "task_id", queryRoute.Route.TaskIDParam)

	// The provider-shaped body a downstream gateway running this plugin sends.
	inbound := map[string]any{
		"model": sd2FastModel,
		"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example/a.png"}},
			map[string]any{"type": "text", "text": "a running fox"},
		},
		"resolution": "480p", "ratio": "16:9", "duration": float64(5), "generate_audio": true,
	}
	value, err := plugin.Engine.CallPath(t.Context(), "native", []string{"createTask"}, map[string]any{
		"path": "/sd2/v1/video/generate", "method": http.MethodPost,
		"body": map[string]any{"kind": "json", "value": inbound},
	})
	require.NoError(t, err)
	intent := pluginResultObject(t, value)
	assert.Equal(t, "submit", intent["kind"])
	assert.Equal(t, sd2FastModel, intent["model"])
	assert.Equal(t, "image_to_video", intent["action"])
	requestBody, ok := intent["requestBody"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "a running fox", requestBody["prompt"])

	adaptor, info, c := sd2Submit(t, sd2FastModel, sd2FastModel, requestBody)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	forwarded, err := io.ReadAll(reader)
	require.NoError(t, err)
	inboundJSON, err := common.Marshal(inbound)
	require.NoError(t, err)
	assert.JSONEq(t, string(inboundJSON), string(forwarded), "the upstream request repeats the inbound request")

	// Submit response shape: {"task":{"id":...}}, parsed by this same plugin.
	queued := map[string]any{
		"task_id": "task_public", "status": "SUBMITTED", "progress": "10%",
		"properties": map[string]any{"origin_model_name": sd2FastModel},
		"data":       map[string]any{"task": map[string]any{"id": "task_public", "model": sd2FastModel, "status": "queued", "created_at": "2026-01-01T00:00:00Z"}},
	}
	value, err = plugin.Engine.CallPath(t.Context(), "native", []string{"taskCreated"}, map[string]any{}, queued)
	require.NoError(t, err)
	created := pluginResultObject(t, value)
	assert.Equal(t, map[string]any{"id": "task_public", "model": sd2FastModel, "status": "queued", "created_at": "2026-01-01T00:00:00Z"}, created["task"])
	value, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{}, map[string]any{"body": created})
	require.NoError(t, err)
	assert.Equal(t, "task_public", pluginResultObject(t, value)["taskId"])

	// Status response shape: the upstream usage passes through and the output
	// URL is the content proxy path of this gateway, not the upstream URL.
	succeeded := map[string]any{
		"task_id": "task_public", "status": "SUCCESS", "progress": "100%",
		"properties": map[string]any{"origin_model_name": sd2FastModel},
		"data": map[string]any{"task": map[string]any{
			"id": "task_public", "model": sd2FastModel, "status": "succeeded",
			"outputs": []any{"https://sd2.example/files/out.mp4"}, "duration_seconds": float64(5), "ratio": "16:9",
			"usage": map[string]any{"completion_tokens": float64(108900), "total_tokens": float64(108900)},
		}},
	}
	value, err = plugin.Engine.CallPath(t.Context(), "native", []string{"taskStatus"}, map[string]any{}, succeeded)
	require.NoError(t, err)
	status := pluginResultObject(t, value)
	statusJSON, err := common.Marshal(status)
	require.NoError(t, err)
	assert.NotContains(t, string(statusJSON), "sd2.example")
	task, ok := status["task"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "succeeded", task["status"])
	assert.Equal(t, "task_public", task["id"])
	assert.Equal(t, []any{"/v1/videos/task_public/content"}, task["outputs"])
	assert.Equal(t, map[string]any{"completion_tokens": float64(108900), "total_tokens": float64(108900)}, task["usage"])

	value, err = plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, status)
	require.NoError(t, err)
	result := pluginResultObject(t, value)
	assert.Equal(t, "SUCCESS", result["status"])
	assert.Equal(t, "/v1/videos/task_public/content", result["url"])
	assert.Equal(t, float64(108900), result["totalTokens"])

	// The downstream resolves that path against its channel base URL origin and
	// sends the channel credential: the content is on the channel host.
	cascade := taskplugin.New(plugin)
	cascade.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "downstream-key", ChannelBaseUrl: "https://upstream.example/sd2"}})
	cascaded := &model.Task{TaskID: "task_downstream", Status: model.TaskStatusSuccess}
	cascaded.SetData(status)
	descriptor, err := cascade.BuildContentRequest(cascaded, "video", relaychannel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/task_public/content", descriptor.URL)
	assert.Equal(t, map[string]string{"Authorization": "Bearer downstream-key"}, descriptor.Headers)
	assert.False(t, descriptor.Credentialless)

	// Failures keep the provider error shape.
	failed := map[string]any{
		"task_id": "task_public", "status": "FAILURE", "progress": "100%", "fail_reason": "The request was blocked.",
		"properties": map[string]any{"origin_model_name": sd2FastModel},
		"data":       map[string]any{"task": map[string]any{"id": "task_public", "status": "failed"}},
	}
	value, err = plugin.Engine.CallPath(t.Context(), "native", []string{"taskStatus"}, map[string]any{}, failed)
	require.NoError(t, err)
	status = pluginResultObject(t, value)
	value, err = plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, status)
	require.NoError(t, err)
	result = pluginResultObject(t, value)
	assert.Equal(t, "FAILURE", result["status"])
	assert.Equal(t, "The request was blocked.", result["reason"])
}

func TestThirdPartySD2InboundCascadeRejectsInvalidRequests(t *testing.T) {
	plugin := loadSD2Plugin(t)
	tests := []struct {
		name    string
		body    map[string]any
		message string
	}{
		{"missing model", map[string]any{"kind": "json", "value": map[string]any{"content": []any{map[string]any{"type": "text", "text": "p"}}}}, "model is required"},
		{"missing prompt item", map[string]any{"kind": "json", "value": map[string]any{"model": sd2FastModel, "content": []any{}}}, "content must contain a text item"},
		{"content not an array", map[string]any{"kind": "json", "value": map[string]any{"model": sd2FastModel, "content": "p"}}, "content must be an array"},
		{"body not an object", map[string]any{"kind": "json", "value": []any{float64(1)}}, "request body must be an object"},
		{"no body", map[string]any{"kind": "none"}, "JSON body required"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := plugin.Engine.CallPath(t.Context(), "native", []string{"createTask"}, map[string]any{
				"path": "/sd2/v1/video/generate", "method": http.MethodPost, "body": testCase.body,
			})
			require.ErrorContains(t, err, testCase.message)
		})
	}
}

func TestXaiInboundCascadeRoutes(t *testing.T) {
	registry := jsplugin.NewRegistry()
	source, err := builtinplugins.Source("xai")
	require.NoError(t, err)
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "xai"})
	require.NoError(t, err)

	submitRoute, found := registry.Generation().LookupDeclaredRoute(http.MethodPost, "/xai/v1/videos/generations")
	require.True(t, found)
	assert.Equal(t, "submit", string(submitRoute.Route.Type))
	assert.Equal(t, "createVideoTask", submitRoute.Route.Decode)
	assert.Equal(t, "videoCreated", submitRoute.Route.Render)
	queryRoute, found := registry.Generation().LookupDeclaredRoute(http.MethodGet, "/xai/v1/videos/:request_id")
	require.True(t, found)
	assert.Equal(t, "query", string(queryRoute.Route.Type))
	assert.Equal(t, "videoStatus", queryRoute.Route.Render)
	assert.Equal(t, "request_id", queryRoute.Route.TaskIDParam)

	const model15 = "grok-imagine-video-1.5"
	inbound := map[string]any{
		"model": model15, "prompt": "a running fox", "duration": float64(6),
		"aspect_ratio": "16:9", "resolution": "720p", "seed": float64(42),
		"image": map[string]any{"url": "https://cdn.example/a.png"},
	}
	value, err := plugin.Engine.CallPath(t.Context(), "native", []string{"createVideoTask"}, map[string]any{
		"path": "/xai/v1/videos/generations", "method": http.MethodPost,
		"body": map[string]any{"kind": "json", "value": inbound},
	})
	require.NoError(t, err)
	intent := pluginResultObject(t, value)
	assert.Equal(t, "submit", intent["kind"])
	assert.Equal(t, model15, intent["model"])
	assert.Equal(t, "image_to_video", intent["action"])
	requestBody, ok := intent["requestBody"].(map[string]any)
	require.True(t, ok)

	forwarded := xaiSubmitBody(t, model15, requestBody)
	inboundJSON, err := common.Marshal(inbound)
	require.NoError(t, err)
	forwardedJSON, err := common.Marshal(forwarded)
	require.NoError(t, err)
	assert.JSONEq(t, string(inboundJSON), string(forwardedJSON), "the upstream request repeats the inbound request")

	// Submit response shape: {"request_id":...}.
	queued := map[string]any{"task_id": "task_public", "status": "SUBMITTED", "properties": map[string]any{"origin_model_name": model15}}
	value, err = plugin.Engine.CallPath(t.Context(), "native", []string{"videoCreated"}, map[string]any{}, queued)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"request_id": "task_public"}, pluginResultObject(t, value))
	value, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"request_id": "task_public"}})
	require.NoError(t, err)
	assert.Equal(t, "task_public", pluginResultObject(t, value)["taskId"])

	// Status response shape: the provider CDN URL stays private and clients of
	// the cascade download through the content proxy of this gateway.
	succeeded := map[string]any{
		"task_id": "task_public", "status": "SUCCESS", "progress": "100%",
		"properties": map[string]any{"origin_model_name": model15},
		"data":       map[string]any{"status": "done", "video": map[string]any{"url": "https://vidgen.x.ai/out.mp4", "duration": float64(6)}},
	}
	value, err = plugin.Engine.CallPath(t.Context(), "native", []string{"videoStatus"}, map[string]any{}, succeeded)
	require.NoError(t, err)
	status := pluginResultObject(t, value)
	statusJSON, err := common.Marshal(status)
	require.NoError(t, err)
	assert.NotContains(t, string(statusJSON), "vidgen.x.ai")
	assert.Equal(t, "done", status["status"])
	assert.Equal(t, "task_public", status["request_id"])
	assert.Equal(t, model15, status["model"])
	assert.Equal(t, map[string]any{"url": "/v1/videos/task_public/content", "duration": float64(6)}, status["video"])

	value, err = plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, status)
	require.NoError(t, err)
	result := pluginResultObject(t, value)
	assert.Equal(t, "SUCCESS", result["status"])
	assert.Equal(t, "/v1/videos/task_public/content", result["url"])

	cascade := taskplugin.New(plugin)
	cascade.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "downstream-key", ChannelBaseUrl: "https://upstream.example/xai"}})
	cascaded := &model.Task{TaskID: "task_downstream", Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: model15}}
	cascaded.SetData(status)
	artifacts, err := cascade.ListArtifacts(cascaded)
	require.NoError(t, err)
	assert.Equal(t, []relaychannel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
	descriptor, err := cascade.BuildContentRequest(cascaded, "video", relaychannel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/task_public/content", descriptor.URL)
	assert.Equal(t, map[string]string{"Authorization": "Bearer downstream-key"}, descriptor.Headers)
	assert.False(t, descriptor.Credentialless)

	// A gateway-hosted video URL is not handed to clients as a provider URL.
	rendered, err := cascade.ConvertToOpenAIVideo(cascaded)
	require.NoError(t, err)
	var video map[string]any
	require.NoError(t, common.Unmarshal(rendered, &video))
	assert.Equal(t, "completed", video["status"])
	assert.NotContains(t, video, "metadata")

	failed := map[string]any{
		"task_id": "task_public", "status": "FAILURE", "progress": "100%", "fail_reason": "blocked",
		"properties": map[string]any{"origin_model_name": model15},
		"data":       map[string]any{"status": "failed", "error": map[string]any{"code": "content_blocked", "message": "blocked"}},
	}
	value, err = plugin.Engine.CallPath(t.Context(), "native", []string{"videoStatus"}, map[string]any{}, failed)
	require.NoError(t, err)
	status = pluginResultObject(t, value)
	assert.Equal(t, "failed", status["status"])
	assert.Equal(t, map[string]any{"code": "content_blocked", "message": "blocked"}, status["error"])
	value, err = plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, status)
	require.NoError(t, err)
	result = pluginResultObject(t, value)
	assert.Equal(t, "FAILURE", result["status"])
	assert.Equal(t, "blocked", result["reason"])
}

func TestXaiInboundCascadeRejectsInvalidRequests(t *testing.T) {
	plugin := loadXaiPlugin(t)
	tests := []struct {
		name    string
		body    map[string]any
		message string
	}{
		{"missing model", map[string]any{"kind": "json", "value": map[string]any{"prompt": "p"}}, "model is required"},
		{"missing prompt", map[string]any{"kind": "json", "value": map[string]any{"model": "grok-imagine-video"}}, "prompt is required"},
		{"body not an object", map[string]any{"kind": "json", "value": []any{float64(1)}}, "request body must be an object"},
		{"no body", map[string]any{"kind": "none"}, "JSON body required"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := plugin.Engine.CallPath(t.Context(), "native", []string{"createVideoTask"}, map[string]any{
				"path": "/xai/v1/videos/generations", "method": http.MethodPost, "body": testCase.body,
			})
			require.ErrorContains(t, err, testCase.message)
		})
	}
}
