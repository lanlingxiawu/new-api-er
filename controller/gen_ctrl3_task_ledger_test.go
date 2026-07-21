package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// task.go — task listing (admin + user scope).
// ---------------------------------------------------------------------------

func TestGetAllTask_OK(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/task?p=1&page_size=10", nil)
	asAdmin(ctx, 1)
	GetAllTask(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

func TestGetUserTask_OK(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, http.MethodGet, "/api/task/self?p=1&page_size=10", nil)
	asUser(ctx, u.Id)
	GetUserTask(ctx)
	require.True(t, decodeResp(t, rec).Success, "body: %s", rec.Body.String())
}

// ---------------------------------------------------------------------------
// employee_ledger.go — pure query-parsing helpers + gated stats endpoints.
// ---------------------------------------------------------------------------

func TestParseIntQueryWithDefault(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodGet, "/x?limit=15", nil)
	v, err := parseIntQueryWithDefault(ctx, "limit", 20)
	require.NoError(t, err)
	require.Equal(t, 15, v)

	ctx2, _ := newCtx(t, http.MethodGet, "/x", nil)
	v, err = parseIntQueryWithDefault(ctx2, "limit", 20)
	require.NoError(t, err)
	require.Equal(t, 20, v)

	ctx3, _ := newCtx(t, http.MethodGet, "/x?limit=-4", nil)
	_, err = parseIntQueryWithDefault(ctx3, "limit", 20)
	require.Error(t, err)

	ctx4, _ := newCtx(t, http.MethodGet, "/x?limit=abc", nil)
	_, err = parseIntQueryWithDefault(ctx4, "limit", 20)
	require.Error(t, err)
}

func TestParseOptionalIntQuery(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodGet, "/x", nil)
	v, err := parseOptionalIntQuery(ctx, "id")
	require.NoError(t, err)
	require.Equal(t, 0, v)

	ctx2, _ := newCtx(t, http.MethodGet, "/x?id=7", nil)
	v, err = parseOptionalIntQuery(ctx2, "id")
	require.NoError(t, err)
	require.Equal(t, 7, v)

	ctx3, _ := newCtx(t, http.MethodGet, "/x?id=-1", nil)
	_, err = parseOptionalIntQuery(ctx3, "id")
	require.Error(t, err)
}

func TestParseOptionalPointerQueries(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodGet, "/x?a=5&b=1.5&c=true", nil)
	i64, err := parseOptionalInt64PointerQuery(ctx, "a")
	require.NoError(t, err)
	require.NotNil(t, i64)
	require.Equal(t, int64(5), *i64)

	f64, err := parseOptionalFloat64PointerQuery(ctx, "b")
	require.NoError(t, err)
	require.NotNil(t, f64)
	require.Equal(t, 1.5, *f64)

	b, err := parseOptionalBoolPointerQuery(ctx, "c")
	require.NoError(t, err)
	require.NotNil(t, b)
	require.True(t, *b)

	// Absent keys -> nil, no error.
	empty, _ := newCtx(t, http.MethodGet, "/x", nil)
	pi, err := parseOptionalInt64PointerQuery(empty, "a")
	require.NoError(t, err)
	require.Nil(t, pi)

	// Malformed -> error.
	bad, _ := newCtx(t, http.MethodGet, "/x?a=xx&b=yy&c=zz", nil)
	_, err = parseOptionalInt64PointerQuery(bad, "a")
	require.Error(t, err)
	_, err = parseOptionalFloat64PointerQuery(bad, "b")
	require.Error(t, err)
	_, err = parseOptionalBoolPointerQuery(bad, "c")
	require.Error(t, err)
}

func TestLedgerSemaphore_AcquireRelease(t *testing.T) {
	sem := make(chan struct{}, 1)
	require.True(t, tryAcquireLedgerSemaphore(sem))
	require.False(t, tryAcquireLedgerSemaphore(sem)) // full
	releaseLedgerSemaphore(sem)
	require.True(t, tryAcquireLedgerSemaphore(sem))
	releaseLedgerSemaphore(sem)
	releaseLedgerSemaphore(sem) // release on empty is a no-op
}

func TestGetLedgerRequestUserID(t *testing.T) {
	// Not set -> 0.
	ctx, _ := newCtx(t, http.MethodGet, "/x", nil)
	require.Equal(t, 0, getLedgerRequestUserID(ctx))

	// int value.
	ctx2, _ := newCtx(t, http.MethodGet, "/x", nil)
	ctx2.Set(string(constant.ContextKeyUserId), 42)
	require.Equal(t, 42, getLedgerRequestUserID(ctx2))

	// string value.
	ctx3, _ := newCtx(t, http.MethodGet, "/x", nil)
	ctx3.Set(string(constant.ContextKeyUserId), "99")
	require.Equal(t, 99, getLedgerRequestUserID(ctx3))
}

func TestDefaultLedgerEndTime_Aligned(t *testing.T) {
	// Result is minute-aligned.
	require.Equal(t, int64(0), defaultLedgerEndTime()%60)
}

func TestParseConsumptionCostLedgerCommonFilter_InvalidTag(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodGet, "/x?tag=__nope__", nil)
	_, err := parseConsumptionCostLedgerCommonFilter(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid tag")
}

func TestParseConsumptionCostLedgerCommonFilter_DefaultsRange(t *testing.T) {
	// No time filter and no unique filter -> defaults applied, no error.
	ctx, _ := newCtx(t, http.MethodGet, "/x", nil)
	f, err := parseConsumptionCostLedgerCommonFilter(ctx)
	require.NoError(t, err)
	require.Greater(t, f.EndTime, f.StartTime)
}

func TestAdminGetConsumptionCostLedgerStats_BadFilter(t *testing.T) {
	// Invalid tag -> ledgerJSONError with HTTP 400 (bespoke envelope).
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/x?tag=__nope__", nil)
	asAdmin(ctx, 1)
	AdminGetConsumptionCostLedgerStats(ctx)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminGetConsumptionCostLedgerStats_OK(t *testing.T) {
	requireLogDB(t)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	asAdmin(ctx, 1)
	AdminGetConsumptionCostLedgerStats(ctx)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestAdminListConsumptionCostLedger_OK(t *testing.T) {
	requireLogDB(t)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/x?limit=5", nil)
	asAdmin(ctx, 1)
	AdminListConsumptionCostLedger(ctx)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestParseConsumptionCostLedgerListFilter_CursorMismatch(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodGet, "/x?cursor_created_at=100", nil)
	_, err := parseConsumptionCostLedgerListFilter(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cursor")
}

// ---------------------------------------------------------------------------
// employee_ledger_backfill.go — pure helpers + endpoints.
// ---------------------------------------------------------------------------

func TestIsValidDateParam(t *testing.T) {
	require.True(t, isValidDateParam("2026-01-02"))
	require.False(t, isValidDateParam("2026-1-2"))
	require.False(t, isValidDateParam("2026/01/02"))
	require.False(t, isValidDateParam("not-a-date"))
	require.False(t, isValidDateParam("20260102"))
}

func TestBoolStr(t *testing.T) {
	require.Equal(t, "true", boolStr(true))
	require.Equal(t, "false", boolStr(false))
}

func TestAdminGetFallbackStatus_InvalidDate(t *testing.T) {
	// When backfill enabled, an invalid date returns an i18n error; when disabled
	// it short-circuits to success with HasFile=false. Either way HTTP 200.
	ctx, rec := newCtx(t, http.MethodGet, "/x?date=bad", nil)
	asAdmin(ctx, 1)
	AdminGetFallbackStatus(ctx)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminTriggerBackfill_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, http.MethodPost, "/x", "{bad")
	asAdmin(ctx, 1)
	AdminTriggerBackfill(ctx)
	require.False(t, decodeResp(t, rec).Success)
}

func TestAdminGetBackfillResult_OK(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/x", nil)
	asAdmin(ctx, 1)
	AdminGetBackfillResult(ctx)
	require.True(t, decodeResp(t, rec).Success)
}

// ---------------------------------------------------------------------------
// employee_ledger_export.go — parse filter + Redis-required gating.
// ---------------------------------------------------------------------------

func TestParseConsumptionCostLedgerExportFilter_MissingTime(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodGet, "/x", nil)
	_, err := parseConsumptionCostLedgerExportFilter(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "required")
}

func TestParseConsumptionCostLedgerExportFilter_InvalidTag(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodGet, "/x?tag=__nope__", nil)
	_, err := parseConsumptionCostLedgerExportFilter(ctx)
	require.Error(t, err)
}

func TestAdminCreateLedgerExport_NoRedis(t *testing.T) {
	// Redis disabled by default in the harness -> 503.
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	asAdmin(ctx, 1)
	AdminCreateLedgerExport(ctx)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAdminGetLedgerExport_NoRedis(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	idParam(ctx, "job_id", "abc")
	asAdmin(ctx, 1)
	AdminGetLedgerExport(ctx)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAdminGetLedgerExportDownloadURL_NoRedis(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	idParam(ctx, "job_id", "abc")
	asAdmin(ctx, 1)
	AdminGetLedgerExportDownloadURL(ctx)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAdminDownloadLedgerExport_NoRedis(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	idParam(ctx, "token", "tok")
	AdminDownloadLedgerExport(ctx)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
