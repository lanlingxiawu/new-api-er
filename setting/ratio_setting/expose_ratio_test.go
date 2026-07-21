package ratio_setting

import (
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExposeRatioEnabled_Toggle(t *testing.T) {
	prev := IsExposeRatioEnabled()
	t.Cleanup(func() { SetExposeRatioEnabled(prev) })

	SetExposeRatioEnabled(true)
	assert.True(t, IsExposeRatioEnabled())
	SetExposeRatioEnabled(false)
	assert.False(t, IsExposeRatioEnabled())
}

func TestGetExposedData_BuildsAllSections(t *testing.T) {
	InvalidateExposedDataCache()
	t.Cleanup(InvalidateExposedDataCache)

	data := GetExposedData()
	for _, key := range []string{
		"model_ratio", "completion_ratio", "cache_ratio",
		"create_cache_ratio", "model_price",
	} {
		_, ok := data[key]
		assert.True(t, ok, "exposed data must include %s", key)
	}
	mr, ok := data["model_ratio"].(map[string]float64)
	require.True(t, ok)
	assert.Equal(t, 1.25, mr["gpt-4o"])
}

func TestGetExposedData_FastPathReturnsCachedClone(t *testing.T) {
	InvalidateExposedDataCache()
	t.Cleanup(InvalidateExposedDataCache)

	// First call builds + caches; second call returns via the pre-lock fast path.
	_ = GetExposedData()
	data := GetExposedData()
	mr, ok := data["model_ratio"].(map[string]float64)
	require.True(t, ok)
	assert.Equal(t, 1.25, mr["gpt-4o"])
}

func TestGetExposedData_ReturnsIndependentClone(t *testing.T) {
	InvalidateExposedDataCache()
	t.Cleanup(InvalidateExposedDataCache)

	first := GetExposedData()
	first["injected"] = "x" // mutate the returned top-level map
	second := GetExposedData()
	_, leaked := second["injected"]
	assert.False(t, leaked, "each caller gets a fresh clone of the cached gin.H")
}

func TestGetExposedData_CachedWithinTTL(t *testing.T) {
	InvalidateExposedDataCache()
	t.Cleanup(InvalidateExposedDataCache)

	GetExposedData() // populate cache
	c, ok := exposedData.Load().(*exposedCache)
	require.True(t, ok)
	require.NotNil(t, c)
	assert.True(t, c.expiresAt.After(time.Now()))
	assert.True(t, c.expiresAt.Before(time.Now().Add(exposedDataTTL+time.Second)))
}

func TestInvalidateExposedDataCache(t *testing.T) {
	GetExposedData() // populate
	InvalidateExposedDataCache()
	c, _ := exposedData.Load().(*exposedCache)
	assert.Nil(t, c, "invalidation stores a nil cache pointer")

	// Next read rebuilds successfully.
	data := GetExposedData()
	assert.NotNil(t, data["model_ratio"])
	t.Cleanup(InvalidateExposedDataCache)
}

func TestCloneGinH(t *testing.T) {
	src := gin.H{"a": 1, "b": "two"}
	dst := cloneGinH(src)
	assert.Equal(t, src, dst)
	dst["a"] = 999
	assert.Equal(t, 1, src["a"], "clone must be independent")
}
