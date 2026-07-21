package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCacheRatio_Hit(t *testing.T) {
	ratio, ok := GetCacheRatio("gpt-4o")
	assert.True(t, ok)
	assert.Equal(t, 0.5, ratio)
}

func TestGetCacheRatio_Miss_DefaultsToOne(t *testing.T) {
	ratio, ok := GetCacheRatio("model-without-cache-ratio")
	assert.False(t, ok)
	assert.Equal(t, 1.0, ratio, "cache ratio default must be 1 (no discount)")
}

func TestGetCacheRatio_ExplicitZeroIsHonored(t *testing.T) {
	snap := snapshotFloatMap(cacheRatioMap)
	t.Cleanup(func() { restoreFloatMap(cacheRatioMap, snap) })
	require.NoError(t, UpdateCacheRatioByJSONString(`{"free-cache":0}`))
	ratio, ok := GetCacheRatio("free-cache")
	assert.True(t, ok, "an explicit 0 entry must report found (not the default path)")
	assert.Equal(t, 0.0, ratio)
}

func TestGetCreateCacheRatio_Hit(t *testing.T) {
	ratio, ok := GetCreateCacheRatio("claude-opus-4-8")
	assert.True(t, ok)
	assert.Equal(t, 1.25, ratio)
}

func TestGetCreateCacheRatio_Miss_DefaultsTo125(t *testing.T) {
	ratio, ok := GetCreateCacheRatio("model-without-create-cache-ratio")
	assert.False(t, ok)
	assert.Equal(t, 1.25, ratio, "create-cache ratio default is 1.25")
}

func TestCacheRatio_JSONRoundTrip(t *testing.T) {
	snap := snapshotFloatMap(cacheRatioMap)
	t.Cleanup(func() { restoreFloatMap(cacheRatioMap, snap) })

	require.NoError(t, UpdateCacheRatioByJSONString(`{"m":0.3}`))
	assert.JSONEq(t, `{"m":0.3}`, CacheRatio2JSONString())
	assert.Equal(t, map[string]float64{"m": 0.3}, GetCacheRatioMap())
	assert.Equal(t, map[string]float64{"m": 0.3}, GetCacheRatioCopy())
}

func TestCreateCacheRatio_JSONRoundTrip(t *testing.T) {
	snap := snapshotFloatMap(createCacheRatioMap)
	t.Cleanup(func() { restoreFloatMap(createCacheRatioMap, snap) })

	require.NoError(t, UpdateCreateCacheRatioByJSONString(`{"m":1.5}`))
	assert.JSONEq(t, `{"m":1.5}`, CreateCacheRatio2JSONString())
	assert.Equal(t, map[string]float64{"m": 1.5}, GetCreateCacheRatioCopy())
}

func TestCacheRatio_MalformedReturnsError(t *testing.T) {
	snap := snapshotFloatMap(cacheRatioMap)
	t.Cleanup(func() { restoreFloatMap(cacheRatioMap, snap) })
	require.Error(t, UpdateCacheRatioByJSONString(`{oops`))
}

func TestCreateCacheRatio_MalformedReturnsError(t *testing.T) {
	snap := snapshotFloatMap(createCacheRatioMap)
	t.Cleanup(func() { restoreFloatMap(createCacheRatioMap, snap) })
	require.Error(t, UpdateCreateCacheRatioByJSONString(`{oops`))
}

func TestDefaultCacheRatio_Anchors(t *testing.T) {
	// Spot-check representative discount anchors are exactly as documented.
	assert.Equal(t, 0.1, defaultCacheRatio["gpt-5"])
	assert.Equal(t, 0.25, defaultCacheRatio["gpt-4.1"])
	assert.Equal(t, 0.5, defaultCacheRatio["gpt-4o"])
	assert.Equal(t, 0.1, defaultCacheRatio["claude-opus-4-8"])
	assert.Equal(t, 1.25, defaultCreateCacheRatio["claude-opus-4-8"])
}
