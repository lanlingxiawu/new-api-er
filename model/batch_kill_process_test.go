package model

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 生产用 kill -9 停进程。SIGKILL 不能被捕获，main 里只监听 SIGINT/SIGTERM 的
// 关停刷盘（FlushBatchUpdate、ShutdownRelayLogFlush）不会执行。
// 子进程里放着尚未落库的批量额度差额和一条消费日志，父进程用 Process.Kill
// （Windows 的 TerminateProcess，与 Linux 的 kill -9 一样不可捕获）结束它。
const kill9ChildEnv = "BATCH_KILL9_CHILD"

func TestKill9DropsUnflushedBatchAndLogs(t *testing.T) {
	if os.Getenv(kill9ChildEnv) == "1" {
		runKill9Child(t)
		return
	}

	dbPath := filepath.Join(t.TempDir(), "kill9.db")
	cmd := exec.Command(os.Args[0],
		"-test.run", "^TestKill9DropsUnflushedBatchAndLogs$",
		"-test.v", "-test.timeout", "60s")
	cmd.Env = append(os.Environ(),
		kill9ChildEnv+"=1",
		"BATCH_KILL9_DB="+dbPath,
		"SQL_DSN=", "LOG_SQL_DSN=")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = cmd.Stdout
	require.NoError(t, cmd.Start())

	ready := make(chan struct{})
	outputDone := make(chan struct{})
	var output []byte
	go func() {
		defer close(outputDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			output = append(output, line...)
			output = append(output, '\n')
			if strings.Contains(string(line), "KILL9_READY") {
				close(ready)
			}
		}
	}()

	select {
	case <-ready:
	case <-outputDone:
		t.Fatalf("child exited before ready:\n%s", output)
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child did not become ready")
	}

	require.NoError(t, cmd.Process.Kill())
	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		t.Fatal("child still running after Kill")
	}
	<-outputDone
	t.Logf("child output:\n%s", output)
	require.NotContains(t, string(output), "KILL9_FLUSHED", "kill -9 must not enter the shutdown flush")

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var user User
	require.NoError(t, db.Where("username = ?", "kill9-user").First(&user).Error)
	require.Equal(t, 1000, user.Quota, "in-memory batch quota delta must not reach the database")
	require.Equal(t, 0, user.UsedQuota)
	require.Equal(t, 0, user.RequestCount)

	var logs int64
	require.NoError(t, db.Model(&Log{}).Where("request_id = ?", "kill9-request").Count(&logs).Error)
	require.Zero(t, logs, "consume log still in the memory buffer must not reach the database")
}

func runKill9Child(t *testing.T) {
	dbPath := os.Getenv("BATCH_KILL9_DB")
	require.NotEmpty(t, dbPath)

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Log{}))
	DB = db
	LOG_DB = db
	common.BatchUpdateEnabled = true
	common.LogConsumeEnabled = true
	common.RedisEnabled = false
	common.DataExportEnabled = false

	user := &User{Username: "kill9-user", Password: "kill9-password", Quota: 1000, AffCode: "kill9aff"}
	require.NoError(t, db.Create(user).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	_, err = sqlDB.Exec("PRAGMA wal_checkpoint(FULL)")
	require.NoError(t, err)

	// 与生产一致：差额只进内存，周期 5 秒才写库。这里不启动周期任务，
	// 这样唯一的落库路径就是下面的信号处理器。
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -100)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 100)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	quota, ok := storeValue(BatchUpdateTypeUserQuota, user.Id)
	require.True(t, ok)
	require.Equal(t, -100, quota)

	cfg := operation_setting.GetRelayLogPipelineSetting()
	cfg.Enabled = true
	cfg.ConsumeBufMaxEntries = 10_000
	cfg.FlushIntervalMs = 60_000
	relayLogFallbackDir = filepath.Join(filepath.Dir(dbPath), "fallback")
	StartRelayLogFlushLoop()
	require.True(t, enqueueAsyncRelayLog(relayLogKindConsume, &Log{
		UserId: user.Id, Username: user.Username, Type: LogTypeConsume,
		RequestId: "kill9-request", ModelName: "kill9-model", Quota: 100,
	}, nil, nil))
	var before int64
	require.NoError(t, db.Model(&Log{}).Where("request_id = ?", "kill9-request").Count(&before).Error)
	require.Zero(t, before)

	fmt.Println("KILL9_READY")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	FlushBatchUpdate()
	ShutdownRelayLogFlush(5 * time.Second)
	fmt.Printf("KILL9_FLUSHED %v\n", sig)
}
