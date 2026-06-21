package controller

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const ledgerExportMaxRangeSeconds = model.LedgerExportMaxRangeSeconds

func AdminCreateLedgerExport(c *gin.Context) {
	if !ledgerExportRequireRedis(c) {
		return
	}

	userID := getLedgerRequestUserID(c)

	filter, err := parseConsumptionCostLedgerExportFilter(c)
	if err != nil {
		ledgerJSONError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Passive orphan file cleanup triggered by each new job creation.
	// Run in goroutine so slow Redis checks don't block this request.
	go model.CleanupOrphanLedgerExportFiles()

	// Generate the job ID upfront so it can serve as the lock value.
	jobID := model.NewLedgerExportJobID()

	// Acquire the global lock first — don't burn the user's cooldown if the
	// system is already busy with another export.
	if !model.AcquireLedgerExportGlobalLock(jobID) {
		ledgerJSONError(c, http.StatusTooManyRequests, "An export task is already running, please wait for it to complete.")
		return
	}

	// Check per-user cooldown only after the lock is confirmed available.
	if model.CheckAndSetLedgerExportUserCooldown(userID) {
		model.ReleaseLedgerExportGlobalLock(jobID)
		ledgerJSONError(c, http.StatusTooManyRequests, "Export request too frequent, please wait 5 minutes.")
		return
	}

	job, err := model.CreateLedgerExportJob(jobID, userID)
	if err != nil {
		model.ReleaseLedgerExportGlobalLock(jobID)
		ledgerJSONError(c, http.StatusInternalServerError, "Failed to create export job.")
		return
	}

	model.StartLedgerExport(job, filter)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"job_id": job.JobID},
	})
}

func AdminGetLedgerExport(c *gin.Context) {
	if !ledgerExportRequireRedis(c) {
		return
	}

	jobID := c.Param("job_id")
	userID := getLedgerRequestUserID(c)

	job, err := model.GetLedgerExportJob(jobID)
	if err != nil {
		ledgerJSONError(c, http.StatusInternalServerError, "Failed to get export job.")
		return
	}
	if job == nil {
		ledgerJSONError(c, http.StatusNotFound, "Export job not found or expired.")
		return
	}
	if job.UserID != userID {
		ledgerJSONError(c, http.StatusForbidden, "Access denied.")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"job_id":    job.JobID,
			"status":    job.Status,
			"progress":  job.Progress,
			"row_count": job.RowCount,
			"error":     job.Error,
		},
	})
}

// AdminGetLedgerExportDownloadURL issues a one-time 60-second download token.
// Called via axios (with proper auth headers); the returned URL can then be
// opened by the browser directly without any custom headers.
func AdminGetLedgerExportDownloadURL(c *gin.Context) {
	if !ledgerExportRequireRedis(c) {
		return
	}

	jobID := c.Param("job_id")
	userID := getLedgerRequestUserID(c)

	job, err := model.GetLedgerExportJob(jobID)
	if err != nil {
		ledgerJSONError(c, http.StatusInternalServerError, "Failed to get export job.")
		return
	}
	if job == nil {
		ledgerJSONError(c, http.StatusNotFound, "Export job not found or expired.")
		return
	}
	if job.UserID != userID {
		ledgerJSONError(c, http.StatusForbidden, "Access denied.")
		return
	}
	if job.Status != model.LedgerExportStatusReady {
		ledgerJSONError(c, http.StatusBadRequest, fmt.Sprintf("Export is not ready (status: %s).", job.Status))
		return
	}
	if model.IsLedgerDownloadBusy() {
		ledgerJSONError(c, http.StatusTooManyRequests, "Another ledger download is already running, please wait for it to complete.")
		return
	}

	token, err := model.CreateLedgerDownloadToken(jobID, userID)
	if err != nil {
		ledgerJSONError(c, http.StatusInternalServerError, "Failed to create download token.")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"url": fmt.Sprintf("/dl/ledger/%s", token)},
	})
}

// AdminDownloadLedgerExport is mounted OUTSIDE the gzip/AdminAuth groups.
// Auth is provided by the one-time token in the URL path (no custom headers needed).
func AdminDownloadLedgerExport(c *gin.Context) {
	if !ledgerExportRequireRedis(c) {
		return
	}

	token := c.Param("token")
	lockID := token
	if !model.AcquireLedgerDownloadGlobalLock(lockID) {
		ledgerJSONError(c, http.StatusTooManyRequests, "Another ledger download is already running, please wait for it to complete.")
		return
	}
	defer model.ReleaseLedgerDownloadGlobalLock(lockID)

	jobID, userID, err := model.ValidateAndConsumeLedgerDownloadToken(token)
	if err != nil {
		ledgerJSONError(c, http.StatusInternalServerError, "Failed to validate download token.")
		return
	}
	if jobID == "" {
		ledgerJSONError(c, http.StatusNotFound, "Download token not found or expired.")
		return
	}

	job, err := model.GetLedgerExportJob(jobID)
	if err != nil {
		ledgerJSONError(c, http.StatusInternalServerError, "Failed to get export job.")
		return
	}
	if job == nil || job.UserID != userID || job.Status != model.LedgerExportStatusReady {
		ledgerJSONError(c, http.StatusNotFound, "Export job not available.")
		return
	}

	filePaths := job.FilePaths
	if len(filePaths) == 0 && job.FilePath != "" {
		filePaths = []string{job.FilePath}
	}
	if len(filePaths) == 0 {
		ledgerJSONError(c, http.StatusInternalServerError, "Export file unavailable.")
		return
	}

	copyErr := streamLedgerExportFiles(c, filePaths)

	// Only clean up on success — preserve file on error so admin can investigate.
	if copyErr == nil {
		model.DeleteLedgerExportJob(jobID)
		for _, path := range filePaths {
			_ = os.Remove(path)
		}
	}
}

func streamLedgerExportFiles(c *gin.Context, filePaths []string) error {
	filenameSuffix := time.Now().Format("20060102-150405")
	c.Header("Cache-Control", "no-store")

	if len(filePaths) == 1 {
		f, err := os.Open(filePaths[0])
		if err != nil {
			ledgerJSONError(c, http.StatusInternalServerError, "Export file unavailable.")
			return err
		}
		defer f.Close()

		filename := fmt.Sprintf("ledger-export-%s.csv.gz", filenameSuffix)
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		c.Header("Content-Type", "application/gzip")
		if info, err := f.Stat(); err == nil {
			c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
		}
		_, err = io.Copy(c.Writer, f)
		return err
	}

	filename := fmt.Sprintf("ledger-export-%s.zip", filenameSuffix)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", "application/zip")
	zw := zip.NewWriter(c.Writer)
	for i, path := range filePaths {
		f, err := os.Open(path)
		if err != nil {
			_ = zw.Close()
			return err
		}
		partName := fmt.Sprintf("ledger-export-%s-part-%04d.csv.gz", filenameSuffix, i+1)
		part, err := zw.Create(partName)
		if err != nil {
			_ = f.Close()
			_ = zw.Close()
			return err
		}
		if _, err := io.Copy(part, f); err != nil {
			_ = f.Close()
			_ = zw.Close()
			return err
		}
		if err := f.Close(); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}

func parseConsumptionCostLedgerExportFilter(c *gin.Context) (model.ConsumptionCostLedgerCommonFilter, error) {
	var (
		err    error
		filter model.ConsumptionCostLedgerCommonFilter
	)

	filter.Id, err = parseOptionalIntQuery(c, "id")
	if err != nil {
		return filter, err
	}
	filter.LogId, err = parseOptionalIntQuery(c, "log_id")
	if err != nil {
		return filter, err
	}
	filter.UserId, err = parseOptionalIntQuery(c, "user_id")
	if err != nil {
		return filter, err
	}
	filter.ChannelId, err = parseOptionalIntQuery(c, "channel_id")
	if err != nil {
		return filter, err
	}
	filter.ModelName = strings.TrimSpace(c.Query("model_name"))
	filter.GroupName = strings.TrimSpace(c.Query("group_name"))
	filter.Tag = strings.TrimSpace(c.Query("tag"))
	if !model.ValidConsumptionCostLedgerTag(filter.Tag) {
		return filter, errors.New("invalid tag")
	}

	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		return filter, err
	}
	filter.StartTime = timeRange.StartTime
	filter.EndTime = timeRange.EndTime

	hasUniqueFilter := filter.Id > 0 || filter.LogId > 0
	if filter.StartTime <= 0 || filter.EndTime <= 0 {
		if !hasUniqueFilter {
			return filter, errors.New("start_time and end_time are required")
		}
		// Unique-ID query: use a wide default range so the cursor scan can find the record.
		filter.EndTime = time.Now().Unix()
		filter.StartTime = filter.EndTime - ledgerExportMaxRangeSeconds
	}
	if filter.EndTime <= filter.StartTime {
		return filter, errors.New("end_time must be greater than start_time")
	}
	if !hasUniqueFilter && filter.EndTime-filter.StartTime > ledgerExportMaxRangeSeconds {
		return filter, errors.New("time range cannot exceed 24 hours")
	}

	return filter, nil
}

func ledgerExportRequireRedis(c *gin.Context) bool {
	if !common.RedisEnabled || common.RDB == nil {
		ledgerJSONError(c, http.StatusServiceUnavailable, "Export feature requires Redis.")
		return false
	}
	return true
}
