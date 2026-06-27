package service

// business_stats_backfill.go — admin-triggered fallback-log backfill service.
//
// Architecture summary:
//   - GetFallbackFileStatus  – lightweight os.Stat with process-local TTL cache.
//   - TriggerBackfill        – starts a single-instance background task goroutine.
//   - GetBackfillResult      – returns the last task result (or running state).
//   - runBackfillTask        – reads the file, batches records, flushes, deletes.
//
// Rule 0: nothing here runs on the relay hot-path. All DB writes are in the
// admin-triggered goroutine with controlled batch sizes and inter-flush sleeps.
// Rule 1: all JSON ops go through common.Unmarshal / common.Marshal.
// Rule 10: background goroutine uses common.SysLog/SysError (no request context).

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// ─── Fallback file status ────────────────────────────────────────────────────

// FallbackFileStatus is returned by GetFallbackFileStatus and embedded as
// fallback_hint in ledger query responses.
type FallbackFileStatus struct {
	HasFile     bool   `json:"has_file"`
	Date        string `json:"date,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
	UpdatedAt   int64  `json:"updated_at,omitempty"`
	CanBackfill bool   `json:"can_backfill"`
}

type fallbackStatCacheEntry struct {
	status    FallbackFileStatus
	expiresAt time.Time
}

var (
	fallbackStatCacheMu sync.Mutex
	fallbackStatCache   = make(map[string]*fallbackStatCacheEntry) // key: date "2006-01-02"
)

// GetFallbackFileStatus returns the file status for a historical date, using a
// short process-local cache to avoid hitting the filesystem on every ledger poll.
//
// Returns an empty FallbackFileStatus{} (has_file=false) when:
//   - the feature is disabled
//   - date is today or in the future
//   - date format is invalid
func GetFallbackFileStatus(date string) FallbackFileStatus {
	cfg := operation_setting.GetBusinessStatsFallbackBackfillSetting()
	if !cfg.Enabled {
		return FallbackFileStatus{HasFile: false, CanBackfill: false}
	}
	if !isHistoricalDate(date) {
		return FallbackFileStatus{HasFile: false, CanBackfill: false}
	}

	ttl := time.Duration(cfg.StatusCacheSeconds) * time.Second

	fallbackStatCacheMu.Lock()
	if entry, ok := fallbackStatCache[date]; ok && time.Now().Before(entry.expiresAt) {
		status := entry.status
		fallbackStatCacheMu.Unlock()
		return status
	}
	fallbackStatCacheMu.Unlock()

	status := statFallbackFile(date)

	fallbackStatCacheMu.Lock()
	fallbackStatCache[date] = &fallbackStatCacheEntry{
		status:    status,
		expiresAt: time.Now().Add(ttl),
	}
	fallbackStatCacheMu.Unlock()

	return status
}

// InvalidateFallbackFileStatusCache removes the cached entry for date so that
// the next call will re-stat the file.  Called after the backfill task deletes
// (or fails to delete) the file.
func InvalidateFallbackFileStatusCache(date string) {
	fallbackStatCacheMu.Lock()
	delete(fallbackStatCache, date)
	fallbackStatCacheMu.Unlock()
}

func statFallbackFile(date string) FallbackFileStatus {
	dir := model.FallbackLogDir()
	name := model.FallbackFilename(date)
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		// File does not exist or cannot be stated — treat as no file.
		return FallbackFileStatus{HasFile: false, CanBackfill: false}
	}
	return FallbackFileStatus{
		HasFile:     true,
		Date:        date,
		FileName:    name,
		FileSize:    info.Size(),
		UpdatedAt:   info.ModTime().Unix(),
		CanBackfill: true,
	}
}

// isHistoricalDate returns true iff date (format "2006-01-02") is strictly
// before today in the configured business-stats timezone.
func isHistoricalDate(date string) bool {
	cfg := operation_setting.GetCommissionTierResetSetting()
	loc, _ := operation_setting.ResolveCommissionTierResetLocation(cfg.Timezone)
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return false
	}
	// Compare date-only start-of-day timestamps.
	todayStart := model.BusinessDayStart(time.Now().Unix())
	return t.Unix() < todayStart
}

// IsSingleHistoricalDate returns true iff date (format "2006-01-02") is strictly
// before today in the configured business-stats timezone.  This is the exported
// version of isHistoricalDate for use by the controller layer.
func IsSingleHistoricalDate(date string) bool {
	return isHistoricalDate(date)
}

// IsSingleHistoricalDayFilter checks whether the filter covers exactly one
// calendar day (in the configured timezone) that is strictly before today.
// Returns the date string and true when the condition is met.
func IsSingleHistoricalDayFilter(startTime, endTime int64) (date string, ok bool) {
	if startTime <= 0 || endTime <= 0 || endTime <= startTime {
		return "", false
	}
	startDay := model.BusinessDayStart(startTime)
	// endTime is typically the exclusive upper bound (start of next day), so we
	// probe endTime-1 to get the last second that belongs to the query range.
	endDay := model.BusinessDayStart(endTime - 1)
	if startDay != endDay {
		return "", false
	}
	todayStart := model.BusinessDayStart(time.Now().Unix())
	if startDay >= todayStart {
		return "", false
	}
	dateStr := model.BusinessDayDate(startDay)
	return dateStr, true
}

// ─── Backfill task state ─────────────────────────────────────────────────────

// BackfillResult summarises the outcome of the most recent (or running) task.
//
// Success is the overall outcome flag the UI keys off: true once the task has
// finished without any flush/scan error (LastError == ""), false while running or
// when it finished with an error.  It is distinct from SuccessCount, which is the
// number of records actually written to the DB.  Both are reported because a clean
// run can legitimately write zero new rows (e.g. a re-run where every record was
// already present).
type BackfillResult struct {
	Running      bool   `json:"running"`
	Success      bool   `json:"success"`
	Date         string `json:"date,omitempty"`
	TotalLines   int    `json:"total_lines"`
	SuccessCount int    `json:"success_count"`
	DiscardCount int    `json:"discard_count"`
	FileDeleted  bool   `json:"file_deleted"`
	StartedAt    int64  `json:"started_at,omitempty"`
	FinishedAt   int64  `json:"finished_at,omitempty"`
	LastError    string `json:"last_error,omitempty"`
}

var (
	backfillMu     sync.Mutex
	backfillResult BackfillResult
)

// TriggerBackfill starts the backfill task for the given historical date in a
// new goroutine.  Returns an error if the feature is disabled, the date is not
// historical, or a task is already running.
func TriggerBackfill(date string) error {
	cfg := operation_setting.GetBusinessStatsFallbackBackfillSetting()
	if !cfg.Enabled {
		return errBackfillDisabled
	}
	if !isHistoricalDate(date) {
		return errBackfillDateMustBeHistorical
	}
	if status := statFallbackFile(date); !status.HasFile {
		return errBackfillFileNotFound
	}

	backfillMu.Lock()
	defer backfillMu.Unlock()
	if backfillResult.Running {
		return errBackfillAlreadyRunning
	}
	backfillResult = BackfillResult{
		Running:   true,
		Date:      date,
		StartedAt: time.Now().Unix(),
	}
	go runBackfillTask(date)
	return nil
}

// GetBackfillResult returns a snapshot of the current task result.
func GetBackfillResult() BackfillResult {
	backfillMu.Lock()
	defer backfillMu.Unlock()
	return backfillResult
}

// sentinel errors used by controller to select the correct i18n key
var (
	errBackfillDisabled             = fmt.Errorf("backfill_disabled")
	errBackfillDateMustBeHistorical = fmt.Errorf("backfill_date_must_be_historical")
	errBackfillAlreadyRunning       = fmt.Errorf("backfill_already_running")
	errBackfillFileNotFound         = fmt.Errorf("backfill_file_not_found")
)

// IsErrBackfill* let the controller match sentinel errors without importing private vars.
func IsErrBackfillDisabled(err error) bool             { return err == errBackfillDisabled }
func IsErrBackfillDateMustBeHistorical(err error) bool { return err == errBackfillDateMustBeHistorical }
func IsErrBackfillAlreadyRunning(err error) bool       { return err == errBackfillAlreadyRunning }
func IsErrBackfillFileNotFound(err error) bool         { return err == errBackfillFileNotFound }

// ─── Backfill task ───────────────────────────────────────────────────────────

// fallbackLogLine is the JSON structure written by writeBusinessStatsFallback.
type fallbackLogLine struct {
	Time    int64          `json:"time"`
	Kind    string         `json:"kind"`
	Reason  string         `json:"reason"`
	Payload map[string]any `json:"payload"`
}

// rawCostPayload is the shape stored under payload["cost"] for both
// business_stats_skipped and cost_commission_create entries.
type rawCostPayload struct {
	LogId        *int    `json:"log_id"`
	UserId       int     `json:"user_id"`
	ChannelId    int     `json:"channel_id"`
	ChannelName  string  `json:"channel_name"`
	GroupName    string  `json:"group_name"`
	ModelName    string  `json:"model_name"`
	RevenueQuota int64   `json:"revenue_quota"`
	CostQuota    int64   `json:"cost_quota"`
	GroupRatio   float64 `json:"group_ratio"`
	CostRatio    float64 `json:"cost_ratio"`
	CreatedAt    int64   `json:"created_at"`
}

// rawCommissionPayload is the shape stored under payload["commission"].
type rawCommissionPayload struct {
	EmployeeId      int     `json:"employee_id"`
	EmployeeUserId  int     `json:"employee_user_id"`
	CustomerUserId  int     `json:"customer_user_id"`
	LogId           *int    `json:"log_id"`
	ModelName       string  `json:"model_name"`
	ChannelId       int     `json:"channel_id"`
	RevenueQuota    int64   `json:"revenue_quota"`
	CostQuota       int64   `json:"cost_quota"`
	ProfitQuota     int64   `json:"profit_quota"`
	CommissionQuota int64   `json:"commission_quota"`
	CommissionRate  float64 `json:"commission_rate"`
	CostRatio       float64 `json:"cost_ratio"`
	GroupRatio      float64 `json:"group_ratio"`
	CreatedAt       int64   `json:"created_at"`
}

func runBackfillTask(date string) {
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("backfill_task: panic recovered date=%s: %v", date, r)
			common.SysError(msg)
			finishBackfill(msg, false)
		}
	}()

	common.SysLog(fmt.Sprintf("backfill_task: started date=%s", date))

	dir := model.FallbackLogDir()
	name := model.FallbackFilename(date)
	path := filepath.Join(dir, name)

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			common.SysLog(fmt.Sprintf("backfill_task: file not found date=%s path=%s", date, path))
			InvalidateFallbackFileStatusCache(date)
			finishBackfill(errBackfillFileNotFound.Error(), false)
			return
		}
		common.SysError(fmt.Sprintf("backfill_task: open failed date=%s: %s", date, err.Error()))
		finishBackfill(err.Error(), false)
		return
	}
	defer f.Close()

	cfg := operation_setting.GetBusinessStatsFallbackBackfillSetting()

	writer := &backfillLedgerWriter{}
	flushCtrl := &backfillFlushController{lastFlushAt: time.Now()}

	lookupCache := newBackfillLookupCache()

	var (
		totalLines   int
		successCount int
		discardCount int
		lastErr      string
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, cfg.MaxReadLineBytes), cfg.MaxReadLineBytes)

	for scanner.Scan() {
		// Re-read config each iteration so admin changes take effect immediately.
		cfg = operation_setting.GetBusinessStatsFallbackBackfillSetting()

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		totalLines++

		var entry fallbackLogLine
		if err := common.Unmarshal(line, &entry); err != nil {
			common.SysLog(fmt.Sprintf("backfill_task: json parse error date=%s line=%d: %s", date, totalLines, err.Error()))
			discardCount++
			updateBackfillProgress(totalLines, successCount, discardCount)
			continue
		}

		handled, errMsg := processBackfillEntry(entry, writer, lookupCache)
		if !handled {
			if errMsg != "" {
				common.SysLog(fmt.Sprintf("backfill_task: discard date=%s line=%d: %s", date, totalLines, errMsg))
			}
			discardCount++
		}
		// successCount is incremented only after a successful flush; see below.
		updateBackfillProgress(totalLines, successCount, discardCount)

		// Flush if batch thresholds are met.
		if flushCtrl.shouldFlush(writer, cfg) {
			flushed, fErr := writer.flush(cfg)
			successCount += flushed // count records that actually reached the DB
			if fErr != nil {
				common.SysError(fmt.Sprintf("backfill_task: flush error date=%s: %s", date, fErr.Error()))
				lastErr = fErr.Error()
			}
			flushCtrl.recordFlush()
			time.Sleep(time.Duration(cfg.BatchSleepMs) * time.Millisecond)
		}
	}

	if err := scanner.Err(); err != nil {
		common.SysError(fmt.Sprintf("backfill_task: scan error date=%s: %s", date, err.Error()))
		finishBackfillFull(totalLines, successCount, discardCount, false, err.Error())
		return
	}

	// Final drain flush.
	if writer.hasData() {
		flushed, fErr := writer.flush(cfg)
		successCount += flushed
		if fErr != nil {
			common.SysError(fmt.Sprintf("backfill_task: final flush error date=%s: %s", date, fErr.Error()))
			lastErr = fErr.Error()
		}
	}

	// Close the file before attempting removal.  On Windows os.Remove fails with
	// "being used by another process" while the handle is still open, so relying on
	// the deferred Close (which only runs after this function returns) would leave
	// the fallback file undeleted and the admin banner showing forever.  The
	// deferred Close above remains as a safety net for the early-return error paths;
	// a second Close here is a harmless no-op.
	_ = f.Close()

	// Delete file only when all flushes succeeded.  If any flush failed, keep the
	// file so the admin can re-trigger; records that were already written will be
	// skipped by ON CONFLICT DO NOTHING on re-run (for records with a valid log_id).
	fileDeleted := false
	if lastErr == "" {
		if delErr := os.Remove(path); delErr != nil {
			common.SysError(fmt.Sprintf("backfill_task: delete file failed date=%s: %s", date, delErr.Error()))
			lastErr = "file deletion failed: " + delErr.Error()
		} else {
			fileDeleted = true
			common.SysLog(fmt.Sprintf("backfill_task: file deleted date=%s path=%s", date, path))
		}
	} else {
		common.SysLog(fmt.Sprintf("backfill_task: file retained due to flush error date=%s", date))
	}
	// Invalidate the stat cache so the next ledger query reflects reality.
	InvalidateFallbackFileStatusCache(date)

	common.SysLog(fmt.Sprintf(
		"backfill_task: finished date=%s total=%d success=%d discard=%d file_deleted=%v",
		date, totalLines, successCount, discardCount, fileDeleted,
	))
	finishBackfillFull(totalLines, successCount, discardCount, fileDeleted, lastErr)
}

// processBackfillEntry classifies a single log line and pushes it into the
// appropriate buffer.  Returns (true, "") on success and (false, reason) on discard.
func processBackfillEntry(
	entry fallbackLogLine,
	writer *backfillLedgerWriter,
	lookupCache *backfillLookupCache,
) (handled bool, discardReason string) {
	switch entry.Kind {
	case "cost_commission_create":
		return processExactPairEntry(entry, writer)
	case "business_stats_skipped":
		return processCostOnlyEntry(entry, writer, lookupCache)
	default:
		return false, fmt.Sprintf("unsupported kind=%s", entry.Kind)
	}
}

// processExactPairEntry handles "cost_commission_create" entries that carry
// both a cost and a commission object in the payload.
func processExactPairEntry(entry fallbackLogLine, writer *backfillLedgerWriter) (bool, string) {
	costRaw, hasCost := entry.Payload["cost"]
	commRaw, hasComm := entry.Payload["commission"]
	if !hasCost || !hasComm {
		return false, "cost_commission_create: missing cost or commission in payload"
	}

	costBytes, err := common.Marshal(costRaw)
	if err != nil {
		return false, "cost_commission_create: marshal cost: " + err.Error()
	}
	commBytes, err := common.Marshal(commRaw)
	if err != nil {
		return false, "cost_commission_create: marshal commission: " + err.Error()
	}

	var cp rawCostPayload
	if err := common.Unmarshal(costBytes, &cp); err != nil {
		return false, "cost_commission_create: unmarshal cost: " + err.Error()
	}
	var comm rawCommissionPayload
	if err := common.Unmarshal(commBytes, &comm); err != nil {
		return false, "cost_commission_create: unmarshal commission: " + err.Error()
	}

	if cp.CreatedAt == 0 {
		cp.CreatedAt = entry.Time
	}
	if comm.CreatedAt == 0 {
		comm.CreatedAt = cp.CreatedAt
	}

	costRec := rawCostToModel(cp)
	commRec := rawCommToModel(comm)
	writer.addPair(costRec, commRec)
	return true, ""
}

// processCostOnlyEntry handles "business_stats_skipped" entries that carry only
// a cost object.  It attempts to re-derive employee attribution and commission.
func processCostOnlyEntry(
	entry fallbackLogLine,
	writer *backfillLedgerWriter,
	lookupCache *backfillLookupCache,
) (bool, string) {
	if lookupCache == nil {
		lookupCache = newBackfillLookupCache()
	}
	costRaw, hasCost := entry.Payload["cost"]
	if !hasCost {
		return false, "business_stats_skipped: missing cost in payload"
	}

	costBytes, err := common.Marshal(costRaw)
	if err != nil {
		return false, "business_stats_skipped: marshal cost: " + err.Error()
	}
	var cp rawCostPayload
	if err := common.Unmarshal(costBytes, &cp); err != nil {
		return false, "business_stats_skipped: unmarshal cost: " + err.Error()
	}
	if cp.CreatedAt == 0 {
		cp.CreatedAt = entry.Time
	}

	costRec := rawCostToModel(cp)

	// Attempt employee attribution using existing lookup logic.
	ctx := context.Background()
	inviterId, err := lookupCache.getInviterID(ctx, cp.UserId)
	if err != nil || inviterId == 0 || inviterId == cp.UserId {
		// No employee attribution – cost only.
		writer.addCostOnly(costRec)
		return true, ""
	}

	mutual, err := lookupCache.isMutualInvitation(ctx, cp.UserId, inviterId)
	if err != nil || mutual {
		writer.addCostOnly(costRec)
		return true, ""
	}

	emp, err := lookupCache.getEmployee(ctx, inviterId)
	if err != nil || emp == nil {
		writer.addCostOnly(costRec)
		return true, ""
	}

	// Look up commission rate snapshot for this employee on this business day.
	statDate := model.BusinessDayStart(cp.CreatedAt)
	rate, ok := lookupCache.getCommissionRate(ctx, emp.UserId, statDate)
	if !ok {
		writer.addCostOnly(costRec)
		return true, ""
	}

	revenueQuota := costRec.RevenueQuota
	costQuota := costRec.CostQuota
	profitQuota := revenueQuota - costQuota
	commissionQuota := calcCommissionQuota(profitQuota, rate)

	commRec := &model.EmployeeCommissionLog{
		EmployeeId:      emp.Id,
		EmployeeUserId:  emp.UserId,
		CustomerUserId:  cp.UserId,
		LogId:           costRec.LogId,
		ModelName:       cp.ModelName,
		ChannelId:       cp.ChannelId,
		RevenueQuota:    revenueQuota,
		CostQuota:       costQuota,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		CommissionRate:  rate,
		CostRatio:       cp.CostRatio,
		GroupRatio:      cp.GroupRatio,
		CreatedAt:       cp.CreatedAt,
	}
	writer.addPair(costRec, commRec)
	return true, ""
}

type backfillLookupCache struct {
	inviterByUserID       map[int]int
	mutualByUserPair      map[string]bool
	employeeByUserID      map[int]*model.EmployeeProfile
	rateByEmployeeDay     map[string]float64
	currentRateByEmployee map[int]float64
}

func newBackfillLookupCache() *backfillLookupCache {
	return &backfillLookupCache{
		inviterByUserID:       make(map[int]int),
		mutualByUserPair:      make(map[string]bool),
		employeeByUserID:      make(map[int]*model.EmployeeProfile),
		rateByEmployeeDay:     make(map[string]float64),
		currentRateByEmployee: make(map[int]float64),
	}
}

func (c *backfillLookupCache) getInviterID(ctx context.Context, userID int) (int, error) {
	if inviterID, ok := c.inviterByUserID[userID]; ok {
		return inviterID, nil
	}
	inviterID, err := model.GetUserInviterIdWithContext(ctx, userID)
	if err != nil {
		return 0, err
	}
	c.inviterByUserID[userID] = inviterID
	return inviterID, nil
}

func (c *backfillLookupCache) isMutualInvitation(ctx context.Context, userID, inviterID int) (bool, error) {
	key := fmt.Sprintf("%d:%d", userID, inviterID)
	if mutual, ok := c.mutualByUserPair[key]; ok {
		return mutual, nil
	}
	mutual, err := model.IsMutualInvitationWithContext(ctx, userID, inviterID)
	if err != nil {
		return false, err
	}
	c.mutualByUserPair[key] = mutual
	return mutual, nil
}

func (c *backfillLookupCache) getEmployee(ctx context.Context, inviterID int) (*model.EmployeeProfile, error) {
	if emp, ok := c.employeeByUserID[inviterID]; ok {
		return emp, nil
	}
	emp, err := model.GetEmployeeByUserIdWithContext(ctx, inviterID)
	if err != nil {
		return nil, err
	}
	c.employeeByUserID[inviterID] = emp
	return emp, nil
}

func (c *backfillLookupCache) getCommissionRate(ctx context.Context, employeeUserID int, statDate int64) (float64, bool) {
	key := fmt.Sprintf("%d:%d", employeeUserID, statDate)
	if rate, ok := c.rateByEmployeeDay[key]; ok {
		return rate, true
	}
	if rate, found := model.GetCommissionRateSnapshotForDay(ctx, employeeUserID, statDate); found {
		c.rateByEmployeeDay[key] = rate
		return rate, true
	}
	if rate, ok := c.currentRateByEmployee[employeeUserID]; ok {
		c.rateByEmployeeDay[key] = rate
		return rate, true
	}
	rate, err := model.GetEffectiveCommissionRateWithContext(ctx, employeeUserID)
	if err != nil {
		return 0, false
	}
	c.currentRateByEmployee[employeeUserID] = rate
	c.rateByEmployeeDay[key] = rate
	return rate, true
}

// ─── Backfill ledger writer ───────────────────────────────────────────────────

type backfillLedgerWriter struct {
	costOnlyBuf []*model.ConsumptionCost
	pairBuf     []*model.CostCommissionBackfillPair
}

func (w *backfillLedgerWriter) addCostOnly(c *model.ConsumptionCost) {
	w.costOnlyBuf = append(w.costOnlyBuf, c)
}

func (w *backfillLedgerWriter) addPair(c *model.ConsumptionCost, l *model.EmployeeCommissionLog) {
	w.pairBuf = append(w.pairBuf, &model.CostCommissionBackfillPair{Cost: c, Commission: l})
}

func (w *backfillLedgerWriter) hasData() bool {
	return len(w.costOnlyBuf) > 0 || len(w.pairBuf) > 0
}

// flush writes buffered records to the DB and returns the number of records that
// were actually inserted — rows skipped by ON CONFLICT DO NOTHING (already present
// from a previous run) are NOT counted, so successCount reflects real new writes
// rather than how many records were submitted to the flush.
// On partial failure (costs flush but pairs fail), the count of already-inserted
// cost records is still returned so the caller can credit them in successCount.
func (w *backfillLedgerWriter) flush(cfg *operation_setting.BusinessStatsFallbackBackfillSetting) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	batchSize := cfg.WriteBatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	flushed := 0
	if len(w.costOnlyBuf) > 0 {
		n, err := model.BackfillFlushCosts(ctx, w.costOnlyBuf, batchSize)
		if err != nil {
			return flushed, err
		}
		flushed += n
		w.costOnlyBuf = nil
	}
	if len(w.pairBuf) > 0 {
		n, err := model.BackfillFlushPairs(ctx, w.pairBuf, batchSize)
		if err != nil {
			return flushed, err
		}
		flushed += n
		w.pairBuf = nil
	}
	return flushed, nil
}

// ─── Flush controller ────────────────────────────────────────────────────────

type backfillFlushController struct {
	lastFlushAt time.Time
}

func (fc *backfillFlushController) shouldFlush(
	w *backfillLedgerWriter,
	cfg *operation_setting.BusinessStatsFallbackBackfillSetting,
) bool {
	if !w.hasData() {
		return false
	}
	total := len(w.costOnlyBuf) + len(w.pairBuf)
	if cfg.WriteBatchSize > 0 && total >= cfg.WriteBatchSize {
		return true
	}
	// FlushIntervalSec <= 0 means "size-only" mode: skip time-based flush to avoid
	// flushing on every iteration (time.Since is always >= 0 when interval is 0).
	if cfg.FlushIntervalSec > 0 && time.Since(fc.lastFlushAt) >= time.Duration(cfg.FlushIntervalSec)*time.Second {
		return true
	}
	return false
}

func (fc *backfillFlushController) recordFlush() {
	fc.lastFlushAt = time.Now()
}

// ─── State helpers ────────────────────────────────────────────────────────────

func updateBackfillProgress(total, success, discard int) {
	backfillMu.Lock()
	backfillResult.TotalLines = total
	backfillResult.SuccessCount = success
	backfillResult.DiscardCount = discard
	backfillMu.Unlock()
}

func finishBackfill(lastErr string, fileDeleted bool) {
	backfillMu.Lock()
	backfillResult.Running = false
	backfillResult.Success = lastErr == ""
	backfillResult.FinishedAt = time.Now().Unix()
	backfillResult.FileDeleted = fileDeleted
	if lastErr != "" {
		backfillResult.LastError = lastErr
	}
	backfillMu.Unlock()
}

func finishBackfillFull(total, success, discard int, fileDeleted bool, lastErr string) {
	backfillMu.Lock()
	backfillResult.Running = false
	backfillResult.Success = lastErr == ""
	backfillResult.TotalLines = total
	backfillResult.SuccessCount = success
	backfillResult.DiscardCount = discard
	backfillResult.FileDeleted = fileDeleted
	backfillResult.FinishedAt = time.Now().Unix()
	if lastErr != "" {
		backfillResult.LastError = lastErr
	}
	backfillMu.Unlock()
}

// ─── Conversion helpers ───────────────────────────────────────────────────────

func rawCostToModel(cp rawCostPayload) *model.ConsumptionCost {
	ts := cp.CreatedAt
	if ts == 0 {
		ts = time.Now().Unix()
	}
	return &model.ConsumptionCost{
		LogId:        cp.LogId,
		UserId:       cp.UserId,
		ChannelId:    cp.ChannelId,
		ChannelName:  cp.ChannelName,
		GroupName:    cp.GroupName,
		ModelName:    cp.ModelName,
		RevenueQuota: cp.RevenueQuota,
		CostQuota:    cp.CostQuota,
		GroupRatio:   cp.GroupRatio,
		CostRatio:    cp.CostRatio,
		CreatedAt:    ts,
	}
}

func rawCommToModel(c rawCommissionPayload) *model.EmployeeCommissionLog {
	ts := c.CreatedAt
	if ts == 0 {
		ts = time.Now().Unix()
	}
	return &model.EmployeeCommissionLog{
		EmployeeId:      c.EmployeeId,
		EmployeeUserId:  c.EmployeeUserId,
		CustomerUserId:  c.CustomerUserId,
		LogId:           c.LogId,
		ModelName:       c.ModelName,
		ChannelId:       c.ChannelId,
		RevenueQuota:    c.RevenueQuota,
		CostQuota:       c.CostQuota,
		ProfitQuota:     c.ProfitQuota,
		CommissionQuota: c.CommissionQuota,
		CommissionRate:  c.CommissionRate,
		CostRatio:       c.CostRatio,
		GroupRatio:      c.GroupRatio,
		CreatedAt:       ts,
	}
}
