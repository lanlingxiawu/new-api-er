package model

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// Pure helpers: keys, paths, ids, CSV record formatting.
// ===========================================================================

func TestLedgerExportKeyAndPathHelpers(t *testing.T) {
	assert.Equal(t, ledgerExportJobKeyPfx+"abc", ledgerExportJobRedisKey("abc"))
	assert.Equal(t, ledgerExportUserCooldownKeyPfx+"7", ledgerExportUserCooldownKey(7))

	p := LedgerExportFilePath("job1")
	assert.Equal(t, filepath.Join(os.TempDir(), "ledger-export-job1.csv.gz"), p)

	pp := LedgerExportPartFilePath("job1", 3)
	assert.Equal(t, filepath.Join(os.TempDir(), "ledger-export-job1-part-0003.csv.gz"), pp)

	id1 := NewLedgerExportJobID()
	id2 := NewLedgerExportJobID()
	assert.NotEmpty(t, id1)
	assert.NotEqual(t, id1, id2, "job ids must be unique")
}

func TestLedgerExportRecord(t *testing.T) {
	t.Run("full values", func(t *testing.T) {
		logID := 55
		gm := 0.6
		item := &ConsumptionCostLedgerItem{
			CreatedAt: 1_600_000_000, Tags: []string{"profit", "reversal"}, LogId: &logID,
			UserId: 3, ChannelId: 9, ChannelName: "chan", ModelName: "gpt", GroupName: "grp",
			RevenueQuota: 1000, CostQuota: 400, ProfitQuota: 600, GrossMargin: &gm,
			GroupRatio: 1.25, CostRatio: 0.4,
		}
		rec := ledgerExportRecord(item)
		require.Len(t, rec, 14)
		assert.Equal(t, "profit|reversal", rec[1])
		assert.Equal(t, "55", rec[2])
		assert.Equal(t, "3", rec[3])
		assert.Equal(t, "9", rec[4])
		assert.Equal(t, "chan", rec[5])
		assert.Equal(t, "gpt", rec[6])
		assert.Equal(t, "grp", rec[7])
		assert.Equal(t, "1000", rec[8])
		assert.Equal(t, "400", rec[9])
		assert.Equal(t, "600", rec[10])
		assert.Equal(t, "0.600000", rec[11])
		assert.Equal(t, "1.250000", rec[12])
		assert.Equal(t, "0.400000", rec[13])
	})

	t.Run("nil gross margin and nil log id => empty cells", func(t *testing.T) {
		item := &ConsumptionCostLedgerItem{CreatedAt: 1_600_000_000, RevenueQuota: 0}
		rec := ledgerExportRecord(item)
		assert.Equal(t, "", rec[2], "nil LogId => empty")
		assert.Equal(t, "", rec[11], "nil GrossMargin => empty")
	})
}

// ===========================================================================
// Redis-backed: job lifecycle, download tokens, locks, cooldown, cleanup.
// ===========================================================================

func TestLedgerExportJobLifecycle(t *testing.T) {
	enableRedis(t)
	jobID := NewLedgerExportJobID()
	t.Cleanup(func() { DeleteLedgerExportJob(jobID) })

	job, err := CreateLedgerExportJob(jobID, 4242)
	require.NoError(t, err)
	assert.Equal(t, LedgerExportStatusPending, job.Status)
	assert.Equal(t, 4242, job.UserID)
	assert.Greater(t, job.CreatedAt, int64(0))

	got, err := GetLedgerExportJob(jobID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, jobID, got.JobID)
	assert.Equal(t, LedgerExportStatusPending, got.Status)

	DeleteLedgerExportJob(jobID)
	gone, err := GetLedgerExportJob(jobID)
	require.NoError(t, err)
	assert.Nil(t, gone, "deleted job returns nil,nil")

	// unknown job id also returns nil,nil (redis.Nil handled)
	none, err := GetLedgerExportJob(NewLedgerExportJobID())
	require.NoError(t, err)
	assert.Nil(t, none)
}

func TestLedgerDownloadToken_OneTimeUse(t *testing.T) {
	enableRedis(t)
	jobID := NewLedgerExportJobID()
	token, err := CreateLedgerDownloadToken(jobID, 99)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	gotJob, gotUser, err := ValidateAndConsumeLedgerDownloadToken(token)
	require.NoError(t, err)
	assert.Equal(t, jobID, gotJob)
	assert.Equal(t, 99, gotUser)

	// second use fails: token was deleted (one-time)
	gotJob2, gotUser2, err := ValidateAndConsumeLedgerDownloadToken(token)
	require.NoError(t, err)
	assert.Equal(t, "", gotJob2)
	assert.Equal(t, 0, gotUser2)
}

func TestValidateAndConsumeLedgerDownloadToken_MalformedData(t *testing.T) {
	enableRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	t.Run("missing colon separator", func(t *testing.T) {
		token := "malformed-" + uniq("t")
		key := ledgerDownloadTokenKeyPfx + token
		require.NoError(t, common.RDB.Set(ctx, key, "nocolon", time.Minute).Err())
		t.Cleanup(func() { _ = common.RedisDel(key) })
		_, _, err := ValidateAndConsumeLedgerDownloadToken(token)
		assert.Error(t, err)
	})

	t.Run("non-integer user id", func(t *testing.T) {
		token := "malformed2-" + uniq("t")
		key := ledgerDownloadTokenKeyPfx + token
		require.NoError(t, common.RDB.Set(ctx, key, "job:notanint", time.Minute).Err())
		t.Cleanup(func() { _ = common.RedisDel(key) })
		_, _, err := ValidateAndConsumeLedgerDownloadToken(token)
		assert.Error(t, err)
	})
}

func TestLedgerExportGlobalLock_OwnerRelease(t *testing.T) {
	enableRedis(t)
	jobA := NewLedgerExportJobID()
	jobB := NewLedgerExportJobID()
	t.Cleanup(func() { ReleaseLedgerExportGlobalLock(jobA); ReleaseLedgerExportGlobalLock(jobB) })

	require.True(t, AcquireLedgerExportGlobalLock(jobA))
	assert.False(t, AcquireLedgerExportGlobalLock(jobB), "global lock is exclusive")

	// non-owner release must NOT free the lock
	ReleaseLedgerExportGlobalLock(jobB)
	assert.False(t, AcquireLedgerExportGlobalLock(jobB), "still held after wrong-owner release")

	// owner release frees it
	ReleaseLedgerExportGlobalLock(jobA)
	assert.True(t, AcquireLedgerExportGlobalLock(jobB), "reacquirable after owner release")
	ReleaseLedgerExportGlobalLock(jobB)
}

func TestLedgerDownloadGlobalLock(t *testing.T) {
	enableRedis(t)
	lockID := uniq("dl")
	t.Cleanup(func() { ReleaseLedgerDownloadGlobalLock(lockID) })

	assert.False(t, IsLedgerDownloadBusy(), "no lock held initially")
	require.True(t, AcquireLedgerDownloadGlobalLock(lockID))
	assert.True(t, IsLedgerDownloadBusy(), "busy while held")
	assert.False(t, AcquireLedgerDownloadGlobalLock(uniq("dl2")), "exclusive")

	ReleaseLedgerDownloadGlobalLock(lockID)
	assert.False(t, IsLedgerDownloadBusy(), "freed after release")
}

func TestCheckAndSetLedgerExportUserCooldown(t *testing.T) {
	enableRedis(t)
	userID := nextTestID()
	t.Cleanup(func() { _ = common.RedisDel(ledgerExportUserCooldownKey(userID)) })

	assert.False(t, CheckAndSetLedgerExportUserCooldown(userID), "first call: not on cooldown")
	assert.True(t, CheckAndSetLedgerExportUserCooldown(userID), "second call: on cooldown")
}

func TestCleanupOrphanLedgerExportFiles(t *testing.T) {
	enableRedis(t)

	// orphan: file exists, no redis job key -> removed
	orphanID := "orphan-" + uniq("j")
	orphanPath := LedgerExportFilePath(orphanID)
	require.NoError(t, os.WriteFile(orphanPath, []byte("x"), 0o644))
	t.Cleanup(func() { _ = os.Remove(orphanPath) })

	// live: file exists AND redis job key exists -> kept
	liveID := "live-" + uniq("j")
	livePath := LedgerExportFilePath(liveID)
	require.NoError(t, os.WriteFile(livePath, []byte("x"), 0o644))
	_, err := CreateLedgerExportJob(liveID, 1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(livePath); DeleteLedgerExportJob(liveID) })

	CleanupOrphanLedgerExportFiles()

	_, statErr := os.Stat(orphanPath)
	assert.True(t, os.IsNotExist(statErr), "orphan file must be removed")
	_, statErr = os.Stat(livePath)
	assert.NoError(t, statErr, "file with a live job key must be kept")
}

// ===========================================================================
// Sharded CSV export: writes real gzip parts from ledger rows, then reads back.
// Also covers newLedgerExportPartWriter / Write / Close via the happy path.
// ===========================================================================

func TestWriteLedgerExportCSVSharded(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	base := ledgerAggBase()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.ChannelName = "expA"
		c.CreatedAt = base + 10
		c.RevenueQuota = 1000
		c.CostQuota = 400
	})
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.ChannelName = "expA"
		c.CreatedAt = base + 20
		c.RevenueQuota = 500
		c.CostQuota = 700
	})

	jobID := "wtest-" + uniq("j")
	filePath := LedgerExportFilePath(jobID)
	t.Cleanup(func() { _ = os.Remove(filePath) })

	filter := ConsumptionCostLedgerCommonFilter{
		ChannelId: ch,
		StartTime: base,
		EndTime:   base + 300,
	}
	var lastRows int64
	rowCount, filePaths, err := writeLedgerExportCSVSharded(
		context.Background(), jobID, filter, filePath,
		func(progress int, rows int64) { lastRows = rows },
	)
	require.NoError(t, err)
	assert.EqualValues(t, 2, rowCount)
	assert.EqualValues(t, 2, lastRows)
	require.Len(t, filePaths, 1)
	for _, p := range filePaths {
		t.Cleanup(func() { _ = os.Remove(p) })
	}

	records := readGzCSV(t, filePaths[0])
	require.Len(t, records, 3, "header + 2 data rows")
	assert.Equal(t, "Time", records[0][0])
	assert.Equal(t, "Revenue Quota", records[0][8])

	// rows are ordered created_at desc: base+20 first
	revenues := []string{records[1][8], records[2][8]}
	assert.ElementsMatch(t, []string{"1000", "500"}, revenues)
	// snapshot channel name flows through (Fill keeps existing snapshot)
	assert.Equal(t, "expA", records[1][5])
}

// Forces the per-file row cap low so the export rolls over into multiple part
// files, covering newLedgerExportPartWriter / Close mid-stream.
func TestWriteLedgerExportCSVSharded_PartFileRollover(t *testing.T) {
	requireDB(t)
	cfg := operation_setting.GetLedgerDetailSetting()
	orig := cfg.ExportRowsPerFile
	cfg.ExportRowsPerFile = 1 // one data row per part file
	t.Cleanup(func() { cfg.ExportRowsPerFile = orig })

	ch := nextTestID()
	base := ledgerAggBase()
	for i, ts := range []int64{base + 1, base + 2, base + 3} {
		rev := int64(100 * (i + 1))
		mkCost(t, func(c *ConsumptionCost) {
			c.ChannelId = ch
			c.CreatedAt = ts
			c.RevenueQuota = rev
			c.CostQuota = 10
		})
	}

	jobID := "roll-" + uniq("j")
	filePath := LedgerExportFilePath(jobID)
	t.Cleanup(func() { _ = os.Remove(filePath) })

	rowCount, filePaths, err := writeLedgerExportCSVSharded(
		context.Background(), jobID,
		ConsumptionCostLedgerCommonFilter{ChannelId: ch, StartTime: base, EndTime: base + 300},
		filePath, func(int, int64) {},
	)
	require.NoError(t, err)
	assert.EqualValues(t, 3, rowCount)
	assert.Len(t, filePaths, 3, "3 rows with cap 1 => 3 part files")
	total := 0
	for _, p := range filePaths {
		t.Cleanup(func() { _ = os.Remove(p) })
		recs := readGzCSV(t, p)
		total += len(recs) - 1 // minus header
	}
	assert.Equal(t, 3, total, "every row written exactly once across parts")
}

func TestWriteLedgerExportCSVSharded_UniqueFilterEarlyReturn(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	logID := nextTestID()
	base := ledgerAggBase()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.LogId = &logID
		c.CreatedAt = base + 15
		c.RevenueQuota = 321
		c.CostQuota = 21
	})

	jobID := "wtest2-" + uniq("j")
	filePath := LedgerExportFilePath(jobID)
	t.Cleanup(func() { _ = os.Remove(filePath) })

	// unique filter (LogId>0) + time range; export returns after first hit
	filter := ConsumptionCostLedgerCommonFilter{
		LogId:     logID,
		StartTime: base,
		EndTime:   base + 300,
	}
	rowCount, filePaths, err := writeLedgerExportCSVSharded(
		context.Background(), jobID, filter, filePath, func(int, int64) {},
	)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rowCount)
	for _, p := range filePaths {
		t.Cleanup(func() { _ = os.Remove(p) })
	}
	records := readGzCSV(t, filePaths[0])
	require.Len(t, records, 2)
	assert.Equal(t, "321", records[1][8])
}

// runLedgerExport is the goroutine body behind StartLedgerExport; run it
// synchronously to verify it drives the job to Ready and persists the file.
func TestRunLedgerExport_DrivesJobToReady(t *testing.T) {
	enableRedis(t)
	ch := nextTestID()
	base := ledgerAggBase()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = base + 11
		c.RevenueQuota = 100
		c.CostQuota = 10
	})

	jobID := NewLedgerExportJobID()
	job, err := CreateLedgerExportJob(jobID, 1)
	require.NoError(t, err)
	t.Cleanup(func() {
		DeleteLedgerExportJob(jobID)
		_ = os.Remove(LedgerExportFilePath(jobID))
		ReleaseLedgerExportGlobalLock(jobID)
	})

	filter := ConsumptionCostLedgerCommonFilter{ChannelId: ch, StartTime: base, EndTime: base + 300}
	runLedgerExport(job, filter)

	stored, err := GetLedgerExportJob(jobID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, LedgerExportStatusReady, stored.Status)
	assert.EqualValues(t, 100, stored.Progress)
	assert.EqualValues(t, 1, stored.RowCount)
	require.NotEmpty(t, stored.FilePaths)
	for _, p := range stored.FilePaths {
		t.Cleanup(func() { _ = os.Remove(p) })
	}
	_, statErr := os.Stat(stored.FilePath)
	assert.NoError(t, statErr, "export file exists on disk")
}

func TestStartLedgerExport_Async(t *testing.T) {
	enableRedis(t)
	ch := nextTestID()
	base := ledgerAggBase()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = base + 12
		c.RevenueQuota = 200
		c.CostQuota = 20
	})
	jobID := NewLedgerExportJobID()
	job, err := CreateLedgerExportJob(jobID, 1)
	require.NoError(t, err)
	t.Cleanup(func() {
		DeleteLedgerExportJob(jobID)
		_ = os.Remove(LedgerExportFilePath(jobID))
		ReleaseLedgerExportGlobalLock(jobID)
	})

	StartLedgerExport(job, ConsumptionCostLedgerCommonFilter{ChannelId: ch, StartTime: base, EndTime: base + 300})

	require.Eventually(t, func() bool {
		s, e := GetLedgerExportJob(jobID)
		return e == nil && s != nil && s.Status == LedgerExportStatusReady
	}, 8*time.Second, 100*time.Millisecond, "async export should reach Ready")
}

// readGzCSV decodes a gzip-compressed CSV export part into records.
func readGzCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gz.Close()
	records, err := csv.NewReader(gz).ReadAll()
	require.NoError(t, err)
	return records
}
