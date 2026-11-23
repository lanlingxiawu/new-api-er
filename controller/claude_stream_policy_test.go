package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminClaudeStreamValidation(t *testing.T) {
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.CasbinRule{}))
	withGroupRatio(t, map[string]float64{"default": 1})
	user := mkUser(t, func(u *model.User) { u.Setting = `{"language":"en","future_option":7}` })
	for _, value := range []any{false, true, nil} {
		response := updateUserWithGroupRatios(t, user, map[string]any{"claude_stream_validation": value})
		require.True(t, response.Success, response.Message)
		stored, err := model.GetUserById(user.Id, false)
		require.NoError(t, err)
		if value == nil {
			require.Nil(t, stored.GetSetting().ClaudeStreamValidation)
		} else {
			require.Equal(t, value, *stored.GetSetting().ClaudeStreamValidation)
		}
		require.Contains(t, stored.Setting, `"future_option":7`)
		// An old admin form omits the field; this must preserve the policy.
		require.True(t, updateUserWithGroupRatios(t, user, map[string]any{"remark": "changed"}).Success)
		after, err := model.GetUserById(user.Id, false)
		require.NoError(t, err)
		require.Equal(t, stored.GetSetting().ClaudeStreamValidation, after.GetSetting().ClaudeStreamValidation)

		ctx, rec := newCtx(t, http.MethodGet, "/api/user/", nil)
		asAdmin(ctx, nextTestID())
		ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(user.Id)}}
		GetUser(ctx)
		response = decodeResp(t, rec)
		require.True(t, response.Success, response.Message)
		var projected map[string]any
		require.NoError(t, common.Unmarshal(response.Data, &projected))
		require.Contains(t, projected, "claude_stream_validation")
		require.Equal(t, value, projected["claude_stream_validation"])
	}
	for _, invalid := range []any{"false", 0, map[string]any{}} {
		require.False(t, updateUserWithGroupRatios(t, user, map[string]any{"claude_stream_validation": invalid}).Success)
	}
}

func TestSelfCannotChangeClaudeStreamValidation(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(u *model.User) { u.Setting = `{"claude_stream_validation":false}` })
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/self", map[string]any{
		"language": "fr", "claude_stream_validation": true, "setting": `{"claude_stream_validation":true}`,
	})
	ctx.Set("id", user.Id)
	ctx.Set("role", common.RoleCommonUser)
	UpdateSelf(ctx)
	require.True(t, decodeResp(t, rec).Success)
	stored, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	require.Equal(t, "fr", stored.GetSetting().Language)
	require.Equal(t, common.GetPointer(false), stored.GetSetting().ClaudeStreamValidation)
}

func TestAdminProtocolValidationAndLegacyConflict(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1})
	user := mkUser(t, func(u *model.User) { u.Setting = `{"claude_stream_validation":false,"language":"en"}` })
	for _, value := range []any{false, true, nil} {
		response := updateUserWithGroupRatios(t, user, map[string]any{"stream_protocol_validation": value, "claude_stream_validation": false})
		require.True(t, response.Success, response.Message)
		stored, err := model.GetUserById(user.Id, false)
		require.NoError(t, err)
		require.Nil(t, stored.GetSetting().ClaudeStreamValidation)
		if value == nil {
			require.Nil(t, stored.GetSetting().StreamProtocolValidation)
		} else {
			require.Equal(t, value, *stored.GetSetting().StreamProtocolValidation)
			require.False(t, updateUserWithGroupRatios(t, user, map[string]any{"claude_stream_validation": true}).Success)
		}
		require.True(t, updateUserWithGroupRatios(t, user, map[string]any{"remark": "unchanged policy"}).Success)
		after, err := model.GetUserById(user.Id, false)
		require.NoError(t, err)
		require.Equal(t, stored.GetSetting().StreamProtocolValidation, after.GetSetting().StreamProtocolValidation)
	}
}

func TestCreateUserProtocolExplicitInheritOverridesLegacy(t *testing.T) {
	requireDB(t)
	username := uniq("protocol")
	ctx, rec := newCtx(t, http.MethodPost, "/api/user/", map[string]any{
		"username": username, "password": "password123", "role": common.RoleCommonUser,
		"stream_protocol_validation": nil, "claude_stream_validation": false,
	})
	asAdmin(ctx, 1)
	CreateUser(ctx)
	response := decodeResp(t, rec)
	require.True(t, response.Success, response.Message)
	var created model.User
	require.NoError(t, model.DB.Where("username = ?", username).First(&created).Error)
	t.Cleanup(func() { model.DB.Unscoped().Delete(&model.User{}, created.Id) })
	require.Nil(t, created.GetSetting().StreamProtocolValidation)
	require.Nil(t, created.GetSetting().ClaudeStreamValidation)
}
