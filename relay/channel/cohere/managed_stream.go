package cohere

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// managedCohereStream 以有界行读取替代旧双无缓冲通道，取消后等待读者退出。
// 参数 c 是下游上下文，resp 是原始 NDJSON，info 为请求会话；返回渠道计量和中转错误，异常用量由统一结算选择。
func managedCohereStream(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	id, created := helper.GetResponseID(c), common.GetTimestamp()
	usage := &dto.Usage{}
	var text strings.Builder
	helper.StreamLineScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var v CohereResponse
		if err := common.UnmarshalJsonStr(data, &v); err != nil {
			sr.Error(err)
			return
		}
		chunk := &dto.ChatCompletionsStreamResponse{Id: id, Created: created, Object: "chat.completion.chunk", Model: info.UpstreamModelName}
		choice := dto.ChatCompletionsStreamResponseChoice{Index: 0}
		if v.IsFinished {
			reason := stopReasonCohere2OpenAI(v.FinishReason)
			choice.FinishReason = &reason
			if v.Response != nil {
				usage.PromptTokens = v.Response.Meta.BilledUnits.InputTokens
				usage.CompletionTokens = v.Response.Meta.BilledUnits.OutputTokens
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
		} else {
			choice.Delta.Role = "assistant"
			choice.Delta.SetContentString(v.Text)
			text.WriteString(v.Text)
		}
		chunk.Choices = []dto.ChatCompletionsStreamResponseChoice{choice}
		if err := helper.ObjectData(c, chunk); err != nil {
			sr.Stop(err)
			return
		}
		if v.IsFinished {
			sr.Done()
		}
	})
	helper.Done(c)
	if usage.PromptTokens == 0 {
		usage = service.ResponseText2UsageFromStream(c, text.String(), info.UpstreamModelName, info.GetEstimatePromptTokens(), info.ReceivedResponseCount)
	}
	return usage, nil
}
