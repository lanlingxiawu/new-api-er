package claude

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForkStrictTerminationWritesAndOwnedTimeout(t *testing.T) {
	for _, kind := range []string{"write", "short", "flush", "owned_timeout"} {
		t.Run(kind, func(t *testing.T) {
			c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + strictText + strictStop)))
			if kind == "flush" {
				c, _ = gin.CreateTestContext(&strictFlushFailure{httptest.NewRecorder()})
				c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			}
			ctx, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			switch kind {
			case "write", "short":
				c.Writer = &strictFailWriter{ResponseWriter: c.Writer, failOn: "message_start", short: kind == "short"}
			case "owned_timeout":
				common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &strictTimeoutControl{})
				common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, 31)
			}
			service.BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(resp, nil)
			if kind == "owned_timeout" {
				cancel()
			}
			_, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.False(t, info.StreamResult.EffectiveContent)
			d := info.StreamResult.Diagnostic.Termination
			require.NotNil(t, d)
			require.NotNil(t, d.FirstObserved)
			require.NotNil(t, d.UpstreamClose)
			if kind == "owned_timeout" {
				require.False(t, info.StreamResult.ClientGone)
				require.Equal(t, "gateway", d.FirstObserved.Side)
				require.Equal(t, "total_timeout", d.FirstObserved.TimeoutKind)
				require.Equal(t, 31, d.FirstObserved.TimeoutSeconds)
				return
			}
			require.True(t, info.StreamResult.ClientGone)
			require.Equal(t, "downstream", d.FirstObserved.Side)
			op, class := "write", "closed"
			if kind == "short" {
				class = "short_write"
			}
			if kind == "flush" {
				op = "flush"
			}
			require.Equal(t, op, d.FirstObserved.Operation)
			require.Equal(t, class, d.FirstObserved.ErrorClass)
		})
	}
}

func TestForkStrictTerminationObservation(t *testing.T) {

	for _, tc := range []struct {
		name, body, reason, side, operation, class string
		output                                     int
	}{
		{"normal", strictStart + strictText + strictStop, "done", "", "", "", 7},
		{"incomplete", strictStart, "upstream_incomplete", "upstream", "read", "eof", 0},
		{"json", strictStart + "event: message_delta\ndata: {broken}\n\n", "upstream_json_error", "upstream", "protocol", "protocol", 0},
		{"sequence", strictStart + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "upstream_protocol_error", "upstream", "protocol", "protocol", 0},
		{"provider", strictStart + "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"synthetic\"}}\n\n", "upstream_error", "upstream", "protocol", "provider_error", 0},
		{"cancel", "", "client_gone", "downstream", "request_cancel", "canceled", 0},
	} {
		t.Run(fmt.Sprintf("%s", tc.name), func(t *testing.T) {
			c, rec, resp, info := strictTestContext(io.NopCloser(strings.NewReader(tc.body)))
			ctx, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			service.BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(resp, nil)
			if tc.name == "cancel" {
				cancel()
			}
			usage, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
			require.Equal(t, tc.output, usage.CompletionTokens)
			if tc.name == "normal" {
				require.Equal(t, strings.Replace(tc.body, `"output_tokens":7}`, `"output_tokens":7,"input_tokens":100}`, 1), rec.Body.String())
			}
			d := info.StreamResult.Diagnostic.Termination

			require.NotNil(t, d)
			require.NotNil(t, d.UpstreamClose)
			if tc.name == "normal" {
				require.Nil(t, d.FirstObserved)
				return
			}
			require.NotNil(t, d.FirstObserved)
			require.Equal(t, tc.side, d.FirstObserved.Side)
			require.Equal(t, tc.operation, d.FirstObserved.Operation)
			require.Equal(t, tc.class, d.FirstObserved.ErrorClass)
		})
	}

}
