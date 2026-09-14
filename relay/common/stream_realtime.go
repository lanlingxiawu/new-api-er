package common

import "github.com/tidwall/gjson"

// IsRealtimeRecoverableError 判断已解析对象 v 是否为可恢复的客户端事件错误。
// 仅供 Realtime 使用；请求错误只透传，不结束活动轮次、不采纳其中的用量、不占用终止 error 去重位。
func IsRealtimeRecoverableError(v gjson.Result) bool {
	return v.Get("type").String() == "error" && v.Get("error.type").String() == "invalid_request_error"
}

// isRealtimeRoundCompletion 判断 v 是否明确结束当前 Realtime 轮次而非连接；不改写原始 status。
// 取消以及达到输出 token 上限或内容过滤的 incomplete 保留正常轮次计量；显式错误与未知状态仍交给异常流程。
func isRealtimeRoundCompletion(v gjson.Result) bool {
	if v.Get("type").String() != "response.done" {
		return false
	}
	for _, path := range []string{"error", "response.error", "response.status_details.error"} {
		if e := v.Get(path); e.Exists() && e.Type != gjson.Null {
			return false
		}
	}
	switch v.Get("response.status").String() {
	case "completed", "cancelled":
		return true
	case "incomplete":
		reason := v.Get("response.status_details.reason").String()
		return reason == "max_output_tokens" || reason == "content_filter"
	}
	return false
}
