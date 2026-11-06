package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Synthetic vendor-schema fixtures only. No credentials, external provider,
// database, settlement queue or business balance is used by this component load.
// Protocol source: https://platform.claude.com/docs/en/build-with-claude/streaming
type forkFaultLoadCase struct {
	name, body, mode, reason, side, operation string
	prompt, output                            int // -1 permits a bounded partial-content estimate.
	failed, gone                              bool
}

func forkFaultLoadCases() []forkFaultLoadCase {
	start := forkClaudeStart(`{"input_tokens":21,"output_tokens":0}`)
	text := forkClaudeText(0)
	partial := service.EstimateTokenByModel(forkClaudeModel, "Hello, 世界 🌍")
	full := start + text + forkClaudeClose(0) + forkClaudeFinish("end_turn", 12)
	errorFrame := forkClaudeEvent(`{"type":"error","error":{"type":"overloaded_error","message":"Synthetic overload"}}`)
	large := strings.Replace(full, "Hello, 世界 🌍", strings.Repeat("x", 128<<10), 1)
	return []forkFaultLoadCase{
		{"text", full, "", "done", "", "", 21, 12, false, false},
		{"thinking_text", start + forkClaudeThinking(0) + forkClaudeText(1) + forkClaudeClose(1) + forkClaudeFinish("end_turn", 12), "", "done", "", "", 21, 12, false, false},
		{"tool", start + forkClaudeTool(0) + forkClaudeFinish("tool_use", 12), "", "done", "", "", 21, 12, false, false},
		{"explicit_zero", forkClaudeStart(`{"input_tokens":0,"output_tokens":0}`) + forkClaudeFinish("end_turn", 0), "", "done", "", "", 0, 0, false, false},
		{"missing_usage", forkClaudeStart(""), "", "upstream_incomplete", "upstream", "read", 0, 0, true, false},
		{"empty_body", "", "", "upstream_incomplete", "upstream", "read", 0, 0, true, false},
		// Confirmed input with no delivered text, tool, or media is released.
		{"start_only", start, "", "upstream_incomplete", "upstream", "read", 0, 0, true, false},
		{"text_eof", start + text, "", "upstream_incomplete", "upstream", "read", 21, partial, true, false},
		{"short_http_body", start + text, "short", "upstream_read_error", "upstream", "read", 21, partial, true, false},
		{"tcp_reset", start + text, "rst", "upstream_read_error", "upstream", "read", 21, partial, true, false},
		{"half_frame", start + "event: content_block_delta\ndata: {\"type\":", "", "upstream_incomplete", "upstream", "read", 0, 0, true, false},
		{"invalid_json", start + "event: content_block_start\ndata: {oops}\n\n", "", "upstream_json_error", "upstream", "protocol", 0, 0, true, false},
		{"invalid_sse", start + "event: ping\ndata: {\"type\":\"message_stop\"}\n\n", "", "upstream_protocol_error", "upstream", "protocol", 0, 0, true, false},
		{"invalid_sequence", start + forkClaudeClose(0), "", "upstream_protocol_error", "upstream", "protocol", 0, 0, true, false},
		{"provider_error", start + errorFrame, "", "upstream_error", "upstream", "protocol", 0, 0, true, false},
		{"http_429", "", "http_429", "http_rejected", "", "", 0, 0, true, false},
		{"http_500", "", "http_500", "http_rejected", "", "", 0, 0, true, false},
		{"http_503", "", "http_503", "http_rejected", "", "", 0, 0, true, false},
		{"delayed_headers", full, "slow_headers", "done", "", "", 21, 12, false, false},
		{"no_response_headers", "", "no_headers", "before_headers_timeout", "", "", 0, 0, true, false},
		{"headers_then_silence", "", "stall", "timeout", "gateway", "timeout", 0, 0, true, false},
		{"heartbeat_total_timeout", start, "heartbeat", "timeout", "gateway", "timeout", 0, 0, true, false},
		{"text_then_timeout", start + text, "stall", "timeout", "gateway", "timeout", 21, partial, true, false},
		{"client_cancel", start + text, "cancel", "client_gone", "downstream", "", 21, partial, true, true},
		{"slow_downstream", large, "slow", "done", "", "", 21, 12, false, false},
		{"cancel_timeout_cross", start + text, "cross", "client_gone", "downstream", "", 21, partial, true, true},
	}
}

type forkFaultLoadState struct {
	fixture    forkFaultLoadCase
	converted  bool
	ack        chan struct{}
	ackOnce    sync.Once
	originDone chan struct{}
	result     chan forkFaultLoadResult
}

type forkFaultLoadResult struct {
	problem                                           string
	reason                                            string
	status, prompt, output, errors, terminals, closes int
	failed, gone                                      bool
	termination                                       *relaycommon.StreamTerminationDiagnostic
	diagnosticBytes                                   int
}

type forkFaultTimeoutControl struct{ expired atomic.Bool }

func (c *forkFaultTimeoutControl) RelayTimeoutKind() string {
	if c.expired.Load() {
		return "total_timeout"
	}
	return ""
}
func (c *forkFaultTimeoutControl) WriteTerminalError(write func()) { write() }

// Counts accepted terminal bytes without retaining a complete response copy.
type forkFaultLoadWriter struct {
	http.ResponseWriter
	state             *forkFaultLoadState
	errors, terminals int
	textWritten       bool
	headerWritten     bool
}

func (w *forkFaultLoadWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *forkFaultLoadWriter) WriteHeader(status int) {
	if !w.headerWritten {
		w.headerWritten = true
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *forkFaultLoadWriter) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	n, err := w.ResponseWriter.Write(p)
	text := string(p[:max(0, min(n, len(p)))])
	w.errors += strings.Count(text, `"error":`)
	w.terminals += strings.Count(text, "event: message_stop\n") + strings.Count(text, "data: [DONE]")
	if strings.Contains(text, "Hello") {
		w.textWritten = true
	}
	return n, err
}
func (w *forkFaultLoadWriter) Flush() { _ = w.FlushError() }
func (w *forkFaultLoadWriter) FlushError() error {
	w.WriteHeader(http.StatusOK)
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err == nil && w.textWritten {
		w.state.ackOnce.Do(func() { close(w.state.ack) })
	}
	return err
}

type forkFaultCountBody struct {
	io.ReadCloser
	closes atomic.Int32
}

func (b *forkFaultCountBody) Close() error { b.closes.Add(1); return b.ReadCloser.Close() }

func forkFaultLoadCheck(state *forkFaultLoadState, got forkFaultLoadResult) error {
	want := state.fixture
	if got.problem != "" {
		return errors.New(got.problem)
	}
	if got.reason != want.reason {
		return fmt.Errorf("reason=%s want=%s", got.reason, want.reason)
	}
	if want.mode == "no_headers" {
		if got.status != http.StatusGatewayTimeout || got.termination != nil || got.closes != 0 {
			return fmt.Errorf("pre-header gate: %+v", got)
		}
		return nil
	}
	if strings.HasPrefix(want.mode, "http_") {
		status, _ := strconv.Atoi(strings.TrimPrefix(want.mode, "http_"))
		if got.status != status || got.termination != nil || got.closes != 1 || got.prompt != 0 || got.output != 0 || got.terminals != 0 {
			return fmt.Errorf("rejected HTTP gate: %+v", got)
		}
		return nil
	}
	if got.status != 200 || got.failed != want.failed || got.gone != want.gone || got.closes != 1 {
		return fmt.Errorf("status/outcome/close mismatch: %+v", got)
	}
	prompt := want.prompt
	if want.name == "explicit_zero" && state.converted {
		prompt = 50
	} // Preserved upstream conversion estimate policy.
	if got.prompt != prompt || got.output < 0 || got.output > 100000 || want.output >= 0 && got.output != want.output {
		return fmt.Errorf("usage=%d/%d want=%d/%d", got.prompt, got.output, prompt, want.output)
	}
	if got.errors > 1 || got.terminals > 1 {
		return fmt.Errorf("duplicate termination: errors=%d complete=%d", got.errors, got.terminals)
	}
	if want.failed && got.terminals != 0 || !want.failed && (got.terminals != 1 || got.errors != 0) {
		return fmt.Errorf("terminal mismatch: errors=%d complete=%d", got.errors, got.terminals)
	}
	if want.failed && !want.gone && got.errors != 1 {
		return fmt.Errorf("missing error: %d", got.errors)
	}
	if got.termination == nil || got.termination.UpstreamClose == nil {
		return errors.New("missing cleanup evidence")
	}
	first := got.termination.FirstObserved
	if !want.failed {
		if first != nil {
			return fmt.Errorf("normal first observation: %+v", first)
		}
		return nil
	}
	if first == nil || first.Side != want.side || want.operation != "" && first.Operation != want.operation {
		return fmt.Errorf("first observation=%+v want=%s/%s", first, want.side, want.operation)
	}
	if (want.mode == "stall" || want.mode == "heartbeat") && first.TimeoutKind != "total_timeout" {
		return fmt.Errorf("timeout kind=%s", first.TimeoutKind)
	}
	return nil
}

// Opt-in closed-loop load. Both network hops are actual loopback HTTP; arrivals
// are bounded by workers. This measures component/harness capacity, not an
// authenticated production gateway or database/billing throughput.
func TestForkClaudeMixedFaultLoad(t *testing.T) {
	if os.Getenv("FORK_CLAUDE_FAULT_LOAD") != "1" {
		t.Skip("opt-in loopback mixed fault component load")
	}
	fixtures := forkFaultLoadCases()
	workers, count, stallMS := 4, len(fixtures)*2*4, 80
	for _, p := range []struct {
		name   string
		dst    *int
		lo, hi int
	}{
		{"FORK_CLAUDE_FAULT_WORKERS", &workers, 1, 128},
		{"FORK_CLAUDE_FAULT_REQUESTS", &count, len(fixtures) * 2, 100000},
		{"FORK_CLAUDE_FAULT_STALL_MS", &stallMS, 20, 5000},
	} {
		if raw := os.Getenv(p.name); raw != "" {
			n, err := strconv.Atoi(raw)
			require.NoError(t, err)
			require.GreaterOrEqual(t, n, p.lo)
			require.LessOrEqual(t, n, p.hi)
			*p.dst = n
		}
	}
	oldMode := gin.Mode()
	gin.SetMode(gin.ReleaseMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	var states sync.Map // At most workers entries; removed after both HTTP owners exit.
	var originActive, proxyActive atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originActive.Add(1)
		defer originActive.Add(-1)
		value, ok := states.Load(r.URL.Query().Get("id"))
		if !ok {
			http.Error(w, "unknown local fixture", 500)
			return
		}
		state := value.(*forkFaultLoadState)
		defer close(state.originDone)
		f := state.fixture
		if f.mode == "no_headers" {
			<-r.Context().Done()
			return
		}
		if strings.HasPrefix(f.mode, "http_") {
			status, _ := strconv.Atoi(strings.TrimPrefix(f.mode, "http_"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"Synthetic HTTP rejection"}}`)
			return
		}
		if f.mode == "slow_headers" {
			timer := time.NewTimer(time.Duration(stallMS/4) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if f.mode == "short" {
			w.Header().Set("Content-Length", strconv.Itoa(len(f.body)+100))
		}
		w.WriteHeader(200)
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		for offset := 0; offset < len(f.body); offset += 37 {
			if _, err := io.WriteString(w, f.body[offset:min(offset+37, len(f.body))]); err != nil {
				return
			}
			if f.mode == "slow" {
				if err := http.NewResponseController(w).Flush(); err != nil {
					return
				}
			}
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		if f.mode == "heartbeat" {
			ticker := time.NewTicker(time.Duration(max(1, stallMS/8)) * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
					if _, err := io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n"); err != nil {
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
				}
			}
		}
		if f.mode == "rst" {
			select {
			case <-state.ack:
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second):
				return
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			_ = conn.Close()
			return
		}
		if f.mode == "stall" || f.mode == "cancel" || f.mode == "cross" {
			<-r.Context().Done()
		}
	}))
	defer origin.Close()
	upTransport := &http.Transport{MaxConnsPerHost: workers, MaxIdleConns: workers, MaxIdleConnsPerHost: workers}
	defer upTransport.CloseIdleConnections()
	upClient := &http.Client{Transport: upTransport}
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyActive.Add(1)
		defer proxyActive.Add(-1)
		value, ok := states.Load(r.URL.Query().Get("id"))
		if !ok {
			http.Error(w, "unknown local fixture", 500)
			return
		}
		state := value.(*forkFaultLoadState)
		result := forkFaultLoadResult{status: 200}
		defer func() { state.result <- result }()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		upCtx, upCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer upCancel()
		writer := &forkFaultLoadWriter{ResponseWriter: w, state: state}
		c, _ := gin.CreateTestContext(writer)
		c.Request = r.WithContext(ctx)
		info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: forkClaudeModel}}
		info.SetEstimatePromptTokens(50)
		if state.converted {
			info.RelayFormat = types.RelayFormatOpenAI
		}
		control := &forkFaultTimeoutControl{}
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
		service.BeginStreamAttempt(c, info)
		if state.fixture.mode == "no_headers" || state.fixture.mode == "stall" || state.fixture.mode == "cross" || state.fixture.mode == "heartbeat" {
			timer := time.AfterFunc(time.Duration(stallMS)*time.Millisecond, func() { control.expired.Store(true); cancel(); upCancel() })
			defer timer.Stop()
		}
		req, err := http.NewRequestWithContext(upCtx, http.MethodPost, origin.URL+"?id="+r.URL.Query().Get("id"), nil)
		if err != nil {
			result.problem = err.Error()
			return
		}
		resp, err := upClient.Do(req)
		info.StreamSession.ObserveTransport(resp, err)
		if err != nil {
			if !control.expired.Load() {
				result.problem = "unexpected local transport failure before headers"
			}
			result.reason = "before_headers_timeout"
			result.status = 504
			other := model.NewLogOther()
			service.AppendStreamErrorDiagnostic(c, other, err)
			if d, ok := other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic); ok {
				result.termination = d.Termination
			}
			w.WriteHeader(504)
			return
		}
		body := &forkFaultCountBody{ReadCloser: resp.Body}
		resp.Body = body
		info.StreamDiagnostic.Observe(resp)
		info.StreamSession.ObserveHTTP(resp)
		if resp.StatusCode != http.StatusOK {
			apiErr := service.RelayErrorHandler(c, resp, false)
			result.reason, result.status, result.failed = "http_rejected", apiErr.StatusCode, true
			result.closes = int(body.closes.Load())
			other := model.NewLogOther()
			service.AppendStreamErrorDiagnostic(c, other, apiErr)
			if _, exists := other.Snapshot()["stream_diagnostic"]; exists {
				result.problem = "non-success HTTP response entered private stream capture"
			}
			w.WriteHeader(apiErr.StatusCode)
			return
		}
		usage, apiErr := ClaudeStreamHandler(c, resp, info)
		if apiErr != nil {
			result.problem = apiErr.Error()
			return
		}
		usage = service.FinalizeStreamUsage(c, info, usage)
		if usage == nil || info.StreamResult == nil {
			result.problem = "missing finalized outcome"
			return
		}
		result.reason = string(info.StreamStatus.EndReason)
		result.failed = info.StreamResult.Failed
		result.gone = info.StreamResult.ClientGone
		result.prompt, result.output = usage.PromptTokens, usage.CompletionTokens
		result.errors, result.terminals, result.closes = writer.errors, writer.terminals, int(body.closes.Load())
		result.termination = info.StreamDiagnostic.Termination.Snapshot()
		other := model.NewLogOther()
		service.AppendStreamLogInfo(info, other)
		raw, marshalErr := common.Marshal(other)
		if marshalErr != nil {
			result.problem = marshalErr.Error()
			return
		}
		result.diagnosticBytes = len(raw)
		if d := info.StreamDiagnostic.Snapshot(); len(d.BodyHead)+len(d.BodyTail) > 2048 {
			result.problem = "capture exceeded bounds"
		}
	}))
	proxy.Listener = slowClientListener{proxy.Listener}
	proxy.Start()
	defer proxy.Close()
	downTransport := &http.Transport{MaxConnsPerHost: workers, MaxIdleConns: workers, MaxIdleConnsPerHost: workers}
	defer downTransport.CloseIdleConnections()
	downClient := &http.Client{Transport: downTransport, Timeout: 12 * time.Second}
	baseline := runtime.NumGoroutine()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var peakHeap atomic.Uint64
	var peakGoroutines atomic.Int64
	peakHeap.Store(before.HeapAlloc)
	peakGoroutines.Store(int64(baseline))
	sampleStop, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-sampleStop:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				if stats.HeapAlloc > peakHeap.Load() {
					peakHeap.Store(stats.HeapAlloc)
				}
				if n := int64(runtime.NumGoroutine()); n > peakGoroutines.Load() {
					peakGoroutines.Store(n)
				}
			}
		}
	}()
	latencies := make([]time.Duration, count)
	problems := make([]string, count)
	scenarioCounts := make([]atomic.Int64, len(fixtures)*2)
	var expectedFaults, bytes atomic.Int64
	jobs := make(chan int, workers)
	var wg sync.WaitGroup
	started := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				start := time.Now()
				index := id % (len(fixtures) * 2)
				f := fixtures[index/2]
				state := &forkFaultLoadState{fixture: f, converted: index%2 == 1, ack: make(chan struct{}), originDone: make(chan struct{}), result: make(chan forkFaultLoadResult, 1)}
				key := strconv.Itoa(id)
				states.Store(key, state)
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				req, _ := http.NewRequestWithContext(ctx, http.MethodPost, proxy.URL+"?id="+key, nil)
				resp, err := downClient.Do(req)
				if err == nil {
					if f.mode == "cancel" || f.mode == "cross" {
						select {
						case <-state.ack:
						case <-ctx.Done():
						}
						cancel()
					} else if f.mode == "slow" {
						buf := make([]byte, 4096)
						for {
							_, readErr := resp.Body.Read(buf)
							if readErr != nil {
								break
							}
							time.Sleep(time.Millisecond)
						}
					} else {
						_, _ = io.Copy(io.Discard, resp.Body)
					}
					_ = resp.Body.Close()
				}
				select {
				case result := <-state.result:
					if check := forkFaultLoadCheck(state, result); check != nil {
						problems[id] = fmt.Sprintf("%s/converted_%t: %v", f.name, state.converted, check)
					}
					bytes.Add(int64(result.diagnosticBytes))
				case <-time.After(12 * time.Second):
					problems[id] = "proxy did not finish"
				}
				cancel()
				select {
				case <-state.originDone:
				case <-time.After(2 * time.Second):
					if problems[id] == "" {
						problems[id] = "origin did not drain"
					}
				}
				states.Delete(key)
				latencies[id] = time.Since(start)
				scenarioCounts[index].Add(1)
				if f.failed {
					expectedFaults.Add(1)
				}
			}
		}()
	}
	for i := 0; i < count; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(started)
	close(sampleStop)
	<-sampled
	downTransport.CloseIdleConnections()
	upTransport.CloseIdleConnections()
	proxy.Close()
	origin.Close()
	require.Eventually(t, func() bool {
		return originActive.Load() == 0 && proxyActive.Load() == 0 && runtime.NumGoroutine() <= baseline+2
	}, 5*time.Second, 10*time.Millisecond, "local owners must drain")
	runtime.ReadMemStats(&after)
	runtime.GC()
	var retained runtime.MemStats
	runtime.ReadMemStats(&retained)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	unexpected := 0
	examples := make([]string, 0, 5)
	for _, p := range problems {
		if p != "" {
			unexpected++
			if len(examples) < 5 {
				examples = append(examples, p)
			}
		}
	}
	coverage := map[string]int64{}
	for i := range scenarioCounts {
		coverage[fmt.Sprintf("%s/converted_%t", fixtures[i/2].name, i%2 == 1)] = scenarioCounts[i].Load()
	}
	report := map[string]any{"requests": count, "workers": workers, "expected_faults": expectedFaults.Load(), "unexpected": unexpected, "examples": examples, "elapsed_s": elapsed.Seconds(), "rps": float64(count) / elapsed.Seconds(), "p50_ms": float64(latencies[count/2]) / 1e6, "p95_ms": float64(latencies[count*95/100]) / 1e6, "p99_ms": float64(latencies[count*99/100]) / 1e6, "stall_ms": stallMS, "heap_before": before.HeapAlloc, "heap_after_gc": retained.HeapAlloc, "allocated_bytes": after.TotalAlloc - before.TotalAlloc, "gc_cycles": after.NumGC - before.NumGC, "goroutines_before": baseline, "goroutines_after": runtime.NumGoroutine(), "diagnostic_bytes": bytes.Load(), "coverage": coverage}
	report["heap_peak"], report["goroutines_peak"], report["gc_pause_ms"] = peakHeap.Load(), peakGoroutines.Load(), float64(after.PauseTotalNs-before.PauseTotalNs)/1e6
	encoded, err := common.Marshal(report)
	require.NoError(t, err)
	t.Logf("CLAUDE_MIXED_FAULT_LOAD %s", encoded)
	require.Zero(t, unexpected, "unexpected behavior; examples=%v", examples)
}
