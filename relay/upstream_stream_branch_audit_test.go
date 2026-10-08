package relay

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Branch audit of the upstream-stream adaptation (non-stream client, streaming
// upstream). These cover gaps left by the existing upstream_stream_* tests:
// SSE framing variants, reads split at arbitrary byte boundaries, the scanner
// line ceiling, multi-choice and interleaved tool-call assembly, usage parity
// with the plain OpenaiHandler, error-frame shapes, a real transport with a
// client that goes away, and Rule 5 zero values across the stream rewrite.

func auditDecode(t *testing.T, rec *httptest.ResponseRecorder) dto.OpenAITextResponse {
	t.Helper()
	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// Every framing a spec-conforming SSE producer may use must assemble to the
// same answer; non-data fields (event, id, retry, comments) carry nothing.
func TestBranchAudit_SSEFramingVariants(t *testing.T) {
	cases := map[string]struct {
		body      string
		completed bool
	}{
		"data without space": {
			"data:{\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n" +
				"data:{\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n" +
				"data:[DONE]\n", true},
		"leading whitespace": {
			"  data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n" +
				"\tdata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n" +
				"data: [DONE]\n", true},
		"event id retry and comment lines": {
			"retry: 3000\n: keep-alive\nevent: message\nid: 1\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
				"event: message\nid: 2\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"event: done\ndata: [DONE]\n\n", true},
		"CRLF with blank CRLF lines": {
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\r\n\r\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\r\n\r\n" +
				"data: [DONE]\r\n\r\n", true},
		"whitespace-only data line skipped": {
			"data:    \n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":\"stop\"}]}\n" +
				"data: [DONE]   \n", true},
		"frames after DONE are not read": {
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":\"stop\"}]}\n" +
				"data: [DONE]\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" extra\"}}]}\n", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(tc.body))
			require.Nil(t, apiErr)
			out := auditDecode(t, rec)
			require.Len(t, out.Choices, 1)
			assert.Equal(t, "Hello", out.Choices[0].Message.StringContent())
			assert.Equal(t, "stop", out.Choices[0].FinishReason)
			if tc.completed {
				assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
			}
		})
	}
}

// oneByteBody returns the body one byte per Read, so every frame, every
// multi-byte UTF-8 rune and the CRLF pair are split across reads.
type oneByteBody struct{ r io.Reader }

func (b *oneByteBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return b.r.Read(p[:1])
}

func TestBranchAudit_FramesSplitAcrossReads(t *testing.T) {
	body := "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你好，\"}}]}\r\n\r\n" +
		"data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"世界🌍\"},\"finish_reason\":\"stop\"}]}\r\n\r\n" +
		"data: {\"id\":\"c\",\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4,\"total_tokens\":13}}\r\n\r\n" +
		"data: [DONE]\r\n\r\n"
	c, rec, info := bufferedCase(t)
	resp := upstreamSSE("")
	resp.Body = io.NopCloser(&oneByteBody{r: strings.NewReader(body)})

	usage, apiErr := handleAdaptedUpstreamStream(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 9, usage.PromptTokens)
	assert.Equal(t, 4, usage.CompletionTokens)
	out := auditDecode(t, rec)
	assert.Equal(t, "你好，世界🌍", out.Choices[0].Message.StringContent(), "runes split across reads must not be mangled")
	assert.Equal(t, 3, info.ReceivedResponseCount)
}

// A single line past the scanner ceiling is a read error (bufio.ErrTooLong).
// Output that already arrived is delivered marked incomplete; with none the
// request fails for the retry path. Memory is bounded by the ceiling either way.
func TestBranchAudit_LineBeyondScannerCeiling(t *testing.T) {
	previous := constant.StreamScannerMaxBufferMB
	constant.StreamScannerMaxBufferMB = 1
	t.Cleanup(func() { constant.StreamScannerMaxBufferMB = previous })
	huge := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"" + strings.Repeat("x", 2<<20) + "\"}}]}\n"

	t.Run("after output", func(t *testing.T) {
		c, rec, info := bufferedCase(t)
		usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(contentFrame+huge))
		require.Nil(t, apiErr)
		require.NotNil(t, usage)
		assert.Greater(t, usage.CompletionTokens, 0)
		out := auditDecode(t, rec)
		assert.Equal(t, "paid output", out.Choices[0].Message.StringContent())
		assert.Equal(t, "length", out.Choices[0].FinishReason)
		assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
		assert.ErrorIs(t, info.StreamStatus.EndError, bufio.ErrTooLong)
	})
	t.Run("first line", func(t *testing.T) {
		c, rec, info := bufferedCase(t)
		usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(huge))
		require.NotNil(t, apiErr)
		assert.Nil(t, usage)
		assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
		assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
		assert.Empty(t, rec.Body.String())
	})
}

// Tool-call fragments for different indexes arrive interleaved (parallel tool
// calls); each call must collect only its own fragments, keep first-seen order,
// and be counted once for per-call pricing and the estimate surcharge.
func TestBranchAudit_InterleavedToolCallsByIndex(t *testing.T) {
	operation_setting.SetToolPriceForTest("audit_priced", 0.01)
	defer operation_setting.SetToolPriceForTest("audit_priced", 0)

	body := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"audit_priced","arguments":""}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"other","arguments":"{\"y\""}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":"}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":":2}"}},{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	c, rec, info := bufferedCase(t)
	info.OriginModelName = "gpt-4o"
	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)

	out := auditDecode(t, rec)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "tool_calls", out.Choices[0].FinishReason)
	calls := out.Choices[0].Message.ParseToolCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, "call_a", calls[0].ID)
	assert.Equal(t, "audit_priced", calls[0].Function.Name)
	assert.Equal(t, `{"x":1}`, calls[0].Function.Arguments)
	assert.Equal(t, "call_b", calls[1].ID)
	assert.Equal(t, "other", calls[1].Function.Name)
	assert.Equal(t, `{"y":2}`, calls[1].Function.Arguments)
	assert.Equal(t, "function", calls[1].Type)
	assert.False(t, gjson.Get(rec.Body.String(), "choices.0.message.tool_calls.0.index").Exists())
	assert.Equal(t, gjson.Null, gjson.Get(rec.Body.String(), "choices.0.message.content").Type,
		"a tool-call-only turn reports content null as the non-stream API does")

	assert.Equal(t, 1, toolCallCount(info, "audit_priced"))
	// No usage reported: estimate plus the 7-token surcharge per call, as the stream path does.
	require.NotNil(t, usage)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
	assert.GreaterOrEqual(t, usage.CompletionTokens, 2*7)
}

// n is forced to 1 for adaptation, but a stray index must still assemble into
// its own choice, sorted by index, each with its own finish_reason and extras.
func TestBranchAudit_MultipleChoicesSortedWithOwnFields(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":1,"delta":{"role":"assistant","content":"B"}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":"A"}}]}`,
		`data: {"id":"c","choices":[{"index":1,"delta":{"refusal":"no"},"finish_reason":"stop"},{"index":0,"delta":{"content":"a"},"finish_reason":"length"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	c, rec, info := bufferedCase(t)
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	out := auditDecode(t, rec)
	require.Len(t, out.Choices, 2)
	assert.Equal(t, 0, out.Choices[0].Index)
	assert.Equal(t, "Aa", out.Choices[0].Message.StringContent())
	assert.Equal(t, "length", out.Choices[0].FinishReason)
	assert.Equal(t, 1, out.Choices[1].Index)
	assert.Equal(t, "B", out.Choices[1].Message.StringContent())
	assert.Equal(t, "stop", out.Choices[1].FinishReason)
	assert.False(t, gjson.Get(rec.Body.String(), "choices.0.message.refusal").Exists(), "refusal belongs to index 1 only")
	assert.Equal(t, "no", gjson.Get(rec.Body.String(), "choices.1.message.refusal").String())
}

// Billing reads the typed usage (cache and reasoning details included); the
// client receives upstream's usage object byte-for-byte, non-standard keys too,
// exactly as the plain path forwards upstream's body.
func TestBranchAudit_UsageDetailsBilledAndForwardedVerbatim(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":null}`,
		`data: {"id":"c","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens_details":{"reasoning_tokens":30},"cost":0.25}}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	c, rec, info := bufferedCase(t)
	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Equal(t, 50, usage.CompletionTokens)
	assert.Equal(t, 80, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 30, usage.CompletionTokenDetails.ReasoningTokens)
	assert.Equal(t, 0.25, gjson.Get(rec.Body.String(), "usage.cost").Float())
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens), "real usage is not a local estimate")
}

// A later "usage": null (some upstreams repeat the key on every frame) must not
// erase the usage an earlier frame reported, in billing or in the body.
func TestBranchAudit_LaterNullUsageDoesNotClobber(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`data: {"id":"c","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	c, rec, info := bufferedCase(t)
	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, 7, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, int64(10), gjson.Get(rec.Body.String(), "usage.total_tokens").Int())
}

// Parity with main's plain non-stream handler: for the same answer delivered as
// one JSON body (plain) or as SSE (adapted), the billed usage, the client's
// text, finish_reason and the content-filter reject reason are the same.
func TestBranchAudit_UsageParityWithOpenaiHandler(t *testing.T) {
	cases := map[string]struct {
		plain  string
		stream string
	}{
		"upstream usage": {
			`{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}`,
			`data: {"id":"c","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}` + "\n" +
				`data: {"id":"c","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}` + "\n" +
				"data: [DONE]\n",
		},
		"zero prompt tokens": {
			`{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":5,"total_tokens":5,"completion_tokens_details":{"reasoning_tokens":2}}}`,
			`data: {"id":"c","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}` + "\n" +
				`data: {"id":"c","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":5,"total_tokens":5,"completion_tokens_details":{"reasoning_tokens":2}}}` + "\n" +
				"data: [DONE]\n",
		},
		"content filter": {
			`{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hel"},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":12,"completion_tokens":1,"total_tokens":13}}`,
			`data: {"id":"c","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":"content_filter"}]}` + "\n" +
				`data: {"id":"c","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":1,"total_tokens":13}}` + "\n" +
				"data: [DONE]\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			plainC, plainRec, plainInfo := bufferedCase(t)
			plainInfo.UpstreamStreamAdapted = false
			plainInfo.SetEstimatePromptTokens(33)
			plainResp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(tc.plain))}
			plainUsage, plainErr := openai.OpenaiHandler(plainC, plainInfo, plainResp)
			require.Nil(t, plainErr)

			c, rec, info := bufferedCase(t)
			info.SetEstimatePromptTokens(33)
			usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(tc.stream))
			require.Nil(t, apiErr)

			assert.Equal(t, plainUsage.PromptTokens, usage.PromptTokens)
			assert.Equal(t, plainUsage.CompletionTokens, usage.CompletionTokens)
			assert.Equal(t, plainUsage.TotalTokens, usage.TotalTokens)
			assert.Equal(t, plainUsage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, plainUsage.CompletionTokenDetails.ReasoningTokens, usage.CompletionTokenDetails.ReasoningTokens)

			plainOut, out := auditDecode(t, plainRec), auditDecode(t, rec)
			require.Len(t, out.Choices, len(plainOut.Choices))
			assert.Equal(t, plainOut.Choices[0].Message.StringContent(), out.Choices[0].Message.StringContent())
			assert.Equal(t, plainOut.Choices[0].FinishReason, out.Choices[0].FinishReason)
			assert.Equal(t, gjson.Get(plainRec.Body.String(), "usage").Raw, gjson.Get(rec.Body.String(), "usage").Raw,
				"the client's usage object matches the plain path's")
			assert.Equal(t,
				common.GetContextKeyString(plainC, constant.ContextKeyAdminRejectReason),
				common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
		})
	}
}

// In-band error shapes: a typed OpenAI error keeps its type/message for the
// controller; an untyped or non-object error becomes a generic error that does
// not echo the raw frame; "error": null and an "error" key below the top level
// are ordinary frames.
func TestBranchAudit_ErrorFrameShapes(t *testing.T) {
	t.Run("typed error first frame", func(t *testing.T) {
		c, rec, info := bufferedCase(t)
		_, apiErr := handleAdaptedUpstreamStream(c, info,
			upstreamSSE(`data: {"error":{"message":"quota gone","type":"insufficient_quota","code":"insufficient_quota"}}`+"\n"))
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
		assert.False(t, types.IsSkipRetryError(apiErr), "an upstream error frame stays retryable")
		assert.Empty(t, rec.Body.String(), "the controller writes the error")
		// Message/type fidelity is asserted in upstream_stream_branch_audit_regression_test.go.
	})
	t.Run("string error fails the request", func(t *testing.T) {
		c, rec, info := bufferedCase(t)
		_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(`data: {"error":"exploded"}`+"\n"))
		require.NotNil(t, apiErr)
		assert.Empty(t, rec.Body.String())
	})
	t.Run("null error is content", func(t *testing.T) {
		c, rec, info := bufferedCase(t)
		_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(
			`data: {"error":null,"choices":[{"index":0,"delta":{"content":"fine"},"finish_reason":"stop"}]}`+"\ndata: [DONE]\n"))
		require.Nil(t, apiErr)
		assert.Equal(t, "fine", auditDecode(t, rec).Choices[0].Message.StringContent())
	})
	t.Run("nested error key is content", func(t *testing.T) {
		c, rec, info := bufferedCase(t)
		_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(
			`data: {"choices":[{"index":0,"delta":{"content":"fine","tool_calls":[{"index":0,"id":"t","type":"function","function":{"name":"f","arguments":"{\"error\":1}"}}]},"finish_reason":"tool_calls"}]}`+"\ndata: [DONE]\n"))
		require.Nil(t, apiErr)
		out := auditDecode(t, rec)
		assert.Equal(t, `{"error":1}`, out.Choices[0].Message.ParseToolCalls()[0].Function.Arguments)
	})
}

// Error codes and statuses the controller's retry decision sees.
func TestBranchAudit_FailureCodes(t *testing.T) {
	cases := map[string]struct {
		body string
		code types.ErrorCode
	}{
		"empty body":          {"", types.ErrorCodeBadResponse},
		"only DONE":           {"data: [DONE]\n", types.ErrorCodeBadResponse},
		"malformed first":     {"data: {\"choices\":[\n", types.ErrorCodeBadResponseBody},
		"type mismatch first": {`data: {"choices":[{"index":"0","delta":{"content":"x"}}]}` + "\n", types.ErrorCodeBadResponseBody},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(tc.body))
			require.NotNil(t, apiErr)
			assert.Nil(t, usage)
			assert.Equal(t, tc.code, apiErr.GetErrorCode())
			assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
			assert.False(t, types.IsSkipRetryError(apiErr), "an upstream failure stays retryable")
			assert.Empty(t, rec.Body.String())
		})
	}
}

// Upstream's Content-Length describes the SSE body, not the assembled JSON;
// copying it would truncate or hang the client. Content-Type is replaced.
func TestBranchAudit_UpstreamLengthHeaderNotCopied(t *testing.T) {
	c, rec, info := bufferedCase(t)
	resp := upstreamSSE(`data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\ndata: [DONE]\n")
	resp.Header.Set("Content-Length", "3")
	resp.Header.Set("X-Upstream-Trace", "abc")
	_, apiErr := handleAdaptedUpstreamStream(c, info, resp)
	require.Nil(t, apiErr)
	assert.Equal(t, strconv.Itoa(rec.Body.Len()), rec.Header().Get("Content-Length"),
		"the length written is the assembled body's, never upstream's")
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "abc", rec.Header().Get("X-Upstream-Trace"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// signalBody signals once the first read returned data.
type signalBody struct {
	io.ReadCloser
	once  sync.Once
	first chan struct{}
}

func (b *signalBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.once.Do(func() { close(b.first) })
	}
	return n, err
}

// Over a real HTTP transport: the client goes away after the first frame while
// upstream keeps the stream open. The handler must return promptly (no reader
// left blocked), release the upstream connection (the server sees the cancel),
// and settle what was received as client_gone.
func TestBranchAudit_ClientGoneOverRealTransport(t *testing.T) {
	serverSawCancel := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"c","choices":[{"index":0,"delta":{"content":"paid output"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(serverSawCancel)
		case <-time.After(10 * time.Second):
		}
	}))
	defer server.Close()

	c, _, info := bufferedCase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	body := &signalBody{ReadCloser: resp.Body, first: make(chan struct{})}
	resp.Body = body
	go func() {
		<-body.first
		cancel()
	}()

	done := make(chan struct{})
	var usage *dto.Usage
	var apiErr *types.NewAPIError
	go func() {
		defer close(done)
		usage, apiErr = handleAdaptedUpstreamStream(c, info, resp)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler still blocked on the upstream body after the client left")
	}
	select {
	case <-serverSawCancel:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream connection was not released")
	}
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Greater(t, usage.CompletionTokens, 0)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
}

// Rule 5: explicit zero values the client sent must reach upstream after the
// stream rewrite, and again in the plain re-send that strips it.
func TestBranchAudit_ZeroValuesSurviveStreamRewrite(t *testing.T) {
	c, info, _ := adaptCase(t)
	raw := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"temperature":0,"top_p":0,"presence_penalty":0,"frequency_penalty":0,"seed":0,"n":1,"max_tokens":0,"logprobs":false,"parallel_tool_calls":false,"stream":false,"metadata":{"stream":"keep"}}`
	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.UnmarshalJsonStr(raw, &request))
	require.True(t, shouldAdaptUpstreamStream(c, info, &request))
	applyUpstreamStreamAdaptation(c, info, &request)

	adapted, err := common.Marshal(&request)
	require.NoError(t, err)
	for _, path := range []string{"temperature", "top_p", "presence_penalty", "frequency_penalty", "seed", "max_tokens"} {
		v := gjson.GetBytes(adapted, path)
		require.True(t, v.Exists(), "%s: explicit zero dropped on the adapted request", path)
		assert.Equal(t, float64(0), v.Float(), path)
	}
	assert.Equal(t, "false", gjson.GetBytes(adapted, "logprobs").Raw)
	assert.Equal(t, "false", gjson.GetBytes(adapted, "parallel_tool_calls").Raw)
	assert.Equal(t, "true", gjson.GetBytes(adapted, "stream").Raw)
	assert.True(t, gjson.GetBytes(adapted, "stream_options.include_usage").Bool())

	plain, err := withoutUpstreamStream(adapted)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(plain, "stream").Exists())
	assert.False(t, gjson.GetBytes(plain, "stream_options").Exists())
	assert.Equal(t, "keep", gjson.GetBytes(plain, "metadata.stream").String(), "only the top-level keys are removed")
	for _, path := range []string{"temperature", "top_p", "seed", "max_tokens", "logprobs", "parallel_tool_calls"} {
		assert.Equal(t, gjson.GetBytes(adapted, path).Raw, gjson.GetBytes(plain, path).Raw, path)
	}
}

// Request shapes on the eligibility boundary.
func TestBranchAudit_RequestEligibilityBoundaries(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want bool
	}{
		"top_logprobs zero is still set": {`{"top_logprobs":0}`, false},
		"modalities text only":           {`{"modalities":["text"]}`, true},
		"modalities not an array":        {`{"modalities":"audio"}`, false},
		"modalities empty":               {`{"modalities":[]}`, true},
		"audio null":                     {`{"audio":null}`, true},
		"functions null":                 {`{"functions":null}`, true},
		"function_call none":             {`{"function_call":"none"}`, false},
		"n zero":                         {`{"n":0}`, false},
		"tools are fine":                 {`{"tools":[{"type":"function","function":{"name":"f"}}]}`, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, info, _ := adaptCase(t)
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.raw, &request))
			assert.Equal(t, tc.want, requestSupportsUpstreamStream(&request, info))
		})
	}
}

// Status boundaries of the one-time plain re-send.
func TestBranchAudit_PlainResendStatusBoundaries(t *testing.T) {
	cases := map[int]bool{
		199: false, 200: false, 399: false,
		400: true, 404: true, 409: true, 499: true,
		401: false, 402: false, 403: false, 408: false, 413: false, 429: false,
		500: true, 501: false, 502: false, 503: false, 504: false, 599: false,
	}
	for status, want := range cases {
		assert.Equal(t, want, adaptedFailureWorthPlainResend(status), "status %d", status)
	}
}

// The plain re-send is itself a transport failure: the error is a DoRequest
// failure (retryable), and the adapted flag stays cleared.
func TestBranchAudit_PlainResendTransportFailure(t *testing.T) {
	c, _, info := bufferedCase(t)
	resp, closer, apiErr := fallBackFromRejectedAdaptation(c, info, failingAdaptor{}, rejected(http.StatusBadRequest), adaptedStorage(t))
	require.NotNil(t, apiErr)
	assert.Nil(t, resp)
	assert.Nil(t, closer)
	assert.Equal(t, types.ErrorCodeDoRequestFailed, apiErr.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(apiErr))
	assert.False(t, info.UpstreamStreamAdapted)
}

type failingAdaptor struct{ recordingAdaptor }

func (failingAdaptor) DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (any, error) {
	return nil, io.ErrUnexpectedEOF
}

// A closed adapted body storage (cannot rebuild the plain request) leaves the
// original response to the ordinary error path instead of failing harder.
func TestBranchAudit_PlainResendWithClosedStorage(t *testing.T) {
	c, _, info := bufferedCase(t)
	storage, err := common.CreateBodyStorage([]byte(`{"model":"m","stream":true}`))
	require.NoError(t, err)
	require.NoError(t, storage.Close())
	adaptor := &recordingAdaptor{status: http.StatusOK}
	original := rejected(http.StatusBadRequest)
	resp, closer, apiErr := fallBackFromRejectedAdaptation(c, info, adaptor, original, storage)
	require.Nil(t, apiErr)
	assert.Nil(t, closer)
	assert.Same(t, original, resp)
	assert.Empty(t, adaptor.sent)
	assert.True(t, info.UpstreamStreamAdapted)
}
