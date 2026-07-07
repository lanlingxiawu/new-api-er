package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperTieredUsesPreloadedRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-test-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
	req.Body = nil
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"stream":true}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, 1500, priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)
}

// TestHandleGroupRatioUserExclusive verifies the billing resolution point:
// per-user exclusive ratio wins when enabled, and any degrade path yields the
// exact same result as with the feature disabled (Rule 0 / design §7.2).
func TestHandleGroupRatioUserExclusive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"default":{"vip":1.5}}`))
	ratio_setting.SetUserExclusiveGroupRatioEnabled(false)
	defer ratio_setting.SetUserExclusiveGroupRatioEnabled(false)

	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		return c
	}

	t.Run("flag on: exclusive ratio applied and marked special", func(t *testing.T) {
		ratio_setting.SetUserExclusiveGroupRatioEnabled(true)
		info := HandleGroupRatio(newCtx(), &relaycommon.RelayInfo{
			UserGroup:       "default",
			UsingGroup:      "vip",
			UserGroupRatios: map[string]float64{"vip": 0.5},
		})
		require.Equal(t, 0.5, info.GroupRatio)
		require.True(t, info.HasSpecialRatio)
		require.Equal(t, 0.5, info.GroupSpecialRatio)
	})

	t.Run("flag off: exclusive ignored, group-group ratio used", func(t *testing.T) {
		ratio_setting.SetUserExclusiveGroupRatioEnabled(false)
		info := HandleGroupRatio(newCtx(), &relaycommon.RelayInfo{
			UserGroup:       "default",
			UsingGroup:      "vip",
			UserGroupRatios: map[string]float64{"vip": 0.5},
		})
		require.Equal(t, 1.5, info.GroupRatio)
		require.True(t, info.HasSpecialRatio)
	})

	t.Run("degrade: flag on with nil map equals flag off", func(t *testing.T) {
		ratio_setting.SetUserExclusiveGroupRatioEnabled(false)
		off := HandleGroupRatio(newCtx(), &relaycommon.RelayInfo{
			UserGroup: "default", UsingGroup: "vip",
		})
		ratio_setting.SetUserExclusiveGroupRatioEnabled(true)
		on := HandleGroupRatio(newCtx(), &relaycommon.RelayInfo{
			UserGroup: "default", UsingGroup: "vip",
		})
		require.Equal(t, off.GroupRatio, on.GroupRatio)
		require.Equal(t, off.HasSpecialRatio, on.HasSpecialRatio)
		require.Equal(t, off.GroupSpecialRatio, on.GroupSpecialRatio)
	})
}
