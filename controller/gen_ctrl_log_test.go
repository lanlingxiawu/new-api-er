package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// log.go: export-text helpers, deprecated endpoints, and handler input guards.

func TestSearchLogs_Deprecated(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/log/search", nil)
	SearchAllLogs(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)

	ctx2, rec2 := newCtx(t, "GET", "/api/log/self/search", nil)
	SearchUserLogs(ctx2)
	resp2 := decodeResp(t, rec2)
	assert.False(t, resp2.Success)
}

func TestGetLogByKey_NoTokenId(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/log/token", nil)
	// token_id not set -> 0 -> invalid token
	GetLogByKey(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestGetEmployeeCustomerLogs_NotEmployee(t *testing.T) {
	requireDB(t)
	// A fresh common user is not an enabled employee.
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, "GET", "/api/log/employee", nil)
	asUser(ctx, u.Id)
	GetEmployeeCustomerLogs(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)

	ctx2, rec2 := newCtx(t, "GET", "/api/log/employee/stat", nil)
	asUser(ctx2, u.Id)
	GetEmployeeCustomerLogsStat(ctx2)
	resp2 := decodeResp(t, rec2)
	assert.False(t, resp2.Success)
}

func TestGetAllLogs_EmptyFilterSucceeds(t *testing.T) {
	requireLogDB(t)
	// Narrow the query to a non-existent username so the result set is empty
	// and deterministic while still exercising the model query path.
	ctx, rec := newCtx(t, "GET", "/api/log/?username="+uniq("nouser")+"&p=1&page_size=10", nil)
	GetAllLogs(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
}

func TestGetUserLogs_EmptyFilterSucceeds(t *testing.T) {
	requireLogDB(t)
	ctx, rec := newCtx(t, "GET", "/api/log/self?p=1&page_size=10", nil)
	asUser(ctx, nextTestID())
	GetUserLogs(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
}

func TestGetLogsStat_And_SelfStat(t *testing.T) {
	requireLogDB(t)
	ctx, rec := newCtx(t, "GET", "/api/log/stat?username="+uniq("nouser"), nil)
	GetLogsStat(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)

	ctx2, rec2 := newCtx(t, "GET", "/api/log/self/stat", nil)
	ctx2.Set("username", uniq("nouser"))
	GetLogsSelfStat(ctx2)
	resp2 := decodeResp(t, rec2)
	assert.True(t, resp2.Success)
}
