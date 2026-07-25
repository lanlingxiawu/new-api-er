package controller

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

var initClientOnce sync.Once

func ensureHTTPClient() {
	initClientOnce.Do(func() { service.InitHttpClient() })
}

func mjTestChannel(baseURL string) *model.Channel {
	u := baseURL
	return &model.Channel{BaseURL: &u, Key: "test-key"}
}

func TestFetchMidjourneyTasks_Success(t *testing.T) {
	ensureHTTPClient()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"id":"mj-1","status":"SUCCESS","progress":"100%"}]`)
	}))
	defer srv.Close()

	items, err := fetchMidjourneyTasks(context.Background(), mjTestChannel(srv.URL), []string{"mj-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].MjId != "mj-1" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestFetchMidjourneyTasks_Non200(t *testing.T) {
	ensureHTTPClient()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `boom`)
	}))
	defer srv.Close()

	items, err := fetchMidjourneyTasks(context.Background(), mjTestChannel(srv.URL), []string{"mj-1"})
	if err == nil {
		t.Fatal("expected error on non-200 status")
	}
	if items != nil {
		t.Fatalf("expected nil items on error, got %+v", items)
	}
}

func TestFetchMidjourneyTasks_BadJSON(t *testing.T) {
	ensureHTTPClient()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{not-json`)
	}))
	defer srv.Close()

	if _, err := fetchMidjourneyTasks(context.Background(), mjTestChannel(srv.URL), []string{"mj-1"}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

// TestFetchMidjourneyTasks_ContextTimeoutNoLeak drives the timeout/cancel path:
// the parent context deadline (150ms) is shorter than both the server delay and
// the function's internal 15s timeout, so the request is cancelled. The fix must
// still drain+close and cancel without leaking.
func TestFetchMidjourneyTasks_ContextTimeoutNoLeak(t *testing.T) {
	ensureHTTPClient()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	if _, err := fetchMidjourneyTasks(ctx, mjTestChannel(srv.URL), []string{"mj-1"}); err == nil {
		t.Fatal("expected context timeout error")
	}
}

// randomUpstream returns an httptest handler that randomly simulates the mix of
// upstream conditions seen in production: valid responses, delays, non-2xx,
// rate-limit, malformed bodies, oversized error bodies and abrupt connection
// drops. Used to stress the fetch path.
func randomUpstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch rand.Intn(7) {
		case 0: // valid, small
			_, _ = fmt.Fprint(w, `[{"id":"mj-1","status":"IN_PROGRESS","progress":"50%"}]`)
		case 1: // 500 with small body -> drained via cap path
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `internal error`)
		case 2: // 429 empty
			w.WriteHeader(http.StatusTooManyRequests)
		case 3: // malformed json on 200
			_, _ = fmt.Fprint(w, `{bad`)
		case 4: // small delay then valid
			time.Sleep(time.Duration(rand.Intn(40)) * time.Millisecond)
			_, _ = fmt.Fprint(w, `[{"id":"mj-2","status":"SUCCESS","progress":"100%"}]`)
		case 5: // oversized (>512KB) NON-200 body -> exercises the drain cap
			w.WriteHeader(http.StatusBadGateway)
			big := make([]byte, 700*1024)
			for i := range big {
				big[i] = 'a'
			}
			_, _ = w.Write(big)
		case 6: // abrupt connection drop mid-response
			hj, ok := w.(http.Hijacker)
			if !ok {
				_, _ = fmt.Fprint(w, `[]`)
				return
			}
			conn, _, err := hj.Hijack()
			if err == nil {
				_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\npartial"))
				_ = conn.Close()
			}
		}
	}
}

// TestFetchMidjourneyTasks_StressNoLeak hammers the fetch path with a randomized
// upstream (delays, errors, non-2xx, oversized/partial bodies) and asserts that
// goroutines return to baseline afterwards — i.e. no response-body/connection or
// context leak. It also logs heap and GC metrics (GC count is a CPU-pressure
// proxy) so a regression toward the production memory/CPU symptom is visible.
//
// Crank up load for a soak/"stress to crash" run with STRESS_ITER, e.g.
//
//	STRESS_ITER=200000 go test ./controller/ -run StressNoLeak -v -timeout 30m
func TestFetchMidjourneyTasks_StressNoLeak(t *testing.T) {
	// 该用例默认 3000 次 / 48 并发，含随机分支、随机 sleep、runtime.GC 与
	// goroutine 基线阈值判断，结果依赖机器负载与调度，放在常规测试路径中
	// 既不稳定也拖慢 CI（项目约定禁止随机压力/时序测试）。
	// 响应体关闭、超时与非 2xx 等确定性契约由本文件其余用例覆盖。
	if os.Getenv("STRESS_TEST") == "" {
		t.Skip("stress test disabled by default; set STRESS_TEST=1 to enable")
	}
	ensureHTTPClient()

	srv := httptest.NewServer(randomUpstream())
	defer srv.Close()
	ch := mjTestChannel(srv.URL)

	iterations := 3000
	workers := 48
	if testing.Short() {
		iterations = 300
		workers = 12
	}
	if v := os.Getenv("STRESS_ITER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			iterations = n
		}
	}
	if v := os.Getenv("STRESS_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}

	client := service.GetHttpClient()

	// Warm up and settle a baseline (server + pool goroutines reach steady state).
	for i := 0; i < workers && i < 64; i++ {
		_, _ = fetchMidjourneyTasks(context.Background(), ch, []string{"warm"})
	}
	client.CloseIdleConnections()
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)
	g0 := runtime.NumGoroutine()

	// Background sampler: track peak goroutines / heap during the load so a mid-run
	// leak or explosion is visible even if it settles by the end.
	stop := make(chan struct{})
	var peakGoroutines int64 = int64(g0)
	var peakHeapKiB int64
	var samplerDone sync.WaitGroup
	samplerDone.Add(1)
	go func() {
		defer samplerDone.Done()
		tick := time.NewTicker(1 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				g := int64(runtime.NumGoroutine())
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				heapKiB := int64(ms.HeapInuse / 1024)
				if g > atomic.LoadInt64(&peakGoroutines) {
					atomic.StoreInt64(&peakGoroutines, g)
				}
				if heapKiB > atomic.LoadInt64(&peakHeapKiB) {
					atomic.StoreInt64(&peakHeapKiB, heapKiB)
				}
				t.Logf("[sample] goroutines=%d heap=%dKiB gc=%d", g, heapKiB, ms.NumGC)
			}
		}
	}()

	start := time.Now()
	var errCount, connErrCount int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := fetchMidjourneyTasks(context.Background(), ch, []string{"mj-1"}); err != nil {
				atomic.AddInt64(&errCount, 1)
				// "do request failed" == transport/connect-level failure (e.g. Windows
				// ephemeral-port exhaustion under extreme concurrency), as opposed to the
				// non-2xx / bad-JSON / abrupt-drop errors the mock returns by design.
				if strings.Contains(err.Error(), "do request failed") {
					atomic.AddInt64(&connErrCount, 1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(stop)
	samplerDone.Wait()

	// Let idle connections / transient goroutines wind down before measuring.
	client.CloseIdleConnections()
	time.Sleep(300 * time.Millisecond)
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	g1 := runtime.NumGoroutine()

	t.Logf("iterations=%d workers=%d elapsed=%s (%.0f req/s)",
		iterations, workers, elapsed, float64(iterations)/elapsed.Seconds())
	t.Logf("goroutines: baseline=%d peak=%d after=%d delta=%d",
		g0, atomic.LoadInt64(&peakGoroutines), g1, g1-g0)
	t.Logf("heap-inuse: baseline=%dKiB peak=%dKiB after=%dKiB",
		m0.HeapInuse/1024, atomic.LoadInt64(&peakHeapKiB), m1.HeapInuse/1024)
	t.Logf("heap-objects: baseline=%d after=%d", m0.HeapObjects, m1.HeapObjects)
	t.Logf("GC cycles during load: %d (CPU-pressure proxy)", m1.NumGC-m0.NumGC)
	t.Logf("errors: total=%d (incl. by-design non-2xx/bad-json/drop) connect-level=%d (port/handle exhaustion signal)",
		atomic.LoadInt64(&errCount), atomic.LoadInt64(&connErrCount))

	// A body/connection or context leak scales with iterations (each unclosed
	// conn keeps a parked goroutine). After CloseIdleConnections + GC the count
	// must return near baseline; a generous slack absorbs pool/server internals.
	if delta := g1 - g0; delta > workers+20 {
		t.Fatalf("possible goroutine leak: baseline=%d after=%d delta=%d (iterations=%d)",
			g0, g1, delta, iterations)
	}
}
