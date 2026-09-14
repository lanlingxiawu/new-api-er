package common

import "context"

// StreamPrivateContextKey 标记统一流式诊断请求，阻止原始流数据进入普通应用及请求日志。
const StreamPrivateContextKey = "unified_stream_private"

// IsPrivateStream 读取请求 ctx 的私有流标记，供普通日志避开请求及响应原文；不负责判定上游异常或允许诊断落库。
func IsPrivateStream(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(StreamPrivateContextKey).(bool)
	return value
}
