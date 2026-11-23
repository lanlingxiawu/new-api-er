package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRetrieveModelMatchesVisibleList(t *testing.T) {
	requireDB(t)
	withSelfUseModeEnabled(t)
	group := uniq("detail")
	channel := mkChannel(t, func(channel *model.Channel) { channel.Group = group; channel.Models = "local-custom-model,gpt-4o" })
	require.NoError(t, channel.AddAbilities(model.DB))
	t.Cleanup(func() { model.DB.Where("channel_id = ?", channel.Id).Delete(&model.Ability{}) })
	for _, modelName := range []string{"local-custom-model", "gpt-4o", "not-visible"} {
		for _, restricted := range []bool{false, true} {
			ctx, recorder := newCtx(t, "GET", "/v1/models/"+modelName, nil)
			common.SetContextKey(ctx, constant.ContextKeyTokenGroup, group)
			common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, restricted)
			common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{"local-custom-model": true})
			ctx.Params = []gin.Param{{Key: "model", Value: modelName}}
			RetrieveModel(ctx, constant.ChannelTypeOpenAI)
			if modelName == "not-visible" || (restricted && modelName == "gpt-4o") {
				require.Contains(t, recorder.Body.String(), "model_not_found")
			} else {
				var detail dto.OpenAIModels
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &detail))
				require.Equal(t, modelName, detail.Id)
				listCtx, listRecorder := newCtx(t, "GET", "/v1/models", nil)
				common.SetContextKey(listCtx, constant.ContextKeyTokenGroup, group)
				ListModels(listCtx, constant.ChannelTypeOpenAI)
				list := decodeListModelsPayload(t, listRecorder)
				require.Contains(t, list.Data, detail)
			}
		}
	}
}

func TestRetrieveModelNamespacedID(t *testing.T) {
	requireDB(t)
	engine := gin.New()
	engine.GET("/v1/models/*model", func(ctx *gin.Context) {
		allowDetailModel(t, ctx, "local/small-model")
		RetrieveModel(ctx, constant.ChannelTypeOpenAI)
	})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest("GET", "/v1/models/local/small-model", nil))
	var detail dto.OpenAIModels
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &detail))
	require.Equal(t, "local/small-model", detail.Id)
}
