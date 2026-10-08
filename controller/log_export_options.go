package controller

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func GetLogExportFilterOptions(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	employee := isEmployeeExportRoute(c)
	field, keyword := c.Query("field"), c.Query("keyword")
	if len(keyword) > 128 {
		employeeExportError(c, model.ErrEmployeeExportInvalid)
		return
	}
	if field == "customer" || field == "username" {
		query := model.DB.WithContext(ctx).Model(&model.User{})
		if employee {
			query = query.Where("inviter_id = ? AND role = ?", c.GetInt("id"), common.RoleCommonUser)
		}
		if keyword != "" {
			query = query.Where("username LIKE ? OR display_name LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
		}
		cursor, _ := strconv.Atoi(c.Query("cursor"))
		var users []struct {
			Id          int
			Username    string
			DisplayName string
		}
		err := query.Select("id, username, display_name").Where("id > ?", cursor).Order("id").Limit(51).Find(&users).Error
		if err != nil {
			employeeExportError(c, err)
			return
		}
		next := ""
		if len(users) > 50 {
			users = users[:50]
			next = strconv.Itoa(users[49].Id)
		}
		items := []model.LogExportFilterOption{}
		for _, user := range users {
			value := user.Username
			if field == "customer" {
				value = strconv.Itoa(user.Id)
			}
			label := user.Username
			if user.DisplayName != "" {
				label += " · " + user.DisplayName
			}
			items = append(items, model.LogExportFilterOption{Value: value, Label: label})
		}
		common.ApiSuccess(c, gin.H{"items": items, "next_cursor": next})
		return
	}
	var ids []int
	if employee {
		if field != "model_name" && field != "token_name" && field != "group" {
			employeeExportError(c, model.ErrEmployeeExportAccess)
			return
		}
		if err := model.AuthorizeEmployeeExport(ctx, c.GetInt("id")); err != nil {
			employeeExportError(c, err)
			return
		}
		template, err := model.LookupEmployeeExportTemplate(ctx, c.Query("template_id"))
		if err != nil {
			employeeExportError(c, err)
			return
		}
		if !slices.Contains(template.AllowedFilters, field) {
			employeeExportError(c, model.ErrEmployeeExportAccess)
			return
		}
		var requested []int
		for _, raw := range c.QueryArray("customer_ids") {
			id, err := strconv.Atoi(raw)
			if err != nil {
				employeeExportError(c, model.ErrEmployeeExportInvalid)
				return
			}
			requested = append(requested, id)
		}
		ids, err = model.ResolveEmployeeExportCustomers(ctx, c.GetInt("id"), requested, len(requested) == 0)
		if err != nil {
			employeeExportError(c, err)
			return
		}
	}
	start, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if start <= 0 || end < start || end-start > operation_setting.GetLogExportSetting().GetAdminMaxRangeSec() {
		employeeExportError(c, model.ErrEmployeeExportInvalid)
		return
	}
	filter := model.LogExportFilter{StartTimestamp: start, EndTimestamp: end}
	if employee {
		filter.LogType = model.LogTypeConsume
	}
	items, next, err := model.LogExportFilterOptions(ctx, filter, field, keyword, c.Query("cursor"), ids)
	if err != nil {
		employeeExportError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "next_cursor": next})
}
