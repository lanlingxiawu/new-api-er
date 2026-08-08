package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type relayResponseMarkerStub struct {
	called       bool
	lastIsStream bool
	pending      bool
}

func (marker *relayResponseMarkerStub) MarkResponse(isStream bool) bool {
	marker.called = true
	marker.lastIsStream = isStream
	return !isStream
}

func (marker *relayResponseMarkerStub) RelayResponsePending() bool {
	return marker.pending
}

func TestRelayHTTPClientOnlyRemovesGlobalTimeoutForManagedRequests(t *testing.T) {
	transport := http.DefaultTransport
	source := &http.Client{Transport: transport, Timeout: 30 * time.Second}

	assert.Same(t, source, RelayHTTPClient(nil, source))

	unmanaged, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Same(t, source, RelayHTTPClient(unmanaged, source))

	managed, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(managed, constant.ContextKeyRelayTimeoutControl, struct{}{})
	resolved := RelayHTTPClient(managed, source)
	require.NotSame(t, source, resolved)
	assert.Zero(t, resolved.Timeout)
	assert.Same(t, source.Transport, resolved.Transport)
	assert.Equal(t, 30*time.Second, source.Timeout, "the cached shared client must remain unchanged")
}

func TestRelayHTTPClientDoesNotCopyClientWithoutTimeout(t *testing.T) {
	managed, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(managed, constant.ContextKeyRelayTimeoutControl, struct{}{})
	source := &http.Client{}
	assert.Same(t, source, RelayHTTPClient(managed, source))
}

func TestNewRelayHTTPClientKeepsLegacyTimeoutForUnmanagedCalls(t *testing.T) {
	previous := common.RelayTimeout
	common.RelayTimeout = 17
	t.Cleanup(func() { common.RelayTimeout = previous })

	client := newRelayHTTPClient(http.DefaultTransport)
	assert.Equal(t, 17*time.Second, client.Timeout)

	unmanaged, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Same(t, client, RelayHTTPClient(unmanaged, client))
	assert.Equal(t, 17*time.Second, client.Timeout)
}

func TestRelayResponseTraceContextOnlyMarksNonStreamActualRequest(t *testing.T) {
	for _, test := range []struct {
		name      string
		isStream  bool
		pending   bool
		wantTrace bool
	}{
		{name: "non_stream_pending", pending: true, wantTrace: true},
		{name: "non_stream_response_already_received"},
		{name: "stream", isStream: true, pending: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyIsStream, test.isStream)
			marker := &relayResponseMarkerStub{pending: test.pending}
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, marker)

			ctx := RelayResponseTraceContext(c, context.Background())
			trace := httptrace.ContextClientTrace(ctx)
			if !test.wantTrace {
				assert.Nil(t, trace)
				assert.False(t, marker.called)
				return
			}
			require.NotNil(t, trace)
			require.NotNil(t, trace.GotFirstResponseByte)
			trace.GotFirstResponseByte()
			assert.True(t, marker.called)
		})
	}
}

func TestRelayResponseTraceContextCapturesMarkerInsteadOfGinContext(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyIsStream, false)
	original := &relayResponseMarkerStub{pending: true}
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, original)

	ctx := RelayResponseTraceContext(c, context.Background())
	trace := httptrace.ContextClientTrace(ctx)
	require.NotNil(t, trace)
	require.NotNil(t, trace.GotFirstResponseByte)

	// Simulate Gin recycling the Context before the transport callback runs.
	replacement := &relayResponseMarkerStub{pending: true}
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, replacement)
	common.SetContextKey(c, constant.ContextKeyIsStream, true)
	trace.GotFirstResponseByte()

	assert.True(t, original.called)
	assert.False(t, original.lastIsStream)
	assert.False(t, replacement.called)
}
