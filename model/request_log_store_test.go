package model

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 读取：分段记录按偏移读；旧格式整文件条目继续可读（升级前的快照会带回它们）
// ---------------------------------------------------------------------------

func TestReadRequestLogFile_SegmentRecord(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)

	log := &RequestLog{
		CreatedAt:       1000,
		Username:        "alice",
		UseTimeMs:       55,
		RequestHeaders:  `{"Authorization":["Bearer x"]}`,
		RequestBody:     `{"a":1}`,
		ResponseHeaders: `{"Content-Type":["application/json"]}`,
		ResponseBody:    `{"ok":true}`,
	}
	RecordRequestLog(log)
	rel := requestLogIndexSnapshot()[0].rel

	assert.True(t, requestLogFileExists(rel))
	got, err := readRequestLogFile(rel)
	require.NoError(t, err)
	assert.Equal(t, log.Id, got.Id)
	assert.Equal(t, log.Username, got.Username)
	assert.EqualValues(t, 55, got.UseTimeMs)
	assert.Equal(t, log.RequestBody, got.RequestBody)
	assert.Equal(t, log.ResponseBody, got.ResponseBody)
	assert.Equal(t, log.RequestHeaders, got.RequestHeaders)
}

func TestReadRequestLogFile_LegacyFile(t *testing.T) {
	requestLogTestStore(t)
	rel := "2026-01-01/0/1000_rid.json"
	full := filepath.Join(requestLogRoot, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(`{"id":7,"request_body":"legacy"}`), 0o644))

	assert.True(t, requestLogFileExists(rel))
	got, err := readRequestLogFile(rel)
	require.NoError(t, err)
	assert.Equal(t, 7, got.Id)
	assert.Equal(t, "legacy", got.RequestBody)
}

func TestReadRequestLogFile_MissingAndCorrupted(t *testing.T) {
	requestLogTestStore(t)

	_, err := readRequestLogFile("2026-01-01/0/missing.json")
	assert.Error(t, err)
	assert.False(t, requestLogFileExists("2026-01-01/0/missing.json"))

	_, err = readRequestLogFile(requestLogRecordRel("2026-01-01/seg-missing.jsonl", 0, 10))
	assert.Error(t, err)

	rel := "2026-01-01/0/broken.json"
	full := filepath.Join(requestLogRoot, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte("not json"), 0o644))
	_, err = readRequestLogFile(rel)
	assert.Error(t, err)

	// 偏移指向记录中间：读出来不是完整 JSON，必须报错而不是返回半条数据。
	seg := "2026-01-01/seg-broken.jsonl"
	segFull := filepath.Join(requestLogRoot, filepath.FromSlash(seg))
	require.NoError(t, os.WriteFile(segFull, []byte(`{"id":1}`+"\n"+`{"id":2}`+"\n"), 0o644))
	_, err = readRequestLogFile(requestLogRecordRel(seg, 3, 5))
	assert.Error(t, err)
	// 超出文件末尾
	_, err = readRequestLogFile(requestLogRecordRel(seg, 10, 100))
	assert.Error(t, err)
}

func TestRequestLogStore_NotReady(t *testing.T) {
	requestLogTestStore(t)
	prev := requestLogReady
	requestLogReady = false
	t.Cleanup(func() { requestLogReady = prev })

	_, err := readRequestLogFile("a/0/b.json")
	assert.ErrorIs(t, err, errRequestLogStoreUnavailable)
	assert.False(t, requestLogFileExists("a/0/b.json"))
	assert.False(t, requestLogFileExists(requestLogRecordRel("a/seg-b.jsonl", 0, 1)))
}

// 切换根目录时，共享 writer 的活动分段属于旧目录，必须先关掉。
func TestInitRequestLogStore_ClosesDefaultWriterSegment(t *testing.T) {
	requestLogTestStore(t)
	RecordRequestLog(&RequestLog{Username: "u"})
	require.NotNil(t, defaultRequestLogWriter.file)

	InitRequestLogStore()
	assert.Nil(t, defaultRequestLogWriter.file)
}

// ---------------------------------------------------------------------------
// 环境变量：必须在 InitRequestLogStore 里读，且做范围收敛
// ---------------------------------------------------------------------------

func TestInitRequestLogStore_EnvClamping(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REQUEST_LOG_DIR", dir)
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")

	t.Setenv("REQUEST_LOG_WRITERS", "0")
	InitRequestLogStore()
	assert.Equal(t, 1, RequestLogWriterCount(), "0 must clamp to 1")

	t.Setenv("REQUEST_LOG_WRITERS", "-5")
	InitRequestLogStore()
	assert.Equal(t, 1, RequestLogWriterCount())

	t.Setenv("REQUEST_LOG_WRITERS", "999")
	InitRequestLogStore()
	assert.Equal(t, maxRequestLogWriters, RequestLogWriterCount(), "must clamp to the upper bound")

	t.Setenv("REQUEST_LOG_WRITERS", "6")
	t.Setenv("REQUEST_LOG_MAX_INFLIGHT", "0")
	InitRequestLogStore()
	assert.Equal(t, 6, RequestLogWriterCount())
	assert.Equal(t, 1, RequestLogQueueSize(), "queue depth 0 must clamp to 1")

	t.Setenv("REQUEST_LOG_MAX_INFLIGHT", "2500")
	InitRequestLogStore()
	assert.Equal(t, 2500, RequestLogQueueSize())

}

// 清理协程会回收根目录下一切不属于本布局的内容，所以非独占目录必须整体拒绝接管，
// 而不是"照常写入但不清理"（那会让磁盘无界增长）。
func TestInitRequestLogStore_RejectsNonExclusiveRoot(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "someone-elses.log"), []byte("x"), 0o644))
	t.Setenv("REQUEST_LOG_DIR", dir)
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")

	InitRequestLogStore()

	assert.False(t, RequestLogStoreReady(), "a directory holding unrelated files must not be adopted")
	assert.FileExists(t, filepath.Join(dir, "someone-elses.log"), "the unrelated file must be left alone")
}

// 升级路径：老版本没写过标记文件，但目录里只有日期目录，应当照常接管并补上标记。
func TestInitRequestLogStore_AdoptsExistingLayoutAndMarksOwnership(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "2026-08-06", "3"), 0o755))
	t.Setenv("REQUEST_LOG_DIR", dir)
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")

	InitRequestLogStore()

	require.True(t, RequestLogStoreReady())
	assert.FileExists(t, filepath.Join(dir, requestLogOwnerMarker))
}

// 标记文件被清理协程删掉，下次启动就再也无法证明目录独占。
func TestSweep_KeepsOwnerMarker(t *testing.T) {
	dir := requestLogTestStore(t)
	marker := filepath.Join(dir, requestLogOwnerMarker)
	past := time.Now().Add(-72 * time.Hour)
	require.NoError(t, os.Chtimes(marker, past, past))

	SweepRequestLogFiles()

	assert.FileExists(t, marker)
}

func TestInitRequestLogStore_UnavailableRoot(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	// 根目录的父级是一个文件 -> MkdirAll 必然失败
	t.Setenv("REQUEST_LOG_DIR", filepath.Join(blocker, "relay_log"))
	InitRequestLogStore()
	assert.False(t, RequestLogStoreReady())
}

func TestResolveRequestLogRoot_DefaultsToExecutableDir(t *testing.T) {
	t.Setenv("REQUEST_LOG_DIR", "")
	root := resolveRequestLogRoot()
	assert.Equal(t, "relay_log", filepath.Base(root))
	exe, err := os.Executable()
	if err == nil {
		assert.Equal(t, filepath.Dir(exe), filepath.Dir(root))
	}
}
