package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func employeeExportError(c *gin.Context, err error) {
	logger.LogError(c, "employee export: "+err.Error())
	var filterError *logExportFilterError
	if errors.As(err, &filterError) {
		common.ApiErrorI18n(c, filterError.key, filterError.args)
		return
	}
	switch {
	case errors.Is(err, model.ErrEmployeeExportAccess):
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgForbidden)})
	case errors.Is(err, model.ErrEmployeeExportNoCustomers):
		common.ApiErrorI18n(c, i18n.MsgEmployeeExportNoCustomers)
	case errors.Is(err, model.ErrEmployeeExportTooManyCustomers):
		common.ApiErrorI18n(c, i18n.MsgEmployeeExportTooMany, map[string]any{"Max": operation_setting.GetLogExportSetting().GetEmployeeMaxCustomersPerJob()})
	case errors.Is(err, model.ErrEmployeeExportNameTaken):
		common.ApiErrorI18n(c, i18n.MsgEmployeeExportNameTaken)
	case errors.Is(err, model.ErrEmployeeExportConflict):
		common.ApiErrorI18n(c, i18n.MsgEmployeeExportConflict)
	default:
		common.ApiErrorI18n(c, i18n.MsgEmployeeExportInvalid)
	}
}

// isEmployeeExportRoute reports whether the request came through the employee export group, which
// only ever exposes the caller's own employee jobs (admins who are also employees included).
func isEmployeeExportRoute(c *gin.Context) bool {
	return strings.HasPrefix(c.FullPath(), "/api/user/employee/export")
}

func AdminEmployeeExportTemplates(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	id, _ := strconv.Atoi(c.Param("id"))
	if c.Request.Method == http.MethodGet {
		query := model.DB.WithContext(ctx).Model(&model.EmployeeExportTemplate{})
		if id > 0 {
			var tpl model.EmployeeExportTemplate
			if err := query.First(&tpl, id).Error; err != nil {
				employeeExportError(c, err)
				return
			}
			common.ApiSuccess(c, gin.H{"template": tpl})
			return
		}
		cursor, _ := strconv.Atoi(c.Query("cursor"))
		limit, _ := strconv.Atoi(c.Query("limit"))
		if limit < 1 || limit > 100 {
			limit = 100
		}
		if keyword := c.Query("keyword"); keyword != "" {
			query = query.Where("name LIKE ?", "%"+keyword+"%")
		}
		if cursor > 0 {
			query = query.Where("id > ?", cursor)
		}
		items := []model.EmployeeExportTemplate{}
		if err := query.Order("id").Limit(limit + 1).Find(&items).Error; err != nil {
			employeeExportError(c, err)
			return
		}
		next := 0
		if len(items) > limit {
			items = items[:limit]
			next = items[len(items)-1].Id
		}
		common.ApiSuccess(c, gin.H{"items": items, "next_cursor": next, "builtin": model.EmployeeBuiltinExportTemplates()})
		return
	}
	if c.Request.Method == http.MethodDelete {
		if id <= 0 {
			employeeExportError(c, model.ErrEmployeeExportInvalid)
			return
		}
		if err := model.DB.WithContext(ctx).Delete(&model.EmployeeExportTemplate{}, id).Error; err != nil {
			employeeExportError(c, err)
			return
		}
		recordLogExportAudit(c, "employee_export.template_delete", i18n.T(c, "employee_export.template_deleted"), map[string]interface{}{"template_id": id})
		common.ApiSuccess(c, nil)
		return
	}
	var tpl model.EmployeeExportTemplate
	if err := common.UnmarshalBodyReusable(c, &tpl); err != nil {
		employeeExportError(c, err)
		return
	}
	if c.Request.Method == http.MethodPut && id <= 0 {
		employeeExportError(c, model.ErrEmployeeExportInvalid)
		return
	}
	tpl.Id = id
	tpl.CreatedAt, tpl.UpdatedAt, tpl.CreatedBy = 0, 0, 0
	if err := model.SaveEmployeeExportTemplate(ctx, &tpl, c.GetInt("id")); err != nil {
		employeeExportError(c, err)
		return
	}
	recordLogExportAudit(c, "employee_export.template_save", i18n.T(c, "employee_export.template_saved"), map[string]interface{}{"template_id": tpl.Id, "version": tpl.Version})
	common.ApiSuccess(c, tpl)
}

func EmployeeExportCapabilities(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	err := model.AuthorizeEmployeeExport(ctx, c.GetInt("id"))
	if errors.Is(err, model.ErrEmployeeExportAccess) {
		common.ApiSuccess(c, gin.H{"enabled": false, "templates": []model.EmployeeExportTemplate{}})
		return
	}
	if err != nil {
		employeeExportError(c, err)
		return
	}
	templates, err := model.ListEmployeeExportTemplates(ctx)
	if err != nil {
		employeeExportError(c, err)
		return
	}
	// Labels for every column a template can reference, used by the read-only preview.
	columns := employeeExportColumnDTOs()
	setting := operation_setting.GetLogExportSetting()
	common.ApiSuccess(c, gin.H{"enabled": setting.Enabled, "templates": templates, "columns": columns, "max_range_sec": setting.GetAdminMaxRangeSec(), "max_customers": setting.GetEmployeeMaxCustomersPerJob(), "anomaly_kinds": model.LogExportAnomalyKinds(), "quota_per_unit": common.QuotaPerUnit, "max_filter_values": setting.GetMaxFilterValues()})
}

func employeeExportColumnDTOs() []logExportColumnDTO {
	columns := []logExportColumnDTO{}
	for _, column := range model.EmployeeExportColumns() {
		columns = append(columns, logExportColumnDTO{Key: column.Key, Label: column.Label, Group: column.Group, Audience: column.Audience.String()})
	}
	return columns
}

func GetEmployeeExportColumns(c *gin.Context) {
	columns := employeeExportColumnDTOs()
	defaults := []string{"username"}
	if preset, ok := model.LookupBuiltinLogExportTemplate(model.LogExportTemplateCustomerInvoice); ok {
		defaults = append(defaults, preset.Columns...)
	}
	common.ApiSuccess(c, gin.H{"columns": columns, "default_columns": defaults, "max_columns": model.LogExportMaxColumns})
}

type employeeExportRequest struct {
	TemplateID     string                     `json:"template_id"`
	Timezone       string                     `json:"timezone"`
	CustomerIDs    []int                      `json:"customer_ids"`
	AllCustomers   bool                       `json:"all_customers"`
	StartTimestamp int64                      `json:"start_timestamp"`
	EndTimestamp   int64                      `json:"end_timestamp"`
	Filters        map[string]json.RawMessage `json:"filters"`
}

// buildEmployeeExportFilter shares the administrator filter semantics while enforcing template access.
func buildEmployeeExportFilter(values map[string]json.RawMessage, allowed []string) (model.LogExportFilter, error) {
	for key := range values {
		if !slices.Contains(allowed, key) {
			return model.LogExportFilter{}, model.ErrEmployeeExportInvalid
		}
	}
	data, err := common.Marshal(values)
	if err != nil {
		return model.LogExportFilter{}, model.ErrEmployeeExportInvalid
	}
	var req createLogExportJobRequest
	if err := common.Unmarshal(data, &req); err != nil {
		return model.LogExportFilter{}, model.ErrEmployeeExportInvalid
	}
	for _, value := range []string{req.ModelName, req.TokenName, req.Group} {
		if len(value) > 256 {
			return model.LogExportFilter{}, model.ErrEmployeeExportInvalid
		}
	}
	for _, value := range []*int{req.QuotaMin, req.CompletionTokensMin, req.CompletionTokensMax, req.UseTimeMin, req.MinRetryCount} {
		if value != nil && (*value < 0 || int64(*value) > 9007199254740991) {
			return model.LogExportFilter{}, model.ErrEmployeeExportInvalid
		}
	}
	return buildLogExportFilter(req)
}

func prepareEmployeeExport(c *gin.Context) (*model.LogExportJob, error) {
	var raw map[string]interface{}
	if err := common.UnmarshalBodyReusable(c, &raw); err != nil {
		return nil, err
	}
	for key := range raw {
		if !slices.Contains([]string{"template_id", "timezone", "customer_ids", "all_customers", "start_timestamp", "end_timestamp", "filters"}, key) {
			return nil, model.ErrEmployeeExportInvalid
		}
	}
	var req employeeExportRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := model.AuthorizeEmployeeExport(ctx, c.GetInt("id")); err != nil {
		return nil, err
	}
	setting := operation_setting.GetLogExportSetting()
	if !setting.Enabled || req.StartTimestamp <= 0 || req.EndTimestamp < req.StartTimestamp || req.EndTimestamp-req.StartTimestamp > setting.GetAdminMaxRangeSec() {
		return nil, model.ErrEmployeeExportInvalid
	}
	tpl, err := model.LookupEmployeeExportTemplate(ctx, req.TemplateID)
	if err != nil {
		return nil, err
	}
	if !tpl.Builtin {
		if err := tpl.Validate(); err != nil {
			return nil, err
		}
	}
	options := tpl.Options
	// The requester's timezone applies only when the template does not pin one (builtin templates).
	// A zone the server's tzdata lacks falls back to server time rather than blocking the export.
	if options.Timezone == "" && req.Timezone != "" {
		if _, err := time.LoadLocation(req.Timezone); err == nil {
			options.Timezone = req.Timezone
		}
	}
	ids, err := model.ResolveEmployeeExportCustomers(ctx, c.GetInt("id"), req.CustomerIDs, req.AllCustomers)
	if err != nil {
		return nil, err
	}
	// A single customer is named in the download file; the lookup only labels the file, so a failure is not fatal.
	customerName := ""
	if !req.AllCustomers && len(ids) == 1 {
		var customer model.User
		if err := model.DB.WithContext(ctx).Select("username").First(&customer, ids[0]).Error; err == nil {
			customerName = customer.Username
		}
	}
	filter, err := buildEmployeeExportFilter(req.Filters, tpl.AllowedFilters)
	if err != nil {
		return nil, err
	}
	filter.LogType = model.LogTypeConsume
	filter.StartTimestamp, filter.EndTimestamp = req.StartTimestamp, req.EndTimestamp
	job := &model.LogExportJob{
		UserID: c.GetInt("id"), Username: c.GetString("username"), Lang: i18n.GetLangFromContext(c),
		Format: tpl.Format, Columns: tpl.Columns, Options: options, Mode: tpl.Mode, SummaryDims: tpl.SummaryDims,
		Filters: filter,
		EmployeeScope: &model.EmployeeExportScope{TemplateKey: tpl.Key, TemplateName: tpl.Name, TemplateVersion: tpl.Version, CustomerIDs: ids,
			AllCustomers: req.AllCustomers, CustomerName: customerName},
	}
	return job, nil
}

func EmployeeCreateExport(c *gin.Context) {
	job, err := prepareEmployeeExport(c)
	if err != nil {
		employeeExportError(c, err)
		return
	}
	if !model.LogExportAvailable() {
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	setting := operation_setting.GetLogExportSetting()
	if model.CountActiveLogExportJobs(job.UserID) >= setting.GetMaxActiveJobsPerUser() {
		common.ApiErrorI18n(c, i18n.MsgLogExportBusy)
		return
	}
	if err := model.CheckLogExportDiskSpace(); err != nil {
		common.ApiErrorI18n(c, i18n.MsgLogExportDiskFull)
		return
	}
	job.JobID = model.NewLogExportJobID()
	if !model.AcquireLogExportSlot(job.JobID, setting.GetMaxConcurrentJobs(), time.Duration(setting.GetTimeoutSec())*time.Second) {
		common.ApiErrorI18n(c, i18n.MsgLogExportBusy)
		return
	}
	if model.CheckAndSetLogExportCooldown(job.UserID) {
		model.ReleaseLogExportSlot(job.JobID)
		common.ApiErrorI18n(c, i18n.MsgLogExportCooldown, map[string]any{"Minutes": (setting.GetUserCooldownSec() + 59) / 60})
		return
	}
	if err := model.CreateLogExportJob(job); err != nil {
		model.ReleaseLogExportSlot(job.JobID)
		model.ClearLogExportCooldown(job.UserID)
		employeeExportError(c, err)
		return
	}
	model.StartLogExport(job)
	auditEmployeeExportJobCreated(c, job)
	common.ApiSuccess(c, gin.H{"job_id": job.JobID})
}

func EmployeeExportEstimate(c *gin.Context) {
	job, err := prepareEmployeeExport(c)
	if err != nil {
		employeeExportError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), logExportEstimateTimeout)
	defer cancel()
	limit := operation_setting.GetLogExportSetting().GetXlsxMaxRows() + 1
	var total int64
	capped := false
	for _, id := range job.EmployeeScope.CustomerIDs {
		job.Filters.UserId = id
		rows, cap, err := model.EstimateLogExportRows(ctx, job.Filters, limit-int(total))
		if err != nil {
			logger.LogError(c, "employee export estimate: "+err.Error())
			common.ApiSuccess(c, gin.H{"rows": 0, "available": false, "capped": false})
			return
		}
		total += rows
		if cap || total >= int64(limit) {
			capped = true
			break
		}
	}
	common.ApiSuccess(c, gin.H{"rows": total, "available": true, "capped": capped})
}

// auditEmployeeExportJobCreated adds the template and customer count so the audit shows what was exported.
func auditEmployeeExportJobCreated(c *gin.Context, job *model.LogExportJob) {
	auditLogExportJobCreated(c, job, map[string]interface{}{
		"template_key": job.EmployeeScope.TemplateKey, "template_version": job.EmployeeScope.TemplateVersion,
		"customer_count": len(job.EmployeeScope.CustomerIDs),
	})
}

// Dashboard-only guard. It must never be attached to a relay route.
func EmployeeExportGuard(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := model.AuthorizeEmployeeExport(ctx, c.GetInt("id")); err != nil {
		employeeExportError(c, err)
		c.Abort()
		return
	}
	c.Next()
}
