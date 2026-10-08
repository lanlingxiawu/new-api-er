package model

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Branch audit of the request log memory index + segment store: boundaries and
// failure modes the per-file tests do not reach (batch trims across the limit,
// corrupted/truncated segments, trim racing sweep, Windows open-handle removal).

func rlAuditIds(t *testing.T) []int {
	t.Helper()
	var ids []int
	for _, e := range requestLogIndexSnapshot() {
		ids = append(ids, e.meta.Id)
	}
	return ids
}

func rlAuditBatch(prefix string, n int) []*RequestLog {
	out := make([]*RequestLog, n)
	for i := range out {
		out[i] = &RequestLog{Username: "u", RequestId: prefix + strconv.Itoa(i), RequestBody: prefix + "-body-" + strconv.Itoa(i)}
	}
	return out
}

// A batch that lands exactly on max must not trim; one entry past it trims to
// min and keeps the newest min entries.
func TestRLAudit_BatchAppendExactlyAtMaxThenOnePast(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 3, 5)
	w := segmentTestWriter(t)

	w.Record(rlAuditBatch("a", 5))
	assert.Equal(t, []int{1, 2, 3, 4, 5}, rlAuditIds(t), "len == max must not trim")

	w.Record(rlAuditBatch("b", 1))
	assert.Equal(t, []int{4, 5, 6}, rlAuditIds(t), "len == max+1 trims to the newest min")
}

// A single batch larger than max still ends at exactly min newest entries.
func TestRLAudit_SingleBatchLargerThanMaxKeepsNewestMin(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 3, 5)
	w := segmentTestWriter(t)

	w.Record(rlAuditBatch("a", 8))
	assert.Equal(t, []int{6, 7, 8}, rlAuditIds(t))
	for _, id := range []int{6, 7, 8} {
		_, err := GetRequestLogById(id)
		require.NoError(t, err)
	}
}

// Age rotation is ">= MaxAge": one tick below the limit keeps appending to the
// same segment and the records stay readable at their offsets.
func TestRLAudit_AgeRotationJustBelowLimitKeepsSegment(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)

	first := &RequestLog{Username: "u", RequestId: "1", RequestBody: "one"}
	w.Record([]*RequestLog{first})
	w.opened = time.Now().Add(-requestLogSegmentMaxAge + 5*time.Second)
	second := &RequestLog{Username: "u", RequestId: "2", RequestBody: "two"}
	w.Record([]*RequestLog{second})

	assert.Equal(t, segmentOf(t, first.Id), segmentOf(t, second.Id))
	assert.Len(t, listSegments(t), 1)
	for _, l := range []*RequestLog{first, second} {
		got, err := GetRequestLogById(l.Id)
		require.NoError(t, err)
		assert.Equal(t, l.RequestBody, got.RequestBody)
	}
}

// A crash mid-write leaves the last record truncated: only that record becomes
// unavailable; earlier records in the same segment remain readable.
func TestRLAudit_TruncatedSegmentTailLosesOnlyLastRecord(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)
	logs := rlAuditBatch("t", 3)
	w.Record(logs)
	seg := segmentOf(t, logs[2].Id)
	w.Close()

	var lastRel string
	for _, e := range requestLogIndexSnapshot() {
		if e.meta.Id == logs[2].Id {
			lastRel = e.rel
		}
	}
	_, off, n, ok := parseRequestLogRecordRel(lastRel)
	require.True(t, ok)
	require.NoError(t, os.Truncate(requestLogFullPath(seg), off+int64(n/2)))

	for _, l := range logs[:2] {
		got, err := GetRequestLogById(l.Id)
		require.NoError(t, err)
		assert.Equal(t, l.RequestBody, got.RequestBody)
	}
	_, err := GetRequestLogById(logs[2].Id)
	assert.ErrorIs(t, err, errRequestLogNotFound)
	assert.False(t, requestLogBodyExists(lastRel, nil), "restore must drop a record past the truncated end")
}

// Garbage in the middle of a segment (same length, so offsets of neighbours are
// intact) only affects that record.
func TestRLAudit_GarbageRecordIsNotFoundNeighboursReadable(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)
	logs := rlAuditBatch("g", 3)
	w.Record(logs)
	seg := segmentOf(t, logs[1].Id)
	w.Close()

	var midRel string
	for _, e := range requestLogIndexSnapshot() {
		if e.meta.Id == logs[1].Id {
			midRel = e.rel
		}
	}
	_, off, n, _ := parseRequestLogRecordRel(midRel)
	f, err := os.OpenFile(requestLogFullPath(seg), os.O_WRONLY, 0)
	require.NoError(t, err)
	garbage := make([]byte, n)
	for i := range garbage {
		garbage[i] = 0xff
	}
	_, err = f.WriteAt(garbage, off)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = GetRequestLogById(logs[1].Id)
	assert.ErrorIs(t, err, errRequestLogNotFound)
	for _, l := range []*RequestLog{logs[0], logs[2]} {
		got, err := GetRequestLogById(l.Id)
		require.NoError(t, err)
		assert.Equal(t, l.RequestBody, got.RequestBody)
	}
}

// Malformed rel encodings (bad offset, missing segment part, empty) resolve to
// "not found" instead of panicking or reading outside the record.
func TestRLAudit_BadRelEncodingsAreNotFound(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	for i, rel := range []string{"2026-01-01/seg-x.jsonl#abc#1", "#0#1", "2026-01-01/seg-x.jsonl#0#0", "2026-01-01/seg-x.jsonl#-1#5"} {
		appendRequestLogIndexBatch([]requestLogIndexEntry{{meta: &RequestLog{Id: 100 + i}, rel: rel}})
		_, err := GetRequestLogById(100 + i)
		assert.ErrorIs(t, err, errRequestLogNotFound, rel)
	}
	// An entry with an empty rel is indistinguishable from "not indexed".
	appendRequestLogIndexBatch([]requestLogIndexEntry{{meta: &RequestLog{Id: 200}, rel: ""}})
	_, err := GetRequestLogById(200)
	assert.ErrorIs(t, err, errRequestLogNotFound)
}

// Once trimming evicts every entry of a closed segment, the next sweep reclaims
// the whole segment; a segment that still has one referenced record survives.
func TestRLAudit_SweepReclaimsSegmentOnlyWhenAllEntriesTrimmed(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 2, 4)

	wa := NewRequestLogWriter()
	a := rlAuditBatch("a", 2)
	wa.Record(a)
	segA := segmentOf(t, a[0].Id)
	wa.Close()

	wb := NewRequestLogWriter()
	b := rlAuditBatch("b", 3)
	wb.Record(b) // 5 > max(4): trim to the newest 2, both in segment B
	segB := segmentOf(t, b[2].Id)
	wb.Close()
	require.Equal(t, []int{b[1].Id, b[2].Id}, rlAuditIds(t))

	stats := SweepRequestLogFiles()
	assert.Zero(t, stats.Errors)
	_, err := os.Stat(requestLogFullPath(segA))
	assert.True(t, os.IsNotExist(err), "fully evicted segment must be reclaimed")
	_, err = os.Stat(requestLogFullPath(segB))
	assert.NoError(t, err, "segment with a live record must be kept whole")
	got, err := GetRequestLogById(b[1].Id)
	require.NoError(t, err)
	assert.Equal(t, b[1].RequestBody, got.RequestBody)
}

// Segments left by another process (different proc tag) are judged only by
// index references, regardless of how large their sequence number is.
func TestRLAudit_SweepForeignProcessSegments(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	today := time.Now().Format("2006-01-02")
	orphan := today + "/seg-120000-othertag-999999999.jsonl"
	kept := today + "/seg-120000-othertag-999999998.jsonl"
	for _, rel := range []string{orphan, kept} {
		full := requestLogFullPath(rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(`{"id":1}`+"\n"), 0o644))
	}
	appendRequestLogIndexBatch([]requestLogIndexEntry{{meta: &RequestLog{Id: 1}, rel: requestLogRecordRel(kept, 0, 8)}})

	SweepRequestLogFiles()
	_, err := os.Stat(requestLogFullPath(orphan))
	assert.True(t, os.IsNotExist(err), "a foreign-process segment with no reference is an orphan even with a huge seq")
	_, err = os.Stat(requestLogFullPath(kept))
	assert.NoError(t, err)
	_, err = GetRequestLogById(1)
	assert.NoError(t, err)
}

// DeleteOldRequestLog is strictly "created_at < target": the entry at the
// target second is kept.
func TestRLAudit_DeleteOldRequestLogBoundaryIsExclusive(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	for _, ts := range []int64{99, 100, 101} {
		RecordRequestLog(&RequestLog{Username: "u", CreatedAt: ts})
	}
	n, err := DeleteOldRequestLog(100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	list, total, _ := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	assert.EqualValues(t, 2, total)
	require.Len(t, list, 2)
	assert.EqualValues(t, 101, list[0].CreatedAt)
	assert.EqualValues(t, 100, list[1].CreatedAt)
}

// Time filters are inclusive on both ends; entries sharing a timestamp are
// returned latest-recorded first and are split deterministically across pages.
func TestRLAudit_TimeRangeInclusiveAndTiesPageStably(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	for i := 0; i < 5; i++ {
		RecordRequestLog(&RequestLog{Username: "u", CreatedAt: 500, RequestId: "tie-" + strconv.Itoa(i)})
	}
	RecordRequestLog(&RequestLog{Username: "u", CreatedAt: 499, RequestId: "before"})
	RecordRequestLog(&RequestLog{Username: "u", CreatedAt: 501, RequestId: "after"})

	_, total, _ := GetAllRequestLogs("", "", 0, "", 0, 500, 500, 0, 10)
	assert.EqualValues(t, 5, total, "start == end must include the ties at that second")
	_, total, _ = GetAllRequestLogs("", "", 0, "", 0, 499, 501, 0, 10)
	assert.EqualValues(t, 7, total)
	_, total, _ = GetAllRequestLogs("", "", 0, "", 0, 502, 0, 0, 10)
	assert.EqualValues(t, 0, total)
	_, total, _ = GetAllRequestLogs("", "", 0, "", 0, 501, 499, 0, 10)
	assert.EqualValues(t, 0, total, "inverted range matches nothing")

	var seen []string
	for start := 0; start < 5; start += 2 {
		page, total, _ := GetAllRequestLogs("", "", 0, "", 0, 500, 500, start, 2)
		assert.EqualValues(t, 5, total)
		for _, l := range page {
			seen = append(seen, l.RequestId)
		}
	}
	assert.Equal(t, []string{"tie-4", "tie-3", "tie-2", "tie-1", "tie-0"}, seen, "ties: no duplicate/missing entries across pages")
}

// ClearAll: detail is gone immediately; the segment is reclaimed by the next
// sweep once no writer holds it open.
func TestRLAudit_ClearAllThenSweepReclaims(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := NewRequestLogWriter()
	logs := rlAuditBatch("c", 2)
	w.Record(logs)
	seg := segmentOf(t, logs[0].Id)

	n, _ := ClearAllRequestLogs()
	assert.EqualValues(t, 2, n)
	_, err := GetRequestLogById(logs[0].Id)
	assert.ErrorIs(t, err, errRequestLogNotFound)

	SweepRequestLogFiles()
	_, err = os.Stat(requestLogFullPath(seg))
	assert.NoError(t, err, "the active segment is protected even with no references")

	w.Close()
	SweepRequestLogFiles()
	_, err = os.Stat(requestLogFullPath(seg))
	assert.True(t, os.IsNotExist(err))
}

// Windows refuses to delete a file that another handle (a concurrent detail
// read) holds open. The sweep must count it as an error, leave the rest of the
// round intact, and reclaim it on the next round once the handle is closed.
func TestRLAudit_OpenHandleRemovalIsRetriedNextSweep(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := NewRequestLogWriter()
	w.Record(rlAuditBatch("h", 1))
	seg := w.rel
	w.Close()
	_, _ = ClearAllRequestLogs()

	reader, err := os.Open(requestLogFullPath(seg))
	require.NoError(t, err)
	stats := SweepRequestLogFiles()
	_, statErr := os.Stat(requestLogFullPath(seg))
	if runtime.GOOS == "windows" {
		assert.GreaterOrEqual(t, stats.Errors, 1, "Windows: open file cannot be removed")
		assert.NoError(t, statErr, "file survives this round")
	} else {
		assert.True(t, os.IsNotExist(statErr))
	}
	require.NoError(t, reader.Close())

	SweepRequestLogFiles()
	_, statErr = os.Stat(requestLogFullPath(seg))
	assert.True(t, os.IsNotExist(statErr), "reclaimed on the next round")
}

// Trimming and sweeping concurrently with several writers: every entry that is
// still indexed at the end must be readable (index ⊆ disk survives eviction).
func TestRLAudit_ConcurrentTrimAndSweepKeepIndexSubsetOfDisk(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 20, 40)

	const writers, batches, perBatch = 4, 40, 7
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			w := NewRequestLogWriter()
			defer w.Close()
			for b := 0; b < batches; b++ {
				w.Record(rlAuditBatch("w"+strconv.Itoa(g)+"-"+strconv.Itoa(b)+"-", perBatch))
				if b%10 == 9 {
					w.opened = time.Now().Add(-requestLogSegmentMaxAge) // force rotations
				}
			}
		}(g)
	}
	stop := make(chan struct{})
	var bg sync.WaitGroup
	bg.Add(1)
	go func() {
		defer bg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				SweepRequestLogFiles()
			}
		}
	}()
	wg.Wait()
	close(stop)
	bg.Wait()
	SweepRequestLogFiles()

	n := requestLogIndexLen()
	assert.GreaterOrEqual(t, n, 20)
	assert.LessOrEqual(t, n, 40)
	for _, e := range requestLogIndexSnapshot() {
		got, err := GetRequestLogById(e.meta.Id)
		require.NoError(t, err, "indexed entry %d lost to a concurrent trim+sweep", e.meta.Id)
		assert.Equal(t, e.meta.RequestId, got.RequestId)
	}
}

// Restore from a snapshot whose last segment was truncated by a crash: records
// inside the file survive, the truncated one is dropped, entries with nil meta
// or empty rel are skipped, and the id sequence resumes above the kept max.
func TestRLAudit_RestorePartialSegmentAndMalformedEntries(t *testing.T) {
	enableRedis(t)
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	requestLogClearSnapshot(t)

	w := NewRequestLogWriter()
	logs := rlAuditBatch("r", 3)
	w.Record(logs)
	seg := w.rel
	w.Close()
	snap := requestLogIndexSnapshot()
	_, off, n, _ := parseRequestLogRecordRel(snap[2].rel)
	require.NoError(t, os.Truncate(requestLogFullPath(seg), off+int64(n)-1))

	entries := []requestLogSnapshotEntry{
		{Meta: nil, Rel: snap[0].rel},
		{Meta: &RequestLog{Id: 999}, Rel: ""},
	}
	for _, e := range snap {
		entries = append(entries, requestLogSnapshotEntry{Meta: e.meta, Rel: e.rel})
	}
	data, err := common.Marshal(entries)
	require.NoError(t, err)
	require.NoError(t, common.RequestLogRDB.Set(context.Background(), requestLogSnapshotKey, string(data), requestLogSnapshotTTL).Err())

	requestLogResetIndex(t)
	RestoreRequestLogs()
	assert.Equal(t, []int{logs[0].Id, logs[1].Id}, rlAuditIds(t))

	fresh := &RequestLog{Username: "u", RequestId: "fresh"}
	RecordRequestLog(fresh)
	assert.Equal(t, logs[1].Id+1, fresh.Id, "sequence resumes above the max kept id")
}
