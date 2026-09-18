package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

func MidjourneyErrorWrapper(code int, desc string) *taskdto.MidjourneyResponse {
	return &taskdto.MidjourneyResponse{
		Code:        code,
		Description: desc,
	}
}

func MidjourneyErrorWithStatusCodeWrapper(code int, desc string, statusCode int) *taskdto.MidjourneyResponseWithStatusCode {
	return &taskdto.MidjourneyResponseWithStatusCode{
		StatusCode: statusCode,
		Response:   *MidjourneyErrorWrapper(code, desc),
	}
}

//// OpenAIErrorWrapper wraps an error into an OpenAIErrorWithStatusCode
//func OpenAIErrorWrapper(err error, code string, statusCode int) *dto.OpenAIErrorWithStatusCode {
//	text := err.Error()
//	lowerText := strings.ToLower(text)
//	if !strings.HasPrefix(lowerText, "get file base64 from url") && !strings.HasPrefix(lowerText, "mime type is not supported") {
//		if strings.Contains(lowerText, "post") || strings.Contains(lowerText, "dial") || strings.Contains(lowerText, "http") {
//			common.SysLog(fmt.Sprintf("error: %s", text))
//			text = "请求上游地址失败"
//		}
//	}
//	openAIError := dto.OpenAIError{
//		Message: text,
//		Type:    "new_api_error",
//		Code:    code,
//	}
//	return &dto.OpenAIErrorWithStatusCode{
//		Error:      openAIError,
//		StatusCode: statusCode,
//	}
//}
//
//func OpenAIErrorWrapperLocal(err error, code string, statusCode int) *dto.OpenAIErrorWithStatusCode {
//	openaiErr := OpenAIErrorWrapper(err, code, statusCode)
//	openaiErr.LocalError = true
//	return openaiErr
//}

func ClaudeErrorWrapper(err error, code string, statusCode int) *dto.ClaudeErrorWithStatusCode {
	text := cleanErrorText(err)
	lowerText := strings.ToLower(text)
	if !strings.HasPrefix(lowerText, "get file base64 from url") {
		if strings.Contains(lowerText, "post") || strings.Contains(lowerText, "dial") || strings.Contains(lowerText, "http") {
			common.SysLog(fmt.Sprintf("error: %s", text))
			text = "请求上游地址失败"
		}
	}
	claudeError := types.ClaudeError{
		Message: text,
		Type:    "new_api_error",
	}
	return &dto.ClaudeErrorWithStatusCode{
		Error:      claudeError,
		StatusCode: statusCode,
	}
}

func ClaudeErrorWrapperLocal(err error, code string, statusCode int) *dto.ClaudeErrorWithStatusCode {
	claudeErr := ClaudeErrorWrapper(err, code, statusCode)
	claudeErr.LocalError = true
	return claudeErr
}

// MessageWithCurrentRequestId removes stale request-id markers from a message
// and appends the current request id when the context has one.
func MessageWithCurrentRequestId(c *gin.Context, message string) string {
	requestId := ""
	if c != nil {
		requestId = c.GetString(common.RequestIdKey)
	}
	if requestId == "" {
		return common.StripRequestIds(message)
	}
	return common.MessageWithRequestId(message, requestId)
}

// ProcessChannelError handles local logging, channel auto-disable and relay
// error-log recording for a failed upstream channel.
func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	publicSummary := StreamPublicErrorSummary(c, err)
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(MessageWithCurrentRequestId(c, publicSummary))))
	if ShouldDisableChannel(err) && channelError.AutoBan {
		gopool.Go(func() {
			DisableChannel(channelError, publicSummary)
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		channelId := c.GetInt("channel_id")
		if channelError.ChannelId != 0 {
			channelId = channelError.ChannelId
		}
		other := make(map[string]interface{})
		if c.Request != nil && c.Request.URL != nil {
			other["request_path"] = c.Request.URL.Path
		}
		other["error_type"] = err.GetErrorType()
		other["error_code"] = err.GetErrorCode()
		other["status_code"] = err.StatusCode
		other["channel_id"] = channelId
		channelName := c.GetString("channel_name")
		if channelError.ChannelName != "" {
			channelName = channelError.ChannelName
		}
		other["channel_name"] = channelName
		other["channel_type"] = c.GetInt("channel_type")
		adminInfo := make(map[string]interface{})
		adminInfo["use_channel"] = c.GetStringSlice("use_channel")
		if common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
			adminInfo["is_multi_key"] = true
			adminInfo["multi_key_index"] = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
		}
		AppendChannelAffinityAdminInfo(c, adminInfo)
		other["admin_info"] = adminInfo
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		AppendStreamErrorDiagnostic(c, other, err)
		model.RecordErrorLog(c, userId, channelId, modelName, tokenName, MessageWithCurrentRequestId(c, publicSummary), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}

// upstreamErrorMessage selects only explicit strings for logs. Normalize each
// candidate before deciding whether it shadows the next supported field; the
// response parser and its retry/status decisions remain independent.
func upstreamErrorMessage(response dto.GeneralErrorResponse) string {
	var nested string
	switch common.GetJsonType(response.Error) {
	case "object":
		var object struct {
			Message string `json:"message"`
		}
		if common.Unmarshal(response.Error, &object) == nil {
			nested = object.Message
		}
	case "string":
		_ = common.Unmarshal(response.Error, &nested)
	}
	for _, candidate := range []string{nested, response.Message, response.Msg, response.Err, response.ErrorMsg, response.Detail, response.Header.Message, response.Response.Error.Message} {
		if message := common.StripRequestIds(candidate); message != "" {
			return message
		}
	}
	return ""
}

// RelayErrorHandler 把非成功 HTTP 响应转换为统一中转错误并关闭响应体，响应诊断模式避免普通日志带出 body。
// 参数 ctx：含响应专用标记的请求上下文；resp：上游 HTTP 错误响应；showBodyWhenFail：解析失败时是否把预览附入错误。
// 返回 newApiErr：转换后的状态码及错误信息；读取失败时保留初始化的 HTTP 状态错误。
func RelayErrorHandler(ctx context.Context, resp *http.Response, showBodyWhenFail bool) (newApiErr *types.NewAPIError) {
	newApiErr = types.InitOpenAIError(types.ErrorCodeBadResponseStatusCode, resp.StatusCode)
	defer CloseResponseBodyGracefully(resp)
	responseOnly, _ := ctx.Value(relaycommon.StreamResponseOnlyKey).(bool)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	var errResponse dto.GeneralErrorResponse
	responseBodyText := common.StripRequestIds(string(responseBody))
	responseBodyPreview := common.LocalLogPreview(responseBodyText)
	buildErrWithBody := func(message string) error {
		message = common.StripRequestIds(message)
		if message == "" {
			return fmt.Errorf("bad response status code %d, body: %s", resp.StatusCode, responseBodyText)
		}
		return fmt.Errorf("bad response status code %d, message: %s, body: %s", resp.StatusCode, message, responseBodyText)
	}

	err = common.Unmarshal(responseBody, &errResponse)
	if err != nil {
		if showBodyWhenFail {
			newApiErr.Err = buildErrWithBody("")
		} else {
			if !responseOnly {
				logger.LogError(ctx, fmt.Sprintf("bad response status code %d, body: %s", resp.StatusCode, responseBodyPreview))
			}
			newApiErr.Err = fmt.Errorf("bad response status code %d", resp.StatusCode)
		}
		return
	}

	logMessage := types.ErrOptionWithUpstreamMessage(upstreamErrorMessage(errResponse))
	if common.GetJsonType(errResponse.Error) == "object" {
		// General format error (OpenAI, Anthropic, Gemini, etc.)
		oaiError := errResponse.TryToOpenAIError()
		if oaiError != nil {
			newAPIErrorOptions := quotaExhaustedErrorOptions(oaiError.Message)
			newAPIErrorOptions = append(newAPIErrorOptions, logMessage)
			newApiErr = types.WithOpenAIError(*oaiError, resp.StatusCode, newAPIErrorOptions...)
			if showBodyWhenFail {
				newApiErr.Err = buildErrWithBody(newApiErr.Error())
			}
			return
		}
	}
	message := errResponse.ToMessage()
	if message == "" && !responseOnly {
		// The body parsed as JSON but carried no usable error message; log the
		// raw body so the upstream failure remains diagnosable.
		logger.LogError(ctx, fmt.Sprintf("bad response status code %d with empty error message, body: %s", resp.StatusCode, responseBodyPreview))
	}
	newAPIErrorOptions := quotaExhaustedErrorOptions(message)
	newAPIErrorOptions = append(newAPIErrorOptions, logMessage)
	newApiErr = types.NewOpenAIError(errors.New(message), types.ErrorCodeBadResponseStatusCode, resp.StatusCode, newAPIErrorOptions...)
	if showBodyWhenFail {
		newApiErr.Err = buildErrWithBody(newApiErr.Error())
	}
	return
}

func quotaExhaustedErrorOptions(message string) []types.NewAPIErrorOptions {
	lowerMessage := strings.ToLower(message)
	if strings.Contains(lowerMessage, "quota exceeded") ||
		strings.Contains(lowerMessage, "resource_exhausted") && strings.Contains(lowerMessage, "limit: 0") ||
		strings.Contains(lowerMessage, "free-usage-exhausted") ||
		strings.Contains(lowerMessage, "used all the included free usage") {
		return []types.NewAPIErrorOptions{types.ErrOptionWithSkipRetry()}
	}
	return nil
}

func ResetStatusCode(newApiErr *types.NewAPIError, statusCodeMappingStr string) {
	if newApiErr == nil {
		return
	}
	if statusCodeMappingStr == "" || statusCodeMappingStr == "{}" {
		return
	}
	statusCodeMapping := make(map[string]any)
	err := common.Unmarshal([]byte(statusCodeMappingStr), &statusCodeMapping)
	if err != nil {
		return
	}
	if newApiErr.StatusCode == http.StatusOK {
		return
	}
	codeStr := strconv.Itoa(newApiErr.StatusCode)
	if value, ok := statusCodeMapping[codeStr]; ok {
		intCode, ok := parseStatusCodeMappingValue(value)
		if !ok {
			return
		}
		newApiErr.StatusCode = intCode
	}
}

func parseStatusCodeMappingValue(value any) (int, bool) {
	switch v := value.(type) {
	case string:
		if v == "" {
			return 0, false
		}
		statusCode, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return statusCode, true
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	case json.Number:
		statusCode, err := strconv.Atoi(v.String())
		if err != nil {
			return 0, false
		}
		return statusCode, true
	default:
		return 0, false
	}
}

func TaskErrorWrapperLocal(err error, code string, statusCode int) *taskdto.TaskError {
	openaiErr := TaskErrorWrapper(err, code, statusCode)
	openaiErr.LocalError = true
	return openaiErr
}

func TaskErrorWrapper(err error, code string, statusCode int) *taskdto.TaskError {
	text := cleanErrorText(err)
	lowerText := strings.ToLower(text)
	if strings.Contains(lowerText, "post") || strings.Contains(lowerText, "dial") || strings.Contains(lowerText, "http") {
		common.SysLog(fmt.Sprintf("error: %s", text))
		//text = "请求上游地址失败"
		text = common.MaskSensitiveInfo(text)
	}
	//避免暴露内部错误
	taskError := &taskdto.TaskError{
		Code:       code,
		Message:    text,
		StatusCode: statusCode,
		Error:      err,
	}

	return taskError
}

// TaskErrorFromAPIError 将 PreConsumeBilling 返回的 NewAPIError 转换为 TaskError。
func TaskErrorFromAPIError(apiErr *types.NewAPIError) *taskdto.TaskError {
	if apiErr == nil {
		return nil
	}
	return &taskdto.TaskError{
		Code:       string(apiErr.GetErrorCode()),
		Message:    apiErr.Error(),
		StatusCode: apiErr.StatusCode,
		Error:      apiErr.Err,
		SkipRetry:  types.IsSkipRetryError(apiErr),
	}
}

func cleanErrorText(err error) string {
	if err == nil {
		return ""
	}
	return common.StripRequestIds(err.Error())
}
