package controller

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// A management token never falls through to the general user edit handlers.
// The route middleware has already checked its current module grant and scope.
func adminTokenUserRequest(c *gin.Context) {
	route := c.Request.Method + " " + c.FullPath()
	module := service.AdminTokenRouteModule(route)
	if module == "" || c.GetString("admin_token_module") != module || !middleware.RequirePATPermission(c, authz.AdminMenuUsersView) {
		writeAdminTokenUserError(c, service.ErrAdminTokenDenied)
		return
	}
	switch route {
	case "POST /api/user/manage":
		var request ManageRequest
		if !decodeAdminTokenUserBody(c, &request, "id", "action", "mode", "value") {
			return
		}
		if request.Action != "add_quota" {
			writeAdminTokenUserError(c, service.ErrAdminTokenDenied)
			return
		}
		manageUserQuota(c, request)
	case "PUT /api/user/":
		var request struct {
			ID          int    `json:"id"`
			GroupRatios string `json:"group_ratios"`
		}
		if !decodeAdminTokenUserBody(c, &request, "id", "group_ratios") {
			return
		}
		if _, err := common.UnmarshalJSONObject([]byte(request.GroupRatios)); err != nil || request.ID <= 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		var user model.User
		if err := model.DB.WithContext(c.Request.Context()).Select("id", "role", "group_ratios").First(&user, request.ID).Error; err != nil {
			writeAdminTokenUserError(c, err)
			return
		}
		if user.Role >= c.GetInt("role") {
			writeAdminTokenUserError(c, service.ErrAdminTokenDenied)
			return
		}
		updated, removed, _, ok := prepareUserGroupRatios(c, user.GroupRatios, request.GroupRatios, false)
		if !ok {
			return
		}
		change, err := model.UpdateUserGroupRatios(c.Request.Context(), user.Id, c.GetInt("role"), user.GroupRatios, updated)
		if err != nil {
			writeAdminTokenUserError(c, err)
			return
		}
		target := model.AuditTarget("user", change.UserID, change.Username)
		target.Consistency = "atomic"
		model.RequestAuditChanges(c).Add(target, model.AuditFields{"group_ratios": change.Before}, model.AuditFields{"group_ratios": change.After})
		recordManageAuditFor(c, change.UserID, "user.update", map[string]any{"id": change.UserID, "username": change.Username})
		if err := model.PublishUserAuthCache(change.UserID); err != nil {
			logger.LogError(c, "user ratios committed but cache publication failed: "+err.Error())
			common.ApiErrorStatus(c, 200, errors.New(i18n.T(c, i18n.MsgAdminTokenUserCommitted)), gin.H{"committed": true})
			return
		}
		common.ApiSuccess(c, gin.H{"id": change.UserID, "group_ratios": change.After, "removed_group_ratios": removed})
	case "POST /api/admin/employee/:id/assign-customer", "DELETE /api/admin/employee/:id/customer/:user_id":
		if !middleware.RequirePATPermission(c, authz.AdminMenuEmployeesView) {
			writeAdminTokenUserError(c, service.ErrAdminTokenDenied)
			return
		}
		employeeID, err := strconv.Atoi(c.Param("id"))
		if err != nil || employeeID <= 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		var request struct {
			UserID int `json:"user_id"`
		}
		unassign := c.Request.Method == http.MethodDelete
		if unassign {
			if !decodeAdminTokenUserBody(c, nil) {
				return
			}
			request.UserID, err = strconv.Atoi(c.Param("user_id"))
			if err != nil {
				common.ApiErrorI18n(c, i18n.MsgInvalidParams)
				return
			}
		} else if !decodeAdminTokenUserBody(c, &request, "user_id") {
			return
		}
		change, err := model.ChangeCustomerEmployee(c.Request.Context(), employeeID, request.UserID, unassign)
		if err != nil {
			writeAdminTokenUserError(c, err)
			return
		}
		recordCustomerAssignment(c, change)
		common.ApiSuccess(c, gin.H{"id": change.UserID, "assigned_employee_user_id": change.After})
	default:
		writeAdminTokenUserError(c, service.ErrAdminTokenDenied)
	}
}

func decodeAdminTokenUserBody(c *gin.Context, destination any, fields ...string) bool {
	if c.Request.URL.RawQuery != "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return false
	}
	var body io.Reader = http.NoBody
	if c.Request.Body != nil {
		body = c.Request.Body
	}
	raw, err := io.ReadAll(io.LimitReader(body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return false
	}
	if destination == nil {
		if len(raw) != 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return false
		}
		return true
	}
	object, err := common.UnmarshalJSONObject(raw)
	if err != nil || len(object) != len(fields) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return false
	}
	for key, value := range object {
		if !slices.Contains(fields, key) || common.GetJsonType(value) == "null" {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return false
		}
	}
	if err := common.Unmarshal(raw, destination); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return false
	}
	return true
}

func writeAdminTokenUserError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAdminTokenDenied), errors.Is(err, model.ErrUserQuotaPermission):
		common.ApiErrorStatus(c, http.StatusForbidden, errors.New(i18n.T(c, i18n.MsgAdminTokenDenied)))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorI18n(c, i18n.MsgAdminTokenTargetMissing)
	case errors.Is(err, model.ErrUserGroupRatiosConflict):
		common.ApiErrorI18n(c, i18n.MsgAdminTokenUserChanged)
	case errors.Is(err, model.ErrInvalidCustomerAssignment):
		common.ApiErrorI18n(c, i18n.MsgAdminTokenAssignmentInvalid)
	case errors.Is(err, model.ErrEmployeeAssignmentDisabled):
		common.ApiErrorI18n(c, i18n.MsgAdminTokenEmployeeDisabled)
	case errors.Is(err, model.ErrCustomerNotAssigned):
		common.ApiErrorI18n(c, i18n.MsgAdminTokenAssignmentMissing)
	default:
		logger.LogError(c, "restricted user operation failed: "+err.Error())
		common.ApiErrorStatus(c, http.StatusInternalServerError, errors.New(i18n.T(c, i18n.MsgDatabaseError)))
	}
}

func recordCustomerAssignment(c *gin.Context, change *model.CustomerAssignment) {
	target := model.AuditTarget("user", change.UserID, change.Username)
	target.Consistency = "atomic"
	model.RequestAuditChanges(c).Add(target, model.AuditFields{"inviter_id": change.Before}, model.AuditFields{"inviter_id": change.After})
	recordManageAuditFor(c, change.UserID, "user.update", map[string]any{"id": change.UserID, "username": change.Username})
}
