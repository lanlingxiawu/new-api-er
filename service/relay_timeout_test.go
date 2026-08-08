package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRelayTimeoutOverridePartitions(t *testing.T) {
	for _, seconds := range []int{RelayTimeoutUnlimited, 0, 1, MaxRelayTimeoutSeconds} {
		require.NoError(t, ValidateRelayTimeoutOverride(seconds))
	}
	for _, seconds := range []int{-2, MaxRelayTimeoutSeconds + 1} {
		require.Error(t, ValidateRelayTimeoutOverride(seconds))
	}
}

func TestResolveRelayTimeoutOverride(t *testing.T) {
	assert.Equal(t, 300, ResolveRelayTimeoutOverride(0, 300), "zero inherits the global value")
	assert.Equal(t, 20, ResolveRelayTimeoutOverride(20, 300), "positive user value overrides the global value")
	assert.Zero(t, ResolveRelayTimeoutOverride(RelayTimeoutUnlimited, 300), "-1 disables the corresponding timeout")
	assert.Equal(t, 300, ResolveRelayTimeoutOverride(-2, 300), "invalid legacy values inherit")
	assert.Equal(t, 300, ResolveRelayTimeoutOverride(MaxRelayTimeoutSeconds+1, 300), "invalid legacy values inherit")
	assert.Zero(t, ResolveRelayTimeoutOverride(0, 0))
}

func TestRelayRequestContextOnlyBindsManagedRequests(t *testing.T) {
	type contextKey string
	const (
		managedKey contextKey = "managed"
		requestKey contextKey = "request"
	)
	requestContext := context.WithValue(context.Background(), managedKey, "managed")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)

	originalContext := context.WithValue(context.Background(), requestKey, "original")
	req := httptest.NewRequest(http.MethodPost, "https://example.com", nil).WithContext(originalContext)

	assert.Equal(t, context.Background(), RelayRequestContext(c))
	assert.Same(t, req, BindRelayRequestContext(c, req))

	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, struct{}{})
	assert.Equal(t, "managed", RelayRequestContext(c).Value(managedKey))
	bound := BindRelayRequestContext(c, req)
	require.NotSame(t, req, bound)
	assert.Equal(t, "managed", bound.Context().Value(managedKey))
	assert.Equal(t, "original", req.Context().Value(requestKey), "the caller's request must remain unchanged")
}

type relayTimeoutKindStub string

func (stub relayTimeoutKindStub) RelayTimeoutKind() string { return string(stub) }

func TestRelayContextErrorOnlyHandlesManagedCancellation(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	unmanaged, _ := gin.CreateTestContext(httptest.NewRecorder())
	unmanaged.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(cancelled)
	assert.Nil(t, RelayContextError(unmanaged), "legacy cancellation remains on the original error path")

	managed, _ := gin.CreateTestContext(httptest.NewRecorder())
	managed.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(cancelled)
	common.SetContextKey(managed, constant.ContextKeyRelayTimeoutControl, relayTimeoutKindStub(""))
	managedErr := RelayContextError(managed)
	require.NotNil(t, managedErr)
	assert.Equal(t, types.ErrorCodeDoRequestFailed, managedErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(managedErr))

	common.SetContextKey(managed, constant.ContextKeyRelayTimeoutControl, relayTimeoutKindStub("total_timeout"))
	timeoutErr := RelayContextError(managed)
	require.NotNil(t, timeoutErr)
	assert.Equal(t, types.ErrorCodeRelayTimeout, timeoutErr.GetErrorCode())
	assert.Equal(t, http.StatusGatewayTimeout, timeoutErr.StatusCode)
	assert.True(t, types.IsSkipRetryError(timeoutErr))
}
