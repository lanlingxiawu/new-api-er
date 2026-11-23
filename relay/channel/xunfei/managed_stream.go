package xunfei

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// managedXunfeiStream 串行读取讯飞原始 WS 响应并转换 SSE，所有错误交给统一终止与结算。
// 参数 c：下游响应和请求生命周期上下文；request/domain/authURL/appID 沿用既有渠道转换；info 保存请求会话和首字时间，不采集请求。
func managedXunfeiStream(c *gin.Context, request dto.GeneralOpenAIRequest, domain, authURL, appID string, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	session := info.StreamSession
	mainMode := info.MainStreamMode()
	value, _ := c.Get(relaycommon.StreamResponseCaptureKey)
	capture, _ := value.(*relaycommon.StreamResponseCapture)
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	// 只有我方时限断开上游连接；客户端离开由流式会话的写失败关闭上游（与 HTTP 流式一致）。
	upstreamContext, release := service.RelayUpstreamContext(c)
	defer release()
	conn, resp, err := dialer.DialContext(upstreamContext, authURL, nil)
	session.ObserveWebSocketHandshake(resp, err)
	capture.ObserveStreamHandshake(resp, err)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	defer relaycommon.StreamConnectionLifetime(upstreamContext, conn)()
	session.BindUpstream(conn)
	if !mainMode {
		conn.SetReadLimit(relaycommon.MaxStreamFrameBytes)
	}
	data, err := common.Marshal(requestOpenAI2Xunfei(request, appID, domain))
	if err != nil {
		// 本地请求序列化失败仍沿用旧返回码，诊断来源与上游写入错误区分。
		session.FailRelay("upstream_read_error", err)
	}
	if err == nil {
		if !mainMode {
			_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		}
		err = conn.WriteMessage(websocket.TextMessage, data)
	}
	if err != nil {
		session.EndRead(err)
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	helper.SetEventStreamHeaders(c)
	usage := &dto.Usage{}
	if mainMode {
		defer helper.Done(c)
	}
	for {
		if deadline, ok := service.RelayRequestDeadline(c); ok {
			_ = conn.SetReadDeadline(deadline.Add(10 * time.Millisecond))
		} else if !mainMode && constant.StreamingTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(time.Duration(constant.StreamingTimeout) * time.Second))
		}
		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			session.EndRead(readErr)
			if mainMode {
				capture.SetError(readErr)
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, readErr)
				logger.LogLegacyStreamError(c, "xunfei read error: "+readErr.Error())
			}
			return usage, nil
		}
		capture.WriteStreamPayload(raw)
		if err := session.ObserveEvent("", raw); err != nil {
			return usage, nil
		}
		var response XunfeiChatResponse
		if err := common.Unmarshal(raw, &response); err != nil {
			session.Fail("upstream_json_error", err)
			logger.LogLegacyStreamError(c, "xunfei response decode error: "+err.Error())
			return usage, nil
		}
		if response.Header.Code != 0 && !mainMode {
			session.Fail("upstream_error", errors.New(response.Header.Message))
			return usage, nil
		}
		info.SetFirstResponseTime()
		info.ReceivedResponseCount++
		if mainMode {
			usage.PromptTokens += response.Payload.Usage.Text.PromptTokens
			usage.CompletionTokens += response.Payload.Usage.Text.CompletionTokens
			usage.TotalTokens += response.Payload.Usage.Text.TotalTokens
		} else {
			usage = service.BuildConfirmedStreamUsage(info, session.Snapshot().Evidence)
		}
		if err := helper.ObjectData(c, streamResponseXunfei2OpenAI(&response)); err != nil {
			return usage, nil
		}
		service.MarkRelayResponse(c)
		if session.ClientError() != nil {
			return usage, nil
		}
		if response.Payload.Choices.Status == 2 {
			if !mainMode {
				helper.Done(c)
			}
			return usage, nil
		}
	}
}
