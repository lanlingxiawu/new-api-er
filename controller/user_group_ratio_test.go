package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withGroupRatio installs a temporary GroupRatio registry and restores the
// original afterwards.
func withGroupRatio(t *testing.T, ratios map[string]float64) {
	t.Helper()
	previous := ratio_setting.GroupRatio2JSONString()
	encoded, err := common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(encoded)))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previous))
	})
}

func updateUserWithGroupRatios(t *testing.T, user *model.User, body map[string]any) apiResp {
	t.Helper()
	payload := map[string]any{"id": user.Id, "username": user.Username, "group": user.Group}
	for k, v := range body {
		payload[k] = v
	}
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", payload)
	asAdmin(ctx, nextTestID())
	UpdateUser(ctx)
	return decodeResp(t, rec)
}

func storedGroupRatios(t *testing.T, userID int) string {
	t.Helper()
	var stored model.User
	require.NoError(t, model.DB.First(&stored, userID).Error)
	return stored.GroupRatios
}

// The reported bug: a group deleted long ago left a stale rule behind, and the
// rule then blocked every later edit of that user.
func TestUpdateUserDropsRulesForDeletedGroups(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "kept": 2})
	user := mkUser(t, func(u *model.User) {
		u.GroupRatios = `{"kept":2,"deleted":3}`
	})

	resp := updateUserWithGroupRatios(t, user, map[string]any{
		"group_ratios": `{"kept":2,"deleted":3}`,
		"remark":       "edited",
	})

	require.True(t, resp.Success, "a stale rule must not block an unrelated edit")
	assert.JSONEq(t, `{"kept":2}`, storedGroupRatios(t, user.Id))

	var data struct {
		RemovedGroupRatios []string `json:"removed_group_ratios"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	assert.Equal(t, []string{"deleted"}, data.RemovedGroupRatios)
}

func TestUpdateUserKeepsRulesForExistingGroups(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2, "free": 1})
	user := mkUser(t, nil)

	resp := updateUserWithGroupRatios(t, user, map[string]any{
		"group_ratios": `{"vip":2.5,"free":0}`,
	})

	require.True(t, resp.Success)
	assert.JSONEq(t, `{"vip":2.5,"free":0}`, storedGroupRatios(t, user.Id),
		"an explicit 0 is a real ratio and must survive")
	assert.Empty(t, resp.Data, "nothing was removed, so no data payload")
}

// An invalid ratio on a group that still exists is a genuine input error and
// must stay an error — only unknown groups are silently dropped.
func TestUpdateUserRejectsNegativeRatioForExistingGroup(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	user := mkUser(t, nil)

	resp := updateUserWithGroupRatios(t, user, map[string]any{
		"group_ratios": `{"vip":-1}`,
	})

	assert.False(t, resp.Success)
	assert.Empty(t, storedGroupRatios(t, user.Id))
}

func TestUpdateUserRejectsInvalidGroupRatiosJSON(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1})
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"default":1}` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{
		"group_ratios": `{"default":`,
	})

	assert.False(t, resp.Success)
	assert.JSONEq(t, `{"default":1}`, storedGroupRatios(t, user.Id),
		"a rejected save must not touch the stored rules")
}

// Omitting the optional field must inherit; without the pointer shadow field an
// API client that leaves it out would wipe every rule.
func TestUpdateUserOmittedGroupRatiosInheritsStoredValue(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":2}` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{"remark": "no ratios in body"})

	require.True(t, resp.Success)
	assert.JSONEq(t, `{"vip":2}`, storedGroupRatios(t, user.Id))
}

func TestUpdateUserExplicitEmptyObjectClearsGroupRatios(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":2}` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": "{}"})

	require.True(t, resp.Success)
	assert.Equal(t, "{}", storedGroupRatios(t, user.Id))
}

func TestUpdateUserDropsAllRulesWhenEveryGroupIsGone(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1})
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"goneA":1,"goneB":2}` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{
		"group_ratios": `{"goneA":1,"goneB":2}`,
	})

	require.True(t, resp.Success)
	assert.Equal(t, "{}", storedGroupRatios(t, user.Id))

	var data struct {
		RemovedGroupRatios []string `json:"removed_group_ratios"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	assert.Equal(t, []string{"goneA", "goneB"}, data.RemovedGroupRatios, "removed names must be sorted")
}
