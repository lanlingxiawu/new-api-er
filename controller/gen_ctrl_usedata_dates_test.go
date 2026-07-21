package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// usedata.go: the non-flow quota-date endpoints (GetAllQuotaDates /
// GetQuotaDatesByUser / GetUserQuotaDates). The flow-quota endpoints and
// parseFlowQuotaTimeRange are covered in usedata_flow_test.go.

func TestGetUserQuotaDates_SpanTooLarge(t *testing.T) {
	// Span > 30 days (2592000s) is rejected before any DB access.
	ctx, rec := newCtx(t, "GET", "/x?start_timestamp=0&end_timestamp=2592001", nil)
	asUser(ctx, nextTestID())
	GetUserQuotaDates(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestGetUserQuotaDates_ValidRange(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, "GET", "/x?start_timestamp=1&end_timestamp=1000", nil)
	asUser(ctx, u.Id)
	GetUserQuotaDates(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
}

func TestGetAllQuotaDates_NarrowUsername(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, "GET", "/x?start_timestamp=1&end_timestamp=1000&username="+uniq("nouser"), nil)
	GetAllQuotaDates(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
}

func TestGetQuotaDatesByUser_NarrowRange(t *testing.T) {
	requireDB(t)
	ctx, rec := newCtx(t, "GET", "/x?start_timestamp=1&end_timestamp=1000", nil)
	GetQuotaDatesByUser(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
}
