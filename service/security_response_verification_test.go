package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStopResponseProofBindsExactGroupAndVersion(t *testing.T) {
	a, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeAuditSecurityResponseStop, Context: []byte(`{"alert_id":1,"version":2,"response_group_id":3,"action":"stop_automatic_response"}`)})
	require.NoError(t, err)
	b, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeAuditSecurityResponseStop, Context: []byte(`{"alert_id":1,"version":2,"response_group_id":4,"action":"stop_automatic_response"}`)})
	require.NoError(t, err)
	require.NotEqual(t, a.ContextHash, b.ContextHash)
	for _, raw := range []string{`{"alert_id":1,"version":2,"response_group_id":3}`, `{"alert_id":1,"version":2,"response_group_id":3,"action":"disable_account"}`, `{"alert_id":1,"version":0,"response_group_id":3,"action":"stop_automatic_response"}`} {
		_, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeAuditSecurityResponseStop, Context: []byte(raw)})
		require.Error(t, err)
	}
}
