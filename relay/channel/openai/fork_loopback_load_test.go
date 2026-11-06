package openai

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Opt-in local experiments only. No router/authentication, real provider, DB,
// logging settlement, or credentials are involved. Repeats are whole matrices.
// FORK_LOOPBACK_LOAD=1 FORK_LOOPBACK_REPEAT=1 FORK_LOOPBACK_WORKERS=2
// go test ./relay/channel/openai -run '^TestForkLoopbackLoad' -count=1 -v
type forkLoadChecks struct{ failures []string }

func (c *forkLoadChecks) Errorf(format string, args ...any) {
	if len(c.failures) < 8 {
		c.failures = append(c.failures, fmt.Sprintf(format, args...))
	}
}

type forkLoadResult struct {
	name                         string
	expectedFault, observedFault bool
	failures                     []string
	errorCategory                string
	elapsed                      time.Duration
}

type forkLoadReport struct {
	Suite                string         `json:"suite"`
	Requests             int            `json:"requests"`
	ExpectedFaults       int            `json:"expected_faults"`
	ObservedFaults       int            `json:"observed_faults"`
	Unexpected           int            `json:"unexpected"`
	UnexpectedByCategory map[string]int `json:"unexpected_by_category"`
	ConnectionAttempts   int64          `json:"connection_attempts"`
	ConnectionSuccesses  int64          `json:"connection_successes"`
	Workers              int            `json:"workers"`
	Repeats              int            `json:"repeats"`
	ElapsedMS            float64        `json:"elapsed_ms"`
	RPS                  float64        `json:"rps"`
	P50MS                float64        `json:"p50_ms"`
	P95MS                float64        `json:"p95_ms"`
	P99MS                float64        `json:"p99_ms"`
	GoroutinesBefore     int            `json:"goroutines_before"`
	GoroutinesPeak       int            `json:"goroutines_peak"`
	GoroutinesAfter      int            `json:"goroutines_after"`
	HeapBefore           uint64         `json:"heap_before"`
	HeapAfter            uint64         `json:"heap_after_gc"`
	AllocatedBytes       uint64         `json:"allocated_bytes"`
	GCs                  uint32         `json:"gc_cycles"`
	GCPauseMS            float64        `json:"gc_pause_ms"`
	Cases                map[string]int `json:"cases"`
	Samples              []string       `json:"unexpected_samples,omitempty"`
}

// Count actual active TCP dials, including failures. Reused HTTP connections do
// not add attempts; each fully established Realtime job needs two connections.
type forkLoadConnections struct {
	attempts, successes atomic.Int64
}

func (c *forkLoadConnections) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c.attempts.Add(1)
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err == nil {
		c.successes.Add(1)
	}
	return conn, err
}

func forkLoadRequest(plan forkLoadResult, job int, run func(int) forkLoadResult) (result forkLoadResult) {
	defer func() {
		// A panic/setup failure must still belong to its planned scenario and
		// cannot disappear from expected-fault or request totals.
		result.name, result.expectedFault = plan.name, plan.expectedFault
		if p := recover(); p != nil {
			result.errorCategory = "panic"
			result.failures = []string{fmt.Sprintf("fixture panic: %v", p)}
		}
	}()
	return run(job)
}

func (r *forkLoadReport) Record(result forkLoadResult) {
	r.Requests++
	r.Cases[result.name]++
	if result.expectedFault {
		r.ExpectedFaults++
	}
	if result.observedFault {
		r.ObservedFaults++
	}
	if len(result.failures) == 0 {
		return
	}
	r.Unexpected++
	category := result.errorCategory
	if category != "transport_setup" && category != "panic" {
		category = "invariant"
	}
	r.UnexpectedByCategory[category]++
	if len(r.Samples) < 20 {
		r.Samples = append(r.Samples, result.name+" ["+category+"]: "+strings.Join(result.failures, "; "))
	}
}

func TestForkLoopbackReportAccounting(t *testing.T) {
	report := forkLoadReport{Cases: map[string]int{}, UnexpectedByCategory: map[string]int{"transport_setup": 0, "invariant": 0, "panic": 0}}
	for job := range 30 {
		plan := forkLoadResult{name: strconv.Itoa(job), expectedFault: job != 29}
		result := forkLoadRequest(plan, job, func(job int) forkLoadResult {
			switch {
			case job < 25:
				return forkLoadResult{errorCategory: "transport_setup", failures: []string{"synthetic dial failure"}}
			case job < 27:
				return forkLoadResult{failures: []string{"synthetic invariant failure"}}
			case job == 27:
				panic("synthetic fixture panic")
			case job == 28:
				return forkLoadResult{observedFault: true}
			default:
				return forkLoadResult{}
			}
		})
		report.Record(result)
	}
	require.Equal(t, 30, report.Requests)
	require.Equal(t, 29, report.ExpectedFaults)
	require.Equal(t, 1, report.ObservedFaults, "fixture setup failures are not expected upstream faults")
	require.Equal(t, 28, report.Unexpected)
	require.Equal(t, map[string]int{"transport_setup": 25, "invariant": 2, "panic": 1}, report.UnexpectedByCategory)
	require.Len(t, report.Samples, 20, "only samples are capped; category totals include all failures")
	require.Len(t, report.Cases, 30)
	for _, count := range report.Cases {
		require.Equal(t, 1, count, "every planned request, including panics, is counted once")
	}

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	connections := &forkLoadConnections{}
	conn, err := connections.DialContext(context.Background(), "tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = connections.DialContext(ctx, "tcp", server.Listener.Addr().String())
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int64(2), connections.attempts.Load())
	require.Equal(t, int64(1), connections.successes.Load())
}

func forkLoadConfig(t *testing.T) (int, int) {
	t.Helper()
	if os.Getenv("FORK_LOOPBACK_LOAD") != "1" {
		t.Skip("opt-in local load fixture; set FORK_LOOPBACK_LOAD=1")
	}
	repeat, workers := 1, 2
	for _, item := range []struct {
		key    string
		target *int
		max    int
	}{
		{"FORK_LOOPBACK_REPEAT", &repeat, 1000}, {"FORK_LOOPBACK_WORKERS", &workers, 128},
	} {
		if raw := os.Getenv(item.key); raw != "" {
			v, err := strconv.Atoi(raw)
			require.NoError(t, err)
			require.GreaterOrEqual(t, v, 1)
			require.LessOrEqual(t, v, item.max)
			*item.target = v
		}
	}
	oldMode, oldIdle := gin.Mode(), constant.StreamingTimeout
	gin.SetMode(gin.TestMode)
	constant.StreamingTimeout = 0
	t.Cleanup(func() { gin.SetMode(oldMode); constant.StreamingTimeout = oldIdle })
	return repeat, workers
}

// The harness has bounded workers, one sampler, and bounded result storage. All
// sockets/servers are closed before the final heap/goroutine snapshot.
func forkLoadRun(t *testing.T, suite string, plan []forkLoadResult, repeat, workers int, connections *forkLoadConnections, run func(int) forkLoadResult, cleanup func()) {
	t.Helper()
	count := len(plan)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	report := forkLoadReport{Suite: suite, Workers: workers, Repeats: repeat, GoroutinesBefore: runtime.NumGoroutine(), HeapBefore: before.HeapAlloc, Cases: map[string]int{}, UnexpectedByCategory: map[string]int{"transport_setup": 0, "invariant": 0, "panic": 0}}
	var peak atomic.Int64
	peak.Store(int64(report.GoroutinesBefore))
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if value := int64(runtime.NumGoroutine()); value > peak.Load() {
					peak.Store(value)
				}
			}
		}
	}()
	jobs, results := make(chan int, workers), make(chan forkLoadResult, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for job := range jobs {
				start := time.Now()
				result := forkLoadRequest(plan[job%count], job, run)
				result.elapsed = time.Since(start)
				results <- result
			}
		})
	}
	start := time.Now()
	go func() {
		for job := range count * repeat {
			jobs <- job
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	latencies := make([]time.Duration, 0, count*repeat)
	for result := range results {
		report.Record(result)
		latencies = append(latencies, result.elapsed)
	}
	elapsed := time.Since(start)
	cleanup()
	report.ConnectionAttempts, report.ConnectionSuccesses = connections.attempts.Load(), connections.successes.Load()
	close(stop)
	<-stopped
	runtime.GC()
	runtime.ReadMemStats(&after)
	report.GoroutinesPeak, report.GoroutinesAfter = int(peak.Load()), runtime.NumGoroutine()
	report.HeapAfter, report.AllocatedBytes = after.HeapAlloc, after.TotalAlloc-before.TotalAlloc
	report.GCs, report.GCPauseMS = after.NumGC-before.NumGC, float64(after.PauseTotalNs-before.PauseTotalNs)/1e6
	report.ElapsedMS, report.RPS = float64(elapsed)/float64(time.Millisecond), float64(report.Requests)/elapsed.Seconds()
	slices.Sort(latencies)
	if len(latencies) > 0 {
		// Nearest-rank percentiles, including the largest sample for small p99 sets.
		report.P50MS = float64(latencies[(len(latencies)*50+99)/100-1]) / float64(time.Millisecond)
		report.P95MS = float64(latencies[(len(latencies)*95+99)/100-1]) / float64(time.Millisecond)
		report.P99MS = float64(latencies[(len(latencies)*99+99)/100-1]) / float64(time.Millisecond)
	}
	encoded, err := common.Marshal(report)
	require.NoError(t, err)
	t.Logf("FORK_LOOPBACK_REPORT %s", encoded)
	require.Equal(t, count*repeat, report.Requests)
	require.Equal(t, report.Unexpected, report.UnexpectedByCategory["transport_setup"]+report.UnexpectedByCategory["invariant"]+report.UnexpectedByCategory["panic"])
	require.Zero(t, report.Unexpected, "unexpected samples: %v", report.Samples)
	require.Equal(t, report.ExpectedFaults, report.ObservedFaults)
}

type forkLoadHTTPCase struct {
	name, fault, body, reason, side, operation string
	upstream, client                           types.RelayFormat
	failed                                     bool
	input, output                              int // -1: this fault intentionally has no exact usage promise.
}

func forkLoadHTTPCases() []forkLoadHTTPCase {
	var cases []forkLoadHTTPCase
	for _, upstream := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, client := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude} {
			start, text := forkVendorSSE(forkVendorChatStart), forkVendorSSE(forkVendorChatText)
			usage := forkVendorSSE(`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28,"prompt_tokens_details":{"cached_tokens":5}}}`)
			finish := forkVendorSSE(forkVendorChatFinish) + "data: [DONE]\n\n"
			zero := start + forkVendorSSE(forkVendorChatFinish) + forkVendorSSE(`{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`) + "data: [DONE]\n\n"
			tool := forkVendorSSE(`{"id":"chatcmpl_fixture","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_fixture","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":null}]}`) + usage + usage + forkVendorSSE(`{"id":"chatcmpl_fixture","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`) + "data: [DONE]\n\n"
			if upstream == types.RelayFormatOpenAIResponses {
				start = forkVendorSSE(forkVendorResponsesStart)
				text = forkVendorSSE(forkVendorResponsesItem) + forkVendorSSE(forkVendorResponsesPart) + forkVendorSSE(forkVendorResponsesText)
				usage = forkVendorSSE(`{"type":"response.in_progress","response":{"id":"resp_fixture","status":"in_progress","usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28,"input_tokens_details":{"cached_tokens":5}}}}`)
				finish = forkVendorSSE(forkVendorResponsesTextDone) + forkVendorSSE(forkVendorResponsesPartDone) + forkVendorSSE(forkVendorResponsesItemDone) + forkVendorSSE(forkVendorResponsesTerminal("response.completed", "completed", `{"input_tokens":20,"output_tokens":8,"total_tokens":28,"input_tokens_details":{"cached_tokens":5}}`))
				zero = start + forkVendorSSE(`{"type":"response.completed","response":{"id":"resp_fixture","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)
				tool = start + forkVendorSSE(`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_fixture","type":"function_call","call_id":"call_fixture","name":"lookup","arguments":"","status":"in_progress"}}`) + forkVendorSSE(`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_fixture","delta":"{\"city\":\"Paris\"}"}`) + usage + usage + forkVendorSSE(`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_fixture","type":"function_call","call_id":"call_fixture","name":"lookup","arguments":"{\"city\":\"Paris\"}","status":"completed"}}`) + forkVendorSSE(`{"type":"response.completed","response":{"id":"resp_fixture","model":"gpt-4o","status":"completed","output":[{"id":"fc_fixture","type":"function_call","call_id":"call_fixture","name":"lookup","arguments":"{\"city\":\"Paris\"}","status":"completed"}],"usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28,"input_tokens_details":{"cached_tokens":5}}}}`)
			}
			prefix := start + text
			for _, fault := range []string{"text", "tool", "zero", "duplicate", "eof_before", "eof_after", "reset", "json", "shape", "bad_usage", "provider", "missing_terminal", "client_cancel", "write_fail", "stall_before", "stall_after", "bad_terminal", "failed_terminal"} {
				if upstream == types.RelayFormatOpenAI && (fault == "bad_terminal" || fault == "failed_terminal") {
					continue
				}
				tc := forkLoadHTTPCase{name: string(upstream) + "_to_" + string(client) + "/" + fault, upstream: upstream, client: client, fault: fault, body: prefix + usage + usage + finish, input: 20, output: 8}
				switch fault {
				case "text":
				case "tool":
					tc.body = tool
				case "zero":
					tc.body, tc.input, tc.output = zero, -1, 0
				case "duplicate":
					tc.body += finish
				default:
					tc.failed, tc.input, tc.output = true, -1, -1
					tc.body, tc.reason, tc.side, tc.operation = prefix, "upstream_incomplete", "upstream", "read"
					switch fault {
					case "eof_before":
						tc.body = ""
					case "eof_after":
					case "reset":
						tc.reason = "upstream_read_error"
					case "json":
						tc.body += "data: {broken}\n\n"
						tc.reason, tc.operation = "upstream_json_error", "protocol"
					case "shape":
						tc.body += "data: []\n\n"
						tc.reason, tc.operation = "upstream_protocol_error", "protocol"
					case "bad_usage":
						tc.body += "data: {\"usage\":{\"input_tokens\":20,\"output_tokens\":-1}}\n\n"
						tc.reason, tc.operation = "upstream_json_error", "protocol"
					case "provider":
						tc.body += "event: error\ndata: {\"error\":{\"type\":\"server_error\",\"message\":\"synthetic load fixture\"}}\n\n"
						tc.reason, tc.operation = "upstream_error", "protocol"
					case "missing_terminal":
						tc.body += "data: [DONE]\n\n"
						tc.reason, tc.operation = "upstream_protocol_error", "protocol"
					case "client_cancel":
						tc.reason, tc.side, tc.operation = "client_gone", "downstream", "request_cancel"
					case "write_fail":
						tc.reason, tc.side, tc.operation = "client_gone", "downstream", "write"
					case "stall_before":
						tc.body, tc.reason, tc.side, tc.operation = "", "timeout", "gateway", "timeout"
					case "stall_after":
						tc.reason, tc.side, tc.operation = "timeout", "gateway", "timeout"
					case "bad_terminal":
						tc.body += forkVendorSSE(`{"type":"response.completed","response":[]}`)
						tc.reason, tc.operation = "upstream_protocol_error", "protocol"
					case "failed_terminal":
						tc.body += forkVendorSSE(`{"type":"response.failed","response":{"id":"resp_fixture","status":"failed","error":{"code":"server_error","message":"synthetic load fixture"}}}`)
						tc.reason, tc.operation = "upstream_error", "protocol"
					}
				}
				cases = append(cases, tc)
			}
		}
	}
	return cases
}

type forkLoadHTTPExchange struct {
	tc   forkLoadHTTPCase
	seen chan struct{}
	once sync.Once
}

type forkLoadHTTPWriter struct {
	gin.ResponseWriter
	exchange *forkLoadHTTPExchange
	cancel   context.CancelFunc
	timeout  *forkRealtimeObservedTimeout
}

func (w *forkLoadHTTPWriter) Write(p []byte) (int, error) {
	if w.exchange.tc.fault == "write_fail" {
		return 0, io.ErrClosedPipe
	}
	n, err := w.ResponseWriter.Write(p)
	if strings.Contains(string(p), forkVendorText) {
		w.exchange.once.Do(func() {
			close(w.exchange.seen)
			switch w.exchange.tc.fault {
			case "client_cancel":
				w.cancel()
			case "stall_after":
				w.timeout.expired.Store(true)
				w.cancel()
			}
		})
	}
	return n, err
}

func TestForkLoopbackLoadHTTP(t *testing.T) {
	repeat, workers := forkLoadConfig(t)
	cases := forkLoadHTTPCases()
	var exchanges sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, ok := exchanges.Load(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		ex := value.(*forkLoadHTTPExchange)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// Prime-sized writes fragment SSE delimiters and JSON across real reads.
		for offset := 0; offset < len(ex.tc.body); offset += 17 {
			if _, err := io.WriteString(w, ex.tc.body[offset:min(offset+17, len(ex.tc.body))]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
		switch ex.tc.fault {
		case "reset":
			select {
			case <-ex.seen:
			case <-r.Context().Done():
				return
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				if tcp, ok := conn.(*net.TCPConn); ok {
					_ = tcp.SetLinger(0)
				}
				_ = conn.Close()
			}
		case "client_cancel", "stall_before", "stall_after", "write_fail":
			<-r.Context().Done()
		}
	}))
	connections := &forkLoadConnections{}
	transport := &http.Transport{MaxIdleConns: workers * 2, MaxIdleConnsPerHost: workers, MaxConnsPerHost: workers, DialContext: connections.DialContext}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	plan := make([]forkLoadResult, len(cases))
	for i, tc := range cases {
		plan[i] = forkLoadResult{name: tc.name, expectedFault: tc.failed}
	}
	forkLoadRun(t, "openai_http", plan, repeat, workers, connections, func(job int) forkLoadResult {
		tc := cases[job%len(cases)]
		result, check := forkLoadResult{name: tc.name, expectedFault: tc.failed}, &forkLoadChecks{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		safety := time.AfterFunc(4*time.Second, cancel)
		defer safety.Stop()
		path := "/" + strconv.Itoa(job)
		ex := &forkLoadHTTPExchange{tc: tc, seen: make(chan struct{})}
		exchanges.Store(path, ex)
		defer exchanges.Delete(path)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if !assert.NoError(check, err) {
			result.failures, result.errorCategory = check.failures, "transport_setup"
			return result
		}
		resp, err := client.Do(request)
		if !assert.NoError(check, err) {
			result.failures, result.errorCategory = check.failures, "transport_setup"
			return result
		}
		defer resp.Body.Close()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		timeout := &forkRealtimeObservedTimeout{kind: "total_timeout"}
		if tc.fault == "stall_before" {
			timeout.kind = "response_timeout"
		}
		if strings.HasPrefix(tc.fault, "stall_") {
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, timeout)
			common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, 1)
			common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, 1)
		}
		common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, "refund")
		c.Writer = &forkLoadHTTPWriter{ResponseWriter: c.Writer, exchange: ex, cancel: cancel, timeout: timeout}
		info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, ShouldIncludeUsage: true, RelayFormat: tc.client, FinalRequestRelayFormat: tc.upstream, OriginModelName: "gpt-4o", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
		info.SetEstimatePromptTokens(11)
		service.BeginStreamAttempt(c, info)
		info.StreamSession.ObserveTransport(resp, nil)
		info.StreamDiagnostic.Observe(resp)
		info.StreamSession.ObserveHTTP(resp)
		if tc.fault == "stall_before" {
			timer := time.AfterFunc(10*time.Millisecond, func() { timeout.expired.Store(true); cancel() })
			defer timer.Stop()
		}
		var usage *dto.Usage
		var apiErr *types.NewAPIError
		switch {
		case tc.upstream == types.RelayFormatOpenAI && tc.client == types.RelayFormatOpenAIResponses:
			usage, apiErr = OaiChatToResponsesStreamHandler(c, info, resp)
		case tc.upstream == types.RelayFormatOpenAI:
			usage, apiErr = OaiStreamHandler(c, info, resp)
		case tc.client == types.RelayFormatOpenAIResponses:
			usage, apiErr = OaiResponsesStreamHandler(c, info, resp)
		default:
			usage, apiErr = OaiResponsesToChatStreamHandler(c, info, resp)
		}
		if apiErr != nil && tc.fault == "write_fail" {
			// Conversion handlers retain their pre-existing propagated writer
			// error; the common lifecycle still owns the final stream result.
			assert.Equal(check, http.StatusInternalServerError, apiErr.StatusCode)
			assert.Equal(check, io.ErrClosedPipe.Error(), apiErr.Err.Error())
		} else {
			assert.Nil(check, apiErr)
		}
		usage = service.FinalizeStreamUsage(c, info, usage)
		wire := recorder.Body.String()
		assert.Same(check, usage, service.FinalizeStreamUsage(c, info, usage))
		assert.Equal(check, wire, recorder.Body.String())
		assert.False(check, info.StreamResult.SettlementAttempted)
		result.observedFault = info.StreamResult.Failed
		assert.Equal(check, tc.failed, result.observedFault)
		if tc.failed {
			switch tc.fault {
			case "client_cancel", "write_fail":
				assert.True(check, info.StreamResult.ClientGone)
				assert.Contains(check, []string{"client_gone", "handler_stop"}, string(info.StreamStatus.EndReason))
			case "stall_before", "stall_after":
				assert.False(check, info.StreamResult.ClientGone)
				assert.Contains(check, []string{"timeout", "client_gone", "handler_stop"}, string(info.StreamStatus.EndReason))
			default:
				assert.Equal(check, tc.reason, string(info.StreamStatus.EndReason))
			}
			d := info.StreamDiagnostic.Termination.Snapshot()
			if assert.NotNil(check, d) && assert.NotNil(check, d.FirstObserved) {
				assert.Equal(check, tc.side, d.FirstObserved.Side)
				assert.Equal(check, tc.operation, d.FirstObserved.Operation)
			}
			counts := forkVendorEventCounts(wire)
			assert.Zero(check, counts["DONE"]+counts["response.completed"]+counts["message_stop"], "failure cannot gain a success terminal")
		} else {
			assert.True(check, info.StreamResult.Diagnostic.UsageFinal)
			assert.Equal(check, tc.fault != "zero", info.StreamResult.EffectiveContent)
			if tc.input >= 0 {
				assert.Equal(check, tc.input, usage.PromptTokens)
				cached := usage.PromptTokensDetails.CachedTokens
				if usage.InputTokensDetails != nil {
					cached = usage.InputTokensDetails.CachedTokens
				}
				assert.Equal(check, 5, cached)
				assert.Equal(check, 5, info.StreamResult.Diagnostic.UsageEvidence["cached_tokens"])
				if usage.BillingUsage != nil {
					canonical, ok := usage.BillingUsage.CanonicalUsage()
					if assert.True(check, ok) {
						assert.Equal(check, 20, canonical.PromptTokens)
						assert.Equal(check, 8, canonical.CompletionTokens)
						assert.Equal(check, 5, canonical.PromptTokensDetails.CachedTokens)
					}
				}
			}
			if tc.output >= 0 {
				assert.Equal(check, tc.output, usage.CompletionTokens)
			}
			if tc.fault == "zero" {
				assert.Zero(check, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
				assert.Zero(check, info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
			}
		}
		assert.GreaterOrEqual(check, usage.PromptTokens, 0)
		assert.GreaterOrEqual(check, usage.CompletionTokens, 0)
		result.failures = check.failures
		return result
	}, func() { transport.CloseIdleConnections(); server.Close() })
}

// One shared loopback upgrade server serves all bounded workers. Each job
// closes its four sockets immediately; t.Cleanup does not retain per-job peers.
func TestForkLoopbackLoadRealtime(t *testing.T) {
	repeat, workers := forkLoadConfig(t)
	cases := []string{"normal", "three_rounds", "duplicate_done", "zero", "tool", "eof_before", "missing_done", "bad_json", "bad_usage", "provider_error", "failed_done", "client_cancel", "upstream_write", "downstream_write", "no_response_timeout"}
	plan := make([]forkLoadResult, len(cases))
	for i, mode := range cases {
		plan[i] = forkLoadResult{name: mode, expectedFault: !slices.Contains([]string{"normal", "three_rounds", "duplicate_done", "zero", "tool"}, mode)}
	}
	connections := &forkLoadConnections{}
	var pending sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, ok := pending.Load(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			value.(chan *websocket.Conn) <- peer
		}
	}))
	pair := func(ctx context.Context, key string) (*websocket.Conn, *websocket.Conn, *forkRealtimeObservedConn, error) {
		accepted := make(chan *websocket.Conn, 1)
		pending.Store(key, accepted)
		defer pending.Delete(key)
		var transport *forkRealtimeObservedConn
		dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second, NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := connections.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			transport = &forkRealtimeObservedConn{Conn: conn}
			return transport, nil
		}}
		managed, _, err := dialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+key, nil)
		if err != nil {
			return nil, nil, nil, err
		}
		select {
		case peer := <-accepted:
			return managed, peer, transport, nil
		case <-ctx.Done():
			_ = managed.Close()
			return nil, nil, nil, ctx.Err()
		}
	}
	forkLoadRun(t, "openai_realtime", plan, repeat, workers, connections, func(job int) forkLoadResult {
		mode := cases[job%len(cases)]
		failed := plan[job%len(plan)].expectedFault
		result, check := forkLoadResult{name: mode, expectedFault: failed}, &forkLoadChecks{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		safety := time.AfterFunc(4*time.Second, cancel)
		defer safety.Stop()
		client, clientPeer, clientTransport, err := pair(ctx, fmt.Sprintf("/client-%d", job))
		if !assert.NoError(check, err) {
			result.failures, result.errorCategory = check.failures, "transport_setup"
			return result
		}
		defer client.Close()
		defer clientPeer.Close()
		target, targetPeer, targetTransport, err := pair(ctx, fmt.Sprintf("/target-%d", job))
		if !assert.NoError(check, err) {
			result.failures, result.errorCategory = check.failures, "transport_setup"
			return result
		}
		defer target.Close()
		defer targetPeer.Close()
		_ = clientPeer.SetReadDeadline(time.Now().Add(3 * time.Second))
		_ = clientPeer.SetWriteDeadline(time.Now().Add(3 * time.Second))
		_ = targetPeer.SetWriteDeadline(time.Now().Add(3 * time.Second))
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
		timeout := &forkRealtimeObservedTimeout{kind: "response_timeout"}
		if mode == "no_response_timeout" {
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, timeout)
			common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, 1)
		}
		billing := &realtimeEndBilling{reserved: make(chan struct{}, 8)}
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
		service.BeginStreamAttempt(c, info)
		info.StreamSession.ObserveWebSocketHandshake(&http.Response{StatusCode: http.StatusSwitchingProtocols}, nil)
		info.StreamSession.BindUpstream(target)
		type ending struct {
			usage      *dto.RealtimeUsage
			panicValue any
		}
		finished := make(chan ending, 1)
		go func() {
			var value ending
			defer func() { value.panicValue = recover(); finished <- value }()
			_, value.usage = managedRealtimeHandler(c, info)
		}()
		rounds := 0
		if !failed {
			rounds = 1
		}
		if mode == "three_rounds" || mode == "duplicate_done" {
			rounds = 3
		}
		for round := range rounds {
			messages := []string{`{"type":"response.text.delta","delta":"hi"}`}
			if mode == "zero" {
				messages = nil
			}
			if mode == "tool" {
				messages = []string{`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_fixture","type":"function_call","call_id":"call_fixture","name":"lookup","arguments":""}}`, `{"type":"response.function_call_arguments.done","item_id":"fc_fixture","call_id":"call_fixture","name":"lookup","arguments":"{\"city\":\"Paris\"}"}`}
			}
			input, output, cached := 10, 2, 3
			if mode == "zero" {
				input, output, cached = 0, 0, 0
			}
			done := fmt.Sprintf(`{"type":"response.done","response":{"id":"round_%d","status":"completed","usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d,"input_token_details":{"cached_tokens":%d,"text_tokens":%d},"output_token_details":{"text_tokens":%d}}}}`, round, input, output, input+output, cached, input, output)
			messages = append(messages, done)
			if mode == "duplicate_done" {
				messages = append(messages, done, done)
			}
			for _, message := range messages {
				if !assert.NoError(check, targetPeer.WriteMessage(websocket.TextMessage, []byte(message))) {
					break
				}
				_, _, readErr := clientPeer.ReadMessage()
				if !assert.NoError(check, readErr) {
					break
				}
			}
		}
		side, operation := "upstream", "read"
		switch mode {
		case "normal", "three_rounds", "duplicate_done", "zero", "tool":
			assert.NoError(check, targetPeer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")))
		case "eof_before":
			_ = targetPeer.Close()
		case "missing_done":
			assert.NoError(check, targetPeer.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.text.delta","delta":"hi"}`)))
			_, _, err = clientPeer.ReadMessage()
			assert.NoError(check, err)
			_ = targetPeer.Close()
		case "bad_json", "bad_usage", "provider_error", "failed_done":
			side, operation = "upstream", "protocol"
			payload := `{"type":"broken"`
			switch mode {
			case "bad_usage":
				payload = `{"type":"response.done","response":{"id":"bad","status":"completed","usage":{"input_tokens":-1,"output_tokens":2}}}`
			case "provider_error":
				payload = `{"type":"error","error":{"type":"server_error","message":"synthetic load fixture"}}`
			case "failed_done":
				payload = `{"type":"response.done","response":{"id":"bad","status":"failed","status_details":{"type":"failed","error":{"type":"server_error","message":"synthetic load fixture"}}}}`
			}
			assert.NoError(check, targetPeer.WriteMessage(websocket.TextMessage, []byte(payload)))
		case "client_cancel":
			side, operation = "downstream", "request_cancel"
			cancel()
		case "upstream_write":
			side, operation = "upstream", "write"
			targetTransport.failWrite.Store(true)
			assert.NoError(check, clientPeer.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{}}`)))
		case "downstream_write":
			side, operation = "downstream", "write"
			clientTransport.failWrite.Store(true)
			assert.NoError(check, targetPeer.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.created","session":{}}`)))
		case "no_response_timeout":
			side, operation = "gateway", "timeout"
			timer := time.AfterFunc(10*time.Millisecond, func() { timeout.expired.Store(true); cancel() })
			defer timer.Stop()
		}
		var end ending
		select {
		case end = <-finished:
		case <-time.After(5 * time.Second):
			cancel()
			_ = client.Close()
			_ = target.Close()
			check.Errorf("handler did not finish within safety bound")
			result.failures = check.failures
			return result
		}
		if !assert.Nil(check, end.panicValue) {
			result.errorCategory = "panic"
		}
		if !assert.NotNil(check, end.usage) || !assert.NotNil(check, info.StreamResult) {
			result.failures = check.failures
			return result
		}
		result.observedFault = info.StreamResult.Failed
		assert.Equal(check, failed, result.observedFault)
		assert.Zero(check, billing.settles)
		assert.Zero(check, billing.refunds)
		assert.Equal(check, end.usage.InputTokens+end.usage.OutputTokens, end.usage.TotalTokens)
		if !failed {
			input, output, cached := rounds*10, rounds*2, rounds*3
			if mode == "zero" {
				input, output, cached = 0, 0, 0
			}
			assert.Equal(check, input, end.usage.InputTokens)
			assert.Equal(check, output, end.usage.OutputTokens)
			assert.Equal(check, cached, end.usage.InputTokenDetails.CachedTokens)
			assert.Equal(check, rounds, billing.reserves, "duplicate done must not add reservations")
			assert.Equal(check, mode != "zero", info.StreamResult.EffectiveContent)
			assert.Equal(check, input, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
			assert.Equal(check, output, info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
		}
		d := info.StreamDiagnostic.Termination.Snapshot()
		if assert.NotNil(check, d) {
			assert.NotNil(check, d.UpstreamClose)
			if failed && assert.NotNil(check, d.FirstObserved) {
				assert.Equal(check, side, d.FirstObserved.Side)
				assert.Equal(check, operation, d.FirstObserved.Operation)
			} else if !failed {
				assert.Nil(check, d.FirstObserved)
			}
		}
		result.failures = check.failures
		return result
	}, server.Close)
}
