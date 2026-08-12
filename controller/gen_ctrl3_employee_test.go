package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// employee.go — Admin CRUD, tier config, channel cost, self-service.
// Factories create a real user + EmployeeProfile with row-scoped cleanup.
// The global mutating handlers (AdminTriggerTierReset / AdminSwitchCommissionPeriod)
// are intentionally NOT exercised: they rewrite every employee's tier level in the
// shared dev DB and would pollute other tests (Rule: never global-mutate shared tables).
// ---------------------------------------------------------------------------

// mkEmployee inserts a user and an active EmployeeProfile for that user.
func mkEmployee(t *testing.T, mut func(u *model.User)) (*model.User, *model.EmployeeProfile) {
	t.Helper()
	requireDB(t)
	u := mkUser(t, mut)
	emp := &model.EmployeeProfile{UserId: u.Id, Status: 1, Remark: "emp-remark"}
	require.NoError(t, model.CreateEmployee(emp))
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.EmployeeProfile{}, emp.Id)
			model.DB.Exec("DELETE FROM user_extensions WHERE user_id = ?", u.Id)
			model.DB.Exec("DELETE FROM customer_profiles WHERE customer_user_id = ?", u.Id)
		}
	})
	return u, emp
}

// mkTier inserts a commission tier and registers cleanup.
func mkTier(t *testing.T, level int, group string, threshold, rate float64) *model.EmployeeCommissionTier {
	t.Helper()
	requireDB(t)
	tier := &model.EmployeeCommissionTier{Level: level, Group: group, ThresholdUsd: threshold, Rate: rate}
	require.NoError(t, model.CreateTier(tier))
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.EmployeeCommissionTier{}, tier.Id)
		}
	})
	return tier
}

func idParam(ctx *gin.Context, key, val string) {
	ctx.Params = append(ctx.Params, gin.Param{Key: key, Value: val})
}

// ---- AdminListEmployees ----

func TestAdminListEmployees_OK(t *testing.T) {
	requireDB(t)
	u, _ := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee?page=1&page_size=20&user_id="+strconv.Itoa(u.Id), nil)
	asAdmin(ctx, 1)
	AdminListEmployees(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
}

func TestAdminListEmployees_PageClamp(t *testing.T) {
	requireDB(t)
	// page<1 and page_size out of range clamp to defaults; no crash.
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee?page=-3&page_size=9999&sort_order=asc", nil)
	asAdmin(ctx, 1)
	AdminListEmployees(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)
}

// ---- AdminCreateEmployee ----

func TestAdminCreateEmployee_Success(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	body := CreateEmployeeRequest{UserId: u.Id, Remark: "hello"}
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee", body)
	asAdmin(ctx, 1)
	AdminCreateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	// cleanup employee + extension rows
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Exec("DELETE FROM employee_profiles WHERE user_id = ?", u.Id)
			model.DB.Exec("DELETE FROM user_extensions WHERE user_id = ?", u.Id)
			model.DB.Exec("DELETE FROM customer_profiles WHERE customer_user_id = ?", u.Id)
		}
	})
}

func TestAdminCreateEmployee_UserNotFound(t *testing.T) {
	requireDB(t)
	body := CreateEmployeeRequest{UserId: 999_999_991}
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee", body)
	asAdmin(ctx, 1)
	AdminCreateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "user not found")
}

func TestAdminCreateEmployee_TierNotFound(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	body := CreateEmployeeRequest{UserId: u.Id, TierId: 999_999_991}
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee", body)
	asAdmin(ctx, 1)
	AdminCreateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "tier not found")
}

func TestAdminCreateEmployee_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/admin/employee", "{bad")
	asAdmin(ctx, 1)
	AdminCreateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

func TestAdminCreateEmployee_MissingUserId(t *testing.T) {
	// user_id is binding:required -> validation error.
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee", CreateEmployeeRequest{})
	asAdmin(ctx, 1)
	AdminCreateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

// ---- AdminUpdateEmployee ----

func TestAdminUpdateEmployee_Success(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodPut, "/api/admin/employee/"+strconv.Itoa(emp.Id), UpdateEmployeeRequest{Status: 2, Remark: "upd"})
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	asAdmin(ctx, 1)
	AdminUpdateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
}

func TestAdminUpdateEmployee_InvalidId(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/admin/employee/x", "{}")
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	AdminUpdateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid id")
}

func TestAdminUpdateEmployee_NotFound(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPut, "/api/admin/employee/999999991", UpdateEmployeeRequest{Status: 1})
	idParam(ctx, "id", "999999991")
	asAdmin(ctx, 1)
	AdminUpdateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "not found")
}

func TestAdminUpdateEmployee_BadStatus(t *testing.T) {
	// status must be 1..2 -> validation error for 5.
	ctx, rec := newCtx(t, http.MethodPut, "/api/admin/employee/1", UpdateEmployeeRequest{Status: 5})
	idParam(ctx, "id", "1")
	asAdmin(ctx, 1)
	AdminUpdateEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

// ---- AdminDeleteEmployee ----

func TestAdminDeleteEmployee_Success(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodDelete, "/api/admin/employee/"+strconv.Itoa(emp.Id), nil)
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	asAdmin(ctx, 1)
	AdminDeleteEmployee(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)
}

func TestAdminDeleteEmployee_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodDelete, "/api/admin/employee/x", nil)
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	AdminDeleteEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid id")
}

// ---- AdminAddEmployeePerformance ----

func TestAdminAddEmployeePerformance_Success(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/"+strconv.Itoa(emp.Id)+"/performance",
		AddEmployeePerformanceRequest{ProfitUsd: 1.5, Reason: "bonus"})
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	asAdmin(ctx, 7)
	AdminAddEmployeePerformance(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Exec("DELETE FROM employee_commission_logs WHERE employee_user_id = ?", emp.UserId)
			model.DB.Exec("DELETE FROM employee_performance_adjust_logs WHERE employee_user_id = ?", emp.UserId)
		}
	})
}

func TestAdminAddEmployeePerformance_AllowsAmountBeyondBillingQuotaLimit(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/"+strconv.Itoa(emp.Id)+"/performance",
		AddEmployeePerformanceRequest{ProfitUsd: 100_000, Reason: "large adjustment"})
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	asAdmin(ctx, 7)
	AdminAddEmployeePerformance(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Exec("DELETE FROM employee_commission_logs WHERE employee_user_id = ?", emp.UserId)
			model.DB.Exec("DELETE FROM employee_performance_adjust_logs WHERE employee_user_id = ?", emp.UserId)
		}
	})
}

func TestAdminAddEmployeePerformance_EmployeeNotFound(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/999999991/performance",
		AddEmployeePerformanceRequest{ProfitUsd: 1})
	idParam(ctx, "id", "999999991")
	asAdmin(ctx, 1)
	AdminAddEmployeePerformance(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "employee not found")
}

func TestAdminAddEmployeePerformance_OutOfRange(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	// Huge value overflows int32 quota bounds -> "out of range".
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/"+strconv.Itoa(emp.Id)+"/performance",
		AddEmployeePerformanceRequest{ProfitUsd: 1e30})
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	asAdmin(ctx, 1)
	AdminAddEmployeePerformance(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "out of range")
}

// ---- AdminRevertPerformanceAdjustment ----

func TestAdminRevertPerformanceAdjustment_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/performance/x/revert", nil)
	idParam(ctx, "logId", "x")
	asAdmin(ctx, 1)
	AdminRevertPerformanceAdjustment(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid id")
}

func TestAdminRevertPerformanceAdjustment_NotFound(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/performance/999999991/revert", nil)
	idParam(ctx, "logId", "999999991")
	asAdmin(ctx, 1)
	AdminRevertPerformanceAdjustment(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
}

// ---- Commission log / summary read endpoints ----

func TestAdminListCommissionLogs_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/commission?page=1&page_size=20", nil)
	asAdmin(ctx, 1)
	AdminListCommissionLogs(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminListCommissionChannelOptions_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/commission/channels", nil)
	asAdmin(ctx, 1)
	AdminListCommissionChannelOptions(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminCommissionSummary_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/commission/summary", nil)
	asAdmin(ctx, 1)
	AdminCommissionSummary(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminListCommissionResetPeriodStats_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/commission/monthly", nil)
	asAdmin(ctx, 1)
	AdminListCommissionResetPeriodStats(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminCommissionCalendarStats_MissingTime(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/commission/calendar", nil)
	asAdmin(ctx, 1)
	AdminCommissionCalendarStats(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "required")
}

func TestAdminCommissionMonthlyExport_MissingTime(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/commission/monthly-export", nil)
	asAdmin(ctx, 1)
	AdminCommissionMonthlyExport(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "required")
}

func TestAdminCommissionOverview_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/overview", nil)
	asAdmin(ctx, 1)
	AdminCommissionOverview(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestAdminChannelProfitPage_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/overview/channels", nil)
	asAdmin(ctx, 1)
	AdminChannelProfitPage(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

// ---- Channel cost config ----

func TestAdminChannelCost_UpsertListDelete(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	// Upsert (create path).
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/channel/cost", UpsertChannelCostRequest{ChannelId: ch.Id, CostRatio: 0.6, Remark: "r"})
	asAdmin(ctx, 1)
	AdminUpsertChannelCost(ctx)
	require.True(t, decodeResp(t, rec).Success)
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Exec("DELETE FROM channel_cost_configs WHERE channel_id = ?", ch.Id)
		}
	})

	// Upsert again (update path).
	ctx2, rec2 := newCtx(t, http.MethodPost, "/api/admin/channel/cost", UpsertChannelCostRequest{ChannelId: ch.Id, CostRatio: 0.7})
	asAdmin(ctx2, 1)
	AdminUpsertChannelCost(ctx2)
	require.True(t, decodeResp(t, rec2).Success)

	// List includes it.
	ctx3, rec3 := newCtx(t, http.MethodGet, "/api/admin/channel/cost", nil)
	asAdmin(ctx3, 1)
	AdminListChannelCosts(ctx3)
	require.True(t, decodeResp(t, rec3).Success)

	// Delete.
	ctx4, rec4 := newCtx(t, http.MethodDelete, "/api/admin/channel/cost/"+strconv.Itoa(ch.Id), nil)
	idParam(ctx4, "channel_id", strconv.Itoa(ch.Id))
	asAdmin(ctx4, 1)
	AdminDeleteChannelCost(ctx4)
	require.True(t, decodeResp(t, rec4).Success)
}

func TestAdminUpsertChannelCost_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/admin/channel/cost", "{bad")
	asAdmin(ctx, 1)
	AdminUpsertChannelCost(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminDeleteChannelCost_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodDelete, "/api/admin/channel/cost/x", nil)
	idParam(ctx, "channel_id", "x")
	asAdmin(ctx, 1)
	AdminDeleteChannelCost(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid channel id")
}

// ---- Tier CRUD ----

func TestAdminTiers_ListNonPaged(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/tiers", nil)
	asAdmin(ctx, 1)
	AdminListTiers(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminTiers_ListPaged(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/tiers?page=1&page_size=10", nil)
	asAdmin(ctx, 1)
	AdminListTiers(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminTier_CreateUpdateDelete(t *testing.T) {
	requireDB(t)
	grp := uniq("grp")
	// Create.
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/tiers", CreateTierRequest{Level: 1, Group: grp, ThresholdUsd: 0, Rate: 0.1})
	asAdmin(ctx, 1)
	AdminCreateTier(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	var created model.EmployeeCommissionTier
	require.NoError(t, unmarshalData(resp.Data, &created))
	require.NotZero(t, created.Id)
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Unscoped().Delete(&model.EmployeeCommissionTier{}, created.Id)
		}
	})

	// Update.
	ctx2, rec2 := newCtx(t, http.MethodPut, "/api/admin/employee/tiers/"+strconv.FormatInt(created.Id, 10),
		UpdateTierRequest{Level: 2, Group: grp, ThresholdUsd: 100, Rate: 0.2})
	idParam(ctx2, "id", strconv.FormatInt(created.Id, 10))
	asAdmin(ctx2, 1)
	AdminUpdateTier(ctx2)
	require.True(t, decodeResp(t, rec2).Success)

	// Delete.
	ctx3, rec3 := newCtx(t, http.MethodDelete, "/api/admin/employee/tiers/"+strconv.FormatInt(created.Id, 10), nil)
	idParam(ctx3, "id", strconv.FormatInt(created.Id, 10))
	asAdmin(ctx3, 1)
	AdminDeleteTier(ctx3)
	require.True(t, decodeResp(t, rec3).Success)
}

func TestAdminCreateTier_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/admin/employee/tiers", "{bad")
	asAdmin(ctx, 1)
	AdminCreateTier(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminCreateTier_RateTooHigh(t *testing.T) {
	// rate has binding lte=1 -> validation error.
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/tiers", CreateTierRequest{Level: 1, Rate: 5})
	asAdmin(ctx, 1)
	AdminCreateTier(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminUpdateTier_InvalidId(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPut, "/api/admin/employee/tiers/x", "{}")
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	AdminUpdateTier(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid id")
}

func TestAdminUpdateTier_NotFound(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPut, "/api/admin/employee/tiers/999999991",
		UpdateTierRequest{Level: 1, Rate: 0.1})
	idParam(ctx, "id", "999999991")
	asAdmin(ctx, 1)
	AdminUpdateTier(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "not found")
}

func TestAdminDeleteTier_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodDelete, "/api/admin/employee/tiers/x", nil)
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	AdminDeleteTier(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

// ---- AdminSetEmployeeTier ----

func TestAdminSetEmployeeTier_Success(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	tier := mkTier(t, 1, uniq("g"), 0, 0.1)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/"+strconv.Itoa(emp.Id)+"/tier",
		SetEmployeeTierRequest{TierId: tier.Id, Source: "manual"})
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	asAdmin(ctx, 1)
	AdminSetEmployeeTier(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Exec("DELETE FROM employee_tier_levels WHERE user_id = ?", emp.UserId)
			model.DB.Exec("DELETE FROM employee_tier_logs WHERE user_id = ?", emp.UserId)
		}
	})
}

func TestAdminSetEmployeeTier_InvalidId(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/admin/employee/x/tier", "{}")
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	AdminSetEmployeeTier(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid employee id")
}

func TestAdminSetEmployeeTier_EmployeeNotFound(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/999999991/tier",
		SetEmployeeTierRequest{TierId: 0})
	idParam(ctx, "id", "999999991")
	asAdmin(ctx, 1)
	AdminSetEmployeeTier(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "employee not found")
}

// ---- Tier logs / reset config ----

func TestAdminListTierLogs_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/tiers/logs?page=1&page_size=20", nil)
	asAdmin(ctx, 1)
	AdminListTierLogs(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestAdminGetTierResetConfig_OK(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/tiers/reset-config", nil)
	asAdmin(ctx, 1)
	AdminGetTierResetConfig(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

// ---- Employee self-service ----

func TestGetMyEmployeeProfile_NotEmployee(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/profile", nil)
	asUser(ctx, u.Id)
	GetMyEmployeeProfile(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "not an employee")
}

func TestGetMyEmployeeProfile_IsEmployee(t *testing.T) {
	requireDB(t)
	u, _ := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/profile", nil)
	asUser(ctx, u.Id)
	GetMyEmployeeProfile(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestGetMyCommissionLogs_NotEmployee(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/commission", nil)
	asUser(ctx, u.Id)
	GetMyCommissionLogs(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "permission denied")
}

func TestGetMyCommissionLogs_IsEmployee(t *testing.T) {
	requireDB(t)
	u, _ := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/commission?page=1&page_size=20", nil)
	asUser(ctx, u.Id)
	GetMyCommissionLogs(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestGetMyCommissionSummary_IsEmployee(t *testing.T) {
	requireDB(t)
	u, _ := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/commission/summary", nil)
	asUser(ctx, u.Id)
	GetMyCommissionSummary(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestGetMyCommissionSummary_NotEmployee(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/commission/summary", nil)
	asUser(ctx, u.Id)
	GetMyCommissionSummary(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestGetMyCommissionResetPeriodStats_IsEmployee(t *testing.T) {
	requireDB(t)
	u, _ := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/commission/monthly", nil)
	asUser(ctx, u.Id)
	GetMyCommissionResetPeriodStats(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

func TestGetMyCommissionCalendarStats_MissingTime(t *testing.T) {
	requireDB(t)
	u, _ := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/commission/calendar", nil)
	asUser(ctx, u.Id)
	GetMyCommissionCalendarStats(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "required")
}

// ---- Customer assignment ----

func TestAdminAssignCustomerToEmployee_Success(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	cust := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/"+strconv.Itoa(emp.Id)+"/assign-customer",
		gin.H{"user_id": cust.Id})
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	asAdmin(ctx, 1)
	AdminAssignCustomerToEmployee(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "body: %s", rec.Body.String())
	t.Cleanup(func() {
		if model.DB != nil {
			model.DB.Exec("DELETE FROM customer_profiles WHERE customer_user_id = ?", cust.Id)
		}
	})
}

func TestAdminAssignCustomerToEmployee_InvalidId(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/api/admin/employee/x/assign-customer", "{}")
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	AdminAssignCustomerToEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid employee id")
}

func TestAdminAssignCustomerToEmployee_EmployeeNotFound(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPost, "/api/admin/employee/999999991/assign-customer",
		gin.H{"user_id": 5})
	idParam(ctx, "id", "999999991")
	asAdmin(ctx, 1)
	AdminAssignCustomerToEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "employee not found")
}

func TestAdminListEmployeeCustomers_OK(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/"+strconv.Itoa(emp.Id)+"/customers", nil)
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	asAdmin(ctx, 1)
	AdminListEmployeeCustomers(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestAdminListEmployeeCustomers_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee/x/customers", nil)
	idParam(ctx, "id", "x")
	asAdmin(ctx, 1)
	AdminListEmployeeCustomers(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid employee id")
}

func TestAdminUnassignCustomerFromEmployee_InvalidCustomerId(t *testing.T) {
	requireDB(t)
	_, emp := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodDelete, "/api/admin/employee/"+strconv.Itoa(emp.Id)+"/customer/x", nil)
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	idParam(ctx, "user_id", "x")
	asAdmin(ctx, 1)
	AdminUnassignCustomerFromEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid customer id")
}

func TestAdminUnassignCustomerFromEmployee_SelfUnassign(t *testing.T) {
	requireDB(t)
	u, emp := mkEmployee(t, nil)
	ctx, rec := newCtx(t, http.MethodDelete, "/api/admin/employee/"+strconv.Itoa(emp.Id)+"/customer/"+strconv.Itoa(u.Id), nil)
	idParam(ctx, "id", strconv.Itoa(emp.Id))
	idParam(ctx, "user_id", strconv.Itoa(u.Id))
	asAdmin(ctx, 1)
	AdminUnassignCustomerFromEmployee(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "themselves")
}
