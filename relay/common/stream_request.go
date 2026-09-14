package common

import (
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/tidwall/gjson"
)

// UpdateStreamExpectedChoices 在转换、字段移除及参数覆盖后，从实际出站 data 更新本次流的候选数。
// info 为已安装会话的请求；data 是调用方已有的最终 JSON，只读取计数字段，不保存原文或改写请求。
// 发送前 HTTP 门控尚未开放，因此按会话是否存在隔离非流式/关闭开关；缺失字段清除入口旧值。
func (info *RelayInfo) UpdateStreamExpectedChoices(data []byte) {
	if info == nil || info.StreamSession == nil {
		return
	}
	var value gjson.Result
	switch info.GetFinalRequestRelayFormat() {
	case types.RelayFormatOpenAI:
		value = gjson.GetBytes(data, "n")
	case types.RelayFormatGemini:
		value = gjson.GetBytes(data, "generationConfig.candidateCount")
		// Gemini 参数解析接受 candidate_count；覆盖规则也可能直接使用该别名。
		if alias := gjson.GetBytes(data, "generationConfig.candidate_count"); alias.Exists() && alias.Type != gjson.Null {
			value = alias
		}
	}
	count := 0
	if value.Type == gjson.Number && value.Float() == float64(value.Int()) && value.Int() > 0 && value.Int() <= 1_000_000_000 {
		count = int(value.Int())
	}
	info.StreamSession.mu.Lock()
	info.StreamSession.ExpectedChoices = count
	info.StreamSession.mu.Unlock()
}

// UpdateStreamExpectedImages 从已有 JSON data 的 path 字段更新本次会话的图片期望张数，不记录或改写请求正文。
// path 由调用方按实际协议选择：标准/MiniMax/xAI 为 n，Ali 完整 JSON 为 parameters.n、透传 parameters 子对象为 n。
// 缺省、null 或无效值按默认 1；非法请求仍由原校验/上游处理，未安装会话时跳过且不提前开放 HTTP 门控。
func (info *RelayInfo) UpdateStreamExpectedImages(data []byte, path string) {
	if info == nil || info.StreamSession == nil {
		return
	}
	count := 1
	value := gjson.GetBytes(data, path)
	if value.Type == gjson.Number && value.Float() == float64(value.Int()) && value.Int() > 0 && value.Int() <= 1_000_000_000 {
		count = int(value.Int())
	}
	info.StreamSession.mu.Lock()
	info.StreamSession.ExpectedImages = count
	info.StreamSession.mu.Unlock()
}
