package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// An Anthropic upstream error relayed to a Claude-format client keeps its
// error.type (clients branch on overloaded_error / rate_limit_error) instead of
// the formatted nil code "<nil>".
func TestRelayErrorHandlerKeepsAnthropicErrorTypeForClaudeClients(t *testing.T) {
	cases := []struct {
		status  int
		errType string
	}{
		{529, "overloaded_error"},
		{http.StatusTooManyRequests, "rate_limit_error"},
		{http.StatusBadRequest, "invalid_request_error"},
		{http.StatusUnauthorized, "authentication_error"},
		{http.StatusForbidden, "permission_error"},
		{http.StatusNotFound, "not_found_error"},
		{http.StatusInternalServerError, "api_error"},
	}
	for _, tc := range cases {
		t.Run(tc.errType, func(t *testing.T) {
			body := `{"type":"error","error":{"type":"` + tc.errType + `","message":"upstream said no"}}`
			resp := &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(body))}

			newAPIError := RelayErrorHandler(context.Background(), resp, false)

			require.NotNil(t, newAPIError)
			require.Equal(t, tc.status, newAPIError.StatusCode)
			claudeErr := newAPIError.ToClaudeError()
			require.Equal(t, tc.errType, claudeErr.Type)
			require.Equal(t, "upstream said no", claudeErr.Message)
			raw, err := common.Marshal(claudeErr)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "<nil>")
			openAIRaw, err := common.Marshal(newAPIError.ToOpenAIError())
			require.NoError(t, err)
			require.NotContains(t, string(openAIRaw), "<nil>")
		})
	}
}

// Masking replaces the upstream error with a fresh local one; its Claude type
// must still follow the status (529 stays overloaded_error) and must not leak
// the upstream text.
func TestPresentRelayErrorMaskedKeepsClaudeTypeForStatus(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	upstream := types.WithOpenAIError(types.OpenAIError{Message: "secret upstream detail", Type: "overloaded_error"}, 529)

	got, message := PresentRelayError(errorDisplayCtx(), upstream, "secret upstream detail (request id: r1)", "r1")

	require.NotNil(t, got)
	require.NotSame(t, upstream, got)
	got.SetMessage(message)
	claudeErr := got.ToClaudeError()
	require.Equal(t, "overloaded_error", claudeErr.Type)
	require.Equal(t, "服务暂时不可用 (request id: r1)", claudeErr.Message)
	require.NotContains(t, claudeErr.Message, "secret")
}
