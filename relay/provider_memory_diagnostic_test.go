package relay_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Generates invalid JSON without allocating a fixture as large as the response.
// These are diagnostic assertions of current behavior, not desired limits.
type providerDiagnosticBody struct {
	remaining int
	read      int
	closed    bool
	terminal  error
}

func (b *providerDiagnosticBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, b.terminal
	}
	n := min(len(p), b.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	b.remaining -= n
	b.read += n
	return n, nil
}

func (b *providerDiagnosticBody) Close() error { b.closed = true; return nil }

func TestProviderMemoryDiagnosticMalformedResponses(t *testing.T) {
	type handler func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
	handlers := map[string]handler{
		"openai_chat":   openai.OpenaiHandler,
		"gemini_native": gemini.GeminiTextGenerationHandler,
		"claude": func(c *gin.Context, info *relaycommon.RelayInfo, r *http.Response) (*dto.Usage, *types.NewAPIError) {
			return claude.ClaudeHandler(c, r, info)
		},
		"openai_compact": func(c *gin.Context, _ *relaycommon.RelayInfo, r *http.Response) (*dto.Usage, *types.NewAPIError) {
			return openai.OaiResponsesCompactionHandler(c, r)
		},
	}
	for name, handle := range handlers {
		for _, size := range []int{256*1024 - 1, 256 * 1024, 256*1024 + 1, 4 * 1024 * 1024} {
			for _, terminal := range []error{io.EOF, io.ErrUnexpectedEOF} {
				t.Run(fmt.Sprintf("%s/%d/%v", name, size, terminal), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
					body := &providerDiagnosticBody{remaining: size, terminal: terminal}
					_, err := handle(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, ContentLength: -1})
					require.NotNil(t, err)
					require.Equal(t, size, body.read, "current handlers read the whole malformed response before rejecting it")
					require.True(t, body.closed)
				})
			}
		}
	}
}
