package controller

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Branch audit of UpdateUser (fork rewrite): role/level guards, the admin-only
// exclusive-ratio field on master vs slave nodes, cleanup coordination, and the
// Redis user cache refresh that relay depends on (group_ratios changes do not
// bump auth_version, so PublishUserAuthCache is the only thing that moves them).

type auditUpdateResult struct {
	resp    apiResp
	message string
}

func auditUpdateUser(t *testing.T, actorID, actorRole int, body map[string]any, wantMsgKey string) auditUpdateResult {
	t.Helper()
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", body)
	ctx.Set("id", actorID)
	ctx.Set("role", actorRole)
	ctx.Set("username", "audit-operator")
	UpdateUser(ctx)
	out := auditUpdateResult{resp: decodeResp(t, rec)}
	if wantMsgKey != "" {
		out.message = common.TranslateMessage(ctx, wantMsgKey)
	}
	return out
}

func auditUserBody(u *model.User, extra map[string]any) map[string]any {
	body := map[string]any{"id": u.Id, "username": u.Username, "display_name": u.DisplayName, "group": u.Group}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func auditStoredUser(t *testing.T, id int) model.User {
	t.Helper()
	var u model.User
	require.NoError(t, model.DB.First(&u, id).Error)
	return u
}

func TestBranchAuditUpdateUser_LevelGuards(t *testing.T) {
	requireDB(t)
	admin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser; u.DisplayName = "admin-before" })
	otherAdmin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser; u.DisplayName = "peer-before" })
	root := mkUser(t, func(u *model.User) { u.Role = common.RoleRootUser; u.DisplayName = "root-before" })

	cases := []struct {
		name    string
		actor   int
		role    int
		target  *model.User
		wantKey string
	}{
		{"admin edits peer admin", admin.Id, common.RoleAdminUser, otherAdmin, i18n.MsgUserNoPermissionHigherLevel},
		{"admin edits root", admin.Id, common.RoleAdminUser, root, i18n.MsgUserNoPermissionHigherLevel},
		{"admin edits itself", admin.Id, common.RoleAdminUser, admin, i18n.MsgUserNoPermissionHigherLevel},
		{"common user edits admin", nextTestID(), common.RoleCommonUser, admin, i18n.MsgUserNoPermissionHigherLevel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := auditUpdateUser(t, tc.actor, tc.role,
				auditUserBody(tc.target, map[string]any{"display_name": "hijacked", "remark": "hijacked"}), tc.wantKey)
			assert.False(t, res.resp.Success)
			assert.Equal(t, res.message, res.resp.Message)
			stored := auditStoredUser(t, tc.target.Id)
			assert.NotEqual(t, "hijacked", stored.DisplayName)
			assert.NotEqual(t, "hijacked", stored.Remark)
		})
	}

	// Root may edit an administrator (no admin_permissions in the body: authz untouched).
	res := auditUpdateUser(t, root.Id, common.RoleRootUser,
		auditUserBody(otherAdmin, map[string]any{"display_name": "peer-after"}), "")
	require.True(t, res.resp.Success, res.resp.Message)
	assert.Equal(t, "peer-after", auditStoredUser(t, otherAdmin.Id).DisplayName)
}

func TestBranchAuditUpdateUser_RoleCannotBeChangedThroughEdit(t *testing.T) {
	requireDB(t)
	target := mkUser(t, nil)
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser, -1, 5} {
		res := auditUpdateUser(t, nextTestID(), common.RoleRootUser,
			auditUserBody(target, map[string]any{"role": role, "display_name": "escalated"}), i18n.MsgInvalidParams)
		assert.False(t, res.resp.Success, "role %d", role)
		assert.Equal(t, res.message, res.resp.Message)
	}
	stored := auditStoredUser(t, target.Id)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.NotEqual(t, "escalated", stored.DisplayName)

	// Echoing the current role (what the edit form sends) is accepted.
	res := auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"role": common.RoleCommonUser, "display_name": "same-role"}), "")
	require.True(t, res.resp.Success, res.resp.Message)
	assert.Equal(t, common.RoleCommonUser, auditStoredUser(t, target.Id).Role)
}

// A non-root admin smuggling admin_permissions must fail the whole transaction:
// the profile fields written earlier in the same transaction roll back.
func TestBranchAuditUpdateUser_NonRootAdminPermissionsRollBack(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	target := mkUser(t, func(u *model.User) { u.DisplayName = "before"; u.GroupRatios = `{"vip":2}` })

	res := auditUpdateUser(t, nextTestID(), common.RoleAdminUser, auditUserBody(target, map[string]any{
		"display_name":      "after",
		"group_ratios":      `{"vip":0.1}`,
		"admin_permissions": map[string]map[string]bool{"system_settings.site.notice": {"view": true}},
	}), "")

	assert.False(t, res.resp.Success)
	stored := auditStoredUser(t, target.Id)
	assert.Equal(t, "before", stored.DisplayName)
	assert.JSONEq(t, `{"vip":2}`, stored.GroupRatios)
}

func withMasterNode(t *testing.T, master bool) {
	t.Helper()
	prev := common.IsMasterNode
	common.IsMasterNode = master
	t.Cleanup(func() { common.IsMasterNode = prev })
}

// Slave nodes cannot validate against the authoritative group table, so they
// only accept an unchanged round trip — and keep the stored bytes verbatim.
func TestBranchAuditUpdateUser_SlaveNodeGroupRatios(t *testing.T) {
	requireDB(t)
	withMasterNode(t, false)
	target := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":2}` })

	res := auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"group_ratios": `{"vip":0.5}`}), i18n.MsgGroupRatioMasterRequired)
	assert.False(t, res.resp.Success)
	assert.Equal(t, res.message, res.resp.Message)
	assert.Equal(t, `{"vip":2}`, auditStoredUser(t, target.Id).GroupRatios)

	res = auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"group_ratios": `{ "vip" : 2.0 }`, "remark": "slave-edit"}), "")
	require.True(t, res.resp.Success, res.resp.Message)
	stored := auditStoredUser(t, target.Id)
	assert.Equal(t, `{"vip":2}`, stored.GroupRatios, "slave must not rewrite the stored rules")
	assert.Equal(t, "slave-edit", stored.Remark)
}

// While a cleanup for a group is running, a new or changed rule on that group
// is refused; unchanged carried-over rules do not trip the guard.
func TestBranchAuditUpdateUser_CleanupInProgressGuard(t *testing.T) {
	requireDB(t)
	withMasterNode(t, true)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2, "svip": 3})
	prev := activeGroupRatioCleanups
	var asked []string
	activeGroupRatioCleanups = func(groups []string) []string {
		asked = append([]string(nil), groups...)
		var busy []string
		for _, g := range groups {
			if g == "vip" {
				busy = append(busy, g)
			}
		}
		return busy
	}
	t.Cleanup(func() { activeGroupRatioCleanups = prev })
	target := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":2}` })

	res := auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"group_ratios": `{"vip":1.5}`}), "")
	assert.False(t, res.resp.Success)
	assert.Equal(t, []string{"vip"}, asked)
	assert.Equal(t, `{"vip":2}`, auditStoredUser(t, target.Id).GroupRatios)

	res = auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"group_ratios": `{"vip":2,"svip":1}`}), "")
	require.True(t, res.resp.Success, res.resp.Message)
	assert.Equal(t, []string{"svip"}, asked, "only new/changed groups are checked")
	assert.JSONEq(t, `{"vip":2,"svip":1}`, auditStoredUser(t, target.Id).GroupRatios)
}

// Relay reads group_ratios from the Redis user hash. An admin edit must be
// visible there immediately, without clobbering the cached (in-flight) quota.
func TestBranchAuditUpdateUser_GroupRatiosRefreshRedisCacheKeepsQuota(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	withMasterNode(t, true)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	target := mkUser(t, func(u *model.User) { u.Quota = 1000; u.GroupRatios = `{"vip":2}` })
	cacheKey := fmt.Sprintf("user:%d", target.Id)
	rctx := context.Background()
	t.Cleanup(func() {
		_ = common.RDB.Del(rctx, cacheKey,
			fmt.Sprintf("auth:user:fence:%d", target.Id), fmt.Sprintf("auth:user:version:%d", target.Id)).Err()
	})
	_, err := model.GetUserCache(target.Id) // fills the hash
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(rctx, cacheKey, "Quota", 777).Err()) // simulate in-flight deductions

	res := auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"group_ratios": `{"vip":0.5}`}), "")
	require.True(t, res.resp.Success, res.resp.Message)

	cached, err := common.RDB.HGet(rctx, cacheKey, "GroupRatios").Result()
	require.NoError(t, err)
	assert.Equal(t, `{"vip":0.5}`, cached)
	assert.Equal(t, map[string]float64{"vip": 0.5}, model.GetUserGroupRatios(target.Id))
	quota, err := common.RDB.HGet(rctx, cacheKey, "Quota").Int()
	require.NoError(t, err)
	assert.Equal(t, 777, quota, "profile edits must not overwrite the cached quota")
}
