package controller

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// request_log.go — input-validation guards (these short-circuit before any DB
// access, so they run without a request-log table). The list/detail happy
// paths depend on the request-log store and are out of scope here.
// ---------------------------------------------------------------------------

func TestGetRequestLogDetail_InvalidID(t *testing.T) {
	for _, id := range []string{"abc", "0", "-5"} {
		ctx, rec := newCtx(t, http.MethodGet, "/api/request_log/"+id, nil)
		ctx.AddParam("id", id)
		asRoot(ctx, nextTestID())
		GetRequestLogDetail(ctx)
		resp := decodeResp(t, rec)
		require.False(t, resp.Success, "id=%s must be rejected", id)
		require.Equal(t, "invalid id", resp.Message)
	}
}

func TestDeleteHistoryRequestLogs_MissingTimestamp(t *testing.T) {
	// target_timestamp absent -> parsed as 0 -> rejected.
	ctx, rec := newCtx(t, http.MethodDelete, "/api/request_log/history", nil)
	asRoot(ctx, nextTestID())
	DeleteHistoryRequestLogs(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "target_timestamp is required")
}
