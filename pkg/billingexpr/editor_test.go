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

func TestDescribeEditorFixedPricingForms(t *testing.T) {
	InvalidateCache()
	at := []struct {
		source   string
		branches []EditorBranch
	}{
		{
			source: `tier("request", fixed(2))`,
			branches: []EditorBranch{
				{Name: "request", Min: 0, Unit: "request", Price: float64Pointer(2)},
			},
		},
		{
			source: `v1:len <= 272000 ? tier("short", fixed(1)) : tier("long", fixed(2))`,
			branches: []EditorBranch{
				{Name: "short", Min: 0, Max: int64Pointer(272000), Unit: "request", Price: float64Pointer(1)},
				{Name: "long", Min: 272001, Unit: "request", Price: float64Pointer(2)},
			},
		},
		{
			source:   `tier("image", fixed(0.05)) * image_count`,
			branches: []EditorBranch{{Name: "image", Min: 0, Unit: "image", Price: float64Pointer(0.05), Quantity: "image_count"}},
		},
		{
			source:   `image_count * tier("image", fixed(0.05))`,
			branches: []EditorBranch{{Name: "image", Min: 0, Unit: "image", Price: float64Pointer(0.05), Quantity: "image_count"}},
		},
		{
			source: `len <= 272000 ? tier("short", fixed(0.05)) * image_count : tier("long", fixed(0.1)) * image_count`,
			branches: []EditorBranch{
				{Name: "short", Min: 0, Max: int64Pointer(272000), Unit: "image", Price: float64Pointer(0.05), Quantity: "image_count"},
				{Name: "long", Min: 272001, Unit: "image", Price: float64Pointer(0.1), Quantity: "image_count"},
			},
		},
	}
	for _, tc := range at {
		t.Run(tc.source, func(t *testing.T) {
			editor, err := DescribeEditor(tc.source)
			require.NoError(t, err)
			require.Equal(t, "fixed", editor.Kind)
			require.Equal(t, tc.source, editor.Source)
			require.Equal(t, ExprHashString(tc.source), editor.SourceHash)
			require.True(t, editor.CanScale)
			require.Empty(t, editor.Reason)
			require.Len(t, editor.Branches, len(tc.branches))
			for index, want := range tc.branches {
				got := editor.Branches[index]
				require.Equal(t, want.Name, got.Name)
				require.Equal(t, want.Name, got.Label)
				require.True(t, got.Wrapped)
				require.True(t, got.Reachable)
				require.Equal(t, want.Min, got.Min)
				if want.Max == nil {
					require.Nil(t, got.Max)
				} else {
					require.NotNil(t, got.Max)
					require.Equal(t, *want.Max, *got.Max)
				}
				require.Equal(t, want.Unit, got.Unit)
				require.Equal(t, want.Quantity, got.Quantity)
				require.NotNil(t, got.Price)
				require.Equal(t, *want.Price, *got.Price)
			}
		})
	}
	require.Contains(t, mustDescribeEditor(t, `tier("image", fixed(0.05)) * image_count`).UsedVars, "image_count")
	require.Empty(t, cache, "editor analysis must not populate the relay cache")
}

func mustDescribeEditor(t *testing.T, source string) *EditorDescription {
	t.Helper()
	editor, err := DescribeEditor(source)
	require.NoError(t, err)
	return editor
}

func TestDescribeEditorFixedPricingUnreachableBranch(t *testing.T) {
	editor := mustDescribeEditor(t, `len < 0 ? tier("neg", fixed(1)) : tier("base", fixed(2))`)
	require.Equal(t, "fixed", editor.Kind)
	require.Len(t, editor.Branches, 2)
	require.Equal(t, "neg", editor.Branches[0].Name)
	require.False(t, editor.Branches[0].Reachable)
	require.Equal(t, "base", editor.Branches[1].Name)
	require.True(t, editor.Branches[1].Reachable)
}

func TestDescribeEditorFixedPricingCustomAndRejected(t *testing.T) {
	for _, source := range []string{
		`has(param("mode"),"hd") ? tier("image", fixed(0.1)) * image_count : tier("image", fixed(0.05)) * image_count`,
		`len>100 ? tier("a", fixed(2)) : tier("b", p*2)`,
	} {
		editor, err := DescribeEditor(source)
		require.NoError(t, err, source)
		require.Equal(t, "custom", editor.Kind)
		require.Equal(t, "custom_rule", editor.Reason)
		require.Empty(t, editor.Branches)
	}
	for _, source := range []string{`fixed(1)`, `p*2 + fixed(1)`, `tier("a", fixed(-1))`} {
		_, err := DescribeEditor(source)
		require.Error(t, err, source)
	}
}

func float64Pointer(value float64) *float64 { return &value }

func int64Pointer(value int64) *int64 { return &value }
