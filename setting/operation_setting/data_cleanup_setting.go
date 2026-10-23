package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// Disabling cleanup stops new batches; permanent deduplication receipts always
// remain effective, including after this switch is turned off.
type DataCleanupSetting struct {
	Enabled         bool `json:"enabled"`
	BatchSize       int  `json:"batch_size"`
	BatchSleepMS    int  `json:"batch_sleep_ms"`
	BatchTimeoutSec int  `json:"batch_timeout_sec"`
	TaskTimeoutSec  int  `json:"task_timeout_sec"`
}

var dataCleanupSetting = DataCleanupSetting{Enabled: true, BatchSize: 100, BatchSleepMS: 200, BatchTimeoutSec: 3, TaskTimeoutSec: 1800}

func init() { config.GlobalConfig.Register("data_cleanup_setting", &dataCleanupSetting) }

func GetDataCleanupSetting() DataCleanupSetting {
	s := dataCleanupSetting
	s.BatchSize = min(max(s.BatchSize, 1), 500)
	s.BatchSleepMS = min(max(s.BatchSleepMS, 100), 5000)
	s.BatchTimeoutSec = min(max(s.BatchTimeoutSec, 1), 10)
	s.TaskTimeoutSec = min(max(s.TaskTimeoutSec, 60), 7200)
	return s
}
