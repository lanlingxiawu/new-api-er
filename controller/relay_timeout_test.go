package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
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

func TestNormalizeRelayTaskTimeoutOnlyRewritesUncommittedOwnedTimeout(t *testing.T) {
	timeoutContext := relayTimeoutTestContext(t, "total_timeout")
	normalized := normalizeRelayTaskTimeout(timeoutContext, &taskdto.TaskError{Code: "upstream", StatusCode: http.StatusBadGateway})
	require.NotNil(t, normalized)
	assert.Equal(t, string(types.ErrorCodeRelayTimeout), normalized.Code)
	assert.Equal(t, http.StatusGatewayTimeout, normalized.StatusCode)
	assert.True(t, normalized.LocalError)
	assert.ErrorIs(t, normalized.Error, context.DeadlineExceeded)

	committedContext := relayTimeoutTestContext(t, "response_timeout")
	_, writeErr := committedContext.Writer.Write([]byte("accepted"))
	require.NoError(t, writeErr)
	assert.Nil(t, normalizeRelayTaskTimeout(committedContext, nil))

	notTimedOut := relayTimeoutTestContext(t, "")
	original := &taskdto.TaskError{Code: "upstream", StatusCode: http.StatusBadGateway}
	assert.Same(t, original, normalizeRelayTaskTimeout(notTimedOut, original))
}

func TestRelayRetryStopsForOwnedTimeout(t *testing.T) {
	c := relayTimeoutTestContext(t, "response_timeout")
	relayErr := relayTimeoutAPIError(c)
	assert.False(t, shouldRetry(c, relayErr, 2))
	taskErr := &taskdto.TaskError{Code: string(types.ErrorCodeRelayTimeout), StatusCode: http.StatusGatewayTimeout}
	assert.False(t, shouldRetryTaskRelay(c, 1, taskErr, 2))
}

func TestTaskChannelErrorHandlingKeepsTimeoutLoggingBranch(t *testing.T) {
	for _, test := range []struct {
		name string
		err  *taskdto.TaskError
		want bool
	}{
		{name: "upstream error", err: &taskdto.TaskError{Code: "upstream"}, want: true},
		{name: "unrelated local error", err: &taskdto.TaskError{Code: "local", LocalError: true}},
		{name: "owned timeout", err: &taskdto.TaskError{Code: string(types.ErrorCodeRelayTimeout), LocalError: true}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, shouldProcessTaskChannelError(test.err))
		})
	}
}

func TestRespondMidjourneyTimeoutKeepsNumericCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", nil)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, relayTimeoutStateStub("response_timeout"))
	common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, 12)

	respondMidjourneyTimeout(c)

	assert.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	var response struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
		Type        string `json:"type"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, 4, response.Code)
	assert.Equal(t, "upstream_error", response.Type)
	assert.NotEmpty(t, response.Description)
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

func TestMidjourneyTimeoutOnlyStartsForSubmitModes(t *testing.T) {
	for _, test := range []struct {
		mode int
		want bool
	}{
		{mode: relayconstant.RelayModeMidjourneyImagine, want: true},
		{mode: relayconstant.RelayModeMidjourneyTaskFetch},
		{mode: relayconstant.RelayModeMidjourneyTaskFetchByCondition},
		{mode: relayconstant.RelayModeMidjourneyTaskImageSeed},
		{mode: relayconstant.RelayModeMidjourneyNotify},
	} {
		assert.Equal(t, test.want, shouldStartMidjourneyTimeout(test.mode))
	}
}
