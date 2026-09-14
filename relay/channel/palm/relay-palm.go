package palm

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// https://developers.generativeai.google/api/rest/generativelanguage/models/generateMessage#request-body
// https://developers.generativeai.google/api/rest/generativelanguage/models/generateMessage#response-body

func responsePaLM2OpenAI(response *PaLMChatResponse) *dto.OpenAITextResponse {
	fullTextResponse := dto.OpenAITextResponse{
		Choices: make([]dto.OpenAITextResponseChoice, 0, len(response.Candidates)),
	}
	for i, candidate := range response.Candidates {
		choice := dto.OpenAITextResponseChoice{
			Index: i,
			Message: dto.Message{
				Role:    "assistant",
				Content: candidate.Content,
			},
			FinishReason: "stop",
		}
		fullTextResponse.Choices = append(fullTextResponse.Choices, choice)
	}
	return &fullTextResponse
}

func streamResponsePaLM2OpenAI(palmResponse *PaLMChatResponse) *dto.ChatCompletionsStreamResponse {
	var choice dto.ChatCompletionsStreamResponseChoice
	if len(palmResponse.Candidates) > 0 {
		choice.Delta.SetContentString(palmResponse.Candidates[0].Content)
	}
	choice.FinishReason = &constant.FinishReasonStop
	var response dto.ChatCompletionsStreamResponse
	response.Object = "chat.completion.chunk"
	response.Model = "palm2"
	response.Choices = []dto.ChatCompletionsStreamResponseChoice{choice}
	return &response
}

// palmStreamHandler 将完整 PaLM JSON 转为下游 SSE；c 保存会话，resp 提供原始响应，受管路径使用单个所有者避免取消后协程滞留。
func palmStreamHandler(c *gin.Context, resp *http.Response) (*types.NewAPIError, string) {
	if value, ok := c.Get(relaycommon.StreamSessionKey); ok {
		if session, _ := value.(*relaycommon.StreamSession); session.Active() {
			// PaLM 原协议返回完整 JSON；请求所有者直接转换，避免取消后旧发送协程滞留。
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				// io.ReadAll 会消费 EOF；完整 JSON 中的上游 error 仍由会话保留，先终止再进行成功转换。
				err = session.ProtocolError()
			}
			if err != nil {
				session.EndRead(err)
				return types.NewError(err, types.ErrorCodeBadResponseBody), ""
			}
			var upstream PaLMChatResponse
			if err := common.Unmarshal(body, &upstream); err != nil {
				session.Fail("upstream_json_error", err)
				return types.NewError(err, types.ErrorCodeBadResponseBody), ""
			}
			// 完整 JSON 仍需实际 PaLM 内容；避免未知形状或空候选生成 stop 并计收输入费用。
			if len(upstream.Candidates) == 0 || strings.TrimSpace(upstream.Candidates[0].Content) == "" {
				err := errors.New("PaLM response has no output content")
				session.Fail("upstream_protocol_error", err)
				return types.NewError(err, types.ErrorCodeBadResponseBody), ""
			}
			chunk := streamResponsePaLM2OpenAI(&upstream)
			chunk.Id = helper.GetResponseID(c)
			chunk.Created = common.GetTimestamp()
			helper.SetEventStreamHeaders(c)
			if err := helper.ObjectData(c, chunk); err != nil {
				return types.NewError(err, types.ErrorCodeBadResponseBody), ""
			}
			helper.Done(c)
			if len(upstream.Candidates) > 0 {
				return nil, upstream.Candidates[0].Content
			}
			return nil, ""
		}
	}
	responseText := ""
	responseId := helper.GetResponseID(c)
	createdTime := common.GetTimestamp()
	dataChan := make(chan string)
	stopChan := make(chan bool)
	go func() {
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.LogLegacyStreamError(c, "error reading stream response: "+err.Error())
			stopChan <- true
			return
		}
		service.CloseResponseBodyGracefully(resp)
		var palmResponse PaLMChatResponse
		err = json.Unmarshal(responseBody, &palmResponse)
		if err != nil {
			logger.LogLegacyStreamError(c, "error unmarshalling stream response: "+err.Error())
			stopChan <- true
			return
		}
		fullTextResponse := streamResponsePaLM2OpenAI(&palmResponse)
		fullTextResponse.Id = responseId
		fullTextResponse.Created = createdTime
		if len(palmResponse.Candidates) > 0 {
			responseText = palmResponse.Candidates[0].Content
		}
		jsonResponse, err := json.Marshal(fullTextResponse)
		if err != nil {
			logger.LogLegacyStreamError(c, "error marshalling stream response: "+err.Error())
			stopChan <- true
			return
		}
		dataChan <- string(jsonResponse)
		stopChan <- true
	}()
	helper.SetEventStreamHeaders(c)
	c.Stream(func(w io.Writer) bool {
		select {
		case data := <-dataChan:
			c.Render(-1, common.CustomEvent{Data: "data: " + data})
			return true
		case <-stopChan:
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			return false
		}
	})
	service.CloseResponseBodyGracefully(resp)
	return nil, responseText
}

func palmHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	service.CloseResponseBodyGracefully(resp)
	var palmResponse PaLMChatResponse
	err = json.Unmarshal(responseBody, &palmResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if palmResponse.Error.Code != 0 || len(palmResponse.Candidates) == 0 {
		return nil, types.WithOpenAIError(types.OpenAIError{
			Message: palmResponse.Error.Message,
			Type:    palmResponse.Error.Status,
			Param:   "",
			Code:    palmResponse.Error.Code,
		}, resp.StatusCode)
	}
	fullTextResponse := responsePaLM2OpenAI(&palmResponse)
	usage := service.ResponseText2Usage(c, palmResponse.Candidates[0].Content, info.UpstreamModelName, info.GetEstimatePromptTokens())
	fullTextResponse.Usage = *usage
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	service.IOCopyBytesGracefully(c, resp, jsonResponse)
	return usage, nil
}
