package service

import (
	"github.com/QuantumNous/new-api/pkg/retryguard"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func GroupRetryGuardDisabled(c *gin.Context) bool {
	if !operation_setting.GroupRetryGuardSnapshot().Enabled {
		return false
	}
	return retryguard.Disabled(relayAttemptGroup(c))
}

// ClaimGroupRetryGuardSample reuses upstream evidence, without another timer
// or retaining Gin state in a background callback. Settlement and failure
// monitoring can claim at most one sample for a client request.
func ClaimGroupRetryGuardSample(c *gin.Context, info *relaycommon.RelayInfo, success bool) bool {
	if !operation_setting.GroupRetryGuardSnapshot().Enabled || info == nil || info.RetryGuardSampled == nil || !info.HasChannelMeta() || info.IsChannelTest || info.RelayFormat == types.RelayFormatOpenAIRealtime {
		return false
	}
	if c != nil && c.Request != nil && c.Request.Context().Err() != nil && !IsRelayRequestTimeout(c) {
		return false
	}
	if info.StreamResult != nil && info.StreamResult.ClientGone {
		return false
	}
	observed := success || info.HasSendResponse() || info.StreamResult != nil
	if state := relayUpstreamSentState(c, false); state != nil {
		if exchange := state.current.Load(); exchange != nil {
			observed = exchange.wrote.Load()
		}
	}
	if !observed && info.LastError != nil {
		observed = info.LastError.GetErrorCode() == types.ErrorCodeBadResponseStatusCode
	}
	return observed && info.RetryGuardSampled.CompareAndSwap(false, true)
}
