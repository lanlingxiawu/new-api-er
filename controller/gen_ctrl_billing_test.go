package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// billing.go: OpenAI-compatible subscription/usage endpoints (user-quota path).

func TestGetSubscription_UserQuotaPath(t *testing.T) {
	requireDB(t)
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)

	prevDisplay := common.DisplayTokenStatEnabled
	common.DisplayTokenStatEnabled = false
	t.Cleanup(func() { common.DisplayTokenStatEnabled = prevDisplay })

	u := mkUser(t, func(u *model.User) {
		u.Quota = int(common.QuotaPerUnit) // remain quota = 1 USD unit
	})

	ctx, rec := newCtx(t, "GET", "/dashboard/billing/subscription", nil)
	asUser(ctx, u.Id)
	GetSubscription(ctx)

	assert.Equal(t, 200, rec.Code)
	var sub OpenAISubscriptionResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &sub))
	assert.Equal(t, "billing_subscription", sub.Object)
	assert.InDelta(t, 1.0, sub.HardLimitUSD, 1e-9)
}

func TestGetUsage_UserQuotaPath(t *testing.T) {
	requireDB(t)
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)

	prevDisplay := common.DisplayTokenStatEnabled
	common.DisplayTokenStatEnabled = false
	t.Cleanup(func() { common.DisplayTokenStatEnabled = prevDisplay })

	u := mkUser(t, func(u *model.User) {
		u.UsedQuota = int(common.QuotaPerUnit) // used = 1 USD unit
	})

	ctx, rec := newCtx(t, "GET", "/dashboard/billing/usage", nil)
	asUser(ctx, u.Id)
	GetUsage(ctx)

	assert.Equal(t, 200, rec.Code)
	var usage OpenAIUsageResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &usage))
	assert.Equal(t, "list", usage.Object)
	// TotalUsage is in 0.01 USD units -> 1 USD * 100.
	assert.InDelta(t, 100.0, usage.TotalUsage, 1e-6)
}
