package controller

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// employee.go / subscription.go: small pure helpers (page normalization, id
// masking, advance-reset defaulting).

func TestNormalizePrefixedPage(t *testing.T) {
	ctx, _ := newCtx(t, "GET", "/x?comm_page=2&comm_page_size=30", nil)
	page, size := normalizePrefixedPage(ctx, "comm_")
	assert.Equal(t, 2, page)
	assert.Equal(t, 30, size)

	// Defaults when unset.
	ctx2, _ := newCtx(t, "GET", "/x", nil)
	page, size = normalizePrefixedPage(ctx2, "comm_")
	assert.Equal(t, 1, page)
	assert.Equal(t, 20, size)

	// Out-of-range clamps.
	ctx3, _ := newCtx(t, "GET", "/x?comm_page=-1&comm_page_size=999", nil)
	page, size = normalizePrefixedPage(ctx3, "comm_")
	assert.Equal(t, 1, page)
	assert.Equal(t, 20, size)
}

func TestResolveAdvanceResetTime(t *testing.T) {
	assert.True(t, resolveAdvanceResetTime(nil), "nil defaults to true")
	tr := true
	fa := false
	assert.True(t, resolveAdvanceResetTime(&tr))
	assert.False(t, resolveAdvanceResetTime(&fa))
}

func TestAdminAddEmployeePerformance_InvalidEmployeeId(t *testing.T) {
	// Non-numeric :id param short-circuits before any DB access.
	ctx, rec := newRawCtx(t, "POST", "/api/admin/employee/x/performance", "{}")
	ctx.Params = []gin.Param{{Key: "id", Value: "notint"}}
	asAdmin(ctx, 1)
	AdminAddEmployeePerformance(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "invalid employee id")
}

func TestAdminAddEmployeePerformance_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, "POST", "/api/admin/employee/1/performance", "{bad")
	ctx.Params = []gin.Param{{Key: "id", Value: "1"}}
	asAdmin(ctx, 1)
	AdminAddEmployeePerformance(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}
