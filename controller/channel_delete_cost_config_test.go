package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test report 2026-09-29 #11: DELETE /api/channel/:id and the batch delete left
// the channel's channel_cost_configs row behind.

func channelDeleteCostConfig(t *testing.T, channelID int, ratio float64) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.ChannelCostConfig{ChannelId: channelID, CostRatio: ratio}).Error)
	t.Cleanup(func() { model.DB.Where("channel_id = ?", channelID).Delete(&model.ChannelCostConfig{}) })
}

func channelDeleteCostConfigCount(t *testing.T, ids ...int) int64 {
	t.Helper()
	var n int64
	require.NoError(t, model.DB.Model(&model.ChannelCostConfig{}).Where("channel_id IN ?", ids).Count(&n).Error)
	return n
}

func TestDeleteChannelHandlerRemovesCostConfig(t *testing.T) {
	ch := mkChannel(t, nil)
	keep := mkChannel(t, nil)
	channelDeleteCostConfig(t, ch.Id, 0.6)
	channelDeleteCostConfig(t, keep.Id, 0.7)

	ctx, rec := newCtx(t, http.MethodDelete, "/api/channel/"+strconv.Itoa(ch.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ch.Id)}}
	asRoot(ctx, 1)
	DeleteChannel(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	assert.EqualValues(t, 0, channelDeleteCostConfigCount(t, ch.Id))
	assert.EqualValues(t, 1, channelDeleteCostConfigCount(t, keep.Id))
}

func TestDeleteChannelBatchHandlerRemovesCostConfigs(t *testing.T) {
	c1 := mkChannel(t, nil)
	c2 := mkChannel(t, nil)
	keep := mkChannel(t, nil)
	channelDeleteCostConfig(t, c1.Id, 0.5)
	channelDeleteCostConfig(t, c2.Id, 1.1)
	channelDeleteCostConfig(t, keep.Id, 0.9)

	ctx, rec := newCtx(t, http.MethodPost, "/api/channel/batch", map[string]any{"ids": []int{c1.Id, c2.Id}})
	asRoot(ctx, 1)
	DeleteChannelBatch(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	assert.EqualValues(t, 0, channelDeleteCostConfigCount(t, c1.Id, c2.Id))
	assert.EqualValues(t, 1, channelDeleteCostConfigCount(t, keep.Id))
}
