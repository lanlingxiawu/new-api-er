package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
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
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type forkAWSLoadCase struct {
	name                                      string
	wire                                      []byte
	failed, effective, confirmed, cache, tool bool
}

func forkAWSLoadCases(t *testing.T) []forkAWSLoadCase {
	t.Helper()
	start := forkAWSStart(`{"input_tokens":21,"output_tokens":1,"cache_read_input_tokens":20,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":3}}`)
	text := append([]string{start}, forkAWSText(0)...)
	complete := append(append([]string(nil), text...), forkAWSClose(0))
	complete = append(complete, forkAWSFinish("end_turn")...)
	tools := append([]string{start}, forkAWSTool(0)...)
	tools = append(tools, forkAWSFinish("tool_use")...)
	crc := forkAWSEncode(t, []string{start, `{"type":"ping"}`})
	crc[len(crc)-1] ^= 1
	exception := bytes.NewBuffer(forkAWSEncode(t, text))
	require.NoError(t, eventstream.NewEncoder().Encode(exception, eventstream.Message{Headers: eventstream.Headers{
		{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue(eventstreamapi.ExceptionMessageType)},
		{Name: eventstreamapi.ExceptionTypeHeader, Value: eventstream.StringValue("modelStreamErrorException")},
		{Name: eventstreamapi.ContentTypeHeader, Value: eventstream.StringValue("application/json")},
	}, Payload: []byte(`{"message":"Local Bedrock stream failure"}`)}))
	return []forkAWSLoadCase{
		{name: "text_cache", wire: forkAWSEncode(t, complete), effective: true, confirmed: true, cache: true},
		{name: "tool_cache", wire: forkAWSEncode(t, tools), effective: true, confirmed: true, cache: true, tool: true},
		{name: "eof_after_start", wire: forkAWSEncode(t, []string{start}), failed: true, confirmed: true, cache: true},
		{name: "eof_after_text", wire: forkAWSEncode(t, text), failed: true, effective: true, confirmed: true, cache: true},
		{name: "missing_usage", wire: forkAWSEncode(t, []string{forkAWSStart("")}), failed: true},
		{name: "explicit_zero", wire: forkAWSEncode(t, []string{forkAWSStart(`{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0}`)}), failed: true, confirmed: true},
		{name: "sdk_crc_after_usage", wire: crc, failed: true, confirmed: true, cache: true},
		{name: "sdk_exception", wire: exception.Bytes(), failed: true, effective: true, confirmed: true, cache: true},
		{name: "invalid_usage_after_valid", wire: forkAWSEncode(t, []string{start, `{"type":"message_delta","usage":{"output_tokens":-1}}`}), failed: true, confirmed: true, cache: true},
		{name: "truncated_sdk_prelude", wire: forkAWSEncode(t, []string{start})[:7], failed: true},
	}
}

type forkAWSLoadResult struct {
	index        int
	format       types.RelayFormat
	info         *relaycommon.RelayInfo
	usage        *dto.Usage
	body         string
	elapsed      time.Duration
	err          error
	conn         *forkAWSTraceConn
	reused       bool
	requestTrace *forkAWSRequestWriteTrace
}

type forkAWSRequestWriteTrace struct {
	mu       sync.Mutex
	err      error
	finished bool
}

func (r *forkAWSRequestWriteTrace) snapshot() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finished, r.err
}

// Capture the first socket close only for the diagnostic workload; a failed
// sample can then distinguish a local Transport close from decoded SDK errors.
type forkAWSTraceConn struct {
	net.Conn
	mu         sync.Mutex
	closeStack []byte
}

func (c *forkAWSTraceConn) Close() error {
	c.mu.Lock()
	if c.closeStack == nil {
		stack := make([]byte, 8192)
		c.closeStack = stack[:runtime.Stack(stack, false)]
	}
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *forkAWSTraceConn) closeTrace() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.closeStack)
}

func forkAWSLoadRequest(client *http.Client, base *url.URL, index int, format types.RelayFormat) (result forkAWSLoadResult) {
	started := time.Now()
	result.index, result.format = index, format
	result.requestTrace = &forkAWSRequestWriteTrace{}
	defer func() {
		result.elapsed = time.Since(started)
		if p := recover(); p != nil {
			result.err = fmt.Errorf("handler panic: %v", p)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	c := newAwsTestContext(rec, ctx)
	info := newAwsTestRelayInfo()
	info.DisablePing = true
	info.RelayFormat, info.OriginModelName, info.UpstreamModelName = format, forkAWSModel, forkAWSModel
	info.SetEstimatePromptTokens(50)
	service.BeginStreamAttempt(c, info)
	sdk := newAwsTestClient(awsRequestBodyHTTPClient{Client: relaycommon.StreamDiagnosticHTTPClient{Capture: info.StreamDiagnostic, Stream: info.StreamSession, Client: awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		// The SDK still signs/encodes the request, but this injected executor can
		// only reach the loopback httptest listener; no AWS credential discovery.
		local := request.Clone(request.Context())
		local = local.WithContext(httptrace.WithClientTrace(local.Context(), &httptrace.ClientTrace{
			GotConn: func(conn httptrace.GotConnInfo) {
				result.conn, _ = conn.Conn.(*forkAWSTraceConn)
				result.reused = conn.Reused
			},
			WroteRequest: func(info httptrace.WroteRequestInfo) {
				result.requestTrace.mu.Lock()
				result.requestTrace.err, result.requestTrace.finished = info.Err, true
				result.requestTrace.mu.Unlock()
			},
		}))
		target := *base
		target.Path = fmt.Sprintf("/%d", index)
		local.URL = &target
		local.Host = base.Host
		return client.Do(local)
	})}})
	input := newAwsStreamInput()
	input.ModelId = new(string)
	*input.ModelId = forkAWSModel
	input.Body = []byte(`{"anthropic_version":"bedrock-2023-05-31","max_tokens":128,"messages":[{"role":"user","content":"local fixture"}]}`)
	_, usage := awsStreamHandler(c, info, &Adaptor{AwsClient: sdk, AwsReq: input})
	selected := service.FinalizeStreamUsage(c, info, usage)
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

func forkAWSLoadValidate(r forkAWSLoadResult, tc forkAWSLoadCase) error {
	if r.err != nil {
		return r.err
	}
	out := r.info.StreamResult
	if out.Failed != tc.failed || out.EffectiveContent != tc.effective {
		return fmt.Errorf("failed/effective=%t/%t want=%t/%t reason=%s", out.Failed, out.EffectiveContent, tc.failed, tc.effective, r.info.StreamSession.Snapshot().Reason)
	}
	var text, args strings.Builder
	errors, terminal := 0, 0
	for _, line := range strings.Split(r.body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			terminal++
			continue
		}
		v := gjson.Parse(data)
		if v.Get("error").IsObject() {
			errors++
		}
		if r.format == types.RelayFormatClaude {
			if v.Get("type").String() == "message_stop" {
				terminal++
			}
			if v.Get("delta.type").String() == "text_delta" {
				text.WriteString(v.Get("delta.text").String())
			}
			if v.Get("delta.type").String() == "input_json_delta" {
				args.WriteString(v.Get("delta.partial_json").String())
			}
		} else {
			for _, choice := range v.Get("choices").Array() {
				text.WriteString(choice.Get("delta.content").String())
				for _, call := range choice.Get("delta.tool_calls").Array() {
					args.WriteString(call.Get("function.arguments").String())
				}
			}
		}
	}
	if tc.failed {
		if terminal != 0 || errors != 1 {
			return fmt.Errorf("failed stream errors=%d success_terminal=%d", errors, terminal)
		}
	} else if terminal != 1 || errors != 0 {
		return fmt.Errorf("normal stream errors=%d terminal=%d", errors, terminal)
	}
	wantText := ""
	if tc.effective && !tc.tool {
		wantText = "Hello, 世界 🌍"
	}
	if text.String() != wantText {
		return fmt.Errorf("incremental text %q want %q", text.String(), wantText)
	}
	if tc.tool && (!gjson.Valid(args.String()) || gjson.Get(args.String(), "location").String() != "東京") {
		return fmt.Errorf("tool JSON lost/duplicated: %q", args.String())
	}
	if out.SettlementAttempted {
		return fmt.Errorf("workload entered wallet settlement")
	}
	u := r.usage
	if canonical, ok := u.BillingUsage.CanonicalUsage(); ok {
		u = canonical
	}
	chargeConfirmed := tc.confirmed && (!tc.failed || tc.effective)
	wantPrompt, wantCache, wantFive, wantHour := 0, 0, 0, 0
	if chargeConfirmed && tc.cache {
		wantPrompt, wantCache, wantFive, wantHour = 21, 20, 7, 3
	}
	if u.PromptTokens != wantPrompt || u.PromptTokensDetails.CachedTokens != wantCache || u.ClaudeCacheCreation5mTokens != wantFive || u.ClaudeCacheCreation1hTokens != wantHour {
		return fmt.Errorf("usage input/cache/5m/1h=%d/%d/%d/%d want=%d/%d/%d/%d source=%s", u.PromptTokens, u.PromptTokensDetails.CachedTokens, u.ClaudeCacheCreation5mTokens, u.ClaudeCacheCreation1hTokens, wantPrompt, wantCache, wantFive, wantHour, out.UsageSource)
	}
	if !tc.failed && u.CompletionTokens != 12 {
		return fmt.Errorf("completed output=%d want12", u.CompletionTokens)
	}
	if u.CompletionTokens < 0 {
		return fmt.Errorf("negative output")
	}
	if tc.failed && !tc.effective && (out.UsageSource != "none" || u.TotalTokens != 0) {
		return fmt.Errorf("undelivered failure must release usage: source=%s total=%d", out.UsageSource, u.TotalTokens)
	}
	return nil
}

func TestForkAWSVendorConcurrentLoad(t *testing.T) {
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
	mode := gin.Mode()
	gin.SetMode(gin.TestMode)
	common.LogWriterMu.Lock()
	oldWriter, oldErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		gin.SetMode(mode)
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = oldWriter, oldErrorWriter
		common.LogWriterMu.Unlock()
	})
	cases := forkAWSLoadCases(t)
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	baselineGoroutines := runtime.NumGoroutine()
	var acceptedRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil || i < 0 || i >= len(cases) {
			http.Error(w, "unknown local fixture", 404)
			return
		}
		// Bedrock cannot generate a successful stream before accepting the
		// complete invocation JSON. An immediate mock 200 races Smithy's request
		// body Close against net/http's still-running request writeLoop.
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if readErr != nil || !gjson.ValidBytes(body) || gjson.GetBytes(body, "anthropic_version").String() != "bedrock-2023-05-31" || gjson.GetBytes(body, "max_tokens").Int() != 128 || !gjson.GetBytes(body, "messages").IsArray() {
			http.Error(w, "incomplete local invocation fixture", http.StatusBadRequest)
			return
		}
		acceptedRequests.Add(1)
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.Header().Set("X-Amzn-Bedrock-Content-Type", "application/json")
		for wire := cases[i].wire; len(wire) > 0; {
			n := min(17, len(wire))
			if _, err := w.Write(wire[:n]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			wire = wire[n:]
		}
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	transport := &http.Transport{MaxIdleConns: workers, MaxIdleConnsPerHost: workers, MaxConnsPerHost: workers}
	if os.Getenv("FORK_VENDOR_TRACE_CLOSE") == "1" {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &forkAWSTraceConn{Conn: conn}, nil
		}
	}
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
		t.Logf("vendor=Bedrock drained=true gc=true heap_baseline=%d heap_after_drain=%d goroutines_baseline=%d goroutines_after_drain=%d", baseline.HeapAlloc, drained.HeapAlloc, baselineGoroutines, runtime.NumGoroutine())
	}()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	formats := []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI}
	t.Run("loopback", func(t *testing.T) {
		acceptedBefore := acceptedRequests.Load()
		type job struct {
			index  int
			format types.RelayFormat
		}
		jobs := make(chan job, workers)
		results := make(chan forkAWSLoadResult, workers)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		goroutines := runtime.NumGoroutine()
		started := time.Now()
		var group sync.WaitGroup
		for n := 0; n < workers; n++ {
			group.Add(1)
			go func() {
				defer group.Done()
				for j := range jobs {
					results <- forkAWSLoadRequest(client, base, j.index, j.format)
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
			if err := forkAWSLoadValidate(r, cases[r.index]); err != nil {
				bad++
				if bad <= 8 {
					t.Logf("FAIL %s/%s: %v", cases[r.index].name, r.format, err)
					if r.info != nil {
						snapshot := r.info.StreamSession.Snapshot()
						capture := r.info.StreamDiagnostic.Snapshot()
						t.Logf("FAIL_DETAIL received_events=%d reason=%s error=%v read_error=%q read_eof=%t captured_bytes=%d body=%q", r.info.ReceivedResponseCount, snapshot.Reason, snapshot.Err, capture.ReadError, capture.ReadEOF, len(capture.BodyHead)+len(capture.BodyTail), r.body)
						t.Logf("FAIL_CONNECTION reused=%t close_stack=%s", r.reused, r.conn.closeTrace())
						written, writeErr := r.requestTrace.snapshot()
						t.Logf("FAIL_REQUEST written=%t write_error=%v", written, writeErr)
					}
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
		t.Logf("vendor=Bedrock workers=%d repetitions=%d requests=%d successful=%d expected_failure=%d incorrect=%d elapsed=%s requests_per_second=%.2f p50=%s p95=%s p99=%s allocated_bytes=%d heap_before=%d heap_live=%d goroutines_before=%d goroutines_live=%d", workers, repeats, len(latencies), success, failed, bad, elapsed, float64(len(latencies))/elapsed.Seconds(), latencies[len(latencies)/2], latencies[(len(latencies)-1)*95/100], latencies[(len(latencies)-1)*99/100], after.TotalAlloc-before.TotalAlloc, before.HeapAlloc, after.HeapAlloc, goroutines, runtime.NumGoroutine())
		require.Len(t, latencies, len(cases)*len(formats)*repeats)
		require.Equal(t, int64(len(latencies)), acceptedRequests.Load()-acceptedBefore, "every successful HTTP response follows a complete valid invocation")
		require.Zero(t, bad, "all local SDK responses must satisfy protocol and usage invariants")
	})
}
