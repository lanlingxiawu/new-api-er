package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type copyInner struct {
	Vals []int
}

type copyOuter struct {
	Name  string
	Inner *copyInner
}

func TestDeepCopy_NilSource(t *testing.T) {
	_, err := DeepCopy[copyOuter](nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be nil")
}

func TestDeepCopy_Independent(t *testing.T) {
	src := &copyOuter{Name: "a", Inner: &copyInner{Vals: []int{1, 2, 3}}}
	dst, err := DeepCopy(src)
	require.NoError(t, err)
	require.NotNil(t, dst)

	assert.Equal(t, src.Name, dst.Name)
	require.NotNil(t, dst.Inner)
	assert.Equal(t, src.Inner.Vals, dst.Inner.Vals)

	// mutating the copy must not affect the source (deep, not shallow)
	dst.Inner.Vals[0] = 99
	assert.Equal(t, 1, src.Inner.Vals[0])
	assert.NotSame(t, src.Inner, dst.Inner)
}
