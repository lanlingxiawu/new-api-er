package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestErrorLogCleanupOptionValidation(t *testing.T) {
	require.ErrorIs(t, validateOptionValue("data_cleanup_setting.error_log_batch_size", "10001"), operation_setting.ErrErrorLogCleanupSetting)
	require.ErrorIs(t, validateOptionValue("data_cleanup_setting.error_log_batch_timeout_sec", "0"), operation_setting.ErrErrorLogCleanupSetting)
	require.ErrorIs(t, validateOptionValue("data_cleanup_setting.error_log_auto_continue", "invalid"), operation_setting.ErrErrorLogCleanupSetting)
	require.NoError(t, validateOptionValue("data_cleanup_setting.error_log_batch_size", "2000"))
}
