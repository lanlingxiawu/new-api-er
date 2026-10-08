package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func responsesWSErrorDisplayCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "r1")
	return c
}

func withResponsesWSErrorDisplay(t *testing.T, setting operation_setting.RelayErrorDisplaySetting) {
	t.Helper()
	require.NoError(t, operation_setting.ValidateRelayErrorDisplaySetting(setting))
	previous := operation_setting.GetRelayErrorDisplaySetting()
	operation_setting.ReplaceRelayErrorDisplaySetting(setting)
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
}

func upstreamResponsesWSError() *types.NewAPIError {
	return types.WithOpenAIError(types.OpenAIError{
		Message: "No available channel for model x under group secret-group",
		Type:    "new_api_error",
		Code:    "model_not_found",
	}, http.StatusServiceUnavailable)
}

func TestPresentResponsesWSError_NilAndDisabledUnchanged(t *testing.T) {
	assert.Nil(t, presentResponsesWSError(responsesWSErrorDisplayCtx(), nil))

	withResponsesWSErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	original := upstreamResponsesWSError()
	got := presentResponsesWSError(responsesWSErrorDisplayCtx(), original)
	assert.Same(t, original, got)
	assert.Equal(t, "No available channel for model x under group secret-group", got.ToOpenAIError().Message)
}

// WebSocket clients get the same masking as the HTTP relay.
func TestPresentResponsesWSError_HidesUpstreamDetails(t *testing.T) {
	withResponsesWSErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	got := presentResponsesWSError(responsesWSErrorDisplayCtx(), upstreamResponsesWSError())
	require.NotNil(t, got)
	message := got.ToOpenAIError().Message
	assert.Contains(t, message, "服务暂时不可用")
	assert.NotContains(t, message, "secret-group")
}

func TestPresentResponsesWSError_LocalErrorKept(t *testing.T) {
	withResponsesWSErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	local := types.NewErrorWithStatusCode(errors.New("model is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	got := presentResponsesWSError(responsesWSErrorDisplayCtx(), local)
	assert.Contains(t, got.ToOpenAIError().Message, "model is required")
}
