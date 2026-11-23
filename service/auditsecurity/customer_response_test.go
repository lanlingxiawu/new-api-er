package auditsecurity

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCustomerResponseProjection(t *testing.T) {
	group := int64(123)
	response := model.SecurityAccountResponse{ID: 10, ResponseGroupID: &group, TargetUserID: 99, Action: "disable_account", Outcome: "failed", ReasonCode: "internal-sensitive-error", Source: "auto_ban", CreatedAt: 12345}
	alert := projectCustomerAlert(model.SecurityAlert{Response: &response, Responses: []model.SecurityAccountResponse{response}})
	data, err := common.Marshal(alert)
	require.NoError(t, err)
	for _, forbidden := range []string{"response_group_id", "target_user_id", "reason_code", "internal-sensitive-error", "auto_ban"} {
		require.NotContains(t, string(data), forbidden)
	}
	require.Equal(t, "unknown", alert.Response.Reason)
}
