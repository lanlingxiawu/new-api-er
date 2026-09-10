package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// ClaudeStreamSetting 分别控制严格流式处理和原始响应采集，两项开关互相独立。
type ClaudeStreamSetting struct {
	Enabled         bool `json:"enabled"`          // 默认 true；控制原生 Claude 流式协议校验和异常结算路径。
	CaptureResponse bool `json:"capture_response"` // 默认 true；控制只读采集上游响应头/body，不决定严格解析是否启用。
}

// claudeStreamSetting 保存配置注册初值，运行时通过配置管理器发布快照。
var claudeStreamSetting = ClaudeStreamSetting{Enabled: true, CaptureResponse: true}

// claudeStreamSnapshot 提供并发读取使用的配置快照，调用方在请求内冻结所需布尔值。
var claudeStreamSnapshot config.Snapshot[ClaudeStreamSetting]

// init 注册 Claude 开关并在配置更新时发布快照。
// 无参数和返回值；注册的无参回调只发布当前配置结构，不执行中转请求。
func init() {
	config.GlobalConfig.RegisterSnapshot("claude_stream_setting", &claudeStreamSetting, func() { claudeStreamSnapshot.Publish(claudeStreamSetting) })
}

// GetClaudeStreamSetting 取得配置管理器已发布的 Claude 流式/采集设置快照。
// 无参数；返回当前配置指针，调用者按请求读取或冻结字段，不长期持有它作为跨请求可变配置。
func GetClaudeStreamSetting() *ClaudeStreamSetting { return claudeStreamSnapshot.Load() }
