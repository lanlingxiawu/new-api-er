package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ginParam(key, value string) gin.Param {
	return gin.Param{Key: key, Value: value}
}

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, common.Unmarshal(raw, &payload))
	return payload
}

// 默认关闭时下载一律拒绝（业务错误 HTTP 200 + success:false）。
func TestServePprofProfile_DisabledByDefault(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/system-info/pprof/heap", nil)
	ctx.Params = append(ctx.Params, ginParam("name", "/heap"))
	ServePprofProfile(ctx)

	body := decodeBody(t, rec.Body.Bytes())
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, false, body["success"])
	assert.NotEmpty(t, body["message"])
}

// 状态接口不依赖开关，永远可读。
func TestGetPprofStatus(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/system-info/pprof-status", nil)
	GetPprofStatus(ctx)

	body := decodeBody(t, rec.Body.Bytes())
	require.Equal(t, true, body["success"])
	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, data["enabled"])
	assert.NotEmpty(t, data["profiles"], "应下发可下载 profile 列表")
}

// seconds 边界：缺省 / 合法 / 上限 / 越界 / 非法 / 0 / 负数。
func TestParseProfileSeconds(t *testing.T) {
	cases := []struct {
		query string
		want  int
		ok    bool
	}{
		{"", defaultProfileSeconds, true},
		{"?seconds=1", 1, true},
		{"?seconds=120", 120, true},
		{"?seconds=121", 0, false},
		{"?seconds=abc", 0, false},
		{"?seconds=0", 0, false},
		{"?seconds=-1", 0, false},
	}
	for _, tc := range cases {
		ctx, rec := newCtx(t, http.MethodGet, "/api/system-info/pprof/profile"+tc.query, nil)
		seconds, ok := parseProfileSeconds(ctx)
		assert.Equalf(t, tc.ok, ok, "query=%q", tc.query)
		if tc.ok {
			assert.Equal(t, tc.want, seconds)
			assert.Empty(t, rec.Body.Bytes(), "校验通过时不应写响应")
			continue
		}
		body := decodeBody(t, rec.Body.Bytes())
		assert.Equal(t, false, body["success"])
	}
}

func TestIsKnownProfile(t *testing.T) {
	for _, name := range service.AllProfiles {
		assert.True(t, service.IsKnownProfile(name), name)
	}
	assert.True(t, service.IsKnownProfile("cmdline"))
	assert.True(t, service.IsKnownProfile("symbol"))
	for _, name := range []string{"", "cpu", "HEAP", "../heap", "index"} {
		assert.False(t, service.IsKnownProfile(name), name)
	}
}

func TestServeErrorsDistinct(t *testing.T) {
	assert.NotEqual(t, service.ErrCPUProfileBusy, service.ErrTraceBusy)
}
