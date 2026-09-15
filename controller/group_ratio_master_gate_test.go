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

// 分组倍率与用户专属倍率只允许在主节点修改。
// 设计见 docs/design/user-exclusive-group-ratio-deletion.md。

func asSlaveNode(t *testing.T) {
	t.Helper()
	previous := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = previous })
}

// 从节点的内存分组表可能落后于主节点；在这里保存会把已存在的分组误判为「新增」并清掉
// 用户专属倍率，所以必须拒绝，且不能写库、不能改内存。
func TestUpdateOptionGroupRatioRequiresMasterNode(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1})
	asSlaveNode(t)

	var before model.Option
	hadRow := model.DB.Where(&model.Option{Key: "GroupRatio"}).First(&before).Error == nil

	ctx, rec := newCtx(t, http.MethodPut, "/api/option/", map[string]any{
		"key": "GroupRatio", "value": `{"default":2}`,
	})
	asAdmin(ctx, nextTestID())
	UpdateOption(ctx)

	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.Message)
	assert.InDelta(t, 1, ratio_setting.GetGroupRatioCopy()["default"], 1e-9, "memory must be untouched")
	var after model.Option
	if hadRow {
		require.NoError(t, model.DB.Where(&model.Option{Key: "GroupRatio"}).First(&after).Error)
		assert.Equal(t, before.Value, after.Value, "the stored value must be untouched")
	} else {
		assert.Error(t, model.DB.Where(&model.Option{Key: "GroupRatio"}).First(&after).Error)
	}
}

func TestUpdateUserGroupRatiosRequireMasterNode(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	asSlaveNode(t)
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":2}` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"vip":3}`})
	assert.False(t, resp.Success)
	assert.JSONEq(t, `{"vip":2}`, storedGroupRatios(t, user.Id))

	// 表单把原值（格式不同）原样回传不算修改，不能挡住与倍率无关的编辑。
	resp = updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{ "vip": 2 }`, "remark": "unrelated edit"})
	assert.True(t, resp.Success, resp.Message)

	// 不带 group_ratios 的编辑同样放行。
	resp = updateUserWithGroupRatios(t, user, map[string]any{"remark": "another unrelated edit"})
	assert.True(t, resp.Success, resp.Message)
}

// 从节点的分组表可能落后：主节点刚新建的分组在这里还不存在。原样回传的专属倍率必须保持原样，
// 不能按本节点的旧分组表「自愈」删掉。
func TestUpdateUserOnSlaveKeepsRatiosForGroupsUnknownLocally(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1})
	asSlaveNode(t)
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"brand-new":0.5}` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"brand-new":0.5}`, "remark": "unrelated edit"})
	assert.True(t, resp.Success, resp.Message)
	assert.JSONEq(t, `{"brand-new":0.5}`, storedGroupRatios(t, user.Id))
}

// 分组刚被删除或重建、历史专属倍率还在清理时，不接受为它设置新规则（扫描可能随后把新规则当残留删掉）；
// 与清理中的分组无关的改动照常保存。
func TestUpdateUserRejectsExclusiveRatioWhileGroupCleanupRuns(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	previous := activeGroupRatioCleanups
	activeGroupRatioCleanups = func(groups []string) []string {
		var busy []string
		for _, name := range groups {
			if name == "vip" {
				busy = append(busy, name)
			}
		}
		return busy
	}
	t.Cleanup(func() { activeGroupRatioCleanups = previous })
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"default":0.9}` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"default":0.9,"vip":1.5}`})
	assert.False(t, resp.Success)
	assert.NotContains(t, resp.Message, "group_ratio_cleanup_in_progress", "the message must be translated")
	assert.JSONEq(t, `{"default":0.9}`, storedGroupRatios(t, user.Id))

	resp = updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"default":0.8}`})
	assert.True(t, resp.Success, resp.Message)
	assert.JSONEq(t, `{"default":0.8}`, storedGroupRatios(t, user.Id))
}

// 只有新增或改了值的条目才需要与进行中的清理协调；原样回传的旧条目可能正是清理要删的残留。
func TestChangedGroupRatioNames(t *testing.T) {
	assert.Equal(t, []string{"added", "changed"}, changedGroupRatioNames(
		map[string]float64{"kept": 1, "changed": 2, "removed": 3},
		map[string]float64{"kept": 1, "changed": 2.5, "added": 1},
	))
	assert.Empty(t, changedGroupRatioNames(nil, nil))
	assert.Empty(t, changedGroupRatioNames(map[string]float64{"vip": 2}, map[string]float64{"vip": 2}))
	assert.Equal(t, []string{"free"}, changedGroupRatioNames(nil, map[string]float64{"free": 0}),
		"an explicit zero is a real rule, not an absent one")
}
