package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

var (
	ledgerListSemaphore       = make(chan struct{}, 2)
	ledgerListUserSemaphores  = map[int]chan struct{}{}
	ledgerListUserSemaphoresM sync.Mutex
)

func AdminGetConsumptionCostLedgerStats(c *gin.Context) {
	filter, err := parseConsumptionCostLedgerCommonFilter(c)
	if err != nil {
		ledgerJSONError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	result, err := model.GetConsumptionCostLedgerStats(ctx, filter)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			ledgerJSONError(c, http.StatusGatewayTimeout, "Ledger stats query timed out.")
			return
		}
		if errors.Is(err, context.Canceled) {
			ledgerJSONError(c, 499, "Ledger stats query canceled.")
			return
		}
		ledgerJSONError(c, http.StatusInternalServerError, "Ledger stats query failed.")
		return
	}
	hint := ledgerFallbackHint(filter.StartTime, filter.EndTime)
	ledgerJSONSuccessWithHint(c, result, hint)
}

func AdminListConsumptionCostLedger(c *gin.Context) {
	userID := getLedgerRequestUserID(c)
	userSemaphore := getLedgerUserSemaphore(userID)
	if !tryAcquireLedgerSemaphore(userSemaphore) {
		ledgerJSONError(c, http.StatusTooManyRequests, "Ledger query is busy for this account, please retry later.")
		return
	}
	defer releaseLedgerSemaphore(userSemaphore)
	if !tryAcquireLedgerSemaphore(ledgerListSemaphore) {
		ledgerJSONError(c, http.StatusTooManyRequests, "Ledger query is busy, please retry later.")
		return
	}
	defer releaseLedgerSemaphore(ledgerListSemaphore)

	filter, err := parseConsumptionCostLedgerListFilter(c)
	if err != nil {
		ledgerJSONError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	page, err := model.ListConsumptionCostLedger(ctx, filter)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			ledgerJSONError(c, http.StatusGatewayTimeout, "Ledger query timed out.")
			return
		}
		if errors.Is(err, context.Canceled) {
			ledgerJSONError(c, 499, "Ledger query canceled.")
			return
		}
		ledgerJSONError(c, http.StatusInternalServerError, "Ledger query failed.")
		return
	}
	// 查询时解析渠道名（channels + 日聚合快照，不读流水表的 channel_name 列）。
	// 单页查询无需跨页缓存，传 nil。
	model.FillConsumptionCostLedgerChannelNames(page.Items, nil)
	hint := ledgerFallbackHint(filter.StartTime, filter.EndTime)
	ledgerJSONSuccessWithHint(c, page, hint)
}

// AdminReverseConsumptionCostLedger 对某条台账记录执行定向冲销：插入一条镜像消费记录、
// 重新走结算侧效应（余额 + 台账 + 提成），使该笔消费的净影响归零。仅管理员可调用。
// 台账记录 id 走请求体（避免与同级静态路由 stats/export/fallback 冲突）。
func AdminReverseConsumptionCostLedger(c *gin.Context) {
	var req struct {
		Id int `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Id <= 0 {
		ledgerJSONError(c, http.StatusBadRequest, "Invalid ledger id.")
		return
	}
	adminId := c.GetInt("id")
	adminName := c.GetString("username")
	result, err := service.ReverseConsumptionCostLedger(c, req.Id, adminId, adminName)
	if err != nil {
		ledgerJSONError(c, http.StatusBadRequest, err.Error())
		return
	}
	ledgerJSONSuccess(c, result)
}

func parseConsumptionCostLedgerListFilter(c *gin.Context) (model.ConsumptionCostLedgerFilter, error) {
	statsFilter, err := parseConsumptionCostLedgerCommonFilter(c)
	if err != nil {
		return model.ConsumptionCostLedgerFilter{}, err
	}
	ldCfg := operation_setting.GetLedgerDetailSetting()
	limit, err := parseIntQueryWithDefault(c, "limit", ldCfg.GetListDefaultLimit())
	if err != nil {
		return model.ConsumptionCostLedgerFilter{}, err
	}
	if limit <= 0 {
		limit = ldCfg.GetListDefaultLimit()
	}
	if limit > ldCfg.GetListMaxLimit() {
		limit = ldCfg.GetListMaxLimit()
	}
	cursorCreated, err := parseOptionalInt64Query(c, "cursor_created_at")
	if err != nil {
		return model.ConsumptionCostLedgerFilter{}, err
	}
	cursorId, err := parseOptionalIntQuery(c, "cursor_id")
	if err != nil {
		return model.ConsumptionCostLedgerFilter{}, err
	}
	if (cursorCreated > 0) != (cursorId > 0) {
		return model.ConsumptionCostLedgerFilter{}, errors.New("cursor_created_at and cursor_id must be provided together")
	}
	filter := model.ConsumptionCostLedgerFilter{
		ConsumptionCostLedgerCommonFilter: statsFilter.ConsumptionCostLedgerCommonFilter,
		CursorCreated:                     cursorCreated,
		CursorId:                          cursorId,
		Limit:                             limit,
	}
	return filter, nil
}

func parseConsumptionCostLedgerCommonFilter(c *gin.Context) (model.ConsumptionCostLedgerStatsFilter, error) {
	var err error
	filter := model.ConsumptionCostLedgerStatsFilter{}
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
	if filter.StartTime == 0 && filter.EndTime == 0 && !hasUniqueFilter {
		filter.EndTime = defaultLedgerEndTime()
		filter.StartTime = filter.EndTime - operation_setting.GetLedgerDetailSetting().GetListDefaultRangeSec()
	}
	if !hasUniqueFilter {
		if filter.StartTime <= 0 || filter.EndTime <= 0 {
			return filter, errors.New("start_time and end_time are required")
		}
		if filter.EndTime <= filter.StartTime {
			return filter, errors.New("end_time must be greater than start_time")
		}
		if filter.EndTime-filter.StartTime > operation_setting.GetLedgerDetailSetting().GetListMaxRangeSec() {
			return filter, errors.New("time range cannot exceed 24 hours")
		}
	}
	return filter, nil
}

func defaultLedgerEndTime() int64 {
	now := time.Now().Unix()
	return now - now%60
}

func parseIntQueryWithDefault(c *gin.Context, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("invalid " + key)
	}
	return value, nil
}

func parseOptionalIntQuery(c *gin.Context, key string) (int, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("invalid " + key)
	}
	return value, nil
}

func parseOptionalInt64PointerQuery(c *gin.Context, key string) (*int64, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, errors.New("invalid " + key)
	}
	return &value, nil
}

func parseOptionalFloat64PointerQuery(c *gin.Context, key string) (*float64, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, errors.New("invalid " + key)
	}
	return &value, nil
}

func parseOptionalBoolPointerQuery(c *gin.Context, key string) (*bool, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, errors.New("invalid " + key)
	}
	return &value, nil
}

func tryAcquireLedgerSemaphore(semaphore chan struct{}) bool {
	select {
	case semaphore <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseLedgerSemaphore(semaphore chan struct{}) {
	select {
	case <-semaphore:
	default:
	}
}

func getLedgerRequestUserID(c *gin.Context) int {
	value, exists := c.Get(string(constant.ContextKeyUserId))
	if !exists {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func getLedgerUserSemaphore(userID int) chan struct{} {
	ledgerListUserSemaphoresM.Lock()
	defer ledgerListUserSemaphoresM.Unlock()
	semaphore, ok := ledgerListUserSemaphores[userID]
	if !ok {
		semaphore = make(chan struct{}, 1)
		ledgerListUserSemaphores[userID] = semaphore
	}
	return semaphore
}

func ledgerJSONSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

// ledgerJSONSuccessWithHint wraps the data and an optional fallback_hint into the
// response body.  hint is nil when the filter does not qualify for backfill checking.
func ledgerJSONSuccessWithHint(c *gin.Context, data any, hint *service.FallbackFileStatus) {
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       "",
		"data":          data,
		"fallback_hint": hint,
	})
}

// ledgerFallbackHint returns the FallbackFileStatus for the query's date when it is a
// single historical day, or nil when the filter does not qualify.
func ledgerFallbackHint(startTime, endTime int64) *service.FallbackFileStatus {
	date, ok := service.IsSingleHistoricalDayFilter(startTime, endTime)
	if !ok {
		return nil
	}
	status := service.GetFallbackFileStatus(date)
	return &status
}

func ledgerJSONError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"success": false,
		"message": message,
	})
}
