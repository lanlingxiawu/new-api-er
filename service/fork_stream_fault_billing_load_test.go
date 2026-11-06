package service

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type forkBillingLoadCase struct {
	name, expr string
	usage      dto.Usage
	// Hand-calculated quota before the group multiplier, not computed by the
	// expression engine under test. Fractional quotas are rounded after group.
	baseQuota   float64
	undelivered bool
}

func runForkBillingLoadCase(tc forkBillingLoadCase, ratio float64, failure int) error {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/local-billing-fixture", nil)
	funding := &claudeSettlementFixture{}
	if failure != 0 {
		funding.err = errors.New("synthetic funding failure")
		funding.committed = failure == 2
	}
	info := &relaycommon.RelayInfo{Billing: funding, UserQuota: 1_000_000_000,
		StreamResult:          &relaycommon.StreamOutcome{Failed: tc.undelivered, ConfirmedUsage: true, EffectiveContent: !tc.undelivered},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: tc.expr, ExprHash: billingexpr.ExprHashString(tc.expr), GroupRatio: ratio, QuotaPerUnit: 500000}}
	info.StreamResult.UsageSource = info.StreamResult.SelectUsageSource()
	params := BuildTieredTokenParams(&tc.usage, tc.usage.UsageSemantic == "anthropic", map[string]bool{"cr": true, "cc": true, "cc1h": true, "cc12h": true})
	ok, quota, _ := TryTieredSettle(info, params)
	if info.StreamResult.UsageSource == "none" {
		quota = 0
	}
	want := int(math.Round(tc.baseQuota * ratio))
	if !ok || quota != want {
		return fmt.Errorf("%s ratio=%g quota=%d want=%d", tc.name, ratio, quota, want)
	}
	other := model.NewLogOther()
	settle := ConsumptionSettlementParams{Quota: quota, CountUsage: true, LedgerQuota: quota, Other: other}
	if !settleStreamQuota(c, info, &settle) || settleStreamQuota(c, info, &settle) {
		return fmt.Errorf("%s duplicate settlement guard", tc.name)
	}
	state := "settled"
	if quota == 0 {
		state = "released"
	}
	logged := quota
	if failure == 1 {
		state = "failed"
		logged = 0
	}
	if failure == 2 {
		state = "partial"
	}
	if funding.calls != 1 || funding.actual != want || settle.Quota != logged || info.StreamResult.SettlementState != state || info.StreamResult.IntendedQuota != want {
		return fmt.Errorf("%s failure=%d settlement mismatch calls=%d actual=%d logged=%d state=%s", tc.name, failure, funding.calls, funding.actual, settle.Quota, info.StreamResult.SettlementState)
	}
	AppendStreamLogInfo(info, other)
	row := model.Log{Other: other.JSONString()}
	if err := row.AfterFind(nil); err != nil {
		return err
	}
	return nil
}

// Opt-in CPU/settlement-boundary load. Network handlers have separate real
// loopback tests; this test never starts the DB, log queue or quota counters.
func TestForkStreamFaultBillingLoad(t *testing.T) {
	if os.Getenv("STREAM_FAULT_BILLING_LOAD") != "1" {
		t.Skip("opt-in isolated pricing and terminal settlement load")
	}
	workers, count := 32, 3000
	for _, item := range []struct {
		key    string
		target *int
		limit  int
	}{{"STREAM_FAULT_BILLING_WORKERS", &workers, 256}, {"STREAM_FAULT_BILLING_REQUESTS", &count, 500000}} {
		if raw := os.Getenv(item.key); raw != "" {
			n, err := strconv.Atoi(raw)
			require.NoError(t, err)
			require.Greater(t, n, 0)
			require.LessOrEqual(t, n, item.limit)
			*item.target = n
		}
	}
	oldMode := gin.Mode()
	gin.SetMode(gin.ReleaseMode)
	common.LogWriterMu.Lock()
	oldWriter, oldErrors := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		gin.SetMode(oldMode)
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = oldWriter, oldErrors
		common.LogWriterMu.Unlock()
	})
	const expr = `len <= 272000 ? tier("short", p*2+c*10+cr*0.2+cc*2.5+cc1h*4+cc12h*5) : tier("long", p*8+c*30+cr*0.8+cc*10+cc1h*16+cc12h*20)`
	cases := []forkBillingLoadCase{
		{"openai_cache", expr, dto.Usage{PromptTokens: 300, CompletionTokens: 40, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200}}, 320, false},
		{"claude_cache", expr, dto.Usage{UsageSemantic: "anthropic", PromptTokens: 100, CompletionTokens: 7, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200, CachedCreationTokens: 50}, ClaudeCacheCreation5mTokens: 50}, 217.5, false},
		{"claude_cache_only", expr, dto.Usage{UsageSemantic: "anthropic", PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200, CachedCreationTokens: 50}, ClaudeCacheCreation5mTokens: 50}, 82.5, false},
		{"explicit_zero_output", expr, dto.Usage{PromptTokens: 300, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200}}, 120, false},
		{"all_zero", expr, dto.Usage{}, 0, false},
		{"interrupted_estimate", expr, dto.Usage{PromptTokens: 375867}, 1503468, false},
		{"tier_at_boundary", expr, dto.Usage{PromptTokens: 272000}, 272000, false},
		{"tier_above_boundary", expr, dto.Usage{PromptTokens: 272001}, 1088004, false},
		{"undelivered_token_refund", expr, dto.Usage{PromptTokens: 300, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200}}, 0, true},
		{"fixed_success", `tier("request",fixed(0.02))`, dto.Usage{}, 10000, false},
		{"undelivered_fixed_refund", `tier("request",fixed(0.02))`, dto.Usage{PromptTokens: 300}, 0, true},
		{"claude_1h", expr, dto.Usage{UsageSemantic: "anthropic", PromptTokens: 2, CompletionTokens: 8, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 1000, CachedCreationTokens: 20}, ClaudeCacheCreation5mTokens: 12, ClaudeCacheCreation1hTokens: 8}, 173, false},
	}
	ratios := []float64{0, 1, 3.3}
	for _, tc := range cases {
		for _, ratio := range ratios {
			for failure := range 3 {
				require.NoError(t, runForkBillingLoadCase(tc, ratio, failure), "warmup")
			}
		}
	}
	runtime.GC()
	baseline := runtime.NumGoroutine()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	latency := make([]time.Duration, count)
	jobs := make(chan int, workers)
	var wg sync.WaitGroup
	var failures atomic.Int64
	var first string
	var once sync.Once
	started := time.Now()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				at := time.Now()
				tc := cases[i%len(cases)]
				ratio := ratios[(i/len(cases))%len(ratios)]
				failure := (i / (len(cases) * len(ratios))) % 3
				if err := runForkBillingLoadCase(tc, ratio, failure); err != nil {
					failures.Add(1)
					once.Do(func() { first = err.Error() })
				}
				latency[i] = time.Since(at)
			}
		}()
	}
	for i := range count {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	runtime.GC()
	var retained runtime.MemStats
	runtime.ReadMemStats(&retained)
	sort.Slice(latency, func(i, j int) bool { return latency[i] < latency[j] })
	report := map[string]any{"family": "billing", "requests": count, "workers": workers, "scenario_combinations": len(cases) * len(ratios) * 3, "unexpected": failures.Load(), "elapsed_s": elapsed.Seconds(), "rps": float64(count) / elapsed.Seconds(), "p50_ms": float64(latency[count/2]) / 1e6, "p95_ms": float64(latency[count*95/100]) / 1e6, "p99_ms": float64(latency[count*99/100]) / 1e6, "heap_before": before.HeapAlloc, "heap_after_gc": retained.HeapAlloc, "alloc_bytes_per_request": (after.TotalAlloc - before.TotalAlloc) / uint64(count), "gc_cycles": after.NumGC - before.NumGC, "goroutines_before": baseline, "goroutines_after": runtime.NumGoroutine()}
	data, err := common.Marshal(report)
	require.NoError(t, err)
	t.Logf("FAULT_BILLING_LOAD %s", data)
	require.Zero(t, failures.Load(), first)
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= baseline+2 }, time.Second, 10*time.Millisecond)
}
