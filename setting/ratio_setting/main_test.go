package ratio_setting

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

// TestMain seeds every ratio map from its defaults exactly once, mirroring what
// InitRatioSettings does at process start. Individual tests that mutate a global
// map MUST snapshot/restore it (see helpers below) so ordering never leaks state.
func TestMain(m *testing.M) {
	InitRatioSettings()
	os.Exit(m.Run())
}

// snapshotFloatMap copies a *RWMap[string,float64] so a test can restore it after
// mutation (Update*ByJSONString replaces the whole backing map).
func snapshotFloatMap(m *types.RWMap[string, float64]) map[string]float64 {
	return m.ReadAll()
}

func restoreFloatMap(m *types.RWMap[string, float64], snap map[string]float64) {
	m.Clear()
	m.AddAll(snap)
}

// withSelfUseMode temporarily flips operation_setting.SelfUseModeEnabled and
// returns a restore func.
func withSelfUseMode(t *testing.T, enabled bool) {
	t.Helper()
	prev := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = enabled
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = prev })
}

// resetUserGroupRatioCache clears the package-global memo cache so cache/sweep
// tests start from a clean slate. White-box access to unexported globals.
func resetUserGroupRatioCache() {
	parsedUserGroupRatios.Range(func(k, _ any) bool {
		parsedUserGroupRatios.Delete(k)
		return true
	})
	parsedUserGroupRatiosCount.Store(0)
	parsedUserGroupRatiosSweep.Store(false)
	parsedUserGroupRatiosMax.Store(defaultUserGroupRatioCacheMax)
}
