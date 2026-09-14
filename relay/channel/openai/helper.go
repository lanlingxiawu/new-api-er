package openai

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

// HandleStreamFormat 按 info 的下游协议转换 data；forceFormat/thinkToContent 沿用 OpenAI 格式选项。
// 受管 Claude 的计数由实际转换分支推进，跳过的前置事件不占用 message_start 的首帧位置。
func HandleStreamFormat(c *gin.Context, info *relaycommon.RelayInfo, data string, forceFormat bool, thinkToContent bool) error {
	if info.RelayFormat != types.RelayFormatClaude || !info.StreamSession.Active() {
		info.SendResponseCount++
	}

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		return sendStreamData(c, info, data, forceFormat, thinkToContent)
	case types.RelayFormatClaude:
		return handleClaudeFormat(c, data, info)
	case types.RelayFormatGemini:
		return handleGeminiFormat(c, data, info)
	}
	return nil
}

// handleClaudeFormat 将当前 data 转换为 Claude 事件；c 为输出上下文，info 保存首帧及终态，返回转换错误。
func handleClaudeFormat(c *gin.Context, data string, info *relaycommon.RelayInfo) error {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(data), &streamResponse); err != nil {
		return err
	}
	if info.StreamSession.Active() && info.ClaudeConvertInfo == nil {
		info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{} // 首帧也可能携带 finish/usage，先建立请求局部转换状态。
	}

	if streamResponse.Usage != nil {
		info.ClaudeConvertInfo.Usage = streamResponse.Usage
	}
	if info.StreamSession.Active() {
		// 旧转换器在无 usage 的 finish 帧会直接返回，连同尾部正文一起丢弃。
		// 受管模式先交付当前内容，实际 stop_reason 留给确认完成后的 usage/收尾；不提前改变 Done。
		complete := info.StreamSession.ProtocolComplete()
		if len(streamResponse.Choices) == 0 && !complete {
			return nil
		}
		for i := range streamResponse.Choices {
			choice := &streamResponse.Choices[i]
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				info.ClaudeConvertInfo.FinishReason = *choice.FinishReason
				if streamResponse.Usage == nil || !complete {
					choice.FinishReason = nil
				}
			}
		}
		// 以实际 message_start 状态初始化，而不是把上游 ping/空事件数当成已发送帧数。
		if !info.ClaudeConvertInfo.MessageStartSent {
			info.SendResponseCount = 1
		} else {
			info.SendResponseCount++
		}
	}
	result, err := relayconvert.ConvertStreamResponse(c, info, types.RelayFormatClaude, &streamResponse)
	if err != nil {
		return err
	}
	claudeResponses, ok := result.Value.([]*dto.ClaudeResponse)
	if !ok {
		return fmt.Errorf("expected Claude stream responses, got %T", result.Value)
	}
	for _, resp := range claudeResponses {
		helper.ClaudeData(c, *resp)
	}
	return nil
}

func handleGeminiFormat(c *gin.Context, data string, info *relaycommon.RelayInfo) error {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(data), &streamResponse); err != nil {
		logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
		return err
	}

	result, err := relayconvert.ConvertStreamResponse(c, info, types.RelayFormatGemini, &streamResponse)
	if err != nil {
		return err
	}
	geminiResponse, ok := result.Value.(*dto.GeminiChatResponse)
	if !ok {
		return fmt.Errorf("expected Gemini stream response, got %T", result.Value)
	}

	// 如果返回 nil，表示没有实际内容，跳过发送
	if geminiResponse == nil {
		return nil
	}

	geminiResponseStr, err := common.Marshal(geminiResponse)
	if err != nil {
		logger.LogError(c, "failed to marshal gemini response: "+err.Error())
		return err
	}

	// send gemini format response
	c.Render(-1, common.CustomEvent{Data: "data: " + string(geminiResponseStr)})
	_ = helper.FlushWriter(c)
	return nil
}

func ProcessStreamResponse(streamResponse dto.ChatCompletionsStreamResponse, responseTextBuilder *strings.Builder, toolCount *int) error {
	for _, choice := range streamResponse.Choices {
		responseTextBuilder.WriteString(choice.Delta.GetContentString())
		responseTextBuilder.WriteString(choice.Delta.GetReasoningContent())
		if choice.Delta.ToolCalls != nil {
			if len(choice.Delta.ToolCalls) > *toolCount {
				*toolCount = len(choice.Delta.ToolCalls)
			}
			for _, tool := range choice.Delta.ToolCalls {
				responseTextBuilder.WriteString(tool.Function.Name)
				responseTextBuilder.WriteString(tool.Function.Arguments)
			}
		}
	}
	return nil
}

func processTokenData(relayMode int, data string, responseTextBuilder *strings.Builder, toolCount *int) error {
	switch relayMode {
	case relayconstant.RelayModeChatCompletions:
		var streamResponse dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			return err
		}
		return ProcessStreamResponse(streamResponse, responseTextBuilder, toolCount)
	case relayconstant.RelayModeCompletions:
		var streamResponse dto.CompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			return err
		}
		processCompletionsStreamResponse(streamResponse, responseTextBuilder)
	}
	return nil
}

func processCompletionsStreamResponse(streamResponse dto.CompletionsStreamResponse, responseTextBuilder *strings.Builder) {
	for _, choice := range streamResponse.Choices {
		responseTextBuilder.WriteString(choice.Text)
	}
}

func handleLastResponse(lastStreamData string, responseId *string, createAt *int64,
	systemFingerprint *string, model *string, usage **dto.Usage,
	containStreamUsage *bool, info *relaycommon.RelayInfo,
	shouldSendLastResp *bool) error {

	var lastStreamResponse dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(lastStreamData), &lastStreamResponse); err != nil {
		return err
	}

	*responseId = lastStreamResponse.Id
	*createAt = lastStreamResponse.Created
	*systemFingerprint = lastStreamResponse.GetSystemFingerprint()
	*model = lastStreamResponse.Model

	if service.ValidUsage(lastStreamResponse.Usage) {
		*containStreamUsage = true
		*usage = lastStreamResponse.Usage
		if !info.ShouldIncludeUsage {
			*shouldSendLastResp = lo.SomeBy(lastStreamResponse.Choices, func(choice dto.ChatCompletionsStreamResponseChoice) bool {
				return choice.Delta.GetContentString() != "" || choice.Delta.GetReasoningContent() != ""
			})
		}
	}

	return nil
}

// HandleFinalResponse 按下游协议完成正常转换；受管写入器会过滤异常情况下的成功尾帧。
// 参数 c/info 为本请求；lastStreamData 为最后上游 JSON，responseId/createAt/model/systemFingerprint 为响应元数据，usage/containStreamUsage 为计量及来源标记。
// 最后上游 JSON 的解析失败与本地转换/序列化错误分别登记来源，不通过日志函数设置终态。
func HandleFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, lastStreamData string,
	responseId string, createAt int64, model string, systemFingerprint string,
	usage *dto.Usage, containStreamUsage bool) {

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if info.ShouldIncludeUsage && !containStreamUsage {
			response := helper.GenerateFinalUsageResponse(responseId, createAt, model, *usage)
			response.SetSystemFingerprint(systemFingerprint)
			helper.ObjectData(c, response)
		}
		helper.Done(c)

	case types.RelayFormatClaude:
		if info.ClaudeConvertInfo == nil {
			info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone}
		}

		var streamResponse dto.ChatCompletionsStreamResponse
		if err := common.Unmarshal(common.StringToByteSlice(lastStreamData), &streamResponse); err != nil {
			info.StreamSession.Fail("upstream_json_error", err)
			logger.LogLegacyStreamError(c, "error unmarshalling stream response: "+err.Error())
		} else {
			info.ClaudeConvertInfo.Usage = usage

			claudeResponses := service.StreamResponseOpenAI2Claude(&streamResponse, info)
			for _, resp := range claudeResponses {
				_ = helper.ClaudeData(c, *resp)
			}
		}

		// If the upstream stream ends without a finish_reason, close the Claude
		// stream with the converter's normal stop sequence.
		if info.ClaudeConvertInfo != nil && info.ClaudeConvertInfo.MessageStartSent && !info.ClaudeConvertInfo.Done {
			info.ClaudeConvertInfo.Usage = usage
			finishReason := info.FinishReason
			if finishReason == "" {
				finishReason = "stop"
			}
			synthetic := helper.GenerateStopResponse(responseId, createAt, model, finishReason)
			synthetic.Usage = usage
			for _, resp := range service.StreamResponseOpenAI2Claude(synthetic, info) {
				_ = helper.ClaudeData(c, *resp)
			}
		}
		info.ClaudeConvertInfo.Done = true

	case types.RelayFormatGemini:
		var streamResponse dto.ChatCompletionsStreamResponse
		if err := common.Unmarshal(common.StringToByteSlice(lastStreamData), &streamResponse); err != nil {
			info.StreamSession.Fail("upstream_json_error", err)
			logger.LogLegacyStreamError(c, "error unmarshalling stream response: "+err.Error())
			return
		}

		// 这里处理的是 openai 最后一个流响应，其 delta 为空，有 finish_reason 字段
		// 因此相比较于 google 官方的流响应，由 openai 转换而来会多一个 parts 为空，finishReason 为 STOP 的响应
		// 而包含最后一段文本输出的响应（倒数第二个）的 finishReason 为 null
		// 暂不知是否有程序会不兼容。

		result, err := relayconvert.ConvertStreamResponse(c, info, types.RelayFormatGemini, &streamResponse)
		if err != nil {
			info.StreamSession.Fail("response_conversion_error", err)
			logger.LogLegacyStreamError(c, "error converting Gemini stream response: "+err.Error())
			return
		}
		geminiResponse, ok := result.Value.(*dto.GeminiChatResponse)
		if !ok {
			err := fmt.Errorf("expected Gemini stream response, got %T", result.Value)
			info.StreamSession.Fail("response_conversion_error", err)
			logger.LogLegacyStreamError(c, err.Error())
			return
		}

		// openai 流响应开头的空数据
		if geminiResponse == nil {
			return
		}

		geminiResponseStr, err := common.Marshal(geminiResponse)
		if err != nil {
			info.StreamSession.Fail("response_conversion_error", err)
			logger.LogLegacyStreamError(c, "error marshalling gemini response: "+err.Error())
			return
		}

		// 发送最终的 Gemini 响应
		c.Render(-1, common.CustomEvent{Data: "data: " + string(geminiResponseStr)})
		_ = helper.FlushWriter(c)
	}
}

func sendResponsesStreamData(c *gin.Context, streamResponse dto.ResponsesStreamResponse, data string) {
	if data == "" {
		return
	}
	_ = helper.ResponseChunkData(c, streamResponse, data)
}
