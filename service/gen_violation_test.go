package service

import (
	"errors"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// violation_fee.go — CSAM violation-fee detection/normalization (pure) plus
// the DB-free guard branches of ChargeViolationFeeIfNeeded.
// ===========================================================================

func TestViol_IsViolationFeeCode(t *testing.T) {
	assert.True(t, IsViolationFeeCode(types.ErrorCodeViolationFeeGrokCSAM))
	assert.True(t, IsViolationFeeCode(types.ErrorCode("violation_fee.anything")))
	assert.False(t, IsViolationFeeCode(types.ErrorCodeModelPriceError))
	assert.False(t, IsViolationFeeCode(types.ErrorCode("")))
}

func TestViol_HasCSAMViolationMarker(t *testing.T) {
	assert.False(t, HasCSAMViolationMarker(nil))

	csam := types.NewError(errors.New("Failed check: SAFETY_CHECK_TYPE=CSAM"), types.ErrorCodeModelPriceError)
	assert.True(t, HasCSAMViolationMarker(csam))

	guidelines := types.NewError(errors.New("Content violates usage guidelines"), types.ErrorCodeModelPriceError)
	assert.True(t, HasCSAMViolationMarker(guidelines))

	benign := types.NewError(errors.New("upstream timeout"), types.ErrorCodeModelPriceError)
	assert.False(t, HasCSAMViolationMarker(benign))
}

func TestViol_WrapAsViolationFeeGrokCSAM(t *testing.T) {
	assert.Nil(t, WrapAsViolationFeeGrokCSAM(nil))

	in := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeModelPriceError, 456)
	out := WrapAsViolationFeeGrokCSAM(in)
	require.NotNil(t, out)
	assert.Equal(t, types.ErrorCodeViolationFeeGrokCSAM, out.GetErrorCode())
	assert.Equal(t, 456, out.StatusCode, "status code preserved")
}

func TestViol_NormalizeViolationFeeError(t *testing.T) {
	assert.Nil(t, NormalizeViolationFeeError(nil))

	// CSAM marker -> wrapped to grok csam code
	csam := types.NewError(errors.New("Failed check: SAFETY_CHECK_TYPE"), types.ErrorCodeModelPriceError)
	assert.Equal(t, types.ErrorCodeViolationFeeGrokCSAM, NormalizeViolationFeeError(csam).GetErrorCode())

	// already a violation-fee code (no marker) -> code preserved, skip-retry set
	pre := types.NewError(errors.New("no marker"), types.ErrorCode("violation_fee.custom"))
	got := NormalizeViolationFeeError(pre)
	assert.Equal(t, types.ErrorCode("violation_fee.custom"), got.GetErrorCode())

	// benign -> returned unchanged
	benign := types.NewError(errors.New("timeout"), types.ErrorCodeModelPriceError)
	assert.Same(t, benign, NormalizeViolationFeeError(benign))
}

func TestViol_ShouldChargeViolationFee(t *testing.T) {
	assert.False(t, shouldChargeViolationFee(nil))

	byCode := types.NewError(errors.New("x"), types.ErrorCodeViolationFeeGrokCSAM)
	assert.True(t, shouldChargeViolationFee(byCode))

	byMarker := types.NewError(errors.New("Content violates usage guidelines"), types.ErrorCodeModelPriceError)
	assert.True(t, shouldChargeViolationFee(byMarker), "safety net matches marker even without normalized code")

	benign := types.NewError(errors.New("ok"), types.ErrorCodeModelPriceError)
	assert.False(t, shouldChargeViolationFee(benign))
}

func TestViol_CalcViolationFeeQuota(t *testing.T) {
	// amount<=0 or groupRatio<=0 -> 0
	assert.Equal(t, 0, calcViolationFeeQuota(0, 1))
	assert.Equal(t, 0, calcViolationFeeQuota(-1, 1))
	assert.Equal(t, 0, calcViolationFeeQuota(1, 0))
	assert.Equal(t, 0, calcViolationFeeQuota(1, -1))

	// amount 2 * QuotaPerUnit * groupRatio 1.5 (QuotaPerUnit default 500000)
	want := int(2 * 500000 * 1.5)
	assert.Equal(t, want, calcViolationFeeQuota(2, 1.5))
}

func TestViol_ChargeViolationFeeIfNeeded_GuardBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{}
	apiErr := types.NewError(errors.New("Failed check: SAFETY_CHECK_TYPE"), types.ErrorCodeViolationFeeGrokCSAM)

	// nil args -> false (no DB touched)
	assert.False(t, ChargeViolationFeeIfNeeded(nil, info, apiErr))
	assert.False(t, ChargeViolationFeeIfNeeded(c, nil, apiErr))
	assert.False(t, ChargeViolationFeeIfNeeded(c, info, nil))

	// benign error -> not charged
	benign := types.NewError(errors.New("timeout"), types.ErrorCodeModelPriceError)
	assert.False(t, ChargeViolationFeeIfNeeded(c, info, benign))
}
