package model

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 分段追加存储：每个 writer 一个活动分段，一条记录一行 JSON，索引记 分段#偏移#长度。
// ---------------------------------------------------------------------------

func segmentTestWriter(t *testing.T) *RequestLogWriter {
	t.Helper()
	w := NewRequestLogWriter()
	t.Cleanup(w.Close)
	return w
}

// listSegments 返回根目录下全部分段文件的相对路径。
func listSegments(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(requestLogRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && isRequestLogSegmentName(info.Name()) {
			rel, _ := filepath.Rel(requestLogRoot, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	require.NoError(t, err)
	return out
}

func segmentOf(t *testing.T, id int) string {
	t.Helper()
	reqLogMu.Lock()
	defer reqLogMu.Unlock()
	for _, e := range reqLogItems {
		if e.meta.Id == id {
			seg, _, _, ok := parseRequestLogRecordRel(e.rel)
			require.True(t, ok, "new entries must use the segment encoding: %s", e.rel)
			return seg
		}
	}
	t.Fatalf("id %d not indexed", id)
	return ""
}

func TestRequestLogWriter_BatchRoundTrip(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)

	bodies := []string{
		"",
		"line1\nline2\r\n\t\"quoted\"",
		"中文与 emoji 🚀",
		strings.Repeat("x", 100*1024),
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
	}
	logs := make([]*RequestLog, len(bodies))
	for i, b := range bodies {
		logs[i] = &RequestLog{Username: "u", RequestId: "rid-" + strconv.Itoa(i), RequestBody: b, ResponseBody: b + "!"}
	}
	w.Record(logs)

	require.Equal(t, len(bodies), requestLogIndexLen())
	segs := listSegments(t)
	require.Len(t, segs, 1, "one batch goes into the writer's single active segment")
	for i, log := range logs {
		require.NotZero(t, log.Id)
		assert.NotZero(t, log.CreatedAt)
		got, err := GetRequestLogById(log.Id)
		require.NoError(t, err)
		assert.Equal(t, bodies[i], got.RequestBody)
		assert.Equal(t, bodies[i]+"!", got.ResponseBody)
		assert.Equal(t, log.RequestId, got.RequestId)
	}

	// 文件本身是 JSONL：一行一条，可直接 grep request id 排障。
	f, err := os.Open(filepath.Join(requestLogRoot, filepath.FromSlash(segs[0])))
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lines := 0
	for sc.Scan() {
		var rec RequestLog
		require.NoError(t, common.Unmarshal(sc.Bytes(), &rec))
		assert.Equal(t, "rid-"+strconv.Itoa(lines), rec.RequestId)
		lines++
	}
	require.NoError(t, sc.Err())
	assert.Equal(t, len(bodies), lines)
}

func TestRequestLogWriter_SkipsNilAndEmptyBatch(t *testing.T) {
	requestLogTestStore(t)
	w := segmentTestWriter(t)

	w.Record(nil)
	w.Record([]*RequestLog{nil, nil})
	assert.Zero(t, requestLogIndexLen())
	assert.Empty(t, listSegments(t), "no segment is created for an empty batch")
}

// 跨多次写入的偏移必须累加正确。
func TestRequestLogWriter_AppendsAcrossBatches(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)

	var all []*RequestLog
	for b := 0; b < 4; b++ {
		batch := []*RequestLog{
			{Username: "u", RequestId: "a" + strconv.Itoa(b), RequestBody: strings.Repeat("a", b*10)},
			{Username: "u", RequestId: "b" + strconv.Itoa(b), RequestBody: strings.Repeat("b", b*7+1)},
		}
		w.Record(batch)
		all = append(all, batch...)
	}
	assert.Len(t, listSegments(t), 1)
	for _, log := range all {
		got, err := GetRequestLogById(log.Id)
		require.NoError(t, err)
		assert.Equal(t, log.RequestBody, got.RequestBody)
	}
}

// 单批超过刷写阈值时分多次写，每一段的偏移都要对。
func TestRequestLogWriter_LargeBatchFlushesInChunks(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)

	big := strings.Repeat("z", requestLogFlushBytes/2+1)
	logs := []*RequestLog{
		{Username: "u", RequestId: "1", RequestBody: big},
		{Username: "u", RequestId: "2", RequestBody: big},
		{Username: "u", RequestId: "3", RequestBody: big},
	}
	w.Record(logs)
	for _, log := range logs {
		got, err := GetRequestLogById(log.Id)
		require.NoError(t, err)
		assert.Equal(t, big, got.RequestBody)
	}
}

func TestRequestLogWriter_Rotation(t *testing.T) {
	cases := []struct {
		name  string
		force func(w *RequestLogWriter)
	}{
		{"date changes", func(w *RequestLogWriter) { w.date = "2000-01-01" }},
		{"size limit", func(w *RequestLogWriter) { w.size = requestLogSegmentMaxBytes }},
		{"age limit", func(w *RequestLogWriter) { w.opened = time.Now().Add(-requestLogSegmentMaxAge) }},
		{"root changes", func(w *RequestLogWriter) { w.root = w.root + "-old" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requestLogTestStore(t)
			requestLogWithLimits(t, 100, 1000)
			w := segmentTestWriter(t)

			first := &RequestLog{Username: "u", RequestId: "first", RequestBody: "one"}
			w.Record([]*RequestLog{first})
			tc.force(w)
			second := &RequestLog{Username: "u", RequestId: "second", RequestBody: "two"}
			w.Record([]*RequestLog{second})

			assert.NotEqual(t, segmentOf(t, first.Id), segmentOf(t, second.Id))
			assert.Len(t, listSegments(t), 2)
			for _, log := range []*RequestLog{first, second} {
				got, err := GetRequestLogById(log.Id)
				require.NoError(t, err)
				assert.Equal(t, log.RequestBody, got.RequestBody)
			}
		})
	}
}

// 写失败时整批不进索引，且下一批换新分段，不在坏句柄上反复失败。
func TestRequestLogWriter_WriteFailureDropsBatchAndReopens(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)

	w.Record([]*RequestLog{{Username: "u", RequestId: "ok-1"}})
	require.Equal(t, 1, requestLogIndexLen())
	broken := w.rel
	require.NoError(t, w.file.Close()) // 之后的 write 必然失败

	w.Record([]*RequestLog{{Username: "u", RequestId: "lost-1"}, {Username: "u", RequestId: "lost-2"}})
	assert.Equal(t, 1, requestLogIndexLen(), "a failed write must not index any entry of the batch")

	after := &RequestLog{Username: "u", RequestId: "ok-2"}
	w.Record([]*RequestLog{after})
	assert.Equal(t, 2, requestLogIndexLen())
	assert.NotEqual(t, broken, segmentOf(t, after.Id))
	_, err := GetRequestLogById(after.Id)
	require.NoError(t, err)
}

// 目录创建失败（根目录被换成普通文件）时整批丢弃。
func TestRequestLogWriter_OpenFailureDropsBatch(t *testing.T) {
	requestLogTestStore(t)
	w := segmentTestWriter(t)
	require.NoError(t, os.RemoveAll(requestLogRoot))
	require.NoError(t, os.WriteFile(requestLogRoot, []byte("x"), 0o644))
	t.Cleanup(func() { _ = os.Remove(requestLogRoot) })

	w.Record([]*RequestLog{{Username: "u", RequestId: "rid"}})
	assert.Zero(t, requestLogIndexLen())
}

// 日期目录被清理协程删掉后（跨天时可能发生），下一个分段要能重建目录。
func TestRequestLogWriter_RecreatesSweptDateDir(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)

	w.Record([]*RequestLog{{Username: "u", RequestId: "1"}})
	w.Close()
	require.NoError(t, os.RemoveAll(filepath.Join(requestLogRoot, time.Now().Format("2006-01-02"))))

	log := &RequestLog{Username: "u", RequestId: "2", RequestBody: "b"}
	w.Record([]*RequestLog{log})
	got, err := GetRequestLogById(log.Id)
	require.NoError(t, err)
	assert.Equal(t, "b", got.RequestBody)
}

func TestRequestLogWriter_ConcurrentWritersReadsAndSweeps(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 1000, 5000)

	const writers, batches, perBatch = 4, 30, 5
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			w := NewRequestLogWriter()
			defer w.Close()
			for b := 0; b < batches; b++ {
				batch := make([]*RequestLog, perBatch)
				for i := range batch {
					batch[i] = &RequestLog{Username: "u", RequestId: "r" + strconv.Itoa(g*1000+b*perBatch+i), RequestBody: strconv.Itoa(g)}
				}
				w.Record(batch)
			}
		}(g)
	}
	stop := make(chan struct{})
	var bg sync.WaitGroup
	bg.Add(2)
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
	go func() {
		defer bg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				list, _, _ := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 5)
				for _, l := range list {
					_, _ = GetRequestLogById(l.Id)
				}
			}
		}
	}()
	wg.Wait()
	close(stop)
	bg.Wait()
	SweepRequestLogFiles()

	// 与清理并发之后，每条已进索引的日志仍然读得到。
	require.Equal(t, writers*batches*perBatch, requestLogIndexLen())
	for _, e := range requestLogIndexSnapshot() {
		_, err := GetRequestLogById(e.meta.Id)
		require.NoError(t, err, "indexed entry %d lost to a concurrent sweep", e.meta.Id)
	}
}

// ---------------------------------------------------------------------------
// 清理：粒度是分段；活动分段即使还没有索引引用也必须保留。
// ---------------------------------------------------------------------------

func TestSweep_SegmentKeptWhileReferenced(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 1, 2)
	w := segmentTestWriter(t)

	old := &RequestLog{Username: "u", RequestId: "old"}
	w.Record([]*RequestLog{old})
	oldSeg := segmentOf(t, old.Id)
	w.Close()

	w.Record([]*RequestLog{{Username: "u", RequestId: "n1"}, {Username: "u", RequestId: "n2"}})
	w.Record([]*RequestLog{{Username: "u", RequestId: "n3"}}) // 触发淘汰：old 出索引
	w.Close()

	SweepRequestLogFiles()
	assert.NoFileExists(t, requestLogFullPath(oldSeg), "a segment with no indexed entry is reclaimed")
	for _, e := range requestLogIndexSnapshot() {
		_, err := GetRequestLogById(e.meta.Id)
		require.NoError(t, err)
	}
}

func TestSweep_ActiveSegmentKeptWithoutReferences(t *testing.T) {
	requestLogTestStore(t)
	w := segmentTestWriter(t)
	w.Record([]*RequestLog{{Username: "u", RequestId: "1"}})
	active := w.rel
	_, _ = ClearAllRequestLogs()

	SweepRequestLogFiles()
	assert.FileExists(t, requestLogFullPath(active), "the writer's open segment must never be deleted")

	// 关闭后不再受保护，下一轮回收。
	w.Close()
	SweepRequestLogFiles()
	assert.NoFileExists(t, requestLogFullPath(active))
}

// 活动分段所在的旧日期目录不能被整目录删除（写入方长时间空闲时会出现）。
func TestSweep_OldDateDirWithActiveSegmentSurvives(t *testing.T) {
	requestLogTestStore(t)
	oldDay := time.Now().AddDate(0, 0, -30)
	rel, name := registerRequestLogSegment(oldDay)
	t.Cleanup(func() { unregisterRequestLogSegment(rel) })
	full := requestLogFullPath(rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte("{}\n"), 0o644))
	require.True(t, isRequestLogSegmentName(name))

	SweepRequestLogFiles()
	assert.FileExists(t, full)
}

// 快照之后才登记的分段（本进程、序号更大）视为"可能尚未进索引"，必须保留。
func TestKeepRequestLogSegment_CreatedAfterSnapshot(t *testing.T) {
	_, snapSeq := requestLogSegmentSnapshot()
	rel, name := registerRequestLogSegment(time.Now())
	unregisterRequestLogSegment(rel)

	assert.True(t, keepRequestLogSegment(rel, name, nil, snapSeq), "newer than the snapshot")
	assert.False(t, keepRequestLogSegment(rel, name, nil, snapSeq+1), "not newer than the snapshot")
	assert.True(t, keepRequestLogSegment(rel, name, map[string]struct{}{rel: {}}, snapSeq+1), "referenced")

	foreign := "seg-010203-zzzzzz-999999999.jsonl"
	assert.False(t, keepRequestLogSegment("2026-01-01/"+foreign, foreign, nil, 0), "another process's segment is judged only by references")
}

func TestSweep_MalformedSegmentLikeNamesAreOrphans(t *testing.T) {
	requestLogTestStore(t)
	today := time.Now().Format("2006-01-02")
	junk := writeOrphanFile(t, today+"/seg-garbage.jsonl")
	SweepRequestLogFiles()
	assert.NoFileExists(t, junk)
}

// ---------------------------------------------------------------------------
// 升级兼容：旧的 <date>/<bucket>/<file>.json 条目仍可读、可回收。
// ---------------------------------------------------------------------------

func writeLegacyIndexed(t *testing.T, rel string, log *RequestLog) {
	t.Helper()
	data, err := common.Marshal(log)
	require.NoError(t, err)
	full := requestLogFullPath(rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, data, 0o644))
	appendRequestLogIndexBatch([]requestLogIndexEntry{{meta: cloneRequestLogMeta(log), rel: rel}})
}

func TestLegacyEntry_ReadAndSweep(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	today := time.Now().Format("2006-01-02")

	kept := &RequestLog{Id: 900001, Username: "u", CreatedAt: time.Now().Unix(), RequestBody: "legacy-body"}
	writeLegacyIndexed(t, today+"/3/1_kept.json", kept)
	orphan := writeOrphanFile(t, today+"/3/2_orphan.json")

	got, err := GetRequestLogById(kept.Id)
	require.NoError(t, err)
	assert.Equal(t, "legacy-body", got.RequestBody)

	SweepRequestLogFiles()
	assert.FileExists(t, requestLogFullPath(today+"/3/1_kept.json"))
	assert.NoFileExists(t, orphan)
}

// ---------------------------------------------------------------------------
// rel 编码与存在性判断
// ---------------------------------------------------------------------------

func TestParseRequestLogRecordRel(t *testing.T) {
	seg, off, n, ok := parseRequestLogRecordRel(requestLogRecordRel("2026-09-25/seg-a.jsonl", 123, 45))
	require.True(t, ok)
	assert.Equal(t, "2026-09-25/seg-a.jsonl", seg)
	assert.EqualValues(t, 123, off)
	assert.Equal(t, 45, n)

	for _, bad := range []string{
		"2026-09-25/3/1_x.json", // 旧格式
		"",
		"#1#2",
		"a#1",
		"a#x#2",
		"a#1#y",
		"a#-1#2",
		"a#1#0",
		"a#1#-2",
		"a#1#2#3",
	} {
		_, _, _, ok := parseRequestLogRecordRel(bad)
		assert.False(t, ok, bad)
	}
}

func TestRequestLogBodyExists_SegmentBoundsAndMemo(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	w := segmentTestWriter(t)
	logs := []*RequestLog{{Username: "u", RequestId: "1"}, {Username: "u", RequestId: "2"}}
	w.Record(logs)

	cache := map[string]int64{}
	var rels []string
	for _, e := range requestLogIndexSnapshot() {
		rels = append(rels, e.rel)
		assert.True(t, requestLogBodyExists(e.rel, cache))
	}
	assert.Len(t, cache, 1, "two records in one segment cost one stat")

	seg, off, n, _ := parseRequestLogRecordRel(rels[1])
	assert.False(t, requestLogBodyExists(requestLogRecordRel(seg, off, n+1_000_000), nil), "a record past the end of a truncated segment does not exist")
	assert.False(t, requestLogBodyExists(requestLogRecordRel("2000-01-01/seg-missing.jsonl", 0, 1), nil))
}
