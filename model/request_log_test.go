package model

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// request_log.go 现在把列表字段放进程内存索引、正文放本机磁盘，不再走数据库或
// Redis（Redis 只在进程首尾做一次索引快照）。测试必须隔离共享的全局索引与目录。
// ---------------------------------------------------------------------------

// requestLogTestStore 把正文根目录指到 t.TempDir()，并关闭后台清理协程
// （清理由测试显式调用 SweepRequestLogFiles 触发，避免定时器带来的不确定性）。
func requestLogTestStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("REQUEST_LOG_DIR", dir)
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")
	InitRequestLogStore()
	require.True(t, RequestLogStoreReady())
	requestLogResetIndex(t)
	return dir
}

// requestLogResetIndex 清空共享索引与自增序号，前后各一次。
func requestLogResetIndex(t *testing.T) {
	t.Helper()
	reset := func() {
		reqLogMu.Lock()
		reqLogItems = nil
		reqLogMu.Unlock()
		atomic.StoreInt64(&reqLogSeq, 0)
	}
	reset()
	t.Cleanup(reset)
}

// requestLogWithLimits 临时覆盖全局的 min/max 清理阈值。
func requestLogWithLimits(t *testing.T, minC, maxC int) {
	t.Helper()
	prevMin, prevMax := common.RequestLogMinCount, common.RequestLogMaxCount
	common.RequestLogMinCount = minC
	common.RequestLogMaxCount = maxC
	t.Cleanup(func() {
		common.RequestLogMinCount = prevMin
		common.RequestLogMaxCount = prevMax
	})
}

func requestLogIndexLen() int {
	reqLogMu.Lock()
	defer reqLogMu.Unlock()
	return len(reqLogItems)
}

// countRequestLogFiles 统计根目录下所有 .json 正文文件。
func countRequestLogFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(info.Name()) == ".json" {
			count++
		}
		return nil
	})
	require.NoError(t, err)
	return count
}

// ---------------------------------------------------------------------------
// 纯逻辑：effectiveRequestLogLimits 归一化
// ---------------------------------------------------------------------------

func TestEffectiveRequestLogLimits(t *testing.T) {
	cases := []struct {
		name             string
		minC, maxC       int
		wantMax, wantMin int
	}{
		{"normal", 1000, 5000, 5000, 1000},
		{"max zero -> default", 0, 0, defaultRequestLogMaxCount, 0},
		{"max negative -> default", 10, -5, defaultRequestLogMaxCount, 10},
		{"min negative -> 0", -100, 200, 200, 0},
		{"min == max", 200, 200, 200, 100},
		{"min > max", 400, 200, 200, 100},
		{"min just below max", 199, 200, 200, 199},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			requestLogWithLimits(t, c.minC, c.maxC)
			gotMax, gotMin := effectiveRequestLogLimits()
			assert.Equal(t, c.wantMax, gotMax, "maxCount")
			assert.Equal(t, c.wantMin, gotMin, "minCount")
			// 函数保证的不变量：0 <= min < max
			assert.GreaterOrEqual(t, gotMin, 0)
			assert.Less(t, gotMin, gotMax)
		})
	}
}

// ---------------------------------------------------------------------------
// 纯逻辑：matchRequestLog 过滤矩阵
// ---------------------------------------------------------------------------

func TestMatchRequestLog(t *testing.T) {
	base := &RequestLog{
		Username:   "alice",
		ModelName:  "gpt-4o",
		ChannelId:  7,
		RequestId:  "req-1",
		StatusCode: 200,
		CreatedAt:  1000,
	}
	assert.True(t, matchRequestLog(base, "", "", 0, "", 0, 0, 0))
	assert.True(t, matchRequestLog(base, "alice", "gpt-4o", 7, "req-1", 200, 500, 1500))
	// 每个条件独立否决（条件覆盖）
	assert.False(t, matchRequestLog(base, "bob", "", 0, "", 0, 0, 0))
	assert.False(t, matchRequestLog(base, "", "gpt-3", 0, "", 0, 0, 0))
	assert.False(t, matchRequestLog(base, "", "", 8, "", 0, 0, 0))
	assert.False(t, matchRequestLog(base, "", "", 0, "req-2", 0, 0, 0))
	assert.False(t, matchRequestLog(base, "", "", 0, "", 500, 0, 0))
	// 时间边界：created_at = 1000
	assert.False(t, matchRequestLog(base, "", "", 0, "", 0, 1001, 0))
	assert.True(t, matchRequestLog(base, "", "", 0, "", 0, 1000, 0))
	assert.False(t, matchRequestLog(base, "", "", 0, "", 0, 0, 999))
	assert.True(t, matchRequestLog(base, "", "", 0, "", 0, 0, 1000))
}

func TestCloneRequestLogMeta(t *testing.T) {
	log := &RequestLog{
		Id:              5,
		Username:        "u",
		UseTimeMs:       42,
		RequestHeaders:  "h",
		RequestBody:     "b",
		ResponseHeaders: "rh",
		ResponseBody:    "rb",
	}
	m := cloneRequestLogMeta(log)
	assert.Equal(t, 5, m.Id)
	assert.Equal(t, "u", m.Username)
	assert.EqualValues(t, 42, m.UseTimeMs)
	// 元数据副本剥掉大字段
	assert.Empty(t, m.RequestHeaders)
	assert.Empty(t, m.RequestBody)
	assert.Empty(t, m.ResponseHeaders)
	assert.Empty(t, m.ResponseBody)
	// 原对象不受影响
	assert.Equal(t, "h", log.RequestHeaders)
}

// ---------------------------------------------------------------------------
// 写入：先落盘、成功后才进索引
// ---------------------------------------------------------------------------

func TestRecordRequestLog_WritesFileThenIndexes(t *testing.T) {
	root := requestLogTestStore(t)
	requestLogWithLimits(t, 1000, 5000)

	// nil 直接返回
	RecordRequestLog(nil)
	assert.Zero(t, requestLogIndexLen())

	log := &RequestLog{
		Username:     "alice",
		ModelName:    "gpt-4o",
		ChannelId:    3,
		RequestId:    "rid-1",
		StatusCode:   200,
		UseTimeMs:    123,
		CreatedAt:    1000,
		RequestBody:  "reqbody",
		ResponseBody: "respbody",
	}
	RecordRequestLog(log)

	assert.NotZero(t, log.Id)
	assert.Equal(t, 1, requestLogIndexLen())
	assert.Equal(t, 1, countRequestLogFiles(t, root))

	// 索引里只有元数据
	list, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].RequestBody)
	assert.EqualValues(t, 123, list[0].UseTimeMs)

	// 详情从磁盘读回完整正文
	detail, err := GetRequestLogById(log.Id)
	require.NoError(t, err)
	assert.Equal(t, "reqbody", detail.RequestBody)
	assert.Equal(t, "respbody", detail.ResponseBody)
	assert.EqualValues(t, 123, detail.UseTimeMs)
}

func TestRecordRequestLog_FillsCreatedAt(t *testing.T) {
	requestLogTestStore(t)
	RecordRequestLog(&RequestLog{Username: "u"})
	list, _, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.NotZero(t, list[0].CreatedAt)
}

// 写盘失败必须不留索引：否则详情点开是空的，比没有日志更误导。
func TestRecordRequestLog_WriteFailureLeavesNoIndex(t *testing.T) {
	requestLogTestStore(t)
	// 把根目录换成普通文件，MkdirAll 必然失败
	require.NoError(t, os.RemoveAll(requestLogRoot))
	require.NoError(t, os.WriteFile(requestLogRoot, []byte("x"), 0o644))

	RecordRequestLog(&RequestLog{Username: "u", CreatedAt: 1000, RequestId: "rid"})
	assert.Zero(t, requestLogIndexLen())
}

// 存储不可用时整条丢弃。
func TestRecordRequestLog_StoreNotReady(t *testing.T) {
	requestLogResetIndex(t)
	prev := requestLogReady
	requestLogReady = false
	t.Cleanup(func() { requestLogReady = prev })

	RecordRequestLog(&RequestLog{Username: "u", CreatedAt: 1000})
	assert.Zero(t, requestLogIndexLen())
}

// ---------------------------------------------------------------------------
// 索引淘汰边界
// ---------------------------------------------------------------------------

func TestRequestLogIndexTrimBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		minC, maxC int
		insert     int
		wantIndex  int
	}{
		{"below max", 2, 5, 4, 4},
		{"at max", 2, 5, 5, 5},
		// 只有超过 max 才触发截断，且结果恰好是 min
		{"above max", 2, 5, 6, 2},
		{"min zero clears", 0, 3, 4, 0},
		// 错配同样必须发生截断（退化为 max/2）
		{"min equals max", 4, 4, 5, 2},
		{"min greater than max", 9, 4, 5, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := requestLogTestStore(t)
			requestLogWithLimits(t, c.minC, c.maxC)
			for i := 0; i < c.insert; i++ {
				RecordRequestLog(&RequestLog{Username: "u", CreatedAt: int64(1000 + i), RequestId: "rid-" + strconv.Itoa(i)})
			}
			assert.Equal(t, c.wantIndex, requestLogIndexLen(), "index size")
			// 淘汰只动索引，磁盘文件一个都不能少
			assert.Equal(t, c.insert, countRequestLogFiles(t, root), "files must survive index eviction")
		})
	}
}

func TestRequestLogIndexTrimKeepsNewest(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 2, 4)
	for i := 0; i < 5; i++ {
		RecordRequestLog(&RequestLog{Username: "u", CreatedAt: int64(1000 + i), RequestId: "rid-" + strconv.Itoa(i)})
	}
	list, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, list, 2)
	// 最新在前
	assert.EqualValues(t, 1004, list[0].CreatedAt)
	assert.EqualValues(t, 1003, list[1].CreatedAt)
}

// ---------------------------------------------------------------------------
// 查询：排序、过滤、分页
// ---------------------------------------------------------------------------

func TestGetAllRequestLogs_OrderFilterPaging(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)

	for i := 0; i < 5; i++ {
		RecordRequestLog(&RequestLog{
			Username:   "alice",
			ModelName:  "gpt-4o",
			ChannelId:  1,
			StatusCode: 200,
			CreatedAt:  int64(1000 + i),
			RequestId:  "rid-" + strconv.Itoa(i),
		})
	}
	RecordRequestLog(&RequestLog{Username: "bob", ModelName: "claude", ChannelId: 2, StatusCode: 500, CreatedAt: 2000, RequestId: "rid-bob"})

	// 无过滤：严格倒序
	list, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 6, total)
	require.Len(t, list, 6)
	for i := 1; i < len(list); i++ {
		assert.GreaterOrEqual(t, list[i-1].CreatedAt, list[i].CreatedAt, "must be newest-first")
	}

	// 每个过滤条件
	_, total, _ = GetAllRequestLogs("alice", "", 0, "", 0, 0, 0, 0, 10)
	assert.EqualValues(t, 5, total)
	_, total, _ = GetAllRequestLogs("", "claude", 0, "", 0, 0, 0, 0, 10)
	assert.EqualValues(t, 1, total)
	_, total, _ = GetAllRequestLogs("", "", 2, "", 0, 0, 0, 0, 10)
	assert.EqualValues(t, 1, total)
	_, total, _ = GetAllRequestLogs("", "", 0, "rid-bob", 0, 0, 0, 0, 10)
	assert.EqualValues(t, 1, total)
	_, total, _ = GetAllRequestLogs("", "", 0, "", 500, 0, 0, 0, 10)
	assert.EqualValues(t, 1, total)
	_, total, _ = GetAllRequestLogs("", "", 0, "", 0, 1002, 1003, 0, 10)
	assert.EqualValues(t, 2, total)
	// 组合过滤
	_, total, _ = GetAllRequestLogs("alice", "gpt-4o", 1, "rid-0", 200, 0, 0, 0, 10)
	assert.EqualValues(t, 1, total)
	// 无命中
	list, total, _ = GetAllRequestLogs("nobody", "", 0, "", 0, 0, 0, 0, 10)
	assert.EqualValues(t, 0, total)
	assert.Empty(t, list)

	// 分页边界
	page, total, _ := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 2)
	assert.EqualValues(t, 6, total)
	assert.Len(t, page, 2)
	page, _, _ = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 4, 10) // 跨越末尾
	assert.Len(t, page, 2)
	page, _, _ = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 6, 10) // startIdx == 总数
	assert.Empty(t, page)
	page, _, _ = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 99, 10) // 越界
	assert.Empty(t, page)
	page, _, _ = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 0) // num <= 0
	assert.Empty(t, page)
	page, _, _ = GetAllRequestLogs("", "", 0, "", 0, 0, 0, -1, 10) // 负 startIdx
	assert.Empty(t, page)

	// 中间页：只物化窗口内的条目，total 仍是全量命中数
	page, total, _ = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 2, 2)
	assert.EqualValues(t, 6, total)
	require.Len(t, page, 2)
	assert.Equal(t, "rid-3", page[0].RequestId, "offset 2 must start at the third newest")
	assert.Equal(t, "rid-2", page[1].RequestId)

	// 过滤 + 分页组合：窗口相对的是过滤后的结果集
	page, total, _ = GetAllRequestLogs("alice", "", 0, "", 0, 0, 0, 1, 2)
	assert.EqualValues(t, 5, total)
	require.Len(t, page, 2)
	assert.Equal(t, "rid-3", page[0].RequestId)
	assert.Equal(t, "rid-2", page[1].RequestId)
}

func TestGetAllRequestLogs_EmptyIndex(t *testing.T) {
	requestLogTestStore(t)
	list, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
	assert.Empty(t, list)
}

// ---------------------------------------------------------------------------
// 详情
// ---------------------------------------------------------------------------

func TestGetRequestLogById_NotFound(t *testing.T) {
	requestLogTestStore(t)
	_, err := GetRequestLogById(123456)
	assert.ErrorIs(t, err, errRequestLogNotFound)
}

// 正文文件被外部删除后，详情必须返回"不可用"而不是半条数据。
func TestGetRequestLogById_FileMissing(t *testing.T) {
	root := requestLogTestStore(t)
	log := &RequestLog{Username: "u", CreatedAt: 1000, RequestId: "rid", RequestBody: "b"}
	RecordRequestLog(log)

	require.NoError(t, os.RemoveAll(root))
	_, err := GetRequestLogById(log.Id)
	assert.ErrorIs(t, err, errRequestLogNotFound)
}

// ---------------------------------------------------------------------------
// 按时间删除 / 清空
// ---------------------------------------------------------------------------

func TestDeleteOldRequestLog(t *testing.T) {
	root := requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	for i := 0; i < 5; i++ {
		RecordRequestLog(&RequestLog{Username: "u", CreatedAt: int64(1000 + i), RequestId: "rid-" + strconv.Itoa(i)})
	}

	deleted, err := DeleteOldRequestLog(1003)
	require.NoError(t, err)
	assert.EqualValues(t, 3, deleted)
	assert.Equal(t, 2, requestLogIndexLen())
	// 文件回收交给清理协程，删除操作本身不碰磁盘
	assert.Equal(t, 5, countRequestLogFiles(t, root))

	// 边界：目标时间早于所有条目 -> 0
	deleted, err = DeleteOldRequestLog(1)
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted)
	assert.Equal(t, 2, requestLogIndexLen())
}

// 清空返回的必须是条目数，不是文件/键数——UI 文案是"已清除 N 条请求日志"。
func TestClearAllRequestLogs_ReturnsEntryCount(t *testing.T) {
	root := requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	for i := 0; i < 3; i++ {
		RecordRequestLog(&RequestLog{Username: "u", CreatedAt: int64(1000 + i), RequestId: "rid-" + strconv.Itoa(i)})
	}

	cleared, err := ClearAllRequestLogs()
	require.NoError(t, err)
	assert.EqualValues(t, 3, cleared)
	assert.Zero(t, requestLogIndexLen())
	assert.Equal(t, 3, countRequestLogFiles(t, root), "files are reclaimed by the sweeper, not by clear")

	stats := SweepRequestLogFiles()
	assert.Zero(t, stats.Errors)
	assert.Zero(t, countRequestLogFiles(t, root), "the next periodic sweep reclaims cleared bodies")

	// 空索引再清一次 -> 0
	cleared, err = ClearAllRequestLogs()
	require.NoError(t, err)
	assert.EqualValues(t, 0, cleared)
}

// ---------------------------------------------------------------------------
// 并发
// ---------------------------------------------------------------------------

func TestRecordRequestLog_Concurrent(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 40, 80)

	const goroutines, perGoroutine = 8, 25
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				RecordRequestLog(&RequestLog{
					Username:  "u",
					CreatedAt: int64(1000 + g*perGoroutine + i),
					RequestId: "rid-" + strconv.Itoa(g) + "-" + strconv.Itoa(i),
				})
			}
		}(g)
	}
	// 并发读不得 panic 或读到撕裂的切片
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_, _, _ = GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
		}
	}()
	wg.Wait()
	<-done

	size := requestLogIndexLen()
	assert.LessOrEqual(t, size, 80, "index must respect max")
	assert.GreaterOrEqual(t, size, 40, "index must not drop below min after a trim")
}
