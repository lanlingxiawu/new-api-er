package controller

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAuditTokenConfigurationValuesUseProjectedChanges(t *testing.T) {
	if model.DB == nil || model.LOG_DB == nil {
		t.Skip("databases not configured")
	}
	userID := int(time.Now().UnixNano()%1000000000) + 1000000000
	token := seedToken(t, model.DB, userID, "audit-values", fmt.Sprintf("audit-secret-%d", time.Now().UnixNano()))
	t.Cleanup(func() { model.DB.Where("id = ?", token.Id).Delete(&model.Token{}) })
	c, w := newAuthenticatedContext(t, "PUT", "/api/token/", map[string]any{
		"id": token.Id, "name": "renamed", "expired_time": -1, "remain_quota": 0, "unlimited_quota": false,
		"model_limits_enabled": true, "model_limits": "configured-model", "allow_ips": "203.0.113.57", "group": "default",
	}, userID)
	UpdateToken(c)
	require.True(t, decodeAPIResponse(t, w).Success)
	changes := model.RequestAuditChanges(c)
	require.Len(t, changes.Targets, 1)
	fields := map[string]model.AuditFieldChange{}
	for _, field := range changes.Targets[0].Fields {
		fields[field.Path[0]] = field
	}
	require.Equal(t, `100`, string(fields["remain_quota"].Old.Value))
	require.Equal(t, `0`, string(fields["remain_quota"].New.Value))
	require.Equal(t, `"configured-model"`, string(fields["model_limits"].New.Value))
	require.Equal(t, `"203.0.113.57"`, string(fields["allow_ips"].New.Value))
	params, err := common.Marshal(tokenAuditParams(c))
	require.NoError(t, err)
	require.NotContains(t, string(params), "configured-model")
	require.NotContains(t, string(params), "203.0.113.57")
	event := &model.AuditLog{UserId: userID, Other: model.AuditOther{Changes: changes}}
	model.ProjectAuditLogs([]*model.AuditLog{event}, true, common.RoleCommonUser)
	require.NotNil(t, event.Other.Changes)
	encoded, err := common.Marshal(event)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), token.Key)
}
