package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditexport"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// AuditExportScope binds scope at route registration and requires a dashboard
// session even where the permission route also supports management PATs.
func AuditExportScope(self bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgForbidden)})
			return
		}
		scope := "audit_admin"
		if self {
			scope = "audit_self"
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		access, err := auditexport.CurrentAccess(ctx, c.GetInt("id"), scope)
		if err != nil {
			respondLogExportManagementError(c, err)
			c.Abort()
			return
		}
		c.Set("log_export_access", access)
		c.Next()
	}
}

func parseAuditExportRequest(c *gin.Context, self bool) (*model.AuditExportFilter, model.LogExportOptions, error) {
	invalid := errors.New("invalid audit export request")
	opts := model.LogExportOptions{Header: true, CSVBOM: true}
	var body map[string]json.RawMessage
	if err := common.UnmarshalBodyReusable(c, &body); err != nil {
		return nil, opts, err
	}
	for key := range body {
		if key != "filters" && key != "options" {
			return nil, opts, invalid
		}
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(body["filters"], &fields); err != nil || fields == nil {
		return nil, opts, invalid
	}
	for key, value := range fields {
		if string(value) == "null" || !slices.Contains([]string{"start_timestamp", "end_timestamp", "category", "success", "username", "request_id", "token_ref", "exclude_token_ref", "ip", "actor_type", "auth_method", "route", "route_missing", "ip_risk"}, key) || self && slices.Contains([]string{"username", "ip_risk", "actor_type", "route", "route_missing"}, key) {
			return nil, opts, invalid
		}
	}
	f := &model.AuditExportFilter{}
	if err := common.Unmarshal(body["filters"], f); err != nil {
		return nil, opts, err
	}
	f.IP = strings.TrimSpace(f.IP)
	if !validAuditFilterFields(f.AuditLogFilter) || !model.CanFilterAuditActor(f.ActorType, logExportRequestAccess(c).Role) || !slices.Contains([]string{"", "abnormal", "attention", "high"}, f.IPRisk) {
		return nil, opts, invalid
	}
	if self {
		f.UserId, f.SelfView = c.GetInt("id"), true
		if !validAuditActivityFilter(f.AuditLogFilter) {
			return nil, opts, invalid
		}
	}
	if raw, ok := body["options"]; ok {
		var options map[string]json.RawMessage
		if common.Unmarshal(raw, &options) != nil || options == nil {
			return nil, opts, invalid
		}
		for key, value := range options {
			if string(value) == "null" || !slices.Contains([]string{"header", "csv_bom", "timezone"}, key) {
				return nil, opts, invalid
			}
		}
		if err := common.Unmarshal(raw, &opts); err != nil {
			return nil, opts, err
		}
	}
	if opts.Timezone != "" {
		if _, err := time.LoadLocation(opts.Timezone); err != nil {
			return nil, opts, invalid
		}
	}
	return f, opts, nil
}

func CreateAuditLogExportJob(c *gin.Context) {
	access := logExportRequestAccess(c)
	self := access.Scope == "audit_self"
	f, opts, err := parseAuditExportRequest(c, self)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgAuditExportInvalid)
		return
	}
	setting := operation_setting.GetLogExportSetting()
	if !setting.Enabled || self && !operation_setting.GetLogSetting().UserExportEnabled {
		common.ApiErrorI18n(c, i18n.MsgLogExportDisabled)
		return
	}
	if !model.LogExportAvailable() {
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	if f.StartTimestamp <= 0 || f.EndTimestamp <= 0 {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeRequired)
		return
	}
	if f.EndTimestamp < f.StartTimestamp || f.EndTimestamp > 253402300799 {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeInvalid)
		return
	}
	maxRange := setting.GetAdminMaxRangeSec()
	if self {
		maxRange = setting.GetUserMaxRangeSec()
	}
	if f.EndTimestamp-f.StartTimestamp > maxRange {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeTooLong, map[string]any{"Days": maxRange / 86400})
		return
	}
	job := &model.LogExportJob{DataSource: model.LogExportDataAudit, SelfExport: self, JobID: model.NewLogExportJobID(), UserID: access.UserID, Username: c.GetString("username"), CreatorRole: access.Role, AuditFilters: f, Options: opts, Lang: i18n.GetLangFromContext(c), Format: model.LogExportFormatCSVGz, Mode: model.LogExportModeDetail,
		Filters: model.LogExportFilter{UserId: f.UserId, StartTimestamp: f.StartTimestamp, EndTimestamp: f.EndTimestamp}}
	if self {
		job.AuditProjectionVersion = model.AuditActivityProjectionVersion
	}
	if view, _ := access.Permissions(job); !view {
		respondLogExportManagementError(c, model.ErrEmployeeExportAccess)
		return
	}
	submitLogExportJob(c, job)
}
