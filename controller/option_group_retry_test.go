package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An invalid per-group retry map is rejected with a translated message that
// says what is allowed — not a raw Go error such as an unmarshal failure or
// an English "must not exceed 20: vip" shown to Chinese admins.
func TestUpdateOptionRejectsInvalidGroupRetryTimes(t *testing.T) {
	for name, value := range map[string]string{
		"out of range": `{\"vip\":21}`,
		"not json":     `not json`,
		"wrong type":   `{\"vip\":\"x\"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/",
				strings.NewReader(`{"key":"GroupRetryTimes","value":"`+value+`"}`))

			ctx.Set("role", common.RoleRootUser) // the unscoped write path is root-only
			UpdateOption(ctx)

			var payload struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			assert.False(t, payload.Success)
			assert.Equal(t, i18n.T(ctx, i18n.MsgSettingGroupRetryTimesInvalid), payload.Message)
			assert.NotContains(t, payload.Message, "unmarshal")
			assert.NotContains(t, payload.Message, "must not exceed")
		})
	}
}

func TestGroupRetryTimesInvalidMessageIsTranslated(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/", nil)
	message := i18n.T(ctx, i18n.MsgSettingGroupRetryTimesInvalid)
	assert.NotEqual(t, i18n.MsgSettingGroupRetryTimesInvalid, message, "the key must have a translation")
	assert.Contains(t, message, "20")
}
