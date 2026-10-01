package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The whole point of holding: a non-stream request adapted to stream upstream
// receives its first byte almost immediately. If that stopped the response
// timer the user's non_stream_response_timeout would bound nothing at all.
func TestRelayTimeoutHeldResponseTimerStillExpires(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 30*time.Millisecond, 0, false)
	control.HoldRelayResponse()

	time.Sleep(5 * time.Millisecond)
	assert.False(t, service.MarkRelayResponse(c),
		"first upstream byte must not stop a held response timer")

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind(),
		"the deadline the user configured must still fire")
}

// Control: without the hold, the existing non-stream behaviour is unchanged.
func TestRelayTimeoutUnheldNonStreamStillStopsOnFirstByte(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 30*time.Millisecond, 0, false)

	time.Sleep(5 * time.Millisecond)
	assert.True(t, service.MarkRelayResponse(c),
		"unadapted non-stream requests must keep stopping on the first byte")

	time.Sleep(60 * time.Millisecond)
	assert.NoError(t, ctx.Err())
	assert.Equal(t, relayTimeoutKindNone, control.ExpiredKind())
}

// A held request still honours its absolute bound; holding only suppresses the
// first-byte stop, it does not grant unlimited runtime.
func TestRelayTimeoutHeldStillHonoursTotalTimeout(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 0, 30*time.Millisecond, false)
	control.HoldRelayResponse()
	service.MarkRelayResponse(c)

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindTotal, control.ExpiredKind())
}

// Every stop path funnels through MarkResponse, including downstream writes and
// the SDK marker used by websocket-backed channels.
func TestRelayTimeoutHoldBlocksWriterStopPath(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 30*time.Millisecond, 0, false)
	control.HoldRelayResponse()

	_, err := c.Writer.Write([]byte(`{"id":"partial"}`))
	require.NoError(t, err)

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind(),
		"a downstream write must not stop a held response timer either")
}

func TestRelayTimeoutHoldIsIdempotentAndOrderIndependent(t *testing.T) {
	c, _, control := timeoutTestContext(t, time.Minute, 0, false)
	control.HoldRelayResponse()
	control.HoldRelayResponse()
	assert.False(t, service.MarkRelayResponse(c))

	// Holding after a mark already stopped the timer must not resurrect it.
	c2, _, control2 := timeoutTestContext(t, time.Minute, 0, false)
	assert.True(t, service.MarkRelayResponse(c2))
	control2.HoldRelayResponse()
	assert.False(t, service.MarkRelayResponse(c2))
}

// The hold is per-attempt. A retry may land on a channel that does not stream,
// and that attempt must get the ordinary first-byte stop back; the new attempt
// re-applies the hold if it adapts again.
func TestRelayTimeoutRetryClearsHold(t *testing.T) {
	c, _, control := timeoutTestContext(t, time.Minute, 0, false)
	control.HoldRelayResponse()
	assert.False(t, service.MarkRelayResponse(c))

	require.True(t, service.RestartRelayResponseTimeout(c))
	assert.True(t, service.MarkRelayResponse(c),
		"a retry onto a non-streaming channel must stop on its first byte")
}

// ...and an attempt that adapts again re-holds, so its deadline still bounds
// the whole response rather than just the first chunk.
func TestRelayTimeoutRetryCanReapplyHold(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 30*time.Millisecond, 0, false)
	control.HoldRelayResponse()
	require.True(t, service.RestartRelayResponseTimeout(c))

	service.HoldRelayResponseTimer(c)
	assert.False(t, service.MarkRelayResponse(c))

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
}

func TestHoldRelayResponseTimer_ViaService(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 30*time.Millisecond, 0, false)
	assert.True(t, service.HoldRelayResponseTimer(c))
	assert.False(t, service.MarkRelayResponse(c))

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
}

// Unmanaged requests have no controller; holding is a no-op, not a panic.
func TestHoldRelayResponseTimer_Unmanaged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	assert.False(t, service.HoldRelayResponseTimer(c))

	// A value that does not implement the holder interface must also be safe.
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, struct{}{})
	assert.False(t, service.HoldRelayResponseTimer(c))
}

func TestHoldRelayResponse_NilControl(t *testing.T) {
	var control *relayTimeoutControl
	assert.NotPanics(t, func() { control.HoldRelayResponse() })
}

// ---------------------------------------------------------------------------
// Response window on retry (design §6.2)
// ---------------------------------------------------------------------------

// A retry gets the full response window even when less total budget is left.
// Trimming the window to the budget ended the request no sooner — both timers
// then fire at the total deadline — but let the response timer win the race,
// so a request that ran out of TOTAL budget was reported as a response timeout
// with the response-timeout seconds in its message.
func TestRelayTimeoutRetryLeavesShortBudgetToTotalTimer(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, time.Minute, 60*time.Millisecond, false)

	require.True(t, service.RestartRelayResponseTimeout(c))

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindTotal, control.ExpiredKind(),
		"running out of total budget must be reported as a total timeout")
}

// Once the total budget has expired, a retry's restart must neither re-arm the
// response timer nor relabel the expiry.
func TestRelayTimeoutRetryAfterTotalExpiryKeepsTotalKind(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, time.Minute, 20*time.Millisecond, false)
	waitForTimeout(t, ctx)
	require.Equal(t, relayTimeoutKindTotal, control.ExpiredKind())

	assert.False(t, service.RestartRelayResponseTimeout(c))
	assert.Equal(t, relayTimeoutKindTotal, control.ExpiredKind())
}

// With no total timeout there is no budget, so the full window still applies.
func TestRelayTimeoutRetryKeepsFullWindowWithoutBudget(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 40*time.Millisecond, 0, false)
	require.True(t, service.RestartRelayResponseTimeout(c))

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
}

// Remaining budget larger than the response window leaves the window alone.
func TestRelayTimeoutRetryKeepsWindowWhenBudgetIsAmple(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 30*time.Millisecond, 10*time.Second, false)
	require.True(t, service.RestartRelayResponseTimeout(c))

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
}

// Releasing the hold stops the response timer the way a plain non-stream
// response's arrival does; the total timer still bounds the request.
func TestRelayTimeoutReleaseStopsHeldResponseTimer(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 40*time.Millisecond, 150*time.Millisecond, false)
	require.True(t, service.HoldRelayResponseTimer(c))
	assert.False(t, control.MarkResponse(false), "held: the first byte must not stop the timer")

	require.True(t, service.ReleaseRelayResponseTimer(c))

	waitForTimeout(t, ctx)
	assert.Equal(t, relayTimeoutKindTotal, control.ExpiredKind(),
		"after release only the total timer may end the request")
}

// Releasing after the response timer already fired changes nothing.
func TestRelayTimeoutReleaseAfterExpiryIsNoop(t *testing.T) {
	c, ctx, control := timeoutTestContext(t, 20*time.Millisecond, 0, false)
	require.True(t, service.HoldRelayResponseTimer(c))
	waitForTimeout(t, ctx)

	assert.False(t, service.ReleaseRelayResponseTimer(c))
	assert.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
}
