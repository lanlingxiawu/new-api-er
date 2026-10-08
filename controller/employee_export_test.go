package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func employeeControllerExportFixture(t *testing.T) (int, int, *model.EmployeeExportTemplate) {
	t.Helper()
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.EmployeeExportTemplate{}))
	employee := mkControllerExportEmployee(t)
	customer := mkUser(t, func(u *model.User) { u.InviterId = employee.Id })
	tpl := &model.EmployeeExportTemplate{
		Name: uniq("employee_tpl"), Enabled: true, Columns: []string{"created_at", "cost_usd"},
		Format: "csv_gz", Mode: "detail", AllowedFilters: []string{"model_name"},
		Options: model.LogExportOptions{CSVBOM: true, Header: true, Timezone: "UTC"},
	}
	require.NoError(t, model.SaveEmployeeExportTemplate(context.Background(), tpl, employee.Id))
	t.Cleanup(func() { model.DB.Delete(&model.EmployeeExportTemplate{}, tpl.Id) })
	withExportSetting(t, func(s *operation_setting.LogExportSetting) { s.Enabled = true; s.EmployeeExportEnabled = true })
	return employee.Id, customer.Id, tpl
}

func mkControllerExportEmployee(t *testing.T) *model.User {
	t.Helper()
	employee := mkUser(t, nil)
	profile := &model.EmployeeProfile{UserId: employee.Id, Status: 1}
	require.NoError(t, model.DB.Create(profile).Error)
	deleteByID(t, &model.EmployeeProfile{}, profile.Id)
	return employee
}

func TestEmployeeExportRequestCannotOverrideTemplateOrCustomerScope(t *testing.T) {
	employee, customer, tpl := employeeControllerExportFixture(t)
	now := time.Now().Unix()
	body := map[string]any{"template_id": tpl.Key, "timezone": "Asia/Tokyo", "customer_ids": []int{customer}, "start_timestamp": now - 60, "end_timestamp": now}
	ctx, _ := newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	job, err := prepareEmployeeExport(asUser(ctx, employee))
	require.NoError(t, err)
	require.Equal(t, tpl.Columns, job.Columns)
	require.Equal(t, tpl.Options, job.Options, "a template-pinned timezone wins over the requester's")
	require.Equal(t, model.LogTypeConsume, job.Filters.LogType)
	require.Equal(t, []int{customer}, job.EmployeeScope.CustomerIDs)
	var customerUser model.User
	require.NoError(t, model.DB.First(&customerUser, customer).Error)
	require.Equal(t, customerUser.Username, job.EmployeeScope.CustomerName, "a single customer is named in the download file")
	require.False(t, job.EmployeeScope.AllCustomers)

	for _, key := range []string{"columns", "options", "format", "mode", "summary_dims", "username", "user_id", "employee_scope"} {
		body[key] = nil
		ctx, _ := newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
		_, err := prepareEmployeeExport(asUser(ctx, employee))
		require.ErrorIs(t, err, model.ErrEmployeeExportInvalid, key)
		delete(body, key)
	}
	other := mkUser(t, nil)
	body["customer_ids"] = []int{customer, other.Id}
	ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	_, err = prepareEmployeeExport(asUser(ctx, employee))
	require.ErrorIs(t, err, model.ErrEmployeeExportAccess)
	body["customer_ids"] = []int{customer}
	body["filters"] = map[string]string{"channel": "1"}
	ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	_, err = prepareEmployeeExport(asUser(ctx, employee))
	require.ErrorIs(t, err, model.ErrEmployeeExportInvalid)
	body["filters"] = map[string]string{"model_name": "gpt-test"}
	ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	job, err = prepareEmployeeExport(asUser(ctx, employee))
	require.NoError(t, err)
	require.Equal(t, "gpt-test", job.Filters.ModelName)
	for _, id := range []string{strconv.Itoa(tpl.Id + 100000000), model.LogExportTemplateBilling, model.LogExportTemplateAudit, ""} {
		body["template_id"] = id
		ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
		_, err = prepareEmployeeExport(asUser(ctx, employee))
		require.ErrorIs(t, err, model.ErrEmployeeExportAccess, id)
	}
}

// Every active employee may use the shared builtin reconciliation templates without any per-employee setup.
func TestEmployeeExportBuiltinTemplateForAnyEmployee(t *testing.T) {
	_, _, _ = employeeControllerExportFixture(t)
	employee := mkControllerExportEmployee(t)
	customer := mkUser(t, func(u *model.User) { u.InviterId = employee.Id })
	now := time.Now().Unix()
	body := map[string]any{"template_id": model.LogExportTemplateCustomerInvoice, "timezone": "Asia/Tokyo", "all_customers": true, "start_timestamp": now - 60, "end_timestamp": now}
	ctx, _ := newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	job, err := prepareEmployeeExport(asUser(ctx, employee.Id))
	require.NoError(t, err)
	builtin := model.EmployeeBuiltinExportTemplates()[0]
	require.Equal(t, model.LogExportTemplateCustomerInvoice, builtin.Key)
	require.Equal(t, builtin.Columns, job.Columns)
	require.Equal(t, model.LogExportFormatCSVGz, job.Format, "builtin templates export a compressed package")
	require.True(t, job.Options.CSVBOM)
	require.True(t, job.Options.Header)
	require.Equal(t, "Asia/Tokyo", job.Options.Timezone, "builtin templates export in the requester's timezone")
	require.Equal(t, model.LogExportTemplateCustomerInvoice, job.EmployeeScope.TemplateKey)
	require.Equal(t, []int{customer.Id}, job.EmployeeScope.CustomerIDs)
	require.True(t, job.EmployeeScope.AllCustomers)
	require.Empty(t, job.EmployeeScope.CustomerName, "all-customer exports are labelled as such, not by the only customer")

	// A zone missing from the server's tzdata must not block the export; it falls back to server time.
	body["timezone"] = "Not/AZone"
	ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	job, err = prepareEmployeeExport(asUser(ctx, employee.Id))
	require.NoError(t, err)
	require.Empty(t, job.Options.Timezone)
	delete(body, "timezone")
	ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	job, err = prepareEmployeeExport(asUser(ctx, employee.Id))
	require.NoError(t, err)
	require.Empty(t, job.Options.Timezone)

	nonEmployee := mkUser(t, nil)
	ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	_, err = prepareEmployeeExport(asUser(ctx, nonEmployee.Id))
	require.ErrorIs(t, err, model.ErrEmployeeExportAccess)
}

func TestEmployeeExportCapabilitiesListSharedTemplates(t *testing.T) {
	employee, _, tpl := employeeControllerExportFixture(t)
	disabled := &model.EmployeeExportTemplate{Name: uniq("employee_tpl_off"), Enabled: false, Columns: []string{"created_at"}, Format: "csv_gz", Mode: "detail"}
	require.NoError(t, model.SaveEmployeeExportTemplate(context.Background(), disabled, employee))
	t.Cleanup(func() { model.DB.Delete(&model.EmployeeExportTemplate{}, disabled.Id) })
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/export/capabilities", nil)
	EmployeeExportCapabilities(asUser(ctx, employee))
	response := decodeResp(t, rec)
	require.True(t, response.Success)
	var capabilities struct {
		Enabled   bool                           `json:"enabled"`
		Templates []model.EmployeeExportTemplate `json:"templates"`
		Columns   []logExportColumnDTO           `json:"columns"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &capabilities))
	require.True(t, capabilities.Enabled)
	keys := []string{}
	for _, item := range capabilities.Templates {
		keys = append(keys, item.Key)
	}
	require.Equal(t, model.LogExportTemplateCustomerInvoice, keys[0])
	require.NotContains(t, keys, model.LogExportTemplateBilling, "only the customer invoice is built in")
	require.NotContains(t, keys, model.LogExportTemplateLegacy)
	require.Contains(t, keys, tpl.Key)
	require.NotContains(t, keys, disabled.Key)
	for _, column := range capabilities.Columns {
		require.NotEqual(t, "channel_id", column.Key)
		require.NotEqual(t, "retry_chain", column.Key)
	}
}

func TestEmployeeExportDownloadRechecksCustomerTransfer(t *testing.T) {
	employee, customer, tpl := employeeControllerExportFixture(t)
	enableRedis(t)
	now := time.Now().Unix()
	ctx, _ := newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", map[string]any{
		"template_id": tpl.Key, "customer_ids": []int{customer}, "start_timestamp": now - 60, "end_timestamp": now,
	})
	job, err := prepareEmployeeExport(asUser(ctx, employee))
	require.NoError(t, err)
	job.JobID = model.NewLogExportJobID()
	require.NoError(t, model.CreateLogExportJob(job))
	t.Cleanup(func() { model.DeleteLogExportJob(job) })
	path := filepath.Join(t.TempDir(), "part.csv.gz")
	require.NoError(t, os.WriteFile(path, []byte("test-export"), 0600))
	job.Parts = []model.LogExportPart{{Index: 1, Path: path, Rows: 1}}
	job.Status = model.LogExportStatusReady
	model.UpdateLogExportJob(job)
	token, err := model.CreateLogExportDownloadToken(job.JobID, employee, 1)
	require.NoError(t, err)
	ctx, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	ctx.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(ctx)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "test-export", rec.Body.String())
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", customer).Update("inviter_id", 0).Error)
	// Even an already redeemed resumable download token must fail after transfer.
	ctx, rec = newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	ctx.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(ctx)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotContains(t, rec.Body.String(), "test-export")
	ctx, rec = newCtx(t, http.MethodGet, "/api/user/employee/export/jobs/"+job.JobID+"/download-url", nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
	GetLogExportDownloadURL(asUser(ctx, employee))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestEmployeeExportTemplateDeleteAndAuthorization(t *testing.T) {
	employee, _, tpl := employeeControllerExportFixture(t)
	t.Cleanup(func() { model.LOG_DB.Where("user_id = ?", employee).Delete(&model.AuditLog{}) })
	var before int64
	require.NoError(t, model.LOG_DB.Model(&model.AuditLog{}).Where("user_id = ? AND action = ?", employee, "employee_export.template_delete").Count(&before).Error)
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee-export/templates/"+strconv.Itoa(tpl.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tpl.Id)}}
	AdminEmployeeExportTemplates(asAdmin(ctx, employee))
	require.True(t, decodeResp(t, rec).Success)
	require.Contains(t, rec.Body.String(), tpl.Name)
	ctx, rec = newCtx(t, http.MethodDelete, "/api/admin/employee-export/templates/"+strconv.Itoa(tpl.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tpl.Id)}}
	AdminEmployeeExportTemplates(asAdmin(ctx, employee))
	require.True(t, decodeResp(t, rec).Success)
	_, err := model.LookupEmployeeExportTemplate(context.Background(), tpl.Key)
	require.ErrorIs(t, err, model.ErrEmployeeExportAccess)
	require.Eventually(t, func() bool {
		var count int64
		err := model.LOG_DB.Model(&model.AuditLog{}).Where("user_id = ? AND action = ?", employee, "employee_export.template_delete").Count(&count).Error
		return err == nil && count > before
	}, 3*time.Second, 10*time.Millisecond)
}

func TestEmployeeExportCapabilitiesDisabled(t *testing.T) {
	withExportSetting(t, func(s *operation_setting.LogExportSetting) { s.EmployeeExportEnabled = false })
	ctx, rec := newCtx(t, http.MethodGet, "/api/user/employee/export/capabilities", nil)
	EmployeeExportCapabilities(asUser(ctx, 1))
	require.True(t, decodeResp(t, rec).Success)
	require.Contains(t, strings.ReplaceAll(rec.Body.String(), " ", ""), `"enabled":false`)
}

func TestEmployeeExportOptionsRequireGrantedFilter(t *testing.T) {
	employee, customer, tpl := employeeControllerExportFixture(t)
	now := time.Now().Unix() - 120
	log := &model.Log{UserId: customer, CreatedAt: now, Type: model.LogTypeConsume, ModelName: "permitted-model", RequestId: uniq("export-options")}
	require.NoError(t, model.LOG_DB.Create(log).Error)
	t.Cleanup(func() { model.LOG_DB.Where("id = ?", log.Id).Delete(&model.Log{}) })
	engine := gin.New()
	engine.GET("/api/user/employee/export/options", func(c *gin.Context) { asUser(c, employee); GetLogExportFilterOptions(c) })
	for _, tc := range []struct {
		field      string
		templateID string
		status     int
	}{
		{"model_name", tpl.Key, http.StatusOK},
		{"group", tpl.Key, http.StatusForbidden},
		{"channel", tpl.Key, http.StatusForbidden},
		{"model_name", strconv.Itoa(tpl.Id + 100000000), http.StatusForbidden},
		{"model_name", model.LogExportTemplateCustomerInvoice, http.StatusOK},
		{"model_name", model.LogExportTemplateAudit, http.StatusForbidden},
	} {
		path := "/api/user/employee/export/options?field=" + tc.field + "&template_id=" + tc.templateID + "&start_timestamp=" + strconv.FormatInt(now-1, 10) + "&end_timestamp=" + strconv.FormatInt(now+1, 10) + "&customer_ids=" + strconv.Itoa(customer)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, tc.status, rec.Code)
		if tc.status == http.StatusOK {
			require.Contains(t, rec.Body.String(), "permitted-model")
		} else {
			require.NotContains(t, rec.Body.String(), "permitted-model")
		}
	}
}

func TestEmployeeExportCatalogOffersInternalColumnsButNotAdminOnly(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/admin/employee-export/columns", nil)
	GetEmployeeExportColumns(asAdmin(ctx, 1))
	response := decodeResp(t, rec)
	require.True(t, response.Success)
	var catalog struct {
		Columns  []logExportColumnDTO `json:"columns"`
		Defaults []string             `json:"default_columns"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &catalog))
	require.Contains(t, catalog.Defaults, "username")
	require.NotContains(t, catalog.Defaults, "quota", "new templates start from the customer invoice")
	audiences := map[string]string{}
	for _, column := range catalog.Columns {
		require.False(t, column.AdminOnly, column.Key)
		audiences[column.Key] = column.Audience
	}
	require.Equal(t, "internal", audiences["quota"], "internal columns are offered and badged")
	require.Equal(t, "internal", audiences["ip"])
	require.Equal(t, "customer", audiences["username"])
	require.Equal(t, "customer", audiences["cost_usd"])
	require.NotContains(t, audiences, "channel_name")
	require.NotContains(t, audiences, "channel_id")
}

func TestEmployeeExportErrorMessagesAreActionable(t *testing.T) {
	withExportSetting(t, func(s *operation_setting.LogExportSetting) { s.EmployeeMaxCustomersPerJob = 7 })
	for _, tc := range []struct {
		err     error
		status  int
		message string
	}{
		{model.ErrEmployeeExportTooManyCustomers, http.StatusOK, "7"},
		{model.ErrEmployeeExportNoCustomers, http.StatusOK, ""},
		{model.ErrEmployeeExportNameTaken, http.StatusOK, ""},
		{model.ErrEmployeeExportConflict, http.StatusOK, ""},
		{model.ErrEmployeeExportInvalid, http.StatusOK, ""},
		{model.ErrEmployeeExportAccess, http.StatusForbidden, ""},
	} {
		ctx, rec := newCtx(t, http.MethodGet, "/", nil)
		employeeExportError(ctx, tc.err)
		require.Equal(t, tc.status, rec.Code, tc.err.Error())
		var response apiResp
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
		require.False(t, response.Success)
		require.NotContains(t, response.Message, tc.err.Error(), "internal error text must not reach the user")
		require.Contains(t, response.Message, tc.message)
	}
	messages := map[string]bool{}
	for _, err := range []error{model.ErrEmployeeExportTooManyCustomers, model.ErrEmployeeExportNoCustomers, model.ErrEmployeeExportNameTaken, model.ErrEmployeeExportConflict, model.ErrEmployeeExportInvalid} {
		ctx, rec := newCtx(t, http.MethodGet, "/", nil)
		employeeExportError(ctx, err)
		messages[decodeResp(t, rec).Message] = true
	}
	require.Len(t, messages, 5, "each failure gets its own message")
}

// Admins who are also employees must only see and act on employee jobs through the employee routes.
func TestEmployeeExportRoutesHideAdminJobs(t *testing.T) {
	employee, customer, tpl := employeeControllerExportFixture(t)
	enableRedis(t)
	now := time.Now().Unix()
	adminJob := &model.LogExportJob{JobID: model.NewLogExportJobID(), UserID: employee, Status: model.LogExportStatusReady,
		Filters: model.LogExportFilter{StartTimestamp: now - 60, EndTimestamp: now}}
	require.NoError(t, model.CreateLogExportJob(adminJob))
	t.Cleanup(func() { model.DeleteLogExportJob(adminJob) })
	employeeJob := &model.LogExportJob{JobID: model.NewLogExportJobID(), UserID: employee, Status: model.LogExportStatusReady,
		Filters:       model.LogExportFilter{StartTimestamp: now - 60, EndTimestamp: now},
		EmployeeScope: &model.EmployeeExportScope{TemplateKey: tpl.Key, CustomerIDs: []int{customer}}}
	require.NoError(t, model.CreateLogExportJob(employeeJob))
	t.Cleanup(func() { model.DeleteLogExportJob(employeeJob) })

	engine := gin.New()
	engine.GET("/api/user/employee/export/jobs", func(c *gin.Context) { asAdmin(c, employee); GetLogExportJobs(c) })
	engine.GET("/api/user/employee/export/jobs/:job_id", func(c *gin.Context) { asAdmin(c, employee); GetLogExportJob(c) })
	engine.GET("/api/log/export/jobs", func(c *gin.Context) { asAdmin(c, employee); GetLogExportJobs(c) })

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/user/employee/export/jobs", nil))
	require.Contains(t, rec.Body.String(), employeeJob.JobID)
	require.NotContains(t, rec.Body.String(), adminJob.JobID)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/user/employee/export/jobs/"+adminJob.JobID, nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/log/export/jobs", nil))
	require.Contains(t, rec.Body.String(), adminJob.JobID, "the admin export center keeps its existing view")
}

func TestEmployeeExportJobAuditIncludesScope(t *testing.T) {
	employee, customer, tpl := employeeControllerExportFixture(t)
	t.Cleanup(func() { model.LOG_DB.Where("user_id = ?", employee).Delete(&model.AuditLog{}) })
	now := time.Now().Unix()
	ctx, _ := newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", map[string]any{
		"template_id": tpl.Key, "customer_ids": []int{customer}, "start_timestamp": now - 60, "end_timestamp": now,
	})
	ctx = asUser(ctx, employee)
	job, err := prepareEmployeeExport(ctx)
	require.NoError(t, err)
	job.JobID = model.NewLogExportJobID()
	auditEmployeeExportJobCreated(ctx, job)
	require.Eventually(t, func() bool {
		var logs []model.AuditLog
		err := model.LOG_DB.Where("user_id = ?", employee).Find(&logs).Error
		if err != nil {
			return false
		}
		for _, log := range logs {
			metadata, err := common.Marshal(log.Other)
			if err == nil && strings.Contains(string(metadata), job.JobID) && strings.Contains(string(metadata), `"template_key":"`+tpl.Key+`"`) && strings.Contains(string(metadata), `"customer_count":1`) {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
}
