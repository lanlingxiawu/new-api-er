package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acquireForTest is acquire without a deadline, for tests that only exercise the counting.
func (l *priceMonitorSub2APIKeyLimiter) acquireForTest(host string, limit int) bool {
	acquired, err := l.acquire(context.Background(), host, limit)
	if err != nil {
		panic(err)
	}
	return acquired
}

// gatedSub2APIBilling is a billing endpoint that holds every request until release is closed
// and answers with the status/body returned by answer (call number starts at 1).
type gatedSub2APIBilling struct {
	calls   atomic.Int32
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
	answer  func(call int32) (int, string)
}

func newGatedSub2APIBilling(answer func(call int32) (int, string)) *gatedSub2APIBilling {
	return &gatedSub2APIBilling{arrived: make(chan struct{}, 64), release: make(chan struct{}), answer: answer}
}

func (g *gatedSub2APIBilling) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := g.calls.Add(1)
		g.arrived <- struct{}{}
		select {
		case <-g.release:
		case <-r.Context().Done():
			return
		}
		status, body := g.answer(call)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	// Registered after Close, so it runs first: a failed assertion never leaves handlers blocked.
	t.Cleanup(g.open)
	return server
}

// open lets every held and future request through.
func (g *gatedSub2APIBilling) open() { g.once.Do(func() { close(g.release) }) }

const (
	sub2apiBillingOKBody       = `{"object":"sub2api.key_billing","group_rate_multiplier":1,"resolved_rate_multiplier":1.5}`
	sub2apiBillingRejectedBody = `{"type":"error","error":{"type":"authentication_error"}}`
)

type sub2apiKeyResult struct {
	billing  sub2apiKeyBilling
	answered bool
	err      error
}

// startConcurrentKeyFetches starts n concurrent fetches of the same base URL and key in one run,
// waits until the first request reaches the server and the others are queued behind it.
func startConcurrentKeyFetches(t *testing.T, run *priceMonitorSub2APIRun, gate *gatedSub2APIBilling, baseURL string, n int) <-chan sub2apiKeyResult {
	t.Helper()
	results := make(chan sub2apiKeyResult, n)
	for i := 0; i < n; i++ {
		go func() {
			billing, answered, err := run.fetchKeyBilling(context.Background(), http.DefaultClient, "h", baseURL, "sk-same", priceMonitorSub2APIMaxRejectedPerHost)
			results <- sub2apiKeyResult{billing, answered, err}
		}()
	}
	select {
	case <-gate.arrived:
	case <-time.After(5 * time.Second):
		require.Fail(t, "no request reached the upstream")
	}
	// Give the other callers time to find the in-progress call (they must not send).
	time.Sleep(50 * time.Millisecond)
	return results
}

// L3: two channels with the same base URL and key (or the price and ratio steps at once) ask
// the upstream once per run; the others wait for that answer.
func TestSub2APIRunConcurrentSameKeySendsOnce(t *testing.T) {
	gate := newGatedSub2APIBilling(func(int32) (int, string) { return http.StatusOK, sub2apiBillingOKBody })
	url := gate.server(t).URL
	run := newPriceMonitorSub2APIRunWith(newPriceMonitorSub2APIKeyLimiter(time.Now))

	results := startConcurrentKeyFetches(t, run, gate, url, 5)
	require.EqualValues(t, 1, gate.calls.Load(), "concurrent callers wait instead of sending")
	gate.open()
	for i := 0; i < 5; i++ {
		result := <-results
		require.True(t, result.answered)
		require.NoError(t, result.err)
		assert.InDelta(t, 1.5, result.billing.ResolvedRateMultiplier, 1e-9)
	}
	_, answered, err := run.fetchKeyBilling(context.Background(), http.DefaultClient, "h", url, "sk-same", priceMonitorSub2APIMaxRejectedPerHost)
	require.True(t, answered)
	require.NoError(t, err)
	assert.EqualValues(t, 1, gate.calls.Load(), "a later caller in the same run reuses the answer")
}

// A rejected key asked concurrently is sent once and counted once against the host's budget.
func TestSub2APIRunConcurrentRejectedKeyCountsOnce(t *testing.T) {
	gate := newGatedSub2APIBilling(func(int32) (int, string) { return http.StatusUnauthorized, sub2apiBillingRejectedBody })
	url := gate.server(t).URL
	limiter := newPriceMonitorSub2APIKeyLimiter(time.Now)
	run := newPriceMonitorSub2APIRunWith(limiter)

	results := startConcurrentKeyFetches(t, run, gate, url, 3)
	gate.open()
	for i := 0; i < 3; i++ {
		result := <-results
		require.True(t, result.answered)
		assert.Equal(t, priceMonitorRatioReasonRejected, sub2apiBillingErrorReason(result.err))
	}
	assert.EqualValues(t, 1, gate.calls.Load())
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	require.Contains(t, limiter.hosts, "h")
	assert.Len(t, limiter.hosts["h"].rejected, 1, "the rejected key is counted once")
}

// A transient failure is not shared as the answer: a caller that waited on it asks again itself.
func TestSub2APIRunTransientFailureIsRetriedByWaiter(t *testing.T) {
	gate := newGatedSub2APIBilling(func(call int32) (int, string) {
		if call == 1 {
			return http.StatusBadGateway, `bad gateway`
		}
		return http.StatusOK, sub2apiBillingOKBody
	})
	url := gate.server(t).URL
	run := newPriceMonitorSub2APIRunWith(newPriceMonitorSub2APIKeyLimiter(time.Now))

	results := startConcurrentKeyFetches(t, run, gate, url, 2)
	gate.open()
	var failed, succeeded int
	for i := 0; i < 2; i++ {
		result := <-results
		require.True(t, result.answered)
		if result.err != nil {
			failed++
		} else {
			succeeded++
		}
	}
	assert.Equal(t, 1, failed)
	assert.Equal(t, 1, succeeded)
	assert.EqualValues(t, 2, gate.calls.Load())
}

// L4: a caller waiting for a slot gives up when its context expires; it neither sends nor keeps
// waiting (it holds one of the pricing fetch's concurrency slots meanwhile).
func TestSub2APIKeyLimiterAcquireHonoursContext(t *testing.T) {
	limiter := newPriceMonitorSub2APIKeyLimiter(time.Now)
	const limit = 2
	require.True(t, limiter.acquireForTest("h", limit))
	require.True(t, limiter.acquireForTest("h", limit))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	acquired, err := limiter.acquire(ctx, "h", limit)
	assert.False(t, acquired)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(started), 2*time.Second, "the wait ends with the context")

	expired, cancelExpired := context.WithCancel(context.Background())
	cancelExpired()
	acquired, err = limiter.acquire(expired, "free-host", limit)
	assert.False(t, acquired, "an expired context takes no slot even when one is free")
	assert.ErrorIs(t, err, context.Canceled)

	limiter.mu.Lock()
	assert.Equal(t, 2, limiter.hosts["h"].inflight, "a waiter that gave up holds no slot")
	assert.NotContains(t, limiter.hosts, "free-host")
	limiter.mu.Unlock()

	waiting := make(chan bool, 1)
	go func() { waiting <- limiter.acquireForTest("h", limit) }()
	limiter.release("h", "")
	require.True(t, <-waiting, "a release still wakes a waiter with a live context")
	limiter.release("h", "")
	limiter.release("h", "")
	limiter.mu.Lock()
	assert.Empty(t, limiter.hosts)
	limiter.mu.Unlock()
}

// End to end: with the host's slots all in flight, a fetch whose context expires while waiting
// sends nothing and reports the timeout (a transient failure, not a rejection).
func TestSub2APIRunFetchGivesUpWhileWaitingForSlot(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(sub2apiBillingOKBody))
	}))
	defer server.Close()
	limiter := newPriceMonitorSub2APIKeyLimiter(time.Now)
	for i := 0; i < priceMonitorSub2APIMaxRejectedPerHost; i++ {
		require.True(t, limiter.acquireForTest("h", priceMonitorSub2APIMaxRejectedPerHost))
	}
	run := newPriceMonitorSub2APIRunWith(limiter)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, answered, err := run.fetchKeyBilling(ctx, http.DefaultClient, "h", server.URL, "sk", priceMonitorSub2APIMaxRejectedPerHost)
	assert.True(t, answered)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, priceMonitorRatioReasonUnavailable, sub2apiBillingErrorReason(err))
	assert.Zero(t, calls.Load(), "no key is sent after the context expired")

	// The expired attempt left no in-progress entry behind: the next caller asks normally.
	for i := 0; i < priceMonitorSub2APIMaxRejectedPerHost; i++ {
		limiter.release("h", "")
	}
	_, answered, err = run.fetchKeyBilling(context.Background(), http.DefaultClient, "h", server.URL, "sk", priceMonitorSub2APIMaxRejectedPerHost)
	assert.True(t, answered)
	assert.NoError(t, err)
	assert.EqualValues(t, 1, calls.Load())
}

// A caller waiting for another caller's answer also stops at its own deadline.
func TestSub2APIRunWaiterHonoursContext(t *testing.T) {
	gate := newGatedSub2APIBilling(func(int32) (int, string) { return http.StatusOK, sub2apiBillingOKBody })
	url := gate.server(t).URL
	run := newPriceMonitorSub2APIRunWith(newPriceMonitorSub2APIKeyLimiter(time.Now))
	leader := make(chan error, 1)
	go func() {
		_, _, err := run.fetchKeyBilling(context.Background(), http.DefaultClient, "h", url, "sk", priceMonitorSub2APIMaxRejectedPerHost)
		leader <- err
	}()
	<-gate.arrived

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, answered, err := run.fetchKeyBilling(ctx, http.DefaultClient, "h", url, "sk", priceMonitorSub2APIMaxRejectedPerHost)
	assert.True(t, answered)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	gate.open()
	require.NoError(t, <-leader)
	assert.EqualValues(t, 1, gate.calls.Load())
}
