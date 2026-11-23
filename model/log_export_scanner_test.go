package model

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

type exportRecoveryPosition struct {
	at int64
	id int
}

type exportRecoveryAttempt struct {
	start, end int64
	cursor     any
}

// This reader exercises the scanner contract without mocking GORM. Integration
// tests separately execute the real readers on PostgreSQL and MySQL.
type exportRecoveryReader struct {
	reverse        bool
	positions      []exportRecoveryPosition
	attempts       []exportRecoveryAttempt
	failAt         map[int]error
	failAlways     error
	validateAt     map[int]error
	validations    int
	cancelOnRead   context.CancelFunc
	onRead         func()
	hideEveryThird bool
}

func (r *exportRecoveryReader) Header() []string    { return []string{"id"} }
func (r *exportRecoveryReader) BatchSize(n int) int { return n }
func (r *exportRecoveryReader) Reverse() bool       { return r.reverse }
func (r *exportRecoveryReader) Validate(context.Context, bool) error {
	r.validations++
	return r.validateAt[r.validations]
}

func (r *exportRecoveryReader) Read(_ context.Context, start, end int64, cursor any, limit int) (LogExportBatch, error) {
	r.attempts = append(r.attempts, exportRecoveryAttempt{start: start, end: end, cursor: cursor})
	if r.onRead != nil {
		r.onRead()
	}
	if r.cancelOnRead != nil {
		r.cancelOnRead()
	}
	err := r.failAt[len(r.attempts)]
	if r.failAlways != nil {
		err = r.failAlways
	}
	if err != nil {
		// Drivers may return rows together with an error. None may escape a retry.
		return LogExportBatch{Scanned: 17, Cursor: "invalid partial cursor", Position: end,
			Rows: []LogExportRow{{At: end, Values: []string{"partial must be discarded"}}}}, err
	}
	batch := LogExportBatch{Position: end}
	last, hasCursor := cursor.(exportRecoveryPosition)
	for _, row := range r.positions {
		if row.at < start || row.at > end {
			continue
		}
		if hasCursor {
			if !r.reverse && (row.at < last.at || row.at == last.at && row.id <= last.id) {
				continue
			}
			if r.reverse && (row.at > last.at || row.at == last.at && row.id >= last.id) {
				continue
			}
		}
		batch.Scanned++
		batch.Cursor, batch.Position = row, row.at
		if !r.hideEveryThird || row.id%3 != 0 {
			batch.Rows = append(batch.Rows, LogExportRow{At: row.at, Values: []string{strconv.Itoa(row.id)}})
		}
		if batch.Scanned == limit {
			break
		}
	}
	batch.Done = batch.Scanned < limit
	return batch, nil
}

func TestLogExportScannerResetsQueryFeedbackBetweenScopes(t *testing.T) {
	useUserCacheMiniRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSize, s.WindowSec, s.BatchSleepMs = 3, 60, 100
	})
	const base int64 = 1_000_000
	job := newExportJobFor("", base, base+59, nil)
	scanner := newLogExportScanner(job, nil, false, nil)
	scanner.gate, _ = fakeGate(0, false, time.Now())
	for scope := range 2 {
		scanner.gate.Observe(exportQuerySample{cost: time.Nanosecond, windowSec: 60, batchSize: 3, rowsRead: 3})
		scanner.gate.penalty = 300 * time.Millisecond
		before := scanner.gate.ThrottledMs()
		firstRead := true
		scanner.reader = &exportRecoveryReader{
			hideEveryThird: true,
			positions:      []exportRecoveryPosition{{base, 1}, {base, 2}, {base, 3}},
			onRead: func() {
				if firstRead {
					require.Equal(t, [3]exportQueryBaseline{}, scanner.gate.queryBaselines)
					firstRead = false
				}
			},
		}
		var ids []string
		err := scanner.runBatches(context.Background(), func(batch LogExportBatch) error {
			for _, row := range batch.Rows {
				ids = append(ids, row.Values[0])
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, []string{"1", "2"}, ids)
		require.EqualValues(t, (scope+1)*3, job.ScannedRows)
		require.Equal(t, 300*time.Millisecond, scanner.gate.penalty, "each scope must establish fresh successful-read baselines")
		require.Greater(t, scanner.gate.ThrottledMs(), before, "scope changes must not reset accumulated waits")
	}
}

func TestLogExportScannerRecoversWithoutMissingOrRepeatingRows(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, scenario := range []struct {
			name          string
			failures      []int
			lateFirstPage bool
			shortTail     bool
			hideRows      bool
		}{
			{name: "first query", failures: []int{1}},
			{name: "same second pagination", failures: []int{2}},
			{name: "past the original midpoint", failures: []int{2}, lateFirstPage: true},
			{name: "unread tail below floor", failures: []int{2}, shortTail: true},
			{name: "repeated timeouts", failures: []int{1, 2, 3, 4}},
			{name: "candidate cursor differs from output", failures: []int{2}, hideRows: true},
		} {
			t.Run(fmt.Sprintf("reverse=%t/%s", reverse, scenario.name), func(t *testing.T) {
				useUserCacheMiniRedis(t)
				fastExportSettings(t, func(s *operation_setting.LogExportSetting) { s.BatchSize = 3 })
				const base int64 = 1_000_000
				r := &exportRecoveryReader{reverse: reverse, failAt: make(map[int]error), hideEveryThird: scenario.hideRows}
				for _, call := range scenario.failures {
					r.failAt[call] = fmt.Errorf("query interrupted: %w", context.DeadlineExceeded)
				}
				offsets := []int64{0, 59, 60, 61, 119, 120, 1799, 1800, 1801, 3599, 3600, 4000}
				if scenario.lateFirstPage {
					offsets = []int64{3000, 3001, 3299, 3300, 3599, 3600, 4000}
					if reverse {
						offsets = []int64{0, 59, 60, 299, 300, 301}
					}
				}
				if scenario.shortTail {
					offsets = []int64{3590, 3599}
					if reverse {
						offsets = []int64{0, 9}
					}
				}
				for _, offset := range offsets {
					for range 5 {
						r.positions = append(r.positions, exportRecoveryPosition{at: base + offset, id: len(r.positions) + 1})
					}
				}
				if reverse {
					slices.Reverse(r.positions)
				}
				end := base + 4000
				if reverse && scenario.lateFirstPage || scenario.shortTail {
					end = base + 3599
				}
				job := newExportJobFor("", base, end, nil)
				scanner := newLogExportScanner(job, nil, false, nil)
				scanner.reader = r
				var got []string
				previousProgress := 0
				err := scanner.runBatches(context.Background(), func(batch LogExportBatch) error {
					require.GreaterOrEqual(t, job.Progress, previousProgress)
					previousProgress = job.Progress
					for _, row := range batch.Rows {
						got = append(got, row.Values[0])
					}
					return nil
				})
				require.NoError(t, err)
				var want []string
				for _, row := range r.positions {
					if !scenario.hideRows || row.id%3 != 0 {
						want = append(want, strconv.Itoa(row.id))
					}
				}
				require.Equal(t, want, got)
				require.EqualValues(t, len(r.positions), job.ScannedRows)
				require.Equal(t, 99, job.Progress)
				for _, failed := range scenario.failures {
					require.Greater(t, len(r.attempts), failed)
					before, after := r.attempts[failed-1], r.attempts[failed]
					require.Less(t, after.end-after.start, before.end-before.start)
					require.Equal(t, before.cursor, after.cursor, "only the last successful cursor is reusable")
					for _, later := range r.attempts[failed:] {
						require.LessOrEqual(t, later.end-later.start, max(after.end-after.start, int64(operation_setting.MinLogExportWindowSec)-1), "later windows must respect the learned ceiling")
					}
				}
			})
		}
	}
}

func TestLogExportScannerRecoveryHonorsConfigurationAndTotalDeadline(t *testing.T) {
	useUserCacheMiniRedis(t)
	fastExportSettings(t, nil)
	r := &exportRecoveryReader{failAt: map[int]error{1: context.DeadlineExceeded}}
	job := newExportJobFor("", 1000, 20000, nil)
	scanner := newLogExportScanner(job, nil, false, nil)
	scanner.reader = r
	err := scanner.runBatches(context.Background(), func(LogExportBatch) error {
		operation_setting.GetLogExportSetting().WindowSec = 86400
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, len(r.attempts), 2)
	for _, attempt := range r.attempts[1:] {
		require.LessOrEqual(t, attempt.end-attempt.start+1, int64(1800))
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancel()
	r.attempts = nil
	err = scanner.runBatches(ctx, func(LogExportBatch) error { return nil })
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, r.attempts, "expired total budget must not start another query")
}

func TestLogExportScannerTimeoutRecoveryIsBounded(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, span := range []int64{1, 59, 60, 61, 3600, 86400} {
			t.Run(fmt.Sprintf("reverse=%t/span=%d", reverse, span), func(t *testing.T) {
				useUserCacheMiniRedis(t)
				fastExportSettings(t, func(s *operation_setting.LogExportSetting) { s.WindowSec = 86400 })
				r := &exportRecoveryReader{reverse: reverse, failAlways: context.DeadlineExceeded}
				job := newExportJobFor("", 1000, 1000+span-1, nil)
				scanner := newLogExportScanner(job, nil, false, nil)
				scanner.reader = r
				calls := 0
				err := scanner.runBatches(context.Background(), func(LogExportBatch) error { calls++; return nil })
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Zero(t, calls)
				require.Zero(t, job.ScannedRows)
				require.LessOrEqual(t, len(r.attempts), 12)
				last := r.attempts[len(r.attempts)-1]
				require.Equal(t, min(span, int64(operation_setting.MinLogExportWindowSec)), last.end-last.start+1)
				for i := 1; i < len(r.attempts); i++ {
					require.Less(t, r.attempts[i].end-r.attempts[i].start, r.attempts[i-1].end-r.attempts[i-1].start)
				}
			})
		}
	}
}

func TestLogExportScannerDoesNotRetryNonReadTimeouts(t *testing.T) {
	ordinary := errors.New("database unavailable")
	for _, tc := range []struct {
		name        string
		readErr     error
		validateAt  map[int]error
		cancelRead  bool
		callbackErr error
		want        error
		reads       int
	}{
		{name: "ordinary query error", readErr: ordinary, want: ordinary, reads: 1},
		{name: "query canceled", readErr: context.Canceled, want: context.Canceled, reads: 1},
		{name: "reader access changed", readErr: ErrLogExportAccessChanged, want: ErrLogExportAccessChanged, reads: 1},
		{name: "reader risk revision changed", readErr: ErrLogExportRiskChanged, want: ErrLogExportRiskChanged, reads: 1},
		{name: "validation timeout", validateAt: map[int]error{1: context.DeadlineExceeded}, want: context.DeadlineExceeded},
		{name: "access revoked before retry", readErr: context.DeadlineExceeded, validateAt: map[int]error{2: ErrLogExportAccessChanged}, want: ErrLogExportAccessChanged, reads: 1},
		{name: "parent canceled during read", readErr: context.DeadlineExceeded, cancelRead: true, want: context.Canceled, reads: 1},
		{name: "file callback timeout", callbackErr: context.DeadlineExceeded, want: context.DeadlineExceeded, reads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useUserCacheMiniRedis(t)
			fastExportSettings(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &exportRecoveryReader{failAlways: tc.readErr, validateAt: tc.validateAt}
			if tc.cancelRead {
				r.cancelOnRead = cancel
			}
			job := newExportJobFor("", 1000, 10000, nil)
			scanner := newLogExportScanner(job, nil, false, nil)
			scanner.reader = r
			err := scanner.runBatches(ctx, func(LogExportBatch) error { return tc.callbackErr })
			require.ErrorIs(t, err, tc.want)
			require.Len(t, r.attempts, tc.reads)
		})
	}
}
