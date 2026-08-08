package model

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 磁盘清理：内存索引是唯一真值，没有对应索引的文件即为孤儿。
// 用 os.Chtimes 制造"超过宽限期"，避免测试里睡等。
// ---------------------------------------------------------------------------

func requestLogWithGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	prev := requestLogSweepGrace
	requestLogSweepGrace = grace
	t.Cleanup(func() { requestLogSweepGrace = prev })
}

// writeOrphanFile 在指定相对路径写一个不进索引的文件，并把 mtime 设为 age 之前。
func writeOrphanFile(t *testing.T, rel string, age time.Duration) string {
	t.Helper()
	full := filepath.Join(requestLogRoot, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte("{}"), 0o644))
	if age > 0 {
		past := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(full, past, past))
	}
	return full
}

func dayRel(daysAgo int, bucketSeed int64) (createdAt int64, date string) {
	base := time.Now().AddDate(0, 0, -daysAgo)
	// 对齐到指定桶，方便断言
	ts := base.Unix() - (base.Unix() % 8) + bucketSeed
	return ts, time.Unix(ts, 0).Format("2006-01-02")
}

func TestSweep_KeepsIndexedDeletesStaleOrphan(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	requestLogWithGrace(t, time.Hour)

	log := &RequestLog{Username: "u", CreatedAt: time.Now().Unix(), RequestId: "keep"}
	RecordRequestLog(log)
	indexedRel := requestLogRelPath(log.CreatedAt, "keep", int64(log.Id))

	today := time.Now().Format("2006-01-02")
	stale := writeOrphanFile(t, today+"/0/1_stale.json", 2*time.Hour)
	fresh := writeOrphanFile(t, today+"/0/2_fresh.json", 0)

	stats := SweepRequestLogFiles()

	assert.True(t, requestLogFileExists(indexedRel), "indexed file must survive")
	assert.NoFileExists(t, stale, "orphan past the grace period must be deleted")
	assert.FileExists(t, fresh, "orphan inside the grace period must survive")
	assert.Equal(t, 1, stats.Deleted)
	assert.Zero(t, stats.Errors)
}

// 宽限期是必须的：写盘成功到进索引之间有一个瞬间窗口，没有它并发写入会被误删。
func TestSweep_GraceProtectsFreshFiles(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithGrace(t, time.Hour)

	today := time.Now().Format("2006-01-02")
	justWritten := writeOrphanFile(t, today+"/3/9_inflight.json", 0)

	SweepRequestLogFiles()
	assert.FileExists(t, justWritten)

	// 宽限期归零后同一个文件会被回收
	requestLogSweepGrace = 0
	past := time.Now().Add(-time.Second)
	require.NoError(t, os.Chtimes(justWritten, past, past))
	SweepRequestLogFiles()
	assert.NoFileExists(t, justWritten)
}

func TestSweep_DeletesMalformedNames(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithGrace(t, time.Hour)

	today := time.Now().Format("2006-01-02")
	junkInBucket := writeOrphanFile(t, today+"/0/not-a-log.txt", 2*time.Hour)
	junkInDate := writeOrphanFile(t, today+"/loose.txt", 2*time.Hour)
	junkAtRoot := writeOrphanFile(t, "loose-at-root.txt", 2*time.Hour)

	SweepRequestLogFiles()

	assert.NoFileExists(t, junkInBucket)
	assert.NoFileExists(t, junkInDate)
	assert.NoFileExists(t, junkAtRoot)
}

// 长期停机后重启的关键路径：整目录删除，不逐文件 stat。
// 用"文件 mtime 是当下"来锁住这一点——若走逐文件宽限期判断，这些文件会被保留。
func TestSweep_RemovesOldDateDirWholesale(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithGrace(t, time.Hour)

	oldDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	for i := 0; i < 5; i++ {
		writeOrphanFile(t, oldDate+"/"+strconv.Itoa(i)+"/1_x.json", 0)
	}
	oldDir := filepath.Join(requestLogRoot, oldDate)
	require.DirExists(t, oldDir)

	stats := SweepRequestLogFiles()

	assert.NoDirExists(t, oldDir, "a date directory older than yesterday with no indexed entry is removed wholesale")
	assert.Zero(t, stats.Scanned, "wholesale removal must not stat individual files")
	assert.Equal(t, 1, stats.DirsRemoved)
}

// 老日期目录里仍有索引条目时，必须逐文件判定，不能整体删掉。
func TestSweep_OldDateDirWithIndexedEntryIsScanned(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	requestLogWithGrace(t, time.Hour)

	createdAt, date := dayRel(30, 3)
	log := &RequestLog{Username: "u", CreatedAt: createdAt, RequestId: "keep"}
	RecordRequestLog(log)
	indexedRel := requestLogRelPath(createdAt, "keep", int64(log.Id))

	orphan := writeOrphanFile(t, date+"/5/1_orphan.json", 2*time.Hour)

	SweepRequestLogFiles()

	assert.True(t, requestLogFileExists(indexedRel), "indexed entry keeps its date directory alive")
	assert.NoFileExists(t, orphan)
	assert.DirExists(t, filepath.Join(requestLogRoot, date))
	// 被清空的桶目录随之回收
	assert.NoDirExists(t, filepath.Join(requestLogRoot, date, "5"))
}

func TestSweep_RemovesEmptyBucketAndDateDirs(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithGrace(t, time.Hour)

	// 昨天的空目录（早于今天但不早于 今天-1，因此走不到整目录删除分支）
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	require.NoError(t, os.MkdirAll(filepath.Join(requestLogRoot, yesterday, "2"), 0o755))
	// 今天的空目录必须保留：随时可能有新写入
	today := time.Now().Format("2006-01-02")
	require.NoError(t, os.MkdirAll(filepath.Join(requestLogRoot, today, "4"), 0o755))

	SweepRequestLogFiles()

	assert.NoDirExists(t, filepath.Join(requestLogRoot, yesterday))
	assert.DirExists(t, filepath.Join(requestLogRoot, today, "4"), "today's directories must not be reclaimed")
}

// 单桶文件数超过一批（ReadDir 批大小）时必须全部覆盖。
func TestSweep_HandlesMoreFilesThanOneBatch(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	requestLogWithGrace(t, time.Hour)

	// 让该日期"在用"，从而走逐文件分支而非整目录删除
	createdAt, date := dayRel(30, 1)
	RecordRequestLog(&RequestLog{Username: "u", CreatedAt: createdAt, RequestId: "keep"})

	const orphans = requestLogSweepBatch + 200
	for i := 0; i < orphans; i++ {
		writeOrphanFile(t, date+"/6/"+strconv.Itoa(i)+"_x.json", 2*time.Hour)
	}

	stats := SweepRequestLogFiles()
	assert.Equal(t, orphans, stats.Deleted, "every orphan across all batches must be removed")
	assert.NoDirExists(t, filepath.Join(requestLogRoot, date, "6"))
}

func TestSweep_MissingRootIsNoOp(t *testing.T) {
	requestLogTestStore(t)
	require.NoError(t, os.RemoveAll(requestLogRoot))

	stats := SweepRequestLogFiles()
	assert.Zero(t, stats.Errors)
	assert.Zero(t, stats.Deleted)
}

func TestSweep_StoreNotReadyIsNoOp(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithGrace(t, 0)
	today := time.Now().Format("2006-01-02")
	orphan := writeOrphanFile(t, today+"/0/1_x.json", time.Hour)

	prev := requestLogReady
	requestLogReady = false
	t.Cleanup(func() { requestLogReady = prev })

	stats := SweepRequestLogFiles()
	assert.Zero(t, stats.Deleted)
	assert.FileExists(t, orphan)
}

// 清理与写入并发：不得 panic，也不得删掉刚写进索引的文件。
func TestSweep_ConcurrentWithWrites(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 200, 1000)
	requestLogWithGrace(t, time.Hour)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			RecordRequestLog(&RequestLog{
				Username:  "u",
				CreatedAt: time.Now().Unix(),
				RequestId: "rid-" + strconv.Itoa(i),
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			SweepRequestLogFiles()
		}
	}()
	wg.Wait()

	// 索引里的每一条都必须还能读到正文
	for _, entry := range requestLogIndexSnapshot() {
		assert.True(t, requestLogFileExists(entry.rel), "sweeper must not delete an indexed file: "+entry.rel)
	}
}

func TestIsRequestLogDateDir(t *testing.T) {
	assert.True(t, isRequestLogDateDir("2026-08-07"))
	assert.False(t, isRequestLogDateDir("2026-8-7"))
	assert.False(t, isRequestLogDateDir("2026-13-01"))
	assert.False(t, isRequestLogDateDir("relay_log"))
	assert.False(t, isRequestLogDateDir(""))
	assert.False(t, isRequestLogDateDir(strings.Repeat("1", 10)))
}
