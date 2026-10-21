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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Small kernel buffers make a stalled real TCP peer observable in this fixture.
// They do not replace the production write deadline or change relay buffering.
type slowClientListener struct{ net.Listener }

func (l slowClientListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if err = c.(*net.TCPConn).SetWriteBuffer(4096); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// Observe real transport timeouts without shortening or synthesizing deadlines.
type slowClientWriter struct {
	http.ResponseWriter
	deadlineCalls int
	writeTimeout  bool
}

func (w *slowClientWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *slowClientWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlineCalls++
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
}

func (w *slowClientWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		w.writeTimeout = true
	}
	return n, err
}

func (w *slowClientWriter) Flush() { _ = w.FlushError() }

func (w *slowClientWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		w.writeTimeout = true
	}
	return err
}

type slowClientResult struct {
	err                          string
	elapsed                      time.Duration
	converted, gone, failed      bool
	diagnostic, writeTimeout     bool
	deadlineCalls                int
	observed                     int64
	upstreamKept, downstreamKept int
}

type slowClientSample struct {
	Seconds        float64 `json:"seconds"`
	Heap           uint64  `json:"heap"`
	HeapInuse      uint64  `json:"heap_inuse"`
	StackInuse     uint64  `json:"stack_inuse"`
	Goroutines     int     `json:"goroutines"`
	RelayActive    int64   `json:"relay_active"`
	UpstreamActive int64   `json:"upstream_active"`
}

// Opt-in real-socket component tests; never connect to a real AI provider or DB.
func TestClaudeSlowClientLoad(t *testing.T) {
	if os.Getenv("STREAM_SLOW_CLIENT_LOAD") != "1" {
		t.Skip("opt-in real TCP slow-client component load")
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
	for _, tc := range []struct {
		name, mode          string
		clients, frameBytes int
	}{
		{"fast_64", "fast", 64, 16 << 10},
		{"slow_16", "slow", 16, 16 << 10},
		{"slow_64", "slow", 64, 16 << 10},
		{"slow_128", "slow", 128, 16 << 10},
		{"stopped_64", "stopped", 64, 16 << 10},
		{"disconnect_64", "disconnect", 64, 16 << 10},
		{"large_frame_slow_16", "slow", 16, 256 << 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const textBytes = 2 << 20
			frameCount := textBytes / tc.frameBytes
			start := strings.Replace(strictStart, `"input_tokens":100`, `"input_tokens":100,"cache_creation_input_tokens":1200`, 1)
			blockStart, _, _ := strings.Cut(strictText, "event: content_block_delta")
			delta := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"" + strings.Repeat("x", tc.frameBytes) + "\"}}\n\n"
			stop := strings.Replace(strictStop, "end_turn", "refusal", 1)
			fullBytes := int64(len(start) + len(blockStart) + frameCount*len(delta) + len(stop))
			var upstreamActive, relayActive atomic.Int64
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamActive.Add(1)
				defer upstreamActive.Add(-1)
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := io.WriteString(w, start+blockStart); err != nil {
					return
				}
				for i := 0; i < frameCount; i++ {
					if _, err := io.WriteString(w, delta); err != nil {
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
				}
				_, _ = io.WriteString(w, stop)
			}))
			origin.Listener = slowClientListener{origin.Listener}
			origin.Start()
			defer origin.Close()
			upstreamTransport := &http.Transport{MaxConnsPerHost: tc.clients, DisableKeepAlives: true}
			defer upstreamTransport.CloseIdleConnections()
			upstreamClient := &http.Client{Transport: upstreamTransport}
			results := make(chan slowClientResult, tc.clients)
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				relayActive.Add(1)
				defer relayActive.Add(-1)
				started := diagnosticClockNS()
				result := slowClientResult{converted: r.URL.Query().Get("converted") == "1"}
				defer func() { result.elapsed = time.Duration(diagnosticClockNS() - started); results <- result }()
				req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, origin.URL, nil)
				if err != nil {
					result.err = err.Error()
					return
				}
				resp, err := upstreamClient.Do(req)
				if err != nil {
					result.err = err.Error()
					return
				}
				defer resp.Body.Close()
				writer := &slowClientWriter{ResponseWriter: w}
				c, _ := gin.CreateTestContext(writer)
				c.Request = r
				info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude"}}
				if result.converted {
					info.RelayFormat = types.RelayFormatOpenAI
				}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				usage, apiErr := ClaudeStreamHandler(c, resp, info)
				usage = service.FinalizeStreamUsage(c, info, usage)
				if apiErr != nil {
					result.err = fmt.Sprintf("handler: %v", apiErr)
					return
				}
				if info.StreamResult == nil {
					result.err = "missing outcome"
					return
				}
				outcome := info.StreamResult
				result.failed, result.gone, result.diagnostic = outcome.Failed, outcome.ClientGone, outcome.DiagnosticAvailable
				result.deadlineCalls, result.writeTimeout = writer.deadlineCalls, writer.writeTimeout
				snapshot := info.StreamDiagnostic.Snapshot()
				result.observed = snapshot.ObservedBytes
				result.upstreamKept = len(snapshot.BodyHead) + len(snapshot.BodyTail)
				if body := info.StreamDiagnostic.DownstreamBody(); body != nil {
					result.downstreamKept = len(*body)
				}
				if tc.mode == "fast" || tc.mode == "slow" {
					if usage == nil || usage.PromptTokens != 100 || usage.CompletionTokens != 7 || usage.PromptTokensDetails.CachedCreationTokens != 1200 {
						result.err = fmt.Sprintf("unexpected final usage: %+v", usage)
					}
				}
			}))
			proxy.Listener = slowClientListener{proxy.Listener}
			proxy.Start()
			defer proxy.Close()
			defer origin.CloseClientConnections()
			defer proxy.CloseClientConnections()
			transport := &http.Transport{DisableKeepAlives: true, MaxConnsPerHost: tc.clients,
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
					if err != nil {
						return nil, err
					}
					if err = conn.(*net.TCPConn).SetReadBuffer(4096); err != nil {
						_ = conn.Close()
						return nil, err
					}
					return conn, nil
				}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 90 * time.Second}
			stopClients := make(chan struct{})
			var stopOnce sync.Once
			defer stopOnce.Do(func() { close(stopClients) })
			clientErrors := make(chan error, tc.clients)
			var clientWG sync.WaitGroup
			var bytesRead atomic.Int64
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			baselineGoroutines := runtime.NumGoroutine()
			beg := diagnosticClockNS()
			sampleStop, sampled := make(chan struct{}), make(chan struct{})
			var sampleStopOnce sync.Once
			var samples []slowClientSample
			go func() {
				defer close(sampled)
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-sampleStop:
						return
					case <-ticker.C:
						var m runtime.MemStats
						runtime.ReadMemStats(&m)
						samples = append(samples, slowClientSample{float64(diagnosticClockNS()-beg) / 1e9, m.HeapAlloc, m.HeapInuse, m.StackInuse, runtime.NumGoroutine(), relayActive.Load(), upstreamActive.Load()})
					}
				}
			}()
			defer func() { sampleStopOnce.Do(func() { close(sampleStop) }); <-sampled }()
			for i := 0; i < tc.clients; i++ {
				clientWG.Add(1)
				go func(i int) {
					defer clientWG.Done()
					resp, err := client.Get(fmt.Sprintf("%s?converted=%d", proxy.URL, i%2))
					if err != nil {
						clientErrors <- err
						return
					}
					defer resp.Body.Close()
					if resp.StatusCode != 200 {
						clientErrors <- fmt.Errorf("HTTP %d", resp.StatusCode)
						return
					}
					if tc.mode == "stopped" {
						<-stopClients
						return
					}
					buffer := make([]byte, 4096)
					var received int64
					for {
						n, err := resp.Body.Read(buffer)
						received += int64(n)
						bytesRead.Add(int64(n))
						if tc.mode == "disconnect" && received >= 64<<10 {
							time.Sleep(250 * time.Millisecond)
							return
						}
						if err != nil {
							if err != io.EOF {
								clientErrors <- err
							} else if received < textBytes {
								clientErrors <- fmt.Errorf("truncated response: %d", received)
							}
							return
						}
						if tc.mode == "slow" {
							time.Sleep(20 * time.Millisecond)
						}
					}
				}(i)
			}
			collected := make([]slowClientResult, 0, tc.clients)
			timer := time.NewTimer(85 * time.Second)
			defer timer.Stop()
			for len(collected) < tc.clients {
				select {
				case result := <-results:
					collected = append(collected, result)
				case <-timer.C:
					require.FailNow(t, "relay handlers did not finish within 85 seconds")
				}
			}
			stopOnce.Do(func() { close(stopClients) })
			clientWG.Wait()
			elapsed := time.Duration(diagnosticClockNS() - beg)
			close(clientErrors)
			for err := range clientErrors {
				require.NoError(t, err)
			}
			transport.CloseIdleConnections()
			upstreamTransport.CloseIdleConnections()
			proxy.Close()
			origin.Close()
			require.Eventually(t, func() bool {
				return relayActive.Load() == 0 && upstreamActive.Load() == 0 && runtime.NumGoroutine() <= baselineGoroutines+2
			}, 5*time.Second, 20*time.Millisecond, "relay, upstream and transport workers must drain")
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			peakHeap := before.HeapAlloc
			latencies := make([]float64, 0, len(collected))
			gone, timeouts := 0, 0
			for _, result := range collected {
				require.Empty(t, result.err)
				require.Positive(t, result.deadlineCalls)
				require.LessOrEqual(t, result.upstreamKept, 2048)
				require.LessOrEqual(t, result.downstreamKept, 2732)
				latencies = append(latencies, result.elapsed.Seconds())
				if result.gone {
					gone++
				}
				if result.writeTimeout {
					timeouts++
				}
				if tc.mode == "stopped" || tc.mode == "disconnect" {
					require.True(t, result.failed)
					require.True(t, result.gone)
					require.False(t, result.diagnostic, "client failure alone must not become upstream diagnostic")
					require.Less(t, result.observed, fullBytes, "backpressure must stop draining the upstream")
				} else {
					require.False(t, result.failed)
					require.False(t, result.gone)
					require.True(t, result.diagnostic)
					require.Equal(t, fullBytes, result.observed)
				}
				if tc.mode == "stopped" {
					require.True(t, result.writeTimeout, "real socket write must time out")
					require.GreaterOrEqual(t, result.elapsed, 29*time.Second)
					require.Less(t, result.elapsed, 40*time.Second)
				}
				if tc.mode == "disconnect" {
					require.Less(t, result.elapsed, 10*time.Second)
				}
			}
			sort.Float64s(latencies)
			// Join the sampler before inspecting its slice.
			sampleStopOnce.Do(func() { close(sampleStop) })
			<-sampled
			peakGoroutines := baselineGoroutines
			for _, sample := range samples {
				if sample.Heap > peakHeap {
					peakHeap = sample.Heap
				}
				if sample.Goroutines > peakGoroutines {
					peakGoroutines = sample.Goroutines
				}
			}
			report := map[string]any{"profile": tc.name, "clients": tc.clients, "frame_text_bytes": tc.frameBytes, "total_text_bytes_per_request": textBytes,
				"elapsed_s": elapsed.Seconds(), "relay_p50_s": latencies[len(latencies)/2], "relay_max_s": latencies[len(latencies)-1], "client_gone": gone, "write_timeouts": timeouts,
				"heap_before": before.HeapAlloc, "heap_peak": peakHeap, "heap_after_gc": after.HeapAlloc, "allocated_bytes": after.TotalAlloc - before.TotalAlloc,
				"heap_inuse_after_gc": after.HeapInuse, "heap_sys_after_gc": after.HeapSys, "heap_released_after_gc": after.HeapReleased,
				"goroutines_before": baselineGoroutines, "goroutines_peak": peakGoroutines, "goroutines_after": runtime.NumGoroutine(), "client_bytes_read": bytesRead.Load(), "samples": samples}
			encoded, err := common.Marshal(report)
			require.NoError(t, err)
			t.Logf("SLOW_CLIENT_LOAD %s", encoded)
		})
	}
}
