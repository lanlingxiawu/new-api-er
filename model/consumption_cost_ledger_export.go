package model

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const (
	ledgerExportJobTTL          = 24 * time.Hour
	ledgerExportGlobalLockTTL   = 130 * time.Minute // slightly more than export timeout
	ledgerExportUserCooldown    = 5 * time.Minute
	ledgerExportBatchSize       = 3000
	ledgerExportBatchSleepMs    = 100
	ledgerExportTimeout         = 2 * time.Hour
	ledgerExportWindowSeconds   = int64(3600)
	ledgerExportRowsPerFile     = 1000000
	ledgerExportFilePrefix      = "ledger-export-"
	ledgerExportPartFilePattern = "ledger-export-%s-part-%04d.csv.gz"

	LedgerExportMaxRangeSeconds = int64(24 * 3600)
)

const (
	LedgerExportStatusPending = "pending"
	LedgerExportStatusRunning = "running"
	LedgerExportStatusReady   = "ready"
	LedgerExportStatusFailed  = "failed"
)

const (
	ledgerExportGlobalLockKey      = "ledger:export:global:lock"
	ledgerDownloadGlobalLockKey    = "ledger:export:download:global:lock"
	ledgerExportUserCooldownKeyPfx = "ledger:export:user:cooldown:"
	ledgerExportJobKeyPfx          = "ledger:export:job:"
	ledgerDownloadTokenKeyPfx      = "ledger:export:dl-token:"
	ledgerDownloadTokenTTL         = 60 * time.Second
	ledgerDownloadGlobalLockTTL    = 130 * time.Minute
)

// Lua script: only delete the lock if we own it, preventing accidental release.
const ledgerExportReleaseLockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`

const ledgerDownloadTokenConsumeScript = `
local value = redis.call("GET", KEYS[1])
if not value then
  return nil
end
redis.call("DEL", KEYS[1])
return value
`

type LedgerExportJob struct {
	JobID     string   `json:"job_id"`
	Status    string   `json:"status"`
	Progress  int      `json:"progress"`
	RowCount  int64    `json:"row_count"`
	FilePath  string   `json:"file_path,omitempty"`
	FilePaths []string `json:"file_paths,omitempty"`
	Error     string   `json:"error,omitempty"`
	CreatedAt int64    `json:"created_at"`
	UserID    int      `json:"user_id"`
}

func ledgerExportJobRedisKey(jobID string) string {
	return ledgerExportJobKeyPfx + jobID
}

func ledgerExportUserCooldownKey(userID int) string {
	return ledgerExportUserCooldownKeyPfx + strconv.Itoa(userID)
}

func LedgerExportFilePath(jobID string) string {
	return filepath.Join(os.TempDir(), ledgerExportFilePrefix+jobID+".csv.gz")
}

func LedgerExportPartFilePath(jobID string, part int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf(ledgerExportPartFilePattern, jobID, part))
}

func NewLedgerExportJobID() string {
	return uuid.New().String()
}

func CreateLedgerExportJob(jobID string, userID int) (*LedgerExportJob, error) {
	job := &LedgerExportJob{
		JobID:     jobID,
		Status:    LedgerExportStatusPending,
		CreatedAt: time.Now().Unix(),
		UserID:    userID,
	}
	return job, saveLedgerExportJob(job)
}

func saveLedgerExportJob(job *LedgerExportJob) error {
	data, err := common.Marshal(job)
	if err != nil {
		return err
	}
	return common.RedisSet(ledgerExportJobRedisKey(job.JobID), string(data), ledgerExportJobTTL)
}

func GetLedgerExportJob(jobID string) (*LedgerExportJob, error) {
	raw, err := common.RedisGet(ledgerExportJobRedisKey(jobID))
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var job LedgerExportJob
	if err := common.UnmarshalJsonStr(raw, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

func DeleteLedgerExportJob(jobID string) {
	_ = common.RedisDel(ledgerExportJobRedisKey(jobID))
}

// CreateLedgerDownloadToken creates a one-time 60-second download token tied to a job+user.
func CreateLedgerDownloadToken(jobID string, userID int) (string, error) {
	token := uuid.New().String()
	value := jobID + ":" + strconv.Itoa(userID)
	if err := common.RedisSet(ledgerDownloadTokenKeyPfx+token, value, ledgerDownloadTokenTTL); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateAndConsumeLedgerDownloadToken validates and deletes the token (one-time use).
// Returns ("", 0, nil) when the token is not found or expired.
func ValidateAndConsumeLedgerDownloadToken(token string) (jobID string, userID int, err error) {
	key := ledgerDownloadTokenKeyPfx + token
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := common.RDB.Eval(ctx, ledgerDownloadTokenConsumeScript, []string{key}).Text()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", 0, nil
		}
		return "", 0, err
	}
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid token data")
	}
	uid, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid user_id in token")
	}
	return parts[0], uid, nil
}

func IsLedgerDownloadBusy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n, err := common.RDB.Exists(ctx, ledgerDownloadGlobalLockKey).Result()
	if err != nil {
		common.SysError("IsLedgerDownloadBusy: " + err.Error())
		return false
	}
	return n > 0
}

func AcquireLedgerDownloadGlobalLock(lockID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := common.RDB.SetNX(ctx, ledgerDownloadGlobalLockKey, lockID, ledgerDownloadGlobalLockTTL).Result()
	if err != nil {
		common.SysError("AcquireLedgerDownloadGlobalLock: " + err.Error())
		return false
	}
	return ok
}

func ReleaseLedgerDownloadGlobalLock(lockID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := common.RDB.Eval(ctx, ledgerExportReleaseLockScript, []string{ledgerDownloadGlobalLockKey}, lockID).Err(); err != nil && !errors.Is(err, redis.Nil) {
		common.SysError("ReleaseLedgerDownloadGlobalLock: " + err.Error())
	}
}

func updateLedgerExportJob(job *LedgerExportJob) {
	if err := saveLedgerExportJob(job); err != nil {
		common.SysError("updateLedgerExportJob: " + err.Error())
	}
}

// AcquireLedgerExportGlobalLock acquires the global export lock, storing jobID as the lock value.
func AcquireLedgerExportGlobalLock(jobID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := common.RDB.SetNX(ctx, ledgerExportGlobalLockKey, jobID, ledgerExportGlobalLockTTL).Result()
	if err != nil {
		common.SysError("AcquireLedgerExportGlobalLock: " + err.Error())
		return false
	}
	return ok
}

// ReleaseLedgerExportGlobalLock releases the lock only when jobID matches the stored value.
func ReleaseLedgerExportGlobalLock(jobID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := common.RDB.Eval(ctx, ledgerExportReleaseLockScript, []string{ledgerExportGlobalLockKey}, jobID).Err(); err != nil && !errors.Is(err, redis.Nil) {
		common.SysError("ReleaseLedgerExportGlobalLock: " + err.Error())
	}
}

// CheckAndSetLedgerExportUserCooldown returns true if the user is on cooldown.
// On first call it sets the cooldown key; subsequent calls within the TTL return true.
func CheckAndSetLedgerExportUserCooldown(userID int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := common.RDB.SetNX(ctx, ledgerExportUserCooldownKey(userID), "1", ledgerExportUserCooldown).Result()
	if err != nil {
		common.SysError("CheckAndSetLedgerExportUserCooldown: " + err.Error())
		return false
	}
	return !ok // SETNX returns true when key is newly set (not on cooldown)
}

// CleanupOrphanLedgerExportFiles removes temp files whose Redis job keys no longer exist.
// Called passively on each new export creation.
func CleanupOrphanLedgerExportFiles() {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	pattern := filepath.Join(os.TempDir(), ledgerExportFilePrefix+"*.gz")
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		return
	}
	for _, file := range files {
		base := filepath.Base(file)
		jobID := strings.TrimPrefix(base, ledgerExportFilePrefix)
		jobID = strings.TrimSuffix(jobID, ".csv.gz")
		if idx := strings.Index(jobID, "-part-"); idx > 0 {
			jobID = jobID[:idx]
		}
		if jobID == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		n, err := common.RDB.Exists(ctx, ledgerExportJobRedisKey(jobID)).Result()
		cancel()
		if err != nil || n > 0 {
			continue
		}
		_ = os.Remove(file)
	}
}

// StartLedgerExport launches the export goroutine.
func StartLedgerExport(job *LedgerExportJob, filter ConsumptionCostLedgerCommonFilter) {
	go runLedgerExport(job, filter)
}

func runLedgerExport(job *LedgerExportJob, filter ConsumptionCostLedgerCommonFilter) {
	defer ReleaseLedgerExportGlobalLock(job.JobID)

	job.Status = LedgerExportStatusRunning
	updateLedgerExportJob(job)

	filePath := LedgerExportFilePath(job.JobID)
	ctx, cancel := context.WithTimeout(context.Background(), ledgerExportTimeout)
	defer cancel()

	rowCount, filePaths, err := writeLedgerExportCSVSharded(ctx, job.JobID, filter, filePath, func(progress int, rows int64) {
		job.Progress = progress
		job.RowCount = rows
		updateLedgerExportJob(job)
	})

	if err != nil {
		for _, path := range filePaths {
			_ = os.Remove(path)
		}
		job.Status = LedgerExportStatusFailed
		job.RowCount = rowCount
		job.Error = err.Error()
		updateLedgerExportJob(job)
		return
	}

	job.Status = LedgerExportStatusReady
	job.Progress = 100
	job.RowCount = rowCount
	job.FilePaths = filePaths
	if len(filePaths) > 0 {
		job.FilePath = filePaths[0]
	}
	updateLedgerExportJob(job)
}

func writeLedgerExportCSVSharded(
	ctx context.Context,
	jobID string,
	filter ConsumptionCostLedgerCommonFilter,
	filePath string,
	onProgress func(progress int, rows int64),
) (rowCount int64, filePaths []string, retErr error) {
	writer, err := newLedgerExportPartWriter(filePath)
	if err != nil {
		return 0, nil, err
	}
	filePaths = append(filePaths, filePath)
	defer func() {
		if writer != nil {
			if err := writer.Close(); err != nil && retErr == nil {
				retErr = err
			}
		}
	}()

	exportStart := filter.StartTime
	exportEnd := filter.EndTime
	windowEnd := exportEnd
	hasUniqueFilter := filter.Id > 0 || filter.LogId > 0

	for windowEnd > exportStart {
		windowStart := windowEnd - ledgerExportWindowSeconds
		if windowStart < exportStart {
			windowStart = exportStart
		}
		windowFilter := filter
		windowFilter.StartTime = windowStart
		windowFilter.EndTime = windowEnd

		var cursor *ConsumptionCostLedgerCursor
		listFilter := ConsumptionCostLedgerFilter{
			ConsumptionCostLedgerCommonFilter: windowFilter,
			Limit:                             ledgerExportBatchSize,
		}

		for {
			if cursor != nil {
				listFilter.CursorCreated = cursor.CreatedAt
				listFilter.CursorId = cursor.Id
			}

			page, err := ListConsumptionCostLedger(ctx, listFilter)
			if err != nil {
				return rowCount, filePaths, err
			}

			for i := range page.Items {
				if rowCount > 0 && rowCount%ledgerExportRowsPerFile == 0 {
					if err := writer.Close(); err != nil {
						return rowCount, filePaths, err
					}
					partPath := LedgerExportPartFilePath(jobID, len(filePaths)+1)
					writer, err = newLedgerExportPartWriter(partPath)
					if err != nil {
						return rowCount, filePaths, err
					}
					filePaths = append(filePaths, partPath)
				}

				item := &page.Items[i]
				if err := writer.Write(ledgerExportRecord(item)); err != nil {
					return rowCount, filePaths, err
				}
				rowCount++
			}

			progressPosition := windowStart
			if page.HasMore && page.NextCursor != nil && page.NextCursor.CreatedAt > 0 {
				progressPosition = page.NextCursor.CreatedAt
			}
			progress := 1
			if exportEnd > exportStart {
				elapsed := exportEnd - progressPosition
				total := exportEnd - exportStart
				if total > 0 && elapsed > 0 {
					if p := int(elapsed * 99 / total); p >= 1 && p <= 99 {
						progress = p
					}
				}
			}
			onProgress(progress, rowCount)

			if hasUniqueFilter && rowCount > 0 {
				return rowCount, filePaths, nil
			}
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor

			select {
			case <-ctx.Done():
				return rowCount, filePaths, ctx.Err()
			case <-time.After(ledgerExportBatchSleepMs * time.Millisecond):
			}
		}

		windowEnd = windowStart
	}

	return rowCount, filePaths, nil
}

type ledgerExportPartWriter struct {
	f  *os.File
	bw *bufio.Writer
	gz *gzip.Writer
	cw *csv.Writer
}

func newLedgerExportPartWriter(filePath string) (*ledgerExportPartWriter, error) {
	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("create export file: %w", err)
	}
	writer := &ledgerExportPartWriter{
		f:  f,
		bw: bufio.NewWriterSize(f, 4*1024*1024),
	}
	writer.gz = gzip.NewWriter(writer.bw)
	writer.cw = csv.NewWriter(writer.gz)
	if err := writer.Write([]string{
		"Time", "Tags", "Log ID", "User ID",
		"Channel ID", "Channel", "Model", "Group",
		"Revenue Quota", "Cost Quota", "Profit Quota",
		"Gross Margin", "Group Ratio", "Cost Ratio",
	}); err != nil {
		_ = writer.Close()
		_ = os.Remove(filePath)
		return nil, err
	}
	return writer, nil
}

func (w *ledgerExportPartWriter) Write(record []string) error {
	return w.cw.Write(record)
}

func (w *ledgerExportPartWriter) Close() error {
	w.cw.Flush()
	if err := w.cw.Error(); err != nil {
		_ = w.f.Close()
		return err
	}
	if err := w.gz.Close(); err != nil {
		_ = w.f.Close()
		return err
	}
	if err := w.bw.Flush(); err != nil {
		_ = w.f.Close()
		return err
	}
	return w.f.Close()
}

func ledgerExportRecord(item *ConsumptionCostLedgerItem) []string {
	var grossMargin, logID string
	if item.GrossMargin != nil {
		grossMargin = fmt.Sprintf("%.6f", *item.GrossMargin)
	}
	if item.LogId != nil {
		logID = strconv.Itoa(*item.LogId)
	}
	return []string{
		time.Unix(item.CreatedAt, 0).Format("2006-01-02 15:04:05"),
		strings.Join(item.Tags, "|"),
		logID,
		strconv.Itoa(item.UserId),
		strconv.Itoa(item.ChannelId),
		item.ChannelName,
		item.ModelName,
		item.GroupName,
		strconv.FormatInt(item.RevenueQuota, 10),
		strconv.FormatInt(item.CostQuota, 10),
		strconv.FormatInt(item.ProfitQuota, 10),
		grossMargin,
		fmt.Sprintf("%.6f", item.GroupRatio),
		fmt.Sprintf("%.6f", item.CostRatio),
	}
}
