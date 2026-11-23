package operation_setting

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/setting/config"
)

var ErrErrorLogCleanupSetting = errors.New("invalid error log cleanup setting")

// Disabling cleanup stops new batches; permanent deduplication receipts always
// remain effective, including after this switch is turned off.
type DataCleanupSetting struct {
	Enabled                 bool `json:"enabled"`
	BatchSize               int  `json:"batch_size"`
	BatchSleepMS            int  `json:"batch_sleep_ms"`
	BatchTimeoutSec         int  `json:"batch_timeout_sec"`
	TaskTimeoutSec          int  `json:"task_timeout_sec"`
	ErrorLogBatchSize       int  `json:"error_log_batch_size"`
	ErrorLogBatchTimeoutSec int  `json:"error_log_batch_timeout_sec"`
	ErrorLogAutoContinue    bool `json:"error_log_auto_continue"`
}

var dataCleanupSetting = DataCleanupSetting{Enabled: true, BatchSize: 100, BatchSleepMS: 200, BatchTimeoutSec: 3, TaskTimeoutSec: 1800, ErrorLogBatchSize: 5000, ErrorLogBatchTimeoutSec: 5}

var dataCleanupSnapshot config.Snapshot[DataCleanupSetting]

func init() {
	config.GlobalConfig.RegisterSnapshot("data_cleanup_setting", &dataCleanupSetting, func() {
		dataCleanupSnapshot.Publish(dataCleanupSetting)
	})
}

func ValidateErrorLogCleanupOption(key, value string) error {
	limit := 0
	switch key {
	case "data_cleanup_setting.error_log_batch_size":
		limit = 10000
	case "data_cleanup_setting.error_log_batch_timeout_sec":
		limit = 10
	case "data_cleanup_setting.error_log_auto_continue":
		if value != "true" && value != "false" {
			return ErrErrorLogCleanupSetting
		}
		return nil
	default:
		return nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > limit {
		return ErrErrorLogCleanupSetting
	}
	return nil
}

func GetDataCleanupSetting() DataCleanupSetting {
	s := *dataCleanupSnapshot.Load()
	s.BatchSize = min(max(s.BatchSize, 1), 500)
	s.BatchSleepMS = min(max(s.BatchSleepMS, 100), 5000)
	s.BatchTimeoutSec = min(max(s.BatchTimeoutSec, 1), 10)
	s.TaskTimeoutSec = min(max(s.TaskTimeoutSec, 60), 7200)
	if s.ErrorLogBatchSize <= 0 {
		s.ErrorLogBatchSize = 5000
	}
	if s.ErrorLogBatchTimeoutSec <= 0 {
		s.ErrorLogBatchTimeoutSec = 5
	}
	s.ErrorLogBatchSize = min(s.ErrorLogBatchSize, 10000)
	s.ErrorLogBatchTimeoutSec = min(s.ErrorLogBatchTimeoutSec, 10)
	return s
}
