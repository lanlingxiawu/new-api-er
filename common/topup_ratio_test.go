package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveTopupRatio(t *testing.T) {
	t.Helper()
	topupGroupRatioMutex.Lock()
	orig := topupGroupRatio
	topupGroupRatioMutex.Unlock()
	t.Cleanup(func() {
		topupGroupRatioMutex.Lock()
		topupGroupRatio = orig
		topupGroupRatioMutex.Unlock()
	})
}

func TestTopupGroupRatio2JSONString(t *testing.T) {
	saveTopupRatio(t)
	topupGroupRatioMutex.Lock()
	topupGroupRatio = map[string]float64{"default": 1.5}
	topupGroupRatioMutex.Unlock()

	assert.JSONEq(t, `{"default":1.5}`, TopupGroupRatio2JSONString())
}

func TestUpdateTopupGroupRatioByJSONString(t *testing.T) {
	saveTopupRatio(t)

	require.NoError(t, UpdateTopupGroupRatioByJSONString(`{"vip":2,"svip":3}`))
	assert.Equal(t, 2.0, GetTopupGroupRatio("vip"))
	assert.Equal(t, 3.0, GetTopupGroupRatio("svip"))

	// invalid JSON returns error
	assert.Error(t, UpdateTopupGroupRatioByJSONString(`not-json`))
}

func TestGetTopupGroupRatio(t *testing.T) {
	saveTopupRatio(t)
	topupGroupRatioMutex.Lock()
	topupGroupRatio = map[string]float64{"default": 1, "gold": 4}
	topupGroupRatioMutex.Unlock()

	assert.Equal(t, 4.0, GetTopupGroupRatio("gold"))
	// unknown group falls back to 1
	assert.Equal(t, 1.0, GetTopupGroupRatio("unknown"))
}
