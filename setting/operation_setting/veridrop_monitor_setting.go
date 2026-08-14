package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type VeridropMonitorSetting struct {
	Enabled                    bool    `json:"enabled"`
	BaseURL                    string  `json:"base_url"`
	AdminToken                 string  `json:"admin_api_key"`
	DefaultMode                string  `json:"default_mode"`
	DefaultOpenAIWireAPI       string  `json:"default_openai_wire_api"`
	IncludeLongContext         bool    `json:"include_long_context"`
	IncludeLongContextExtreme  bool    `json:"include_long_context_extreme"`
	MaxConcurrent              int     `json:"max_concurrent"`
	BatchSize                  int     `json:"batch_size"`
	SubmitTimeoutSeconds       int     `json:"submit_timeout_seconds"`
	PollIntervalSeconds        int     `json:"poll_interval_seconds"`
	JobTimeoutSeconds          int     `json:"job_timeout_seconds"`
	AutoDisableEnabled         bool    `json:"auto_disable_enabled"`
	AutoDisableFailedThreshold float64 `json:"auto_disable_failed_threshold"`
}

var veridropMonitorSetting = VeridropMonitorSetting{
	Enabled:                    false,
	BaseURL:                    "",
	AdminToken:                 "",
	DefaultMode:                "quick",
	DefaultOpenAIWireAPI:       "chat_completions",
	IncludeLongContext:         false,
	IncludeLongContextExtreme:  false,
	MaxConcurrent:              2,
	BatchSize:                  100,
	SubmitTimeoutSeconds:       30,
	PollIntervalSeconds:        5,
	JobTimeoutSeconds:          300,
	AutoDisableEnabled:         false,
	AutoDisableFailedThreshold: 40,
}

func init() {
	config.GlobalConfig.Register("veridrop_monitor_setting", &veridropMonitorSetting)
}

func GetVeridropMonitorSetting() *VeridropMonitorSetting {
	if veridropMonitorSetting.MaxConcurrent < 1 {
		veridropMonitorSetting.MaxConcurrent = 1
	}
	if veridropMonitorSetting.MaxConcurrent > 20 {
		veridropMonitorSetting.MaxConcurrent = 20
	}
	if veridropMonitorSetting.BatchSize < 1 {
		veridropMonitorSetting.BatchSize = 100
	}
	if veridropMonitorSetting.BatchSize > 200 {
		veridropMonitorSetting.BatchSize = 200
	}
	if veridropMonitorSetting.SubmitTimeoutSeconds < 1 {
		veridropMonitorSetting.SubmitTimeoutSeconds = 30
	}
	if veridropMonitorSetting.PollIntervalSeconds < 1 {
		veridropMonitorSetting.PollIntervalSeconds = 5
	}
	if veridropMonitorSetting.JobTimeoutSeconds < veridropMonitorSetting.PollIntervalSeconds {
		veridropMonitorSetting.JobTimeoutSeconds = 300
	}
	if veridropMonitorSetting.AutoDisableFailedThreshold < 0 {
		veridropMonitorSetting.AutoDisableFailedThreshold = 0
	}
	if veridropMonitorSetting.AutoDisableFailedThreshold > 100 {
		veridropMonitorSetting.AutoDisableFailedThreshold = 100
	}
	return &veridropMonitorSetting
}
