package model_setting

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		"dreamina-seedance-2-0-260128": {
			"1080p": {NoVideo: 9.9, WithVideo: 6.6},
		},
	})

	// overlay wins for the configured resolution
	price, ok := GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-260128", "1080p", false)
	require.True(t, ok)
	assert.Equal(t, 9.9, price)

	// hasVideoInput selects WithVideo
	price, ok = GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-260128", "1080p", true)
	require.True(t, ok)
	assert.Equal(t, 6.6, price)

	// unconfigured resolution on that model falls back to the merged default matrix
	price, ok = GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-260128", "720p", true)
	require.True(t, ok)
	assert.Equal(t, 4.3, price)
}

func TestGetThirdPartySD2TokenPrice_NormalizesInputKeys(t *testing.T) {
	withSD2Matrix(t, ThirdPartySD2PricingMatrix{
		"dreamina-seedance-2-0-fast-260128": {
			"720P":  {NoVideo: 6.1, WithVideo: 3.6},
			"2160P": {NoVideo: 8.8, WithVideo: 5.5},
		},
	})

	price, ok := GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-fast-260128", "1280x720", false)
	require.True(t, ok)
	assert.Equal(t, 6.1, price)

	// "2160P" overlay key normalized to 4k; query "4k" hits it
	price, ok = GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-fast-260128", "4k", true)
	require.True(t, ok)
	assert.Equal(t, 5.5, price)
}

func TestGetThirdPartySD2TokenPrice_TrimsModelName(t *testing.T) {
	price, ok := GetThirdPartySD2TokenPrice("  dreamina-seedance-2-0-260128  ", "480p", false)
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
	price, ok := GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-fast-260128", "4k", false)
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

	price, ok := GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-260128", "480p", false)
	require.True(t, ok)
	assert.Equal(t, 7.0, price)
	require.NotNil(t, currentThirdPartySD2PricingIndex.Load(), "index rebuilt")
}

// ---------------------------------------------------------------------------
// GetThirdPartySD2PricingMatrix — snapshot + nil-index fallback
// ---------------------------------------------------------------------------

func TestGetThirdPartySD2PricingMatrix_ReturnsDeepCopy(t *testing.T) {
	m := GetThirdPartySD2PricingMatrix()
	require.Contains(t, m, "dreamina-seedance-2-0-260128")

	// mutating the returned copy must not affect subsequent reads
	m["dreamina-seedance-2-0-260128"]["480p"] = ThirdPartySD2ResolutionPricing{NoVideo: 999}
	m["injected"] = map[string]ThirdPartySD2ResolutionPricing{}

	fresh := GetThirdPartySD2PricingMatrix()
	assert.NotContains(t, fresh, "injected")
	assert.Equal(t, 7.0, fresh["dreamina-seedance-2-0-260128"]["480p"].NoVideo)
}

func TestGetThirdPartySD2PricingMatrix_NilIndexFallsBackToDefaults(t *testing.T) {
	original := currentThirdPartySD2PricingIndex.Load()
	t.Cleanup(func() { currentThirdPartySD2PricingIndex.Store(original) })

	currentThirdPartySD2PricingIndex.Store(nil)
	m := GetThirdPartySD2PricingMatrix()
	assert.Contains(t, m, "dreamina-seedance-2-0-260128")
}

func TestGetThirdPartySD2PricingMatrix_NilMatrixInsideIndexFallsBack(t *testing.T) {
	original := currentThirdPartySD2PricingIndex.Load()
	t.Cleanup(func() { currentThirdPartySD2PricingIndex.Store(original) })

	currentThirdPartySD2PricingIndex.Store(&thirdPartySD2PricingIndex{matrix: nil})
	m := GetThirdPartySD2PricingMatrix()
	assert.Contains(t, m, "dreamina-seedance-2-0-260128")
}

// ---------------------------------------------------------------------------
// RebuildThirdPartySD2PricingIndex — overlay merge semantics
// ---------------------------------------------------------------------------

func TestRebuildIndex_OverlayMergesOntoDefaults(t *testing.T) {
	withSD2Matrix(t, ThirdPartySD2PricingMatrix{
		"brand-new-model": {"720p": {NoVideo: 1.1, WithVideo: 0.5}},
		"dreamina-seedance-2-0-260128": {
			"480p": {NoVideo: 100, WithVideo: 50}, // override one default entry
		},
	})
	m := GetThirdPartySD2PricingMatrix()

	// new model added
	assert.Equal(t, 1.1, m["brand-new-model"]["720p"].NoVideo)
	// overridden entry
	assert.Equal(t, 100.0, m["dreamina-seedance-2-0-260128"]["480p"].NoVideo)
	// untouched default entry on the same model survives the merge
	assert.Equal(t, 7.7, m["dreamina-seedance-2-0-260128"]["1080p"].NoVideo)
}

// ---------------------------------------------------------------------------
// cloneThirdPartySD2PricingMatrix
// ---------------------------------------------------------------------------

func TestClonePricingMatrix_Nil(t *testing.T) {
	assert.Nil(t, cloneThirdPartySD2PricingMatrix(nil))
}

func TestClonePricingMatrix_DeepCopy(t *testing.T) {
	src := ThirdPartySD2PricingMatrix{
		"m": {"720p": {NoVideo: 1, WithVideo: 2}},
	}
	dst := cloneThirdPartySD2PricingMatrix(src)
	dst["m"]["720p"] = ThirdPartySD2ResolutionPricing{NoVideo: 9}
	dst["extra"] = map[string]ThirdPartySD2ResolutionPricing{}

	// source unaffected
	assert.Equal(t, 1.0, src["m"]["720p"].NoVideo)
	assert.NotContains(t, src, "extra")
}

// ---------------------------------------------------------------------------
// overlayThirdPartySD2PricingMatrix — skip branches
// ---------------------------------------------------------------------------

func TestOverlayPricingMatrix_SkipsEmptyModelName(t *testing.T) {
	dst := ThirdPartySD2PricingMatrix{}
	overlayThirdPartySD2PricingMatrix(dst, ThirdPartySD2PricingMatrix{
		"   ": {"720p": {NoVideo: 1}},
	})
	assert.Empty(t, dst)
}

func TestOverlayPricingMatrix_SkipsUnnormalizableResolution(t *testing.T) {
	dst := ThirdPartySD2PricingMatrix{}
	overlayThirdPartySD2PricingMatrix(dst, ThirdPartySD2PricingMatrix{
		"m": {"garbage-res": {NoVideo: 1}},
	})
	// model bucket is created but no valid resolution added
	require.Contains(t, dst, "m")
	assert.Empty(t, dst["m"])
}

func TestOverlayPricingMatrix_TrimsModelNameAndCreatesBucket(t *testing.T) {
	dst := ThirdPartySD2PricingMatrix{}
	overlayThirdPartySD2PricingMatrix(dst, ThirdPartySD2PricingMatrix{
		"  spaced-model  ": {"720p": {NoVideo: 3.3}},
	})
	require.Contains(t, dst, "spaced-model")
	assert.Equal(t, 3.3, dst["spaced-model"]["720p"].NoVideo)
}

func TestOverlayPricingMatrix_OverwritesExistingBucket(t *testing.T) {
	dst := ThirdPartySD2PricingMatrix{
		"m": {"720p": {NoVideo: 1}},
	}
	overlayThirdPartySD2PricingMatrix(dst, ThirdPartySD2PricingMatrix{
		"m": {"720p": {NoVideo: 2}, "1080p": {NoVideo: 5}},
	})
	assert.Equal(t, 2.0, dst["m"]["720p"].NoVideo)  // overwritten
	assert.Equal(t, 5.0, dst["m"]["1080p"].NoVideo) // added into existing bucket
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
			matrix:  ThirdPartySD2PricingMatrix{"  ": {"720p": {}}},
			wantErr: "model name cannot be empty",
		},
		{
			name:    "nil resolutions map",
			matrix:  ThirdPartySD2PricingMatrix{"m": nil},
			wantErr: "must map to an object",
		},
		{
			name:    "unnormalizable resolution",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"garbage": {}}},
			wantErr: "resolution cannot be empty",
		},
		{
			name: "duplicate resolution after normalization",
			matrix: ThirdPartySD2PricingMatrix{"m": {
				"720p":     {NoVideo: 1},
				"1280x720": {NoVideo: 2}, // also normalizes to 720p
			}},
			wantErr: "duplicate resolution",
		},
		{
			name:    "negative no_video",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"720p": {NoVideo: -0.1}}},
			wantErr: "no_video",
		},
		{
			name:    "negative with_video",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"720p": {WithVideo: -0.1}}},
			wantErr: "with_video",
		},
		{
			name:    "valid entry",
			matrix:  ThirdPartySD2PricingMatrix{"m": {"720p": {NoVideo: 0, WithVideo: 1.2}}},
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

func TestPreConsumedTokenEstimateConstant(t *testing.T) {
	assert.Equal(t, 1_000_000, ThirdPartySD2PreConsumedTokenEstimate)
	// guards against a silent drift of QuotaPerUnit used by the ratio math.
	assert.Equal(t, 500*1000.0, common.QuotaPerUnit)
}
