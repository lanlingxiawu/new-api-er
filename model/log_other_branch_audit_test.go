package model

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// main projects metadata by role before returning it. Exercise the complete
// visibility matrix, including legacy rows and numbers that float64 loses.
func TestBranchAuditLogOtherVisibilityMatrix(t *testing.T) {
	input := `{"public":9007199254740993,"channel_id":7,"channel_name":"private","channel_type":3,"reject_reason":"legacy","admin_info":{"operator":9},"root_info":{"raw":"secret"},"audit_info":{"route":"private"}}`
	for _, tc := range []struct {
		name        string
		role        logOtherVisibility
		admin, root bool
	}{
		{"user", logOtherVisibilityUser, false, false},
		{"admin", logOtherVisibilityAdmin, true, false},
		{"root", logOtherVisibilityRoot, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLogOtherJSON(input, tc.role)
			var fields map[string]json.RawMessage
			require.NoError(t, common.UnmarshalJsonStr(got, &fields))
			assert.Equal(t, "9007199254740993", string(fields["public"]))
			for _, key := range []string{"admin_info", "audit_info", "channel_id", "channel_name", "channel_type"} {
				_, exists := fields[key]
				assert.Equal(t, tc.admin, exists, key)
			}
			_, root := fields["root_info"]
			assert.Equal(t, tc.root, root)
			assert.NotContains(t, fields, "reject_reason")
			if tc.admin {
				var admin map[string]json.RawMessage
				require.NoError(t, common.Unmarshal(fields["admin_info"], &admin))
				assert.JSONEq(t, `"legacy"`, string(admin["reject_reason"]))
			}
			assert.Equal(t, got, formatLogOtherJSON(got, tc.role), "projection must be idempotent")
			assert.JSONEq(t, formatLogOtherJSON(input, logOtherVisibilityUser), formatLogOtherJSON(got, logOtherVisibilityUser), "privileged projection followed by user projection must not leak")
		})
	}
}

func TestBranchAuditLogOtherInvalidAndLegacyMetadata(t *testing.T) {
	for _, raw := range []string{`{`, `[]`, `42`, `"secret"`, `{"root_info":`} {
		for _, role := range []logOtherVisibility{logOtherVisibilityUser, logOtherVisibilityAdmin} {
			assert.Equal(t, "{}", formatLogOtherJSON(raw, role))
		}
		assert.Equal(t, raw, formatLogOtherJSON(raw, logOtherVisibilityRoot))
	}
	for _, admin := range []string{`null`, `[]`, `42`, `"invalid"`, `{}`} {
		got := formatLogOtherJSON(`{"reject_reason":"old","admin_info":`+admin+`}`, logOtherVisibilityAdmin)
		assert.JSONEq(t, `{"admin_info":{"reject_reason":"old"}}`, got)
	}
	assert.JSONEq(t, `{"admin_info":{"reject_reason":"new"}}`, formatLogOtherJSON(`{"reject_reason":"old","admin_info":{"reject_reason":"new"}}`, logOtherVisibilityAdmin))
	for _, raw := range []string{"", "null", "{}", ` { "amount": 9007199254740993 } `} {
		assert.Equal(t, raw, formatLogOtherJSON(raw, logOtherVisibilityUser), "unchanged JSON should stay byte-identical")
	}
}

func TestBranchAuditLogOtherWriterBoundaries(t *testing.T) {
	other := NewLogOther()
	for _, key := range []string{"", "admin_info", "root_info", "audit_info", "channel_id", "channel_name", "channel_type", "reject_reason"} {
		assert.False(t, other.SetPublic(key, "secret"), key)
	}
	other.MergePublic(map[string]any{"safe": 1, "root_info": "secret"})
	other.MergeAdmin(map[string]any{"operator": 2})
	other.MergeRoot(map[string]any{"diagnostic": 3})
	other.MergeAudit(map[string]any{"route": 4})
	before := other.JSONString()
	snapshot := other.Snapshot()
	snapshot["safe"] = 9
	for _, scope := range []string{"admin_info", "root_info", "audit_info"} {
		snapshot[scope].(map[string]any)["injected"] = true
	}
	assert.Equal(t, before, other.JSONString(), "snapshot top-level and scope maps must be detached")
	encoded, err := common.Marshal(other)
	require.NoError(t, err)
	assert.JSONEq(t, before, string(encoded))
	assert.JSONEq(t, `{"safe":1}`, formatLogOtherJSON(before, logOtherVisibilityUser))
	var absent *LogOther
	assert.False(t, absent.SetPublic("x", 1))
	assert.False(t, absent.SetAdmin("x", 1))
	assert.False(t, absent.SetRoot("x", 1))
	assert.False(t, absent.SetAudit("x", 1))
	assert.Empty(t, absent.JSONString())
	assert.Nil(t, absent.Snapshot())
}
