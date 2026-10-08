package controller

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func costRatioCtx(t *testing.T, channelId int, body any) (*gin.Context, func() apiResp) {
	t.Helper()
	ctx, rec := newCtx(t, "PUT", "/api/price_monitor/channels/"+strconv.Itoa(channelId)+"/cost_ratio", body)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channelId)}}
	asAdmin(ctx, 1)
	return ctx, func() apiResp { return decodeResp(t, rec) }
}

func cleanupCostConfig(t *testing.T, channelId int) {
	t.Cleanup(func() { _ = model.DeleteChannelCostConfig(channelId) })
}

func TestUpdatePriceMonitorChannelCostRatio(t *testing.T) {
	requireDB(t)

	t.Run("writes the cost ratio and keeps the existing remark", func(t *testing.T) {
		channel := mkChannel(t, nil)
		cleanupCostConfig(t, channel.Id)
		require.NoError(t, model.UpsertChannelCostConfig(channel.Id, 1, "contract A"))

		ctx, resp := costRatioCtx(t, channel.Id, map[string]any{"cost_ratio": 0.8})
		UpdatePriceMonitorChannelCostRatio(ctx)
		out := resp()
		require.True(t, out.Success, out.Message)
		assert.Equal(t, 0.8, model.GetChannelCostRatio(channel.Id), "the cost cache is invalidated by the shared write path")
		configs, err := model.GetAllChannelCostConfigs()
		require.NoError(t, err)
		for _, config := range configs {
			if config.ChannelId == channel.Id {
				assert.Equal(t, "contract A", config.Remark)
			}
		}
	})

	t.Run("zero is a valid cost ratio (a free channel)", func(t *testing.T) {
		channel := mkChannel(t, nil)
		cleanupCostConfig(t, channel.Id)
		ctx, resp := costRatioCtx(t, channel.Id, map[string]any{"cost_ratio": 0})
		UpdatePriceMonitorChannelCostRatio(ctx)
		require.True(t, resp().Success)
		assert.Equal(t, 0.0, model.GetChannelCostRatio(channel.Id))
	})

	rejected := map[string]any{
		"missing value": map[string]any{},
		"negative":      map[string]any{"cost_ratio": -0.1},
		"above the max": map[string]any{"cost_ratio": maxPriceMonitorCostRatio + 0.01},
		"not a number":  map[string]any{"cost_ratio": "0.8"},
	}
	for name, body := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			channel := mkChannel(t, nil)
			cleanupCostConfig(t, channel.Id)
			ctx, resp := costRatioCtx(t, channel.Id, body)
			UpdatePriceMonitorChannelCostRatio(ctx)
			out := resp()
			assert.False(t, out.Success)
			assert.NotEmpty(t, out.Message)
			assert.Equal(t, 1.0, model.GetChannelCostRatio(channel.Id), "nothing written")
		})
	}

	t.Run("rejects a disabled channel", func(t *testing.T) {
		channel := mkChannel(t, func(ch *model.Channel) { ch.Status = common.ChannelStatusManuallyDisabled })
		cleanupCostConfig(t, channel.Id)
		ctx, resp := costRatioCtx(t, channel.Id, map[string]any{"cost_ratio": 0.5})
		UpdatePriceMonitorChannelCostRatio(ctx)
		assert.False(t, resp().Success)
		assert.Equal(t, 1.0, model.GetChannelCostRatio(channel.Id))
	})

	t.Run("rejects a missing channel", func(t *testing.T) {
		ctx, resp := costRatioCtx(t, 987654321, map[string]any{"cost_ratio": 0.5})
		UpdatePriceMonitorChannelCostRatio(ctx)
		assert.False(t, resp().Success)
	})
}
