package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// withPaymentComplianceUnconfirmed forces the compliance gate OFF for the test
// (the shared dev DB may have it confirmed), restoring the prior value after.
func withPaymentComplianceUnconfirmed(t *testing.T) {
	t.Helper()
	ps := operation_setting.GetPaymentSetting()
	prev := ps.ComplianceConfirmed
	ps.ComplianceConfirmed = false
	t.Cleanup(func() { ps.ComplianceConfirmed = prev })
}

// ---------------------------------------------------------------------------
// subscription.go — plan CRUD, user self-service, admin binds/resets.
// Payment-compliance-gated handlers are exercised with the gate confirmed
// (withPaymentComplianceConfirmed) and, separately, refused when unconfirmed.
// ---------------------------------------------------------------------------

func mkPlan(t *testing.T, mut func(p *model.SubscriptionPlan)) *model.SubscriptionPlan {
	t.Helper()
	requireDB(t)
	p := &model.SubscriptionPlan{
		Title:         uniq("plan"),
		PriceAmount:   1,
		Currency:      "USD",
		DurationUnit:  model.SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
	}
	if mut != nil {
		mut(p)
	}
	require.NoError(t, model.DB.Create(p).Error)
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.SubscriptionPlan{}, p.Id)
		}
	})
	return p
}

// ---- User APIs ----

func TestGetSubscriptionPlans_ComplianceOff(t *testing.T) {
	withPaymentComplianceUnconfirmed(t)
	// When compliance is unconfirmed the handler returns an empty list (success).
	ctx, rec := newCtx(t, http.MethodGet, "/api/subscription/plans", nil)
	asUser(ctx, 1)
	GetSubscriptionPlans(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)
}

func TestGetSubscriptionPlans_ComplianceOn(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)
	mkPlan(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/subscription/plans", nil)
	asUser(ctx, 1)
	GetSubscriptionPlans(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestGetSubscriptionSelf_OK(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/subscription/self", nil)
	asUser(ctx, u.Id)
	GetSubscriptionSelf(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestUpdateSubscriptionPreference_OK(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodPost, "/api/subscription/preference",
		BillingPreferenceRequest{BillingPreference: "priority_wallet"})
	asUser(ctx, u.Id)
	UpdateSubscriptionPreference(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestUpdateSubscriptionPreference_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/subscription/preference", "{bad")
	asUser(ctx, 1)
	UpdateSubscriptionPreference(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestSubscriptionRequestBalancePay_ComplianceRefused(t *testing.T) {
	withPaymentComplianceUnconfirmed(t)
	// Gate unconfirmed -> refused (HTTP 200 success:false).
	ctx, rec := newCtx(t, http.MethodPost, "/api/subscription/balance-pay",
		SubscriptionBalancePayRequest{PlanId: 1})
	asUser(ctx, 1)
	SubscriptionRequestBalancePay(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestSubscriptionRequestBalancePay_BadParam(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/subscription/balance-pay",
		SubscriptionBalancePayRequest{PlanId: 0})
	asUser(ctx, 1)
	SubscriptionRequestBalancePay(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

// ---- Admin plan CRUD ----

func TestAdminListSubscriptionPlans_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/subscription/plans", nil)
	asAdmin(ctx, 1)
	AdminListSubscriptionPlans(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminCreateSubscriptionPlan_Success(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)
	body := AdminUpsertSubscriptionPlanRequest{}
	body.Plan.Title = uniq("plan")
	body.Plan.PriceAmount = 9
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/plans", body)
	asAdmin(ctx, 1)
	AdminCreateSubscriptionPlan(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	var created model.SubscriptionPlan
	require.NoError(t, unmarshalData(resp.Data, &created))
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.SubscriptionPlan{}, created.Id)
		}
	})
}

func TestAdminCreateSubscriptionPlan_ComplianceRefused(t *testing.T) {
	withPaymentComplianceUnconfirmed(t)
	body := AdminUpsertSubscriptionPlanRequest{}
	body.Plan.Title = "x"
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/plans", body)
	asAdmin(ctx, 1)
	AdminCreateSubscriptionPlan(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminCreateSubscriptionPlan_EmptyTitle(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/plans", AdminUpsertSubscriptionPlanRequest{})
	asAdmin(ctx, 1)
	AdminCreateSubscriptionPlan(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "标题")
}

func TestAdminCreateSubscriptionPlan_PriceTooHigh(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	body := AdminUpsertSubscriptionPlanRequest{}
	body.Plan.Title = "x"
	body.Plan.PriceAmount = 10000
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/plans", body)
	asAdmin(ctx, 1)
	AdminCreateSubscriptionPlan(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "9999")
}

func TestAdminCreateSubscriptionPlan_BadUpgradeGroup(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	body := AdminUpsertSubscriptionPlanRequest{}
	body.Plan.Title = "x"
	body.Plan.UpgradeGroup = "nonexistent_group_zzz"
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/plans", body)
	asAdmin(ctx, 1)
	AdminCreateSubscriptionPlan(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "升级分组")
}

func TestAdminUpdateSubscriptionPlan_Success(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)
	p := mkPlan(t, nil)
	body := AdminUpsertSubscriptionPlanRequest{}
	body.Plan.Title = "updated"
	body.Plan.PriceAmount = 5
	ctx, rec := newCtx(t, http.MethodPut, "/api/admin/subscription/plans/"+strconv.Itoa(p.Id), body)
	idParam(ctx, "id", strconv.Itoa(p.Id))
	asAdmin(ctx, 1)
	AdminUpdateSubscriptionPlan(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestAdminUpdateSubscriptionPlan_InvalidId(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	body := AdminUpsertSubscriptionPlanRequest{}
	body.Plan.Title = "x"
	ctx, rec := newCtx(t, http.MethodPut, "/api/admin/subscription/plans/0", body)
	idParam(ctx, "id", "0")
	asAdmin(ctx, 1)
	AdminUpdateSubscriptionPlan(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "无效")
}

func TestAdminUpdateSubscriptionPlanStatus_Success(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)
	p := mkPlan(t, nil)
	enabled := false
	ctx, rec := newCtx(t, http.MethodPut, "/api/admin/subscription/plans/"+strconv.Itoa(p.Id)+"/status",
		AdminUpdateSubscriptionPlanStatusRequest{Enabled: &enabled})
	idParam(ctx, "id", strconv.Itoa(p.Id))
	asAdmin(ctx, 1)
	AdminUpdateSubscriptionPlanStatus(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminUpdateSubscriptionPlanStatus_MissingEnabled(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	ctx, rec := newCtx(t, http.MethodPut, "/api/admin/subscription/plans/1/status",
		AdminUpdateSubscriptionPlanStatusRequest{})
	idParam(ctx, "id", "1")
	asAdmin(ctx, 1)
	AdminUpdateSubscriptionPlanStatus(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

// ---- Admin binds / resets ----

func TestAdminBindSubscription_BadParam(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/bind",
		AdminBindSubscriptionRequest{UserId: 0, PlanId: 0})
	asAdmin(ctx, 1)
	AdminBindSubscription(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminBindSubscription_PlanNotFound(t *testing.T) {
	requireDB(t)
	withPaymentComplianceConfirmed(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/bind",
		AdminBindSubscriptionRequest{UserId: u.Id, PlanId: 999_999_991})
	asAdmin(ctx, 1)
	AdminBindSubscription(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminListUserSubscriptions_OK(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/subscription/user/"+strconv.Itoa(u.Id), nil)
	idParam(ctx, "id", strconv.Itoa(u.Id))
	asAdmin(ctx, 1)
	AdminListUserSubscriptions(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminListUserSubscriptions_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/subscription/user/0", nil)
	idParam(ctx, "id", "0")
	asAdmin(ctx, 1)
	AdminListUserSubscriptions(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "无效")
}

func TestAdminCreateUserSubscription_BadParam(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/user/5",
		AdminCreateUserSubscriptionRequest{PlanId: 0})
	idParam(ctx, "id", "5")
	asAdmin(ctx, 1)
	AdminCreateUserSubscription(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminResetUserSubscriptionsByPlan_BadParam(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/user/5/reset",
		AdminResetSubscriptionRequest{PlanId: 0})
	idParam(ctx, "id", "5")
	asAdmin(ctx, 1)
	AdminResetUserSubscriptionsByPlan(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminResetPlanSubscriptions_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/plans/0/reset",
		AdminResetSubscriptionRequest{PlanId: 1})
	idParam(ctx, "id", "0")
	asAdmin(ctx, 1)
	AdminResetPlanSubscriptions(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "无效")
}

func TestAdminInvalidateUserSubscription_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/subscription/invalidate/0", nil)
	idParam(ctx, "id", "0")
	asAdmin(ctx, 1)
	AdminInvalidateUserSubscription(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminDeleteUserSubscription_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodDelete, "/api/admin/subscription/0", nil)
	idParam(ctx, "id", "0")
	asAdmin(ctx, 1)
	AdminDeleteUserSubscription(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestResolveAdvanceResetTime_Coverage(t *testing.T) {
	require.True(t, resolveAdvanceResetTime(nil))
	v := false
	require.False(t, resolveAdvanceResetTime(&v))
}
