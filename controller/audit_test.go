package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// fillAuditTargetUser — 被操作用户注入（审计的核心信息）
// ---------------------------------------------------------------------------

func TestFillAuditTargetUser_WritesBothKeys(t *testing.T) {
	params := map[string]interface{}{}
	fillAuditTargetUser(params, 42, "alice")
	assert.Equal(t, 42, params["target_user_id"])
	assert.Equal(t, "alice", params["target_username"])
}

// 操作者对自己动手同样必须留痕——这正是审计上最需要记录的场景。
// 旧实现用 targetUserId != operatorUserId 把它吞掉了。
func TestFillAuditTargetUser_RecordsSelfOperation(t *testing.T) {
	params := map[string]interface{}{}
	fillAuditTargetUser(params, 7, "root")
	assert.Equal(t, 7, params["target_user_id"])
	assert.Equal(t, "root", params["target_username"])
}

func TestFillAuditTargetUser_NonPositiveIDWritesNothing(t *testing.T) {
	for _, id := range []int{0, -1} {
		params := map[string]interface{}{"quota": "$5"}
		fillAuditTargetUser(params, id, "alice")
		assert.NotContains(t, params, "target_user_id")
		assert.NotContains(t, params, "target_username")
		assert.Equal(t, "$5", params["quota"], "既有参数不应被改动")
	}
}

func TestFillAuditTargetUser_DoesNotOverrideExplicitValues(t *testing.T) {
	params := map[string]interface{}{
		"target_user_id":  99,
		"target_username": "explicit",
	}
	fillAuditTargetUser(params, 42, "alice")
	assert.Equal(t, 99, params["target_user_id"])
	assert.Equal(t, "explicit", params["target_username"])
}

// 调用方只给了 ID（例如中间件兜底路径的转写），用户名从库里回查补齐。
func TestFillAuditTargetUser_LooksUpMissingUsername(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)

	params := map[string]interface{}{}
	fillAuditTargetUser(params, u.Id, "")
	assert.Equal(t, u.Id, params["target_user_id"])
	assert.Equal(t, u.Username, params["target_username"])
}

// 用户不存在时只记 ID，不阻断日志写入。
func TestFillAuditTargetUser_UnknownUserKeepsID(t *testing.T) {
	requireDB(t)

	params := map[string]interface{}{}
	fillAuditTargetUser(params, testIDBase+999_000, "")
	assert.Equal(t, testIDBase+999_000, params["target_user_id"])
	assert.NotContains(t, params, "target_username")
}

// ---------------------------------------------------------------------------
// recordManageAudit vs recordManageAuditFor — 资源类操作不得带上被操作用户
// ---------------------------------------------------------------------------

// 渠道/系统设置等资源类操作的对象不是用户。recordManageAudit 曾把操作者自己
// 当作 targetUserId 传进去，依赖「target == operator 则跳过」来避免误注入；
// 该条件移除后必须由调用链本身保证不注入。
func TestRecordManageAudit_ResourceOpHasNoTargetUser(t *testing.T) {
	requireDB(t)
	requireLogDB(t)

	admin := mkUser(t, nil)
	ctx, _ := newCtx(t, "POST", "/api/channel/", nil)
	asAdmin(ctx, admin.Id)

	recordManageAudit(ctx, "channel.delete_batch", map[string]interface{}{"count": 3})

	log := latestManageLog(t, admin.Id)
	require.NotNil(t, log)
	params := auditLogOpParams(t, log)
	assert.Equal(t, "channel.delete_batch", auditLogAction(t, log))
	assert.NotContains(t, params, "target_user_id")
	assert.NotContains(t, params, "target_username")
}

func TestRecordManageAuditForUser_WritesTargetUser(t *testing.T) {
	requireDB(t)
	requireLogDB(t)

	admin := mkUser(t, nil)
	target := mkUser(t, nil)
	ctx, _ := newCtx(t, "POST", "/api/user/manage", nil)
	asAdmin(ctx, admin.Id)

	recordManageAuditForUser(ctx, target.Id, target.Username, "user.quota_add", map[string]interface{}{
		"quota": "$5.00",
	})

	log := latestManageLog(t, admin.Id)
	require.NotNil(t, log)
	// 日志仍归属操作者，被操作用户放在 op.params 里。
	assert.Equal(t, admin.Id, log.UserId)
	params := auditLogOpParams(t, log)
	assert.Equal(t, float64(target.Id), params["target_user_id"])
	assert.Equal(t, target.Username, params["target_username"])
	assert.Contains(t, log.Content, target.Username)
}

// ---------------------------------------------------------------------------
// auditContentEN — 英文兜底文案（导出消费）
// ---------------------------------------------------------------------------

func TestAuditContentEN(t *testing.T) {
	cases := []struct {
		name   string
		action string
		params map[string]interface{}
		want   string
	}{
		{
			name:   "quota add names the target user",
			action: "user.quota_add",
			params: map[string]interface{}{
				"quota": "$5.00", "target_username": "alice", "target_user_id": 42,
			},
			want: "Increased quota of user alice (ID: 42) by $5.00",
		},
		{
			name:   "2fa disable names the target user",
			action: "user.2fa_disable",
			params: map[string]interface{}{"target_username": "bob", "target_user_id": 7},
			want:   "Force-disabled two-factor authentication for user bob (ID: 7)",
		},
		{
			// 用户名回查失败时不能留下 "user  (ID: 42)" 这种断句。
			name:   "missing username collapses whitespace",
			action: "user.delete",
			params: map[string]interface{}{"target_user_id": 42},
			want:   "Deleted user (ID: 42)",
		},
		{
			name:   "unregistered action falls back to the action itself",
			action: "something.unknown",
			params: nil,
			want:   "something.unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, auditContentEN(tc.action, tc.params))
		})
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// latestManageLog returns the newest operation audit row owned by the operator.
// Management audits are written to the independent audit_logs table.
func latestManageLog(t *testing.T, operatorID int) *model.AuditLog {
	t.Helper()
	var log model.AuditLog
	err := model.LOG_DB.Where("user_id = ? AND category = ?", operatorID, model.AuditCategoryOperation).
		Order("id desc").First(&log).Error
	if err != nil {
		t.Fatalf("no manage audit recorded for operator %d: %v", operatorID, err)
	}
	t.Cleanup(func() { model.LOG_DB.Where("user_id = ?", operatorID).Delete(&model.AuditLog{}) })
	return &log
}

func auditLogAction(t *testing.T, log *model.AuditLog) string {
	t.Helper()
	require.NotNil(t, log.Other.Op, "audit log must carry an op descriptor")
	return log.Other.Op.Action
}

// auditLogOpParams decodes op.params (stored as raw JSON values) into plain values.
func auditLogOpParams(t *testing.T, log *model.AuditLog) map[string]any {
	t.Helper()
	require.NotNil(t, log.Other.Op, "audit log must carry an op descriptor")
	params := map[string]any{}
	if len(log.Other.Op.Params) == 0 {
		return params
	}
	encoded, err := common.Marshal(log.Other.Op.Params)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(encoded, &params))
	return params
}
