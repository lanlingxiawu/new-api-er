package operation_setting

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestClaudeStreamValidationConfiguration(t *testing.T) {
	previous := ClaudeStreamValidationEnabled()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_validation_setting", map[string]string{"enabled": strconv.FormatBool(previous)}))
	})
	for _, enabled := range []bool{false, true} {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"claude_stream_validation_setting.enabled": strconv.FormatBool(enabled)}))
		require.Equal(t, enabled, ClaudeStreamValidationEnabled())
	}
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"claude_stream_setting.enabled": "false", "stream_error_setting.enabled": "false"}))
	require.True(t, ClaudeStreamValidationEnabled(), "retired settings must not change the new default")
}
