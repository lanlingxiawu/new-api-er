package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// option.go: GetOptions sensitive-key masking and UpdateOption bad-input guard.
// (Value validation helpers are covered in option_test.go.)

func TestGetOptions_MasksSensitiveKeys(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	common.OptionMap["MyFeatureToken"] = "secret-token"
	common.OptionMap["MyFeatureSecret"] = "secret-secret"
	common.OptionMap["MyPlainSetting"] = "visible"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, "MyFeatureToken")
		delete(common.OptionMap, "MyFeatureSecret")
		delete(common.OptionMap, "MyPlainSetting")
		common.OptionMapRWMutex.Unlock()
	})

	ctx, rec := newCtx(t, "GET", "/api/option", nil)
	GetOptions(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)

	var options []model.Option
	require.NoError(t, common.Unmarshal(resp.Data, &options))

	keys := make(map[string]string)
	for _, o := range options {
		keys[o.Key] = o.Value
	}
	assert.Equal(t, "visible", keys["MyPlainSetting"])
	_, hasToken := keys["MyFeatureToken"]
	_, hasSecret := keys["MyFeatureSecret"]
	assert.False(t, hasToken, "keys ending in Token must be omitted")
	assert.False(t, hasSecret, "keys ending in Secret must be omitted")
	// Synthetic CompletionRatioMeta key is always appended.
	_, hasMeta := keys["CompletionRatioMeta"]
	assert.True(t, hasMeta)
}

func TestGetOptions_FiltersBySystemSettingsScope(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	common.OptionMap["Notice"] = "notice-value"
	common.OptionMap["SystemName"] = "hidden-value"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, "Notice")
		delete(common.OptionMap, "SystemName")
		common.OptionMapRWMutex.Unlock()
	})

	ctx, rec := newCtx(t, "GET", "/api/option?scope=site.notice", nil)
	GetOptions(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)

	var options []model.Option
	require.NoError(t, common.Unmarshal(resp.Data, &options))
	require.Len(t, options, 1)
	assert.Equal(t, "Notice", options[0].Key)
}

func TestUpdateOption_RejectsKeyOutsideScope(t *testing.T) {
	ctx, rec := newCtx(t, "PUT", "/api/option", map[string]any{
		"scope": "site.notice",
		"key":   "SystemName",
		"value": "not-allowed",
	})
	UpdateOption(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestUpdateOptionGroup_RejectsKeyOutsideScope(t *testing.T) {
	ctx, rec := newCtx(t, "PUT", "/api/option/group", map[string]any{
		"scope":  "system-tuning.database-pool",
		"module": "db_pool_setting",
		"values": map[string]string{"active_limit": "1"},
	})
	UpdateOptionGroup(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestUpdateOption_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, "PUT", "/api/option", "{ broken")
	asRoot(ctx, 1)
	UpdateOption(ctx)
	// UpdateOption returns HTTP 400 for a decode failure.
	assert.Equal(t, 400, rec.Code)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, false, out["success"])
}
