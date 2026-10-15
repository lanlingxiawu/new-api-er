package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDescribeEditorStandardForms(t *testing.T) {
	InvalidateCache()
	for _, source := range []string{`p*2+c*4`, `(2*p)+(c*(4))`, `v1:tier("base",p*2+c*4)`} {
		editor, err := DescribeEditor(source)
		require.NoError(t, err)
		require.Equal(t, "flat", editor.Kind)
		require.Equal(t, source, editor.Source)
		require.Equal(t, ExprHashString(source), editor.SourceHash)
		require.Len(t, editor.Branches, 1)
		require.Equal(t, float64(2), editor.Branches[0].Prices["p"])
		require.Equal(t, float64(4), editor.Branches[0].Prices["c"])
		require.True(t, editor.CanScale)
	}
	require.Empty(t, cache, "editor analysis must not populate the relay cache")
}

func TestDescribeEditorKeepsSourceOrderAndUnreachableBranches(t *testing.T) {
	source := `v1:len > 100 ? tier("large", p*2+cr*0) : len < 0 ? p*7 : tier("small",c*4)`
	editor, err := DescribeEditor(source)
	require.NoError(t, err)
	require.Equal(t, "tiered", editor.Kind)
	require.Len(t, editor.Branches, 3)
	require.Equal(t, "large", editor.Branches[0].Name)
	require.Equal(t, int64(101), editor.Branches[0].Min)
	require.False(t, editor.Branches[1].Reachable)
	require.True(t, editor.Branches[2].Reachable)
	require.Equal(t, int64(100), *editor.Branches[2].Max)
	require.Contains(t, editor.UsedVars, "cr")
	require.Contains(t, editor.UsedVars, "len")
	require.Equal(t, float64(0), editor.Branches[0].Prices["cr"])
	require.NotContains(t, editor.Branches[2].Prices, "cr")
}

func TestDescribeEditorCustomAndUnsupported(t *testing.T) {
	for _, source := range []string{`tier("base",p*2)+7`, `hour("UTC")<8?p*2:p*3`, `p<100?p*2:p*3`} {
		editor, err := DescribeEditor(source)
		require.NoError(t, err)
		require.Equal(t, "custom", editor.Kind)
		require.True(t, editor.CanScale)
		require.Empty(t, editor.Branches)
		require.Equal(t, source, editor.Source)
	}
	for _, source := range []string{`v2:p*2`, `fixed(1)`, `tier("a",p*2)|||when(true)*2`} {
		_, err := DescribeEditor(source)
		require.Error(t, err)
	}
}

func TestEditorPreservesUnsupportedImageCachePricing(t *testing.T) {
	editor, err := DescribeEditor(`p*5+img*8+img_cr*2+c*30`)
	require.NoError(t, err)
	require.Equal(t, "custom", editor.Kind)
	require.Contains(t, editor.UsedVars, "img_cr")
	require.False(t, editor.CanScale)
	require.Empty(t, editor.Branches)
}
