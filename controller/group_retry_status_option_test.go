package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupRetryStatusOptionEndpoints(t *testing.T) {
	requireDB(t)
	require.NoError(t, i18n.Init())
	keys := []string{"group_retry_status_setting.enabled", "group_retry_status_setting.rules"}
	var saved []model.Option
	require.NoError(t, model.DB.Where(map[string]any{"key": keys}).Find(&saved).Error)
	old := operation_setting.GetGroupRetryStatusSetting()
	previousOptions := map[string]string{}
	common.OptionMapRWMutex.RLock()
	for _, key := range keys {
		if value, exists := common.OptionMap[key]; exists {
			previousOptions[key] = value
		}
	}
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		model.DB.Where(map[string]any{"key": keys}).Delete(&model.Option{})
		for _, option := range saved {
			model.DB.Create(&option)
		}
		operation_setting.ReplaceGroupRetryStatusSetting(old)
		common.OptionMapRWMutex.Lock()
		for _, key := range keys {
			delete(common.OptionMap, key)
		}
		for key, value := range previousOptions {
			common.OptionMap[key] = value
		}
		common.OptionMapRWMutex.Unlock()
	})
	c, rec := newCtx(t, http.MethodPut, "/api/option/group", map[string]any{
		"module": "group_retry_status_setting", "values": map[string]string{"enabled": "true", "rules": `{"default":"429"}`},
	})
	asRoot(c, nextTestID())
	UpdateOptionGroup(c)
	require.True(t, decodeResp(t, rec).Success, rec.Body.String())
	allowed, found := operation_setting.GroupRetryStatusAllowed("default", 429)
	assert.True(t, allowed)
	assert.True(t, found)
	for _, value := range []string{`{"default":null}`, `{"default":"600"}`, `[]`} {
		c, rec = newCtx(t, http.MethodPut, "/api/option/", map[string]string{"key": keys[1], "value": value})
		asRoot(c, nextTestID())
		UpdateOption(c)
		response := decodeResp(t, rec)
		assert.False(t, response.Success)
		assert.Equal(t, i18n.T(c, i18n.MsgGroupRetryStatusInvalid), response.Message)
		allowed, _ = operation_setting.GroupRetryStatusAllowed("default", 429)
		assert.True(t, allowed)
		var stored model.Option
		require.NoError(t, model.DB.Where(map[string]any{"key": keys[1]}).First(&stored).Error)
		assert.Equal(t, `{"default":"429"}`, stored.Value)
	}
	c, rec = newCtx(t, http.MethodPut, "/api/option/", map[string]string{"key": keys[0], "value": "false"})
	asRoot(c, nextTestID())
	UpdateOption(c)
	require.True(t, decodeResp(t, rec).Success)
	_, found = operation_setting.GroupRetryStatusAllowed("default", 429)
	assert.False(t, found)
}
