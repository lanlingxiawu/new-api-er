package billingexpr

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
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
