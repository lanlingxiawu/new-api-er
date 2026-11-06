package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The last raw read completes while the adapter is writing the last decoded
// event. This forces SDK read-ahead without adding a second event consumer.
type forkAWSReadAheadBody struct {
	data  []byte
	err   error
	ended chan struct{}
	once  sync.Once
}

func (b *forkAWSReadAheadBody) Read(p []byte) (int, error) {
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	b.once.Do(func() { close(b.ended) })
	return 0, b.err
}

func (b *forkAWSReadAheadBody) Close() error { return nil }

type forkAWSReadAheadWriter struct {
	*httptest.ResponseRecorder
	match  []byte
	ended  <-chan struct{}
	waited bool
}

func (w *forkAWSReadAheadWriter) Write(p []byte) (int, error) {
	if !w.waited && len(w.match) > 0 && bytes.Contains(p, w.match) {
		select {
		case <-w.ended:
			w.waited = true
		case <-time.After(time.Second):
			return 0, context.DeadlineExceeded
		}
	}
	return w.ResponseRecorder.Write(p)
}

func TestForkAWSReceivedPrefixBeforeTransportError(t *testing.T) {
	mode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(mode) })
	start := forkAWSStart(`{"input_tokens":21,"output_tokens":1,"cache_read_input_tokens":20,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":3}}`)
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, tc := range []struct {
			name      string
			events    []string
			partial   bool
			effective bool
		}{
			{name: "closed_after_17_bytes", events: []string{start}, partial: true},
			{name: "error_after_start", events: []string{start}},
			{name: "error_after_text", events: append([]string{start}, forkAWSText(0)...), effective: true},
		} {
			t.Run(fmt.Sprintf("%s/%s", format, tc.name), func(t *testing.T) {
				wire := forkAWSEncode(t, tc.events)
				readErr := io.ErrUnexpectedEOF
				if tc.partial {
					wire, readErr = wire[:17], net.ErrClosed
				}
				body := &forkAWSReadAheadBody{data: wire, err: readErr, ended: make(chan struct{})}
				writer := &forkAWSReadAheadWriter{ResponseRecorder: httptest.NewRecorder(), ended: body.ended}
				if !tc.partial {
					if tc.effective {
						writer.match = []byte("Hello,")
					} else if format == types.RelayFormatClaude {
						writer.match = []byte(`"type":"message_start"`)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				c := newAwsTestContext(writer, ctx)
				info := newAwsTestRelayInfo()
				info.DisablePing, info.RelayFormat = true, format
				service.BeginStreamAttempt(c, info)
				client := newAwsTestClient(relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
					return newAwsStreamResponse(r, body), nil
				})})
				apiErr, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})
				require.Nil(t, apiErr)
				final := service.FinalizeStreamUsage(c, info, usage)
				require.ErrorIs(t, info.StreamSession.Snapshot().Err, readErr)
				require.Equal(t, tc.effective, info.StreamResult.EffectiveContent)
				require.Equal(t, wire, append(info.StreamResult.Diagnostic.BodyHead, info.StreamResult.Diagnostic.BodyTail...))
				if len(writer.match) > 0 {
					require.True(t, writer.waited, "SDK raw read error must precede completion of the last downstream write")
				}
				wantInput := 0
				if !tc.partial && tc.effective {
					wantInput = 21
				}
				if canonical, ok := final.BillingUsage.CanonicalUsage(); ok {
					final = canonical
				}
				require.Equal(t, wantInput, final.PromptTokens)
				if tc.partial {
					require.Zero(t, info.ReceivedResponseCount)
					require.Empty(t, info.StreamResult.Diagnostic.UsageEvidence)
					require.Equal(t, "none", info.StreamResult.UsageSource)
				} else {
					require.Equal(t, len(tc.events), info.ReceivedResponseCount)
					require.Equal(t, 21, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
					require.Equal(t, 20, info.StreamResult.Diagnostic.UsageEvidence["cache_read_input_tokens"])
				}
				t.Logf("decoded=%d raw_bytes=%d effective=%t input=%d source=%s raw_error_before_write=%t", info.ReceivedResponseCount, len(wire), info.StreamResult.EffectiveContent, final.PromptTokens, info.StreamResult.UsageSource, writer.waited)
			})
		}
	}
}

// net/http checks for bytes beyond ContentLength after sending the request. The
// SDK closes its request body as soon as Do returns. Exercise both orderings
// using the real SDK-created body and the same io.Copy tail check as net/http.
func TestForkAWSRequestBodyTailProbeOrdering(t *testing.T) {
	for _, closeBeforeProbe := range []bool{false, true} {
		t.Run(strconv.FormatBool(closeBeforeProbe), func(t *testing.T) {
			var sentBody io.ReadCloser
			var sentBytes int64
			var probeErr error
			client := newAwsTestClient(awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
				sentBody = r.Body
				_, writerTo := sentBody.(io.WriterTo)
				require.True(t, writerTo, "the real Smithy request body selects io.Copy's WriterTo path")
				var err error
				sentBytes, err = io.CopyN(io.Discard, sentBody, r.ContentLength)
				require.NoError(t, err)
				require.Equal(t, r.ContentLength, sentBytes, "all invocation JSON is already sent")
				if !closeBeforeProbe {
					_, probeErr = io.Copy(io.Discard, sentBody)
				}
				return newAwsStreamResponse(r, io.NopCloser(bytes.NewReader(nil))), nil
			}))
			resp, err := client.InvokeModelWithResponseStream(context.Background(), newAwsStreamInput())
			require.NoError(t, err)
			defer resp.GetStream().Close()
			if closeBeforeProbe {
				_, probeErr = io.Copy(io.Discard, sentBody)
				require.ErrorIs(t, probeErr, io.EOF, "Smithy's closed WriterTo turns net/http's empty tail probe into a write error")
			} else {
				require.NoError(t, probeErr)
			}
			t.Logf("fully_sent_bytes=%d sdk_closed_before_tail_probe=%t tail_probe_error=%v", sentBytes, closeBeforeProbe, probeErr)
		})
	}
}

func TestForkAWSRequestBodyCompatibilityTailProbe(t *testing.T) {
	t.Run("guard", func(t *testing.T) {
		var transportBody io.ReadCloser
		executor := awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
			transportBody = r.Body
			n, err := io.CopyN(io.Discard, r.Body, r.ContentLength)
			require.NoError(t, err)
			require.Equal(t, r.ContentLength, n)
			return newAwsStreamResponse(r, io.NopCloser(bytes.NewReader(nil))), nil
		})
		client := newAwsTestClient(awsRequestBodyHTTPClient{Client: executor})
		resp, err := client.InvokeModelWithResponseStream(context.Background(), newAwsStreamInput())
		require.NoError(t, err)
		defer resp.GetStream().Close()
		n, err := io.Copy(io.Discard, transportBody)
		require.Zero(t, n)
		require.NoError(t, err, "a completed request's empty tail must not close its active response connection")
	})
}
