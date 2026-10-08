package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type relayTimeoutStateStub string

func (state relayTimeoutStateStub) RelayTimeoutKind() string { return string(state) }

func relayTimeoutTestContext(t *testing.T, kind string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, relayTimeoutStateStub(kind))
	common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, 12)
	common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, 34)
	return c
}

func TestNormalizeRelayTimeoutErrorOnlyForOwnedTimeout(t *testing.T) {
	timeoutContext := relayTimeoutTestContext(t, "response_timeout")
	normalized := normalizeRelayTimeoutError(timeoutContext, nil)
	require.NotNil(t, normalized)
	assert.Equal(t, types.ErrorCodeRelayTimeout, normalized.GetErrorCode())
	assert.Equal(t, http.StatusGatewayTimeout, normalized.StatusCode)
	assert.True(t, types.IsSkipRetryError(normalized))

	committedContext := relayTimeoutTestContext(t, "total_timeout")
	_, writeErr := committedContext.Writer.Write([]byte("done"))
	require.NoError(t, writeErr)
	assert.Nil(t, normalizeRelayTimeoutError(committedContext, nil), "a committed response must not be rewritten")

	notTimedOut := relayTimeoutTestContext(t, "")
	original := types.NewError(errors.New("upstream failed"), types.ErrorCodeBadResponse)
	assert.Same(t, original, normalizeRelayTimeoutError(notTimedOut, original))
}

func TestRelayRetryStopsForOwnedTimeout(t *testing.T) {
	c := relayTimeoutTestContext(t, "response_timeout")
	relayErr := relayTimeoutAPIError(c)
	assert.False(t, shouldRetry(c, relayErr, 2))
}

func TestHandleRelayTimeoutResponseOnlyOwnsTimeoutWrites(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	writes := 0
	write := func() {
		writes++
		c.Status(http.StatusGatewayTimeout)
	}

	assert.False(t, handleRelayTimeoutResponse(c, false, write))
	assert.Zero(t, writes)
	assert.True(t, handleRelayTimeoutResponse(c, true, write))
	assert.Equal(t, 1, writes)

	committed, _ := gin.CreateTestContext(httptest.NewRecorder())
	committed.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, writeErr := committed.Writer.Write([]byte("partial"))
	require.NoError(t, writeErr)
	assert.True(t, handleRelayTimeoutResponse(committed, true, func() { writes++ }))
	assert.Equal(t, 1, writes, "a committed response must not be rewritten")
}

// An adapted non-stream request that timed out settles itself and marks the
// request handled; its channel must still reach channel-error reporting, as
// the same timeout from an unadapted request does.
func TestShouldReportAdaptedRelayTimeout(t *testing.T) {
	channel := &model.Channel{Id: 1}
	adapted := &relaycommon.RelayInfo{UpstreamStreamAdapted: true}

	timedOut := relayTimeoutTestContext(t, "response_timeout")
	assert.True(t, shouldReportAdaptedRelayTimeout(timedOut, adapted, channel))

	notTimedOut := relayTimeoutTestContext(t, "")
	assert.False(t, shouldReportAdaptedRelayTimeout(notTimedOut, adapted, channel),
		"a handled request that did not time out has nothing to report")

	// Streams and other handled paths keep their existing behaviour.
	plain := &relaycommon.RelayInfo{UpstreamStreamAdapted: false}
	assert.False(t, shouldReportAdaptedRelayTimeout(timedOut, plain, channel))

	assert.False(t, shouldReportAdaptedRelayTimeout(timedOut, adapted, nil))
	assert.False(t, shouldReportAdaptedRelayTimeout(timedOut, nil, channel))
}
