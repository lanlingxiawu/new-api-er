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
func SupplementStreamZeroOutput(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, text, audio int) *dto.Usage {
	result := info.StreamResult
	if result == nil || result.UsageSource == "none" || usage == nil || text+audio <= 0 {
		return usage
	}
	effective := effectiveBillingUsage(usage)
	if effective.CompletionTokens > 0 {
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
			u.CandidatesTokenCount = updated.CompletionTokens
			u.TotalTokenCount = u.PromptTokenCount + u.ToolUsePromptTokenCount + u.ThoughtsTokenCount + u.CandidatesTokenCount
			u.CandidatesTokensDetails = []dto.GeminiPromptTokensDetails{{Modality: "TEXT", TokenCount: text}, {Modality: "AUDIO", TokenCount: audio}}
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
