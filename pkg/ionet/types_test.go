package ionet

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIError_Error_WithoutDetails(t *testing.T) {
	e := &APIError{Code: 404, Message: "not found"}
	assert.Equal(t, "not found", e.Error())
}

func TestAPIError_Error_WithDetails(t *testing.T) {
	e := &APIError{Code: 500, Message: "server error", Details: "stack trace"}
	assert.Equal(t, "server error: stack trace", e.Error())
}

// Confirm APIError satisfies the error interface.
func TestAPIError_ImplementsError(t *testing.T) {
	var err error = &APIError{Message: "x"}
	assert.EqualError(t, err, "x")
}
