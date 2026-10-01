package controller

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// relay-timeout-cost-bearing.md §3.2 billing matrix, end to end. For the same
// request cut by our own deadline, the three billing modes must order as
// charged(charge) >= charged(input) >= charged(refund), and refund charges 0
// whenever nothing was delivered. Before the 2026-10-01 fixes, a stream-adapted
// charge request with no output (and a stream cut before its response headers
// in input/charge) charged 0.

// adaptiveHang opens an SSE response (then stalls) only for stream bodies, so a
// charge user's adapted call gets headers while the plain call of the other
// modes never gets a response.
func adaptiveHang() http.Handler {
	stream := sseThenHang()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"stream":true`)) {
			r.Body = io.NopCloser(bytes.NewReader(body))
			stream.ServeHTTP(w, r)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
}

func TestTimeoutChargeFloorInvariant(t *testing.T) {
	for _, sc := range []struct {
		name      string
		upstream  func() http.Handler
		stream    bool
		delivered bool
		// noChargeRecord: an active stream in charge mode bills what was
		// received and the user bears it, so nothing is recorded as absorbed.
		noChargeRecord bool
	}{
		{name: "non-stream, no response headers", upstream: hangUpstream},
		{name: "non-stream, adapted headers without output", upstream: adaptiveHang},
		{name: "stream, no response headers", upstream: hangUpstream, stream: true},
		{name: "stream, headers without delivery", upstream: func() http.Handler { return sseThenHang() }, stream: true, noChargeRecord: true},
		{name: "stream with delivery", upstream: func() http.Handler { return sseThenHang(costBearingContentChunk) }, stream: true, delivered: true},
	} {
		t.Run(sc.name, func(t *testing.T) {
			charged := map[string]int{}
			for _, mode := range []string{"refund", "input", "charge"} {
				f := newCostBearingFixture(t, costBearingOptions{upstream: sc.upstream()})
				f.setUser(t, map[string]any{
					"non_stream_response_timeout": 1, "stream_response_timeout": 1, "stream_total_timeout": 1,
					"non_stream_timeout_billing": mode,
				})
				extra := ""
				if sc.stream {
					extra = `"stream":true,`
				}
				rec := f.chat(t, extra)
				if !sc.stream {
					require.Equal(t, http.StatusGatewayTimeout, rec.Code, "%s: %s", mode, rec.Body.String())
				}
				total := 0
				for _, log := range f.logs(t, model.LogTypeConsume) {
					total += log.Quota
					if !sc.delivered {
						assert.Zero(t, log.CompletionTokens, "%s: output is never charged without delivery", mode)
					}
				}
				f.spent(t, total)
				charged[mode] = total
				if !sc.delivered && mode != "refund" && !(mode == "charge" && sc.noChargeRecord) {
					consumes := f.logs(t, model.LogTypeConsume)
					require.Len(t, consumes, 1, mode)
					assert.NotNil(t, timeoutAbsorbedOf(t, consumes[0].Other), "%s: the absorbed record rides on the consume log", mode)
				}
			}
			assert.GreaterOrEqual(t, charged["charge"], charged["input"], "%v", charged)
			assert.GreaterOrEqual(t, charged["input"], charged["refund"], "%v", charged)
			if !sc.delivered {
				assert.Zero(t, charged["refund"], "%v", charged)
				assert.Positive(t, charged["input"], "%v", charged)
				assert.Positive(t, charged["charge"], "%v", charged)
			}
		})
	}
}

// Our own timeout on a Claude-format non-stream request answers with the
// Anthropic type timeout_error, as the Claude stream terminal frame does.
func TestClaudeNonStreamTimeoutUsesTimeoutErrorType(t *testing.T) {
	f := newCostBearingFixture(t, costBearingOptions{channelType: constant.ChannelTypeAnthropic, upstream: hangUpstream()})
	f.engine.POST("/v1/messages", func(c *gin.Context) { Relay(c, types.RelayFormatClaude) })
	f.setUser(t, map[string]any{"non_stream_response_timeout": 1})
	rec := f.post(t, "/v1/messages", `{"model":"`+f.modelName+`","max_tokens":30,"messages":[{"role":"user","content":"hello there"}]}`)
	require.Equal(t, http.StatusGatewayTimeout, rec.Code, rec.Body.String())
	assert.Equal(t, "error", gjson.Get(rec.Body.String(), "type").String())
	assert.Equal(t, "timeout_error", gjson.Get(rec.Body.String(), "error.type").String(), rec.Body.String())
}
