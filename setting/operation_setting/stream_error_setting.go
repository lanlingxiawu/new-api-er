package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// StreamErrorSetting 控制全部渠道的流式异常处理与响应采集；入口非流式请求不启用新流程。
type StreamErrorSetting struct {
	Enabled         bool `json:"enabled"`          // 默认开启；关闭后回到原渠道处理，Claude 原有开关仍兼容。
	CaptureResponse bool `json:"capture_response"` // 默认开启；有界缓存上游响应头/body 和实际下游 body，仅新流程明确上游异常时保存原始内容。
}

var streamErrorSetting = StreamErrorSetting{Enabled: true, CaptureResponse: true} // 持久化配置对象，仅由配置管理器更新。
var streamErrorSnapshot config.Snapshot[StreamErrorSetting]                       // 供并发请求读取的原子只读快照。

// init 注册持久化设置并发布只读快照；无参数和返回值，不为每次请求访问数据库。
func init() {
	config.GlobalConfig.RegisterSnapshot("stream_error_setting", &streamErrorSetting, func() { streamErrorSnapshot.Publish(streamErrorSetting) })
}

// GetStreamErrorSetting 返回当前设置快照，调用者在请求内冻结布尔值。
func GetStreamErrorSetting() *StreamErrorSetting { return streamErrorSnapshot.Load() }
