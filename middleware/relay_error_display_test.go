package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Distributor/auth errors written by middleware name our own groups; on relay
// routes they go through the configured replacement, on dashboard routes not.
func TestAbortWithOpenAiMessage_RelayErrorDisplay(t *testing.T) {
	previous := operation_setting.GetRelayErrorDisplaySetting()
	operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{Enabled: true,
		Rules: `[{"source":"local","keywords":["no available channel"],"action":"replace","message":"当前模型暂不可用"}]`})
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })

	for _, tc := range []struct {
		name  string
		tag   string
		wants string
	}{
		{"relay route", "relay", "当前模型暂不可用"},
		{"dashboard route", "api", "No available channel for model x under group vip (distributor)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Set(RouteTagKey, tc.tag)
			c.Set(common.RequestIdKey, "rid-9")
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "No available channel for model x under group vip (distributor)", types.ErrorCodeModelNotFound)

			var out struct {
				Error struct {
					Message string `json:"message"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
			assert.Equal(t, tc.wants+" (request id: rid-9)", out.Error.Message)
			assert.Equal(t, "model_not_found", out.Error.Code)
			assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		})
	}
}
