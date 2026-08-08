package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// request_log.go — 入参校验与"不可用"分支。
//
// 这些响应必须走 i18n 键，不能回落成手写字符串或底层错误串：客户端看到
// "redis: nil" / "invalid id" 既无法据此行动，也泄漏了实现细节（Rule 9 / Rule 13）。
// ---------------------------------------------------------------------------

// translated 用一个独立 context 解析 i18n 键，得到与被测响应同语言的期望值。
func translated(t *testing.T, key string) string {
	t.Helper()
	ctx, _ := newCtx(t, http.MethodGet, "/", nil)
	msg := common.TranslateMessage(ctx, key)
	require.NotEmpty(t, msg)
	return msg
}

func TestGetRequestLogDetail_InvalidID(t *testing.T) {
	want := translated(t, i18n.MsgInvalidId)
	for _, id := range []string{"abc", "0", "-5"} {
		ctx, rec := newCtx(t, http.MethodGet, "/api/request_log/"+id, nil)
		ctx.AddParam("id", id)
		asRoot(ctx, nextTestID())
		GetRequestLogDetail(ctx)
		resp := decodeResp(t, rec)
		require.False(t, resp.Success, "id=%s must be rejected", id)
		require.Equal(t, want, resp.Message, "id=%s", id)
	}
}

// 索引里没有该 id（已被淘汰/清空）时返回可读的"不可用"，而不是底层错误。
func TestGetRequestLogDetail_NotFound(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/request_log/987654321", nil)
	ctx.AddParam("id", "987654321")
	asRoot(ctx, nextTestID())
	GetRequestLogDetail(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Equal(t, translated(t, i18n.MsgRequestLogNotFound), resp.Message)
	require.NotContains(t, resp.Message, "redis")
}

func TestDeleteHistoryRequestLogs_MissingTimestamp(t *testing.T) {
	// target_timestamp 缺省 -> 解析为 0 -> 拒绝
	ctx, rec := newCtx(t, http.MethodDelete, "/api/request_log/history", nil)
	asRoot(ctx, nextTestID())
	DeleteHistoryRequestLogs(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Equal(t, translated(t, i18n.MsgRequestLogTimestampRequired), resp.Message)
	require.NotContains(t, resp.Message, "target_timestamp")
}
