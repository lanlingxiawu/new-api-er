package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestRequestLogStartupHistory(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, withRedis := range []bool{false, true} {
			t.Run(fmt.Sprintf("clear=%t/redis=%t", enabled, withRedis), func(t *testing.T) {
				root := requestLogTestStore(t)
				previousRedis, previousClient := common.RedisEnabled, common.RequestLogRDB
				common.RedisEnabled = withRedis
				if withRedis {
					server := miniredis.RunT(t)
					client := redis.NewClient(&redis.Options{Addr: server.Addr()})
					t.Cleanup(func() { require.NoError(t, client.Close()) })
					common.RequestLogRDB = client
				}
				t.Cleanup(func() {
					common.RedisEnabled, common.RequestLogRDB = previousRedis, previousClient
					require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": "false"}))
				})
				value := "false"
				if enabled {
					value = "true"
				}
				require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": value}))
				RecordRequestLog(&RequestLog{Username: "old", CreatedAt: 1000})
				SnapshotRequestLogs()
				closeDefaultRequestLogWriter()
				requestLogResetIndex(t)
				legacy := filepath.Join(root, "2026-01-01", "0", "old.json")
				require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o755))
				require.NoError(t, os.WriteFile(legacy, []byte(`{"id":99,"created_at":500,"request_body":"legacy"}`), 0o600))
				InitializeRequestLogHistory()
				require.FileExists(t, filepath.Join(root, requestLogOwnerMarker))
				if enabled {
					require.NoFileExists(t, legacy)
					require.Zero(t, countRequestLogFiles(t, root))
					require.Zero(t, requestLogIndexLen())
					InitializeRequestLogHistory() // empty directory / repeated startup
				} else {
					require.FileExists(t, legacy)
					require.Equal(t, 2, countRequestLogFiles(t, root))
					require.Equal(t, 2, requestLogIndexLen())
					SweepRequestLogFiles()
					require.Equal(t, 2, countRequestLogFiles(t, root))
				}
				if withRedis {
					n, err := common.RequestLogRDB.Exists(context.Background(), requestLogSnapshotKey).Result()
					require.NoError(t, err)
					require.Zero(t, n)
				}
				RecordRequestLog(&RequestLog{Username: "new", CreatedAt: 2000})
				require.NotEmpty(t, requestLogIndexSnapshot())
				require.True(t, requestLogBodyExists(requestLogIndexSnapshot()[requestLogIndexLen()-1].rel, make(map[string]int64)))
			})
		}
	}
}

func TestRequestLogStartupCleanupSafety(t *testing.T) {
	root := requestLogTestStore(t)
	outside := t.TempDir()
	protected := filepath.Join(outside, "keep.json")
	require.NoError(t, os.WriteFile(protected, []byte("keep"), 0o600))
	unrelated := filepath.Join(root, "unrelated.txt")
	require.NoError(t, os.WriteFile(unrelated, []byte("keep"), 0o600))
	stats := clearRequestLogHistoryFiles()
	require.Zero(t, stats.Errors)
	require.FileExists(t, unrelated)
	requestLogReady = false
	stats = clearRequestLogHistoryFiles()
	require.Zero(t, stats.Deleted)
	requestLogReady = true
	link := filepath.Join(root, "2026-01-01")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("directory symlink unavailable: " + err.Error())
	}
	stats = clearRequestLogHistoryFiles()
	require.Zero(t, stats.Errors)
	require.FileExists(t, protected)
	require.FileExists(t, filepath.Join(root, requestLogOwnerMarker))
}

func TestRequestLogStartupCleanupInvalidRoot(t *testing.T) {
	for _, mode := range []string{"missing_marker", "invalid_marker", "missing_root"} {
		t.Run(mode, func(t *testing.T) {
			root := requestLogTestStore(t)
			marker := filepath.Join(root, requestLogOwnerMarker)
			require.NoError(t, os.Remove(marker))
			if mode == "invalid_marker" {
				require.NoError(t, os.Mkdir(marker, 0o755))
			}
			if mode == "missing_root" {
				require.NoError(t, os.Remove(root))
			}
			stats := clearRequestLogHistoryFiles()
			require.Equal(t, 1, stats.Errors)
			require.Zero(t, stats.Deleted)
		})
	}
}

func TestRequestLogStartupContinuesAfterRedisFailure(t *testing.T) {
	root := requestLogTestStore(t)
	previousRedis, previousClient := common.RedisEnabled, common.RequestLogRDB
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	require.NoError(t, client.Close())
	common.RedisEnabled, common.RequestLogRDB = true, client
	require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": "true"}))
	t.Cleanup(func() {
		common.RedisEnabled, common.RequestLogRDB = previousRedis, previousClient
		require.NoError(t, config.GlobalConfig.UpdateFromMap("request_log_setting", map[string]string{"clear_on_restart": "false"}))
	})
	RecordRequestLog(&RequestLog{Username: "old", CreatedAt: 1000})
	closeDefaultRequestLogWriter()
	requestLogResetIndex(t)
	InitializeRequestLogHistory()
	require.Zero(t, countRequestLogFiles(t, root))
	require.True(t, RequestLogStoreReady())
}
