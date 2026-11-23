package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func employeeAuditContext(c *gin.Context) (context.Context, context.CancelFunc, *gorm.DB) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return ctx, cancel, nil
	}
	db, err := model.AuditReadDB(false)
	if err != nil {
		employeeCustomerAuditResponse(c, nil, err)
		return ctx, cancel, nil
	}
	return ctx, cancel, db
}

func employeeCustomerAuditContext(c *gin.Context) (context.Context, context.CancelFunc, *gorm.DB, int) {
	ctx, cancel, db := employeeAuditContext(c)
	if db == nil {
		return ctx, cancel, nil, 0
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return ctx, cancel, nil, 0
	}
	return ctx, cancel, db, id
}

func employeeCustomerAuditFilter(c *gin.Context, customerID int) (model.CustomerAuditFilter, error) {
	start, end, valid := auditCursorTimeRange(c, 86400)
	f := model.CustomerAuditFilter{EmployeeID: c.GetInt("id"), CustomerID: customerID, Start: start, End: end, Cursor: c.Query("cursor"), RequestID: c.Query("request_id"), IP: c.Query("ip"), View: c.Query("view")}
	if !valid {
		return f, model.ErrCustomerAuditParams
	}
	for _, key := range []string{"actor_type", "auth_method", "route", "route_missing", "token_ref", "exclude_token_ref", "ip_risk", "success", "category"} {
		if c.Query(key) != "" {
			return f, model.ErrCustomerAuditParams
		}
	}
	f.IP = strings.TrimSpace(f.IP)
	if f.IP != "" {
		ip, err := netip.ParseAddr(f.IP)
		if err != nil || ip.Zone() != "" {
			return f, model.ErrCustomerAuditParams
		}
		f.IP = ip.String()
	}
	return f, nil
}

func employeeCustomerAuditResponse(c *gin.Context, data any, err error) {
	switch {
	case err == nil:
		common.ApiSuccess(c, data)
	case errors.Is(err, model.ErrCustomerAuditForbidden):
		c.AbortWithStatus(http.StatusForbidden)
	case errors.Is(err, model.ErrCustomerAuditParams), errors.Is(err, auditsecurity.ErrNoAlertChanges):
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
	case errors.Is(err, model.ErrCustomerAuditScopeChanged):
		logger.LogWarn(c, "customer audit ownership changed while reading")
		common.ApiErrorI18n(c, i18n.MsgAuditCustomerScopeChanged)
	case errors.Is(err, model.ErrSecurityConflict):
		common.ApiErrorI18n(c, "audit_security.conflict")
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorI18n(c, "audit_security.not_found")
	default:
		logger.LogError(c, common.LogText("employee customer audit failed: %v", err))
		common.ApiErrorI18n(c, "audit_security.unavailable")
	}
}

func EmployeeCustomersAuditLogs(c *gin.Context) {
	ctx, cancel, db := employeeAuditContext(c)
	defer cancel()
	if db == nil {
		return
	}
	f, err := employeeCustomerAuditFilter(c, 0)
	if err != nil {
		employeeCustomerAuditResponse(c, nil, err)
		return
	}
	logs, err := model.AuditReadDB(true)
	if err != nil {
		employeeCustomerAuditResponse(c, nil, err)
		return
	}
	page, err := model.QueryEmployeeAuditLogs(ctx, db, logs, model.EmployeeAuditFilter{EmployeeID: f.EmployeeID, Start: f.Start, End: f.End, RequestID: f.RequestID, IP: f.IP, Cursor: f.Cursor, View: f.View})
	employeeCustomerAuditResponse(c, page, err)
}

func EmployeeCustomersSecurityAlerts(c *gin.Context) {
	ctx, cancel, db := employeeAuditContext(c)
	defer cancel()
	if db == nil {
		return
	}
	if c.Query("view") == "unresolved" {
		employeePendingAlerts(c, ctx, db, 0)
		return
	}
	f, err := employeeCustomerAuditFilter(c, 0)
	if err != nil {
		employeeCustomerAuditResponse(c, nil, err)
		return
	}
	page, err := auditsecurity.ListEmployeeCustomerAlerts(ctx, db, model.EmployeeAuditFilter{EmployeeID: f.EmployeeID, Start: f.Start, End: f.End, RequestID: f.RequestID, IP: f.IP, Cursor: f.Cursor, View: f.View})
	employeeCustomerAuditResponse(c, page, err)
}

func EmployeeCustomerAuditLogs(c *gin.Context) {
	ctx, cancel, db, customerID := employeeCustomerAuditContext(c)
	defer cancel()
	if db == nil {
		return
	}
	f, err := employeeCustomerAuditFilter(c, customerID)
	if err != nil {
		employeeCustomerAuditResponse(c, nil, err)
		return
	}
	logs, err := model.AuditReadDB(true)
	if err != nil {
		employeeCustomerAuditResponse(c, nil, err)
		return
	}
	page, err := model.QueryCustomerAuditLogs(ctx, db, logs, f)
	employeeCustomerAuditResponse(c, struct {
		Items      []model.AuditActivity `json:"items"`
		NextCursor string                `json:"next_cursor"`
	}{model.ProjectAuditActivities(page.Items), page.NextCursor}, err)
}

func EmployeeCustomerSecurityAlerts(c *gin.Context) {
	ctx, cancel, db, customerID := employeeCustomerAuditContext(c)
	defer cancel()
	if db == nil {
		return
	}
	if c.Query("view") == "unresolved" {
		employeePendingAlerts(c, ctx, db, customerID)
		return
	}
	f, err := employeeCustomerAuditFilter(c, customerID)
	if err == nil && (f.RequestID != "" || f.IP != "") {
		err = model.ErrCustomerAuditParams
	}
	if err != nil {
		employeeCustomerAuditResponse(c, nil, err)
		return
	}
	page, err := auditsecurity.ListCustomerAlerts(ctx, db, f)
	employeeCustomerAuditResponse(c, page, err)
}

func EmployeeCustomersSecurityPendingCount(c *gin.Context) {
	ctx, cancel, db := employeeAuditContext(c)
	defer cancel()
	if db == nil {
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		employeeCustomerAuditResponse(c, nil, model.ErrCustomerAuditParams)
		return
	}
	count, err := auditsecurity.CountEmployeePendingAlerts(ctx, db, c.GetInt("id"))
	employeeCustomerAuditResponse(c, struct {
		Count int `json:"count"`
	}{count}, err)
}

func employeePendingAlerts(c *gin.Context, ctx context.Context, db *gorm.DB, customerID int) {
	for key, values := range c.Request.URL.Query() {
		if key != "view" && key != "cursor" || len(values) != 1 {
			employeeCustomerAuditResponse(c, nil, model.ErrCustomerAuditParams)
			return
		}
	}
	page, err := auditsecurity.ListPendingCustomerAlerts(ctx, db, auditsecurity.PendingCustomerFilter{EmployeeID: c.GetInt("id"), CustomerID: customerID, Cursor: c.Query("cursor")})
	if customerID == 0 {
		employeeCustomerAuditResponse(c, page, err)
		return
	}
	single := auditsecurity.CustomerAlertPage{Items: []auditsecurity.CustomerSecurityAlert{}, NextCursor: page.NextCursor}
	for _, row := range page.Items {
		single.Items = append(single.Items, row.CustomerSecurityAlert)
	}
	employeeCustomerAuditResponse(c, single, err)
}

func EmployeeCustomerSecurityAlert(c *gin.Context) {
	ctx, cancel, db, customerID := employeeCustomerAuditContext(c)
	defer cancel()
	if db == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("alert_id"), 10, 64)
	if err != nil || id <= 0 {
		employeeCustomerAuditResponse(c, nil, model.ErrCustomerAuditParams)
		return
	}
	detail, err := auditsecurity.GetCustomerAlert(ctx, db, c.GetInt("id"), customerID, id)
	employeeCustomerAuditResponse(c, detail, err)
}

func EmployeeCustomerSecurityAction(c *gin.Context) {
	ctx, cancel, db, customerID := employeeCustomerAuditContext(c)
	defer cancel()
	if db == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("alert_id"), 10, 64)
	if err != nil || id <= 0 {
		employeeCustomerAuditResponse(c, nil, model.ErrCustomerAuditParams)
		return
	}
	var fields map[string]json.RawMessage
	if common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 8192), &fields) != nil {
		employeeCustomerAuditResponse(c, nil, model.ErrCustomerAuditParams)
		return
	}
	for key := range fields {
		if key != "version" && key != "status" && key != "note" && key != "conclusion" {
			employeeCustomerAuditResponse(c, nil, model.ErrCustomerAuditParams)
			return
		}
	}
	raw, err := common.Marshal(fields)
	var request struct {
		Version    int64   `json:"version"`
		Status     string  `json:"status"`
		Note       string  `json:"note"`
		Conclusion *string `json:"conclusion"`
	}
	if err != nil || common.Unmarshal(raw, &request) != nil {
		employeeCustomerAuditResponse(c, nil, model.ErrCustomerAuditParams)
		return
	}
	alert, err := auditsecurity.UpdateCustomerAlert(ctx, db, c.GetInt("role"), c.GetInt("id"), customerID, id, auditsecurity.AlertInvestigation{Version: request.Version, Status: request.Status, Note: request.Note, Conclusion: request.Conclusion})
	employeeCustomerAuditResponse(c, alert, err)
}
