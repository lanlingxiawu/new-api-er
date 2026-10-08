package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
)

// An invalid cache cap is refused with a message that says what is accepted,
// before anything is stored (branch audit L17).
func TestUpdateOptionRejectsInvalidUserGroupRatioCacheMax(t *testing.T) {
	before := ratio_setting.GetUserGroupRatioCacheMax()
	for _, value := range []any{"-5", "abc", 1.5, -1} {
		ctx, rec := newCtx(t, http.MethodPut, "/api/option/", map[string]any{
			"key": "UserExclusiveGroupRatioCacheMax", "value": value,
		})
		asRoot(ctx, nextTestID())
		UpdateOption(ctx)
		resp := decodeResp(t, rec)
		assert.False(t, resp.Success, "%v", value)
		assert.Equal(t, i18n.T(ctx, i18n.MsgSettingUserGroupRatioCacheMaxInvalid), resp.Message)
	}
	assert.Equal(t, before, ratio_setting.GetUserGroupRatioCacheMax())
}
