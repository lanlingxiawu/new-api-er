package model

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 路径推导：<date>/<created_at%8>/<created_at>_<request_id>.json
// ---------------------------------------------------------------------------

func TestRequestLogRelPath_BucketAndDate(t *testing.T) {
	// 秒级时间戳逐秒轮转，8 个桶各命中一次
	for ts := int64(0); ts < 8; ts++ {
		rel := requestLogRelPath(ts, "rid", 1)
		parts := strings.Split(rel, "/")
		require.Len(t, parts, 3, "rel must be <date>/<bucket>/<file>")
		assert.Equal(t, time.Unix(ts, 0).Format("2006-01-02"), parts[0])
		assert.Equal(t, strconv.FormatInt(ts, 10), parts[1], "bucket must be created_at%%8")
		assert.Equal(t, strconv.FormatInt(ts, 10)+"_rid.json", parts[2])
	}
	// 非零起点同样按取余落桶
	rel := requestLogRelPath(1000, "rid", 1)
	assert.Equal(t, "0", strings.Split(rel, "/")[1], "1000%%8 == 0")
	rel = requestLogRelPath(1003, "rid", 1)
	assert.Equal(t, "3", strings.Split(rel, "/")[1])
}

// 负时间戳（时钟异常）不得产生 "-3" 这样的目录名。
func TestRequestLogRelPath_NegativeTimestamp(t *testing.T) {
	rel := requestLogRelPath(-3, "rid", 1)
	bucket := strings.Split(rel, "/")[1]
	assert.NotContains(t, bucket, "-")
	n, err := strconv.Atoi(bucket)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, 0)
	assert.Less(t, n, 8)
}

// 跨日的两个时间戳必须落进不同日期目录。
func TestRequestLogRelPath_SplitsByDay(t *testing.T) {
	now := time.Now()
	today := requestLogRelPath(now.Unix(), "rid", 1)
	yesterday := requestLogRelPath(now.AddDate(0, 0, -1).Unix(), "rid", 1)
	assert.NotEqual(t, strings.Split(today, "/")[0], strings.Split(yesterday, "/")[0])
}

// ---------------------------------------------------------------------------
// request id 净化
// ---------------------------------------------------------------------------

func TestSanitizeRequestId(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "20260807abcdef", "20260807abcdef"},
		{"allowed punctuation", "a.b_c-d", "a.b_c-d"},
		{"forward slash", "a/b", "a_b"},
		{"backslash", `a\b`, "a_b"},
		{"traversal", "../../etc/passwd", ".._.._etc_passwd"},
		{"space and colon", "a b:c", "a_b_c"},
		{"unicode", "日志", strings.Repeat("_", len("日志"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeRequestId(c.in, 7)
			assert.Equal(t, c.want, got)
			assert.NotContains(t, got, "/")
			assert.NotContains(t, got, `\`)
		})
	}
}

func TestSanitizeRequestId_Fallbacks(t *testing.T) {
	// 空串、纯点号都不能变成路径元素
	assert.Equal(t, "noreqid-7", sanitizeRequestId("", 7))
	assert.Equal(t, "noreqid-7", sanitizeRequestId(".", 7))
	assert.Equal(t, "noreqid-7", sanitizeRequestId("..", 7))
	assert.Equal(t, "noreqid-7", sanitizeRequestId("...", 7))
}

func TestSanitizeRequestId_LengthCap(t *testing.T) {
	got := sanitizeRequestId(strings.Repeat("a", 500), 7)
	assert.Len(t, got, requestLogIdMaxLen)
	// 恰好等于上限时不截断
	got = sanitizeRequestId(strings.Repeat("b", requestLogIdMaxLen), 7)
	assert.Len(t, got, requestLogIdMaxLen)
	// 截断后若只剩点号仍走兜底
	assert.Equal(t, "noreqid-7", sanitizeRequestId(strings.Repeat(".", requestLogIdMaxLen+10)+"a", 7))
}

// ---------------------------------------------------------------------------
// 读写
// ---------------------------------------------------------------------------

func TestWriteReadRequestLogFile(t *testing.T) {
	requestLogTestStore(t)

	log := &RequestLog{
		Id:              9,
		CreatedAt:       1000,
		Username:        "alice",
		UseTimeMs:       55,
		RequestHeaders:  `{"Authorization":["Bearer x"]}`,
		RequestBody:     `{"a":1}`,
		ResponseHeaders: `{"Content-Type":["application/json"]}`,
		ResponseBody:    `{"ok":true}`,
	}
	rel := requestLogRelPath(log.CreatedAt, "rid-1", int64(log.Id))
	require.NoError(t, writeRequestLogFile(rel, log))

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

func TestReadRequestLogFile_MissingAndCorrupted(t *testing.T) {
	requestLogTestStore(t)

	_, err := readRequestLogFile("2026-01-01/0/missing.json")
	assert.Error(t, err)
	assert.False(t, requestLogFileExists("2026-01-01/0/missing.json"))

	rel := "2026-01-01/0/broken.json"
	full := filepath.Join(requestLogRoot, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte("not json"), 0o644))
	_, err = readRequestLogFile(rel)
	assert.Error(t, err)
}

func TestRequestLogStore_NotReady(t *testing.T) {
	requestLogTestStore(t)
	prev := requestLogReady
	requestLogReady = false
	t.Cleanup(func() { requestLogReady = prev })

	assert.ErrorIs(t, writeRequestLogFile("a/0/b.json", &RequestLog{}), errRequestLogStoreUnavailable)
	_, err := readRequestLogFile("a/0/b.json")
	assert.ErrorIs(t, err, errRequestLogStoreUnavailable)
	assert.False(t, requestLogFileExists("a/0/b.json"))
}

// 清理协程回收目录后，同一个 <date>/<bucket> 的后续写入必须自愈：
// 目录创建结果被记忆，不作废就会永久失败。
func TestWriteRequestLogFile_RecreatesSweptDirectory(t *testing.T) {
	requestLogTestStore(t)

	rel := requestLogRelPath(1000, "rid-1", 1)
	require.NoError(t, writeRequestLogFile(rel, &RequestLog{Id: 1}))

	// 模拟清理协程删掉整个日期目录
	dateDir := filepath.Join(requestLogRoot, strings.Split(rel, "/")[0])
	require.NoError(t, os.RemoveAll(dateDir))

	rel2 := requestLogRelPath(1000, "rid-2", 2)
	require.NoError(t, writeRequestLogFile(rel2, &RequestLog{Id: 2}), "write must recreate the removed directory")
	assert.True(t, requestLogFileExists(rel2))
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

	// 宽限期保护"写盘成功但尚未进索引"的窗口，任何低于下限的配置都会被抬到下限，
	// 否则清理协程会删掉在途正文。
	t.Setenv("REQUEST_LOG_SWEEP_GRACE_SEC", "-1")
	InitRequestLogStore()
	assert.Equal(t, minRequestLogSweepGrace, requestLogSweepGrace, "negative grace must clamp to the floor")

	t.Setenv("REQUEST_LOG_SWEEP_GRACE_SEC", "0")
	InitRequestLogStore()
	assert.Equal(t, minRequestLogSweepGrace, requestLogSweepGrace, "0 must clamp to the floor")

	t.Setenv("REQUEST_LOG_SWEEP_GRACE_SEC", "900")
	InitRequestLogStore()
	assert.Equal(t, 900*time.Second, requestLogSweepGrace, "values above the floor are kept")
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
	requestLogWithGrace(t, time.Hour)
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
