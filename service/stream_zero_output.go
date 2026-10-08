package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

// SupplementStreamZeroOutput 补充有实际内容但计费用量为零的输出，保留输入与缓存证据。
// text/audio 是调用方按终止原因选择的本地计量；不对媒体原始字节估算。
// 用于正常结束的流：上游给出的非零输出是完整计量，不再与本地估算比较。
func SupplementStreamZeroOutput(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, text, audio int) *dto.Usage {
	return supplementStreamOutput(c, info, usage, text, audio, false)
}

// SupplementStreamPartialOutput 用于异常结束（上游中途报错、用户断开、我方超时）的流：
// 输出取上游确认值与本地估算值中较大者。异常结束时上游的确认输出通常只来自开头的
// message_start（Claude 恒为 1）或中途的累计值，远小于已交付/已接收的内容；主分支在
// 未收到结束事件时同样取两者较大值（relay-claude.go HandleStreamFinalResponse）。
// 参数与 SupplementStreamZeroOutput 相同；确认值已不小于估算时原样返回。
func SupplementStreamPartialOutput(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, text, audio int) *dto.Usage {
	return supplementStreamOutput(c, info, usage, text, audio, true)
}

// supplementStreamOutput 把计费输出提高到 text+audio：raise=false 只在输出为 0 时补，
// raise=true 在输出低于估算时补。输入、缓存与推理细分保持不变，嵌套计费对象同步改写并标记为估算。
func supplementStreamOutput(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, text, audio int, raise bool) *dto.Usage {
	result := info.StreamResult
	if result == nil || result.UsageSource == "none" || usage == nil || text+audio <= 0 {
		return usage
	}
	effective := effectiveBillingUsage(usage)
	if effective.CompletionTokens > 0 && (!raise || effective.CompletionTokens >= text+audio) {
		return usage
	}
	updated := *effective
	updated.CompletionTokens = text + audio
	updated.OutputTokens = updated.CompletionTokens
	updated.TotalTokens = updated.PromptTokens + updated.CompletionTokens
	updated.CompletionTokenDetails.TextTokens = text
	updated.CompletionTokenDetails.AudioTokens = audio
	updated.BillingUsage = dto.CloneBillingUsage(usage.BillingUsage)
	if updated.BillingUsage == nil && updated.UsageSemantic == dto.BillingUsageSemanticAnthropic {
		// 全零 Claude 证据的构造器原本返回 nil；补估后才创建非零计费对象。
		updated.BillingUsage = dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: updated.PromptTokens, OutputTokens: updated.CompletionTokens})
	}
	if billing := updated.BillingUsage; billing != nil {
		billing.Estimated = true
		if u := billing.OpenAIUsage; u != nil {
			u.CompletionTokens = updated.CompletionTokens
			u.OutputTokens = updated.CompletionTokens
			u.TotalTokens = updated.TotalTokens
			u.CompletionTokenDetails = updated.CompletionTokenDetails
		}
		if u := billing.ClaudeUsage; u != nil {
			u.OutputTokens = updated.CompletionTokens
		}
		if u := billing.GeminiUsageMetadata; u != nil {
			// Gemini 计费输出 = candidates + thoughts，thoughts 保留确认值，只补 candidates。
			u.CandidatesTokenCount = max(0, updated.CompletionTokens-u.ThoughtsTokenCount)
			u.TotalTokenCount = u.PromptTokenCount + u.ToolUsePromptTokenCount + u.ThoughtsTokenCount + u.CandidatesTokenCount
			u.CandidatesTokensDetails = []dto.GeminiPromptTokensDetails{{Modality: "TEXT", TokenCount: max(0, u.CandidatesTokenCount-audio)}, {Modality: "AUDIO", TokenCount: audio}}
		}
	}
	if result.ConfirmedUsage {
		result.UsageSource = "mixed"
	}
	if result.Diagnostic.EstimatedUsage == nil {
		result.Diagnostic.EstimatedUsage = make(map[string]int)
	}
	result.Diagnostic.EstimatedUsage["output_tokens"] = updated.CompletionTokens
	result.Diagnostic.EstimatedUsage["output_text_tokens"] = text
	result.Diagnostic.EstimatedUsage["output_audio_tokens"] = audio
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	return &updated
}
