package model

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 磁盘正文的回收：内存索引是唯一真值，磁盘上没有对应索引的文件即为孤儿。
//
// 索引淘汰（条数超上限时截断）刻意不删文件，写入路径因此完全不含删除 IO；
// 回收全部集中到这里，每 REQUEST_LOG_SWEEP_INTERVAL_SEC 一轮。

// requestLogSweepBatch 是单次 ReadDir 的批大小。长期停机后重启时目录里可能堆积
// 远超上限的文件，分批读避免一次性把目录项全部载入内存。
const requestLogSweepBatch = 1000

var (
	requestLogSweepTrigger = make(chan struct{}, 1)
	requestLogSweepOnce    sync.Once
)

type requestLogSweepStats struct {
	Scanned     int
	Deleted     int
	DirsRemoved int
	Errors      int
}

// StartRequestLogSweeper 启动清理协程。必须在 RestoreRequestLogs 之后调用。
func StartRequestLogSweeper() {
	if !RequestLogStoreReady() || requestLogSweepInterval <= 0 {
		return
	}
	requestLogSweepOnce.Do(func() {
		go requestLogSweepLoop()
	})
}

// TriggerRequestLogSweep 请求一次立即清理（清空/按时间删除索引后调用）。
// 非阻塞：已有待处理信号时直接返回，绝不阻塞调用方。
func TriggerRequestLogSweep() {
	select {
	case requestLogSweepTrigger <- struct{}{}:
	default:
	}
}

func requestLogSweepLoop() {
	ticker := time.NewTicker(requestLogSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-requestLogSweepTrigger:
		}
		runRequestLogSweep()
	}
}

func runRequestLogSweep() {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("request log sweep: panic recovered: %v", r))
		}
	}()
	started := time.Now()
	stats := SweepRequestLogFiles()
	if stats.Deleted > 0 || stats.DirsRemoved > 0 || stats.Errors > 0 {
		common.SysLog(fmt.Sprintf(
			"request log sweep: scanned=%d deleted=%d dirs_removed=%d errors=%d in %s",
			stats.Scanned, stats.Deleted, stats.DirsRemoved, stats.Errors, time.Since(started)))
	}
}

// SweepRequestLogFiles 执行一轮清理并返回统计。导出供测试与立即触发使用。
func SweepRequestLogFiles() requestLogSweepStats {
	var stats requestLogSweepStats
	if !RequestLogStoreReady() {
		return stats
	}

	keep := make(map[string]struct{})
	datesInUse := make(map[string]struct{})
	for _, entry := range requestLogIndexSnapshot() {
		keep[entry.rel] = struct{}{}
		if idx := strings.IndexByte(entry.rel, '/'); idx > 0 {
			datesInUse[entry.rel[:idx]] = struct{}{}
		}
	}

	now := time.Now()
	cutoff := now.Add(-requestLogSweepGrace)
	today := now.Format("2006-01-02")
	// 早于该日期的日期目录可以整体删除，无需逐文件 stat。取"今天-1"是为了
	// 让宽限期判断（分钟级）与整目录删除（天级）之间留出一整天的安全距离。
	wholeDirBefore := now.AddDate(0, 0, -1).Format("2006-01-02")

	entries, err := os.ReadDir(requestLogRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			stats.Errors++
			common.SysError("request log sweep: cannot read " + requestLogRoot + ": " + err.Error())
		}
		return stats
	}

	for _, entry := range entries {
		name := entry.Name()
		full := filepath.Join(requestLogRoot, name)

		if name == requestLogOwnerMarker {
			// 归属标记：删掉它下次启动就无法证明这个目录是本功能独占的。
			continue
		}
		if !entry.IsDir() {
			// 根目录下的散落文件：InitRequestLogStore 已经确认本目录独占，按孤儿处理。
			stats.Scanned++
			removeRequestLogPathIfStale(full, entry, cutoff, false, &stats)
			continue
		}
		if !isRequestLogDateDir(name) {
			removeRequestLogPathIfStale(full, entry, cutoff, true, &stats)
			continue
		}
		if name < wholeDirBefore {
			if _, used := datesInUse[name]; !used {
				// ReadDir 在多数平台不返回 mtime，逐文件判断宽限期就是逐文件 stat；
				// 长期停机后目录里可能有几十万个文件，整目录删除把这部分开销省掉。
				if rerr := os.RemoveAll(full); rerr == nil {
					stats.DirsRemoved++
				} else {
					stats.Errors++
				}
				continue
			}
		}
		sweepRequestLogDateDir(name, full, keep, cutoff, today, &stats)
	}
	return stats
}

// sweepRequestLogDateDir 处理单个日期目录下的 8 个桶。
func sweepRequestLogDateDir(date, dateFull string, keep map[string]struct{}, cutoff time.Time, today string, stats *requestLogSweepStats) {
	buckets, err := os.ReadDir(dateFull)
	if err != nil {
		stats.Errors++
		return
	}
	remaining := 0
	for _, bucket := range buckets {
		bucketFull := filepath.Join(dateFull, bucket.Name())
		if !bucket.IsDir() {
			stats.Scanned++
			if !removeRequestLogPathIfStale(bucketFull, bucket, cutoff, false, stats) {
				remaining++
			}
			continue
		}
		left := sweepRequestLogBucketDir(date, bucket.Name(), bucketFull, keep, cutoff, stats)
		if left > 0 {
			remaining++
			continue
		}
		// 空桶目录只在非当天回收：当天的目录随时可能有新写入。
		if date < today && os.Remove(bucketFull) == nil {
			stats.DirsRemoved++
		} else {
			remaining++
		}
	}
	if remaining == 0 && date < today {
		if os.Remove(dateFull) == nil {
			stats.DirsRemoved++
		}
	}
}

// sweepRequestLogBucketDir 返回该桶内保留下来的条目数。
func sweepRequestLogBucketDir(date, bucket, bucketFull string, keep map[string]struct{}, cutoff time.Time, stats *requestLogSweepStats) int {
	dir, err := os.Open(bucketFull)
	if err != nil {
		stats.Errors++
		return 1 // 读不到就当作非空，不回收目录
	}
	defer dir.Close()

	prefix := date + "/" + bucket + "/"
	remaining := 0
	for {
		batch, rerr := dir.ReadDir(requestLogSweepBatch)
		for _, entry := range batch {
			stats.Scanned++
			if _, ok := keep[prefix+entry.Name()]; ok {
				remaining++
				continue
			}
			if !removeRequestLogPathIfStale(filepath.Join(bucketFull, entry.Name()), entry, cutoff, entry.IsDir(), stats) {
				remaining++
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				stats.Errors++
			}
			break
		}
		if len(batch) == 0 {
			break
		}
	}
	return remaining
}

// removeRequestLogPathIfStale 删除超过宽限期的孤儿路径，返回是否已删除。
//
// 宽限期是必须的：写盘成功到进索引之间存在一个瞬间窗口，没有它并发写入的文件会被
// 当场当成孤儿删掉。
func removeRequestLogPathIfStale(full string, entry os.DirEntry, cutoff time.Time, recursive bool, stats *requestLogSweepStats) bool {
	info, err := entry.Info()
	if err != nil || !info.ModTime().Before(cutoff) {
		return false
	}
	if recursive {
		err = os.RemoveAll(full)
	} else {
		err = os.Remove(full)
	}
	if err != nil {
		stats.Errors++
		return false
	}
	if recursive {
		stats.DirsRemoved++
	} else {
		stats.Deleted++
	}
	return true
}

// isRequestLogDateDir 判断目录名是否为 YYYY-MM-DD。
func isRequestLogDateDir(name string) bool {
	if len(name) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", name)
	return err == nil
}
