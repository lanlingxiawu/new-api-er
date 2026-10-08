package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyErrorLogOther is what error logs carried before the channel fields
// left other: channel id/name/type at the top level next to the error details.
// Rows like this stay in the table, so every user view must still strip them.
const legacyErrorLogOther = `{"error_type":"openai_error","error_code":"bad_response_status_code","status_code":500,"channel_id":7,"channel_name":"Azure-JP-secret","channel_type":3,"reject_reason":"policy-blocked","admin_info":{"use_channel":["7"]},"audit_info":{"operator":"root"},"big":9007199254740993}`

func otherOf(t *testing.T, log *Log) map[string]interface{} {
	t.Helper()
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	return other
}

func assertNoLegacyChannelFields(t *testing.T, log *Log) {
	t.Helper()
	other := otherOf(t, log)
	for _, key := range []string{"channel_id", "channel_name", "channel_type", "reject_reason", "admin_info", "audit_info"} {
		assert.NotContains(t, other, key)
	}
	assert.NotContains(t, log.Other, "Azure-JP-secret")
	assert.Empty(t, log.ChannelName)
}

// A log owner sees the same projection as upstream: the legacy channel and
// reject-reason keys and the admin/audit scopes go, everything else stays.
func TestGetUserLogs_StripsLegacyChannelFields(t *testing.T) {
	requireLogDB(t)
	uid := nextTestID()
	logCleanupUser(t, uid)
	now := common.GetTimestamp()
	mkLogRow(t, func(l *Log) {
		l.UserId, l.Type, l.CreatedAt, l.Content, l.Other = uid, LogTypeError, now, "boom", legacyErrorLogOther
	})

	logs, _, err := GetUserLogs(uid, LogTypeError, now-10, now+10, "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assertNoLegacyChannelFields(t, logs[0])
	assert.Equal(t, "openai_error", otherOf(t, logs[0])["error_type"], "error details stay")
	// Untouched values are passed through as raw JSON, so integers beyond
	// float64's exact range survive.
	assert.Contains(t, logs[0].Other, "9007199254740993")
}

// The token-key log query (/api/log/token) goes through the same projection.
func TestGetLogByTokenId_StripsLegacyChannelFields(t *testing.T) {
	requireLogDB(t)
	uid := nextTestID()
	tokenID := nextTestID()
	logCleanupUser(t, uid)
	mkLogRow(t, func(l *Log) {
		l.UserId, l.TokenId, l.Type, l.Content, l.Other = uid, tokenID, LogTypeError, "boom", legacyErrorLogOther
	})

	logs, err := GetLogByTokenId(tokenID)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assertNoLegacyChannelFields(t, logs[0])
}

// Empty other stays empty and unparsable other is replaced, as upstream does:
// a broken value must not reach the user verbatim.
func TestFormatUserLogs_EmptyAndInvalidOther(t *testing.T) {
	logs := []*Log{{Other: ""}, {Other: "not-json"}, {Other: `{"request_path":"/v1/chat/completions"}`}}
	formatUserLogs(logs, 0)
	assert.Equal(t, "", logs[0].Other)
	assert.Equal(t, "{}", logs[1].Other)
	assert.Equal(t, `{"request_path":"/v1/chat/completions"}`, logs[2].Other, "nothing to strip, value unchanged")
}

// Employees viewing their customers' logs lose the channel name but keep the
// channel id they locate problems with.
func TestGetEmployeeCustomerLogs_HidesChannelNameKeepsId(t *testing.T) {
	requireLogDB(t)
	employee := mkUser(t, nil)
	customer := mkUser(t, func(u *User) { u.InviterId = employee.Id })
	logCleanupUser(t, customer.Id)
	logCleanupUser(t, employee.Id)
	now := common.GetTimestamp()
	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.Type, l.CreatedAt, l.Content, l.Other = customer.Id, customer.Username, LogTypeError, now, "boom", legacyErrorLogOther
	})

	logs, _, err := GetEmployeeCustomerLogs(EmployeeCustomerLogFilter{EmployeeUserId: employee.Id, CustomerUserId: customer.Id, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, logs, 1)
	other := otherOf(t, logs[0])
	assert.NotContains(t, other, "channel_name")
	assert.NotContains(t, logs[0].Other, "Azure-JP-secret")
	assert.EqualValues(t, 7, other["channel_id"])
}
