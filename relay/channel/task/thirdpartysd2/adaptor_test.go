package thirdpartysd2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertToRequestPayloadUsesDurationFallback(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:    "dreamina-seedance-2-0-260128",
		Prompt:   "test",
		Duration: 6,
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, payload.Duration)
	require.Equal(t, 6, int(*payload.Duration))
}

func TestConvertToRequestPayloadPrefersSecondsOverDuration(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:    "dreamina-seedance-2-0-fast-260128",
		Prompt:   "test",
		Duration: 4,
		Seconds:  "8",
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, payload.Duration)
	require.Equal(t, 8, int(*payload.Duration))
}

func TestConvertToRequestPayloadNormalizesResolutionFromSize(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:  "dreamina-seedance-2-0-260128",
		Prompt: "test",
		Size:   "1280x720",
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "720p", payload.Resolution)
}

func TestConvertToRequestPayloadUsesHigherResolutionThanSize(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:  "dreamina-seedance-2-0-260128",
		Prompt: "test",
		Size:   "1920x1080",
		Metadata: map[string]interface{}{
			"resolution": "480p",
		},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "1080p", payload.Resolution)
}

func TestEstimateBillingAppliesResolutionMatrixPricing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Model: "dreamina-seedance-2-0-260128",
		Size:  "1920x1080",
		Metadata: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "video_url"},
			},
		},
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "dreamina-seedance-2-0-260128",
		PriceData: hosttypes.PriceData{
			ModelPrice: -1,
			UsePrice:   true,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1.5,
			},
		},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)
	require.Nil(t, ratios)
	require.False(t, info.PriceData.UsePrice)
	require.Equal(t, -1.0, info.PriceData.ModelPrice)
	require.Equal(t, model_setting.ThirdPartySD2PriceToModelRatio(4.7), info.PriceData.ModelRatio)
	require.Equal(
		t,
		model_setting.CalculateThirdPartySD2Quota(4.7, model_setting.ThirdPartySD2PreConsumedTokenEstimate, 1.5),
		info.PriceData.Quota,
	)
	require.Equal(t, "1080p", info.PriceData.PricingMetadata["resolution"])
	require.Equal(t, "true", info.PriceData.PricingMetadata["video_input"])
}

func TestEstimateBillingUsesInheritedPricingMetadataForRemix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "test",
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "dreamina-seedance-2-0-260128",
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			InheritedPricingMetadata: map[string]string{
				"resolution":  "1080P",
				"video_input": "true",
			},
		},
		PriceData: hosttypes.PriceData{
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
	}

	(&TaskAdaptor{}).EstimateBilling(ctx, info)
	require.Equal(t, model_setting.ThirdPartySD2PriceToModelRatio(4.7), info.PriceData.ModelRatio)
	require.Equal(
		t,
		model_setting.CalculateThirdPartySD2Quota(4.7, model_setting.ThirdPartySD2PreConsumedTokenEstimate, 1),
		info.PriceData.Quota,
	)
	require.Equal(t, "1080p", info.PriceData.PricingMetadata["resolution"])
	require.Equal(t, "true", info.PriceData.PricingMetadata["video_input"])
}

func TestEstimateBillingUsesValidatedPricingContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/video/generate",
		strings.NewReader(`{"prompt":"test","model":"dreamina-seedance-2-0-260128","size":"1280x720"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		PriceData: hosttypes.PriceData{
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
	}

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(ctx, info)
	require.Nil(t, taskErr)

	// Simulate downstream request mutation after validation; billing should still
	// use the validated matrix context instead of silently falling back.
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "mutated",
	})

	(&TaskAdaptor{}).EstimateBilling(ctx, info)
	require.Equal(t, model_setting.ThirdPartySD2PriceToModelRatio(7.0), info.PriceData.ModelRatio)
	require.Equal(
		t,
		model_setting.CalculateThirdPartySD2Quota(7.0, model_setting.ThirdPartySD2PreConsumedTokenEstimate, 1),
		info.PriceData.Quota,
	)
	require.Equal(t, "720p", info.PriceData.PricingMetadata["resolution"])
	require.Equal(t, "false", info.PriceData.PricingMetadata["video_input"])
}

func TestResolveThirdPartySD2ResolutionUsesLargestDeclaredValue(t *testing.T) {
	resolution := resolveThirdPartySD2Resolution(&relaycommon.TaskSubmitReq{
		Size: "640x480",
		Metadata: map[string]interface{}{
			"resolution": "1080p",
		},
	}, map[string]string{
		"resolution": "720p",
	})
	require.Equal(t, "1080p", resolution)
}

func TestResolveThirdPartySD2ResolutionDoesNotPromoteInheritedResolution(t *testing.T) {
	resolution := resolveThirdPartySD2Resolution(&relaycommon.TaskSubmitReq{
		Size: "640x480",
	}, map[string]string{
		"resolution": "1080p",
	})
	require.Equal(t, "480p", resolution)
}

func TestResolveThirdPartySD2ResolutionFallsBackToInheritedWhenRequestOmitsBoth(t *testing.T) {
	resolution := resolveThirdPartySD2Resolution(&relaycommon.TaskSubmitReq{
		Prompt: "test",
	}, map[string]string{
		"resolution": "1080p",
	})
	require.Equal(t, "1080p", resolution)
}

func TestValidateRequestAndSetActionRejectsUnpricedResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/video/generate",
		strings.NewReader(`{"prompt":"test","model":"dreamina-seedance-2-0-fast-260128","size":"3840x2160"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(ctx, &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	})
	require.NotNil(t, taskErr)
	require.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	require.Contains(t, taskErr.Message, "无定价")
}

func TestValidateRequestAndSetActionRejectsMissingModelContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/video/generate",
		strings.NewReader(`{"prompt":"test","size":"1280x720"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(ctx, &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	})
	require.NotNil(t, taskErr)
	require.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	require.Contains(t, taskErr.Message, "缺少模型名称")
}

func TestParseTaskResultSupportsStringError(t *testing.T) {
	taskInfo, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
		"task": {
			"id": "mvt-6cd7ccae39014a3a",
			"status": "failed",
			"error": "The request failed because the output audio may contain sensitive information."
		}
	}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusFailure, taskInfo.Status)
	require.Equal(t, "100%", taskInfo.Progress)
	require.Equal(t, "The request failed because the output audio may contain sensitive information.", taskInfo.Reason)
}

func TestParseTaskResultSupportsObjectError(t *testing.T) {
	taskInfo, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
		"task": {
			"id": "mvt-6cd7ccae39014a3a",
			"status": "failed",
			"error": {
				"code": "content_blocked",
				"message": "The request was blocked."
			}
		}
	}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusFailure, taskInfo.Status)
	require.Equal(t, "The request was blocked.", taskInfo.Reason)
}

func TestConvertToOpenAIVideoSupportsStringError(t *testing.T) {
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(&model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusFailure,
		Progress:   "100%",
		FailReason: "fallback message",
		Properties: model.Properties{
			OriginModelName: "dreamina-seedance-2-0-260128",
		},
		Data: []byte(`{
			"task": {
				"id": "mvt-6cd7ccae39014a3a",
				"status": "failed",
				"error": "The request failed because the output audio may contain sensitive information."
			}
		}`),
	})
	require.NoError(t, err)

	var video dto.OpenAIVideo
	err = common.Unmarshal(body, &video)
	require.NoError(t, err)
	require.NotNil(t, video.Error)
	require.Equal(t, "The request failed because the output audio may contain sensitive information.", video.Error.Message)
	require.Equal(t, "task_failed", video.Error.Code)
}
