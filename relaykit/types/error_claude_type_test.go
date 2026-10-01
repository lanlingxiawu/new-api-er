package types

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/require"
)

// Claude clients and SDKs classify errors by error.type, so a Claude-format
// error must carry an Anthropic type and never the formatted nil code "<nil>".

func TestToClaudeError_UpstreamAnthropicTypeKept(t *testing.T) {
	for _, typ := range []string{
		"invalid_request_error", "authentication_error", "billing_error", "permission_error",
		"not_found_error", "request_too_large", "rate_limit_error", "api_error", "overloaded_error",
	} {
		t.Run(typ, func(t *testing.T) {
			// Shape produced by RelayErrorHandler for an Anthropic error body:
			// type set, no code.
			e := WithOpenAIError(OpenAIError{Message: "upstream says", Type: typ}, 529)
			got := e.ToClaudeError()
			require.Equal(t, typ, got.Type)
			require.Equal(t, "upstream says", got.Message)
		})
	}
}

// An Anthropic type wins over a code: it is the only value a Claude client
// can act on.
func TestToClaudeError_AnthropicTypeWinsOverCode(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "too long", Type: "invalid_request_error", Code: "context_length_exceeded"}, http.StatusBadRequest)
	require.Equal(t, "invalid_request_error", e.ToClaudeError().Type)
}

func TestToClaudeError_StringCodeKeptWhenTypeUnknown(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "slow down", Type: "requests", Code: "rate_limit_exceeded"}, http.StatusTooManyRequests)
	require.Equal(t, "rate_limit_exceeded", e.ToClaudeError().Type)

	// Local errors keep their error code as the type.
	local := NewOpenAIError(errors.New("no quota"), ErrorCodeInsufficientUserQuota, http.StatusForbidden)
	require.Equal(t, string(ErrorCodeInsufficientUserQuota), local.ToClaudeError().Type)
}

func TestToClaudeError_TypeFromStatusWhenNoTypeOrCode(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "invalid_request_error"},
		{http.StatusUnauthorized, "authentication_error"},
		{http.StatusPaymentRequired, "billing_error"},
		{http.StatusForbidden, "permission_error"},
		{http.StatusNotFound, "not_found_error"},
		{http.StatusRequestEntityTooLarge, "request_too_large"},
		{http.StatusTooManyRequests, "rate_limit_error"},
		{529, "overloaded_error"},
		{http.StatusUnprocessableEntity, "invalid_request_error"}, // other 4xx
		{http.StatusInternalServerError, "api_error"},
		{http.StatusBadGateway, "api_error"},
		{http.StatusServiceUnavailable, "api_error"},
		{0, "api_error"},
		{http.StatusOK, "api_error"},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			// Unknown type ("upstream_error" is WithOpenAIError's default), nil code.
			e := WithOpenAIError(OpenAIError{Message: "m"}, tc.status)
			require.Equal(t, tc.want, e.ToClaudeError().Type)
		})
	}
}

func TestToClaudeError_NonStringOrEmptyCodeUsesStatus(t *testing.T) {
	for name, code := range map[string]any{
		"nil":          nil,
		"empty string": "",
		"empty code":   ErrorCode(""),
		"number":       float64(429),
		"object":       map[string]any{"x": 1},
	} {
		t.Run(name, func(t *testing.T) {
			e := WithOpenAIError(OpenAIError{Message: "m", Type: "x", Code: code}, http.StatusTooManyRequests)
			require.Equal(t, "rate_limit_error", e.ToClaudeError().Type)
		})
	}
}

func TestToClaudeError_NeverRendersNil(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "m", Type: "overloaded_error"}, 529)
	raw, err := kitutil.Marshal(e.ToClaudeError())
	require.NoError(t, err)
	require.NotContains(t, string(raw), "<nil>")
	require.JSONEq(t, `{"type":"overloaded_error","message":"m"}`, string(raw))
}

// OpenAI format keeps a missing code as JSON null (valid OpenAI shape), never
// the string "<nil>".
func TestToOpenAIError_NilCodeRendersNull(t *testing.T) {
	e := WithOpenAIError(OpenAIError{Message: "m", Type: "overloaded_error"}, 529)
	raw, err := kitutil.Marshal(e.ToOpenAIError())
	require.NoError(t, err)
	require.NotContains(t, string(raw), "<nil>")
	require.JSONEq(t, `{"message":"m","type":"overloaded_error","param":"","code":null}`, string(raw))
}

// Local errors (NewError / NewErrorWithStatusCode, also the fresh error the
// relay error masking builds) carry no upstream type: Claude clients get the
// Anthropic type for the status, never "new_api_error".
func TestToClaudeError_LocalErrorTypeFromStatus(t *testing.T) {
	cases := []struct {
		err  *NewAPIError
		want string
	}{
		{NewErrorWithStatusCode(errors.New("bad body"), ErrorCodeInvalidRequest, http.StatusBadRequest), "invalid_request_error"},
		{NewErrorWithStatusCode(errors.New("no quota"), ErrorCodeInsufficientUserQuota, http.StatusForbidden), "permission_error"},
		{NewErrorWithStatusCode(errors.New("busy"), "upstream_error", 529), "overloaded_error"},
		{NewErrorWithStatusCode(errors.New("slow down"), "upstream_error", http.StatusTooManyRequests), "rate_limit_error"},
		{NewError(errors.New("boom"), ErrorCodeDoRequestFailed), "api_error"}, // NewError defaults to 500
		{&NewAPIError{errorType: ErrorTypeGeminiError, StatusCode: http.StatusNotFound, Err: errors.New("m")}, "not_found_error"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := tc.err.ToClaudeError()
			require.Equal(t, tc.want, got.Type)
			require.Equal(t, tc.err.Error(), got.Message)
		})
	}
}
