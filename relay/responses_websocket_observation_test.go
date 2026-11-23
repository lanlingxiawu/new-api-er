package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesWSObservationIsPerCreateAndNotStrict(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
		state := &responsesWSCallState{}
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAIResponses, UserSetting: dto.UserSetting{StreamProtocolValidation: common.GetPointer(enabled)}}
		beginResponsesWSObservation(c, info, state, &http.Response{StatusCode: 101, Header: http.Header{"X-Upstream": {"fixture"}}})
		require.Nil(t, info.StreamSession)
		require.Equal(t, relaycommon.StreamOwnerNotApplicable, info.StreamProcessingOwner)
		require.False(t, info.MainStreamMode())
		state.capture.WriteStreamPayload([]byte("first response"))
		first := state.capture
		beginResponsesWSObservation(c, info, state, &http.Response{StatusCode: 101})
		require.NotSame(t, first, state.capture)
		require.Zero(t, state.capture.Snapshot().ObservedBytes)
		raw, err := common.Marshal(state.capture.Snapshot())
		require.NoError(t, err)
		require.NotContains(t, string(raw), "usage_final")
		count := 0
		info.DeferStreamLog(func() { count++ })
		state.finishLog()
		state.finishLog()
		require.Equal(t, 1, count)
	}
}

func TestResponsesWSWriteEvidenceBeforeLogPublication(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	info := &relaycommon.RelayInfo{IsStream: true, StreamStatus: relaycommon.NewStreamStatus()}
	state := &responsesWSCallState{}
	beginResponsesWSObservation(c, info, state, &http.Response{StatusCode: 101})
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	info.DeferStreamLog(func() {
		require.True(t, info.StreamStatus.OutcomeSnapshot().HasErrors)
		require.Contains(t, state.capture.Snapshot().Error, "closed pipe")
	})
	state.observeClientWrite([]byte("terminal"), io.ErrClosedPipe)
	state.finishLog()
}

func TestResponsesWSRecoverableErrorKeepsCallOpenAndRecordsEvidence(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	info := &relaycommon.RelayInfo{IsStream: true, StreamStatus: relaycommon.NewStreamStatus()}
	state := &responsesWSCallState{}
	beginResponsesWSObservation(c, info, state, &http.Response{StatusCode: 101})
	state.observeUpstreamFailure("provider_error", io.ErrUnexpectedEOF)
	require.True(t, info.StreamStatus.HasErrors())
	require.Empty(t, info.StreamStatus.EndReason)
	require.Empty(t, info.StreamStatus.ResponseOutcome())
	require.Nil(t, state.terminal)
	require.Nil(t, info.StreamResult)
	require.Contains(t, state.capture.Snapshot().Error, "unexpected EOF")
	require.Equal(t, "upstream", state.capture.Termination.Snapshot().FirstObserved.Side)
	require.Equal(t, "protocol", state.capture.Termination.Snapshot().FirstObserved.Operation)
}
