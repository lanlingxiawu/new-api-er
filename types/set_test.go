package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSet_NewIsEmpty(t *testing.T) {
	s := NewSet[string]()
	require.NotNil(t, s)
	require.Equal(t, 0, s.Len())
	require.Empty(t, s.Items())
}

func TestSet_AddAndContains(t *testing.T) {
	s := NewSet[string]()
	require.False(t, s.Contains("a"))

	s.Add("a")
	require.True(t, s.Contains("a"))
	require.Equal(t, 1, s.Len())

	// Adding a duplicate does not grow the set.
	s.Add("a")
	require.Equal(t, 1, s.Len())

	s.Add("b")
	require.Equal(t, 2, s.Len())
}

func TestSet_Remove(t *testing.T) {
	s := NewSet[int]()
	s.Add(1)
	s.Add(2)

	s.Remove(1)
	require.False(t, s.Contains(1))
	require.True(t, s.Contains(2))
	require.Equal(t, 1, s.Len())

	// Removing an absent element is a no-op (delete on missing key).
	s.Remove(99)
	require.Equal(t, 1, s.Len())
}

func TestSet_Items(t *testing.T) {
	s := NewSet[string]()
	s.Add("x")
	s.Add("y")
	s.Add("z")

	// Order is unspecified (map iteration), so compare as a multiset.
	assert.ElementsMatch(t, []string{"x", "y", "z"}, s.Items())
}

func TestSet_ItemsEmptyReturnsNonNilEmptySlice(t *testing.T) {
	s := NewSet[int]()
	items := s.Items()
	require.NotNil(t, items)
	require.Len(t, items, 0)
}
