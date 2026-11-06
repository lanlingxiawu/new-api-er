package operation_setting

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestClaudeStreamCompatibilityConfiguration(t *testing.T) {
	previous := ClaudeStreamUpstreamCompatibilityEnabled()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(previous)}))
	})
	require.True(t, previous, "upstream compatibility is enabled without a persisted override")
	for _, enabled := range []bool{false, true} {
		value := strconv.FormatBool(enabled)
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"claude_stream_compat_setting.enabled": value}))
		require.Equal(t, enabled, ClaudeStreamUpstreamCompatibilityEnabled())
		var persisted string
		require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
			if key == "claude_stream_compat_setting.enabled" {
				persisted = value
			}
			return nil
		}))
		require.Equal(t, value, persisted, "the existing option lifecycle preserves the rollback switch")
	}
}
