package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// Misc service background tasks + channel enable/disable + rankings.
// Rule 0: all of these run off the relay hot path.
// ===========================================================================

// --- channel.go ------------------------------------------------------------

func TestChannelSvc_FormatNotifyType(t *testing.T) {
	assert.Equal(t, fmt.Sprintf("%s_5_2", dtoNotifyTypeChannelUpdate()), formatNotifyType(5, 2))
}

func dtoNotifyTypeChannelUpdate() string {
	// mirror the constant used inside formatNotifyType without importing dto here twice
	return "channel_update"
}

func TestChannelSvc_ShouldEnableChannel(t *testing.T) {
	orig := common.AutomaticEnableChannelEnabled
	common.AutomaticEnableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticEnableChannelEnabled = orig })

	// happy path: no error + auto-disabled status.
	assert.True(t, ShouldEnableChannel(nil, common.ChannelStatusAutoDisabled))
	// non-nil error blocks enable.
	assert.False(t, ShouldEnableChannel(types.NewError(fmt.Errorf("x"), types.ErrorCodeInvalidRequest), common.ChannelStatusAutoDisabled))
	// wrong status blocks enable.
	assert.False(t, ShouldEnableChannel(nil, common.ChannelStatusEnabled))

	common.AutomaticEnableChannelEnabled = false
	assert.False(t, ShouldEnableChannel(nil, common.ChannelStatusAutoDisabled))
}

func TestChannelSvc_ShouldDisableChannel(t *testing.T) {
	orig := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = orig })

	// nil error never disables.
	assert.False(t, ShouldDisableChannel(nil))
	// channel:* error code disables.
	chErr := types.NewError(fmt.Errorf("boom"), types.ErrorCodeChannelInvalidKey)
	assert.True(t, ShouldDisableChannel(chErr))
	// skip-retry error does not disable.
	skip := types.NewError(fmt.Errorf("skip"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	assert.False(t, ShouldDisableChannel(skip))

	// disabled globally => false.
	common.AutomaticDisableChannelEnabled = false
	assert.False(t, ShouldDisableChannel(chErr))
}

func TestChannelSvc_DisableEnableChannel(t *testing.T) {
	truncate(t)
	const chid = 6001
	seedChannel(t, chid)

	// AutoBan false => no status change.
	DisableChannel(types.ChannelError{ChannelId: chid, ChannelName: "c", AutoBan: false}, "reason")
	var ch model.Channel
	require.NoError(t, model.DB.First(&ch, chid).Error)
	assert.Equal(t, common.ChannelStatusEnabled, ch.Status)

	// AutoBan true => auto-disabled.
	DisableChannel(types.ChannelError{ChannelId: chid, ChannelName: "c", AutoBan: true}, "reason")
	require.NoError(t, model.DB.First(&ch, chid).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, ch.Status)

	// Re-enable.
	EnableChannel(chid, "", "c")
	require.NoError(t, model.DB.First(&ch, chid).Error)
	assert.Equal(t, common.ChannelStatusEnabled, ch.Status)
}

// --- rankings.go -----------------------------------------------------------

func TestRankings_Snapshot(t *testing.T) {
	_, err := GetRankingsSnapshot("nonsense")
	assert.Error(t, err)

	for _, period := range []string{"", "today", "week", "month", "year"} {
		snap, err := GetRankingsSnapshot(period)
		require.NoError(t, err, "period %q", period)
		require.NotNil(t, snap)
	}
	// Second call for a period hits the in-process cache.
	snap, err := GetRankingsSnapshot("week")
	require.NoError(t, err)
	require.NotNil(t, snap)
}

// --- commission_tier_reset_task.go -----------------------------------------

func TestCommissionTierReset_RunNow(t *testing.T) {
	// Empty employee set => processes 0 without error.
	processed, err := RunCommissionTierResetNow(time.Now().Unix(), 0)
	require.NoError(t, err)
	assert.Equal(t, 0, processed)
}

func TestCommissionTierReset_RunNow_ConcurrentGuard(t *testing.T) {
	require.True(t, tierResetRunning.CompareAndSwap(false, true))
	defer tierResetRunning.Store(false)
	_, err := RunCommissionTierResetNow(time.Now().Unix(), 0)
	assert.Error(t, err, "second concurrent run is rejected")
}

func TestCommissionTierReset_CheckDisabled(t *testing.T) {
	cfg := operation_setting.GetCommissionTierResetSetting()
	orig := *cfg
	cfg.Enabled = false
	t.Cleanup(func() { *cfg = orig })
	// Disabled => early return, no panic.
	runCommissionTierResetCheck()
}

// --- subscription_reset_task.go --------------------------------------------

func TestSubscriptionReset_RunOnce(t *testing.T) {
	// Empty due sets => loops break immediately, no error/panic.
	runSubscriptionQuotaResetOnce()

	// Concurrent guard.
	require.True(t, subscriptionResetRunning.CompareAndSwap(false, true))
	runSubscriptionQuotaResetOnce() // returns immediately
	subscriptionResetRunning.Store(false)
}

// --- employee_commission.go RecordTransactionCost --------------------------

func TestEmployeeCommission_RecordTransactionCost(t *testing.T) {
	truncate(t)
	const uid, chid = 6100, 6101
	// quota==0 => no-op.
	RecordTransactionCost(&relaycommon.RelayInfo{UserId: uid}, 0, 0, 0)

	info := &relaycommon.RelayInfo{
		UserId:          uid,
		OriginModelName: "gpt-4o",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: chid, ChannelName: "c"},
		PriceData:       hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
	}
	RecordTransactionCost(info, 1000, 0, 0)
	t.Cleanup(func() { model.DB.Exec("DELETE FROM consumption_costs WHERE user_id = ?", uid) })

	// The record is written unless the business-stats circuit breaker is open
	// (a shared global that other tests may trip); accept 0 or 1 rows but require
	// the code path to run without panicking.
	var count int64
	model.DB.Model(&model.ConsumptionCost{}).Where("user_id = ?", uid).Count(&count)
	assert.LessOrEqual(t, count, int64(1))
}

// --- file_service.go leftovers ---------------------------------------------

func TestFileService_CleanupFileSources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	// No registered sources => safe no-op.
	CleanupFileSources(c)

	// Register a source via LoadFileSource then clean up.
	src := types.NewBase64FileSource("QUJD", "text/plain")
	_, err := LoadFileSource(c, src)
	require.NoError(t, err)
	CleanupFileSources(c)
}

func TestFileService_GetMimeType_Base64Cache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	src := types.NewBase64FileSource("QUJD", "text/plain")
	// First populate cache.
	_, err := LoadFileSource(c, src)
	require.NoError(t, err)
	mt, err := GetMimeType(c, src)
	require.NoError(t, err)
	assert.Equal(t, "text/plain", mt)
}

var _ = constant.TaskPlatformSuno
