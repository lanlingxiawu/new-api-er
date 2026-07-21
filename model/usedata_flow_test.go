package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// usedata_flow.go builds the "flow" breakdown from quota_data, dispatching by
// role (self / admin / root) and resolving token + channel display names. All
// queries require use_group <> '' and a created_at window. Tests isolate by a
// unique created_at window + unique user/group so cross-user rows never leak.
// ---------------------------------------------------------------------------

func TestGetFlowQuotaData_Self(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)
	base := usedataUniqTime()
	grp := uniq("fg")
	m1, m2 := uniq("fm1"), uniq("fm2")

	// two models for the same user/token/group; m2 has higher quota
	mkQuotaData(t, func(q *QuotaData) {
		q.UserID, q.TokenID, q.UseGroup, q.ModelName, q.CreatedAt = u.Id, tk.Id, grp, m1, base
		q.Quota, q.Count, q.TokenUsed = 100, 1, 10
	})
	mkQuotaData(t, func(q *QuotaData) {
		q.UserID, q.TokenID, q.UseGroup, q.ModelName, q.CreatedAt = u.Id, tk.Id, grp, m2, base
		q.Quota, q.Count, q.TokenUsed = 300, 1, 30
	})
	// a use_group='' row must be excluded by flowQuotaBaseQuery
	mkQuotaData(t, func(q *QuotaData) {
		q.UserID, q.TokenID, q.UseGroup, q.ModelName, q.CreatedAt = u.Id, tk.Id, "", uniq("fx"), base
		q.Quota = 999
	})

	rows, err := GetFlowQuotaData(base, base, "", u.Id, common.RoleCommonUser)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// ordered by quota DESC -> m2 first
	assert.Equal(t, m2, rows[0].ModelName)
	assert.EqualValues(t, 300, rows[0].Quota)
	assert.Equal(t, m1, rows[1].ModelName)
	// token name resolved for both
	assert.Equal(t, tk.Name, rows[0].TokenName)
	assert.Equal(t, tk.Name, rows[1].TokenName)
}

func TestGetFlowQuotaData_SelfDeletedTokenNameEmpty(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	base := usedataUniqTime()
	grp := uniq("fg")

	// token_id that does not exist -> TokenName stays empty (frontend renders label)
	mkQuotaData(t, func(q *QuotaData) {
		q.UserID, q.TokenID, q.UseGroup, q.ModelName, q.CreatedAt = u.Id, 0x7fffff00, grp, uniq("dm"), base
		q.Quota = 5
	})
	rows, err := GetFlowQuotaData(base, base, "", u.Id, common.RoleCommonUser)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Empty(t, rows[0].TokenName)
}

func TestGetFlowQuotaData_Admin(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ch := mkChannel(t, nil)
	base := usedataUniqTime()
	grp := uniq("ag")
	model := uniq("am")

	mkQuotaData(t, func(q *QuotaData) {
		q.UserID, q.Username, q.ChannelID, q.UseGroup, q.ModelName, q.CreatedAt = u.Id, u.Username, ch.Id, grp, model, base
		q.Quota, q.Count, q.TokenUsed = 200, 2, 20
	})

	// admin without username filter
	rows, err := GetFlowQuotaData(base, base, "", 0, common.RoleAdminUser)
	require.NoError(t, err)
	var mine *FlowQuotaData
	for _, r := range rows {
		if r.Username == u.Username {
			mine = r
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, 200, mine.Quota)
	assert.Equal(t, ch.Name, mine.ChannelName) // channel name resolved from DB

	// admin WITH username filter narrows to that user
	rows, err = GetFlowQuotaData(base, base, u.Username, 0, common.RoleAdminUser)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, u.Username, rows[0].Username)
}

func TestGetFlowQuotaData_AdminUnknownChannelFallback(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	base := usedataUniqTime()
	grp := uniq("ag")

	// channel_id that does not exist -> fallback "channel-<id>"
	mkQuotaData(t, func(q *QuotaData) {
		q.UserID, q.Username, q.ChannelID, q.UseGroup, q.ModelName, q.CreatedAt = u.Id, u.Username, 0x7ffffe00, grp, uniq("am"), base
		q.Quota = 7
	})
	rows, err := GetFlowQuotaData(base, base, u.Username, 0, common.RoleAdminUser)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "channel-2147483136", rows[0].ChannelName) // 0x7ffffe00 = 2147483136
}

func TestGetFlowQuotaData_Root(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)
	ch := mkChannel(t, nil)
	base := usedataUniqTime()
	grp := uniq("rg")
	model := uniq("rm")

	mkQuotaData(t, func(q *QuotaData) {
		q.UserID, q.Username, q.TokenID, q.ChannelID = u.Id, u.Username, tk.Id, ch.Id
		q.UseGroup, q.ModelName, q.CreatedAt, q.NodeName = grp, model, base, "node-x"
		q.Quota, q.Count, q.TokenUsed = 500, 3, 50
	})

	rows, err := GetFlowQuotaData(base, base, u.Username, u.Id, common.RoleRootUser)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 500, rows[0].Quota)
	assert.Equal(t, "node-x", rows[0].NodeName)
	assert.Equal(t, tk.Name, rows[0].TokenName)   // token resolved
	assert.Equal(t, ch.Name, rows[0].ChannelName) // channel resolved
}

// fillFlowTokenNames / fillFlowChannelNames short-circuit on empty id sets.
func TestFillFlowNames_EmptyInputs(t *testing.T) {
	require.NoError(t, fillFlowTokenNames(nil))
	require.NoError(t, fillFlowChannelNames(nil))
	// rows with only zero ids -> nothing to resolve, no error
	rows := []*FlowQuotaData{{TokenID: 0, ChannelID: 0}}
	require.NoError(t, fillFlowTokenNames(rows))
	require.NoError(t, fillFlowChannelNames(rows))
	assert.Empty(t, rows[0].TokenName)
	assert.Empty(t, rows[0].ChannelName)
}
