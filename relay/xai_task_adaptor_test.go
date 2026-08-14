package relay

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTaskAdaptorReturnsXaiAdaptor(t *testing.T) {
	adaptor := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeXai)))

	require.NotNil(t, adaptor)
	assert.Equal(t, "xai", adaptor.GetChannelName())
	assert.Contains(t, adaptor.GetModelList(), "grok-imagine-video-1.5")
}

func TestTaskModel2DtoIncludesBillingOther(t *testing.T) {
	task := &model.Task{
		TaskID:    "task_xai",
		Platform:  constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeXai)),
		UserId:    9,
		ChannelId: 10,
		Quota:     560000,
		Properties: model.Properties{
			OriginModelName:   "grok-imagine-video-1.5",
			UpstreamModelName: "grok-imagine-video-1.5",
		},
		PrivateData: model.TaskPrivateData{
			Key: "sk-hidden",
			BillingContext: &model.TaskBillingContext{
				ModelPrice:      0.08,
				GroupRatio:      1,
				OriginModelName: "grok-imagine-video-1.5",
				PerCallBilling:  true,
				OtherRatios: map[string]float64{
					"xai_video_units": 14,
				},
				PricingMetadata: map[string]string{
					"xai_video_model":       "grok-imagine-video-1.5",
					"duration_seconds":      "8",
					"resolution":            "720p",
					"reference_image_count": "0",
					"requested_resolution":  "720p",
				},
			},
		},
	}

	dto := TaskModel2Dto(task)

	require.NotNil(t, dto.Other)
	assert.Equal(t, 0.08, dto.Other["model_price"])
	assert.Equal(t, 1.0, dto.Other["group_ratio"])
	assert.Equal(t, 14.0, dto.Other["xai_video_units"])
	assert.Equal(t, "8", dto.Other["pricing_duration_seconds"])
	assert.Equal(t, "720p", dto.Other["pricing_resolution"])
	assert.NotContains(t, dto.Other, "key")
	assert.NotContains(t, dto.Other, "upstream_task_id")
}
