package common

import "sync/atomic"

// StreamResponseGate 只在当前渠道尝试内共享实际响应资格；零值等待成功 HTTP/WS 交换。
// 指针在发布会话/采集器前绑定，之后只通过原子状态更新，SDK 重试不修改请求级配置。
type StreamResponseGate struct {
	accepted atomic.Bool // 最近一次真实交换是否为 HTTP 200 或成功 WS 101，而非映射/占位状态码。
}

// AllowsStream 查询新流式流程的资格，无参数；nil 保留未使用门控的旧采集器及独立协议组件行为。
func (g *StreamResponseGate) AllowsStream() bool {
	return g == nil || g.accepted.Load()
}
