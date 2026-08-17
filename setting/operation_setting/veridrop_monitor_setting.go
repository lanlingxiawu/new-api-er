package operation_setting

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

type VeridropMonitorSetting struct {
	Enabled                   bool   `json:"enabled"`
	BaseURL                   string `json:"base_url"`
	AdminToken                string `json:"admin_api_key"`
	DefaultMode               string `json:"default_mode"`
	DefaultOpenAIWireAPI      string `json:"default_openai_wire_api"`
	IncludeLongContext        bool   `json:"include_long_context"`
	IncludeLongContextExtreme bool   `json:"include_long_context_extreme"`
	MaxConcurrent             int    `json:"max_concurrent"`
	BatchSize                 int    `json:"batch_size"`
	SubmitTimeoutSeconds      int    `json:"submit_timeout_seconds"`
	PollIntervalSeconds       int    `json:"poll_interval_seconds"`
	JobTimeoutSeconds         int    `json:"job_timeout_seconds"`
	AutoDetectionEnabled      bool   `json:"auto_detection_enabled"`
	DetectionIntervalMinutes  int    `json:"detection_interval_minutes"`
}

var veridropMonitorSetting = VeridropMonitorSetting{
	Enabled:                   false,
	BaseURL:                   "",
	AdminToken:                "",
	DefaultMode:               "quick",
	DefaultOpenAIWireAPI:      "chat_completions",
	IncludeLongContext:        false,
	IncludeLongContextExtreme: false,
	MaxConcurrent:             2,
	BatchSize:                 100,
	SubmitTimeoutSeconds:      30,
	PollIntervalSeconds:       5,
	JobTimeoutSeconds:         300,
	AutoDetectionEnabled:      false,
	DetectionIntervalMinutes:  1440,
}

var veridropMonitorSnapshot config.Snapshot[VeridropMonitorSetting]

func init() {
	config.GlobalConfig.RegisterSnapshot("veridrop_monitor_setting", &veridropMonitorSetting, publishVeridropMonitorSetting)
}

func normalizedVeridropMonitorSetting(setting VeridropMonitorSetting) VeridropMonitorSetting {
	if setting.MaxConcurrent < 1 {
		setting.MaxConcurrent = 1
	}
	if setting.MaxConcurrent > 20 {
		setting.MaxConcurrent = 20
	}
	if setting.BatchSize < 1 {
		setting.BatchSize = 100
	}
	if setting.BatchSize > 200 {
		setting.BatchSize = 200
	}
	if setting.SubmitTimeoutSeconds < 1 {
		setting.SubmitTimeoutSeconds = 30
	}
	if setting.PollIntervalSeconds < 1 {
		setting.PollIntervalSeconds = 5
	}
	if setting.JobTimeoutSeconds < setting.PollIntervalSeconds {
		setting.JobTimeoutSeconds = 300
	}
	if setting.DetectionIntervalMinutes <= 0 {
		setting.DetectionIntervalMinutes = 1440
	}
	if setting.DetectionIntervalMinutes < 15 {
		setting.DetectionIntervalMinutes = 15
	}
	if setting.DetectionIntervalMinutes > 43200 {
		setting.DetectionIntervalMinutes = 43200
	}
	return setting
}

// publishVeridropMonitorSetting runs while the config draft lock is held.
func publishVeridropMonitorSetting() {
	veridropMonitorSnapshot.Publish(normalizedVeridropMonitorSetting(veridropMonitorSetting))
}

func GetVeridropMonitorSnapshot() *VeridropMonitorSetting {
	return veridropMonitorSnapshot.Load()
}

func GetVeridropMonitorSetting() VeridropMonitorSetting {
	var out VeridropMonitorSetting
	config.WithConfigDraft(func() { out = veridropMonitorSetting })
	return normalizedVeridropMonitorSetting(out)
}

func ReplaceVeridropMonitorSetting(setting VeridropMonitorSetting) {
	config.WithConfigDraft(func() {
		veridropMonitorSetting = setting
		publishVeridropMonitorSetting()
	})
}

func ValidateVeridropMonitorSetting(setting VeridropMonitorSetting) error {
	baseURL := strings.TrimSpace(setting.BaseURL)
	if setting.Enabled && baseURL == "" {
		return fmt.Errorf("veridrop base URL is required when detection is enabled")
	}
	if baseURL != "" {
		parsed, err := url.ParseRequestURI(baseURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("invalid veridrop base URL")
		}
	}
	if mode := strings.TrimSpace(setting.DefaultMode); mode != "quick" && mode != "standard" && mode != "full" {
		return fmt.Errorf("invalid veridrop detection mode")
	}
	if wireAPI := strings.TrimSpace(setting.DefaultOpenAIWireAPI); wireAPI != "chat_completions" && wireAPI != "responses" {
		return fmt.Errorf("invalid veridrop OpenAI wire API")
	}
	if setting.MaxConcurrent < 1 || setting.MaxConcurrent > 20 {
		return fmt.Errorf("veridrop concurrency must be between 1 and 20")
	}
	if setting.BatchSize < 1 || setting.BatchSize > 200 {
		return fmt.Errorf("veridrop batch size must be between 1 and 200")
	}
	if setting.SubmitTimeoutSeconds < 1 || setting.SubmitTimeoutSeconds > 300 {
		return fmt.Errorf("veridrop submit timeout must be between 1 and 300 seconds")
	}
	if setting.PollIntervalSeconds < 1 || setting.PollIntervalSeconds > 60 {
		return fmt.Errorf("veridrop poll interval must be between 1 and 60 seconds")
	}
	if setting.JobTimeoutSeconds < setting.PollIntervalSeconds || setting.JobTimeoutSeconds > 3600 {
		return fmt.Errorf("veridrop job timeout must be between the poll interval and 3600 seconds")
	}
	if setting.DetectionIntervalMinutes < 15 || setting.DetectionIntervalMinutes > 43200 {
		return fmt.Errorf("veridrop detection interval must be between 15 and 43200 minutes")
	}
	return nil
}
