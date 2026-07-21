package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRWMap_NewIsEmpty(t *testing.T) {
	m := NewRWMap[string, int]()
	require.NotNil(t, m)
	require.Equal(t, 0, m.Len())
}

func TestRWMap_SetGet(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("a", 1)

	v, ok := m.Get("a")
	require.True(t, ok)
	require.Equal(t, 1, v)

	// Overwrite.
	m.Set("a", 2)
	v, ok = m.Get("a")
	require.True(t, ok)
	require.Equal(t, 2, v)
}

func TestRWMap_GetMissing(t *testing.T) {
	m := NewRWMap[string, int]()
	v, ok := m.Get("nope")
	require.False(t, ok)
	require.Equal(t, 0, v) // zero value of V
}

func TestRWMap_AddAll(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("a", 1)
	m.AddAll(map[string]int{"b": 2, "c": 3, "a": 10})

	require.Equal(t, 3, m.Len())
	v, _ := m.Get("a")
	require.Equal(t, 10, v) // AddAll overwrites existing keys
	v, _ = m.Get("b")
	require.Equal(t, 2, v)
}

func TestRWMap_AddAll_EmptyMapIsNoOp(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("a", 1)
	m.AddAll(map[string]int{})
	require.Equal(t, 1, m.Len())
}

func TestRWMap_Clear(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("a", 1)
	m.Set("b", 2)
	m.Clear()
	require.Equal(t, 0, m.Len())
	_, ok := m.Get("a")
	require.False(t, ok)

	// Map remains usable after clear.
	m.Set("c", 3)
	require.Equal(t, 1, m.Len())
}

func TestRWMap_ReadAll_ReturnsCopy(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("a", 1)
	m.Set("b", 2)

	snapshot := m.ReadAll()
	require.Equal(t, map[string]int{"a": 1, "b": 2}, snapshot)

	// Mutating the returned copy must not affect the source.
	snapshot["a"] = 999
	snapshot["new"] = 5
	v, _ := m.Get("a")
	require.Equal(t, 1, v, "source must be unaffected by copy mutation")
	_, ok := m.Get("new")
	require.False(t, ok)
}

func TestRWMap_ReadAll_EmptyReturnsNonNilEmpty(t *testing.T) {
	m := NewRWMap[string, int]()
	snapshot := m.ReadAll()
	require.NotNil(t, snapshot)
	require.Len(t, snapshot, 0)
}

func TestRWMap_Len(t *testing.T) {
	m := NewRWMap[string, int]()
	require.Equal(t, 0, m.Len())
	m.Set("a", 1)
	require.Equal(t, 1, m.Len())
	m.Set("b", 2)
	require.Equal(t, 2, m.Len())
}

func TestRWMap_MarshalUnmarshalJSON_RoundTrip(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("a", 1)
	m.Set("b", 2)

	b, err := m.MarshalJSON()
	require.NoError(t, err)

	m2 := NewRWMap[string, int]()
	err = m2.UnmarshalJSON(b)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"a": 1, "b": 2}, m2.ReadAll())
}

func TestRWMap_UnmarshalJSON_ResetsExistingData(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("old", 100)

	err := m.UnmarshalJSON([]byte(`{"a":1}`))
	require.NoError(t, err)
	require.Equal(t, map[string]int{"a": 1}, m.ReadAll())
	_, ok := m.Get("old")
	require.False(t, ok, "prior data must be discarded")
}

func TestRWMap_UnmarshalJSON_InvalidReturnsError(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("keep", 7)
	err := m.UnmarshalJSON([]byte(`{not json`))
	require.Error(t, err)
	// FIXED: UnmarshalJSON now decodes into a temp map and only swaps on success,
	// so a malformed payload must PRESERVE the existing data, not clear it.
	require.Equal(t, map[string]int{"keep": 7}, m.ReadAll(), "malformed input must preserve existing data")
}

func TestRWMap_MarshalJSONString_Success(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("a", 1)

	s := m.MarshalJSONString()
	require.Equal(t, `{"a":1}`, s)
}

func TestRWMap_MarshalJSONString_EmptyMap(t *testing.T) {
	m := NewRWMap[string, int]()
	require.Equal(t, "{}", m.MarshalJSONString())
}

// unmarshalableValue always fails to marshal, forcing MarshalJSON to error so
// MarshalJSONString falls back to "{}".
type unmarshalableValue struct{}

func (unmarshalableValue) MarshalJSON() ([]byte, error) {
	return nil, assert.AnError
}

func TestRWMap_MarshalJSONString_MarshalErrorReturnsBraces(t *testing.T) {
	m := NewRWMap[string, unmarshalableValue]()
	m.Set("a", unmarshalableValue{})
	require.Equal(t, "{}", m.MarshalJSONString())
}

func TestLoadFromJsonString_Success(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("old", 1)

	err := LoadFromJsonString(m, `{"x":10,"y":20}`)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"x": 10, "y": 20}, m.ReadAll())
	_, ok := m.Get("old")
	require.False(t, ok)
}

func TestLoadFromJsonString_InvalidReturnsError(t *testing.T) {
	m := NewRWMap[string, int]()
	m.Set("keep", 42)
	err := LoadFromJsonString(m, `garbage`)
	require.Error(t, err)
	// FIXED: LoadFromJsonString decodes into a temp map and swaps only on success,
	// so a malformed payload preserves the existing data.
	require.Equal(t, map[string]int{"keep": 42}, m.ReadAll(), "malformed input must preserve existing data")
}

func TestLoadFromJsonStringWithCallback_SuccessInvokesCallback(t *testing.T) {
	m := NewRWMap[string, int]()
	called := false

	err := LoadFromJsonStringWithCallback(m, `{"x":1}`, func() { called = true })
	require.NoError(t, err)
	require.True(t, called, "callback must run on success")
	require.Equal(t, map[string]int{"x": 1}, m.ReadAll())
}

func TestLoadFromJsonStringWithCallback_ErrorSkipsCallback(t *testing.T) {
	m := NewRWMap[string, int]()
	called := false

	m.Set("keep", 5)
	err := LoadFromJsonStringWithCallback(m, `not-json`, func() { called = true })
	require.Error(t, err)
	require.False(t, called, "callback must NOT run when unmarshal fails")
	// FIXED: malformed payload preserves the existing data (temp-map swap).
	require.Equal(t, map[string]int{"keep": 5}, m.ReadAll(), "malformed input must preserve existing data")
}

func TestLoadFromJsonStringWithCallback_NilCallbackIsSafe(t *testing.T) {
	m := NewRWMap[string, int]()
	require.NotPanics(t, func() {
		err := LoadFromJsonStringWithCallback(m, `{"x":1}`, nil)
		require.NoError(t, err)
	})
	require.Equal(t, map[string]int{"x": 1}, m.ReadAll())
}
