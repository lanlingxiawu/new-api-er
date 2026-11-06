package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Synthetic incident facts: final usage exists upstream but is unread when the
// downstream cancels. No credentials, real upstream, database or funds are used.
const forkCustomIncidentTerminal = `{"type":"response.completed","response":{"id":"resp_custom_fixture","model":"gpt-6.1-sol","status":"completed","output":[],"usage":{"input_tokens":153261,"output_tokens":1098,"total_tokens":154359,"input_tokens_details":{"cached_tokens":152986}}}}`

type forkCustomIncidentBody struct {
	prefix, terminal *strings.Reader
	forwarded        chan struct{}
	closed           chan struct{}
	closeOnce        sync.Once
	cancel           context.CancelFunc
	complete         bool
	terminalRead     atomic.Bool
}

func (b *forkCustomIncidentBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	select {
	case <-b.forwarded:
	case <-b.closed:
		return 0, io.ErrClosedPipe
	}
	if b.complete {
		b.terminalRead.Store(true)
		return b.terminal.Read(p)
	}
	// Cancel only after the entire prefix has been flushed downstream. Keep the
	// upstream read blocked until cleanup, reproducing response body closed.
	b.cancel()
	<-b.closed
	return 0, errors.New("http2: response body closed")
}

func (b *forkCustomIncidentBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

type forkCustomIncidentWriter struct {
	gin.ResponseWriter
	recorder  *httptest.ResponseRecorder
	forwarded chan struct{}
	once      sync.Once
}

func (w *forkCustomIncidentWriter) Flush() {
	w.ResponseWriter.Flush()
	if strings.Contains(w.recorder.Body.String(), `"sequence_number":99`) {
		w.once.Do(func() { close(w.forwarded) })
	}
}

func forkCustomIncidentFixture(t *testing.T, kind string, complete bool) (*gin.Context, *relaycommon.RelayInfo, *http.Response, *forkCustomIncidentBody, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	forwarded := make(chan struct{})
	c.Writer = &forkCustomIncidentWriter{ResponseWriter: c.Writer, recorder: recorder, forwarded: forwarded}
	frames := []string{`{"type":"response.created","response":{"id":"resp_custom_fixture","model":"gpt-6.1-sol","status":"in_progress","output":[],"usage":null}}`}
	switch kind {
	case "custom_partial", "custom_done":
		frames = append(frames,
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"ctc_fixture","type":"custom_tool_call","call_id":"call_fixture","name":"apply_patch","input":"","status":"in_progress"}}`,
			`{"type":"response.custom_tool_call_input.delta","item_id":"ctc_fixture","output_index":0,"delta":"*** Begin Patch\n*** Add File: fixture.txt\n+hello world\n*** End Patch"}`)
		if kind == "custom_done" {
			frames = append(frames,
				`{"type":"response.custom_tool_call_input.done","item_id":"ctc_fixture","output_index":0,"input":"*** Begin Patch\n*** Add File: fixture.txt\n+hello world\n*** End Patch"}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"id":"ctc_fixture","type":"custom_tool_call","call_id":"call_fixture","name":"apply_patch","input":"*** Begin Patch\n*** Add File: fixture.txt\n+hello world\n*** End Patch","status":"completed"}}`)
		}
	case "function_partial", "function_done":
		frames = append(frames,
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_fixture","type":"function_call","call_id":"call_fixture","name":"lookup","arguments":"","status":"in_progress"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc_fixture","output_index":0,"delta":"{\"city\":\"Paris\"}"}`)
		if kind == "function_done" {
			frames = append(frames,
				`{"type":"response.function_call_arguments.done","item_id":"fc_fixture","output_index":0,"arguments":"{\"city\":\"Paris\"}"}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_fixture","type":"function_call","call_id":"call_fixture","name":"lookup","arguments":"{\"city\":\"Paris\"}","status":"completed"}}`)
		}
	case "text":
		frames = append(frames, `{"type":"response.output_text.delta","output_index":0,"item_id":"msg_fixture","content_index":0,"delta":"hello world"}`)
	case "created_only":
	default:
		require.FailNow(t, "unknown fixture kind", kind)
	}
	// Mark the final actual event, rather than adding a synthetic heartbeat.
	frames[len(frames)-1] = strings.TrimSuffix(frames[len(frames)-1], "}") + `,"sequence_number":99}`
	var prefix strings.Builder
	for _, frame := range frames {
		prefix.WriteString("event: ")
		var header struct {
			Type string `json:"type"`
		}
		require.NoError(t, common.UnmarshalJsonStr(frame, &header))
		prefix.WriteString(header.Type)
		prefix.WriteString("\ndata: ")
		prefix.WriteString(frame)
		prefix.WriteString("\n\n")
	}
	body := &forkCustomIncidentBody{prefix: strings.NewReader(prefix.String()), terminal: strings.NewReader("event: response.completed\ndata: " + forkCustomIncidentTerminal + "\n\n"), forwarded: forwarded, closed: make(chan struct{}), cancel: cancel, complete: complete}
	t.Cleanup(func() { _ = body.Close() })
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAIResponses, FinalRequestRelayFormat: types.RelayFormatOpenAIResponses, OriginModelName: "gpt-6.1-sol", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6.1-sol"}}
	info.SetEstimatePromptTokens(150161)
	return c, info, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, body, recorder
}

// Originally reproduced against fetched upstream/main through a Go overlay.
// These assertions now protect the corrected custom-tool output estimate.
func TestForkCustomToolIncidentCandidate(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, complete := range []bool{false, true} {
		for _, kind := range []string{"custom_partial", "custom_done", "function_partial", "function_done", "text", "created_only"} {
			t.Run(fmt.Sprintf("%s/complete=%t", kind, complete), func(t *testing.T) {
				c, info, response, body, recorder := forkCustomIncidentFixture(t, kind, complete)
				usage, apiErr := OaiResponsesStreamHandler(c, info, response)
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				require.Contains(t, recorder.Body.String(), `"sequence_number":99`)
				require.Equal(t, complete, body.terminalRead.Load())
				if complete {
					require.Equal(t, 153261, usage.PromptTokens)
					require.Equal(t, 1098, usage.CompletionTokens)
					require.Equal(t, 152986, usage.PromptTokensDetails.CachedTokens)
				} else {
					require.ErrorIs(t, c.Request.Context().Err(), context.Canceled)
					require.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
					require.Equal(t, 150161, usage.PromptTokens)
					require.Zero(t, usage.PromptTokensDetails.CachedTokens)
					if kind == "created_only" {
						require.Zero(t, usage.CompletionTokens)
					} else {
						require.Positive(t, usage.CompletionTokens, "control delta is estimated")
					}
				}
				if strings.HasPrefix(kind, "custom_") && complete {
					for _, price := range []struct {
						expression string
						fullQuota  int
					}{
						{`len <= 272000 ? tier("0_272k", p * 2 + c * 10) : tier("272k_plus", p * 4 + c * 15)`, 523878},
						{`tier("cached", p * 2 + c * 10 + cr * 0.1)`, 44267},
					} {
						info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: price.expression, ExprHash: billingexpr.ExprHashString(price.expression), GroupRatio: 3.3, QuotaPerUnit: 500000}
						_, err := billingexpr.CompileFromCache(price.expression)
						require.NoError(t, err)
						params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(price.expression))
						ok, quota, result := service.TryTieredSettle(info, params)
						require.True(t, ok)
						require.NotNil(t, result)
						wantQuota := 495531 // $0.991062 after integer quota rounding.
						if complete {
							wantQuota = price.fullQuota
						}
						require.Equal(t, wantQuota, quota)
						t.Logf("pricing=%s quota=%d USD=%.6f", price.expression, quota, float64(quota)/500000)
					}
				}
				t.Logf("prompt=%d output=%d cached=%d terminal_read=%t end=%s", usage.PromptTokens, usage.CompletionTokens, usage.PromptTokensDetails.CachedTokens, body.terminalRead.Load(), info.StreamStatus.EndReason)
			})
		}
	}
}
