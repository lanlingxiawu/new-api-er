package types

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAPIErrorStripsNestedRequestIdsFromRenderedMessages(t *testing.T) {
	upstreamMessage := "upstream failed (request id: upstream-a) (request id: upstream-b)"
	err := NewErrorWithStatusCode(errors.New(upstreamMessage), ErrorCodeBadResponseStatusCode, http.StatusBadRequest)

	require.Equal(t, "upstream failed", err.Error())
	require.NotContains(t, err.ErrorWithStatusCode(), "upstream-a")
	require.NotContains(t, err.MaskSensitiveErrorWithStatusCode(), "upstream-b")
	require.Equal(t, 0, strings.Count(err.ToOpenAIError().Message, "(request id:"))
}

func TestWithOpenAIErrorStripsNestedRequestIds(t *testing.T) {
	err := WithOpenAIError(OpenAIError{
		Message: "upstream failed (request id: upstream-a) (request id: upstream-b)",
		Type:    "upstream_error",
		Code:    "bad_response",
	}, http.StatusBadRequest)

	require.Equal(t, "upstream failed", err.Error())
	require.Equal(t, "upstream failed", err.ToOpenAIError().Message)
}

func TestSetMessagePreservesCurrentRequestIdInOutput(t *testing.T) {
	err := WithOpenAIError(OpenAIError{
		Message: "upstream failed (request id: upstream-a)",
		Type:    "upstream_error",
		Code:    "bad_response",
	}, http.StatusBadRequest)

	err.SetMessage("upstream failed (request id: current-gw)")

	require.Equal(t, "upstream failed", err.Error(), "Error() should strip all request IDs")
	require.Contains(t, err.ToOpenAIError().Message, "(request id: current-gw)",
		"ToOpenAIError matching type should preserve current request ID")
	require.Equal(t, 1, strings.Count(err.ToOpenAIError().Message, "(request id:"),
		"should have exactly one request ID")
}

func TestSetMessagePreservesRequestIdInCrossFormatOutput(t *testing.T) {
	err := WithClaudeError(ClaudeError{
		Message: "upstream failed (request id: upstream-a)",
		Type:    "error",
	}, http.StatusBadRequest)

	err.SetMessage("upstream failed (request id: current-gw)")

	oai := err.ToOpenAIError()
	require.Contains(t, oai.Message, "(request id: current-gw)",
		"cross-format ToOpenAIError should preserve current request ID via rawMessage")

	err2 := WithOpenAIError(OpenAIError{
		Message: "upstream failed (request id: upstream-a)",
		Type:    "upstream_error",
		Code:    "bad_response",
	}, http.StatusBadRequest)

	err2.SetMessage("upstream failed (request id: current-gw)")

	claude := err2.ToClaudeError()
	require.Contains(t, claude.Message, "(request id: current-gw)",
		"cross-format ToClaudeError should preserve current request ID via rawMessage")
}
