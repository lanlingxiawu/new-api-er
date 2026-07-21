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
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test scaffolding
// ---------------------------------------------------------------------------

// snapshotRatioSettings saves and restores the in-memory ratio/price maps so
// each test starts from a clean, isolated pricing table.
func snapshotRatioSettings(t *testing.T) {
	t.Helper()
	savedRatio := ratio_setting.ModelRatio2JSONString()
	savedPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatio))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrice))
	})
}

// setFreeModelPreConsume flips the free-model pre-consume gate and restores it.
func setFreeModelPreConsume(t *testing.T, enabled bool) {
	t.Helper()
	q := operation_setting.GetQuotaSetting()
	old := q.EnableFreeModelPreConsume
	q.EnableFreeModelPreConsume = enabled
	t.Cleanup(func() { q.EnableFreeModelPreConsume = old })
}

func newPriceContext(group string) (*gin.Context, *relaycommon.RelayInfo, string) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", group)
	info := &relaycommon.RelayInfo{
		OriginModelName: "",
		UserGroup:       group,
		UsingGroup:      group,
	}
	return ctx, info, group
}

func init() { gin.SetMode(gin.TestMode) }

// ---------------------------------------------------------------------------
// ModelPriceHelper — ratio path (usePrice=false)
// ---------------------------------------------------------------------------

func TestModelPriceHelper_RatioPath(t *testing.T) {
	snapshotRatioSettings(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"ratio-model":2}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":3}`))

	tests := []struct {
		name         string
		group        string
		promptTokens int
		maxTokens    int
		wantQuota    int
	}{
		// preConsumedTokens = max(prompt, 500) + maxTokens; quota = tokens*ratio*groupRatio
		{"prompt above floor no maxtokens", "default", 1000, 0, 2000},
		{"prompt above floor with maxtokens", "default", 1000, 100, 2200},
		{"prompt below floor clamps to 500", "default", 100, 0, 1000},
		{"prompt exactly at floor", "default", 500, 0, 1000},
		{"group ratio multiplies", "vip", 1000, 0, 6000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, info, _ := newPriceContext(tt.group)
			info.OriginModelName = "ratio-model"
			pd, err := ModelPriceHelper(ctx, info, tt.promptTokens, &types.TokenCountMeta{MaxTokens: tt.maxTokens})
			require.NoError(t, err)
			require.False(t, pd.UsePrice)
			require.Equal(t, 2.0, pd.ModelRatio)
			require.Equal(t, tt.wantQuota, pd.QuotaToPreConsume)
			require.Equal(t, pd, info.PriceData)
		})
	}
}

// ---------------------------------------------------------------------------
// ModelPriceHelper — fixed price path (usePrice=true)
// ---------------------------------------------------------------------------

func TestModelPriceHelper_FixedPricePath(t *testing.T) {
	snapshotRatioSettings(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"price-model":0.04}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	ctx, info, _ := newPriceContext("default")
	info.OriginModelName = "price-model"
	// 0.04 * 500000 * 1 = 20000
	pd, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.True(t, pd.UsePrice)
	require.Equal(t, 0.04, pd.ModelPrice)
	require.Equal(t, 20000, pd.QuotaToPreConsume)
}

func TestModelPriceHelper_FixedPriceWithImageRatioAndBillingRatios(t *testing.T) {
	snapshotRatioSettings(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"img-model":0.04}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	ctx, info, _ := newPriceContext("default")
	info.OriginModelName = "img-model"
	meta := &types.TokenCountMeta{
		ImagePriceRatio: 3,                          // modelPrice *= 3 -> 0.12
		BillingRatios:   map[string]float64{"n": 3}, // applied via ApplyOtherRatiosToFloat
	}
	// (0.04*3) * 500000 * 1 * 3 = 180000
	pd, err := ModelPriceHelper(ctx, info, 1000, meta)
	require.NoError(t, err)
	require.True(t, pd.UsePrice)
	require.Equal(t, 180000, pd.QuotaToPreConsume)
	require.True(t, pd.HasOtherRatio("n"))
}

// ---------------------------------------------------------------------------
// ModelPriceHelper — accept-unset-ratio branch and not-configured error
// ---------------------------------------------------------------------------

func TestModelPriceHelper_UnsetRatioModel(t *testing.T) {
	snapshotRatioSettings(t)
	// Ensure the model is absent from both price and ratio maps.
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	t.Run("rejected when user does not accept unset ratio", func(t *testing.T) {
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "never-configured-model"
		info.UserId = 0 // non-admin path, no DB
		_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "never-configured-model")
	})

	t.Run("accepted when user opts into unset ratio (default 37.5)", func(t *testing.T) {
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "never-configured-model"
		info.UserSetting.AcceptUnsetRatioModel = true
		// preConsumedTokens = max(1000,500) = 1000; ratio = 37.5 * 1 = 37.5 -> 37500
		pd, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
		require.NoError(t, err)
		require.Equal(t, 37500, pd.QuotaToPreConsume)
	})
}

// ---------------------------------------------------------------------------
// ModelPriceHelper — free model gating (EnableFreeModelPreConsume=false)
// ---------------------------------------------------------------------------

func TestModelPriceHelper_FreeModelGating(t *testing.T) {
	snapshotRatioSettings(t)
	setFreeModelPreConsume(t, false)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"zero-ratio-model":0}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"zero-price-model":0}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"free":0}`))

	t.Run("group ratio zero marks free", func(t *testing.T) {
		ctx, info, _ := newPriceContext("free")
		info.OriginModelName = "zero-ratio-model"
		pd, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
		require.NoError(t, err)
		require.True(t, pd.FreeModel)
		require.Equal(t, 0, pd.QuotaToPreConsume)
	})

	t.Run("model ratio zero marks free", func(t *testing.T) {
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "zero-ratio-model"
		pd, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
		require.NoError(t, err)
		require.True(t, pd.FreeModel)
		require.Equal(t, 0, pd.QuotaToPreConsume)
	})

	t.Run("model price zero marks free", func(t *testing.T) {
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "zero-price-model"
		pd, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
		require.NoError(t, err)
		require.True(t, pd.FreeModel)
		require.Equal(t, 0, pd.QuotaToPreConsume)
	})
}

// ---------------------------------------------------------------------------
// ModelPriceHelper — int32 saturation on the ratio path
// ---------------------------------------------------------------------------

func TestModelPriceHelper_RatioOverflowRejected(t *testing.T) {
	snapshotRatioSettings(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"overflow-ratio-model":1000000000000}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	ctx, info, _ := newPriceContext("default")
	info.OriginModelName = "overflow-ratio-model"
	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaFromFloat", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

func TestModelPriceHelper_FixedPriceOverflowRejected(t *testing.T) {
	snapshotRatioSettings(t)
	huge := float64(common.MaxQuota) / common.QuotaPerUnit / 2 // *2 later -> overflow
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	m, err := common.Marshal(map[string]float64{"overflow-price-model": huge})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(m)))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	ctx, info, _ := newPriceContext("default")
	info.OriginModelName = "overflow-price-model"
	meta := &types.TokenCountMeta{BillingRatios: map[string]float64{"n": 3}}
	_, err = ModelPriceHelper(ctx, info, 0, meta)
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaFromFloat", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

// ---------------------------------------------------------------------------
// HandleGroupRatio
// ---------------------------------------------------------------------------

func TestHandleGroupRatio(t *testing.T) {
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"default":{"vip":1.5}}`))
	ratio_setting.SetUserExclusiveGroupRatioEnabled(false)
	t.Cleanup(func() { ratio_setting.SetUserExclusiveGroupRatioEnabled(false) })

	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		return c
	}

	t.Run("default ratio when nothing set", func(t *testing.T) {
		info := HandleGroupRatio(newCtx(), &relaycommon.RelayInfo{UserGroup: "default", UsingGroup: "default"})
		require.Equal(t, 1.0, info.GroupRatio)
		require.False(t, info.HasSpecialRatio)
		require.Equal(t, float64(-1), info.GroupSpecialRatio)
	})

	t.Run("auto_group overrides using group", func(t *testing.T) {
		c := newCtx()
		c.Set("auto_group", "vip")
		info := &relaycommon.RelayInfo{UserGroup: "default", UsingGroup: "default"}
		res := HandleGroupRatio(c, info)
		require.Equal(t, "vip", info.UsingGroup)
		// user=default, using=vip -> group-group ratio default->vip = 1.5
		require.Equal(t, 1.5, res.GroupRatio)
	})

	t.Run("user exclusive ratio wins when enabled", func(t *testing.T) {
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

	t.Run("flag off falls through to group-group ratio", func(t *testing.T) {
		ratio_setting.SetUserExclusiveGroupRatioEnabled(false)
		info := HandleGroupRatio(newCtx(), &relaycommon.RelayInfo{
			UserGroup:       "default",
			UsingGroup:      "vip",
			UserGroupRatios: map[string]float64{"vip": 0.5},
		})
		require.Equal(t, 1.5, info.GroupRatio)
		require.True(t, info.HasSpecialRatio)
	})
}

// ---------------------------------------------------------------------------
// ModelPriceHelperPerCall
// ---------------------------------------------------------------------------

func TestModelPriceHelperPerCall(t *testing.T) {
	snapshotRatioSettings(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"free":0}`))

	t.Run("explicit per-call price", func(t *testing.T) {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"percall-price":0.8}`))
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "percall-price"
		// 0.8 * 500000 * 1 = 400000
		pd, err := ModelPriceHelperPerCall(ctx, info)
		require.NoError(t, err)
		require.True(t, pd.UsePrice)
		require.Equal(t, 400000, pd.Quota)
		require.Equal(t, 0.8, pd.ModelPrice)
	})

	t.Run("falls back to default price map", func(t *testing.T) {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "suno_music" // present in defaultModelPrice (0.1)
		// 0.1 * 500000 * 1 = 50000
		pd, err := ModelPriceHelperPerCall(ctx, info)
		require.NoError(t, err)
		require.True(t, pd.UsePrice)
		require.Equal(t, 50000, pd.Quota)
	})

	t.Run("ratio fallback uses half model ratio", func(t *testing.T) {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"percall-ratio":10}`))
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "percall-ratio"
		// modelRatio/2 * 500000 * 1 = 10/2*500000 = 2500000
		pd, err := ModelPriceHelperPerCall(ctx, info)
		require.NoError(t, err)
		require.False(t, pd.UsePrice)
		require.Equal(t, float64(-1), pd.ModelPrice)
		require.Equal(t, 2500000, pd.Quota)
	})

	t.Run("unconfigured non-admin rejected", func(t *testing.T) {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "never-ever-model"
		info.UserId = 0
		_, err := ModelPriceHelperPerCall(ctx, info)
		require.Error(t, err)
		require.Contains(t, err.Error(), "never-ever-model")
	})

	t.Run("free group zeroes quota when gate disabled", func(t *testing.T) {
		setFreeModelPreConsume(t, false)
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"percall-free":0.8}`))
		ctx, info, _ := newPriceContext("free")
		info.OriginModelName = "percall-free"
		pd, err := ModelPriceHelperPerCall(ctx, info)
		require.NoError(t, err)
		require.True(t, pd.FreeModel)
		require.Equal(t, 0, pd.Quota)
	})

	t.Run("ratio fallback free group zeroes quota when gate disabled", func(t *testing.T) {
		setFreeModelPreConsume(t, false)
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"percall-ratio-free":10}`))
		ctx, info, _ := newPriceContext("free")
		info.OriginModelName = "percall-ratio-free"
		pd, err := ModelPriceHelperPerCall(ctx, info)
		require.NoError(t, err)
		require.True(t, pd.FreeModel)
		require.Equal(t, 0, pd.Quota)
	})
}

// ---------------------------------------------------------------------------
// HasModelBillingConfig
// ---------------------------------------------------------------------------

func TestHasModelBillingConfig(t *testing.T) {
	snapshotRatioSettings(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"has-price":0.04}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"has-ratio":2}`))

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"has-expr":"tiered_expr","empty-expr":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"has-expr":"tier(\"b\", p*2)","empty-expr":"   "}`,
	}))
	// sanity: mode resolved
	require.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("has-expr"))

	require.True(t, HasModelBillingConfig("has-price"))
	require.True(t, HasModelBillingConfig("has-ratio"))
	require.True(t, HasModelBillingConfig("has-expr"))
	require.False(t, HasModelBillingConfig("empty-expr"), "tiered mode with blank expr is not configured")
	require.False(t, HasModelBillingConfig("nothing-configured"))
}

// ---------------------------------------------------------------------------
// modelPriceHelperTiered (via ModelPriceHelper)
// ---------------------------------------------------------------------------

func loadTieredConfig(t *testing.T, mode, expr, groupRatio string) {
	t.Helper()
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
	cfg := map[string]string{
		"billing_setting.billing_mode": mode,
		"billing_setting.billing_expr": expr,
	}
	if groupRatio != "" {
		cfg["group_ratio_setting.group_ratio"] = groupRatio
	}
	require.NoError(t, config.GlobalConfig.LoadFromDB(cfg))
}

func TestModelPriceHelperTiered_UsesPreloadedRequestInput(t *testing.T) {
	loadTieredConfig(t,
		`{"tiered-test-model":"tiered_expr"}`,
		`{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
		"")

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
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

	// p=1000, stream=true -> p*3 = 3000; 3000/1e6*500000 = 1500
	pd, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, 1500, pd.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)
	require.Equal(t, info.OriginModelName, info.TieredBillingSnapshot.ModelName)
	require.NotNil(t, info.BillingRequestInput)
}

func TestModelPriceHelperTiered_MaxTokensFallback(t *testing.T) {
	loadTieredConfig(t,
		`{"tiered-fallback-model":"tiered_expr"}`,
		`{"tiered-fallback-model":"tier(\"base\", p * 3 + c * 15)"}`,
		`{"default":1,"free":0}`)

	const promptTokens = 1000
	cases := []struct {
		name      string
		group     string
		maxTokens int
		expected  int
	}{
		// max_tokens omitted, paid group -> fall back to 8192 completion tokens
		// p*3 + c*15 = 3000 + 122880 = 125880 -> /1e6*500000 = 62940
		{"non-free falls back to 8192", "default", 0, 62940},
		// explicit max_tokens verbatim: 3000 + 1500 = 4500 -> 2250
		{"explicit max_tokens verbatim", "default", 100, 2250},
		// free group stays zero
		{"free group stays zero", "free", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("Content-Type", "application/json")
			ctx.Request = req
			ctx.Set("group", tc.group)
			info := &relaycommon.RelayInfo{
				OriginModelName: "tiered-fallback-model",
				UserGroup:       tc.group,
				UsingGroup:      tc.group,
				RequestHeaders:  map[string]string{"Content-Type": "application/json"},
				BillingRequestInput: &billingexpr.RequestInput{
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    []byte(`{}`),
				},
			}
			pd, err := ModelPriceHelper(ctx, info, promptTokens, &types.TokenCountMeta{MaxTokens: tc.maxTokens})
			require.NoError(t, err)
			require.Equal(t, tc.expected, pd.QuotaToPreConsume)
		})
	}
}

func TestModelPriceHelperTiered_OverflowRejected(t *testing.T) {
	loadTieredConfig(t,
		`{"tiered-overflow-model":"tiered_expr"}`,
		`{"tiered-overflow-model":"tier(\"overflow\", p * 1000000000000000)"}`,
		`{"default":1}`)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName:     "tiered-overflow-model",
		UserGroup:           "default",
		UsingGroup:          "default",
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`)},
	}
	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaRound", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

func TestModelPriceHelperTiered_MissingExpr(t *testing.T) {
	// Model marked tiered_expr but no expression configured.
	loadTieredConfig(t,
		`{"tiered-noexpr-model":"tiered_expr"}`,
		`{}`,
		`{"default":1}`)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName:     "tiered-noexpr-model",
		UserGroup:           "default",
		UsingGroup:          "default",
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`)},
	}
	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no billing expression")
}

func TestModelPriceHelperTiered_RunError(t *testing.T) {
	// Missing param() path forces a runtime error inside the expression.
	loadTieredConfig(t,
		`{"tiered-runerr-model":"tiered_expr"}`,
		`{"tiered-runerr-model":"p * param(\"missing\")"}`,
		`{"default":1}`)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName:     "tiered-runerr-model",
		UserGroup:           "default",
		UsingGroup:          "default",
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`)},
	}
	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tiered expr run failed")
}
