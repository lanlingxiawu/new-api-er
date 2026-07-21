package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ratio_config.go: exposure gating for the public ratio-config endpoint.

func TestGetRatioConfig_DisabledForbidden(t *testing.T) {
	prev := ratio_setting.IsExposeRatioEnabled()
	t.Cleanup(func() { ratio_setting.SetExposeRatioEnabled(prev) })

	ratio_setting.SetExposeRatioEnabled(false)
	ctx, rec := newCtx(t, "GET", "/api/ratio_config", nil)
	GetRatioConfig(ctx)
	assert.Equal(t, 403, rec.Code)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, false, out["success"])
}

func TestGetRatioConfig_EnabledSucceeds(t *testing.T) {
	prev := ratio_setting.IsExposeRatioEnabled()
	t.Cleanup(func() { ratio_setting.SetExposeRatioEnabled(prev) })

	ratio_setting.SetExposeRatioEnabled(true)
	ctx, rec := newCtx(t, "GET", "/api/ratio_config", nil)
	GetRatioConfig(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
	assert.NotNil(t, resp.Data)
}
