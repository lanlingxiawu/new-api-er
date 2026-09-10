package claude

import (
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

func stopReasonClaude2OpenAI(reason string) string {
	return relayconvert.StopReasonClaudeToOpenAI(reason)
}

func maybeMarkClaudeRefusal(c *gin.Context, stopReason string) {
	if c == nil {
		return
	}
	if strings.EqualFold(stopReason, "refusal") {
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "claude_stop_reason=refusal")
	}
}

func StreamResponseClaude2OpenAI(claudeResponse *dto.ClaudeResponse) *dto.ChatCompletionsStreamResponse {
	return relayconvert.StreamResponseClaude2OpenAI(claudeResponse)
}

func ResponseClaude2OpenAI(claudeResponse *dto.ClaudeResponse) *dto.OpenAITextResponse {
	return relayconvert.ResponseClaude2OpenAI(claudeResponse)
}

type ClaudeResponseInfo = relayconvert.ClaudeResponseInfo

func cacheCreationTokensForOpenAIUsage(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	openAIUsage := relayconvert.UsageFromClaudeUsage(usage)
	if openAIUsage == nil {
		return 0
	}
	return openAIUsage.PromptTokens - usage.PromptTokens - usage.PromptTokensDetails.CachedTokens
}

func buildOpenAIStyleUsageFromClaudeUsage(usage *dto.Usage) dto.Usage {
	mapped := relayconvert.UsageFromClaudeUsage(usage)
	if mapped == nil {
		return dto.Usage{}
	}
	return *mapped
}

func buildMessageDeltaPatchUsage(claudeResponse *dto.ClaudeResponse, claudeInfo *ClaudeResponseInfo) *dto.ClaudeUsage {
	return relayconvert.BuildMessageDeltaPatchUsage(claudeResponse, claudeInfo)
}

func shouldSkipClaudeMessageDeltaUsagePatch(info *relaycommon.RelayInfo) bool {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		return true
	}
	if info == nil {
		return false
	}
	return info.ChannelSetting.PassThroughBodyEnabled
}

func patchClaudeMessageDeltaUsageData(data string, usage *dto.ClaudeUsage) string {
	return relayconvert.PatchClaudeMessageDeltaUsageData(data, usage)
}

func FormatClaudeResponseInfo(claudeResponse *dto.ClaudeResponse, oaiResponse *dto.ChatCompletionsStreamResponse, claudeInfo *ClaudeResponseInfo) bool {
	return relayconvert.FormatClaudeResponseInfo(claudeResponse, oaiResponse, claudeInfo)
}

func HandleStreamResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, data string) *types.NewAPIError {
	var claudeResponse dto.ClaudeResponse
	err := common.UnmarshalJsonStr(data, &claudeResponse)
	if err != nil {
		common.SysLog("error unmarshalling stream response: " + err.Error())
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	if claudeResponse.StopReason != "" {
		maybeMarkClaudeRefusal(c, claudeResponse.StopReason)
	}
	if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
		maybeMarkClaudeRefusal(c, *claudeResponse.Delta.StopReason)
	}
	if info.RelayFormat == types.RelayFormatClaude {
		FormatClaudeResponseInfo(&claudeResponse, nil, claudeInfo)

		switch claudeResponse.Type {
		case "message_start":
			claudeInfo.HasMessageStart = true
			// message_start, 获取usage
			if claudeResponse.Message != nil {
				info.UpstreamModelName = claudeResponse.Message.Model
			}
		case "content_block_start":
			// 记录当前打开的块，供截断兜底时补发 content_block_stop
			if claudeResponse.Index != nil {
				idx := *claudeResponse.Index
				claudeInfo.OpenBlockIndex = &idx
			}
		case "content_block_stop":
			claudeInfo.OpenBlockIndex = nil
		case "message_delta":
			// 确保 message_delta 的 usage 包含完整的 input_tokens 和 cache 相关字段
			// 解决 AWS Bedrock 等上游返回的 message_delta 缺少这些字段的问题
			if !shouldSkipClaudeMessageDeltaUsagePatch(info) {
				data = patchClaudeMessageDeltaUsageData(data, buildMessageDeltaPatchUsage(&claudeResponse, claudeInfo))
			}
		case "message_stop":
			claudeInfo.MessageStopSent = true
		}
		countClaudeStreamBillableTools(c, info, &claudeResponse)
		helper.ClaudeChunkData(c, claudeResponse, data)
	} else if info.RelayFormat == types.RelayFormatOpenAI {
		response := StreamResponseClaude2OpenAI(&claudeResponse)

		if !FormatClaudeResponseInfo(&claudeResponse, response, claudeInfo) {
			return nil
		}

		countClaudeStreamBillableTools(c, info, &claudeResponse)

		err = helper.ObjectData(c, response)
		if err != nil {
			logger.LogError(c, "send_stream_response_failed: "+err.Error())
		}
	}
	return nil
}

func countClaudeStreamBillableTools(c *gin.Context, info *relaycommon.RelayInfo, claudeResponse *dto.ClaudeResponse) {
	if claudeResponse == nil {
		return
	}
	if claudeResponse.Type == "content_block_start" &&
		claudeResponse.ContentBlock != nil &&
		claudeResponse.ContentBlock.Type == "tool_use" {
		info.CountBillableToolCall(dto.BuildInCallToolUse, claudeResponse.ContentBlock.Name)
	}
	if claudeResponse.Type == "message_delta" &&
		claudeResponse.Usage != nil &&
		claudeResponse.Usage.ServerToolUse != nil &&
		claudeResponse.Usage.ServerToolUse.WebSearchRequests > 0 {
		c.Set("claude_web_search_requests", claudeResponse.Usage.ServerToolUse.WebSearchRequests)
	}
}

// HandleStreamFinalResponse 完成既有流式输出的格式转换与用量收尾；原生 Claude 分支不补造成功结束事件。
// 参数 c：下游写入上下文；info：客户端协议及转换状态；claudeInfo：流处理中累积的响应文本、ID 和用量。
func HandleStreamFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo) {
	if info.ReceivedResponseCount == 0 {
		// 上游零响应：HTTP 200 进入了流式分支，但整段流一条 SSE 数据都没发过来
		// （连接挂起后断开 / 首字超时）。此时既没有 message_start 也没有任何输出，
		// 绝不能用请求体估算的输入 token 兜底计费——否则会对一个零输出的失败请求
		// 收取数百万 token 的费用。归零 usage，交由上层按失败/退款处理，
		// 与 Gemini 通道 (relay-gemini.go: ReceivedResponseCount>0 才估算) 行为对齐。
		claudeInfo.Usage.PromptTokens = 0
		claudeInfo.Usage.CompletionTokens = 0
		claudeInfo.Usage.TotalTokens = 0
	} else if claudeInfo.Usage.CompletionTokens == 0 || !claudeInfo.Done {
		if common.DebugEnabled {
			common.SysLog("claude response usage is not complete, maybe upstream error")
		}
		// 只补缺失字段，不整份覆盖——保留 message_start 已拿到的 cache 字段
		fallback := service.ResponseText2UsageFromStream(c, claudeInfo.ResponseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens(), info.ReceivedResponseCount)
		if claudeInfo.Usage.CompletionTokens == 0 ||
			(!claudeInfo.Done && fallback.CompletionTokens > claudeInfo.Usage.CompletionTokens) {
			claudeInfo.Usage.CompletionTokens = fallback.CompletionTokens
		}
		if claudeInfo.Usage.PromptTokens == 0 {
			claudeInfo.Usage.PromptTokens = fallback.PromptTokens
		}
		claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
	}
	if claudeInfo.Usage != nil {
		claudeInfo.Usage.UsageSemantic = "anthropic"
	}
	if claudeInfo.Usage != nil && claudeInfo.Usage.BillingUsage == nil {
		claudeInfo.Usage.BillingUsage = dto.NewClaudeMessagesBillingUsage(buildMessageDeltaPatchUsage(nil, claudeInfo))
	}

	if info.RelayFormat == types.RelayFormatOpenAI {
		if info.ShouldIncludeUsage {
			openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
			response := helper.GenerateFinalUsageResponse(claudeInfo.ResponseId, claudeInfo.Created, info.UpstreamModelName, openAIUsage)
			err := helper.ObjectData(c, response)
			if err != nil {
				common.SysLog("send final response failed: " + err.Error())
			}
		}
		helper.Done(c)
	}
}

// buildFinalClaudeUsage 由内部累计的 dto.Usage 构造下游 message_delta 所需的 ClaudeUsage。
func buildFinalClaudeUsage(usage *dto.Usage) *dto.ClaudeUsage {
	if usage == nil {
		return &dto.ClaudeUsage{}
	}
	cacheCreation5m, cacheCreation1h := service.NormalizeCacheCreationSplit(
		usage.PromptTokensDetails.CachedCreationTokens,
		usage.ClaudeCacheCreation5mTokens,
		usage.ClaudeCacheCreation1hTokens,
	)
	claudeUsage := &dto.ClaudeUsage{
		InputTokens:                 usage.PromptTokens,
		OutputTokens:                usage.CompletionTokens,
		CacheReadInputTokens:        usage.PromptTokensDetails.CachedTokens,
		CacheCreationInputTokens:    usage.PromptTokensDetails.CachedCreationTokens,
		ClaudeCacheCreation5mTokens: cacheCreation5m,
		ClaudeCacheCreation1hTokens: cacheCreation1h,
	}
	if cacheCreation5m > 0 || cacheCreation1h > 0 {
		claudeUsage.CacheCreation = &dto.ClaudeCacheCreationUsage{
			Ephemeral5mInputTokens: cacheCreation5m,
			Ephemeral1hInputTokens: cacheCreation1h,
		}
	}
	return claudeUsage
}

// ClaudeStreamHandler 根据原生协议和请求内开关选择严格流式处理，否则走既有兼容解析路径。
// 参数 c：请求和下游响应上下文；resp：上游流式 HTTP 响应；info：渠道、输出格式及计费状态。
// 返回解析后的用量及中转错误；严格路径通过 outcome 记录已在流内发送的异常。
func ClaudeStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	if info.RelayFormat == types.RelayFormatClaude && info.UseStrictClaudeStream() {
		return strictClaudeStream(c, resp, info)
	}
	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var err *types.NewAPIError
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		err = HandleStreamResponseData(c, info, claudeInfo, data)
		if err != nil {
			sr.Stop(err)
		}
	})
	if err != nil {
		if info.RelayFormat == types.RelayFormatClaude {
			HandleStreamFinalResponse(c, info, claudeInfo)
		}
		return nil, err
	}

	HandleStreamFinalResponse(c, info, claudeInfo)
	return claudeInfo.Usage, nil
}

func HandleClaudeResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, httpResp *http.Response, data []byte) *types.NewAPIError {
	var claudeResponse dto.ClaudeResponse
	err := common.Unmarshal(data, &claudeResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	maybeMarkClaudeRefusal(c, claudeResponse.StopReason)
	if claudeInfo.Usage == nil {
		claudeInfo.Usage = &dto.Usage{}
	}
	if claudeResponse.Usage != nil {
		claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens
		claudeInfo.Usage.CompletionTokens = claudeResponse.Usage.OutputTokens
		claudeInfo.Usage.TotalTokens = claudeResponse.Usage.InputTokens + claudeResponse.Usage.OutputTokens
		claudeInfo.Usage.UsageSemantic = "anthropic"
		claudeInfo.Usage.BillingUsage = dto.NewClaudeMessagesBillingUsage(claudeResponse.Usage)
		claudeInfo.Usage.PromptTokensDetails.CachedTokens = claudeResponse.Usage.CacheReadInputTokens
		claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens = claudeResponse.Usage.CacheCreationInputTokens
		claudeInfo.Usage.ClaudeCacheCreation5mTokens = claudeResponse.Usage.GetCacheCreation5mTokens()
		claudeInfo.Usage.ClaudeCacheCreation1hTokens = claudeResponse.Usage.GetCacheCreation1hTokens()
	}
	var responseData []byte
	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		openaiResponse := ResponseClaude2OpenAI(&claudeResponse)
		openaiResponse.Usage = buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
		responseData, err = common.Marshal(openaiResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatClaude:
		responseData = data
	}

	if claudeResponse.Usage != nil && claudeResponse.Usage.ServerToolUse != nil && claudeResponse.Usage.ServerToolUse.WebSearchRequests > 0 {
		c.Set("claude_web_search_requests", claudeResponse.Usage.ServerToolUse.WebSearchRequests)
	}

	for _, block := range claudeResponse.Content {
		if block.Type == "tool_use" {
			info.CountBillableToolCall(dto.BuildInCallToolUse, block.Name)
		}
	}

	service.IOCopyBytesGracefully(c, httpResp, responseData)
	return nil
}

func ClaudeHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	logger.LogDebug(c, "responseBody: %s", responseBody)
	handleErr := HandleClaudeResponseData(c, info, claudeInfo, resp, responseBody)
	if handleErr != nil {
		return nil, handleErr
	}
	return claudeInfo.Usage, nil
}
