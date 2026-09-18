package model_setting

import (
	"math"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sd2Model     = "dreamina-seedance-2-0-260128"
	sd2FastModel = "dreamina-seedance-2-0-fast-260128"
)

// withSD2Matrix swaps the configured overlay matrix, rebuilds the atomic index,
// and restores both on cleanup. Every read path goes through the rebuilt index.
func withSD2Matrix(t *testing.T, m ThirdPartySD2PricingMatrix) {
	t.Helper()
	original := cloneThirdPartySD2PricingMatrix(thirdPartySD2PricingSettings.Matrix)
	t.Cleanup(func() {
		thirdPartySD2PricingSettings.Matrix = original
		RebuildThirdPartySD2PricingIndex()
	})
	thirdPartySD2PricingSettings.Matrix = m
	RebuildThirdPartySD2PricingIndex()
}

// withSD2MatrixJSON applies an overlay exactly the way a save does: validate
// the administrator's JSON, decode it into the settings struct, rebuild.
func withSD2MatrixJSON(t *testing.T, raw string) {
	t.Helper()
	require.NoError(t, ValidateThirdPartySD2PricingMatrixJSON(raw))
	var matrix ThirdPartySD2PricingMatrix
	require.NoError(t, common.UnmarshalJsonStr(raw, &matrix))
	withSD2Matrix(t, matrix)
}

func sd2Override(noVideo, withVideo float64) *ThirdPartySD2ResolutionPricing {
	return &ThirdPartySD2ResolutionPricing{NoVideo: &noVideo, WithVideo: &withVideo}
}

func sd2Float(value float64) *float64 {
	return &value
}

// sd2TierCost prices one tier with the model's generated expression.
func sd2TierCost(t *testing.T, modelName, resolution, videoInput string, tokens float64) (float64, string) {
	t.Helper()
	expression, ok := GetThirdPartySD2BillingExpr(modelName)
	require.True(t, ok, "model %s has a billing expression", modelName)
	cost, trace, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{
		Usage: map[string]any{"tokens": tokens, "output_resolution": resolution, "video_input": videoInput},
	})
	require.NoError(t, err)
	return cost, trace.MatchedTier
}

// ---------------------------------------------------------------------------
// NormalizeThirdPartySD2Resolution — every path
// ---------------------------------------------------------------------------

func TestNormalizeThirdPartySD2Resolution(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// direct switch hits
		{"480p literal", "480p", "480p"},
		{"720p literal", "720p", "720p"},
		{"1080p literal", "1080p", "1080p"},
		{"4k literal", "4k", "4k"},
		{"2160p aliases 4k", "2160p", "4k"},
		// case-insensitive + whitespace stripping
		{"uppercase", "720P", "720p"},
		{"padded and spaced", " 1080 P ", "1080p"},
		// "<n>p" suffix parse -> classify
		{"1440p -> 1080p tier", "1440p", "1080p"},
		{"900p -> 720p tier", "900p", "720p"},
		{"600p -> 480p tier", "600p", "480p"},
		{"3000p -> 4k tier", "3000p", "4k"},
		{"0p -> classify default empty", "0p", ""},
		{"non-numeric p, no x -> empty", "abcp", ""},
		// "WxH" parse
		{"landscape 1920x1080 uses height", "1920x1080", "1080p"},
		{"portrait 1080x1920 uses width", "1080x1920", "1080p"},
		{"1280x720", "1280x720", "720p"},
		{"square 4096x4096 -> 4k", "4096x4096", "4k"},
		// invalid WxH
		{"width not a number", "axb", ""},
		{"height not a number", "100xb", ""},
		{"zero width", "0x100", ""},
		{"zero height", "100x0", ""},
		// no p suffix, no x separator
		{"garbage", "foo", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeThirdPartySD2Resolution(tt.in))
		})
	}
}

// classifyThirdPartySD2PricingResolution boundaries (via the "<n>p" path).
func TestClassifyResolutionBoundaries(t *testing.T) {
	// height is exercised through NormalizeThirdPartySD2Resolution("<n>p").
	cases := map[string]string{
		"2160p": "4k",    // >= 2160 boundary (also aliased earlier, use 2161p)
		"2161p": "4k",    // just above 2160
		"2159p": "1080p", // just below 2160
		"1080p": "1080p", // >= 1080 boundary (literal switch, still 1080p)
		"1081p": "1080p",
		"1079p": "720p", // just below 1080
		"720p":  "720p", // literal
		"721p":  "720p",
		"719p":  "480p", // just below 720
		"1p":    "480p", // > 0
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, NormalizeThirdPartySD2Resolution(in))
		})
	}
}

// ---------------------------------------------------------------------------
// thirdPartySD2ResolutionRank + MaxThirdPartySD2Resolution
// ---------------------------------------------------------------------------

func TestThirdPartySD2ResolutionRank(t *testing.T) {
	assert.Equal(t, 1, thirdPartySD2ResolutionRank("480p"))
	assert.Equal(t, 2, thirdPartySD2ResolutionRank("720p"))
	assert.Equal(t, 3, thirdPartySD2ResolutionRank("1080p"))
	assert.Equal(t, 4, thirdPartySD2ResolutionRank("4k"))
	assert.Equal(t, -1, thirdPartySD2ResolutionRank("unknown"))
	assert.Equal(t, -1, thirdPartySD2ResolutionRank(""))
}

func TestMaxThirdPartySD2Resolution(t *testing.T) {
	assert.Equal(t, "1080p", MaxThirdPartySD2Resolution("480p", "1920x1080"))
	assert.Equal(t, "4k", MaxThirdPartySD2Resolution("2160p", "1080p"))
	assert.Equal(t, "720p", MaxThirdPartySD2Resolution("", "1280x720"))
	// order independence
	assert.Equal(t, "4k", MaxThirdPartySD2Resolution("4k", "480p", "720p"))
}

func TestMaxThirdPartySD2Resolution_AllInvalidReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", MaxThirdPartySD2Resolution("garbage", "", "xyz"))
}

func TestMaxThirdPartySD2Resolution_NoArgs(t *testing.T) {
	assert.Equal(t, "", MaxThirdPartySD2Resolution())
}

// ---------------------------------------------------------------------------
// ThirdPartySD2PriceToModelRatio + CalculateThirdPartySD2Quota
// ---------------------------------------------------------------------------

func TestThirdPartySD2PriceToModelRatio(t *testing.T) {
	// 4.7 * 500000 / 1_000_000 = 2.35
	assert.InDelta(t, 2.35, ThirdPartySD2PriceToModelRatio(4.7), 1e-9)
	// non-positive -> 0 (boundary)
	assert.Equal(t, 0.0, ThirdPartySD2PriceToModelRatio(0))
	assert.Equal(t, 0.0, ThirdPartySD2PriceToModelRatio(-1))
}

func TestCalculateThirdPartySD2Quota(t *testing.T) {
	// happy path: round(tokenCount * modelRatio * groupRatio)
	want := int(math.Round(1_000_000 * ThirdPartySD2PriceToModelRatio(4.7) * 1.5))
	assert.Equal(t, want, CalculateThirdPartySD2Quota(4.7, 1_000_000, 1.5))

	// each guard independently forces 0 (condition coverage)
	assert.Equal(t, 0, CalculateThirdPartySD2Quota(0, 1_000_000, 1.5))  // price<=0
	assert.Equal(t, 0, CalculateThirdPartySD2Quota(-1, 1_000_000, 1.5)) // price<0
	assert.Equal(t, 0, CalculateThirdPartySD2Quota(4.7, 0, 1.5))        // tokenCount<=0
	assert.Equal(t, 0, CalculateThirdPartySD2Quota(4.7, -5, 1.5))       // tokenCount<0
	assert.Equal(t, 0, CalculateThirdPartySD2Quota(4.7, 1_000_000, 0))  // groupRatio<=0
	assert.Equal(t, 0, CalculateThirdPartySD2Quota(4.7, 1_000_000, -1)) // groupRatio<0
}

func TestCalculateThirdPartySD2Quota_Rounds(t *testing.T) {
	// pick numbers where rounding matters: half token count -> half quota, rounded.
	got := CalculateThirdPartySD2Quota(4.7, 500_000, 1.5)
	want := int(math.Round(500_000 * ThirdPartySD2PriceToModelRatio(4.7) * 1.5))
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// GetThirdPartySD2TokenPrice — lookup, normalization, fallback
// ---------------------------------------------------------------------------

func TestGetThirdPartySD2TokenPrice_OverlayHit(t *testing.T) {
	withSD2Matrix(t, ThirdPartySD2PricingMatrix{
		sd2Model: {
			"1080p": sd2Override(9.9, 6.6),
		},
	})

	// overlay wins for the configured resolution
	price, ok := GetThirdPartySD2TokenPrice(sd2Model, "1080p", false)
	require.True(t, ok)
	assert.Equal(t, 9.9, price)

	// hasVideoInput selects WithVideo
	price, ok = GetThirdPartySD2TokenPrice(sd2Model, "1080p", true)
	require.True(t, ok)
	assert.Equal(t, 6.6, price)

	// unconfigured resolution on that model falls back to the merged default matrix
	price, ok = GetThirdPartySD2TokenPrice(sd2Model, "720p", true)
	require.True(t, ok)
	assert.Equal(t, 4.3, price)
}

func TestGetThirdPartySD2TokenPrice_NormalizesInputKeys(t *testing.T) {
	withSD2Matrix(t, ThirdPartySD2PricingMatrix{
		sd2FastModel: {
			"720P":  sd2Override(6.1, 3.6),
			"2160P": sd2Override(8.8, 5.5),
		},
	})

	price, ok := GetThirdPartySD2TokenPrice(sd2FastModel, "1280x720", false)
	require.True(t, ok)
	assert.Equal(t, 6.1, price)

	// "2160P" overlay key normalized to 4k; query "4k" hits it
	price, ok = GetThirdPartySD2TokenPrice(sd2FastModel, "4k", true)
	require.True(t, ok)
	assert.Equal(t, 5.5, price)
}

func TestGetThirdPartySD2TokenPrice_TrimsModelName(t *testing.T) {
	price, ok := GetThirdPartySD2TokenPrice("  "+sd2Model+"  ", "480p", false)
	require.True(t, ok)
	assert.Equal(t, 7.0, price)
}

func TestGetThirdPartySD2TokenPrice_ModelNotFound(t *testing.T) {
	price, ok := GetThirdPartySD2TokenPrice("no-such-model", "1080p", false)
	assert.False(t, ok)
	assert.Equal(t, 0.0, price)
}

func TestGetThirdPartySD2TokenPrice_ResolutionNotFound(t *testing.T) {
	// model exists in defaults but resolution not priced for it
	price, ok := GetThirdPartySD2TokenPrice(sd2FastModel, "4k", false)
	assert.False(t, ok)
	assert.Equal(t, 0.0, price)
}

func TestGetThirdPartySD2TokenPrice_NilIndexRebuilds(t *testing.T) {
	// Force the atomic index to nil; the getter must rebuild and still serve.
	original := currentThirdPartySD2PricingIndex.Load()
	t.Cleanup(func() {
		currentThirdPartySD2PricingIndex.Store(original)
		RebuildThirdPartySD2PricingIndex()
	})
	currentThirdPartySD2PricingIndex.Store(nil)

	price, ok := GetThirdPartySD2TokenPrice(sd2Model, "480p", false)
	require.True(t, ok)
	assert.Equal(t, 7.0, price)
	require.NotNil(t, currentThirdPartySD2PricingIndex.Load(), "index rebuilt")
}

// ---------------------------------------------------------------------------
// GetThirdPartySD2PricingMatrix — snapshot + nil-index fallback
// ---------------------------------------------------------------------------

func TestGetThirdPartySD2PricingMatrix_ReturnsDeepCopy(t *testing.T) {
	m := GetThirdPartySD2PricingMatrix()
	require.Contains(t, m, sd2Model)
	require.NotNil(t, m[sd2Model]["480p"].NoVideo)

	// mutating the returned copy must not affect subsequent reads
	*m[sd2Model]["480p"].NoVideo = 999
	m[sd2Model]["720p"] = nil
	m["injected"] = map[string]*ThirdPartySD2ResolutionPricing{}

	fresh := GetThirdPartySD2PricingMatrix()
	assert.NotContains(t, fresh, "injected")
	require.NotNil(t, fresh[sd2Model]["480p"])
	assert.Equal(t, 7.0, *fresh[sd2Model]["480p"].NoVideo)
	require.NotNil(t, fresh[sd2Model]["720p"])
}

func TestGetThirdPartySD2PricingMatrix_NilIndexFallsBackToDefaults(t *testing.T) {
	original := currentThirdPartySD2PricingIndex.Load()
	t.Cleanup(func() { currentThirdPartySD2PricingIndex.Store(original) })

	currentThirdPartySD2PricingIndex.Store(nil)
	m := GetThirdPartySD2PricingMatrix()
	assert.Contains(t, m, sd2Model)
}

func TestGetThirdPartySD2PricingMatrix_NilMatrixInsideIndexFallsBack(t *testing.T) {
	original := currentThirdPartySD2PricingIndex.Load()
	t.Cleanup(func() { currentThirdPartySD2PricingIndex.Store(original) })

	currentThirdPartySD2PricingIndex.Store(&thirdPartySD2PricingIndex{matrix: nil})
	m := GetThirdPartySD2PricingMatrix()
	assert.Contains(t, m, sd2Model)
}

// ---------------------------------------------------------------------------
// RebuildThirdPartySD2PricingIndex — overlay merge semantics
// ---------------------------------------------------------------------------

func TestRebuildIndex_OverlayMergesOntoDefaults(t *testing.T) {
	withSD2Matrix(t, ThirdPartySD2PricingMatrix{
		"brand-new-model": {"720p": sd2Override(1.1, 0.5)},
		sd2Model: {
			"480p": sd2Override(100, 50), // override one default entry
		},
	})
	m := GetThirdPartySD2PricingMatrix()

	// new model added
	assert.Equal(t, 1.1, *m["brand-new-model"]["720p"].NoVideo)
	// overridden entry
	assert.Equal(t, 100.0, *m[sd2Model]["480p"].NoVideo)
	// untouched default entry on the same model survives the merge
	assert.Equal(t, 7.7, *m[sd2Model]["1080p"].NoVideo)
}

// An override that only carries one of the two prices must keep the built-in
// price of the other. Decoding an absent price as 0 made every request at that
// resolution free and refunded the whole reservation on settlement.
func TestRebuildIndex_PartialOverrideKeepsBuiltinPrice(t *testing.T) {
	withSD2MatrixJSON(t, `{"`+sd2Model+`":{"1080p":{"with_video":5.0}}}`)

	noVideo, ok := GetThirdPartySD2TokenPrice(sd2Model, "1080p", false)
	require.True(t, ok)
	assert.Equal(t, 7.7, noVideo, "the built-in no_video price survives")
	withVideo, ok := GetThirdPartySD2TokenPrice(sd2Model, "1080p", true)
	require.True(t, ok)
	assert.Equal(t, 5.0, withVideo)

	// the generated expression bills the same prices
	cost, tier := sd2TierCost(t, sd2Model, "1080p", "none", 1_000_000)
	assert.InDelta(t, 7.7, cost, 1e-9)
	assert.Equal(t, "1080p", tier)
	cost, tier = sd2TierCost(t, sd2Model, "1080p", "video", 1_000_000)
	assert.InDelta(t, 5.0, cost, 1e-9)
	assert.Equal(t, "1080p_video", tier)
}

// A price of 0 is a real free tier and must survive the merge.
func TestRebuildIndex_ExplicitZeroPriceIsKept(t *testing.T) {
	withSD2MatrixJSON(t, `{"`+sd2Model+`":{"480p":{"no_video":0,"with_video":0}}}`)

	price, ok := GetThirdPartySD2TokenPrice(sd2Model, "480p", false)
	require.True(t, ok)
	assert.Equal(t, 0.0, price)
	price, ok = GetThirdPartySD2TokenPrice(sd2Model, "480p", true)
	require.True(t, ok)
	assert.Equal(t, 0.0, price)

	cost, tier := sd2TierCost(t, sd2Model, "480p", "none", 1_000_000)
	assert.Equal(t, 0.0, cost)
	assert.Equal(t, "480p", tier)
	// the other resolutions keep their own prices
	cost, _ = sd2TierCost(t, sd2Model, "720p", "none", 1_000_000)
	assert.InDelta(t, 7.0, cost, 1e-9)
}

// A null resolution removes the tier: the matrix is the only switch that makes
// a model's resolution unavailable, so a removed default must stay removed.
func TestRebuildIndex_NullResolutionRemovesTier(t *testing.T) {
	withSD2MatrixJSON(t, `{"`+sd2Model+`":{"4k":null}}`)

	resolutions, ok := GetThirdPartySD2Resolutions(sd2Model)
	require.True(t, ok)
	assert.Equal(t, []string{"480p", "720p", "1080p"}, resolutions)

	_, ok = GetThirdPartySD2TokenPrice(sd2Model, "4k", false)
	assert.False(t, ok, "the removed resolution is not priced")

	expression, ok := GetThirdPartySD2BillingExpr(sd2Model)
	require.True(t, ok)
	assert.NotContains(t, expression, `"4k"`)
	// the last listed tier is still the unconditional else branch
	assert.True(t, strings.HasSuffix(expression, `tier("1080p_video", u("tokens") * 4.7 / 1000000)`), expression)
	cost, tier := sd2TierCost(t, sd2Model, "1080p", "video", 1_000_000)
	assert.InDelta(t, 4.7, cost, 1e-9)
	assert.Equal(t, "1080p_video", tier)
}

// Removing every resolution leaves the model configured but unpriced: it has no
// expression and reports an empty resolution list, which the relay turns into a
// rejection instead of a generic per-call price.
func TestRebuildIndex_RemovingEveryResolutionLeavesModelUnpriced(t *testing.T) {
	withSD2MatrixJSON(t, `{"`+sd2FastModel+`":{"480p":null,"720p":null}}`)

	resolutions, ok := GetThirdPartySD2Resolutions(sd2FastModel)
	require.True(t, ok, "the model is still configured")
	assert.Empty(t, resolutions)

	_, ok = GetThirdPartySD2BillingExpr(sd2FastModel)
	assert.False(t, ok, "no expression can price the model")

	// the other model is untouched
	_, ok = GetThirdPartySD2BillingExpr(sd2Model)
	assert.True(t, ok)
}

// A resolution the built-in matrix does not price must carry both prices; the
// incomplete tier is dropped rather than billed at a guessed price.
func TestRebuildIndex_NewResolutionWithoutBothPricesIsDropped(t *testing.T) {
	noVideo := 4.0
	withSD2Matrix(t, ThirdPartySD2PricingMatrix{
		sd2FastModel: {"4k": {NoVideo: &noVideo}},
	})

	resolutions, ok := GetThirdPartySD2Resolutions(sd2FastModel)
	require.True(t, ok)
	assert.Equal(t, []string{"480p", "720p"}, resolutions)
	_, ok = GetThirdPartySD2TokenPrice(sd2FastModel, "4k", false)
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// buildThirdPartySD2BillingExpr
// ---------------------------------------------------------------------------

func TestBuildBillingExpr_LastTierIsTheElseBranch(t *testing.T) {
	expression := buildThirdPartySD2BillingExpr(map[string]thirdPartySD2Prices{
		"480p": {NoVideo: 1, WithVideo: 2},
	})
	// one condition for the first variant, the last variant is the fallback
	assert.Equal(t, 1, strings.Count(expression, " ? "))
	assert.True(t, strings.HasSuffix(expression, `tier("480p_video", u("tokens") * 2 / 1000000)`), expression)
}

func TestBuildBillingExpr_NoPricedResolutionRendersNothing(t *testing.T) {
	assert.Empty(t, buildThirdPartySD2BillingExpr(map[string]thirdPartySD2Prices{}))
	// unranked keys cannot be matched by the plugin facts and are skipped
	assert.Empty(t, buildThirdPartySD2BillingExpr(map[string]thirdPartySD2Prices{
		"garbage": {NoVideo: 1, WithVideo: 2},
	}))
}

// ---------------------------------------------------------------------------
// cloneThirdPartySD2PricingMatrix
// ---------------------------------------------------------------------------

func TestClonePricingMatrix_Nil(t *testing.T) {
	assert.Nil(t, cloneThirdPartySD2PricingMatrix(nil))
}

func TestClonePricingMatrix_DeepCopy(t *testing.T) {
	src := ThirdPartySD2PricingMatrix{
		"m": {
			"720p": sd2Override(1, 2),
			"480p": nil,
		},
	}
	dst := cloneThirdPartySD2PricingMatrix(src)
	*dst["m"]["720p"].NoVideo = 9
	dst["m"]["1080p"] = sd2Override(5, 5)
	dst["extra"] = map[string]*ThirdPartySD2ResolutionPricing{}

	// source unaffected, including the tombstone
	assert.Equal(t, 1.0, *src["m"]["720p"].NoVideo)
	assert.NotContains(t, src["m"], "1080p")
	assert.NotContains(t, src, "extra")
	require.Contains(t, dst["m"], "480p")
	assert.Nil(t, dst["m"]["480p"])
}

// ---------------------------------------------------------------------------
// resolveThirdPartySD2PricingMatrix — skip branches and reported problems
// ---------------------------------------------------------------------------

func TestResolveMatrix_SkipsEmptyModelName(t *testing.T) {
	merged, problems := resolveThirdPartySD2PricingMatrix(ThirdPartySD2PricingMatrix{
		"   ": {"720p": sd2Override(1, 1)},
	})
	assert.Empty(t, problems)
	assert.NotContains(t, merged, "")
	assert.NotContains(t, merged, "   ")
	assert.Len(t, merged, len(defaultThirdPartySD2PricingMatrix))
}

func TestResolveMatrix_SkipsUnnormalizableResolution(t *testing.T) {
	merged, problems := resolveThirdPartySD2PricingMatrix(ThirdPartySD2PricingMatrix{
		"m": {"garbage-res": sd2Override(1, 1)},
	})
	assert.Empty(t, problems)
	// model bucket is created but no valid resolution added
	require.Contains(t, merged, "m")
	assert.Empty(t, merged["m"])
}

func TestResolveMatrix_TrimsModelNameAndCreatesBucket(t *testing.T) {
	merged, problems := resolveThirdPartySD2PricingMatrix(ThirdPartySD2PricingMatrix{
		"  spaced-model  ": {"720p": sd2Override(3.3, 1.1)},
	})
	assert.Empty(t, problems)
	require.Contains(t, merged, "spaced-model")
	assert.Equal(t, 3.3, merged["spaced-model"]["720p"].NoVideo)
	assert.Equal(t, 1.1, merged["spaced-model"]["720p"].WithVideo)
}

func TestResolveMatrix_ReportsIncompleteTier(t *testing.T) {
	merged, problems := resolveThirdPartySD2PricingMatrix(ThirdPartySD2PricingMatrix{
		"new-model": {"720p": {WithVideo: sd2Float(1)}},
	})
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "no no_video price")
	assert.Empty(t, merged["new-model"])
}

func TestResolveMatrix_ReportsNonFinitePrice(t *testing.T) {
	_, problems := resolveThirdPartySD2PricingMatrix(ThirdPartySD2PricingMatrix{
		sd2Model: {"720p": {NoVideo: sd2Float(math.Inf(1)), WithVideo: sd2Float(1)}},
	})
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "finite price")
}

// ---------------------------------------------------------------------------
// ValidateThirdPartySD2PricingMatrixJSON + validateThirdPartySD2PricingMatrix
// ---------------------------------------------------------------------------

func TestValidatePricingMatrixJSON_Valid(t *testing.T) {
	err := ValidateThirdPartySD2PricingMatrixJSON(`{
		"model-a": {"720p": {"no_video": 1.0, "with_video": 0.5}},
		"model-b": {"1080p": {"no_video": 0, "with_video": 0}}
	}`)
	assert.NoError(t, err)
}

func TestValidatePricingMatrixJSON_MalformedJSON(t *testing.T) {
	err := ValidateThirdPartySD2PricingMatrixJSON(`{not valid json`)
	assert.Error(t, err)
}

func TestValidatePricingMatrixJSON_NegativeValue(t *testing.T) {
	err := ValidateThirdPartySD2PricingMatrixJSON(`{
		"model-a": {"720p": {"no_video": -1, "with_video": 4.3}}
	}`)
	assert.Error(t, err)
}

// A partial override of a resolution the built-in matrix does not price cannot
// be billed, so it must be refused at save time with an actionable message.
func TestValidatePricingMatrixJSON_PartialNewResolutionRejected(t *testing.T) {
	err := ValidateThirdPartySD2PricingMatrixJSON(`{"model-a": {"720p": {"with_video": 4.3}}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no no_video price")
	assert.Contains(t, err.Error(), "set both no_video and with_video")
}

// A partial override of a built-in resolution inherits the missing price and is
// accepted.
func TestValidatePricingMatrixJSON_PartialBuiltinResolutionAccepted(t *testing.T) {
	assert.NoError(t, ValidateThirdPartySD2PricingMatrixJSON(`{"`+sd2Model+`": {"1080p": {"with_video": 4.3}}}`))
}

// Removing resolutions, including all of them, is a valid configuration.
func TestValidatePricingMatrixJSON_TombstonesAccepted(t *testing.T) {
	assert.NoError(t, ValidateThirdPartySD2PricingMatrixJSON(`{"`+sd2Model+`": {"4k": null}}`))
	assert.NoError(t, ValidateThirdPartySD2PricingMatrixJSON(`{"`+sd2FastModel+`": {"480p": null, "720p": null}}`))
}

// The built-in matrix must itself render billable expressions.
func TestValidatePricingMatrix_DefaultMatrixIsBillable(t *testing.T) {
	assert.NoError(t, validateThirdPartySD2PricingMatrix(ThirdPartySD2PricingMatrix{}))
}

func TestValidatePricingMatrix_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		matrix  ThirdPartySD2PricingMatrix
		wantErr string // substring; "" means no error
	}{
		{
			name:    "nil matrix",
			matrix:  nil,
			wantErr: "must be a JSON object",
		},
		{
			name:    "empty matrix is valid",
			matrix:  ThirdPartySD2PricingMatrix{},
			wantErr: "",
		},
		{
			name:    "empty model name",
			matrix:  ThirdPartySD2PricingMatrix{"  ": {"720p": sd2Override(1, 1)}},
			wantErr: "model name cannot be empty",
		},
		{
			name:    "nil resolutions map",
			matrix:  ThirdPartySD2PricingMatrix{"m": nil},
			wantErr: "must map to an object",
		},
		{
			name:    "unnormalizable resolution",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"garbage": sd2Override(1, 1)}},
			wantErr: "resolution cannot be empty",
		},
		{
			name: "duplicate resolution after normalization",
			matrix: ThirdPartySD2PricingMatrix{"m": {
				"720p":     sd2Override(1, 1),
				"1280x720": sd2Override(2, 2), // also normalizes to 720p
			}},
			wantErr: "duplicate resolution",
		},
		{
			name:    "negative no_video",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"720p": {NoVideo: sd2Float(-0.1), WithVideo: sd2Float(1)}}},
			wantErr: "no_video",
		},
		{
			name:    "negative with_video",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"720p": {NoVideo: sd2Float(1), WithVideo: sd2Float(-0.1)}}},
			wantErr: "with_video",
		},
		{
			name:    "non-finite price",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"720p": {NoVideo: sd2Float(math.NaN()), WithVideo: sd2Float(1)}}},
			wantErr: "finite number",
		},
		{
			name:    "valid entry",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"720p": sd2Override(0, 1.2)}},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateThirdPartySD2PricingMatrix(tt.matrix)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// smokeTestThirdPartySD2BillingExprs — the generated expression must bill
// ---------------------------------------------------------------------------

func TestSmokeTestBillingExprs_PricesEveryConfiguredTier(t *testing.T) {
	require.NoError(t, smokeTestThirdPartySD2BillingExprs(defaultThirdPartySD2PricingMatrix))
}

func TestSmokeTestBillingExprs_RejectsUnusableExpression(t *testing.T) {
	// A non-finite price renders an expression that compiles but blows up on
	// evaluation; without the smoke test it only surfaced as a 400 on every
	// request for that model.
	err := smokeTestThirdPartySD2BillingExprs(thirdPartySD2ResolvedMatrix{
		"m": {"480p": {NoVideo: math.NaN(), WithVideo: 1}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be evaluated")
}

func TestSmokeTestBillingExprs_SkipsModelWithoutPricedResolution(t *testing.T) {
	require.NoError(t, smokeTestThirdPartySD2BillingExprs(thirdPartySD2ResolvedMatrix{
		"m": {},
	}))
}

func TestSmokeTestBillingExprs_RejectsMispricedTier(t *testing.T) {
	// The tier check compares the expression against the configured price, so a
	// builder that priced a tier from the wrong entry is caught here.
	expression := buildThirdPartySD2BillingExpr(map[string]thirdPartySD2Prices{
		"480p": {NoVideo: 1, WithVideo: 2},
	})
	err := smokeTestThirdPartySD2Tier("m", expression, "480p", thirdPartySD2Variant{
		videoInput: "none", tierName: "480p", price: 3,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instead of the configured")
}

func TestPreConsumedTokenEstimateConstant(t *testing.T) {
	assert.Equal(t, 1_000_000, ThirdPartySD2PreConsumedTokenEstimate)
	// guards against a silent drift of QuotaPerUnit used by the ratio math.
	assert.Equal(t, 500*1000.0, common.QuotaPerUnit)
}
