package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const forkLateUsageCreated = `data: {"type":"response.created","response":{"id":"resp_late_fixture","model":"gpt-4o","status":"in_progress","output":[],"usage":null}}` + "\n\n"
const forkLateUsageTerminal = `data: {"type":"response.completed","response":{"id":"resp_late_fixture","model":"gpt-4o","status":"completed","output":[],"usage":{"input_tokens":389301,"output_tokens":1542,"total_tokens":390843,"input_tokens_details":{"cached_tokens":389298}}}}` + "\n\n"

// The final usage exists in the synthetic upstream, but a deterministic fault
// occurs after the created frame is delivered and before that tail is read.
// No timer sleeps, external server, database, or billing side effects are used.
type forkLateUsageBody struct {
	prefix, terminal  *strings.Reader
	forwarded, closed chan struct{}
	closeOnce         sync.Once
	mode              string
	cancel            context.CancelFunc
	terminalRead      bool
}

func (b *forkLateUsageBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	if b.mode != "response_timeout_before_first_frame" {
		select {
		case <-b.forwarded:
		case <-b.closed:
			return 0, io.ErrClosedPipe
		}
	}
	switch b.mode {
	case "complete":
		b.terminalRead = true
		return b.terminal.Read(p)
	case "client_gone", "total_timeout", "response_timeout_before_first_frame":
		b.cancel()
		return 0, context.Canceled
	case "upstream_read_error":
		return 0, io.ErrUnexpectedEOF
	default:
		return 0, io.EOF
	}
}

func (b *forkLateUsageBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

type forkLateUsageWriter struct {
	gin.ResponseWriter
	forwarded chan struct{}
	once      sync.Once
}

func (w *forkLateUsageWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 && err == nil {
		w.once.Do(func() { close(w.forwarded) })
	}
	return n, err
}

func (w *forkLateUsageWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func forkLateUsageFixture(t *testing.T, mode string) (*gin.Context, *relaycommon.RelayInfo, *http.Response, *forkLateUsageBody) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	forwarded := make(chan struct{})
	c.Writer = &forkLateUsageWriter{ResponseWriter: c.Writer, forwarded: forwarded}
	prefix := forkLateUsageCreated
	if mode == "response_timeout_before_first_frame" {
		prefix = ""
	}
	body := &forkLateUsageBody{prefix: strings.NewReader(prefix), terminal: strings.NewReader(forkLateUsageTerminal), forwarded: forwarded, closed: make(chan struct{}), mode: mode, cancel: cancel}
	t.Cleanup(func() { _ = body.Close() })
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAIResponses, FinalRequestRelayFormat: types.RelayFormatOpenAIResponses, OriginModelName: "gpt-4o", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	info.SetEstimatePromptTokens(375867)
	return c, info, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, body
}

// This same file is replayable in the real upstream/main checkout via Go's
// read-only overlay; it deliberately uses only APIs available on both sides.
func TestForkResponsesLateUsageCandidate(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, mode := range []string{"complete", "client_gone", "total_timeout", "response_timeout_before_first_frame", "upstream_eof", "upstream_read_error"} {
		t.Run(mode, func(t *testing.T) {
			c, info, response, body := forkLateUsageFixture(t, mode)
			usage, apiErr := OaiResponsesStreamHandler(c, info, response)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			if mode == "complete" {
				require.True(t, body.terminalRead)
				require.Equal(t, 389301, usage.PromptTokens)
				require.Equal(t, 1542, usage.CompletionTokens)
				require.Equal(t, 389298, usage.PromptTokensDetails.CachedTokens)
			} else {
				require.False(t, body.terminalRead)
				wantPrompt := 375867
				if mode == "response_timeout_before_first_frame" {
					wantPrompt = 0 // Accumulator did not observe any event.
				}
				require.Equal(t, wantPrompt, usage.PromptTokens)
				require.Zero(t, usage.CompletionTokens)
				require.Zero(t, usage.PromptTokensDetails.CachedTokens)
			}
			t.Logf("candidate prompt=%d output=%d cached=%d terminal_read=%t", usage.PromptTokens, usage.CompletionTokens, usage.PromptTokensDetails.CachedTokens, body.terminalRead)
		})
	}
}
