package types

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Our own relay deadline answers Claude clients with the Anthropic type
// timeout_error, the type the Claude stream terminal frame uses for the same
// event; other 5xx keep api_error.
func TestClaudeErrorTypeForRelayTimeout(t *testing.T) {
	timeout := NewErrorWithStatusCode(errors.New("The request exceeded the configured 1 second time limit."), ErrorCodeRelayTimeout, http.StatusGatewayTimeout, ErrOptionWithSkipRetry())
	assert.Equal(t, "timeout_error", timeout.ToClaudeError().Type)
	other := NewErrorWithStatusCode(errors.New("boom"), ErrorCodeBadResponse, http.StatusBadGateway)
	assert.Equal(t, "api_error", other.ToClaudeError().Type)
	carried := WithOpenAIError(OpenAIError{Message: "slow", Type: "timeout_error"}, http.StatusGatewayTimeout)
	assert.Equal(t, "timeout_error", carried.ToClaudeError().Type, "an upstream Anthropic timeout_error is kept")
}
