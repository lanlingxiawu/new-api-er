package relay

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client that asked for a non-stream answer sees nothing of how the upstream
// stream ended (only finish_reason). As on the native stream path, the end is
// recorded in info.StreamStatus, which the consume log carries as stream_status
// for the admin: done / eof / scanner_error (upstream broke) / client_gone /
// timeout / handler_stop (we stopped reading: budget or malformed frame).

const contentFrame = `data: {"id":"c","choices":[{"index":0,"delta":{"content":"paid output"}}]}` + "\n\n"

func TestAdaptedStreamStatus_CompleteIsDone(t *testing.T) {
	c, _, info := bufferedCase(t)
	body := `data: {"id":"c","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.IsNormalEnd())
	assert.False(t, info.StreamStatus.HasErrors())
}

func TestAdaptedStreamStatus_EndWithoutDoneIsEOF(t *testing.T) {
	c, _, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(contentFrame))
	require.Nil(t, apiErr)
	assert.Equal(t, relaycommon.StreamEndReasonEOF, info.StreamStatus.EndReason)
}

// The upstream connection dropped mid-answer: delivered and billed (unchanged),
// and now recorded as scanner_error with the read error for the admin.
func TestAdaptedStreamStatus_UpstreamBreakIsScannerError(t *testing.T) {
	c, rec, info := bufferedCase(t)
	usage, apiErr := handleAdaptedUpstreamStream(c, info, brokenUpstream(contentFrame))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
	require.Error(t, info.StreamStatus.EndError)
	assert.ErrorIs(t, info.StreamStatus.EndError, io.ErrUnexpectedEOF)
	assert.False(t, info.StreamStatus.IsNormalEnd())

	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "length", out.Choices[0].FinishReason, "the client response is unchanged: no custom fields")
}

// The client went away: the read fails because the request context was
// cancelled, not because the upstream broke. Billing is the same (what was
// received), but the record says client_gone, not scanner_error.
func TestAdaptedStreamStatus_ClientGoneIsNotAnUpstreamBreak(t *testing.T) {
	c, _, info := bufferedCase(t)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = c.Request.WithContext(ctx)
	cancel()
	resp := upstreamSSE("")
	resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(contentFrame), failingReader{context.Canceled}))

	usage, apiErr := handleAdaptedUpstreamStream(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Greater(t, usage.CompletionTokens, 0, "what was generated is billed, as on the native stream path")
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
}

func TestAdaptedStreamStatus_TimeoutIsTimeout(t *testing.T) {
	c, _, info := bufferedCase(t)
	markTimedOut(c)
	defer stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {})()
	_, apiErr := handleAdaptedUpstreamStream(c, info, brokenUpstream(contentFrame))
	require.Nil(t, apiErr)
	assert.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
}

// A malformed frame after real output: the output is delivered, we stopped
// reading on purpose (handler_stop), and the bad frame is recorded as an error.
func TestAdaptedStreamStatus_MalformedFrameAfterOutput(t *testing.T) {
	c, _, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(contentFrame+"data: {not json}\n\n"))
	require.Nil(t, apiErr)
	assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.HasErrors(), "the stream ended on a bad frame, so its status is error")
}

// Only a role frame, then a malformed one: there is no output to deliver.
// Before, this returned HTTP 200 with an empty message and billed the input.
func TestAdaptedStreamStatus_MalformedFrameAfterRoleOnlyIsAnError(t *testing.T) {
	c, rec, info := bufferedCase(t)
	role := `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n"
	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(role+"data: {not json}\n\n"))
	require.NotNil(t, apiErr)
	assert.Nil(t, usage)
	assert.Empty(t, rec.Body.String())
}

// The client left before any output: nothing to deliver or bill. Handled as a
// plain non-stream request the client cancelled (service.RelayContextError):
// no retry against another channel for a client that is gone.
func TestAdaptedStreamStatus_ClientGoneWithoutOutputIsNotRetried(t *testing.T) {
	c, rec, info := bufferedCase(t)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, managedControl{})
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = c.Request.WithContext(ctx)
	cancel()
	resp := upstreamSSE("")
	resp.Body = io.NopCloser(failingReader{context.Canceled})

	usage, apiErr := handleAdaptedUpstreamStream(c, info, resp)
	require.NotNil(t, apiErr)
	assert.Nil(t, usage)
	assert.True(t, types.IsSkipRetryError(apiErr), "a client that is gone is not retried on another channel")
	assert.Equal(t, types.ErrorCodeDoRequestFailed, apiErr.GetErrorCode())
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	assert.Empty(t, rec.Body.String())
}

// managedControl marks the request as owned by the relay timeout subsystem
// (an adapted request always is) without a deadline having fired.
type managedControl struct{}

func (managedControl) RelayTimeoutKind() string { return "" }

func TestAdaptedStreamStatus_UpstreamErrorFrameIsHandlerStop(t *testing.T) {
	c, _, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(contentFrame+`data: {"error":{"message":"overloaded","type":"server_error"}}`+"\n\n"))
	require.NotNil(t, apiErr)
	assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.HasErrors())
}

// [DONE] arrived before the deadline fired: the answer went out complete, so
// the stream is recorded as done, not timeout.
func TestAdaptedStreamStatus_TimeoutAfterDoneStaysDone(t *testing.T) {
	c, _, info := bufferedCase(t)
	markTimedOut(c)
	defer stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {})()
	body := `data: {"id":"c","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
	_, _ = handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
}
