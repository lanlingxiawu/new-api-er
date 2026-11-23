package controller

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// employeeUserID is fixed by the authenticated self-service endpoint. Only
// administrators pass zero, allowing the employee filter to come from the URL.
func parseCommissionLogPageFilter(c *gin.Context, employeeUserID int) (model.CommissionLogPageFilter, error) {
	filter := model.CommissionLogPageFilter{}
	var err error
	filter.EmployeeUserId = employeeUserID
	if employeeUserID == 0 {
		filter.EmployeeUserId, err = parseOptionalIntQuery(c, "employee_user_id")
		if err != nil {
			return filter, err
		}
	}
	filter.CustomerUserId, err = parseOptionalIntQuery(c, "customer_user_id")
	if err != nil {
		return filter, err
	}
	filter.ChannelId, err = parseOptionalIntQuery(c, "channel_id")
	if err != nil {
		return filter, err
	}
	filter.PageSize, err = parseIntQueryWithDefault(c, "page_size", 20)
	if err != nil {
		return filter, err
	}
	filter.ModelName = strings.TrimSpace(c.Query("model_name"))
	filter.LossStatus = strings.TrimSpace(c.Query("loss_status"))
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		return filter, err
	}
	filter.StartTime, filter.EndTime = timeRange.StartTime, timeRange.EndTime
	if strings.TrimSpace(c.Query("start_time")) == "" && strings.TrimSpace(c.Query("end_time")) == "" {
		filter.EndTime = time.Now().Unix()
		filter.StartTime = filter.EndTime - 86400
	}
	if c.Query("cursor_created_at") != "" || c.Query("cursor_id") != "" {
		cursor := &model.CommissionLogCursor{}
		cursor.CreatedAt, err = parseOptionalInt64Query(c, "cursor_created_at")
		if err != nil {
			return filter, err
		}
		cursor.Id, err = parseOptionalIntQuery(c, "cursor_id")
		if err != nil {
			return filter, err
		}
		filter.Cursor = cursor
	}
	return filter, filter.Validate()
}

func listCommissionLogPage(c *gin.Context, employeeUserID int) {
	filter, err := parseCommissionLogPageFilter(c, employeeUserID)
	if err != nil {
		logger.LogWarn(c, "invalid commission log query: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgEmployeeCommissionQueryInvalid)
		return
	}
	page, err := model.ListCommissionLogs(c.Request.Context(), filter)
	if err != nil {
		commissionLogQueryError(c, err)
		return
	}
	if employeeUserID != 0 {
		common.ApiSuccess(c, page)
		return
	}
	names := model.ResolveChannelDisplayNamesWithoutLogs(commissionLogChannelIds(page.Items))
	type logWithChannel struct {
		*model.EmployeeCommissionLog
		ChannelName string `json:"channel_name"`
	}
	items := make([]logWithChannel, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, logWithChannel{EmployeeCommissionLog: row, ChannelName: names[row.ChannelId]})
	}
	common.ApiSuccess(c, gin.H{"items": items, "has_more": page.HasMore, "next_cursor": page.NextCursor})
}

func commissionLogQueryError(c *gin.Context, err error) {
	logger.LogError(c, "commission log query failed: "+err.Error())
	if errors.Is(err, context.DeadlineExceeded) {
		common.ApiErrorI18n(c, i18n.MsgEmployeeCommissionQueryTimeout)
		return
	}
	common.ApiErrorI18n(c, i18n.MsgEmployeeCommissionQueryFailed)
}
