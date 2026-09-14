package common

import "github.com/tidwall/gjson"

// IsResponsesLimitCompletion 判断已解析对象 v 是否为正常限制终态，不改写上游状态或正文。
// 支持 Responses 的 incomplete/done 事件与完整 response 对象；只接受明确上限/过滤原因，显式错误优先。
// 返回 true 仅确认终态语义，完整 JSON 的 output 结构、usage 合法性和 SSE event:error 仍由调用方校验。
func IsResponsesLimitCompletion(v gjson.Result) bool {
	response := v
	switch v.Get("type").String() {
	case "response.incomplete", "response.done":
		response = v.Get("response")
	default:
		if v.Get("object").String() != "response" || v.Get("type").Exists() {
			return false
		}
	}
	if !response.IsObject() || response.Get("status").String() != "incomplete" {
		return false
	}
	for _, object := range []gjson.Result{v, response} {
		if err := object.Get("error"); err.Exists() && err.Type != gjson.Null {
			return false
		}
	}
	switch response.Get("incomplete_details.reason").String() {
	case "max_output_tokens", "content_filter":
		return true
	}
	return false
}
