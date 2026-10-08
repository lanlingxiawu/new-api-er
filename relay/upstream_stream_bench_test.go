package relay

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// sseStream builds a realistic upstream body: n content chunks plus a final
// usage frame and [DONE].
func sseStream(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		delta := `{"content":"token%d "}`
		if i == 0 {
			delta = `{"role":"assistant","content":"token%d "}`
		}
		fmt.Fprintf(&b, `data: {"id":"c","created":1790000000,"model":"gpt-4o","choices":[{"index":0,"delta":`+delta+`,"finish_reason":null}]}`+"\n", i)
	}
	b.WriteString(`data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}` + "\n")
	b.WriteString("data: [DONE]\n")
	return b.String()
}

// BenchmarkChatStreamAggregator measures the per-chunk cost of re-assembly,
// which is the work the adaptation adds to a non-stream request.
func BenchmarkChatStreamAggregator(b *testing.B) {
	for _, chunks := range []int{10, 100, 1000} {
		raw := sseStream(chunks)
		lines := strings.Split(strings.TrimSpace(raw), "\n")
		payloads := make([]string, 0, len(lines))
		for _, line := range lines {
			if data, ok := adaptedStreamPayload(line); ok && data != "[DONE]" {
				payloads = append(payloads, data)
			}
		}

		b.Run(fmt.Sprintf("chunks=%d", chunks), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				agg := newChatStreamAggregator("gpt-4o")
				for _, data := range payloads {
					var chunk dto.ChatCompletionsStreamResponse
					if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
						b.Fatal(err)
					}
					agg.AddChunk(&chunk)
				}
				if out := agg.Snapshot("fb", false); out == nil {
					b.Fatal("nil snapshot")
				}
			}
		})
	}
}

// BenchmarkAdaptedStreamErrorFrame measures the per-frame cost of the in-band
// error probe, which runs on EVERY chunk of an adapted stream.
func BenchmarkAdaptedStreamErrorFrame(b *testing.B) {
	cases := map[string]string{
		"ordinary content":     `{"id":"c","choices":[{"index":0,"delta":{"content":"hello there"}}]}`,
		"content saying error": `{"id":"c","choices":[{"index":0,"delta":{"content":"the error was fixed"}}]}`,
		"actual error":         `{"error":{"message":"boom","type":"server_error"}}`,
		// Upstreams that stamp an untyped error on every chunk pay this per frame.
		"untyped error beside content": `{"id":"c","error":{"code":200},"choices":[{"index":0,"delta":{"content":"hello there"}}]}`,
	}
	for name, frame := range cases {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = adaptedStreamErrorFrame(frame)
			}
		})
	}
}

// BenchmarkAdaptedStreamPayload measures SSE line parsing, run once per line.
func BenchmarkAdaptedStreamPayload(b *testing.B) {
	line := `data: {"id":"c","choices":[{"index":0,"delta":{"content":"hello"}}]}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = adaptedStreamPayload(line)
	}
}

// BenchmarkNonStreamBaseline is the control: parsing one complete non-stream
// body, which is what the request would have cost WITHOUT the adaptation.
// Comparing it against BenchmarkChatStreamAggregator gives the real added cost.
func BenchmarkNonStreamBaseline(b *testing.B) {
	body := `{"id":"c","object":"chat.completion","created":1790000000,"model":"gpt-4o",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"` +
		strings.Repeat("token ", 100) + `"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var out dto.OpenAITextResponse
		if err := common.UnmarshalJsonStr(body, &out); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAdaptationDecision measures the gate that runs on every eligible
// non-stream request, including the ones that are NOT adapted — this is the
// cost imposed on users who never enabled the feature.
func BenchmarkAdaptationDecision(b *testing.B) {
	c, info, request := adaptCase(nil)
	b.Run("eligible", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = shouldAdaptUpstreamStream(c, info, request)
		}
	})
	// The common production shape: user never enabled charge, so the gate must
	// bail out on its first condition.
	c2, info2, request2 := adaptCase(nil)
	c2.Set("user_non_stream_timeout_billing", "refund")
	b.Run("not eligible (refund)", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = shouldAdaptUpstreamStream(c2, info2, request2)
		}
	})
}

// realisticSSE mirrors what OpenAI actually sends with include_usage: every
// frame carries object/system_fingerprint/logprobs and a "usage":null that only
// the final frame fills in. The per-frame parse cost depends on these fields,
// so a stripped-down fixture understates it.
func realisticSSE(n int) string {
	var b strings.Builder
	const head = `data: {"id":"chatcmpl-AbCdEf0123456789","object":"chat.completion.chunk","created":1790000000,"model":"gpt-4o-2024-08-06","service_tier":"default","system_fingerprint":"fp_7a1b2c3d4e","choices":[{"index":0,"delta":`
	for i := 0; i < n; i++ {
		delta := fmt.Sprintf(`{"content":" token%d"}`, i)
		if i == 0 {
			delta = `{"role":"assistant","content":"","refusal":null}`
		}
		b.WriteString(head + delta + `,"logprobs":null,"finish_reason":null}],"usage":null}` + "\n\n")
	}
	b.WriteString(head + `{},"logprobs":null,"finish_reason":"stop"}],"usage":null}` + "\n\n")
	b.WriteString(`data: {"id":"chatcmpl-AbCdEf0123456789","object":"chat.completion.chunk","created":1790000000,"model":"gpt-4o-2024-08-06","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// BenchmarkHandleAdaptedUpstreamStream measures the whole adapted response
// path — SSE scanning, per-frame decoding, aggregation and the final write —
// which is what the load-test profile attributed to handleAdaptedUpstreamStream.
func BenchmarkHandleAdaptedUpstreamStream(b *testing.B) {
	gin.SetMode(gin.TestMode)
	for _, chunks := range []int{3, 30, 100, 1000} {
		body := realisticSSE(chunks)
		b.Run(fmt.Sprintf("chunks=%d", chunks), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				info.UpstreamStreamAdapted = true
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
				if _, apiErr := handleAdaptedUpstreamStream(c, info, resp); apiErr != nil {
					b.Fatal(apiErr)
				}
			}
		})
	}
}

// BenchmarkAddFrameExtras measures the per-frame cost of recovering fields the
// stream DTO does not model. It runs on EVERY adapted frame, so the frames that
// carry none of those fields must stay near free.
func BenchmarkAddFrameExtras(b *testing.B) {
	cases := map[string]string{
		"plain content frame":         `{"id":"c","choices":[{"index":0,"delta":{"content":"hello there"}}]}`,
		"null refusal and null usage": `{"id":"c","choices":[{"index":0,"delta":{"content":"hi","refusal":null}}],"usage":null}`,
		"refusal text":                `{"id":"c","choices":[{"index":0,"delta":{"refusal":"I can't help."}}]}`,
		"annotations":                 `{"id":"c","choices":[{"index":0,"delta":{"annotations":[{"type":"url_citation","url_citation":{"url":"https://a"}}]}}]}`,
		// Azure repeats the verdict on most frames; an unchanged one is not copied.
		"azure content filter": `{"id":"c","choices":[{"index":0,"content_filter_results":{"hate":{"filtered":false,"severity":"safe"}},"delta":{"content":"hi"}}]}`,
	}
	for name, frame := range cases {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			agg := newChatStreamAggregator("gpt-4o")
			for i := 0; i < b.N; i++ {
				agg.AddFrameExtras(frame)
				if i%1024 == 0 {
					agg = newChatStreamAggregator("gpt-4o")
				}
			}
		})
	}
}
