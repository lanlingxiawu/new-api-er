package controller

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// Token reads have their own projections and query contract. Never fall through
// to dashboard handlers that expose broader fields or aggregation filters.
func adminTokenReadRequest(c *gin.Context) {
	route := c.Request.Method + " " + c.FullPath()
	module := service.AdminTokenRouteModule(route)
	if module == "" || c.GetString("admin_token_module") != module || !middleware.RequirePATPermission(c, authz.AdminMenuUsersView) {
		writeAdminTokenUserError(c, service.ErrAdminTokenDenied)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	switch route {
	case "GET /api/group/":
		if c.Request.URL.RawQuery != "" {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		groups := make([]string, 0)
		for name := range ratio_setting.GetGroupRatioCopy() {
			groups = append(groups, name)
		}
		slices.Sort(groups)
		common.ApiSuccess(c, groups)
	case "GET /api/admin/employee":
		if !middleware.RequirePATPermission(c, authz.AdminMenuEmployeesView) {
			writeAdminTokenUserError(c, service.ErrAdminTokenDenied)
			return
		}
		query, ok := adminTokenReadQuery(c, "page", "page_size", "status")
		if !ok {
			return
		}
		page, pageSize, status := 1, 20, 1
		for key, values := range query {
			value, err := strconv.Atoi(values[0])
			if err != nil {
				common.ApiErrorI18n(c, i18n.MsgInvalidParams)
				return
			}
			switch key {
			case "page":
				page = value
			case "page_size":
				pageSize = value
			case "status":
				status = value
			}
		}
		if page < 1 || page > 10000 || pageSize < 1 || pageSize > 100 || status < 0 || status > 2 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		items, total, err := model.ListEmployeeChoices(ctx, page, pageSize, status)
		if err != nil {
			writeAdminTokenUserError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
	case "GET /api/log/stat":
		query, ok := adminTokenReadQuery(c, "user_id", "username", "start_timestamp", "end_timestamp", "type")
		if !ok {
			return
		}
		hasUserID, hasUsername := query.Has("user_id"), query.Has("username")
		if (hasUserID && hasUsername) || (query.Has("type") && query.Get("type") != "2") {
			common.ApiErrorI18n(c, i18n.MsgAdminTokenUsageQueryRequired)
			return
		}
		now := common.GetTimestamp()
		start, end := now-60, now
		if hasUserID || hasUsername || query.Has("start_timestamp") || query.Has("end_timestamp") {
			var startErr, endErr error
			start, startErr = strconv.ParseInt(query.Get("start_timestamp"), 10, 64)
			end, endErr = strconv.ParseInt(query.Get("end_timestamp"), 10, 64)
			if startErr != nil || endErr != nil || start <= 0 || end < start || end > now || end-start > 30*24*60*60 {
				common.ApiErrorI18n(c, i18n.MsgAdminTokenUsageQueryRequired)
				return
			}
		}
		var target model.User
		if hasUserID || hasUsername {
			targetQuery := model.DB.WithContext(ctx).Select("id").Where("role < ?", c.GetInt("role"))
			if hasUserID {
				userID, err := strconv.Atoi(query.Get("user_id"))
				if err != nil || userID <= 0 {
					common.ApiErrorI18n(c, i18n.MsgAdminTokenUsageQueryRequired)
					return
				}
				targetQuery = targetQuery.Where("id = ?", userID)
			} else {
				username := query.Get("username")
				if username == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 32 {
					common.ApiErrorI18n(c, i18n.MsgAdminTokenUsageQueryRequired)
					return
				}
				targetQuery = targetQuery.Where("username = ?", username)
			}
			if err := targetQuery.First(&target).Error; err != nil {
				writeAdminTokenUserError(c, err)
				return
			}
		}
		stat, err := model.SumUsedQuotaWithFilter(ctx, model.LogFilter{UserID: target.Id, StartTimestamp: start, EndTimestamp: end})
		if err != nil {
			writeAdminTokenUserError(c, err)
			return
		}
		data := gin.H{
			"quota": stat.Quota, "quota_per_unit": common.QuotaPerUnit,
			"rpm": stat.Rpm, "tpm": stat.Tpm, "start_timestamp": start, "end_timestamp": end,
		}
		if target.Id > 0 {
			data["user_id"] = target.Id
		}
		common.ApiSuccess(c, data)
	default:
		writeAdminTokenUserError(c, service.ErrAdminTokenDenied)
	}
}

// Duplicate or unsupported query parameters must not broaden a token read.
func adminTokenReadQuery(c *gin.Context, fields ...string) (url.Values, bool) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return nil, false
	}
	for field, values := range query {
		if !slices.Contains(fields, field) || len(values) != 1 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return nil, false
		}
	}
	return query, true
}
