package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Pure-logic helpers
// ---------------------------------------------------------------------------

func TestIsTruthyQuery(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", " yes ", "Yes"} {
		assert.Truef(t, isTruthyQuery(v), "%q should be truthy", v)
	}
	for _, v := range []string{"", "0", "false", "no", "maybe"} {
		assert.Falsef(t, isTruthyQuery(v), "%q should be falsy", v)
	}
}

// canManageTargetRole: root can manage anyone; others only strictly-lower roles.
func TestCanManageTargetRole(t *testing.T) {
	// root manages everyone including another root
	assert.True(t, canManageTargetRole(common.RoleRootUser, common.RoleRootUser))
	assert.True(t, canManageTargetRole(common.RoleRootUser, common.RoleAdminUser))
	// admin manages common user (strictly lower)
	assert.True(t, canManageTargetRole(common.RoleAdminUser, common.RoleCommonUser))
	// admin cannot manage a peer admin
	assert.False(t, canManageTargetRole(common.RoleAdminUser, common.RoleAdminUser))
	// admin cannot manage root
	assert.False(t, canManageTargetRole(common.RoleAdminUser, common.RoleRootUser))
	// common user cannot manage a peer
	assert.False(t, canManageTargetRole(common.RoleCommonUser, common.RoleCommonUser))
}

// calculateUserPermissions: distinct shape per role tier.
func TestCalculateUserPermissions(t *testing.T) {
	root := calculateUserPermissions(common.RoleRootUser)
	assert.Equal(t, false, root["sidebar_settings"])

	admin := calculateUserPermissions(common.RoleAdminUser)
	assert.Equal(t, true, admin["sidebar_settings"])
	adminMods := admin["sidebar_modules"].(map[string]interface{})
	adminArea := adminMods["admin"].(map[string]interface{})
	assert.Equal(t, false, adminArea["setting"], "admin must not access system settings")

	commonPerms := calculateUserPermissions(1) // RoleCommonUser
	commonMods := commonPerms["sidebar_modules"].(map[string]interface{})
	assert.Equal(t, false, commonMods["admin"], "common user has no admin area")
}

// generateDefaultSidebarConfig: admin gets an admin area without setting; root
// gets setting=true; common user gets no admin area.
func TestGenerateDefaultSidebarConfig(t *testing.T) {
	adminCfg := generateDefaultSidebarConfig(common.RoleAdminUser)
	assert.Contains(t, adminCfg, "\"admin\"")
	assert.Contains(t, adminCfg, "\"setting\":false")

	rootCfg := generateDefaultSidebarConfig(common.RoleRootUser)
	assert.Contains(t, rootCfg, "\"setting\":true")

	commonCfg := generateDefaultSidebarConfig(1)
	assert.NotContains(t, commonCfg, "\"admin\"")
}

// ---------------------------------------------------------------------------
// checkUpdatePassword: empty new pwd is a no-op; wrong original fails; correct
// original permits the change.
// ---------------------------------------------------------------------------

func TestCheckUpdatePassword_EmptyNewPasswordNoop(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	upd, err := checkUpdatePassword("whatever", "", u.Id)
	require.NoError(t, err)
	assert.False(t, upd)
}

func TestCheckUpdatePassword_WrongOriginalFails(t *testing.T) {
	requireDB(t)
	hashed, err := common.Password2Hash("correct-horse")
	require.NoError(t, err)
	u := mkUser(t, func(u *model.User) { u.Password = hashed })

	upd, err := checkUpdatePassword("wrong-password", "new-secret", u.Id)
	assert.Error(t, err)
	assert.False(t, upd)
}

func TestCheckUpdatePassword_CorrectOriginalSucceeds(t *testing.T) {
	requireDB(t)
	hashed, err := common.Password2Hash("correct-horse")
	require.NoError(t, err)
	u := mkUser(t, func(u *model.User) { u.Password = hashed })

	upd, err := checkUpdatePassword("correct-horse", "new-secret", u.Id)
	require.NoError(t, err)
	assert.True(t, upd)
}

// ---------------------------------------------------------------------------
// GetUser: id parse error, same-level permission denial, success + capabilities.
// ---------------------------------------------------------------------------

func TestGetUser_InvalidID(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/abc", nil)
	asAdmin(ctx, 1)
	ctx.Params = gin.Params{{Key: "id", Value: "abc"}}
	GetUser(ctx)
	assert.False(t, decodeResp(t, rec).Success)
}

func TestGetUser_SameLevelDenied(t *testing.T) {
	requireDB(t)
	target := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/"+strconv.Itoa(target.Id), nil)
	// caller is an admin trying to view a peer admin
	asAdmin(ctx, 999002)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(target.Id)}}
	GetUser(ctx)
	assert.False(t, decodeResp(t, rec).Success)
}

func TestGetUser_Success(t *testing.T) {
	requireDB(t)
	target := mkUser(t, func(u *model.User) { u.Role = common.RoleCommonUser })
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/"+strconv.Itoa(target.Id), nil)
	asAdmin(ctx, 999003)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(target.Id)}}
	GetUser(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var got model.User
	require.NoError(t, common.Unmarshal(resp.Data, &got))
	assert.Equal(t, target.Id, got.Id)
	assert.Empty(t, got.Password, "password must never be serialized to clients")
}

// ---------------------------------------------------------------------------
// CreateUser: validation + privilege gating + happy path.
// ---------------------------------------------------------------------------

func TestCreateUser_MissingCredentials(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/user/", map[string]any{"username": "", "password": ""})
	asAdmin(ctx, 1)
	CreateUser(ctx)
	assert.False(t, decodeResp(t, rec).Success)
}

func TestCreateUser_CannotCreateHigherOrEqualRole(t *testing.T) {
	requireDB(t)
	// admin caller trying to create another admin (role >= myRole)
	ctx, rec := newCtx(t, http.MethodPost, "/api/user/", map[string]any{
		"username": uniq("nu"),
		"password": "password123",
		"role":     common.RoleAdminUser,
	})
	asAdmin(ctx, 1)
	CreateUser(ctx)
	assert.False(t, decodeResp(t, rec).Success)
}

func TestCreateUser_Success(t *testing.T) {
	requireDB(t)
	username := uniq("created")
	ctx, rec := newCtx(t, http.MethodPost, "/api/user/", map[string]any{
		"username": username,
		"password": "password123",
		"role":     common.RoleCommonUser,
	})
	asAdmin(ctx, 1)
	CreateUser(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var created model.User
	require.NoError(t, model.DB.Where("username = ?", username).First(&created).Error)
	t.Cleanup(func() { model.DB.Unscoped().Delete(&model.User{}, created.Id) })
	assert.Equal(t, common.RoleCommonUser, created.Role)
	assert.Equal(t, username, created.DisplayName, "display name defaults to username")
}

// DeleteUser: caller cannot delete a user of higher-or-equal role.
func TestDeleteUser_HigherOrEqualRoleDenied(t *testing.T) {
	requireDB(t)
	target := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	ctx, rec := newCtx(t, http.MethodDelete, "/api/user/"+strconv.Itoa(target.Id), nil)
	asAdmin(ctx, 999004) // same level as target
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(target.Id)}}
	DeleteUser(ctx)
	assert.False(t, decodeResp(t, rec).Success)
}
