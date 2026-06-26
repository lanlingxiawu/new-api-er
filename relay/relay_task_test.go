package relay

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/stretchr/testify/require"
)

func TestGetExternalVideoURLForThirdPartySD2(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_sd2_ok",
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeThirdPartySD2)),
		Status:   model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://example.com/video.mp4",
		},
	}

	require.Equal(t, taskcommon.BuildProxyURL(task.TaskID), getExternalVideoURL(task))
}

func TestGetExternalVideoURLForThirdPartySD2WithoutUpstreamURL(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_sd2_empty",
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeThirdPartySD2)),
		Status:   model.TaskStatusSuccess,
	}

	require.Empty(t, getExternalVideoURL(task))
}

func TestGetExternalVideoURLForThirdPartySD2SkipsSelfProxyURL(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_sd2_proxy",
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeThirdPartySD2)),
		Status:   model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			ResultURL: taskcommon.BuildProxyURL("task_sd2_proxy"),
		},
	}

	require.Empty(t, getExternalVideoURL(task))
}
