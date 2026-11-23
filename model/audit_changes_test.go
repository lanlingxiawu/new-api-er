package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestAuditChangesPreserveValuesAndHideCredentials(t *testing.T) {
	changes := NewAuditChanges()
	changes.Add(AuditTargetChange{Resource: "channel", ID: "42", Visibility: "admin"},
		AuditFields{"priority": int64(9007199254740993), "status": 1, "key": "OLD-SECRET", "base_url": "https://user:pass@example.com/credential?api_key=secret", "unknown": "private", "name": nil},
		AuditFields{"priority": int64(0), "status": 0, "key": "NEW-SECRET", "base_url": "https://example.net/new-secret", "unknown": "new-private", "name": "", "auto_ban": false})
	encoded, err := common.Marshal(changes)
	require.NoError(t, err)
	for _, secret := range []string{"OLD-SECRET", "NEW-SECRET", "user:pass", "credential", "api_key", "new-secret", "private"} {
		require.NotContains(t, string(encoded), secret)
	}
	require.Contains(t, string(encoded), `"9007199254740993"`)
	require.Contains(t, string(encoded), `"value_type":"integer"`)
	require.Contains(t, string(encoded), `"value":false`)
	require.Contains(t, string(encoded), `"value":null`)
	require.Contains(t, string(encoded), `"state":"absent"`)
	require.Contains(t, string(encoded), `https://example.com`)
	require.Equal(t, "committed", changes.Outcome)
	var stored AuditOther
	require.NoError(t, stored.Scan(`{"changes":`+string(encoded)+`}`))
	roundTrip, err := stored.Value()
	require.NoError(t, err)
	require.Contains(t, roundTrip, `"9007199254740993"`)
}

func TestAuditChangesCompareBeforeRedactionAndBoundPayload(t *testing.T) {
	changes := NewAuditChanges()
	target := AuditTargetChange{Resource: "channel", ID: "1", Visibility: "admin"}
	changes.Add(target, AuditFields{"key": "same"}, AuditFields{"key": "same"})
	require.Empty(t, changes.Targets)
	require.Equal(t, "unchanged", changes.Outcome)
	changes.Add(target, AuditFields{"key": "first"}, AuditFields{"key": "second"})
	require.Len(t, changes.Targets[0].Fields, 1)
	for i := 0; i < 150; i++ {
		changes.Add(target, nil, AuditFields{"name": strings.Repeat("n", 5000), "models": strings.Repeat("m", 4000)})
	}
	encoded, err := common.Marshal(changes)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), 64*1024)
	require.LessOrEqual(t, len(changes.Targets), 100)
	require.True(t, changes.Truncated)
}

func TestAuditChangeProjectionDoesNotLeakManagementValues(t *testing.T) {
	changes := NewAuditChanges()
	for _, visibility := range []string{"self", "admin", "root"} {
		changes.Add(AuditTargetChange{Resource: "user", ID: "7", OwnerID: 7, Visibility: visibility}, AuditFields{"status": 1}, AuditFields{"status": 2})
	}
	changes.Add(AuditTargetChange{Resource: "user", ID: "8", OwnerID: 8, Visibility: "self"}, AuditFields{"status": 1}, AuditFields{"status": 2})
	for _, tc := range []struct {
		self        bool
		role, count int
	}{{true, common.RoleRootUser, 1}, {false, common.RoleAdminUser, 3}, {false, common.RoleRootUser, 4}} {
		entry := &AuditLog{UserId: 7, Other: AuditOther{Changes: changes}}
		ProjectAuditLogs([]*AuditLog{entry}, tc.self, tc.role)
		require.Len(t, entry.Other.Changes.Targets, tc.count)
	}
	require.Len(t, changes.Targets, 4, "projection must not mutate the original")
}

func TestAuditChangeNestedSettingsDenyUnknownValues(t *testing.T) {
	changes := NewAuditChanges()
	changes.Add(AuditTargetChange{Resource: "channel", ID: "1", Visibility: "admin"}, nil, AuditFields{
		"setting":         json.RawMessage(`{"force_format":true,"account_balance_token":"secret","proxy":"https://user:password@example.com/path","future_field":"secret"}`),
		"header_override": `{"Authorization":"Bearer secret"}`,
	})
	encoded, err := common.Marshal(changes)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret")
	require.NotContains(t, string(encoded), "password")
	require.Contains(t, string(encoded), `"force_format"`)
	require.Contains(t, string(encoded), `"value":true`)
}

func TestAuditChangesRejectCredentialsInMalformedSafeFields(t *testing.T) {
	changes := NewAuditChanges()
	changes.Add(AuditTarget("channel", 1, ""), nil, AuditFields{
		"setting":       `{"force_format":{"api_key":"secret-value"}}`,
		"model_mapping": `{"Authorization":"secret-value"}`,
		"status":        map[string]any{"password": "secret-value"},
	})
	encoded, err := common.Marshal(changes)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-value")
}

func TestAuditChangesBatchTruncationKeepsRecordedTargetFields(t *testing.T) {
	changes := NewAuditChanges()
	changes.Truncated = true
	changes.Add(AuditTarget("channel", 1, ""), AuditFields{"name": "old", "priority": 1}, AuditFields{"name": "new", "priority": 0})
	require.Len(t, changes.Targets[0].Fields, 2)
}
