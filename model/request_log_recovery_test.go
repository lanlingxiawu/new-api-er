package model

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestRequestLogRecoveryPreservesDamagedFiles(t *testing.T) {
	root := requestLogTestStore(t)
	bad := filepath.Join(root, "2026-01-01", "seg-000000-old-1.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(bad), 0o755))
	require.NoError(t, os.WriteFile(bad, []byte("{broken"), 0o600))
	InitializeRequestLogHistory()
	SweepRequestLogFiles()
	require.FileExists(t, bad)
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
