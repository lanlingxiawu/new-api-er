package model

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ShutdownRelayLogFlush 每次发版都会执行，但它靠 sync.Once 关闭全局通道，
// 在同一个进程里只能跑一次——直接在普通用例里调用会把后面所有用例的管道关掉。
// 因此用子进程跑：父用例负责断言子进程的输出。
const relayLogShutdownChildEnv = "RELAY_LOG_SHUTDOWN_CHILD"

func TestShutdownRelayLogFlushPersistsBufferedLogs(t *testing.T) {
	if os.Getenv(relayLogShutdownChildEnv) == "1" {
		runRelayLogShutdownChild(t)
		return
	}

	dbPath := filepath.Join(t.TempDir(), "shutdown.db")
	cmd := exec.Command(os.Args[0],
		"-test.run", "^TestShutdownRelayLogFlushPersistsBufferedLogs$",
		"-test.v", "-test.timeout", "120s")
	cmd.Env = append(os.Environ(),
		relayLogShutdownChildEnv+"=1",
		"RELAY_LOG_SHUTDOWN_DB="+dbPath,
		// 子进程不需要真实项目库，避免连上共享开发库
		"SQL_DSN=", "LOG_SQL_DSN=")
	output, err := cmd.CombinedOutput()
	t.Logf("子进程输出：\n%s", output)
	require.NoError(t, err, "子进程失败")
	assert.Contains(t, string(output), "SHUTDOWN_CHILD_OK")
}

func runRelayLogShutdownChild(t *testing.T) {
	dbPath := os.Getenv("RELAY_LOG_SHUTDOWN_DB")
	require.NotEmpty(t, dbPath)

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	LOG_DB = db
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false

	fallbackDir := filepath.Join(filepath.Dir(dbPath), "fallback")
	relayLogFallbackDir = fallbackDir

	cfg := operation_setting.GetRelayLogPipelineSetting()
	cfg.Enabled = true
	cfg.ConsumeBufMaxEntries = 10_000
	cfg.FlushIntervalMs = 60_000 // 刷盘间隔调得很长，确保数据是被关停流程写下去的

	var accounted int
	RegisterRelayLogAccountingHandler(func(RelayLogAccountingPayload, int) { accounted++ })

	StartRelayLogFlushLoop()

	const events = 500
	payload := RelayLogAccountingPayload{Version: 1, UserID: 1, Quota: 1}
	for i := 0; i < events; i++ {
		require.True(t, enqueueAsyncRelayLog(relayLogKindConsume,
			&Log{RequestId: "shutdown", ModelName: "shutdown-model", Quota: 1}, &payload, nil))
	}

	// 关停前必须还没落库，否则这个用例证明不了是关停流程写下去的
	var before int64
	require.NoError(t, db.Model(&Log{}).Count(&before).Error)
	require.Zero(t, before, "刷盘间隔 60s，关停前不应该已经落库")

	started := time.Now()
	ShutdownRelayLogFlush(30 * time.Second)
	elapsed := time.Since(started)

	var after int64
	require.NoError(t, db.Model(&Log{}).Count(&after).Error)

	fallbackLines := 0
	if entries, globErr := filepath.Glob(filepath.Join(fallbackDir, "relay-log.jsonl*")); globErr == nil {
		for _, path := range entries {
			data, readErr := os.ReadFile(path)
			if readErr == nil {
				fallbackLines += strings.Count(strings.TrimSpace(string(data)), "\n") + 1
			}
		}
	}

	t.Logf("关停耗时 %s，落库 %d 条，fallback %d 条，记账 %d 次",
		elapsed.Round(time.Millisecond), after, fallbackLines, accounted)

	// 1. 缓冲里的日志不能凭空消失
	require.GreaterOrEqual(t, int(after)+fallbackLines, events,
		"关停丢了日志：落库 %d + fallback %d < 投递 %d", after, fallbackLines, events)
	// 2. 关停必须在预算内返回，不能一直空转到 deadline
	require.Less(t, elapsed, 25*time.Second, "关停没有提前返回，疑似在等永不归零的计数")
	// 3. 关停后不再接收新日志
	require.False(t, enqueueAsyncRelayLog(relayLogKindConsume, &Log{RequestId: "after"}, nil, nil))

	t.Log("SHUTDOWN_CHILD_OK")
}
