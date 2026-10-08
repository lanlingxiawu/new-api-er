package model

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Branch audit of the async relay-log pipeline (model/relay_log_pipeline.go).
// Every test here encodes a contract the pipeline must keep. Regression tests
// for the defects the audit confirmed (and their follow-ups) live in
// relay_log_pipeline_branch_audit_regression_test.go and run in the default build
// like every other test.

// relayLogAuditAccounting records every accounting continuation by user id.
type relayLogAuditAccounting struct {
	mu    sync.Mutex
	calls map[int][]int // user id -> log ids passed to the handler, in call order
}

func (a *relayLogAuditAccounting) record(userID, logID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[userID] = append(a.calls[userID], logID)
}

func (a *relayLogAuditAccounting) idsFor(userID int) []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.calls[userID]...)
}

// useRelayLogAuditAccounting installs a recording accounting handler. Payloads
// whose UserID equals blockUserID block on the returned gate until release is
// called (release is idempotent and also runs at cleanup). Cleanup drains the
// shared aux workers before restoring the previous handler so in-flight jobs
// are never attributed to the next test.
func useRelayLogAuditAccounting(t *testing.T, blockUserID int) (*relayLogAuditAccounting, func()) {
	t.Helper()
	drainRelayLogAux(t, 30*time.Second)
	rec := &relayLogAuditAccounting{calls: map[int][]int{}}
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	previous := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(payload RelayLogAccountingPayload, logID int) {
		if blockUserID != 0 && payload.UserID == blockUserID {
			<-gate
			return
		}
		rec.record(payload.UserID, logID)
	})
	t.Cleanup(func() {
		release()
		drainRelayLogAux(t, 30*time.Second)
		RegisterRelayLogAccountingHandler(previous)
	})
	return rec, release
}

// relayLogAuditBlocker is the user id whose accounting blocks on the gate of
// useRelayLogAuditAccounting; the saturation helpers park workers with it.
const relayLogAuditBlocker = -7_777_001

// saturateRelayLogContinuation parks both fixed continuation workers on a
// blocking payload and leaves one more job queued, with the continuation soft
// cap set to 1: the next continuation dispatch deterministically takes the
// overflow lane (fallback worker). Returns the recorder and a release func.
func saturateRelayLogContinuation(t *testing.T) (*relayLogAuditAccounting, func()) {
	t.Helper()
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	rec, release := useRelayLogAuditAccounting(t, relayLogAuditBlocker)
	cfg.ContinuationBufMaxEntries = 1
	startRelayLogAuxWorkers()

	payload := RelayLogAccountingPayload{Version: 1, UserID: relayLogAuditBlocker}
	DispatchRelayLogAccounting(payload, 0)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return relayLogContinuationBusy.Load() == 1 }))
	DispatchRelayLogAccounting(payload, 0)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return relayLogContinuationBusy.Load() == 2 }))
	DispatchRelayLogAccounting(payload, 0)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return len(relayLogContinuationCh) == 1 }),
		"third blocker must stay queued so the soft cap (1) is reached")
	return rec, release
}

// runWithin fails the test if fn does not return within d (guards against the
// "batch size 0 → i += 0" infinite-loop class of bug).
func runWithin(t *testing.T, d time.Duration, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %s", name, d)
	}
}

// Config edge values (0, negative, 1) must never produce a non-terminating
// flush or retry cycle, and every buffered row must eventually be persisted.
func TestRelayLogBranchAudit_FlushTerminatesForNonPositiveAndMinimalBatchConfig(t *testing.T) {
	cases := []struct {
		name                  string
		outer, inner, perCycl int
	}{
		{"zero", 0, 0, 0},
		{"negative", -1, -5, -3},
		{"one", 1, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := useRelayLogPipelineSQLite(t)
			useRelayLogFallbackDir(t)
			cfg := operation_setting.GetRelayLogPipelineSetting()
			old := *cfg
			t.Cleanup(func() { *cfg = old })
			cfg.OuterBatchSize, cfg.InnerBatchSize, cfg.FlushMaxPerCycle = tc.outer, tc.inner, tc.perCycl
			cfg.FullDrain = false

			for i := 0; i < 5; i++ {
				require.True(t, enqueueRelayLog(relayLogKindConsume,
					&relayLogEvent{Log: &Log{RequestId: fmt.Sprintf("edge-%s-%d", tc.name, i), Type: LogTypeConsume}}))
			}
			for cycle := 0; cycle < 10 && relayLogBacklog() > 0; cycle++ {
				runWithin(t, 5*time.Second, "flushRelayLogs", flushRelayLogs)
			}
			require.Zero(t, relayLogBacklog())

			retryCfg := operation_setting.GetRelayLogRetrySetting()
			oldRetry := *retryCfg
			t.Cleanup(func() { *retryCfg = oldRetry })
			retryCfg.RetryBufMaxEntries = tc.perCycl
			if tc.perCycl <= 0 {
				// Non-positive MaxRetries must clamp to the default, not to
				// "every event is already exhausted".
				retryCfg.MaxRetries = tc.perCycl
			}
			enqueueRelayLogRetry([]*relayLogEvent{{Log: &Log{RequestId: "edge-retry-" + tc.name, Type: LogTypeConsume}}})
			runWithin(t, 5*time.Second, "flushRelayLogRetries", flushRelayLogRetries)

			var n int64
			require.NoError(t, db.Model(&Log{}).Count(&n).Error)
			require.EqualValues(t, 6, n, "every buffered row must be persisted exactly once")
			require.Empty(t, takeRelayLogRetryBatch(10))
		})
	}
}

// A failed INSERT that is later retried successfully must run the accounting
// continuation exactly once, with the id of the persisted row. Further retry
// cycles or re-dispatches of the same event must not repeat it.
func TestRelayLogBranchAudit_ContinuationExactlyOnceAcrossFailedInsertThenRetry(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	rec, _ := useRelayLogAuditAccounting(t, 0)

	broken, err := gorm.Open(sqlite.Open("file:"+uniq("rl_broken")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlBroken, err := broken.DB()
	require.NoError(t, err)
	require.NoError(t, sqlBroken.Close())

	const uid = 7_777_101
	event := &relayLogEvent{
		Log:        &Log{RequestId: "exactly-once", Type: LogTypeConsume, UserId: uid},
		Accounting: &RelayLogAccountingPayload{Version: 1, UserID: uid, Quota: 3},
	}

	LOG_DB = broken
	require.False(t, persistRelayLogEvents([]*relayLogEvent{event}))
	require.Never(t, func() bool { return len(rec.idsFor(uid)) > 0 }, 30*time.Millisecond, time.Millisecond,
		"accounting must wait for the insert outcome")

	LOG_DB = db
	flushRelayLogRetries()
	require.True(t, waitFor(t, 5*time.Second, func() bool { return len(rec.idsFor(uid)) == 1 }))

	var stored Log
	require.NoError(t, db.Where("request_id = ?", "exactly-once").First(&stored).Error)
	flushRelayLogRetries()
	dispatchRelayLogContinuation(event, 999)
	drainRelayLogAux(t, 5*time.Second)
	require.Equal(t, []int{stored.Id}, rec.idsFor(uid))
	var n int64
	require.NoError(t, db.Model(&Log{}).Where("request_id = ?", "exactly-once").Count(&n).Error)
	require.EqualValues(t, 1, n)
}

// LogConsumeEnabled=false: no log row may be buffered, but accounting must run
// exactly once with log_id=0 (the pipeline owns it; EnqueueConsumeLog returns
// true so the caller does not dispatch a second copy).
func TestRelayLogBranchAudit_LogConsumeDisabledRunsAccountingOnceWithoutLog(t *testing.T) {
	useRelayLogFallbackDir(t)
	rec, _ := useRelayLogAuditAccounting(t, 0)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)
	prevConsume, prevExport := common.LogConsumeEnabled, common.DataExportEnabled
	common.LogConsumeEnabled, common.DataExportEnabled = false, false
	t.Cleanup(func() { common.LogConsumeEnabled, common.DataExportEnabled = prevConsume, prevExport })
	prevAccepting := relayLogAccepting.Load()
	t.Cleanup(func() { relayLogAccepting.Store(prevAccepting) })

	for _, accepting := range []bool{true, false} {
		relayLogAccepting.Store(accepting)
		uid := 7_777_201
		if !accepting {
			uid++
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		payload := RelayLogAccountingPayload{Version: 1, UserID: uid, Quota: 5}
		require.True(t, EnqueueConsumeLog(c, uid, RecordConsumeLogParams{ModelName: "m", Quota: 5}, &payload),
			"disabled consume logging is not an intake failure; caller must not re-dispatch accounting")
		// drainRelayLogAux alone is not enough: a job already received by a
		// worker but not yet counted busy looks idle for an instant.
		require.True(t, waitFor(t, 5*time.Second, func() bool { return len(rec.idsFor(uid)) > 0 }))
		require.Never(t, func() bool { return len(rec.idsFor(uid)) > 1 }, 30*time.Millisecond, time.Millisecond)
		require.Equal(t, []int{0}, rec.idsFor(uid))
		require.Zero(t, relayLogBacklog(), "no consume log may be buffered while LogConsumeEnabled=false")
	}
}

func writeRelayLogAuditLines(t *testing.T, path string, lines []string, trailingNewline bool) {
	t.Helper()
	data := strings.Join(lines, "\n")
	if trailingNewline {
		data += "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(data), 0o640))
}

func relayLogAuditRecordLine(t *testing.T, log *Log) string {
	t.Helper()
	data, err := common.Marshal(relayLogFallbackRecord{Version: 1, Kind: "relay_log_only", RecordedAt: 1, Log: log})
	require.NoError(t, err)
	return string(data)
}

// Corrupt JSON, an unknown record version and a crash-truncated trailing line
// must be retained verbatim (never dropped), valid rows around them inserted
// once, and a second replay of the retained file must not re-insert anything.
func TestRelayLogBranchAudit_ReplayRetainsBadLinesAndIsIdempotent(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)
			cfg := operation_setting.GetRelayLogPipelineSetting()
			old := *cfg
			t.Cleanup(func() { *cfg = old })
			cfg.InnerBatchSize = 2

			corrupt := `{not json`
			v2 := `{"version":2,"log":{"request_id":"` + prefix + `v2"}}`
			partialFull := relayLogAuditRecordLine(t, &Log{RequestId: prefix + "partial", Type: LogTypeConsume})
			partial := partialFull[:len(partialFull)/2]
			path := filepath.Join(t.TempDir(), "relay-log.jsonl.1")
			writeRelayLogAuditLines(t, path, []string{
				relayLogAuditRecordLine(t, &Log{RequestId: prefix + "a", Type: LogTypeConsume}),
				corrupt,
				v2,
				relayLogAuditRecordLine(t, &Log{RequestId: prefix + "b", Type: LogTypeConsume}) + "\r",
				relayLogAuditRecordLine(t, &Log{RequestId: prefix + "c", Type: LogTypeConsume}),
				partial,
			}, false)

			first := replayRelayLogFallback(context.Background(), path)
			require.Error(t, first.err)
			require.EqualValues(t, 3, first.processed)
			require.EqualValues(t, 3, first.failed)
			require.Equal(t, corrupt+"\n"+v2+"\n"+partial+"\n", string(mustReadFile(t, path)),
				"bad lines must be retained verbatim, valid lines must not be retained")
			requireOneRowEach(t, db, prefix+"a", prefix+"b", prefix+"c")

			second := replayRelayLogFallback(context.Background(), path)
			require.Error(t, second.err)
			require.Zero(t, second.processed)
			require.EqualValues(t, 3, second.failed)
			require.FileExists(t, path)
			requireOneRowEach(t, db, prefix+"a", prefix+"b", prefix+"c")
			var n int64
			require.NoError(t, db.Model(&Log{}).Where("request_id IN ?", []string{prefix + "partial", prefix + "v2"}).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

// A failed middle batch retains only its own rows; committed batches before
// and after it are not retained, so the follow-up replay inserts only the
// failed rows (no duplicates).
func TestRelayLogBranchAudit_ReplayBatchFailureRetainsOnlyFailedBatch(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)
			cfg := operation_setting.GetRelayLogPipelineSetting()
			old := *cfg
			t.Cleanup(func() { *cfg = old })
			cfg.InnerBatchSize = 1

			path := filepath.Join(t.TempDir(), "relay-log.jsonl.1")
			bLine := relayLogAuditRecordLine(t, &Log{RequestId: prefix + "b", Type: LogTypeConsume})
			writeRelayLogAuditLines(t, path, []string{
				relayLogAuditRecordLine(t, &Log{RequestId: prefix + "a", Type: LogTypeConsume}),
				bLine,
				relayLogAuditRecordLine(t, &Log{RequestId: prefix + "c", Type: LogTypeConsume}),
			}, true)

			oldWriter := relayLogReplayBatchWriter
			t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })
			relayLogReplayBatchWriter = func(ctx context.Context, logs []*Log, batchSize int) error {
				if logs[0].RequestId == prefix+"b" {
					return errors.New("injected batch failure")
				}
				return insertRelayLogs(LOG_DB.WithContext(ctx), logs, batchSize)
			}
			first := replayRelayLogFallback(context.Background(), path)
			require.Error(t, first.err)
			require.EqualValues(t, 2, first.processed)
			require.EqualValues(t, 1, first.failed)
			require.Equal(t, bLine+"\n", string(mustReadFile(t, path)))

			relayLogReplayBatchWriter = oldWriter
			second := replayRelayLogFallback(context.Background(), path)
			require.NoError(t, second.err)
			require.EqualValues(t, 1, second.processed)
			require.NoFileExists(t, path)
			requireOneRowEach(t, db, prefix+"a", prefix+"b", prefix+"c")
		})
	}
}

// insertRelayLogs with a lookup chunk smaller than the number of known ids
// (exercises the chunk loop in storedRelayLogIdentities) on all three DBs:
// committed rows are skipped and keep their id, rolled-back ids are re-inserted
// fresh, id-less rows are inserted once.
func TestRelayLogBranchAudit_InsertRelayLogsChunkedIdentityLookup(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)

			c1 := committedLog(t, db, prefix+"c1")
			c2 := committedLog(t, db, prefix+"c2")
			c3 := committedLog(t, db, prefix+"c3")
			rolled := rolledBackLog(t, db, prefix+"rolled")
			staleID := rolled.Id
			f1 := &Log{RequestId: prefix + "f1", Type: LogTypeConsume}
			f2 := &Log{RequestId: prefix + "f2", Type: LogTypeConsume}
			committedIDs := []int{c1.Id, c2.Id, c3.Id}

			logs := []*Log{c1, f1, c2, rolled, f2, c3}
			require.NoError(t, insertRelayLogs(db, logs, 1))

			ids := requireOneRowEach(t, db, prefix+"c1", prefix+"c2", prefix+"c3", prefix+"rolled", prefix+"f1", prefix+"f2")
			require.Equal(t, committedIDs, []int{c1.Id, c2.Id, c3.Id}, "committed rows keep their id")
			require.Equal(t, committedIDs, []int{ids[prefix+"c1"], ids[prefix+"c2"], ids[prefix+"c3"]})
			require.NotEqual(t, staleID, rolled.Id)
			require.Equal(t, ids[prefix+"rolled"], rolled.Id)
			require.Equal(t, ids[prefix+"f1"], f1.Id)
			require.Equal(t, ids[prefix+"f2"], f2.Id)
		})
	}
}

func makeSizedFile(t *testing.T, path string, size int64) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(size))
	require.NoError(t, file.Close())
}

// Rotation boundary (size >= max rotates, size < max does not), the file-size
// default for 0, and the retention clamp for 0 (-> 8) and oversized (-> 64).
func TestRelayLogBranchAudit_FallbackRotationBoundaryAndRetentionClamp(t *testing.T) {
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	const mb = int64(1024 * 1024)

	t.Run("size boundary", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "relay-log.jsonl")
		cfg.FallbackMaxFileSizeMB, cfg.FallbackMaxFiles = 1, 8
		makeSizedFile(t, path, mb-1)
		require.NoError(t, rotateRelayLogFallback(path))
		require.FileExists(t, path)
		matches, _ := filepath.Glob(path + ".*")
		require.Empty(t, matches)

		makeSizedFile(t, path, mb)
		require.NoError(t, rotateRelayLogFallback(path))
		require.NoFileExists(t, path)
		matches, _ = filepath.Glob(path + ".*")
		require.Len(t, matches, 1)
	})

	t.Run("size zero uses 256MB default", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "relay-log.jsonl")
		cfg.FallbackMaxFileSizeMB, cfg.FallbackMaxFiles = 0, 8
		makeSizedFile(t, path, 2*mb)
		require.NoError(t, rotateRelayLogFallback(path))
		require.FileExists(t, path)
	})

	for _, tc := range []struct {
		name       string
		configured int
		effective  int
	}{{"files zero -> 8", 0, 8}, {"files negative -> 8", -3, 8}, {"files oversized -> 64", 1000, 64}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "relay-log.jsonl")
			cfg.FallbackMaxFileSizeMB, cfg.FallbackMaxFiles = 1, tc.configured
			for i := 0; i < tc.effective-1; i++ {
				require.NoError(t, os.WriteFile(fmt.Sprintf("%s.%d", path, i), []byte("x\n"), 0o640))
			}
			makeSizedFile(t, path, mb)
			require.NoError(t, rotateRelayLogFallback(path), "one slot below the cap still rotates")
			makeSizedFile(t, path, mb)
			require.ErrorIs(t, rotateRelayLogFallback(path), errRelayLogFallbackRetentionFull)
			require.FileExists(t, path, "retention-full must not destroy the active file")
		})
	}
}

// Circuit breaker: open -> nothing written, everything deferred to retry;
// half-open -> exactly one probe row written, the rest deferred; success closes.
func TestRelayLogBranchAudit_CircuitOpenAndHalfOpenProbe(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	mk := func(name string) []*relayLogEvent {
		return []*relayLogEvent{
			{Log: &Log{RequestId: name + "-1", Type: LogTypeConsume}},
			{Log: &Log{RequestId: name + "-2", Type: LogTypeConsume}},
			{Log: &Log{RequestId: name + "-3", Type: LogTypeConsume}},
		}
	}

	relayLogCircuitOpenUntil.Store(time.Now().Add(time.Minute).UnixNano())
	require.Equal(t, "open", GetRelayLogPipelineStatus().CircuitState)
	require.False(t, persistRelayLogEvents(mk("open")))
	require.Len(t, takeRelayLogRetryBatch(0), 3)
	var n int64
	require.NoError(t, db.Model(&Log{}).Count(&n).Error)
	require.Zero(t, n)

	relayLogCircuitOpenUntil.Store(time.Now().Add(-time.Second).UnixNano())
	require.Equal(t, "half_open", GetRelayLogPipelineStatus().CircuitState)
	require.True(t, persistRelayLogEvents(mk("half")))
	require.NoError(t, db.Model(&Log{}).Count(&n).Error)
	require.EqualValues(t, 1, n, "half-open must probe with a single row")
	deferred := takeRelayLogRetryBatch(0)
	require.Len(t, deferred, 2)
	require.Equal(t, "half-2", deferred[0].Log.RequestId)
	require.Equal(t, "closed", GetRelayLogPipelineStatus().CircuitState)
	require.False(t, relayLogHalfOpenProbe.Load(), "probe flag must be released")
	require.EqualValues(t, 1, relayLogPersistedTotal.Load())
}

// When the continuation lane is saturated after a successful INSERT, the
// accounting continuation must still run (through the fallback worker) and run
// exactly once — never lost, never doubled. The reroute is counted as
// overflowed, not dropped: nothing was lost.
func TestRelayLogBranchAudit_ContinuationOverflowStillRunsAccountingExactlyOnce(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	rec, release := saturateRelayLogContinuation(t)
	dropBefore := relayLogContinuationDrop.Load()
	overflowBefore := relayLogContinuationOverflow.Load()
	errorsBefore := relayLogFallbackErrors.Load()

	const uid = 7_777_301
	event := &relayLogEvent{
		Log:        &Log{RequestId: "overflow-once", Type: LogTypeConsume, UserId: uid},
		Accounting: &RelayLogAccountingPayload{Version: 1, UserID: uid, Quota: 1},
	}
	require.True(t, persistRelayLogEvents([]*relayLogEvent{event}))
	require.True(t, waitFor(t, 5*time.Second, func() bool { return len(rec.idsFor(uid)) == 1 }),
		"overflowed accounting must run via the fallback lane even while continuation workers are stuck")
	require.EqualValues(t, overflowBefore+1, relayLogContinuationOverflow.Load())
	require.Equal(t, dropBefore, relayLogContinuationDrop.Load(), "a rerouted continuation was not lost")
	require.Equal(t, errorsBefore, relayLogFallbackErrors.Load(), "the log row is stored; nothing was lost")

	release()
	drainRelayLogAux(t, 10*time.Second)
	dispatchRelayLogContinuation(event, 1)
	drainRelayLogAux(t, 5*time.Second)
	require.Len(t, rec.idsFor(uid), 1)
}

// Status counters: buffer reject-new, retry eviction and fallback writes are
// each counted once, and capacities reflect the live (hot-reloaded) config.
func TestRelayLogBranchAudit_StatusCountersTrackDropsAndFallback(t *testing.T) {
	useRelayLogFallbackDir(t)
	useRelayLogAuditAccounting(t, 0)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	retryCfg := operation_setting.GetRelayLogRetrySetting()
	oldRetry := *retryCfg
	t.Cleanup(func() { *cfg = old; *retryCfg = oldRetry; resetRelayLogPipelineForTest() })
	resetRelayLogPipelineForTest()
	cfg.ConsumeBufMaxEntries, cfg.ErrorBufMaxEntries = 1, 1
	retryCfg.RetryBufMaxEntries = 1

	for i := 0; i < 2; i++ {
		require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "st-consume"}}))
		require.True(t, enqueueRelayLog(relayLogKindError, &relayLogEvent{Log: &Log{RequestId: "st-error"}}))
	}
	enqueueRelayLogRetry([]*relayLogEvent{
		{Log: &Log{RequestId: "st-r1"}}, {Log: &Log{RequestId: "st-r2"}}, {Log: &Log{RequestId: "st-r3"}},
	})
	drainRelayLogAux(t, 5*time.Second)

	status := GetRelayLogPipelineStatus()
	require.Equal(t, RelayLogQueueStatus{Backlog: 1, Capacity: 1, Dropped: 1}, status.Consume)
	require.Equal(t, RelayLogQueueStatus{Backlog: 1, Capacity: 1, Dropped: 1}, status.Error)
	require.Equal(t, RelayLogQueueStatus{Backlog: 1, Capacity: 1, Dropped: 2}, status.Retry)
	require.EqualValues(t, 4, status.FallbackTotal, "1 consume + 1 error + 2 evicted retries spilled to JSONL")
	require.Zero(t, status.FallbackErrors)
	require.Zero(t, status.PersistedTotal)
	require.Equal(t, "st-r3", takeRelayLogRetryBatch(0)[0].Log.RequestId, "retry eviction drops the oldest")
}
