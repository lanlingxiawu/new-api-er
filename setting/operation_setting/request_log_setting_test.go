package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestRequestLogRestartSetting(t *testing.T) {
	require.False(t, GetRequestLogSetting().ClearOnRestart)
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": "false"}))
	})
	for _, value := range []string{"true", "false"} {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"request_log_setting.clear_on_restart": value}))
		require.Equal(t, value == "true", GetRequestLogSetting().ClearOnRestart)
		found := false
		require.NoError(t, config.GlobalConfig.SaveToDB(func(key, saved string) error {
			if key == "request_log_setting.clear_on_restart" {
				found = true
				require.Equal(t, value, saved)
			}
			return nil
		}))
		require.True(t, found)
	}
}
