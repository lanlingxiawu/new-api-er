package billingexpr

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestPreviewRejectsUnboundedWork(t *testing.T) {
	for _, source := range []string{`sum(1..100000000)`, `len(repeat("x",100000000))`, `map(1..99999999,{#*2})`, strings.Repeat("p+", 70) + "p"} {
		_, err := CompilePreview(source)
		require.Error(t, err, source)
	}
}

func TestPreviewScalarBuiltinWhitelist(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   float64
	}{
		{`max(p*2,c*4)`, 20},
		{`min(p*2,c*4)`, 12},
		{`abs(p-c)`, 7},
		{`ceil(p/3)`, 4},
		{`floor(p/3)`, 3},
		{`max(min(p*2,c*4),abs(cr-5))`, 12},
	} {
		t.Run(tc.source, func(t *testing.T) {
			program, err := CompilePreview(tc.source)
			require.NoError(t, err)
			cost, _, err := program.RunAt(TokenParams{P: 10, C: 3, CR: 2}, RequestInput{}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			require.NoError(t, err)
			require.Equal(t, tc.want, cost)
			editor, err := DescribeEditor(tc.source)
			require.NoError(t, err)
			require.Equal(t, "custom", editor.Kind)
			require.True(t, editor.CanScale)
			require.Empty(t, editor.Branches)
			require.Contains(t, editor.UsedVars, "p")
		})
	}
	for _, source := range []string{`max(sum(1..100000000),p)`, `max(len(repeat("x",100000000)),p)`, `max([1,2])`, `sort([2,1])[0]`, `len("text")`, `max(p)`} {
		_, err := CompilePreview(source)
		require.Error(t, err, source)
		_, err = DescribeEditor(source)
		require.Error(t, err, source)
	}
}

func TestAnalyzeStandardCompleteTree(t *testing.T) {
	a, err := AnalyzeStandard(`v1:len <= 100 ? tier("a", p*2+p*3+c*4) : len < 201 ? tier("b",p*6+cr*0) : tier("c",p*8)`)
	require.NoError(t, err)
	require.Len(t, a.Segments, 3)
	require.Equal(t, float64(5), a.Segments[0].Prices["p"])
	require.Equal(t, int64(101), a.Segments[1].Min)
	require.Equal(t, int64(200), *a.Segments[1].Max)
	require.True(t, a.UsedVars["cr"])
	require.Equal(t, float64(0), a.EffectivePrices(a.Segments[0])["cr"])
	for _, s := range []string{`tier("a",p*2)+7`, `p*2+header("x")`, `p<100?tier("a",p*2):tier("b",p*3)`, `v2:p*2`, `fixed(2)`, `tier("a",p*-1)`, `tier("a",p*2)|||when(true)*2`} {
		_, err = AnalyzeStandard(s)
		require.Error(t, err, s)
	}
}

func TestPreviewFixedTimeAndCacheIsolation(t *testing.T) {
	InvalidateCache()
	p, err := CompilePreview(`hour("Asia/Shanghai")==0 ? tier("night",p*2) : tier("day",p*3)`)
	require.NoError(t, err)
	cost, trace, err := p.RunAt(TokenParams{P: 10}, RequestInput{}, time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, float64(20), cost)
	require.Equal(t, "night", trace.MatchedTier)
	require.Empty(t, cache)
	_, _, err = p.RunAt(TokenParams{}, RequestInput{}, time.Time{})
	require.Error(t, err)
}

func TestCompareStandardAlignsBoundaries(t *testing.T) {
	a, err := AnalyzeStandard(`len<=10?tier("a",p*2):tier("b",p*4)`)
	require.NoError(t, err)
	b, err := AnalyzeStandard(`len<=20?tier("a",p*2):tier("b",p*4)`)
	require.NoError(t, err)
	differences := CompareStandard(a, b)
	require.NotEmpty(t, differences)
	require.Equal(t, int64(11), differences[0].Min)
	require.Equal(t, int64(20), *differences[0].Max)
	require.Equal(t, "p", differences[0].Variable)
}

func TestPreviewPreservesExplicitZeroAndBranchVars(t *testing.T) {
	p, err := CompilePreview(`len<=10?tier("a",p*2):tier("b",p*3+cr*0)`)
	require.NoError(t, err)
	require.True(t, p.UsedVars["cr"])
}

func TestPreviewFixedPricingRoundTrip(t *testing.T) {
	InvalidateCache()
	program, err := CompilePreview(`tier("request", fixed(2))`)
	require.NoError(t, err)
	cost, trace, err := program.RunAt(TokenParams{}, RequestInput{}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, 2e6, cost)
	require.Equal(t, BillingUnitRequest, trace.BillingUnit)
	require.NotNil(t, trace.FixedPrice)
	require.Equal(t, 2.0, *trace.FixedPrice)
	require.Equal(t, "request", trace.MatchedTier)
	require.Empty(t, cache, "preview must not populate the relay cache")
}

func TestPreviewFixedPricingLenTierBoundaries(t *testing.T) {
	program, err := CompilePreview(`v1:len <= 272000 ? tier("short", fixed(1)) : tier("long", fixed(2))`)
	require.NoError(t, err)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cost, trace, err := program.RunAt(TokenParams{Len: 272000}, RequestInput{}, at)
	require.NoError(t, err)
	require.Equal(t, 1e6, cost)
	require.Equal(t, "short", trace.MatchedTier)
	require.Equal(t, BillingUnitRequest, trace.BillingUnit)
	cost, trace, err = program.RunAt(TokenParams{Len: 272001}, RequestInput{}, at)
	require.NoError(t, err)
	require.Equal(t, 2e6, cost)
	require.Equal(t, "long", trace.MatchedTier)
}

func TestPreviewFixedPricingImageCount(t *testing.T) {
	program, err := CompilePreview(`tier("image", fixed(0.05)) * image_count`)
	require.NoError(t, err)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	three := 3
	cost, trace, err := program.RunAt(TokenParams{}, RequestInput{ImageCount: &three}, at)
	require.NoError(t, err)
	require.Equal(t, 0.05*1e6*3, cost)
	require.NotNil(t, trace.ImageCount)
	require.Equal(t, 3, *trace.ImageCount)
	require.Equal(t, BillingUnitRequest, trace.BillingUnit)
	cost, trace, err = program.RunAt(TokenParams{}, RequestInput{}, at)
	require.NoError(t, err)
	require.Equal(t, 0.05*1e6, cost)
	require.NotNil(t, trace.ImageCount)
	require.Equal(t, 1, *trace.ImageCount)
	zero := 0
	_, _, err = program.RunAt(TokenParams{}, RequestInput{ImageCount: &zero}, at)
	require.Error(t, err)
	tooMany := dto.MaxImageN + 1
	_, _, err = program.RunAt(TokenParams{}, RequestInput{ImageCount: &tooMany}, at)
	require.Error(t, err)
}

func TestPreviewRejectsInvalidFixedPricing(t *testing.T) {
	for _, source := range []string{`fixed(1)`, `p*2 + fixed(1)`, `tier("a", fixed(-1))`, `tier("a", fixed(param("x")))`, `u("x")*2`} {
		_, err := CompilePreview(source)
		require.Error(t, err, source)
	}
}

func TestPreviewImageCacheReadTokens(t *testing.T) {
	program, err := CompilePreview(`p*5 + img_cr*2 + img*8`)
	require.NoError(t, err)
	require.True(t, program.UsedVars["img_cr"])
	cost, _, err := program.RunAt(TokenParams{P: 10, ImgCR: 3, Img: 8}, RequestInput{}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, 120.0, cost)
}
