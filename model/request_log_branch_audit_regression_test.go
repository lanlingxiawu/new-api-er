package model

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for request-log defects found by the branch audit
// (docs/design/branch-audit-vs-main.md L8, L9).

// L8: the list contract is "newest first by created_at". With several writers
// a batch dequeued earlier (older created_at) can commit after a newer one;
// the index must still come out ordered by created_at, not by commit order.
func TestRLAuditRegression_ListNotNewestFirstAcrossWriters(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)

	slow := NewRequestLogWriter() // worker A: picked up the older request first
	fast := NewRequestLogWriter() // worker B: picked up the newer request, finished first
	t.Cleanup(slow.Close)
	t.Cleanup(fast.Close)

	older := &RequestLog{Username: "u", RequestId: "older", CreatedAt: 1_000}
	newer := &RequestLog{Username: "u", RequestId: "newer", CreatedAt: 1_001}
	fast.Record([]*RequestLog{newer})
	slow.Record([]*RequestLog{older})

	list, _, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.GreaterOrEqual(t, list[0].CreatedAt, list[1].CreatedAt,
		"list must be newest-first by created_at, got %q(%d) before %q(%d)",
		list[0].RequestId, list[0].CreatedAt, list[1].RequestId, list[1].CreatedAt)
}

// L8: out-of-order entries inside one batch and across batches land at their
// created_at position; equal timestamps keep commit order; eviction then drops
// the oldest by created_at rather than the earliest committed.
func TestRequestLogIndex_InsertKeepsCreatedAtOrder(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 2, 4)

	entry := func(id string, ts int64) requestLogIndexEntry {
		return requestLogIndexEntry{meta: &RequestLog{RequestId: id, CreatedAt: ts}, rel: id}
	}
	appendRequestLogIndexBatch([]requestLogIndexEntry{entry("c", 30), entry("a", 10)})
	appendRequestLogIndexBatch([]requestLogIndexEntry{entry("b1", 20), entry("d", 40), entry("b2", 20)})

	// 5 entries > max 4: trimmed to min 2, keeping the newest by created_at.
	list, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	assert.Equal(t, "d", list[0].RequestId)
	assert.Equal(t, "c", list[1].RequestId)

	_, _ = ClearAllRequestLogs()
	requestLogWithLimits(t, 100, 1000)
	appendRequestLogIndexBatch([]requestLogIndexEntry{entry("x", 5), entry("y1", 7)})
	appendRequestLogIndexBatch([]requestLogIndexEntry{entry("y2", 7), entry("w", 1), entry("z", 9)})
	list, _, err = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	ids := make([]string, 0, len(list))
	for _, l := range list {
		ids = append(ids, l.RequestId)
	}
	// Newest first; y2 committed after y1 with the same timestamp, so it lists first.
	assert.Equal(t, []string{"z", "y2", "y1", "x", "w"}, ids)
}

// L9: requestLogBodyExists must not overflow on offset+length. A snapshot rel
// whose offset is near MaxInt64 used to wrap negative and be kept as existing.
func TestRLAuditRegression_BodyExistsOffsetOverflow(t *testing.T) {
	requestLogTestStore(t)
	seg := time.Now().Format("2006-01-02") + "/seg-000000-x-1.jsonl"
	full := requestLogFullPath(seg)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(`{"id":1}`+"\n"), 0o644))

	rel := requestLogRecordRel(seg, math.MaxInt64, 1)
	_, _, _, ok := parseRequestLogRecordRel(rel)
	require.True(t, ok, "the rel parses as a valid segment record")

	assert.False(t, requestLogBodyExists(rel, nil),
		"a record at offset MaxInt64 cannot exist in a 9-byte segment")

	// The read path rejects it before allocating `length` bytes.
	_, err := readRequestLogRecord(seg, math.MaxInt64, 1)
	assert.Error(t, err)
	_, err = readRequestLogRecord(seg, 0, math.MaxInt32)
	assert.Error(t, err, "an oversized length is rejected against the segment size")

	// Boundaries: the whole record exactly fits; one byte more does not.
	assert.True(t, requestLogBodyExists(requestLogRecordRel(seg, 0, 8), nil))
	assert.True(t, requestLogBodyExists(requestLogRecordRel(seg, 0, 9), nil))
	assert.False(t, requestLogBodyExists(requestLogRecordRel(seg, 0, 10), nil))
	assert.False(t, requestLogBodyExists(requestLogRecordRel(seg, 1, 9), nil))
	got, err := readRequestLogRecord(seg, 0, 8)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Id)
}

func TestRequestLogRecordWithin(t *testing.T) {
	cases := []struct {
		size, offset int64
		length       int
		want         bool
	}{
		{10, 0, 10, true},
		{10, 0, 11, false},
		{10, 9, 1, true},
		{10, 10, 1, false},
		{10, math.MaxInt64, 1, false},
		{math.MaxInt64, math.MaxInt64 - 1, 1, true},
		{-1, 0, 1, false}, // segment missing
		{10, -1, 1, false},
		{10, 0, 0, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, requestLogRecordWithin(tc.size, tc.offset, tc.length),
			"size=%d offset=%d length=%d", tc.size, tc.offset, tc.length)
	}
}
