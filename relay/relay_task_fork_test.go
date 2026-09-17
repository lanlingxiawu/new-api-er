package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTaskAddresses(t *testing.T, taskPublicAddress, serverAddress string) {
	t.Helper()
	previousTaskPublic, previousServer := system_setting.TaskPublicAddress, system_setting.ServerAddress
	system_setting.TaskPublicAddress, system_setting.ServerAddress = taskPublicAddress, serverAddress
	t.Cleanup(func() {
		system_setting.TaskPublicAddress, system_setting.ServerAddress = previousTaskPublic, previousServer
	})
}

func TestThirdPartySD2ContentURLPrefersTaskPublicAddress(t *testing.T) {
	tests := []struct {
		name, taskPublic, server, want string
	}{
		{"task public address wins", "https://media.example.com/", "https://api.example.com", "https://media.example.com/v1/videos/task_1/content"},
		{"server address fallback", "", "https://api.example.com/", "https://api.example.com/v1/videos/task_1/content"},
		{"no address keeps the relative path", "", "", "/v1/videos/task_1/content"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			setTaskAddresses(t, testCase.taskPublic, testCase.server)
			assert.Equal(t, testCase.want, thirdPartySD2ContentURL("task_1"))
		})
	}
}

func TestWithThirdPartySD2ContentURL(t *testing.T) {
	setTaskAddresses(t, "", "https://api.example.com")
	rendered := []byte(`{"id":"task_1","object":"video","status":"completed","metadata":{"vendor":"x"}}`)

	for _, platform := range []constant.TaskPlatform{"thirdpartysd2", "58"} {
		t.Run("successful "+string(platform)+" task gets the proxy url", func(t *testing.T) {
			task := &model.Task{TaskID: "task_1", Platform: platform, Status: model.TaskStatusSuccess}
			task.PrivateData.ResultURL = "https://sd2.example/files/secret.mp4"
			var video map[string]any
			require.NoError(t, common.Unmarshal(withThirdPartySD2ContentURL(task, rendered), &video))
			assert.Equal(t, map[string]any{"vendor": "x", "url": "https://api.example.com/v1/videos/task_1/content"}, video["metadata"])
			assert.Equal(t, "completed", video["status"])
		})
	}

	unchanged := []struct {
		name string
		task *model.Task
		body []byte
	}{
		{"other platform", &model.Task{TaskID: "task_1", Platform: "xai", Status: model.TaskStatusSuccess, PrivateData: model.TaskPrivateData{ResultURL: "https://cdn.x.ai/v.mp4"}}, rendered},
		{"unfinished task", &model.Task{TaskID: "task_1", Platform: "thirdpartysd2", Status: model.TaskStatusInProgress, PrivateData: model.TaskPrivateData{ResultURL: "https://sd2.example/v.mp4"}}, rendered},
		{"success without output", &model.Task{TaskID: "task_1", Platform: "thirdpartysd2", Status: model.TaskStatusSuccess}, rendered},
		{"unparsable body", &model.Task{TaskID: "task_1", Platform: "thirdpartysd2", Status: model.TaskStatusSuccess, PrivateData: model.TaskPrivateData{ResultURL: "https://sd2.example/v.mp4"}}, []byte(`not json`)},
	}
	for _, testCase := range unchanged {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, string(testCase.body), string(withThirdPartySD2ContentURL(testCase.task, testCase.body)))
		})
	}
}

func TestRecordXaiTaskPricingMetadata(t *testing.T) {
	source, err := builtinplugins.Source("xai")
	require.NoError(t, err)
	plugin, err := pluginruntime.NewRegistry().RegisterFactory(source, pluginruntime.Options{Key: "xai"})
	require.NoError(t, err)

	newSubmit := func(request map[string]any) (*gin.Context, *relaycommon.RelayInfo, *taskplugin.TaskAdaptor) {
		gin.SetMode(gin.TestMode)
		info := &relaycommon.RelayInfo{
			OriginModelName: "grok-imagine-video",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeXai, ChannelBaseUrl: "https://api.x.ai", ApiKey: "k",
				UpstreamModelName: "grok-imagine-video",
			},
			TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
		}
		adaptor := taskplugin.New(plugin)
		adaptor.Init(info)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
		c.Set("task_request", request)
		return c, info, adaptor
	}

	c, info, adaptor := newSubmit(map[string]any{"prompt": "p", "duration": 8, "images": []any{"file_a", "file_b"}, "metadata": map[string]any{"resolution": "1080p"}})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	recordXaiTaskPricingMetadata(c, info, "xai", adaptor)
	assert.Equal(t, map[string]string{
		"duration_seconds":      "8",
		"resolution":            "720p",
		"reference_image_count": "2",
		"xai_video_model":       "grok-imagine-video",
	}, info.PriceData.PricingMetadata)

	c, info, adaptor = newSubmit(map[string]any{"prompt": "p"})
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	recordXaiTaskPricingMetadata(c, info, "thirdpartysd2", adaptor)
	assert.Nil(t, info.PriceData.PricingMetadata)
}
