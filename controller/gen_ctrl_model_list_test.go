package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// model.go: static model-catalog endpoints and RetrieveModel found/not-found paths.

func TestChannelListModels_ReturnsCatalog(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/models", nil)
	ChannelListModels(ctx)
	assert.Equal(t, 200, rec.Code)
	var out struct {
		Success bool               `json:"success"`
		Data    []dto.OpenAIModels `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.True(t, out.Success)
	assert.NotEmpty(t, out.Data, "static openAIModels catalog must be non-empty")
}

func TestDashboardListModels_ReturnsChannelMap(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/models/dashboard", nil)
	DashboardListModels(ctx)
	assert.Equal(t, 200, rec.Code)
	var out struct {
		Success bool             `json:"success"`
		Data    map[int][]string `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.True(t, out.Success)
	assert.NotEmpty(t, out.Data)
}

func TestRetrieveModel_NotFound(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, "GET", "/v1/models/does-not-exist", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, uniq("absent"))
	ctx.Params = []gin.Param{{Key: "model", Value: "does-not-exist-model-xyz"}}
	RetrieveModel(ctx, constant.ChannelTypeOpenAI)
	assert.Equal(t, 200, rec.Code)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	_, hasErr := out["error"]
	assert.True(t, hasErr, "unknown model must return an OpenAI-style error object")
}

func TestRetrieveModel_FoundOpenAI(t *testing.T) {
	// Pick a real model id from the catalog to exercise the success branch.
	require.NotEmpty(t, openAIModels)
	id := openAIModels[0].Id

	ctx, rec := newCtx(t, "GET", "/v1/models/"+id, nil)
	allowDetailModel(t, ctx, id)
	ctx.Params = []gin.Param{{Key: "model", Value: id}}
	RetrieveModel(ctx, constant.ChannelTypeOpenAI)
	assert.Equal(t, 200, rec.Code)
	var m dto.OpenAIModels
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &m))
	assert.Equal(t, id, m.Id)
}

func TestRetrieveModel_FoundAnthropicShape(t *testing.T) {
	require.NotEmpty(t, openAIModels)
	id := openAIModels[0].Id
	ctx, rec := newCtx(t, "GET", "/v1/models/"+id, nil)
	allowDetailModel(t, ctx, id)
	ctx.Params = []gin.Param{{Key: "model", Value: id}}
	RetrieveModel(ctx, constant.ChannelTypeAnthropic)
	assert.Equal(t, 200, rec.Code)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	// Anthropic shape uses "type":"model" and "display_name".
	assert.Equal(t, "model", out["type"])
	assert.Equal(t, id, out["display_name"])
}

func allowDetailModel(t *testing.T, ctx *gin.Context, modelName string) {
	t.Helper()
	requireDB(t)
	withSelfUseModeEnabled(t)
	group := uniq("detail")
	channel := mkChannel(t, func(channel *model.Channel) { channel.Group = group; channel.Models = modelName })
	require.NoError(t, channel.AddAbilities(model.DB))
	t.Cleanup(func() { model.DB.Where("channel_id = ?", channel.Id).Delete(&model.Ability{}) })
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, group)
}
