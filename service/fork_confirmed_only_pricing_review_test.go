package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

// These pure calculation cases exercise the policy after the final expression
// branch is known. They never reserve funds or write a database record.
func TestForkUndeliveredPricingRefund(t *testing.T) {
	for _, tc := range []struct {
		name, expression string
		estimate, input  int
		delivered        bool
		wantQuota        int
	}{
		{"token branch becomes fixed", `len <= 32000 ? tier("short", fixed(0.01)) : tier("long", p * 2)`, 50000, 100, false, 0},
		{"evaluation fails on confirmed input", `p == 50 ? tier("error", param("missing") * p) : tier("token", p * 2)`, 100, 50, false, 0},
		{"confirmed token branch", `tier("token", p * 2)`, 100, 50, false, 0},
		{"fixed reservation becomes confirmed tokens", `len <= 32000 ? tier("short", fixed(0.01)) : tier("long", p * 2)`, 100, 50000, false, 0},
		{"delivered token branch becomes fixed", `len <= 32000 ? tier("short", fixed(0.01)) : tier("long", p * 2)`, 50000, 100, true, 5000},
		{"delivered evaluation keeps prior fallback", `p == 50 ? tier("error", param("missing") * p) : tier("token", p * 2)`, 100, 50, true, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, trace, err := billingexpr.RunExpr(tc.expression, billingexpr.TokenParams{P: float64(tc.estimate), Len: float64(tc.estimate)})
			require.NoError(t, err)
			reservation := common.QuotaRound(cost / 1_000_000 * common.QuotaPerUnit)
			info := &relaycommon.RelayInfo{
				FinalPreConsumedQuota: reservation,
				TieredBillingSnapshot: &billingexpr.BillingSnapshot{
					BillingMode: "tiered_expr", ExprString: tc.expression, ExprHash: billingexpr.ExprHashString(tc.expression),
					GroupRatio: 1, QuotaPerUnit: common.QuotaPerUnit,
					EstimatedBillingUnit: trace.BillingUnit, EstimatedQuotaAfterGroup: reservation,
				},
				StreamResult: &relaycommon.StreamOutcome{Failed: true, ConfirmedUsage: true, EffectiveContent: tc.delivered},
			}
			info.StreamResult.Diagnostic.UsageEvidence = map[string]int{"input_tokens": tc.input}
			info.StreamResult.UsageSource = info.StreamResult.SelectUsageSource()
			quota := 0

			if info.StreamResult.UsageSource == "upstream" {
				usage := &dto.Usage{PromptTokens: tc.input, TotalTokens: tc.input}
				ok, calculated, result := TryTieredSettle(info, BuildTieredTokenParams(usage, false, billingexpr.UsedVars(tc.expression)))
				require.True(t, ok)
				quota = calculated
				if result != nil {
					require.Equal(t, tc.wantQuota, result.ActualQuotaAfterGroup)
					require.Equal(t, float64(tc.wantQuota), result.ActualQuotaBeforeGroup)
				}
			}
			require.Equal(t, tc.wantQuota, quota)
			if tc.wantQuota == 0 {
				require.Equal(t, "none", info.StreamResult.UsageSource)
			} else {
				require.Equal(t, "upstream", info.StreamResult.UsageSource)
			}
		})
	}
}
