package gemini

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// This is a loopback vendor workload, not a live-provider benchmark or a wallet
// settlement test. It is opt-in; defaults run one correctness pass, and explicit
// env controls let the operator schedule a larger run without other load processes.
type forkGeminiLoadCase struct {
	name, wire, text                      string
	failed, effective, tool, cut, timeout bool
	prompt, output, cache                 int // -1 means an intentionally estimated field.
	confirmed                             bool
}

func forkGeminiLoadCases() []forkGeminiLoadCase {
	const usage = `"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"cachedContentTokenCount":40,"totalTokenCount":120}`
	const prefix = "data: {" + usage + "}\n\n"
	const first = "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Alpha 🌐\"}]}}]," + usage + "}\n\n"
	const last = "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Omega\"}]},\"finishReason\":\"STOP\"}]," + usage + "}\n\n"
	return []forkGeminiLoadCase{
		{name: "complete_cache", wire: first + last, text: "Alpha 🌐Omega", effective: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "max_tokens", wire: first + strings.Replace(last, `"STOP"`, `"MAX_TOKENS"`, 1), text: "Alpha 🌐Omega", effective: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "tool_complete", wire: "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"weather\",\"args\":{\"city\":\"東京\"}}}]},\"finishReason\":\"STOP\"}]," + usage + "}\n\n", effective: true, tool: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "safety", wire: "data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\"}," + usage + "}\n\n", prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "missing_usage", wire: strings.ReplaceAll(first+last, ","+usage, ""), text: "Alpha 🌐Omega", effective: true, prompt: 999, output: -1},
		{name: "explicit_zero", wire: "data: {\"candidates\":[{\"index\":0,\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":0,\"candidatesTokenCount\":0,\"totalTokenCount\":0,\"cachedContentTokenCount\":0}}\n\n", confirmed: true},
		{name: "cache_zero_overwrites", wire: first + strings.Replace(last, `"cachedContentTokenCount":40`, `"cachedContentTokenCount":0`, 1), text: "Alpha 🌐Omega", effective: true, prompt: 100, output: 20, confirmed: true},
		{name: "cache_omitted", wire: first + strings.Replace(last, ","+usage, "", 1), text: "Alpha 🌐Omega", effective: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "eof_without_delivery", wire: prefix, failed: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "eof_after_text", wire: first, text: "Alpha 🌐", failed: true, effective: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "provider_error", wire: first + "data: {\"error\":{\"code\":503,\"message\":\"local vendor unavailable\",\"status\":\"UNAVAILABLE\"}}\n\n", text: "Alpha 🌐", failed: true, effective: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "bad_json", wire: first + "data: {broken}\n\n", text: "Alpha 🌐", failed: true, effective: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "invalid_usage", wire: first + "data: {\"usageMetadata\":{\"promptTokenCount\":-1}}\n\n", text: "Alpha 🌐", failed: true, effective: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "transport_cut", wire: first + "data: {\"candidates\":[", text: "Alpha 🌐", failed: true, effective: true, cut: true, prompt: 100, output: 20, cache: 40, confirmed: true},
		{name: "gateway_deadline", wire: first, text: "Alpha 🌐", failed: true, effective: true, timeout: true, prompt: -1, output: -1, cache: -1, confirmed: true},
	}
}

type forkGeminiLoadTimeout struct{ context.Context }

func (c forkGeminiLoadTimeout) RelayTimeoutKind() string {
	if c.Err() == context.DeadlineExceeded {
		return "total_timeout"
	}
	return ""
}

type forkGeminiLoadResult struct {
	index   int
	format  types.RelayFormat
	info    *relaycommon.RelayInfo
	usage   *dto.Usage
	body    string
	elapsed time.Duration
	err     error
}

func forkGeminiLoadRequest(client *http.Client, base string, index int, tc forkGeminiLoadCase, format types.RelayFormat) (result forkGeminiLoadResult) {
	started := time.Now()
	result.index, result.format = index, format
	defer func() {
		result.elapsed = time.Since(started)
		if p := recover(); p != nil {
			result.err = fmt.Errorf("handler panic: %v", p)
		}
	}()
	deadline := 3 * time.Second
	if tc.timeout {
		deadline = 150 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:streamGenerateContent", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	if tc.timeout {
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, forkGeminiLoadTimeout{ctx})
	}
	info := &relaycommon.RelayInfo{StartTime: started, IsStream: true, DisablePing: true, ShouldIncludeUsage: true, RelayFormat: format, FinalRequestRelayFormat: types.RelayFormatGemini, OriginModelName: "gemini-2.5-flash", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini, UpstreamModelName: "gemini-2.5-flash"}}
	info.SetEstimatePromptTokens(999)
	service.BeginStreamAttempt(c, info)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%d", base, index), nil)
	if err != nil {
		result.err = err
		return
	}
	resp, err := client.Do(request)
	if err != nil {
		result.err = err
		return
	}
	defer resp.Body.Close()
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	info.StreamSession.ObserveHTTP(resp)
	handler := GeminiChatStreamHandler
	if format == types.RelayFormatGemini {
		handler = GeminiTextGenerationStreamHandler
	} else if format == types.RelayFormatOpenAIResponses {
		handler = GeminiResponsesStreamHandler
	}
	u, _ := handler(c, info, resp)
	selected := service.FinalizeStreamUsage(c, info, u)
	before := rec.Body.Len()
	if service.FinalizeStreamUsage(c, info, nil) != selected || rec.Body.Len() != before {
		result.err = fmt.Errorf("finalization is not idempotent")
		return
	}
	if selected == nil || info.StreamResult == nil {
		result.err = fmt.Errorf("missing result/usage")
		return
	}
	result.info, result.usage, result.body = info, selected, rec.Body.String()
	return
}

// Only incremental content is counted: Responses also repeats text in done and
// completed snapshots, which must not be mistaken for duplicated token deltas.
func forkGeminiLoadValidate(r forkGeminiLoadResult, tc forkGeminiLoadCase) error {
	if r.err != nil {
		return r.err
	}
	out := r.info.StreamResult
	if out.Failed != tc.failed || out.EffectiveContent != tc.effective {
		return fmt.Errorf("failed/effective=%t/%t want %t/%t; reason=%s", out.Failed, out.EffectiveContent, tc.failed, tc.effective, r.info.StreamSession.Snapshot().Reason)
	}
	var text, args strings.Builder
	errors, terminals := 0, 0
	for _, line := range strings.Split(r.body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			terminals++
			continue
		}
		v := gjson.Parse(data)
		kind := v.Get("type").String()
		if v.Get("error").IsObject() || kind == "error" || kind == "response.failed" {
			errors++
		}
		switch r.format {
		case types.RelayFormatGemini:
			for _, c := range v.Get("candidates").Array() {
				if c.Get("finishReason").String() != "" {
					terminals++
				}
				for _, p := range c.Get("content.parts").Array() {
					if !p.Get("thought").Bool() {
						text.WriteString(p.Get("text").String())
					}
					if p.Get("functionCall").Exists() {
						args.WriteString(p.Get("functionCall.args").Raw)
					}
				}
			}
			if v.Get("promptFeedback.blockReason").Exists() {
				terminals++
			}
		case types.RelayFormatOpenAI:
			for _, c := range v.Get("choices").Array() {
				text.WriteString(c.Get("delta.content").String())
				for _, call := range c.Get("delta.tool_calls").Array() {
					args.WriteString(call.Get("function.arguments").String())
				}
			}
		case types.RelayFormatClaude:
			if kind == "message_stop" {
				terminals++
			}
			if v.Get("delta.type").String() == "text_delta" {
				text.WriteString(v.Get("delta.text").String())
			}
			if v.Get("delta.type").String() == "input_json_delta" {
				args.WriteString(v.Get("delta.partial_json").String())
			}
		case types.RelayFormatOpenAIResponses:
			if kind == "response.completed" || kind == "response.incomplete" {
				terminals++
			}
			if kind == "response.output_text.delta" {
				text.WriteString(v.Get("delta").String())
			}
			if kind == "response.function_call_arguments.delta" {
				args.WriteString(v.Get("delta").String())
			}
		}
	}
	if text.String() != tc.text {
		return fmt.Errorf("incremental text %q want %q", text.String(), tc.text)
	}
	if tc.tool && (!gjson.Valid(args.String()) || gjson.Get(args.String(), "city").String() != "東京") {
		return fmt.Errorf("tool JSON lost/duplicated: %q", args.String())
	}
	if tc.failed {
		if terminals != 0 || (!tc.timeout && errors != 1) {
			return fmt.Errorf("failed stream errors=%d success_terminals=%d", errors, terminals)
		}
	} else if terminals != 1 || errors != 0 {
		return fmt.Errorf("healthy stream errors=%d terminals=%d", errors, terminals)
	}
	if out.SettlementAttempted {
		return fmt.Errorf("workload unexpectedly entered wallet settlement")
	}
	u := r.usage
	if canonical, ok := u.BillingUsage.CanonicalUsage(); ok {
		u = canonical
	}
	prompt, output, cache := tc.prompt, tc.output, tc.cache

	if tc.failed && !tc.effective && !tc.timeout {
		prompt, output, cache = 0, 0, 0
	}
	if prompt >= 0 && u.PromptTokens != prompt || output >= 0 && u.CompletionTokens != output || cache >= 0 && u.PromptTokensDetails.CachedTokens != cache {
		return fmt.Errorf("usage=%d/%d/cache%d want=%d/%d/cache%d source=%s", u.PromptTokens, u.CompletionTokens, u.PromptTokensDetails.CachedTokens, prompt, output, cache, out.UsageSource)
	}
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.PromptTokensDetails.CachedTokens < 0 {
		return fmt.Errorf("negative usage")
	}
	if tc.timeout {
		d := out.Diagnostic.Termination
		if d == nil || d.FirstObserved == nil || d.FirstObserved.Side != "gateway" || d.FirstObserved.TimeoutKind != "total_timeout" {
			return fmt.Errorf("gateway deadline attribution missing: %+v", d)
		}
	}
	return nil
}

func TestForkGeminiVendorConcurrentLoad(t *testing.T) {
	if os.Getenv("FORK_VENDOR_LOAD") != "1" {
		t.Skip("set FORK_VENDOR_LOAD=1 to run the isolated loopback workload")
	}
	workers, repeats := 4, 1
	for key, target := range map[string]*int{"FORK_VENDOR_LOAD_WORKERS": &workers, "FORK_VENDOR_LOAD_REPEATS": &repeats} {
		if raw := os.Getenv(key); raw != "" {
			v, err := strconv.Atoi(raw)
			require.NoError(t, err)
			*target = v
		}
	}
	require.GreaterOrEqual(t, workers, 1)
	require.LessOrEqual(t, workers, 256)
	require.GreaterOrEqual(t, repeats, 1)
	require.LessOrEqual(t, repeats, 1000)
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	common.LogWriterMu.Lock()
	oldWriter, oldErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		gin.SetMode(previousMode)
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = oldWriter, oldErrorWriter
		common.LogWriterMu.Unlock()
	})
	cases := forkGeminiLoadCases()
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	baselineGoroutines := runtime.NumGoroutine()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil || i < 0 || i >= len(cases) {
			http.Error(w, "unknown local fixture", 404)
			return
		}
		tc := cases[i]
		w.Header().Set("Content-Type", "text/event-stream")
		if tc.cut {
			w.Header().Set("Content-Length", strconv.Itoa(len(tc.wire)+256))
		}
		for remaining := tc.wire; remaining != ""; {
			n := min(11, len(remaining))
			if _, err := io.WriteString(w, remaining[:n]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			remaining = remaining[n:]
		}
		if tc.timeout {
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	transport := &http.Transport{MaxIdleConns: workers, MaxIdleConnsPerHost: workers, MaxConnsPerHost: workers}
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	defer func() {
		transport.CloseIdleConnections()
		server.Close()
		deadline := time.Now().Add(2 * time.Second)
		for runtime.NumGoroutine() > baselineGoroutines && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		runtime.GC()
		var drained runtime.MemStats
		runtime.ReadMemStats(&drained)
		t.Logf("vendor=Gemini drained=true gc=true heap_baseline=%d heap_after_drain=%d goroutines_baseline=%d goroutines_after_drain=%d", baseline.HeapAlloc, drained.HeapAlloc, baselineGoroutines, runtime.NumGoroutine())
	}()
	formats := []types.RelayFormat{types.RelayFormatGemini, types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses}

	t.Run("stream", func(t *testing.T) {
		type job struct {
			index  int
			format types.RelayFormat
		}
		jobs := make(chan job, workers)
		results := make(chan forkGeminiLoadResult, workers)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		goroutines := runtime.NumGoroutine()
		started := time.Now()
		var group sync.WaitGroup
		for i := 0; i < workers; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				for j := range jobs {
					results <- forkGeminiLoadRequest(client, server.URL, j.index, cases[j.index], j.format)
				}
			}()
		}
		go func() {
			for n := 0; n < repeats; n++ {
				for i := range cases {
					for _, format := range formats {
						jobs <- job{i, format}
					}
				}
			}
			close(jobs)
			group.Wait()
			close(results)
		}()
		latencies := make([]time.Duration, 0, len(cases)*len(formats)*repeats)
		bad, success, failed := 0, 0, 0
		for r := range results {
			latencies = append(latencies, r.elapsed)
			if err := forkGeminiLoadValidate(r, cases[r.index]); err != nil {
				bad++
				if bad <= 8 {
					t.Logf("FAIL %s/%s: %v", cases[r.index].name, r.format, err)
				}
			}
			if r.info != nil && r.info.StreamResult != nil && !r.info.StreamResult.Failed {
				success++
			} else {
				failed++
			}
		}
		elapsed := time.Since(started)
		runtime.ReadMemStats(&after)
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		t.Logf("vendor=Gemini workers=%d repetitions=%d requests=%d successful=%d expected_failure=%d incorrect=%d elapsed=%s requests_per_second=%.2f p50=%s p95=%s p99=%s allocated_bytes=%d heap_before=%d heap_live=%d goroutines_before=%d goroutines_live=%d", workers, repeats, len(latencies), success, failed, bad, elapsed, float64(len(latencies))/elapsed.Seconds(), latencies[len(latencies)/2], latencies[(len(latencies)-1)*95/100], latencies[(len(latencies)-1)*99/100], after.TotalAlloc-before.TotalAlloc, before.HeapAlloc, after.HeapAlloc, goroutines, runtime.NumGoroutine())
		require.Len(t, latencies, len(cases)*len(formats)*repeats)
		require.Zero(t, bad, "all local vendor responses must satisfy protocol and usage invariants")
	})

}
