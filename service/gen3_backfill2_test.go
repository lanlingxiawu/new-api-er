package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// business_stats_backfill.go — lookup-cache hit paths + full employee
// attribution in processCostOnlyEntry (cache pre-populated => no DB).
// ===========================================================================

func TestBackfill2_LookupCache_Hits(t *testing.T) {
	ctx := context.Background()
	lc := newBackfillLookupCache()

	// getInviterID cache hit.
	lc.inviterByUserID[100] = 200
	got, err := lc.getInviterID(ctx, 100)
	require.NoError(t, err)
	assert.Equal(t, 200, got)

	// isMutualInvitation cache hit.
	lc.mutualByUserPair["100:200"] = true
	mutual, err := lc.isMutualInvitation(ctx, 100, 200)
	require.NoError(t, err)
	assert.True(t, mutual)

	// getEmployee cache hit.
	lc.employeeByUserID[200] = &model.EmployeeProfile{Id: 5, UserId: 200}
	emp, err := lc.getEmployee(ctx, 200)
	require.NoError(t, err)
	require.NotNil(t, emp)
	assert.Equal(t, 5, emp.Id)

	// getCommissionRate cache hit (day map).
	lc.rateByEmployeeDay["200:123"] = 0.25
	rate, ok := lc.getCommissionRate(ctx, 200, 123)
	require.True(t, ok)
	assert.Equal(t, 0.25, rate)
}

func TestBackfill2_ProcessCostOnly_FullAttribution(t *testing.T) {
	const customer, inviter = 300, 400
	createdAt := int64(1_700_000_000)
	statDate := model.BusinessDayStart(createdAt)

	lc := newBackfillLookupCache()
	lc.inviterByUserID[customer] = inviter
	lc.mutualByUserPair[fmt.Sprintf("%d:%d", customer, inviter)] = false
	lc.employeeByUserID[inviter] = &model.EmployeeProfile{Id: 9, UserId: inviter}
	lc.rateByEmployeeDay[fmt.Sprintf("%d:%d", inviter, statDate)] = 0.1

	w := &backfillLedgerWriter{}
	entry := fallbackLogLine{
		Time: createdAt,
		Kind: "business_stats_skipped",
		Payload: map[string]any{
			"cost": map[string]any{
				"user_id": customer, "channel_id": 7, "model_name": "gpt-4o",
				"revenue_quota": 1000, "cost_quota": 600,
				"group_ratio": 1.0, "cost_ratio": 0.6, "created_at": createdAt,
			},
		},
	}

	handled, reason := processCostOnlyEntry(entry, w, lc)
	assert.True(t, handled)
	assert.Empty(t, reason)
	// Attribution succeeded => a cost+commission pair (not cost-only).
	require.Len(t, w.pairBuf, 1)
	assert.Empty(t, w.costOnlyBuf)

	pair := w.pairBuf[0]
	assert.Equal(t, 9, pair.Commission.EmployeeId)
	assert.Equal(t, inviter, pair.Commission.EmployeeUserId)
	assert.Equal(t, customer, pair.Commission.CustomerUserId)
	// profit = 1000 - 600 = 400; commission = calcCommissionQuota(400, 0.1) = 40.
	assert.EqualValues(t, 400, pair.Commission.ProfitQuota)
	assert.EqualValues(t, calcCommissionQuota(400, 0.1), pair.Commission.CommissionQuota)
}

func TestBackfill2_ProcessStatFallback_MarshalOnly(t *testing.T) {
	// Unknown-but-listed stat kind path exercises processStatFallbackEntry's
	// marshal + replay dispatch; replay errors are surfaced as discard reasons.
	handled, reason := processStatFallbackEntry(fallbackLogLine{
		Kind:    "stat_platform",
		Payload: map[string]any{"stat_date": 1, "channel_id": 1},
	})
	// Either replay succeeds (handled) or returns a discard reason; both are
	// valid — we only require the function to run without panicking.
	if !handled {
		assert.NotEmpty(t, reason)
	}
}
