/*
Adversarial billing tests for the third-party SD2 task channel.

Goal: verify the billing-overflow hardening (int32 saturation) actually reaches
the thirdpartysd2 path, and probe for any residual negative-charge / overflow
problem. See AGENTS.md "Billing safety invariants".

  - Settlement (the real charge) recomputes quota from the UPSTREAM-reported
    token count in service.RecalculateTaskQuotaByTokens:
        actualQuota, clamp := common.QuotaFromFloatChecked(
            float64(totalTokens) * modelRatio * groupRatio * otherMultiplier)
    That function lives in the `service` package (needs a DB and currently does
    not compile because of an unrelated pre-existing test), so we exercise the
    exact protection primitive it uses — the real common.QuotaFromFloatChecked —
    with the identical formula.

  - Pre-consume uses model_setting.CalculateThirdPartySD2Quota with a FIXED
    token estimate; we test the real function directly.
*/
package model_setting

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// settleQuota mirrors the settlement math in service.RecalculateTaskQuotaByTokens.
func settleQuota(totalTokens int, modelRatio, groupRatio, otherMultiplier float64) (int, *common.QuotaClamp) {
	return common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * groupRatio * otherMultiplier)
}

// A hostile/buggy upstream can report an absurd totalTokens (Usage.TotalTokens).
// The settlement must saturate at the int32 quota bound, never overflow to a
// negative (credit) charge, and must record an auditable clamp.
func TestThirdPartySD2_Settlement_SaturatesOnHostileUpstreamTokens(t *testing.T) {
	modelRatio := ThirdPartySD2PriceToModelRatio(7.0) // realistic $7 / 1M tokens
	const groupRatio = 1.0

	for _, tokens := range []int{math.MaxInt32, math.MaxInt64 / 4, math.MaxInt64} {
		q, clamp := settleQuota(tokens, modelRatio, groupRatio, 1.0)

		require.Greaterf(t, q, 0, "settlement must never yield a <=0 charge for tokens=%d", tokens)
		require.Equalf(t, common.MaxQuota, q, "settlement must clamp at int32 MaxQuota for tokens=%d", tokens)
		require.NotNilf(t, clamp, "an overflow clamp must be recorded for auditing, tokens=%d", tokens)
		require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	}
}

// Even with an extra OtherRatios multiplier (video/duration discounts stacked
// the wrong way, or an inflated ratio), settlement stays bounded.
func TestThirdPartySD2_Settlement_SaturatesWithOtherMultiplier(t *testing.T) {
	modelRatio := ThirdPartySD2PriceToModelRatio(7.7)
	q, clamp := settleQuota(math.MaxInt64, modelRatio, 3.0 /*group*/, 5.0 /*other*/)
	require.Equal(t, common.MaxQuota, q)
	require.NotNil(t, clamp)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

// Demonstrates WHY the saturating conversion matters: the pre-fix raw cast
// int(product) diverges from (and is unsafe compared to) the bounded result.
func TestThirdPartySD2_Settlement_ContrastWithUnsafeRawCast(t *testing.T) {
	modelRatio := ThirdPartySD2PriceToModelRatio(7.0)
	const tokens = math.MaxInt64
	product := float64(tokens) * modelRatio * 1.0

	naive := int(product) // pre-fix behaviour (unsafe, implementation-defined on overflow)
	safe, _ := settleQuota(tokens, modelRatio, 1.0, 1.0)

	require.Equal(t, common.MaxQuota, safe) // fixed path: bounded & positive
	require.NotEqual(t, safe, naive)        // raw cast diverges from the safe value
	t.Logf("hostile tokens=%d -> raw int(product)=%d (UNSAFE)  vs  saturated=%d (SAFE)", tokens, naive, safe)
}

// Normal production pre-consume: fixed 1M-token estimate * admin price * group.
func TestThirdPartySD2_PreConsume_NormalCaseIsCorrectAndPositive(t *testing.T) {
	q := CalculateThirdPartySD2Quota(7.0, ThirdPartySD2PreConsumedTokenEstimate, 1.0)
	require.Equal(t, int(7.0*common.QuotaPerUnit*1.0), q)
	require.Greater(t, q, 0)
	require.LessOrEqual(t, q, common.MaxQuota) // within int32 range for a sane price
}

// Guard for the advisory's actual attack surface: with the FIXED token estimate
// the real caller always passes, pre-consume must never go negative across the
// full range of *realistic* admin prices and group ratios.
func TestThirdPartySD2_PreConsume_NeverNegative_ForRealisticConfig(t *testing.T) {
	for _, price := range []float64{0.1, 1, 7, 100, 1000} {
		for _, group := range []float64{0.1, 1, 5, 20} {
			q := CalculateThirdPartySD2Quota(price, ThirdPartySD2PreConsumedTokenEstimate, group)
			require.GreaterOrEqualf(t, q, 0, "pre-consume negative for price=%g group=%g", price, group)
		}
	}
}

// DIAGNOSTIC (residual gap): pre-consume uses a raw int(math.Round(...)) cast
// with NO int32 saturation, unlike settlement. It is not reachable via request
// params (tokenCount is a fixed constant), only by an admin misconfiguring the
// price. This test pins that current, unclamped behaviour so the gap is visible
// and a future saturation fix will surface here.
func TestThirdPartySD2_PreConsume_IsNotInt32Clamped_ResidualGap(t *testing.T) {
	const absurdPrice = 2_000_000.0 // $2M / 1M tokens (admin misconfig)
	q := CalculateThirdPartySD2Quota(absurdPrice, ThirdPartySD2PreConsumedTokenEstimate, 1.0)

	saturated, clamp := common.QuotaFromFloatChecked(
		float64(ThirdPartySD2PreConsumedTokenEstimate) * ThirdPartySD2PriceToModelRatio(absurdPrice) * 1.0)

	t.Logf("pre-consume raw=%d  |  saturating-equivalent=%d (clamp=%v)  |  int32 MaxQuota=%d",
		q, saturated, clamp != nil, common.MaxQuota)

	require.Greater(t, q, common.MaxQuota,
		"pre-consume is currently NOT clamped to int32 (raw cast) — this is the residual gap")
	require.Equal(t, common.MaxQuota, saturated,
		"a saturating conversion would have bounded it")
}
