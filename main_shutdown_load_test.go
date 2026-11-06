package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Test design: real sockets exercise the production shutdown coordinator under load.
// JSON/SSE/mixed partition response types; cancellation, idle and batch-off cover
// alternate paths; 500ms/1s deadlines retain live handlers; slow batch proves that
// the listener closes before a blocked flush and logs remain open until it ends.
// Each worker first completes 16 requests, then holds one accepted request behind
// a barrier. These callbacks observe ordering, not real quota or database writes.
func TestShutdownWithBatchFlush_Load(t *testing.T) {
	if os.Getenv("FORK_SHUTDOWN_LOAD") != "1" {
		t.Skip("set FORK_SHUTDOWN_LOAD=1 to run bounded socket load scenarios")
	}
	workersOverride := 0
	if value := os.Getenv("FORK_SHUTDOWN_WORKERS"); value != "" {
		var err error
		workersOverride, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.Positive(t, workersOverride)
		require.LessOrEqual(t, workersOverride, 512)
	}
	for _, tc := range []struct {
		name       string
		workers    int
		mode       string
		cancelSome bool
		deadline   time.Duration
		slowBatch  bool
		batchOff   bool
	}{
		{name: "sse", workers: 64, mode: "sse"},
		{name: "json", workers: 64, mode: "json"},
		{name: "mixed", workers: 128, mode: "mixed"},
		{name: "client_cancel", workers: 64, mode: "mixed", cancelSome: true},
		{name: "deadline_500ms", workers: 64, mode: "sse", deadline: 500 * time.Millisecond},
		{name: "deadline_1s", workers: 128, mode: "mixed", deadline: time.Second},
		{name: "slow_batch", workers: 64, mode: "mixed", slowBatch: true},
		{name: "batch_off", workers: 64, mode: "mixed", batchOff: true},
		{name: "idle", mode: "json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if workersOverride > 0 && tc.workers > 0 {
				tc.workers = workersOverride
			}
			const warmups = 16
			var accepted, completed, canceled, active atomic.Int64
			release, startHold, batchRelease := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce, holdOnce, batchOnce sync.Once
			held, warm := make(chan struct{}, tc.workers), make(chan struct{}, tc.workers)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			ln := &shutdownLoadListener{Listener: listener, closed: make(chan struct{})}
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				accepted.Add(1)
				active.Add(1)
				defer active.Add(-1)
				stream := r.URL.Path == "/sse"
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: start\n\n")
					w.(http.Flusher).Flush()
				}
				if r.URL.Query().Get("hold") == "1" {
					held <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						canceled.Add(1)
						return
					}
				}
				if stream {
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				}
				completed.Add(1)
			})}
			serveDone := make(chan error, 1)
			go func() { serveDone <- srv.Serve(ln) }()
			transport := &http.Transport{Proxy: nil, MaxIdleConns: tc.workers, MaxIdleConnsPerHost: max(1, tc.workers)}
			client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
			var workers sync.WaitGroup
			workerDone := make(chan struct{})
			stopped := make(chan struct{})
			shutdownStarted, serveJoined := false, false
			workerErrors := make(chan error, max(1, tc.workers))
			cancels := make([]context.CancelFunc, tc.workers)
			t.Cleanup(func() {
				holdOnce.Do(func() { close(startHold) })
				releaseOnce.Do(func() { close(release) })
				batchOnce.Do(func() { close(batchRelease) })
				for _, cancel := range cancels {
					if cancel != nil {
						cancel()
					}
				}
				_ = srv.Close()
				transport.CloseIdleConnections()
				select {
				case <-workerDone:
				case <-time.After(20 * time.Second):
					require.Fail(t, "load workers did not stop")
				}
				if shutdownStarted {
					select {
					case <-stopped:
					case <-time.After(20 * time.Second):
						require.Fail(t, "shutdown coordinator did not stop")
					}
				}
				if !serveJoined {
					select {
					case <-serveDone:
					case <-time.After(5 * time.Second):
						require.Fail(t, "HTTP server did not stop")
					}
				}
			})
			baseURL := "http://" + listener.Addr().String()
			started := time.Now()
			for i := range tc.workers {
				ctx, cancel := context.WithCancel(context.Background())
				cancels[i] = cancel
				workers.Go(func() {
					path := "/" + tc.mode
					if tc.mode == "mixed" {
						path = "/json"
						if i%2 == 0 {
							path = "/sse"
						}
					}
					for range warmups {
						if err := shutdownLoadRequest(context.Background(), client, baseURL+path); err != nil {
							workerErrors <- err
							return
						}
					}
					warm <- struct{}{}
					<-startHold
					err := shutdownLoadRequest(ctx, client, baseURL+path+"?hold=1")
					if tc.cancelSome && i%4 == 0 {
						if !errors.Is(err, context.Canceled) {
							workerErrors <- fmt.Errorf("canceled request returned %v", err)
						}
					} else if err != nil {
						workerErrors <- err
					}
				})
			}
			go func() { workers.Wait(); close(workerDone) }()
			for range tc.workers {
				select {
				case <-warm:
				case err := <-workerErrors:
					require.NoError(t, err)
				case <-time.After(20 * time.Second):
					require.FailNow(t, "warmup did not finish")
				}
			}
			warmDuration := time.Since(started)
			holdOnce.Do(func() { close(startHold) })
			for range tc.workers {
				select {
				case <-held:
				case <-time.After(10 * time.Second):
					require.FailNow(t, "request did not reach hold barrier")
				}
			}
			require.EqualValues(t, tc.workers, active.Load())
			var phases []string
			var drainErr error
			var activeAtFlush int64
			batchEntered, logs := make(chan struct{}), make(chan struct{})
			var batch func()
			if !tc.batchOff {
				batch = func() {
					phases = append(phases, "batch")
					activeAtFlush = active.Load()
					close(batchEntered)
					if tc.slowBatch {
						<-batchRelease
					}
				}
			}
			shutdownStarted = true
			go func() {
				defer close(stopped)
				shutdownWithBatchFlush(func() error {
					phases = append(phases, "drain")
					budget := tc.deadline
					if budget == 0 {
						budget = 10 * time.Second
					}
					ctx, cancel := context.WithTimeout(context.Background(), budget)
					defer cancel()
					drainErr = srv.Shutdown(ctx)
					return drainErr
				}, func() {
					phases = append(phases, "logs")
					if tc.batchOff {
						activeAtFlush = active.Load()
					}
					close(logs)
				}, batch)
			}()
			select {
			case <-ln.closed:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "shutdown did not close listener")
			}
			probeTransport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
			defer probeTransport.CloseIdleConnections()
			probe := &http.Client{Transport: probeTransport, Timeout: time.Second}
			rejected := 0
			for range 20 {
				response, err := probe.Get(baseURL + "/json")
				if response != nil {
					_ = response.Body.Close()
				}
				if err != nil {
					rejected++
				}
			}
			require.Equal(t, 20, rejected)
			if tc.cancelSome {
				for i, cancel := range cancels {
					if i%4 == 0 {
						cancel()
					}
				}
				require.Eventually(t, func() bool { return canceled.Load() == int64((tc.workers+3)/4) }, 5*time.Second, 10*time.Millisecond)
			}
			if tc.deadline == 0 {
				releaseOnce.Do(func() { close(release) })
			}
			if tc.slowBatch {
				select {
				case <-batchEntered:
				case <-time.After(10 * time.Second):
					require.FailNow(t, "batch flush did not begin")
				}
				select {
				case <-logs:
					require.FailNow(t, "logs closed while batch flush was blocked")
				default:
				}
				batchOnce.Do(func() { close(batchRelease) })
			}
			select {
			case <-stopped:
			case <-time.After(15 * time.Second):
				require.FailNow(t, "shutdown coordinator did not finish")
			}
			if tc.deadline > 0 {
				require.ErrorIs(t, drainErr, context.DeadlineExceeded)
				// http.Server.Shutdown returning a deadline does not cancel handlers.
				require.EqualValues(t, tc.workers, activeAtFlush)
				require.EqualValues(t, tc.workers, active.Load())
				releaseOnce.Do(func() { close(release) })
			} else {
				require.NoError(t, drainErr)
				require.Zero(t, activeAtFlush)
			}
			select {
			case <-workerDone:
			case <-time.After(10 * time.Second):
				require.FailNow(t, "clients did not complete after fixture release")
			}
			close(workerErrors)
			for err := range workerErrors {
				require.NoError(t, err)
			}
			require.ErrorIs(t, <-serveDone, http.ErrServerClosed)
			serveJoined = true
			expected := []string{"drain", "batch", "logs"}
			if tc.batchOff {
				expected = []string{"drain", "logs"}
			}
			require.Equal(t, expected, phases)
			require.Zero(t, active.Load())
			require.EqualValues(t, tc.workers*(warmups+1), accepted.Load())
			require.Equal(t, accepted.Load(), completed.Load()+canceled.Load())
			rate := 0.0
			if tc.workers > 0 {
				rate = float64(tc.workers*warmups) / warmDuration.Seconds()
			}
			t.Logf("workers=%d warmup=%.0f req/s accepted=%d completed=%d canceled=%d rejected=%d activeAtFlush=%d", tc.workers, rate, accepted.Load(), completed.Load(), canceled.Load(), rejected, activeAtFlush)
		})
	}
}

type shutdownLoadListener struct {
	net.Listener
	closed chan struct{}
	once   sync.Once
}

func (l *shutdownLoadListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { close(l.closed) })
	return err
}

func shutdownLoadRequest(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK || !(strings.Contains(string(body), "[DONE]") || string(body) == `{"ok":true}`) {
		return fmt.Errorf("incomplete response: status=%d body=%q", resp.StatusCode, body)
	}
	return nil
}
