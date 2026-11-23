package relay

import (
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

// A create has its own bounded observer. The connection's successful handshake
// may be reused, but body samples, errors and log publication never are.
// This path retains main's native protocol checks and does not install a session.
func beginResponsesWSObservation(c *gin.Context, info *relaycommon.RelayInfo, state *responsesWSCallState, handshake *http.Response) {
	channel, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	info.FreezeStreamValidation(channel)
	info.StreamProcessingOwner = relaycommon.StreamOwnerNotApplicable
	info.StreamValidationPolicy.Applicable = common.GetPointer(false)
	info.StreamResponseGate = &relaycommon.StreamResponseGate{}
	info.StreamDiagnostic = relaycommon.NewStreamResponseCapture(info.RetryIndex + 1)
	info.StreamDiagnostic.ResponseGate = info.StreamResponseGate
	info.StreamDiagnostic.Termination = relaycommon.NewStreamTerminationRecorder(c.Request.Context(), nil, 0, 0)
	info.StreamDiagnostic.SetValidationPolicy(info.StreamValidationPolicy)
	info.StreamDiagnostic.ObserveStreamHandshake(handshake, nil)
	state.capture = info.StreamDiagnostic
	state.status = info.StreamStatus
	state.finishLog = nil
	c.Set(relaycommon.StreamResponseCaptureKey, info.StreamDiagnostic)
	c.Set(relaycommon.StreamDiagnosticAttemptKey, info.RetryIndex+1)
	info.DeferStreamLog = func(publish func()) {
		var once sync.Once
		state.finishLog = func() {
			once.Do(func() {
				defer func() {
					if recover() != nil {
						common.SysError("responses websocket log publication failed")
					}
				}()
				publish()
			})
		}
	}
}

// observeClientWrite records the actual write before the deferred log snapshot.
// An observed failure does not change protocol completion or select usage.
func (state *responsesWSCallState) observeClientWrite(body []byte, err error) {
	if err == nil {
		state.capture.WriteDownstreamPayload(body)
		return
	}
	state.capture.SetError(err)
	state.status.RecordError("downstream websocket write failed")
	if state.capture != nil {
		state.capture.Termination.Observe("downstream", "write", err)
	}
}

// observeUpstreamFailure records errors already found by the native handler.
// It never selects a terminal, closes a connection, or changes accounting.
func (state *responsesWSCallState) observeUpstreamFailure(operation string, err error) {
	if err == nil {
		return
	}
	state.capture.SetError(err)
	state.status.RecordError("upstream websocket " + operation + " failed")
	if state.capture != nil {
		state.capture.Termination.Observe("upstream", operation, err)
	}
}
