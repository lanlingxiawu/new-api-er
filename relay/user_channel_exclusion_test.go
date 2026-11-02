package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUserChannelExclusionLockedPaths(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupRelayChannelDB(t)
	ch := &model.Channel{Name: "excluded-secret-name", Key: "test-key", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI}
	ch.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: true})
	require.NoError(t, db.Create(ch).Error)
	for _, path := range []string{"token_pin", "origin_task_pin", "websocket_round"} {
		t.Run(path, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			c.Set(appdto.UserChannelExclusionContextKey, &appdto.UserChannelExclusionPolicy{IDs: []int{ch.Id}})
			switch path {
			case "token_pin":
				service.GetChannelConstraints(c).AddPin(appdto.ChannelPin{ChannelId: ch.Id, Source: appdto.PinSourceToken})
				selected, apiErr := selectResponsesWSChannel(c, "model", &service.RetryParam{Ctx: c, TokenGroup: "default", ModelName: "model"})
				require.Nil(t, selected)
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
				require.NotContains(t, apiErr.Error(), "excluded")
				require.NotContains(t, string(apiErr.GetErrorCode()), "excluded")
			case "origin_task_pin":
				service.GetChannelConstraints(c).AddPin(appdto.ChannelPin{ChannelId: ch.Id, Source: appdto.PinSourceOriginTask, RetryMode: appdto.PinRetrySameChannel})
				info := &relaycommon.RelayInfo{}
				err := ApplyChannelPin(c, info)
				require.NotNil(t, err)
				require.Nil(t, info.LockedChannel)
				require.Equal(t, "origin_task_channel_disabled", err.Code)
			case "websocket_round":
				session := responsesWSSession{lockedChannelID: ch.Id, lockedKey: ch.Key}
				err := session.restoreConnectionContext(c, "model")
				require.NotNil(t, err)
				require.Equal(t, http.StatusForbidden, err.StatusCode)
				require.NotContains(t, err.Error(), "excluded")
			}
			require.Equal(t, "user_channel_excluded", service.UserChannelExclusionPolicy(c).FailureReason)
		})
	}
}

func TestUserChannelExclusionAffinityAndPublicError(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupRelayChannelDB(t)
	require.NoError(t, db.AutoMigrate(&model.Ability{}))
	bound := &model.Channel{Name: "bound", Key: "test", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: "default", Models: "exclusion-model", Priority: common.GetPointer(int64(10))}
	fallback := &model.Channel{Name: "fallback", Key: "test", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: "default", Models: "exclusion-model"}
	for _, ch := range []*model.Channel{bound, fallback} {
		ch.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: true})
		require.NoError(t, db.Create(ch).Error)
		require.NoError(t, db.Create(&model.Ability{ChannelId: ch.Id, Group: "default", Model: "exclusion-model", Enabled: true, Priority: ch.Priority}).Error)
	}
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	model.InitChannelCache()
	t.Cleanup(func() {
		db.Where("channel_id IN ?", []int{bound.Id, fallback.Id}).Delete(&model.Ability{})
		db.Where("id IN ?", []int{bound.Id, fallback.Id}).Delete(&model.Channel{})
		model.InitChannelCache()
		common.MemoryCacheEnabled = previousCache
	})
	affinity := operation_setting.GetChannelAffinitySetting()
	previousAffinity := *affinity
	t.Cleanup(func() { *affinity = previousAffinity })
	snapshot, err := model.BuildRequestPolicy(map[string]string{
		"channel_affinity_setting.enabled":      "true",
		"channel_affinity_setting.session_mode": "strict",
		"channel_affinity_setting.rules":        `[{"name":"exclusion-session","model_regex":[".*"],"key_sources":[{"type":"request_header","key":"X-Session"}],"session_mode":"inherit"}]`,
	})
	require.NoError(t, err)
	*affinity = snapshot.Affinity
	newContext := func(ids []int) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		c.Request.Header.Set("X-Session", t.Name())
		c.Set(appdto.UserChannelExclusionContextKey, &appdto.UserChannelExclusionPolicy{IDs: ids})
		return c
	}
	seed := newContext(nil)
	_, found := service.GetPreferredChannelByAffinity(seed, "exclusion-model", "default")
	require.False(t, found)
	seed.Set("channel_id", bound.Id)
	service.RecordChannelAffinity(seed, bound.Id)
	t.Cleanup(func() { service.ClearCurrentChannelAffinityCache(seed) })
	for _, mode := range []string{"strict", "prefer"} {
		affinity.SessionMode = mode
		c := newContext([]int{bound.Id})
		params := &service.RetryParam{Ctx: c, TokenGroup: "default", ModelName: "exclusion-model"}
		ch, apiErr := selectResponsesWSChannel(c, "exclusion-model", params)
		if mode == "strict" {
			require.Nil(t, ch)
			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
			require.False(t, service.ShouldRetryRelayError(c, apiErr, 2))
		} else {
			require.Nil(t, apiErr)
			require.Equal(t, fallback.Id, ch.Id)
			require.Zero(t, params.TotalAttempts())
		}
		id, found := service.GetPreferredChannelByAffinity(seed, "exclusion-model", "default")
		require.True(t, found, "user-specific exclusions must not erase a shared binding")
		require.Equal(t, bound.Id, id)
	}
	allowed := newContext([]int{bound.Id})
	service.UserChannelExclusionPolicy(allowed).FailureReason = "user_channel_excluded"
	service.GetChannelConstraints(allowed).AddPin(appdto.ChannelPin{ChannelId: fallback.Id, Source: appdto.PinSourceToken})
	selected, selectErr := selectResponsesWSChannel(allowed, "exclusion-model", &service.RetryParam{Ctx: allowed, TokenGroup: "default", ModelName: "exclusion-model"})
	require.Nil(t, selectErr)
	require.Equal(t, fallback.Id, selected.Id)
	require.Empty(t, service.UserChannelExclusionPolicy(allowed).FailureReason, "a successful selection clears a previous preselection failure")
	affinity.Enabled = false
	c := newContext([]int{bound.Id, fallback.Id})
	_, excludedErr := selectResponsesWSChannel(c, "exclusion-model", &service.RetryParam{Ctx: c, TokenGroup: "default", ModelName: "exclusion-model"})
	require.NotNil(t, excludedErr)
	// The same group/model without any live candidates must produce exactly the same public error.
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id IN ?", []int{bound.Id, fallback.Id}).Update("enabled", false).Error)
	require.NoError(t, db.Model(&model.Channel{}).Where("id IN ?", []int{bound.Id, fallback.Id}).Update("status", common.ChannelStatusAutoDisabled).Error)
	model.InitChannelCache()
	empty := newContext(nil)
	_, emptyErr := selectResponsesWSChannel(empty, "exclusion-model", &service.RetryParam{Ctx: empty, TokenGroup: "default", ModelName: "exclusion-model"})
	require.NotNil(t, emptyErr)
	require.Equal(t, emptyErr.StatusCode, excludedErr.StatusCode)
	require.Equal(t, emptyErr.GetErrorCode(), excludedErr.GetErrorCode())
	require.Equal(t, emptyErr.Error(), excludedErr.Error())
}
