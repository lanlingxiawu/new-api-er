package model_setting

import (
	"fmt"
	"maps"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const defaultGeminiSafetySetting = "OFF"

var validGeminiSafetySettings = map[string]struct{}{
	"OFF":                              {},
	"BLOCK_NONE":                       {},
	"BLOCK_ONLY_HIGH":                  {},
	"BLOCK_MEDIUM_AND_ABOVE":           {},
	"BLOCK_LOW_AND_ABOVE":              {},
	"HARM_BLOCK_THRESHOLD_UNSPECIFIED": {},
}

// GeminiSettings defines Gemini model configuration. 注意bool要以enabled结尾才可以生效编辑
type GeminiSettings struct {
	SafetySettings                        map[string]string `json:"safety_settings"`
	VersionSettings                       map[string]string `json:"version_settings"`
	SupportedImagineModels                []string          `json:"supported_imagine_models"`
	ThinkingAdapterEnabled                bool              `json:"thinking_adapter_enabled"`
	ThinkingAdapterBudgetTokensPercentage float64           `json:"thinking_adapter_budget_tokens_percentage"`
	FunctionCallThoughtSignatureEnabled   bool              `json:"function_call_thought_signature_enabled"`
	RemoveFunctionResponseIdEnabled       bool              `json:"remove_function_response_id_enabled"`
}

// 默认配置
var defaultGeminiSettings = GeminiSettings{
	SafetySettings: map[string]string{
		"default": defaultGeminiSafetySetting,
	},
	VersionSettings: map[string]string{
		"default":        "v1beta",
		"gemini-1.0-pro": "v1",
	},
	SupportedImagineModels: []string{
		"gemini-2.0-flash-exp-image-generation",
		"gemini-2.0-flash-exp",
		"gemini-3-pro-image-preview",
		"gemini-3-pro-image",
		"gemini-2.5-flash-image",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-image-preview",
	},
	ThinkingAdapterEnabled:                false,
	ThinkingAdapterBudgetTokensPercentage: 0.6,
	FunctionCallThoughtSignatureEnabled:   true,
	RemoveFunctionResponseIdEnabled:       true,
}

// 全局实例
var geminiSettings = defaultGeminiSettings

var geminiSnapshot config.Snapshot[GeminiSettings]

func init() {
	// 注册到全局配置管理器，并登记快照发布函数。
	// 这个模块几乎全是 map/slice 字段且被 relay 路径读取，
	// 原地改写时读侧可能拿到半个值。
	config.GlobalConfig.RegisterSnapshot("gemini", &geminiSettings, publishGeminiSettings)
}

// publishGeminiSettings 只允许在配置草稿锁内调用（由 RegisterSnapshot 保证）。
// map 与 slice 字段复制后再发布：ConfigManager 反序列化 slice 时会复用草稿原有的底层数组，
// 共享底层存储会让已发布的快照随草稿被原地改写。
func publishGeminiSettings() {
	published := geminiSettings
	published.SafetySettings = maps.Clone(geminiSettings.SafetySettings)
	published.VersionSettings = maps.Clone(geminiSettings.VersionSettings)
	published.SupportedImagineModels = slices.Clone(geminiSettings.SupportedImagineModels)
	geminiSnapshot.Publish(published)
}

// GetGeminiSettings 返回不可变快照。通过它写入不会生效——
// 配置变更必须走管理接口，由 ConfigManager 改草稿后重新发布。
func GetGeminiSettings() *GeminiSettings {
	return geminiSnapshot.Load()
}

// ReplaceGeminiSettings 整体替换配置并立即重新发布快照（供测试使用）。
func ReplaceGeminiSettings(s GeminiSettings) {
	config.WithConfigDraft(func() {
		geminiSettings = s
		publishGeminiSettings()
	})
}

// GetGeminiSafetySetting 从已发布快照读取 key 的安全阈值（relay 路径调用，不读草稿）；
// key 未配置或为空时回落到 "default"，再回落到 OFF。
func GetGeminiSafetySetting(key string) string {
	settings := GetGeminiSettings().SafetySettings
	if value := settings[key]; value != "" {
		return value
	}
	if value := settings["default"]; value != "" {
		return value
	}
	return defaultGeminiSafetySetting
}

// ValidateGeminiSafetySettings validates the JSON persisted by the option API.
// Empty values remain valid because read-time fallback returns the default.
func ValidateGeminiSafetySettings(value string) error {
	var settings map[string]string
	if err := common.UnmarshalJsonStr(value, &settings); err != nil {
		return fmt.Errorf("Gemini safety settings must be a JSON string map: %w", err)
	}
	if settings == nil {
		return fmt.Errorf("Gemini safety settings must be a JSON string map")
	}
	for category, threshold := range settings {
		if threshold == "" {
			continue
		}
		if _, ok := validGeminiSafetySettings[threshold]; !ok {
			return fmt.Errorf("invalid Gemini safety threshold %q for %q", threshold, category)
		}
	}
	return nil
}

// GetGeminiVersionSetting 从已发布快照读取 key 的 API 版本（relay 路径调用，不读草稿）；未配置时返回 "default" 的值。
func GetGeminiVersionSetting(key string) string {
	versions := GetGeminiSettings().VersionSettings
	if value, ok := versions[key]; ok {
		return value
	}
	return versions["default"]
}

// IsGeminiModelSupportImagine 按已发布快照判断 model 是否在图片生成模型列表中（精确匹配，relay 路径调用，不读草稿）。
func IsGeminiModelSupportImagine(model string) bool {
	return slices.Contains(GetGeminiSettings().SupportedImagineModels, model)
}
