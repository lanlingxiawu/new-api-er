package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func normalizeRelayTimeoutError(c *gin.Context, current *types.NewAPIError) *types.NewAPIError {
	if !middleware.IsRelayRequestTimeout(c) {
		return current
	}
	if current == nil && c.Writer.Written() {
		return nil
	}
	return relayTimeoutAPIError(c)
}

func relayTimeoutAPIError(c *gin.Context) *types.NewAPIError {
	message := i18n.T(c, i18n.MsgRelayTimeout, map[string]any{
		"Seconds": middleware.RelayRequestTimeoutSeconds(c),
	})
	return types.NewErrorWithStatusCode(
		errors.New(message),
		types.ErrorCodeRelayTimeout,
		http.StatusGatewayTimeout,
		types.ErrOptionWithSkipRetry(),
	)
}

func handleRelayTimeoutResponse(c *gin.Context, isTimeout bool, write func()) bool {
	if !isTimeout {
		return false
	}
	if !c.Writer.Written() {
		middleware.WriteRelayTimeoutResponse(c, write)
	}
	return true
}

// startRelayRequestTimeout starts the per-user timeout for a Relay request,
// except for requests the upstream bills on submission (relayBilledOnSubmission),
// which run to completion as on main. The RelayInfo is bound to the timeout so
// a refunded timeout can record what the platform absorbed.
// Midjourney and task submissions (RelayMidjourney, RelayTask) never start it.
func startRelayRequestTimeout(c *gin.Context, info *relaycommon.RelayInfo) {
	if relayBilledOnSubmission(c, info) {
		return
	}
	middleware.StartRelayRequestTimeout(c, info.IsStream)
	service.BindRelayTimeoutInfo(c, info)
}

// relayBilledOnSubmission reports a Relay request that the upstream bills as
// soon as it accepts it and that we would then only be waiting on: Coze
// non-stream (create the chat, then poll) and Ali image generation and editing
// (the Wan models submit a task, then poll). Cancelling the wait saves no
// upstream cost, it only refunds the user and loses the result. Decided by the
// channel Distribute preferred (channel_type) before the first attempt: a
// request that starts managed stays managed even if a retry lands on such a
// channel, and one that starts unmanaged stays unmanaged.
func relayBilledOnSubmission(c *gin.Context, info *relaycommon.RelayInfo) bool {
	switch common.GetContextKeyInt(c, constant.ContextKeyChannelType) {
	case constant.ChannelTypeCoze:
		return !info.IsStream
	case constant.ChannelTypeAli:
		return info.RelayMode == relayconstant.RelayModeImagesGenerations || info.RelayMode == relayconstant.RelayModeImagesEdits
	default:
		return false
	}
}

// reportAdaptedRelayTimeout reports a timed-out adapted non-stream attempt to
// channel health. The adapted handler settles its own timeout and marks the
// request handled, and the relay loop returns on that mark before its ordinary
// timeout reporting — so without this, a channel that keeps hanging would be
// invisible to error logs and auto-disable for exactly the users whose requests
// were adapted, while the same timeouts from other users are still reported.
func reportAdaptedRelayTimeout(c *gin.Context, info *relaycommon.RelayInfo, channel *model.Channel) {
	if !shouldReportAdaptedRelayTimeout(c, info, channel) {
		return
	}
	processChannelError(c,
		*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey,
			common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()),
		relayTimeoutAPIError(c),
	)
}

func shouldReportAdaptedRelayTimeout(c *gin.Context, info *relaycommon.RelayInfo, channel *model.Channel) bool {
	return info != nil && channel != nil && info.UpstreamStreamAdapted && middleware.IsRelayRequestTimeout(c)
}
