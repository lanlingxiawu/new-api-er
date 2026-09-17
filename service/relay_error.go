package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return false
	}
	if types.IsChannelError(openaiErr) {
		return true
	}
	if types.IsSkipRetryError(openaiErr) {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	code := openaiErr.StatusCode
	if code >= 200 && code < 300 {
		return false
	}
	if code < 100 || code > 599 {
		return true
	}
	if operation_setting.IsAlwaysSkipRetryCode(openaiErr.GetErrorCode()) {
		return false
	}
	return operation_setting.ShouldRetryByStatusCode(code)
}

// MessageWithCurrentRequestId 去掉 message 中已有的 request id 标记，再附加当前请求的 request id；
// 上下文没有 request id 时只去掉旧标记，避免错误日志带上其他请求（如上游或重试前）的 id。
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

// ProcessChannelError 处理渠道失败的本地日志、自动禁用及错误日志，HTTP 中转、渠道测试与 Responses WebSocket 共用。
// 错误日志内容为公开错误摘要（附当前 request id），流式底层原因单独保存为超级管理员诊断。
// 参数 c：当前请求上下文；channelError：本次失败渠道的身份及配置快照；err：本次中转错误，nil 时直接返回；relayInfo：本请求中转信息，可为 nil。
// 仅在失败路径执行：渠道禁用在 gopool 中异步执行，错误日志经 model.RecordErrorLog 进入日志写入管线。
func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	publicSummary := StreamPublicErrorSummary(c, err)
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(MessageWithCurrentRequestId(c, publicSummary))))
	// 渠道信息取自 channelError 快照而非上下文：异步处理时上下文中的渠道可能已被重试改写。
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
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		AppendStreamErrorDiagnostic(c, other, err)
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, MessageWithCurrentRequestId(c, publicSummary), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}
