package model

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// InitializeRequestLogHistory runs once at startup, before writers, sweepers and
// request serving. Settings saved later only affect the next process startup.
func InitializeRequestLogHistory() {
	defer func() {
		if r := recover(); r != nil {
			requestLogRecoveryIncomplete = true
			common.SysError(fmt.Sprintf("request log startup: panic recovered: %v", r))
		}
	}()
	// Local recovery no longer depends on a one-shot Redis shutdown snapshot.
	if requestLogRedisReady() {
		ctx, cancel := context.WithTimeout(context.Background(), requestLogRedisOpTime)
		defer cancel()
		if err := common.RequestLogRDB.Del(ctx, requestLogSnapshotKey).Err(); err != nil {
			common.SysError("request log startup: cannot discard snapshot: " + err.Error())
		}
	}
	if !operation_setting.GetRequestLogSetting().ClearOnRestart {
		restoreRequestLogsFromDisk()
		return
	}
	stats := clearRequestLogHistoryFiles()
	common.SysLog(fmt.Sprintf("request log startup cleanup: scanned=%d deleted=%d errors=%d", stats.Scanned, stats.Deleted, stats.Errors))
}

// clearRequestLogHistoryFiles only removes recognized date trees from the claimed
// store. Root confines deletion even if a child is a symlink or Windows junction.
// Enumeration is bounded; no writers may be active while this runs.
func clearRequestLogHistoryFiles() requestLogSweepStats {
	var stats requestLogSweepStats
	if !RequestLogStoreReady() {
		return stats
	}
	root, err := os.OpenRoot(requestLogRoot)
	if err != nil {
		common.SysError("request log startup: cannot open root: " + err.Error())
		stats.Errors++
		return stats
	}
	defer root.Close()
	marker, err := root.Lstat(requestLogOwnerMarker)
	if err != nil || !marker.Mode().IsRegular() {
		common.SysError("request log startup: missing or invalid ownership marker")
		stats.Errors++
		return stats
	}
	dir, err := root.Open(".")
	if err != nil {
		common.SysError("request log startup: cannot enumerate root: " + err.Error())
		stats.Errors++
		return stats
	}
	defer dir.Close()
	for _, name := range []string{requestLogRecoveryFile, requestLogRecoveryTemp} {
		if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
			common.SysError("request log startup: cannot remove checkpoint: " + err.Error())
			stats.Errors++
		}
	}
	for {
		entries, readErr := dir.ReadDir(requestLogSweepBatch)
		for _, entry := range entries {
			stats.Scanned++
			if !isRequestLogDateDir(entry.Name()) {
				continue
			}
			if err := root.RemoveAll(entry.Name()); err != nil {
				common.SysError("request log startup: cannot remove " + entry.Name() + ": " + err.Error())
				stats.Errors++
				continue
			}
			stats.Deleted++
		}
		if readErr != nil {
			if readErr != io.EOF {
				common.SysError("request log startup: cannot read directory: " + readErr.Error())
				stats.Errors++
			}
			return stats
		}
	}
}
