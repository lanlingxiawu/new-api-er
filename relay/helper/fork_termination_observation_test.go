package helper

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForkScannerTerminationObservation(t *testing.T) {
	for _, kind := range []string{"normal", "cancel", "conversion_panic", "incomplete", "idle_timeout"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
			session := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
			session.BindContext(ctx)
			session.Termination = relaycommon.NewStreamTerminationRecorder(ctx, nil, 0, 0)
			capture := relaycommon.NewStreamResponseCapture(1)
			capture.Termination = session.Termination
			info := &relaycommon.RelayInfo{IsStream: true, StreamSession: session, StreamDiagnostic: capture, ChannelMeta: &relaycommon.ChannelMeta{}}
			c.Writer = relaycommon.NewStreamWriter(c.Writer, session, nil)
			body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n"
			if kind == "normal" {
				body += "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			if kind == "idle_timeout" {
				previousTimeout := constant.StreamingTimeout
				constant.StreamingTimeout = 1
				t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
				reader, writer := io.Pipe()
				defer writer.Close()
				resp.Body = reader
			}
			StreamEventScannerHandler(c, resp, info, func(_ string, data string, sr *StreamResult) {
				switch kind {
				case "cancel":
					cancel()
				case "conversion_panic":
					panic("synthetic private panic")
				default:
					require.NoError(t, StringData(c, data))
				}
			})
			d := capture.Snapshot().Termination
			require.NotNil(t, d)
			require.NotNil(t, d.UpstreamClose)
			if kind == "normal" {
				require.Nil(t, d.FirstObserved)
				require.True(t, session.ProtocolComplete())
				return
			}
			require.NotNil(t, d.FirstObserved)
			switch kind {
			case "cancel":
				require.Equal(t, "downstream", d.FirstObserved.Side)
				require.Equal(t, "request_cancel", d.FirstObserved.Operation)
			case "conversion_panic":
				require.Equal(t, "gateway", d.FirstObserved.Side)
				require.Equal(t, "panic", d.FirstObserved.ErrorClass)
			case "incomplete":
				require.Equal(t, "upstream", d.FirstObserved.Side)
				require.Equal(t, "eof", d.FirstObserved.ErrorClass)
			case "idle_timeout":
				require.Equal(t, "gateway", d.FirstObserved.Side)
				require.Equal(t, "idle_timeout", d.FirstObserved.TimeoutKind)
				require.Equal(t, 1, d.FirstObserved.TimeoutSeconds)
				require.True(t, d.UpstreamReadAfterClose)
			}
		})
	}
}
