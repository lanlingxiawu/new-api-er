package xai

import (
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func streamResponseXAI2OpenAI(xAIResp *dto.ChatCompletionsStreamResponse, usage *dto.Usage) *dto.ChatCompletionsStreamResponse {
	if xAIResp == nil {
		return nil
	}
	if xAIResp.Usage != nil {
		xAIResp.Usage.CompletionTokens = usage.CompletionTokens
	}
	openAIResp := &dto.ChatCompletionsStreamResponse{
		Id:      xAIResp.Id,
		Object:  xAIResp.Object,
		Created: xAIResp.Created,
		Model:   xAIResp.Model,
		Choices: xAIResp.Choices,
		Usage:   xAIResp.Usage,
	}

	return openAIResp
}

// xAIStreamHandler 转换 xAI 流并保留原计量口径；c/resp 为两端上下文与响应，info 接收私有解析/写出错误。
func xAIStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	usage := &dto.Usage{}
	var responseTextBuilder strings.Builder
	var toolCount int
	var containStreamUsage bool

	helper.SetEventStreamHeaders(c)

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var xAIResp *dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &xAIResp); err != nil {
			logger.LogLegacyStreamError(c, "error unmarshalling stream response: "+err.Error())
			sr.Error(err)
			return
		}

		// 把 xAI 的usage转换为 OpenAI 的usage
		if xAIResp.Usage != nil {
			containStreamUsage = true
			usage.PromptTokens = xAIResp.Usage.PromptTokens
			usage.TotalTokens = xAIResp.Usage.TotalTokens
			usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
			// Each usage frame is a partial cumulative report. Only a reported
			// number replaces a detail; omitted/null fields retain earlier counts.
			reported := gjson.Get(data, "usage")
			input, output := &usage.PromptTokensDetails, &usage.CompletionTokenDetails
			incomingInput, incomingOutput := xAIResp.Usage.PromptTokensDetails, xAIResp.Usage.CompletionTokenDetails
			for _, field := range []struct {
				path     string
				current  *int
				incoming int
			}{
				{"prompt_tokens_details.cached_tokens", &input.CachedTokens, incomingInput.CachedTokens},
				{"prompt_tokens_details.cached_creation_tokens", &input.CachedCreationTokens, incomingInput.CachedCreationTokens},
				{"prompt_tokens_details.cache_write_tokens", &input.CacheWriteTokens, incomingInput.CacheWriteTokens},
				{"prompt_tokens_details.cache_creation_input_tokens", &input.CacheCreationInputTokens, incomingInput.CacheCreationInputTokens},
				{"prompt_tokens_details.text_tokens", &input.TextTokens, incomingInput.TextTokens},
				{"prompt_tokens_details.audio_tokens", &input.AudioTokens, incomingInput.AudioTokens},
				{"prompt_tokens_details.image_tokens", &input.ImageTokens, incomingInput.ImageTokens},
				{"completion_tokens_details.text_tokens", &output.TextTokens, incomingOutput.TextTokens},
				{"completion_tokens_details.audio_tokens", &output.AudioTokens, incomingOutput.AudioTokens},
				{"completion_tokens_details.image_tokens", &output.ImageTokens, incomingOutput.ImageTokens},
				{"completion_tokens_details.reasoning_tokens", &output.ReasoningTokens, incomingOutput.ReasoningTokens},
			} {
				if reported.Get(field.path).Type == gjson.Number {
					*field.current = field.incoming
				}
			}
			if cached := incomingInput.CachedTokensDetails; cached != nil &&
				(cached.TextTokens != nil || cached.AudioTokens != nil || cached.ImageTokens != nil) {
				if input.CachedTokensDetails == nil {
					input.CachedTokensDetails = &dto.CachedTokenDetails{}
				}
				for _, field := range []struct {
					current  **int
					incoming *int
				}{
					{&input.CachedTokensDetails.TextTokens, cached.TextTokens},
					{&input.CachedTokensDetails.AudioTokens, cached.AudioTokens},
					{&input.CachedTokensDetails.ImageTokens, cached.ImageTokens},
				} {
					if field.incoming != nil {
						value := *field.incoming
						*field.current = &value
					}
				}
			}
			if cacheWrite := incomingInput.CacheWriteTokensDetails; cacheWrite != nil {
				reportedWrite := reported.Get("prompt_tokens_details.cache_write_tokens_details")
				for _, field := range []struct {
					path     string
					incoming int
				}{
					{"ttl_30m_tokens", cacheWrite.TTL30mTokens},
					{"ttl_12h_tokens", cacheWrite.TTL12hTokens},
				} {
					if reportedWrite.Get(field.path).Type != gjson.Number {
						continue
					}
					if input.CacheWriteTokensDetails == nil {
						input.CacheWriteTokensDetails = &dto.CacheWriteTokenDetails{}
					}
					if field.path == "ttl_30m_tokens" {
						input.CacheWriteTokensDetails.TTL30mTokens = field.incoming
					} else {
						input.CacheWriteTokensDetails.TTL12hTokens = field.incoming
					}
				}
			}
			// Re-encoding an omitted value struct would emit false zero counts.
			// Send a detached snapshot of the same details used for settlement.
			xAIResp.Usage.PromptTokensDetails = input.Clone()
			xAIResp.Usage.CompletionTokenDetails = *output
		}

		openaiResponse := streamResponseXAI2OpenAI(xAIResp, usage)
		_ = openai.ProcessStreamResponse(*openaiResponse, &responseTextBuilder, &toolCount)
		if err := helper.ObjectData(c, openaiResponse); err != nil {
			logger.LogLegacyStreamError(c, err.Error())
			sr.ConversionError(err)
		}
	})

	if !containStreamUsage {
		usage = service.ResponseText2UsageFromStream(c, responseTextBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens(), info.ReceivedResponseCount)
		usage.CompletionTokens += toolCount * 7
	}

	helper.Done(c)
	service.CloseResponseBodyGracefully(resp)
	return usage, nil
}

func xAIHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	var xaiResponse ChatCompletionResponse
	err = common.Unmarshal(responseBody, &xaiResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if xaiResponse.Usage != nil {
		xaiResponse.Usage.CompletionTokens = xaiResponse.Usage.TotalTokens - xaiResponse.Usage.PromptTokens
		xaiResponse.Usage.CompletionTokenDetails.TextTokens = xaiResponse.Usage.CompletionTokens - xaiResponse.Usage.CompletionTokenDetails.ReasoningTokens
	}

	// new body
	encodeJson, err := common.Marshal(xaiResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	service.IOCopyBytesGracefully(c, resp, encodeJson)

	return xaiResponse.Usage, nil
}
