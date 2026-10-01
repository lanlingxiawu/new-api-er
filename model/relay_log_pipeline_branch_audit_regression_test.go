package model

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// Regression tests for the relay-log pipeline defects found by the branch
// audit (docs/design/branch-audit-vs-main.md: H2, M5, L5, L6, L7). Test names
// keep the audit's identifiers so each one can be traced back to the report.

// fillRelayLogFallbackRetention drives the real pressure path: the fallback
// directory is at its retention cap, so the fallback writer rejects the next
// batch with errRelayLogFallbackRetentionFull. The pipeline is marked as
// running (intake open) for the duration of the test. Returns the fallback dir.
func fillRelayLogFallbackRetention(t *testing.T) string {
	t.Helper()
	dir := useRelayLogFallbackDir(t)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	prevAccepting := relayLogAccepting.Load()
	t.Cleanup(func() {
		drainRelayLogAux(t, 5*time.Second)
		*cfg = old
		relayLogAccepting.Store(prevAccepting)
		resetRelayLogPipelineForTest()
	})
	resetRelayLogPipelineForTest()
	cfg.Enabled = true
	cfg.FallbackMaxFileSizeMB, cfg.FallbackMaxFiles = 1, 1

	base := filepath.Join(dir, "relay-log.jsonl")
	makeSizedFile(t, base, 1024*1024)
	writeFallbackRecords(t, base+".1", &Log{RequestId: "retention-parked", Type: LogTypeConsume})

	relayLogAccepting.Store(true)
	dispatchRelayLogFallbackJob(&relayLogEvent{Kind: relayLogKindConsume, Log: &Log{RequestId: "retention-probe"}}, false)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return relayLogFallbackErrors.Load() == 1 }),
		"precondition: the fallback write must be rejected by the retention cap")
	drainRelayLogAux(t, 5*time.Second)
	require.True(t, relayLogRetentionFullLogged.Load(), "precondition: retention-full state was entered")
	return dir
}

// H2: fallback pressure must never latch intake shut. Retention-full only means
// the fallback lane cannot take more overflow; the primary lane (buffer -> log
// DB) keeps accepting. Once space is freed, the status clears by itself and the
// fallback lane writes again — no restart, no toggle.
func TestRelayLogBranchAuditRegression_IntakeStaysStoppedAfterRetentionRecovers(t *testing.T) {
	dir := fillRelayLogFallbackRetention(t)

	status := GetRelayLogPipelineStatus()
	require.True(t, status.FallbackRetentionFull, "status must show the full fallback directory")
	require.Equal(t, "accepting", status.IntakeState, "pressure must not stop intake")
	require.True(t, enqueueAsyncRelayLog(relayLogKindConsume,
		&Log{RequestId: "during-pressure", Type: LogTypeConsume}, nil, nil),
		"logs must still be accepted while only the fallback directory is full")

	// Operator frees the fallback directory (or a backfill drains it).
	require.NoError(t, os.RemoveAll(dir))
	require.False(t, GetRelayLogPipelineStatus().FallbackRetentionFull, "status must clear once space is freed")
	require.True(t, enqueueAsyncRelayLog(relayLogKindConsume,
		&Log{RequestId: "after-recovery", Type: LogTypeConsume}, nil, nil))

	totalBefore := relayLogFallbackTotal.Load()
	dispatchRelayLogFallbackJob(&relayLogEvent{Kind: relayLogKindConsume, Log: &Log{RequestId: "fallback-after-recovery"}}, false)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return relayLogFallbackTotal.Load() == totalBefore+1 }),
		"the fallback lane must write again once space is freed")
	drainRelayLogAux(t, 5*time.Second)
	require.False(t, relayLogRetentionFullLogged.Load(), "a successful write re-arms the one-shot retention log")
}

// H2: manual backfill is the way to drain parked fallback files, so fallback
// pressure must not make it look like a shutdown.
func TestRelayLogBranchAuditRegression_ReplayRefusedAfterPressureStop(t *testing.T) {
	fillRelayLogFallbackRetention(t)
	resetRelayLogReplayForTest()
	oldWriter := relayLogReplayBatchWriter
	relayLogReplayBatchWriter = func(context.Context, []*Log, int) error { return nil }
	t.Cleanup(func() {
		stopRelayLogFallbackReplay()
		relayLogReplayWG.Wait()
		relayLogReplayBatchWriter = oldWriter
		resetRelayLogReplayForTest()
	})

	started, err := StartRelayLogFallbackReplay()
	require.NoError(t, err, "replay must not be refused outside shutdown")
	require.True(t, started)
	require.True(t, waitFor(t, 5*time.Second, func() bool {
		return GetRelayLogPipelineStatus().Replay.State == "succeeded"
	}))
	require.False(t, GetRelayLogPipelineStatus().FallbackRetentionFull,
		"a finished backfill frees the directory and the pressure state clears by itself")
}

// Shutdown is the one state in which replay is still refused.
func TestRelayLogReplayRefusedWhileShuttingDown(t *testing.T) {
	useRelayLogFallbackDir(t)
	resetRelayLogReplayForTest()
	prevAccepting := relayLogAccepting.Load()
	t.Cleanup(func() { relayLogAccepting.Store(prevAccepting); resetRelayLogReplayForTest() })
	relayLogAccepting.Store(false)

	started, err := StartRelayLogFallbackReplay()
	require.ErrorIs(t, err, ErrRelayLogReplayShuttingDown)
	require.False(t, started)
	require.False(t, relayLogReplayRunning.Load(), "a refused start must release the single-flight flag")
}

// H2: every log refused by a closed intake is counted (per kind in dropped and
// in intake_refused), and pressure alone refuses nothing.
func TestRelayLogBranchAuditRegression_LogsRefusedWhileIntakeStoppedAreNotCounted(t *testing.T) {
	fillRelayLogFallbackRetention(t)

	before := GetRelayLogPipelineStatus()
	for i := 0; i < 3; i++ {
		require.True(t, enqueueAsyncRelayLog(relayLogKindConsume, &Log{RequestId: "pressure-consume", Type: LogTypeConsume}, nil, nil))
	}
	underPressure := GetRelayLogPipelineStatus()
	require.Equal(t, before.Consume.Dropped, underPressure.Consume.Dropped)
	require.Zero(t, underPressure.IntakeRefused, "pressure must not refuse logs")

	// Intake closed (shutdown): the outer check refuses.
	relayLogAccepting.Store(false)
	for i := 0; i < 3; i++ {
		require.False(t, enqueueAsyncRelayLog(relayLogKindConsume, &Log{RequestId: "refused-consume", Type: LogTypeConsume}, nil, nil))
	}
	for i := 0; i < 2; i++ {
		require.False(t, enqueueAsyncRelayLog(relayLogKindError, &Log{RequestId: "refused-error", Type: LogTypeError}, nil, nil))
	}
	after := GetRelayLogPipelineStatus()
	require.Equal(t, "stopped", after.IntakeState)
	require.EqualValues(t, 3, after.Consume.Dropped-underPressure.Consume.Dropped)
	require.EqualValues(t, 2, after.Error.Dropped-underPressure.Error.Dropped)
	require.EqualValues(t, 5, after.IntakeRefused)

	// A relay goroutine that passed the outer check just before shutdown sealed
	// the buffers is refused under the buffer lock and counted the same way.
	relayLogAccepting.Store(true)
	sealRelayLogBuffers()
	backlog := relayLogBacklog()
	require.False(t, enqueueAsyncRelayLog(relayLogKindConsume, &Log{RequestId: "sealed", Type: LogTypeConsume}, nil, nil))
	require.False(t, enqueueRelayLog(relayLogKindError, &relayLogEvent{Log: &Log{RequestId: "sealed-error", Type: LogTypeError}}))
	sealed := GetRelayLogPipelineStatus()
	require.Equal(t, backlog, relayLogBacklog(), "nothing may be parked in a sealed buffer")
	require.Equal(t, "stopped", sealed.IntakeState)
	require.EqualValues(t, 7, sealed.IntakeRefused)
	require.EqualValues(t, 1, sealed.Consume.Dropped-after.Consume.Dropped)
	require.EqualValues(t, 1, sealed.Error.Dropped-after.Error.Dropped)
}

// Worker panics used to stop intake for good. A panicking fallback batch is now
// contained to that batch: the worker keeps consuming and intake stays open.
func TestRelayLogFallbackWorkerSurvivesPanickingBatch(t *testing.T) {
	dir := useRelayLogFallbackDir(t)
	resetRelayLogPipelineForTest()
	prevAccepting := relayLogAccepting.Load()
	t.Cleanup(func() {
		drainRelayLogAux(t, 5*time.Second)
		relayLogAccepting.Store(prevAccepting)
		resetRelayLogPipelineForTest()
	})
	relayLogAccepting.Store(true)
	rec, _ := useRelayLogAuditAccounting(t, 0)
	startRelayLogAuxWorkers()

	relayLogAuxSendMu.RLock()
	relayLogFallbackCh <- relayLogFallbackJob{event: nil} // nil event: the batch writer panics
	relayLogAuxSendMu.RUnlock()
	// FIFO sentinel: whether it lands in the panicking batch (continuations still
	// run in the writer's defer) or in a later one, its accounting runs only after
	// the panicking job has been handled.
	const sentinel = 7_777_601
	dispatchRelayLogFallbackJob(&relayLogEvent{Accounting: &RelayLogAccountingPayload{Version: 1, UserID: sentinel}}, true)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return len(rec.idsFor(sentinel)) == 1 }))
	drainRelayLogAux(t, 5*time.Second)
	require.Zero(t, relayLogFallbackErrors.Load(), "the panicking batch carried no log row, so no log was lost")

	dispatchRelayLogFallbackJob(&relayLogEvent{Kind: relayLogKindConsume, Log: &Log{RequestId: "after-panic-batch"}}, false)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return relayLogFallbackTotal.Load() == 1 }),
		"the same worker must keep writing after a panicking batch")
	data, err := os.ReadFile(filepath.Join(dir, "relay-log.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(data), "after-panic-batch")
	require.True(t, relayLogAccepting.Load(), "a worker panic must not close intake")
}

// F1: a panicking fallback batch counts only the log rows it may have lost.
// Jobs that borrow the fallback worker for accounting carry no log row; their
// accounting still runs (the continuations run in a defer), so neither the
// rerouted accounting nor an accounting-only event may show up as a lost log.
func TestRelayLogFallbackBatchPanicCountsOnlyLogRows(t *testing.T) {
	useRelayLogFallbackDir(t)
	rec, _ := useRelayLogAuditAccounting(t, 0)
	errorsBefore := relayLogFallbackErrors.Load()
	totalBefore := relayLogFallbackTotal.Load()
	dropBefore := relayLogContinuationDrop.Load()

	const rerouted, accountingOnly = 7_777_701, 7_777_702
	batch := []relayLogFallbackJob{
		// A log row waiting to be parked: lost when the batch panics before the write.
		{event: &relayLogEvent{Kind: relayLogKindConsume, Log: &Log{RequestId: "panic-batch-log"}}},
		// Continuation overflow after a successful insert: the row is already stored.
		{event: &relayLogEvent{Log: &Log{RequestId: "panic-batch-stored"},
			Accounting: &RelayLogAccountingPayload{Version: 1, UserID: rerouted}},
			executeContinuation: true, continuationOnly: true, logID: 99},
		// Accounting-only overflow: no log row at all.
		{event: &relayLogEvent{Accounting: &RelayLogAccountingPayload{Version: 1, UserID: accountingOnly}},
			executeContinuation: true, continuationOnly: true},
		{event: nil}, // makes the batch writer panic after the first row was serialized
	}
	relayLogFallbackBusy.Add(int64(len(batch))) // the worker adds this before running a batch
	require.NotPanics(t, func() { runRelayLogFallbackBatch(batch) })

	require.EqualValues(t, errorsBefore+1, relayLogFallbackErrors.Load(), "exactly the one unwritten log row is lost")
	require.Equal(t, totalBefore, relayLogFallbackTotal.Load())
	require.Equal(t, dropBefore, relayLogContinuationDrop.Load())
	require.Equal(t, []int{99}, rec.idsFor(rerouted), "rerouted accounting still runs with its logs.id")
	require.Equal(t, []int{0}, rec.idsFor(accountingOnly))
	require.Zero(t, relayLogFallbackBusy.Load(), "busy is released even when the batch panics")
}

// F2: a panic inside an accounting handler is contained to that job. The fixed
// continuation workers keep consuming (more panics than workers are sent), the
// busy count returns to zero and intake is untouched. The panic is reported via
// the system log; it is not a capacity event, so it moves none of the
// dropped/overflowed/fallback counters.
func TestRelayLogContinuationWorkerSurvivesPanickingAccounting(t *testing.T) {
	drainRelayLogAux(t, 30*time.Second)
	const panicUID, okUID = 7_777_801, 7_777_802
	rec := &relayLogAuditAccounting{calls: map[int][]int{}}
	previous := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(payload RelayLogAccountingPayload, logID int) {
		if payload.UserID == panicUID {
			panic("accounting handler failure")
		}
		rec.record(payload.UserID, logID)
	})
	prevAccepting := relayLogAccepting.Load()
	t.Cleanup(func() {
		drainRelayLogAux(t, 30*time.Second)
		RegisterRelayLogAccountingHandler(previous)
		relayLogAccepting.Store(prevAccepting)
	})
	relayLogAccepting.Store(true)
	dropBefore := relayLogContinuationDrop.Load()
	overflowBefore := relayLogContinuationOverflow.Load()
	errorsBefore := relayLogFallbackErrors.Load()

	for i := 0; i < 3; i++ { // one more than the two fixed workers
		DispatchRelayLogAccounting(RelayLogAccountingPayload{Version: 1, UserID: panicUID}, 0)
	}
	DispatchRelayLogAccounting(RelayLogAccountingPayload{Version: 1, UserID: okUID}, 42)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return len(rec.idsFor(okUID)) == 1 }),
		"workers must keep consuming after panicking jobs")
	drainRelayLogAux(t, 5*time.Second)

	require.Equal(t, []int{42}, rec.idsFor(okUID))
	require.Zero(t, relayLogContinuationBusy.Load())
	require.True(t, relayLogAccepting.Load(), "a worker panic must not close intake")
	require.Equal(t, dropBefore, relayLogContinuationDrop.Load())
	require.Equal(t, overflowBefore, relayLogContinuationOverflow.Load())
	require.Equal(t, errorsBefore, relayLogFallbackErrors.Load())
}

// saturateRelayLogFallbackLane parks the fallback worker on a blocking payload
// (the gate of useRelayLogAuditAccounting, installed by
// saturateRelayLogContinuation) and leaves one job queued with the fallback
// soft cap set to 1, so the next fallback dispatch hits the soft-cap branch.
func saturateRelayLogFallbackLane(t *testing.T) {
	t.Helper()
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	cfg.FallbackQueueCapacity = 1
	blocker := func() {
		dispatchRelayLogFallbackJob(&relayLogEvent{Accounting: &RelayLogAccountingPayload{Version: 1, UserID: relayLogAuditBlocker}}, true)
	}
	blocker()
	require.True(t, waitFor(t, 5*time.Second, func() bool { return relayLogFallbackBusy.Load() >= 1 }))
	// The worker drains whatever is queued into its current batch before it
	// blocks, so keep feeding until one job stays queued behind the blocked batch.
	for i := 0; i < 50 && len(relayLogFallbackCh) == 0; i++ {
		blocker()
		waitFor(t, 20*time.Millisecond, func() bool { return len(relayLogFallbackCh) == 1 })
	}
	require.Equal(t, 1, len(relayLogFallbackCh), "precondition: fallback soft cap (1) is reached")
}

// fillRelayLogContinuationChannel fills the continuation channel to its
// physical capacity while both workers are parked, so the last resort fails.
func fillRelayLogContinuationChannel(t *testing.T) {
	t.Helper()
	filler := relayLogContinuationJob{event: &relayLogEvent{Accounting: &RelayLogAccountingPayload{Version: 1, UserID: relayLogAuditBlocker}}}
	relayLogAuxSendMu.RLock()
	defer relayLogAuxSendMu.RUnlock()
	for len(relayLogContinuationCh) < cap(relayLogContinuationCh) {
		select {
		case relayLogContinuationCh <- filler:
		default:
		}
	}
}

// F2 / L5: with the continuation lane over its soft cap and the fallback lane
// full, the last resort still queues the accounting on the continuation
// channel (below its physical capacity) and passes the real logs.id. This is a
// reroute, not a loss.
func TestRelayLogLastResortCarriesStoredLogID(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	rec, release := saturateRelayLogContinuation(t)
	saturateRelayLogFallbackLane(t)
	dropBefore := relayLogContinuationDrop.Load()
	overflowBefore := relayLogContinuationOverflow.Load()
	errorsBefore := relayLogFallbackErrors.Load()
	queued := len(relayLogContinuationCh)

	const uid = 7_777_901
	event := &relayLogEvent{
		Log:        &Log{RequestId: "last-resort-id", Type: LogTypeConsume, UserId: uid},
		Accounting: &RelayLogAccountingPayload{Version: 1, UserID: uid, Quota: 1},
	}
	require.True(t, persistRelayLogEvents([]*relayLogEvent{event}))
	require.Equal(t, queued+1, len(relayLogContinuationCh), "precondition: the last resort queued the job")
	require.EqualValues(t, overflowBefore+1, relayLogContinuationOverflow.Load())
	require.Equal(t, dropBefore, relayLogContinuationDrop.Load(), "the last resort took the job; nothing was lost")
	require.Equal(t, errorsBefore, relayLogFallbackErrors.Load(), "the row is stored; no log was lost")

	release()
	require.True(t, waitFor(t, 5*time.Second, func() bool { return len(rec.idsFor(uid)) == 1 }))
	var stored Log
	require.NoError(t, db.Where("request_id = ?", "last-resort-id").First(&stored).Error)
	require.Equal(t, []int{stored.Id}, rec.idsFor(uid), "the last resort must carry the stored logs.id, not 0")
}

// F1 / F2: when both lanes are exhausted the accounting is lost. It is counted
// in continuation_dropped exactly once (the reroute itself only counts as
// overflowed), the stored log row is not reported as a fallback error, and
// intake keeps accepting.
func TestRelayLogBothLanesExhaustedDropsContinuationOnceAndKeepsIntake(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	prevAccepting := relayLogAccepting.Load()
	t.Cleanup(func() { relayLogAccepting.Store(prevAccepting); resetRelayLogPipelineForTest() })
	relayLogAccepting.Store(true)
	rec, release := saturateRelayLogContinuation(t)
	saturateRelayLogFallbackLane(t)
	operation_setting.GetRelayLogPipelineSetting().Enabled = true // restored by the saturation helpers
	fillRelayLogContinuationChannel(t)
	dropBefore := relayLogContinuationDrop.Load()
	overflowBefore := relayLogContinuationOverflow.Load()
	errorsBefore := relayLogFallbackErrors.Load()

	const uid = 7_778_001
	event := &relayLogEvent{
		Log:        &Log{RequestId: "both-lanes-exhausted", Type: LogTypeConsume, UserId: uid},
		Accounting: &RelayLogAccountingPayload{Version: 1, UserID: uid, Quota: 1},
	}
	require.True(t, persistRelayLogEvents([]*relayLogEvent{event}))
	require.EqualValues(t, dropBefore+1, relayLogContinuationDrop.Load(), "the lost accounting is counted exactly once")
	require.EqualValues(t, overflowBefore+1, relayLogContinuationOverflow.Load())
	require.Equal(t, errorsBefore, relayLogFallbackErrors.Load(), "the row is stored; only accounting was lost")

	status := GetRelayLogPipelineStatus()
	require.Equal(t, "accepting", status.IntakeState, "exhausted lanes must not stop intake")
	require.EqualValues(t, dropBefore+1, status.ContinuationDropped)
	require.EqualValues(t, overflowBefore+1, status.ContinuationOverflowed)
	backlog := relayLogBacklog()
	require.True(t, enqueueAsyncRelayLog(relayLogKindConsume,
		&Log{RequestId: "after-exhaustion", Type: LogTypeConsume}, nil, nil))
	require.Equal(t, backlog+1, relayLogBacklog(), "the log is buffered, not refused")

	release()
	drainRelayLogAux(t, 30*time.Second)
	require.Empty(t, rec.idsFor(uid), "the dropped accounting never runs")
	require.EqualValues(t, dropBefore+1, relayLogContinuationDrop.Load(), "draining must not count it again")
}

// M5: cancelling a replay mid-file must not replay already committed batches
// again. The source is replaced by exactly the part that was not written.
func TestRelayLogBranchAuditRegression_ReplayCancelledMidFileReinsertsCommittedBatches(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)
			cfg := operation_setting.GetRelayLogPipelineSetting()
			old := *cfg
			t.Cleanup(func() { *cfg = old })
			cfg.InnerBatchSize = 1

			path := filepath.Join(t.TempDir(), "relay-log.jsonl.1")
			writeFallbackRecords(t, path,
				&Log{RequestId: prefix + "a", Type: LogTypeConsume},
				&Log{RequestId: prefix + "b", Type: LogTypeConsume},
			)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			oldWriter := relayLogReplayBatchWriter
			t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })
			calls := 0
			relayLogReplayBatchWriter = func(writeCtx context.Context, logs []*Log, batchSize int) error {
				calls++
				err := insertRelayLogs(LOG_DB.WithContext(writeCtx), logs, batchSize)
				if calls == 1 {
					cancel() // shutdown arrives right after the first batch committed
				}
				return err
			}
			first := replayRelayLogFallback(ctx, path)
			require.ErrorIs(t, first.err, context.Canceled)
			require.EqualValues(t, 1, first.processed)
			require.FileExists(t, path)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NotContains(t, string(data), prefix+"a\"", "the committed line must leave the source")
			require.Contains(t, string(data), prefix+"b\"")
			requireNoReplayArtifacts(t, path)

			relayLogReplayBatchWriter = oldWriter
			second := replayRelayLogFallback(context.Background(), path)
			require.NoError(t, second.err)
			requireOneRowEach(t, db, prefix+"a", prefix+"b")
			require.NoFileExists(t, path)
		})
	}
}

func requireNoReplayArtifacts(t *testing.T, path string) {
	t.Helper()
	require.NoFileExists(t, path+".replay.tmp")
	require.NoFileExists(t, path+".replay.source")
}

// M5: on cancel the new file is: lines retained as failed so far, then the
// untouched remainder, byte for byte. Committed lines are gone.
func TestRelayLogReplayCancelKeepsRetainedLinesAndRemainder(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	cfg.InnerBatchSize = 1

	path := filepath.Join(t.TempDir(), "relay-log.jsonl.1")
	writeFallbackRecords(t, path, &Log{RequestId: "committed", Type: LogTypeConsume})
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	remainder := []byte(`{"version":1,"kind":"relay_log_only","log":{"request_id":"rest-1"}}` + "\n" +
		`{"version":1,"kind":"relay_log_only","log":{"request_id":"rest-2"}}`) // no trailing newline
	bad := []byte("not-json")
	// bad \n committed \n rest-1 \n rest-2 (original already ends with "\n").
	content := append(append(append([]byte{}, bad...), '\n'), original...)
	content = append(content, remainder...)
	require.NoError(t, os.WriteFile(path, content, 0o640))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldWriter := relayLogReplayBatchWriter
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })
	var written []string
	relayLogReplayBatchWriter = func(_ context.Context, logs []*Log, _ int) error {
		for _, log := range logs {
			written = append(written, log.RequestId)
		}
		cancel()
		return nil
	}

	result := replayRelayLogFallback(ctx, path)
	require.ErrorIs(t, result.err, context.Canceled)
	require.Equal(t, []string{"committed"}, written)
	require.EqualValues(t, 1, result.processed)
	require.EqualValues(t, 1, result.failed, "only the unparseable line counts as failed")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	want := append(append(append([]byte{}, bad...), '\n'), remainder...)
	require.Equal(t, string(want), string(got))
	requireNoReplayArtifacts(t, path)
}

// flipContext reports Canceled from the n-th Err() call on, so the test can
// place the cancellation at a deterministic point of the replay loop.
type flipContext struct {
	context.Context
	calls atomic.Int64
	at    int64
}

func (c *flipContext) Err() error {
	if c.calls.Add(1) >= c.at {
		return context.Canceled
	}
	return nil
}

// M5: records already parsed into the pending (unwritten) batch, and a record
// that was only half read when cancellation arrived, are written back intact.
func TestRelayLogReplayCancelKeepsUnwrittenBatchAndPartialRecord(t *testing.T) {
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	oldWriter := relayLogReplayBatchWriter
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })
	var writes atomic.Int64
	relayLogReplayBatchWriter = func(context.Context, []*Log, int) error {
		writes.Add(1)
		return nil
	}

	t.Run("pending batch", func(t *testing.T) {
		cfg.InnerBatchSize = 3
		path := filepath.Join(t.TempDir(), "relay-log.jsonl.1")
		writeFallbackRecords(t, path,
			&Log{RequestId: "p1"}, &Log{RequestId: "p2"}, &Log{RequestId: "p3"}, &Log{RequestId: "p4"})
		original, err := os.ReadFile(path)
		require.NoError(t, err)
		// Err() calls: loop top + one read per short line. The 5th call is the
		// loop top of the third iteration: p1 and p2 sit in the unwritten batch.
		result := replayRelayLogFallback(&flipContext{Context: context.Background(), at: 5}, path)
		require.ErrorIs(t, result.err, context.Canceled)
		require.Zero(t, writes.Load(), "precondition: cancelled before any batch was written")
		require.EqualValues(t, 2, result.failed)
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(original), string(got))
		requireNoReplayArtifacts(t, path)
	})

	t.Run("half-read record", func(t *testing.T) {
		// Batch size 1: had the huge record been read completely it would have
		// been written at once, so writes == 0 proves the cancel hit mid-record.
		cfg.InnerBatchSize = 1
		path := filepath.Join(t.TempDir(), "relay-log.jsonl.1")
		// Larger than the 64KiB reader buffer: read in several fragments.
		writeFallbackRecords(t, path, &Log{RequestId: "huge", Content: strings.Repeat("x", 200*1024)}, &Log{RequestId: "tail"})
		original, err := os.ReadFile(path)
		require.NoError(t, err)
		// 1: loop top, 2: first fragment, 3: second fragment -> cancelled mid-record.
		result := replayRelayLogFallback(&flipContext{Context: context.Background(), at: 3}, path)
		require.ErrorIs(t, result.err, context.Canceled)
		require.Zero(t, writes.Load())
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, len(original), len(got))
		require.True(t, bytes.Equal(original, got), "the half-read record must be written back byte for byte")
		requireNoReplayArtifacts(t, path)
	})
}

// F4: a cancel that arrives before anything in the file was committed or
// retained leaves the source untouched — no copy of the remainder, no two-phase
// replacement (the mtime is pinned in the past to prove the file was not
// rewritten) — and leaves no replay artifacts behind.
func TestRelayLogReplayCancelBeforeAnyCommitLeavesSourceUntouched(t *testing.T) {
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	oldWriter := relayLogReplayBatchWriter
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })
	var writes atomic.Int64
	relayLogReplayBatchWriter = func(context.Context, []*Log, int) error {
		writes.Add(1)
		return nil
	}

	cases := []struct {
		name       string
		batchSize  int
		cancelAt   int64
		logs       []*Log
		wantFailed uint64
	}{
		// 1st Err() call is the loop top: cancelled before the first read.
		{"before first read", 1, 1, []*Log{{RequestId: "u1"}, {RequestId: "u2"}}, 0},
		// Two records parsed into the unwritten batch, none committed.
		{"unwritten batch", 3, 5, []*Log{{RequestId: "u1"}, {RequestId: "u2"}, {RequestId: "u3"}, {RequestId: "u4"}}, 2},
		// Cancelled inside a record larger than the reader buffer.
		{"half-read record", 1, 3, []*Log{{RequestId: "huge", Content: strings.Repeat("x", 200*1024)}, {RequestId: "tail"}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writes.Store(0)
			cfg.InnerBatchSize = tc.batchSize
			path := filepath.Join(t.TempDir(), "relay-log.jsonl.1")
			writeFallbackRecords(t, path, tc.logs...)
			original, err := os.ReadFile(path)
			require.NoError(t, err)
			pinned := time.Now().Add(-time.Hour).Truncate(time.Second)
			require.NoError(t, os.Chtimes(path, pinned, pinned))

			result := replayRelayLogFallback(&flipContext{Context: context.Background(), at: tc.cancelAt}, path)

			require.ErrorIs(t, result.err, context.Canceled)
			require.Zero(t, writes.Load(), "precondition: nothing was committed")
			require.Zero(t, result.processed)
			require.Equal(t, tc.wantFailed, result.failed)
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.True(t, bytes.Equal(original, got), "the source must be byte-identical")
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.True(t, info.ModTime().Equal(pinned), "the source must not be replaced by a rewritten copy")
			requireNoReplayArtifacts(t, path)
		})
	}
}

// A cancelled read hands back the bytes it already consumed.
func TestReadRelayLogJSONLRecordReturnsPartialOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := io.MultiReader(&cancelOnRead{r: strings.NewReader(strings.Repeat("a", 32)), cancel: cancel}, strings.NewReader("tail\n"))
	reader := bufio.NewReaderSize(src, 16)
	record, err := readRelayLogJSONLRecord(ctx, reader)
	require.True(t, errors.Is(err, context.Canceled))
	require.NotEmpty(t, record)
	rest, readErr := io.ReadAll(reader)
	require.NoError(t, readErr)
	require.Equal(t, strings.Repeat("a", 32)+"tail\n", string(record)+string(rest), "no byte may be lost")
}

type cancelOnRead struct {
	r      io.Reader
	cancel context.CancelFunc
}

func (c *cancelOnRead) Read(p []byte) (int, error) {
	c.cancel()
	return c.r.Read(p)
}

// L5: when the continuation lane is saturated after a SUCCESSFUL insert, the
// overflow lane must pass the known logs.id to accounting, and must not park
// the already stored row in the fallback file.
func TestRelayLogBranchAuditRegression_ContinuationOverflowAfterPersistLosesLogID(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	dir := useRelayLogFallbackDir(t)
	rec, _ := saturateRelayLogContinuation(t)

	const uid = 7_777_401
	event := &relayLogEvent{
		Log:        &Log{RequestId: "overflow-id", Type: LogTypeConsume, UserId: uid},
		Accounting: &RelayLogAccountingPayload{Version: 1, UserID: uid, Quota: 1},
	}
	require.True(t, persistRelayLogEvents([]*relayLogEvent{event}))
	require.True(t, waitFor(t, 5*time.Second, func() bool { return len(rec.idsFor(uid)) == 1 }))

	var stored Log
	require.NoError(t, db.Where("request_id = ?", "overflow-id").First(&stored).Error)
	require.Equal(t, []int{stored.Id}, rec.idsFor(uid),
		"the insert succeeded; accounting must receive its logs.id, not 0")
	data, _ := os.ReadFile(filepath.Join(dir, "relay-log.jsonl"))
	require.NotContains(t, string(data), "overflow-id", "a stored row must not be parked for backfill")
	require.Zero(t, relayLogFallbackTotal.Load())
}

// L6: accounting-only events (Log == nil) that overflow the continuation lane
// run their accounting on the fallback worker but write nothing to the JSONL.
func TestRelayLogBranchAuditRegression_AccountingOnlyOverflowWritesLoglessFallbackRecord(t *testing.T) {
	dir := useRelayLogFallbackDir(t)
	rec, _ := saturateRelayLogContinuation(t)
	fallbackBefore := relayLogFallbackTotal.Load()
	errorsBefore := relayLogFallbackErrors.Load()

	const uid = 7_777_501
	DispatchRelayLogAccounting(RelayLogAccountingPayload{Version: 1, UserID: uid, Quota: 1}, 0)
	require.True(t, waitFor(t, 5*time.Second, func() bool { return len(rec.idsFor(uid)) == 1 }),
		"precondition: accounting ran through the fallback lane")

	data, _ := os.ReadFile(filepath.Join(dir, "relay-log.jsonl"))
	var logless int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && !strings.Contains(line, `"log":`) {
			logless++
		}
	}
	require.Zero(t, logless, "fallback JSONL must only contain records that carry a log row")
	require.Equal(t, fallbackBefore, relayLogFallbackTotal.Load(), "fallback_total must count log rows only")
	require.Equal(t, errorsBefore, relayLogFallbackErrors.Load())
}

// L7: the status backlog includes the worker-local pending lanes, matching the
// shutdown's relayLogBacklog().
func TestRelayLogBranchAuditRegression_StatusBacklogOmitsWorkerPending(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	cfg.FlushMaxPerCycle, cfg.FullDrain = 1, false

	for i := 0; i < 3; i++ {
		require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "pending", Type: LogTypeConsume}}))
	}
	require.True(t, enqueueRelayLog(relayLogKindError, &relayLogEvent{Log: &Log{RequestId: "pending-error", Type: LogTypeError}}))
	flushRelayLogs()
	require.Equal(t, 3, relayLogBacklog(), "precondition: two consume and one error event wait in the worker pending lanes")
	status := GetRelayLogPipelineStatus()
	require.Equal(t, 2, status.Consume.Backlog, "status backlog must include events waiting in the worker pending lane")
	require.Equal(t, 1, status.Error.Backlog)

	flushRelayLogs()
	flushRelayLogs()
	flushRelayLogs()
	require.Zero(t, relayLogBacklog())
	status = GetRelayLogPipelineStatus()
	require.Zero(t, status.Consume.Backlog)
	require.Zero(t, status.Error.Backlog)
}
