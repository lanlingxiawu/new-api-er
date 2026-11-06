package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type forkTerminationTimeout string

func (k forkTerminationTimeout) RelayTimeoutKind() string { return string(k) }

func TestForkTerminationAttemptWiring(t *testing.T) {
	c, info, cancel := forkZeroDiagnosticContext(t, 200, true)
	defer cancel()
	require.NotNil(t, info.StreamSession.Termination)
	require.Same(t, info.StreamSession.Termination, info.StreamDiagnostic.Termination)
	previous := info.StreamSession.Termination
	previous.Observe("upstream", "protocol", errors.New("private evidence"))
	BeginStreamAttempt(c, info)
	require.NotSame(t, previous, info.StreamSession.Termination)
	require.Nil(t, info.StreamSession.Termination.Snapshot())
}

func TestForkTerminationCancellationOwnership(t *testing.T) {
	for _, kind := range []string{"", "response_timeout", "total_timeout"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, forkTerminationTimeout(kind))
			common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, 17)
			common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, 31)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			require.NotNil(t, info.StreamDiagnostic.Termination)
			cancel()
			info.StreamDiagnostic.Termination.ObserveContext()
			first := info.StreamDiagnostic.Termination.Snapshot().FirstObserved
			require.NotNil(t, first)
			if kind == "" {
				require.Equal(t, "downstream", first.Side)
				require.Equal(t, "request_cancel", first.Operation)
				require.Equal(t, "canceled", first.ErrorClass)
			} else {
				require.Equal(t, "gateway", first.Side)
				require.Equal(t, "timeout", first.Operation)
				require.Equal(t, kind, first.TimeoutKind)
				seconds := 17
				if kind == "total_timeout" {
					seconds = 31
				}
				require.Equal(t, seconds, first.TimeoutSeconds)
			}
		})
	}
}

func TestForkTerminationLogRefreshPreservesUsage(t *testing.T) {
	c, info, cancel := forkZeroDiagnosticContext(t, 200, true)
	defer cancel()
	require.NotNil(t, info.StreamDiagnostic.Termination)
	cancel()
	usage := FinalizeStreamUsage(c, info, nil)
	require.Equal(t, 375867, usage.PromptTokens)
	require.Zero(t, usage.CompletionTokens)
	require.True(t, info.StreamResult.ClientGone)
	info.StreamResult.DiagnosticAvailable = true
	info.StreamResult.Diagnostic.Termination = nil // A result snapshot predates cleanup.
	info.StreamDiagnostic.Termination.ObserveUpstreamClose("test_cleanup")
	other := model.NewLogOther()
	AppendStreamLogInfo(info, other)
	diagnostic := other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic)
	require.NotNil(t, diagnostic.Termination)
	require.NotNil(t, diagnostic.Termination.UpstreamClose)
	require.Equal(t, "downstream", diagnostic.Termination.FirstObserved.Side)
	require.Equal(t, map[string]int{"input_tokens": 375867, "output_tokens": 0}, diagnostic.EstimatedUsage)
	AppendStreamErrorDiagnostic(c, other, errors.New("later log attachment"))
	later := other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic)
	require.Equal(t, diagnostic.Termination, later.Termination)
	require.Equal(t, diagnostic.EstimatedUsage, later.EstimatedUsage)
	row := model.Log{Other: other.JSONString()}
	require.NoError(t, row.AfterFind(nil))
	require.NotContains(t, row.Other, "first_observed")
}
