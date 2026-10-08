package helper

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// NewInvalidRequestError turns a GetAndValidateRequest failure into the relay
// error sent to the client. It is never retried: the same body fails the same
// way on every channel. A body that cannot be read or decoded gets the status
// and translated message of relaycommon.PublicRequestError instead of Go's
// error text (which names internal struct types, e.g. "cannot unmarshal string
// into Go struct field ClaudeRequest.max_tokens of type uint"); the raw error
// is logged. Validation messages written for clients ("field messages is
// required") are passed through with 400.
func NewInvalidRequestError(c *gin.Context, err error) *types.NewAPIError {
	status, code := http.StatusBadRequest, types.ErrorCodeInvalidRequest
	options := []types.NewAPIErrorOptions{types.ErrOptionWithSkipRetry()}
	if publicStatus, message, ok := relaycommon.PublicRequestError(c, err); ok {
		if publicStatus >= http.StatusInternalServerError {
			code = types.ErrorCodeReadRequestBodyFailed
			logger.LogError(c, "request body read failed: "+common.LocalLogPreview(err.Error()))
		} else {
			logger.LogWarn(c, "request body decode failed: "+common.LocalLogPreview(err.Error()))
		}
		status = publicStatus
		err = errors.New(message)
		// The public message is our own text plus field names from the
		// client's JSON; masking would render a field path as "***.name".
		options = append(options, types.ErrOptionWithSafeMessage())
	}
	options = append(options, types.ErrOptionWithStatusCode(status))
	return types.NewError(err, code, options...)
}
