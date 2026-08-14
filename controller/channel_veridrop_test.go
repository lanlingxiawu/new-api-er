package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestStartEnabledChannelsVeridropDetectionRequiresReadySettings(t *testing.T) {
	setting := operation_setting.GetVeridropMonitorSetting()
	original := *setting
	t.Cleanup(func() { *setting = original })

	setting.Enabled = false
	setting.BaseURL = "https://veridrop.example"
	ctx, rec := newCtx(t, http.MethodPost, "/api/channel/veridrop/detect_enabled", map[string]any{})
	StartEnabledChannelsVeridropDetection(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.NotEmpty(t, resp.Message)

	setting.Enabled = true
	setting.BaseURL = ""
	ctx, rec = newCtx(t, http.MethodPost, "/api/channel/veridrop/detect_enabled", map[string]any{})
	StartEnabledChannelsVeridropDetection(ctx)
	resp = decodeResp(t, rec)
	require.False(t, resp.Success)
	require.NotEmpty(t, resp.Message)
}

func TestListChannelVeridropDetectionTargets(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	channelID := nextTestID()
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id = ?", channelID).Delete(&model.Channel{}).Error)
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

	ctx, rec := newCtx(t, http.MethodGet, "/api/channel/veridrop/targets", nil)
	ctx.Request.URL.RawQuery = "max_channels=1"
	ListChannelVeridropDetectionTargets(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)

	var data service.VeridropDetectionTargets
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	require.Equal(t, 1, data.ChannelCount)
	require.Equal(t, 1, data.ModelCount)
	require.NotEmpty(t, data.Items)
}

func TestStartManualChannelVeridropDetectionRejectsMissingFields(t *testing.T) {
	setting := operation_setting.GetVeridropMonitorSetting()
	original := *setting
	t.Cleanup(func() { *setting = original })
	setting.Enabled = true
	setting.BaseURL = "https://veridrop.example"

	ctx, rec := newCtx(t, http.MethodPost, "/api/channel/veridrop/detect_manual", map[string]any{
		"protocol": "openai",
	})
	StartManualChannelVeridropDetection(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.NotEmpty(t, resp.Message)
}
