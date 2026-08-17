package controller

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestStartEnabledChannelsVeridropDetectionRequiresReadySettings(t *testing.T) {
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })

	setting.Enabled = false
	setting.BaseURL = "https://veridrop.example"
	operation_setting.ReplaceVeridropMonitorSetting(setting)
	ctx, rec := newCtx(t, http.MethodPost, "/api/channel/veridrop/detect_enabled", map[string]any{})
	StartEnabledChannelsVeridropDetection(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.NotEmpty(t, resp.Message)

	setting.Enabled = true
	setting.BaseURL = ""
	operation_setting.ReplaceVeridropMonitorSetting(setting)
	ctx, rec = newCtx(t, http.MethodPost, "/api/channel/veridrop/detect_enabled", map[string]any{})
	StartEnabledChannelsVeridropDetection(ctx)
	resp = decodeResp(t, rec)
	require.False(t, resp.Success)
	require.NotEmpty(t, resp.Message)
}

func TestVeridropDetectRequestPreservesExplicitFalse(t *testing.T) {
	var request veridropDetectRequest
	require.NoError(t, common.UnmarshalJsonStr(`{
        "include_long_context": false,
        "include_long_context_extreme": false
    }`, &request))

	require.NotNil(t, request.IncludeLongContext)
	require.NotNil(t, request.IncludeLongContextExtreme)
	require.False(t, *request.IncludeLongContext)
	require.False(t, *request.IncludeLongContextExtreme)
}

func TestListChannelVeridropDetectionTargets(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	channelID := nextTestID()
	disabledChannelID := nextTestID()
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id IN ?", []int{channelID, disabledChannelID}).Delete(&model.Channel{}).Error)
	})
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Name:    "veridrop-controller-target",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-target",
		Status:  common.ChannelStatusEnabled,
		BaseURL: common.GetPointer("https://target.example/v1"),
		Models:  "gpt-5",
		Group:   "default",
	}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      disabledChannelID,
		Name:    "veridrop-controller-disabled-target",
		Type:    constant.ChannelTypeAnthropic,
		Key:     "sk-disabled-target",
		Status:  common.ChannelStatusManuallyDisabled,
		BaseURL: common.GetPointer("https://disabled-target.example"),
		Models:  "claude-sonnet",
		Group:   "default",
	}).Error)

	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/veridrop/targets", nil)
	ctx.Request.URL.RawQuery = "max_channels=100000"
	ListChannelVeridropDetectionTargets(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)

	var data service.VeridropDetectionTargets
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	require.GreaterOrEqual(t, data.ChannelCount, 1)
	require.GreaterOrEqual(t, data.ModelCount, 1)
	require.Contains(t, data.Items, service.VeridropDetectionTarget{
		ChannelID:       channelID,
		ChannelName:     "veridrop-controller-target",
		ChannelType:     constant.ChannelTypeOpenAI,
		ChannelTypeName: "OpenAI",
		Protocol:        "openai",
		BaseURL:         "https://target.example/v1",
		Models:          []string{"gpt-5"},
		ModelCount:      1,
		Status:          common.ChannelStatusEnabled,
	})
	for _, target := range data.Items {
		require.NotEqual(t, disabledChannelID, target.ChannelID)
	}

	ctx, rec = newCtx(t, http.MethodGet, "/api/channel/veridrop/targets", nil)
	ctx.Request.URL.RawQuery = "max_channels=100000&scope=all"
	ListChannelVeridropDetectionTargets(ctx)
	resp = decodeResp(t, rec)
	require.True(t, resp.Success)
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	require.Contains(t, data.Items, service.VeridropDetectionTarget{
		ChannelID:       disabledChannelID,
		ChannelName:     "veridrop-controller-disabled-target",
		ChannelType:     constant.ChannelTypeAnthropic,
		ChannelTypeName: "Anthropic",
		Protocol:        "anthropic",
		BaseURL:         "https://disabled-target.example",
		Models:          []string{"claude-sonnet"},
		ModelCount:      1,
		Status:          common.ChannelStatusManuallyDisabled,
	})
}

func TestStartChannelsVeridropDetectionRejectsInvalidChannelIDs(t *testing.T) {
	tests := []struct {
		name       string
		channelIDs []int
	}{
		{name: "empty", channelIDs: nil},
		{name: "zero", channelIDs: []int{0}},
		{name: "negative", channelIDs: []int{-1}},
		{name: "duplicate", channelIDs: []int{17, 17}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodPost, "/api/channel/veridrop/detect_batch", map[string]any{
				"channel_ids": test.channelIDs,
			})
			StartChannelsVeridropDetection(ctx)
			resp := decodeResp(t, rec)
			require.False(t, resp.Success)
			require.NotEmpty(t, resp.Message)
		})
	}
}

func TestStartManualChannelVeridropDetectionRejectsMissingFields(t *testing.T) {
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })
	setting.Enabled = true
	setting.BaseURL = "https://veridrop.example"
	operation_setting.ReplaceVeridropMonitorSetting(setting)

	ctx, rec := newCtx(t, http.MethodPost, "/api/channel/veridrop/detect_manual", map[string]any{
		"protocol": "openai",
	})
	StartManualChannelVeridropDetection(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.NotEmpty(t, resp.Message)
}

func TestStartChannelVeridropDetectionCleanupRejectsInvalidRetention(t *testing.T) {
	for _, retentionDays := range []int{-1, 3651} {
		ctx, rec := newCtx(t, http.MethodPost, "/api/channel/veridrop/results/cleanup", map[string]any{
			"retention_days": retentionDays,
		})
		StartChannelVeridropDetectionCleanup(ctx)
		resp := decodeResp(t, rec)
		require.False(t, resp.Success)
		require.NotEmpty(t, resp.Message)
	}
}

func TestListChannelVeridropDetectionResultsRejectsInvalidBatch(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/veridrop/results", nil)
	ctx.Request.URL.RawQuery = "batch=unknown"
	ListChannelVeridropDetectionResults(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.NotEmpty(t, resp.Message)
}

func TestListChannelVeridropDetectionResultsRejectsInvalidFilters(t *testing.T) {
	tests := []string{
		"channel_id=abc",
		"channel_id=-1",
		"limit=abc",
		"limit=101",
		"before_id=-1",
		"updated_after=abc",
		"min_score=-1",
		"max_score=101",
		"min_score=80&max_score=20",
		"error_only=yes",
		"outcome=unknown",
		"outcomes=unknown",
		"outcomes=passed,,failed",
		"outcomes=passed,passed",
		"outcome=passed&outcomes=failed",
		"status=unknown",
		"protocol=unknown",
		"mode=unknown",
		"sort_by=unknown",
		"sort_order=sideways",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodGet, "/api/channel/veridrop/results", nil)
			ctx.Request.URL.RawQuery = query
			ListChannelVeridropDetectionResults(ctx)
			resp := decodeResp(t, rec)
			require.False(t, resp.Success)
			require.NotEmpty(t, resp.Message)
		})
	}
}

func TestListChannelVeridropDetectionResultsReturnsStatistics(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelVeridropDetection{}))
	channelID := 990000000 + int(time.Now().UnixNano()%1000000)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id = ?", channelID).Delete(&model.ChannelVeridropDetection{}).Error)
	})
	require.NoError(t, model.DB.Create([]*model.ChannelVeridropDetection{
		{ChannelID: channelID, ChannelName: "statistics-controller", Model: "passed", Status: model.ChannelVeridropDetectionDone, Verdict: "passed", Score: 90},
		{ChannelID: channelID, ChannelName: "statistics-controller", Model: "failed", Status: model.ChannelVeridropDetectionError},
	}).Error)

	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/veridrop/results", nil)
	ctx.Request.URL.RawQuery = "channel_id=" + fmt.Sprint(channelID) + "&outcomes=passed,failed"
	ListChannelVeridropDetectionResults(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)

	var data struct {
		Items         []*model.ChannelVeridropDetection     `json:"items"`
		MatchingCount int64                                 `json:"matching_count"`
		Summary       model.ChannelVeridropDetectionSummary `json:"summary"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	require.Len(t, data.Items, 2)
	require.EqualValues(t, 2, data.MatchingCount)
	require.EqualValues(t, 2, data.Summary.Total)
	require.EqualValues(t, 1, data.Summary.Passed)
	require.EqualValues(t, 1, data.Summary.Failed)
}

func TestListChannelVeridropDetectionTargetsRejectsInvalidLimit(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/veridrop/targets", nil)
	ctx.Request.URL.RawQuery = "max_channels=invalid"
	ListChannelVeridropDetectionTargets(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.NotEmpty(t, resp.Message)
}
