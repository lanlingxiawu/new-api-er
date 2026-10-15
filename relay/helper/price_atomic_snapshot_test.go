package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestTieredPricingKeepsCapturedExpressionAcrossModeChange(t *testing.T) {
	loadTieredConfig(t, `{"atomic-relay-model":"tiered_expr"}`, `{"atomic-relay-model":"tier(\"original\", p*2)"}`, `{"default":1}`)
	mode, expression, ok := billing_setting.GetModelBilling("atomic-relay-model")
	require.Equal(t, billing_setting.BillingModeTieredExpr, mode)
	require.True(t, ok)
	// Simulate an administrator deleting the expression after mode selection.
	require.NoError(t, config.GlobalConfig.UpdateFromMap("billing_setting", map[string]string{"billing_mode": `{"atomic-relay-model":"ratio"}`, "billing_expr": `{}`}))
	c, info, _ := newPriceContext("default")
	info.OriginModelName = "atomic-relay-model"
	info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(`{}`)}
	_, err := modelPriceHelperTiered(c, info, info.OriginModelName, 1000, &types.TokenCountMeta{MaxTokens: 1}, HandleGroupRatio(c, info), expression)
	require.NoError(t, err)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, expression, info.TieredBillingSnapshot.ExprString)
	require.Equal(t, "original", info.TieredBillingSnapshot.EstimatedTier)
}
