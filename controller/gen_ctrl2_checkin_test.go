package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// checkin.go — the feature is gated behind operation_setting.CheckinSetting.
// Enabled. When disabled (the default), both handlers must short-circuit with
// a business error before touching the DB. The enabled/award path mutates user
// quota + writes a log and is out of scope for a deterministic unit test.
// ---------------------------------------------------------------------------

// withCheckinEnabled forces the checkin gate to a known value and restores it.
func withCheckinDisabled(t *testing.T) {
	t.Helper()
	s := operation_setting.GetCheckinSetting()
	prev := s.Enabled
	s.Enabled = false
	t.Cleanup(func() { s.Enabled = prev })
}

func TestGetCheckinStatus_Disabled(t *testing.T) {
	withCheckinDisabled(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/checkin", nil)
	asUser(ctx, nextTestID())
	GetCheckinStatus(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "签到功能未启用")
}

func TestDoCheckin_Disabled(t *testing.T) {
	withCheckinDisabled(t)
	ctx, rec := newCtx(t, http.MethodPost, "/api/user/checkin", nil)
	asUser(ctx, nextTestID())
	DoCheckin(ctx)
	resp := decodeResp(t, rec)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "签到功能未启用")
}
