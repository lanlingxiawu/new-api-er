package controller

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type adminTokenUserItem struct {
	ID                        int    `json:"id"`
	Username                  string `json:"username"`
	DisplayName               string `json:"display_name"`
	Role                      int    `json:"role"`
	Status                    int    `json:"status"`
	Group                     string `json:"group"`
	Quota                     int    `json:"quota"`
	UsedQuota                 int    `json:"used_quota"`
	GroupRatios               string `json:"group_ratios"`
	InviterID                 int    `json:"-" gorm:"column:inviter_id"`
	EmployeeProfileID         *int   `json:"employee_profile_id,omitempty" gorm:"-"`
	AssignedEmployeeUserID    *int   `json:"assigned_employee_user_id,omitempty" gorm:"-"`
	AssignedEmployeeProfileID *int   `json:"assigned_employee_profile_id,omitempty" gorm:"-"`
	AssignedEmployeeUsername  string `json:"assigned_employee_username,omitempty" gorm:"-"`
	AssignedEmployeeName      string `json:"assigned_employee_name,omitempty" gorm:"-"`
}

func listAdminTokenUsers(c *gin.Context) {
	selectors := 0
	for key, values := range c.Request.URL.Query() {
		if len(values) != 1 || !slices.Contains([]string{"id", "username", "keyword", "page_size", "after_id"}, key) {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		if key == "id" || key == "username" || key == "keyword" {
			selectors++
		}
	}
	after, afterErr := strconv.Atoi(c.DefaultQuery("after_id", "0"))
	limit, limitErr := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if selectors != 1 || afterErr != nil || after != 0 || limitErr != nil || limit < 1 || limit > 100 {
		common.ApiErrorI18n(c, i18n.MsgAdminTokenExactUserRequired)
		return
	}
	idValue, username := c.Query("id"), c.Query("username")
	if keyword, present := c.GetQuery("keyword"); present {
		keyword = strings.TrimSpace(keyword)
		if _, err := strconv.Atoi(keyword); err == nil {
			idValue = keyword
		} else {
			username = keyword
		}
	}
	query := model.DB.WithContext(c.Request.Context()).Model(&model.User{}).
		Select("id", "username", "display_name", "role", "status", "group", "quota", "used_quota", "group_ratios", "inviter_id").Where("role < ?", c.GetInt("role"))
	if idValue != "" {
		id, err := strconv.Atoi(idValue)
		if err != nil || id <= 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		query = query.Where("id = ?", id)
	} else {
		if strings.TrimSpace(username) == "" || utf8.RuneCountInString(username) > 32 {
			common.ApiErrorI18n(c, i18n.MsgAdminTokenExactUserRequired)
			return
		}
		query = query.Where("username = ?", username)
	}
	items := []adminTokenUserItem{}
	if err := query.Limit(1).Find(&items).Error; err != nil {
		logger.LogError(c, "management token exact user lookup failed: "+err.Error())
		common.ApiErrorStatus(c, 500, errors.New(i18n.T(c, i18n.MsgDatabaseError)))
		return
	}
	if len(items) == 1 && middleware.RequirePATPermission(c, authz.AdminMenuEmployeesView) {
		item := &items[0]
		item.EmployeeProfileID, item.AssignedEmployeeUserID, item.AssignedEmployeeProfileID = new(int), new(int), new(int)
		var employees []model.EmployeeProfile
		if err := model.DB.WithContext(c.Request.Context()).Select("id", "user_id").Where("user_id IN ?", []int{item.ID, item.InviterID}).Find(&employees).Error; err != nil {
			writeAdminTokenUserError(c, err)
			return
		}
		for _, employee := range employees {
			if employee.UserId == item.ID {
				*item.EmployeeProfileID = employee.Id
			}
			if employee.UserId == item.InviterID {
				*item.AssignedEmployeeUserID = employee.UserId
				*item.AssignedEmployeeProfileID = employee.Id
			}
		}
		if *item.AssignedEmployeeUserID != 0 {
			var employee model.User
			err := model.DB.WithContext(c.Request.Context()).Select("username", "display_name").First(&employee, *item.AssignedEmployeeUserID).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				writeAdminTokenUserError(c, err)
				return
			}
			item.AssignedEmployeeUsername, item.AssignedEmployeeName = employee.Username, employee.DisplayName
		}
	}
	common.ApiSuccess(c, gin.H{"items": items, "next_cursor": nil, "has_more": false, "quota_per_unit": common.QuotaPerUnit})
}
