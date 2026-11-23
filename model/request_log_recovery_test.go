package model

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestRequestLogRecoverySurvivesRestartAndSweep(t *testing.T) {
	requestLogTestStore(t)
	requestLogWithLimits(t, 10, 20)
	previous := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previous })
	require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": "false"}))
	RecordRequestLog(&RequestLog{Username: "history", RequestBody: "preserved", CreatedAt: 1000})
	closeDefaultRequestLogWriter()
	requestLogResetIndex(t)
	InitRequestLogStore()
	InitializeRequestLogHistory()
	for i := 0; i < 2; i++ {
		SweepRequestLogFiles()
	}
	items := requestLogIndexSnapshot()
	require.Len(t, items, 1)
	log, err := GetRequestLogById(items[0].meta.Id)
	require.NoError(t, err)
	require.Equal(t, "preserved", log.RequestBody)
}

func TestRequestLogRecoveryDoesNotResurrectDeletedEntries(t *testing.T) {
	for _, action := range []string{"clear", "before", "limit"} {
		t.Run(action, func(t *testing.T) {
			requestLogTestStore(t)
			requestLogWithLimits(t, 1, 3)
			for i := 1; i <= 3; i++ {
				RecordRequestLog(&RequestLog{CreatedAt: int64(i), RequestBody: "old"})
			}
			switch action {
			case "clear":
				_, err := ClearAllRequestLogs()
				require.NoError(t, err)
			case "before":
				_, err := DeleteOldRequestLog(3)
				require.NoError(t, err)
			case "limit":
				RecordRequestLog(&RequestLog{CreatedAt: 4, RequestBody: "retained"})
			}
			expected := requestLogIndexLen()
			// This tail was written after the last deletion checkpoint, with no clean shutdown.
			RecordRequestLog(&RequestLog{CreatedAt: 5, RequestBody: "new tail"})
			closeDefaultRequestLogWriter()
			requestLogResetIndex(t)
			InitRequestLogStore()
			InitializeRequestLogHistory()
			SweepRequestLogFiles()
			items := requestLogIndexSnapshot()
			require.Len(t, items, expected+1)
			log, err := GetRequestLogById(items[len(items)-1].meta.Id)
			require.NoError(t, err)
			require.Equal(t, "new tail", log.RequestBody)
		})
	}
}

func TestRequestLogRecoveryReclaimsDamagedFiles(t *testing.T) {
	root := requestLogTestStore(t)
	bad := filepath.Join(root, "2026-01-01", "seg-000000-old-1.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(bad), 0o755))
	require.NoError(t, os.WriteFile(bad, []byte("{broken"), 0o600))
	InitializeRequestLogHistory()
	SweepRequestLogFiles()
	require.NoFileExists(t, bad, "a damaged body must not disable orphan cleanup")
}

func TestRequestLogRecoveryCorruptionDoesNotBlockDeletion(t *testing.T) {
	for _, corruption := range []string{"truncated_tail", "malformed_middle_line", "invalid_record", "legacy_json", "checkpoint_json", "checkpoint_entry"} {
		for _, action := range []string{"clear", "before"} {
			t.Run(corruption+"/"+action, func(t *testing.T) {
				root := requestLogTestStore(t)
				requestLogWithLimits(t, 10, 20)
				old, err := common.Marshal(RequestLog{Id: 1, CreatedAt: 1000, RequestBody: "old"})
				require.NoError(t, err)
				kept, err := common.Marshal(RequestLog{Id: 2, CreatedAt: 2000, RequestBody: "kept"})
				require.NoError(t, err)
				segment := filepath.Join(root, "2026-01-01", "seg-000000-history-1.jsonl")
				require.NoError(t, os.MkdirAll(filepath.Dir(segment), 0o755))
				data := append(append(append([]byte{}, old...), '\n'), kept...)
				data = append(data, '\n')
				switch corruption {
				case "truncated_tail":
					data = append(data, []byte(`{"id":3,"request_body":"interrupted`)...)
				case "malformed_middle_line":
					data = append(append(append(append([]byte{}, old...), '\n'), []byte("{broken\n")...), kept...)
					data = append(data, '\n')
				case "invalid_record":
					data = append(data, []byte("{\"id\":0,\"created_at\":1}\n")...)
				case "legacy_json":
					legacy := filepath.Join(root, "2026-01-01", "0", "broken.json")
					require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o755))
					require.NoError(t, os.WriteFile(legacy, []byte("{broken"), 0o600))
				case "checkpoint_json":
					require.NoError(t, os.WriteFile(filepath.Join(root, requestLogRecoveryFile), []byte("{broken"), 0o600))
				case "checkpoint_entry":
					require.NoError(t, os.WriteFile(filepath.Join(root, requestLogRecoveryFile), []byte(`{"version":1,"sequence":2,"entries":[{"meta":null,"rel":"invalid"}]}`), 0o600))
				}
				require.NoError(t, os.WriteFile(segment, data, 0o600))
				InitializeRequestLogHistory()
				if corruption == "checkpoint_json" || corruption == "checkpoint_entry" {
					require.Zero(t, requestLogIndexLen(), "untrusted deletion boundaries must not resurrect old history")
					RecordRequestLog(&RequestLog{CreatedAt: 1000, RequestBody: "old"})
					RecordRequestLog(&RequestLog{CreatedAt: 2000, RequestBody: "kept"})
				}
				require.Equal(t, 2, requestLogIndexLen())

				want := []string{"new tail"}
				if action == "clear" {
					deleted, err := ClearAllRequestLogs()
					require.NoError(t, err)
					require.EqualValues(t, 2, deleted)
				} else {
					deleted, err := DeleteOldRequestLog(1500)
					require.NoError(t, err)
					require.EqualValues(t, 1, deleted)
					want = []string{"kept", "new tail"}
				}
				RecordRequestLog(&RequestLog{CreatedAt: 3000, RequestBody: "new tail"})
				closeDefaultRequestLogWriter()
				for restart := range 2 {
					requestLogResetIndex(t)
					InitRequestLogStore()
					InitializeRequestLogHistory()
					items := requestLogIndexSnapshot()
					require.Len(t, items, len(want), "restart %d must not resurrect deleted or corrupt records", restart)
					for i, item := range items {
						log, err := GetRequestLogById(item.meta.Id)
						require.NoError(t, err)
						require.Equal(t, want[i], log.RequestBody)
					}
					stats := SweepRequestLogFiles()
					require.Zero(t, stats.Errors)
				}
				if action == "clear" || corruption == "checkpoint_json" || corruption == "checkpoint_entry" {
					require.NoFileExists(t, segment, "unreferenced damaged history must be reclaimed")
				}
			})
		}
	}
}

func TestRequestLogDeletionAfterCorruptCheckpointKeepsUncommittedTail(t *testing.T) {
	root := requestLogTestStore(t)
	requestLogWithLimits(t, 10, 20)
	require.NoError(t, os.WriteFile(filepath.Join(root, requestLogRecoveryFile), []byte("{broken"), 0o600))
	InitializeRequestLogHistory()
	writer := NewRequestLogWriter()
	t.Cleanup(writer.Close)
	writer.Record([]*RequestLog{{CreatedAt: 1000, RequestBody: "old"}})
	// Deterministically stop at the real writer's write-before-index boundary.
	// Deletion must not mark this still-uncommitted record as already processed.
	data, err := common.Marshal(RequestLog{Id: 2, CreatedAt: 2000, RequestBody: "pending"})
	require.NoError(t, err)
	_, err = writer.file.Write(append(data, '\n'))
	require.NoError(t, err)
	deleted, err := ClearAllRequestLogs()
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	writer.Close()
	requestLogResetIndex(t)
	InitRequestLogStore()
	InitializeRequestLogHistory()
	items := requestLogIndexSnapshot()
	require.Len(t, items, 1)
	log, err := GetRequestLogById(items[0].meta.Id)
	require.NoError(t, err)
	require.Equal(t, "pending", log.RequestBody)
}

func TestRequestLogDeletionAfterCorruptCheckpointWriteFailure(t *testing.T) {
	for _, action := range []string{"clear", "before"} {
		t.Run(action, func(t *testing.T) {
			root := requestLogTestStore(t)
			require.NoError(t, os.WriteFile(filepath.Join(root, requestLogRecoveryFile), []byte("{broken"), 0o600))
			InitializeRequestLogHistory()
			RecordRequestLog(&RequestLog{CreatedAt: 1, RequestBody: "preserved"})
			closeDefaultRequestLogWriter()
			temp := filepath.Join(root, requestLogRecoveryTemp)
			require.NoError(t, os.Mkdir(temp, 0o700))
			var err error
			if action == "clear" {
				_, err = ClearAllRequestLogs()
			} else {
				_, err = DeleteOldRequestLog(2)
			}
			require.Error(t, err)
			items := requestLogIndexSnapshot()
			require.Len(t, items, 1)
			SweepRequestLogFiles()
			log, err := GetRequestLogById(items[0].meta.Id)
			require.NoError(t, err)
			require.Equal(t, "preserved", log.RequestBody)
			require.NoError(t, os.Remove(temp))
			deleted, err := ClearAllRequestLogs()
			require.NoError(t, err, "repairing a write error must allow retry without restarting")
			require.EqualValues(t, 1, deleted)
			stats := SweepRequestLogFiles()
			require.Zero(t, stats.Errors)
			require.Zero(t, countRequestLogFiles(t, root))
		})
	}
}

func TestRequestLogDeletionAfterCorruptCheckpointConcurrentSweep(t *testing.T) {
	root := requestLogTestStore(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, requestLogRecoveryFile), []byte("{broken"), 0o600))
	InitializeRequestLogHistory()
	RecordRequestLog(&RequestLog{CreatedAt: 1, RequestBody: "old"})
	closeDefaultRequestLogWriter()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var deleted int64
	var deleteErr error
	wg.Go(func() {
		<-start
		deleted, deleteErr = ClearAllRequestLogs()
	})
	wg.Go(func() {
		<-start
		for range 20 {
			SweepRequestLogFiles()
		}
	})
	close(start)
	wg.Wait()
	require.NoError(t, deleteErr)
	require.EqualValues(t, 1, deleted)
	require.Zero(t, requestLogIndexLen())
	stats := SweepRequestLogFiles()
	require.Zero(t, stats.Errors)
	require.Zero(t, countRequestLogFiles(t, root))
}

func TestRequestLogRecoveryLegacyIDsAndDetails(t *testing.T) {
	root := requestLogTestStore(t)
	requestLogWithLimits(t, 10, 20)
	for i, body := range []string{"first", "second"} {
		name := filepath.Join(root, "2026-01-01", "0", body+".json")
		require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
		data, err := common.Marshal(RequestLog{Id: 1, CreatedAt: int64(i + 1), RequestBody: body, Url: "/v1?key=secret"})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(name, data, 0o600))
	}
	InitializeRequestLogHistory()
	SweepRequestLogFiles()
	items := requestLogIndexSnapshot()
	require.Len(t, items, 2)
	require.NotEqual(t, items[0].meta.Id, items[1].meta.Id)
	for i, body := range []string{"first", "second"} {
		log, err := GetRequestLogById(items[i].meta.Id)
		require.NoError(t, err)
		require.Equal(t, body, log.RequestBody)
		require.Equal(t, items[i].meta.Id, log.Id)
		require.NotContains(t, items[i].meta.Url, "secret")
	}
	RecordRequestLog(&RequestLog{CreatedAt: 3})
	require.Greater(t, requestLogIndexSnapshot()[2].meta.Id, items[1].meta.Id)
}

func TestRequestLogRecoveryDamagedCheckpointAndFailedDeletion(t *testing.T) {
	root := requestLogTestStore(t)
	RecordRequestLog(&RequestLog{CreatedAt: 1, RequestBody: "keep"})
	closeDefaultRequestLogWriter()
	require.NoError(t, os.Mkdir(filepath.Join(root, requestLogRecoveryTemp), 0o755))
	_, err := ClearAllRequestLogs()
	require.Error(t, err)
	require.Equal(t, 1, requestLogIndexLen())
	_, err = DeleteOldRequestLog(2)
	require.Error(t, err)
	require.Equal(t, 1, requestLogIndexLen())
	require.NoError(t, os.WriteFile(filepath.Join(root, requestLogRecoveryFile), []byte("broken"), 0o600))
	requestLogResetIndex(t)
	InitRequestLogStore()
	InitializeRequestLogHistory()
	SweepRequestLogFiles()
	require.Equal(t, 1, countRequestLogFiles(t, root))
}

func TestRequestLogRecoveryRetentionBoundaries(t *testing.T) {
	for _, minCount := range []int{0, 1} {
		t.Run(fmt.Sprintf("min=%d", minCount), func(t *testing.T) {
			requestLogTestStore(t)
			requestLogWithLimits(t, 10, 20)
			for i := 1; i <= 8; i++ {
				RecordRequestLog(&RequestLog{CreatedAt: int64(i)})
			}
			closeDefaultRequestLogWriter()
			requestLogResetIndex(t)
			InitRequestLogStore()
			common.RequestLogMinCount, common.RequestLogMaxCount = minCount, 3
			InitializeRequestLogHistory()
			require.Equal(t, minCount, requestLogIndexLen())
			if minCount > 0 {
				require.EqualValues(t, 8, requestLogIndexSnapshot()[0].meta.CreatedAt)
			}
			// A second restart cannot resurrect records dropped during recovery.
			requestLogResetIndex(t)
			InitRequestLogStore()
			InitializeRequestLogHistory()
			require.Equal(t, minCount, requestLogIndexLen())
		})
	}
}

func TestRequestLogRecoveryClearOnRestartRemovesCheckpoint(t *testing.T) {
	root := requestLogTestStore(t)
	RecordRequestLog(&RequestLog{CreatedAt: 1})
	CheckpointRequestLogs()
	closeDefaultRequestLogWriter()
	requestLogResetIndex(t)
	require.FileExists(t, filepath.Join(root, requestLogRecoveryFile))
	require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": "true"}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": "false"}))
	})
	InitRequestLogStore()
	InitializeRequestLogHistory()
	require.NoFileExists(t, filepath.Join(root, requestLogRecoveryFile))
	require.Zero(t, countRequestLogFiles(t, root))
	require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": "false"}))
	InitRequestLogStore()
	InitializeRequestLogHistory()
	require.Zero(t, requestLogIndexLen())
}
