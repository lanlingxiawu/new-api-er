package claude

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type diagnosticLoadFixture struct {
	name, body            string
	refused               bool
	prompt, output, quota int
}

// runDiagnosticLoadRequest exercises real HTTP, both Claude stream paths,
// usage settlement calculation and serialized diagnostics. It never writes DB.
func runDiagnosticLoadRequest(client *http.Client, url string, fixture diagnosticLoadFixture, converted bool) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"stream":true}`))
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	c, rec, _, info := strictTestContext(nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	info.SetEstimatePromptTokens(0)
	if converted {
		info.RelayFormat = types.RelayFormatOpenAI
	}
	service.BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	info.StreamSession.ObserveHTTP(resp)
	usage, apiErr := ClaudeStreamHandler(c, resp, info)
	if apiErr != nil {
		return 0, fmt.Errorf("handler: %v", apiErr)
	}
	usage = service.FinalizeStreamUsage(c, info, usage)
	if info.StreamResult == nil || info.StreamResult.Failed || info.StreamResult.DiagnosticAvailable != fixture.refused {
		return 0, fmt.Errorf("unexpected stream outcome: %+v", info.StreamResult)
	}
	if usage == nil || usage.PromptTokens != fixture.prompt || usage.CompletionTokens != fixture.output || usage.PromptTokensDetails.CachedCreationTokens != 1200 {
		return 0, fmt.Errorf("unexpected usage: %+v", usage)
	}
	const expr = "p * 10 + c * 50 + cc * 12.5"
	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 3.5, QuotaPerUnit: 500000}
	ok, quota, _ := service.TryTieredSettle(info, service.BuildTieredTokenParams(usage, true, map[string]bool{"cc": true}))
	if !ok || quota != fixture.quota {
		return 0, fmt.Errorf("quota=%d want=%d", quota, fixture.quota)
	}
	otherLog := model.NewLogOther()
	service.AppendStreamLogInfo(info, otherLog)
	other := otherLog.Snapshot()
	diagnostic := other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
	if len(diagnostic.BodyHead)+len(diagnostic.BodyTail) > 2048 {
		return 0, fmt.Errorf("unbounded upstream body")
	}
	if fixture.refused {
		if other["stream_diagnostic_available"] != true || diagnostic.DownstreamBodyBase64 == nil || diagnostic.RejectReason != "claude_stop_reason=refusal" {
			return 0, fmt.Errorf("missing refusal diagnostic")
		}
		if len(*diagnostic.DownstreamBodyBase64) > 2732 || diagnostic.ObservedBytes != int64(len(fixture.body)) {
			return 0, fmt.Errorf("capture bounds or observed length mismatch")
		}
		if !strings.Contains(string(diagnostic.BodyTail)+string(diagnostic.BodyHead), "reasoning_extraction") {
			return 0, fmt.Errorf("missing refusal category in retained response")
		}
	} else if len(diagnostic.BodyHead) != 0 || diagnostic.DownstreamBodyBase64 != nil {
		return 0, fmt.Errorf("normal response should not persist raw content")
	}
	if strings.Contains(rec.Body.String(), "event: error") {
		return 0, fmt.Errorf("unexpected downstream error event")
	}
	data, err := common.Marshal(other)
	return len(data), err
}

// Opt-in component load: 64 bounded workers, scheduled arrivals, loopback-only
// HTTP, no auth/database/log queue. Scheduled latency includes generator lag.
func TestClaudeDiagnosticLoad(t *testing.T) {
	if os.Getenv("STREAM_DIAGNOSTIC_LOAD") != "1" {
		t.Skip("opt-in loopback diagnostic component load")
	}
	seconds := 30
	if raw := os.Getenv("STREAM_DIAGNOSTIC_LOAD_SECONDS"); raw != "" {
		value, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.GreaterOrEqual(t, value, 1)
		require.LessOrEqual(t, value, 120)
		seconds = value
	}
	oldMode := gin.Mode()
	gin.SetMode(gin.ReleaseMode)
	common.LogWriterMu.Lock()
	oldWriter, oldErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		gin.SetMode(oldMode)
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = oldWriter, oldErrorWriter
		common.LogWriterMu.Unlock()
	})
	start := strings.Replace(strictStart, `"input_tokens":100`, `"input_tokens":0,"cache_creation_input_tokens":1200,"cache_creation":{"ephemeral_5m_input_tokens":1200,"ephemeral_1h_input_tokens":0}`, 1)
	stop := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\",\"stop_details\":{\"category\":\"reasoning_extraction\"}},\"usage\":{\"output_tokens\":0}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	largeStart := strings.Replace(start, `"model":"claude"`, `"model":"claude","padding":"`+strings.Repeat("x", 64<<10)+`"`, 1)
	fixtures := []diagnosticLoadFixture{
		{"normal", strings.Replace(start, `"input_tokens":0`, `"input_tokens":100`, 1) + strictText + strictStop, false, 100, 7, 28613},
		{"refusal", start + stop, true, 0, 0, 26250},
		{"large_refusal", largeStart + stop, true, 0, 0, 26250},
	}
	for _, fixture := range fixtures {
		for _, rps := range []int{500, 1667} {
			t.Run(fmt.Sprintf("%s/%d_rps", fixture.name, rps), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, fixture.body)
				}))
				defer server.Close()
				transport := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 64}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
				for i := 0; i < 20; i++ {
					_, err := runDiagnosticLoadRequest(client, server.URL, fixture, i%2 != 0)
					require.NoError(t, err, "warmup")
				}
				const workers = 64
				count := seconds * rps
				serviceLatency, scheduledLatency := make([]time.Duration, count), make([]time.Duration, count)
				jobs := make(chan int, workers)
				var errors, bytes, active, peakActive atomic.Int64
				var firstError string
				var firstErrorOnce sync.Once
				var wg sync.WaitGroup
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				baselineGoroutines := runtime.NumGoroutine()
				var peakHeap atomic.Uint64
				var peakGoroutines atomic.Int64
				peakHeap.Store(before.HeapAlloc)
				sampleDone, sampled := make(chan struct{}), make(chan struct{})
				go func() {
					defer close(sampled)
					ticker := time.NewTicker(100 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-sampleDone:
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
				beg := diagnosticClockNS()
				for w := 0; w < workers; w++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for i := range jobs {
							started := diagnosticClockNS()
							n := active.Add(1)
							for old := peakActive.Load(); n > old && !peakActive.CompareAndSwap(old, n); old = peakActive.Load() {
							}
							size, err := runDiagnosticLoadRequest(client, server.URL, fixture, i%2 != 0)
							active.Add(-1)
							ended := diagnosticClockNS()
							serviceLatency[i] = time.Duration(ended - started)
							scheduledLatency[i] = time.Duration(ended-beg) - time.Duration(i)*time.Second/time.Duration(rps)
							bytes.Add(int64(size))
							if err != nil {
								errors.Add(1)
								firstErrorOnce.Do(func() { firstError = err.Error() })
							}
						}
					}()
				}
				for i := 0; i < count; i++ {
					if wait := time.Duration(beg-diagnosticClockNS()) + time.Duration(i)*time.Second/time.Duration(rps); wait > 0 {
						time.Sleep(wait)
					}
					jobs <- i
				}
				close(jobs)
				wg.Wait()
				elapsed := time.Duration(diagnosticClockNS() - beg)
				close(sampleDone)
				<-sampled
				runtime.ReadMemStats(&after)
				transport.CloseIdleConnections()
				server.Close()
				require.Eventually(t, func() bool { return runtime.NumGoroutine() <= baselineGoroutines+2 }, 3*time.Second, 10*time.Millisecond, "workers and connections must drain")
				runtime.GC()
				var retained runtime.MemStats
				runtime.ReadMemStats(&retained)
				sort.Slice(serviceLatency, func(i, j int) bool { return serviceLatency[i] < serviceLatency[j] })
				sort.Slice(scheduledLatency, func(i, j int) bool { return scheduledLatency[i] < scheduledLatency[j] })
				report := map[string]any{
					"profile": fixture.name, "requests": count, "target_rps": rps, "elapsed_s": elapsed.Seconds(), "rpm": float64(count) * 60 / elapsed.Seconds(), "errors": errors.Load(),
					"service_p50_ms": float64(serviceLatency[count/2]) / 1e6, "service_p95_ms": float64(serviceLatency[count*95/100]) / 1e6, "service_p99_ms": float64(serviceLatency[count*99/100]) / 1e6,
					"scheduled_p99_ms": float64(scheduledLatency[count*99/100]) / 1e6, "scheduled_max_ms": float64(scheduledLatency[count-1]) / 1e6,
					"peak_inflight": peakActive.Load(), "heap_before": before.HeapAlloc, "heap_peak": peakHeap.Load(), "heap_after_gc": retained.HeapAlloc,
					"alloc_bytes_per_request": (after.TotalAlloc - before.TotalAlloc) / uint64(count), "gc_cycles": after.NumGC - before.NumGC,
					"goroutines_before": baselineGoroutines, "goroutines_peak": peakGoroutines.Load(), "goroutines_after": runtime.NumGoroutine(), "serialized_bytes_per_request": bytes.Load() / int64(count),
				}
				encoded, err := common.Marshal(report)
				require.NoError(t, err)
				t.Logf("DIAGNOSTIC_LOAD %s", encoded)
				require.Zero(t, errors.Load(), firstError)
				require.GreaterOrEqual(t, float64(count)/elapsed.Seconds(), float64(rps)*0.95, "must sustain the scheduled arrival rate")
			})
		}
	}
}
