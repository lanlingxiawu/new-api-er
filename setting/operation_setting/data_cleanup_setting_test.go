package operation_setting

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestErrorLogCleanupSettings(t *testing.T) {
	original := dataCleanupSetting
	t.Cleanup(func() {
		config.WithConfigDraft(func() { dataCleanupSetting = original; dataCleanupSnapshot.Publish(original) })
	})
	require.Equal(t, 5000, GetDataCleanupSetting().ErrorLogBatchSize)
	require.Equal(t, 5, GetDataCleanupSetting().ErrorLogBatchTimeoutSec)
	require.False(t, GetDataCleanupSetting().ErrorLogAutoContinue)
	require.Equal(t, 100, GetDataCleanupSetting().BatchSize)
	require.Equal(t, 3, GetDataCleanupSetting().BatchTimeoutSec)
	require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"error_log_batch_size": "0", "error_log_batch_timeout_sec": "0"}))
	require.Equal(t, 5000, GetDataCleanupSetting().ErrorLogBatchSize)
	require.Equal(t, 5, GetDataCleanupSetting().ErrorLogBatchTimeoutSec)
	require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"error_log_batch_size": "10001", "error_log_batch_timeout_sec": "11"}))
	require.Equal(t, 10000, GetDataCleanupSetting().ErrorLogBatchSize)
	require.Equal(t, 10, GetDataCleanupSetting().ErrorLogBatchTimeoutSec)
}

func TestErrorLogCleanupSettingsConcurrentUpdates(t *testing.T) {
	original := GetDataCleanupSetting()
	t.Cleanup(func() {
		config.WithConfigDraft(func() { dataCleanupSetting = original; dataCleanupSnapshot.Publish(original) })
	})
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 500 {
			_ = config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"error_log_batch_size": "2000", "error_log_batch_timeout_sec": "2"})
			_ = config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"error_log_batch_size": "5000", "error_log_batch_timeout_sec": "5"})
		}
	})
	defer workers.Wait()
	for range 2000 {
		value := GetDataCleanupSetting()
		require.Equal(t, value.ErrorLogBatchSize/1000, value.ErrorLogBatchTimeoutSec, "a batch must observe one coherent configuration snapshot")
	}
}

func TestErrorLogCleanupSettingValidation(t *testing.T) {
	for _, field := range []string{"error_log_batch_size", "error_log_batch_timeout_sec"} {
		for _, value := range []string{"", "0", "-1", "1.5", "abc", "10001"} {
			require.ErrorIs(t, ValidateErrorLogCleanupOption("data_cleanup_setting."+field, value), ErrErrorLogCleanupSetting)
		}
		require.NoError(t, ValidateErrorLogCleanupOption("data_cleanup_setting."+field, "1"))
	}
	require.NoError(t, ValidateErrorLogCleanupOption("data_cleanup_setting.error_log_batch_size", "10000"))
	require.NoError(t, ValidateErrorLogCleanupOption("data_cleanup_setting.error_log_batch_timeout_sec", "10"))
	require.ErrorIs(t, ValidateErrorLogCleanupOption("data_cleanup_setting.error_log_batch_timeout_sec", "11"), ErrErrorLogCleanupSetting)
	for _, value := range []string{"true", "false"} {
		require.NoError(t, ValidateErrorLogCleanupOption("data_cleanup_setting.error_log_auto_continue", value))
	}
	require.ErrorIs(t, ValidateErrorLogCleanupOption("data_cleanup_setting.error_log_auto_continue", "invalid"), ErrErrorLogCleanupSetting)
	require.NoError(t, ValidateErrorLogCleanupOption("unrelated", "anything"))
}
