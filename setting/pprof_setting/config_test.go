package pprof_setting

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func restoreSetting(t *testing.T) {
	t.Helper()
	original := pprofSetting
	t.Cleanup(func() { pprofSetting = original })
}

func TestDefaultDisabled(t *testing.T) {
	assert.False(t, Get().Enabled)
	assert.False(t, IsEnabled())
}

// ENABLE_PPROF=true 仅作为启动默认值。
func TestApplyEnvDefaults(t *testing.T) {
	restoreSetting(t)

	os.Unsetenv("ENABLE_PPROF")
	pprofSetting = PprofSetting{}
	ApplyEnvDefaults()
	assert.False(t, IsEnabled())

	t.Setenv("ENABLE_PPROF", "false")
	ApplyEnvDefaults()
	assert.False(t, IsEnabled())

	t.Setenv("ENABLE_PPROF", "true")
	ApplyEnvDefaults()
	assert.True(t, IsEnabled())
}
