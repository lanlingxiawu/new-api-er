package model

import "time"

// SchedulerConfig stores configuration and last-run metadata for each scheduled task.
// This is the source of truth for task intervals and enables dynamic hot-reload without restart.
type SchedulerConfig struct {
	ID              int    `gorm:"primaryKey" json:"id"`
	TaskName        string `gorm:"uniqueIndex;type:varchar(128);not null" json:"task_name"`
	Enabled         int    `gorm:"default:1" json:"enabled"`                      // 1=enabled, 0=disabled
	IntervalSeconds int    `gorm:"default:3600" json:"interval_seconds"`           // execution interval
	TimeoutSeconds  int    `gorm:"default:300" json:"timeout_seconds"`             // max execution time before lock expires
	RequiresMaster  int    `gorm:"default:0" json:"requires_master"`               // 1=only one node runs it
	LastRunTime     int64  `gorm:"default:0" json:"last_run_time"`                 // unix timestamp
	LastRunStatus   string `gorm:"type:varchar(32);default:''" json:"last_run_status"` // success|failed|timeout
	LastError       string `gorm:"type:text" json:"last_error"`
	NextRunTime     int64  `gorm:"default:0" json:"next_run_time"` // unix timestamp; 0 = run ASAP
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

func (s *SchedulerConfig) TableName() string {
	return "scheduler_configs"
}

// GetAllSchedulerConfigs returns all task configs.
func GetAllSchedulerConfigs() ([]SchedulerConfig, error) {
	var configs []SchedulerConfig
	err := DB.Find(&configs).Error
	return configs, err
}

// GetSchedulerConfig returns the config for a single task.
func GetSchedulerConfig(taskName string) (*SchedulerConfig, error) {
	var cfg SchedulerConfig
	err := DB.Where("task_name = ?", taskName).First(&cfg).Error
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// UpsertSchedulerConfig inserts default config if the row doesn't exist.
func UpsertSchedulerConfig(cfg *SchedulerConfig) error {
	now := time.Now().Unix()
	cfg.CreatedAt = now
	cfg.UpdatedAt = now

	// Use save to upsert; rely on the unique index to prevent duplicates.
	result := DB.Where("task_name = ?", cfg.TaskName).FirstOrCreate(cfg)
	return result.Error
}

// UpdateSchedulerConfigFields partially updates a task config.
func UpdateSchedulerConfigFields(taskName string, fields map[string]interface{}) error {
	fields["updated_at"] = time.Now().Unix()
	return DB.Model(&SchedulerConfig{}).Where("task_name = ?", taskName).Updates(fields).Error
}
