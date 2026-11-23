package controller

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEmployeeExportTroubleshootingFilters(t *testing.T) {
	allowed := []string{"model_name", "token_name", "group", "anomaly_only", "anomaly_kinds", "charged", "quota_min", "completion_tokens_min", "completion_tokens_max", "use_time_min", "min_retry_count"}
	for _, tc := range []struct {
		name, body string
		invalid    bool
	}{
		{"legacy strings", `{"model_name":"gpt-test","token_name":"key","group":"default"}`, false},
		{"zero output charged", `{"charged":true,"completion_tokens_max":0}`, false},
		{"false and zero", `{"charged":false,"quota_min":0,"min_retry_count":0}`, false},
		{"all conditions", `{"anomaly_only":true,"anomaly_kinds":["estimated_usage"],"quota_min":1,"completion_tokens_min":2,"completion_tokens_max":3,"use_time_min":4,"min_retry_count":5}`, false},
		{"clear conditions", `{"charged":null,"quota_min":null,"anomaly_kinds":[]}`, false},
		{"negative", `{"quota_min":-1}`, true},
		{"fraction", `{"use_time_min":0.5}`, true},
		{"wrong boolean type", `{"charged":"false"}`, true},
		{"wrong string type", `{"model_name":1}`, true},
		{"wrong list type", `{"anomaly_kinds":"retried"}`, true},
		{"unknown anomaly", `{"anomaly_kinds":["unknown"]}`, true},
		{"inverted bounds", `{"completion_tokens_min":2,"completion_tokens_max":1}`, true},
		{"unsafe integer", `{"quota_min":9007199254740992}`, true},
		{"scope override", `{"user_id":1}`, true},
		{"channel override", `{"channel":1}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req employeeExportRequest
			require.NoError(t, common.UnmarshalJsonStr(`{"filters":`+tc.body+`}`, &req))
			filter, err := buildEmployeeExportFilter(req.Filters, allowed)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			switch tc.name {
			case "legacy strings":
				require.Equal(t, "gpt-test", filter.ModelName)
				require.Equal(t, "key", filter.TokenName)
				require.Equal(t, "default", filter.Group)
			case "zero output charged":
				require.NotNil(t, filter.Charged)
				require.True(t, *filter.Charged)
				require.NotNil(t, filter.CompletionTokensMax)
				require.Zero(t, *filter.CompletionTokensMax)
			case "false and zero":
				require.NotNil(t, filter.Charged)
				require.False(t, *filter.Charged)
				require.NotNil(t, filter.QuotaMin)
				require.Zero(t, *filter.QuotaMin)
				require.True(t, filter.HasRowFilter())
			case "all conditions":
				require.True(t, filter.AnomalyOnly)
				require.Equal(t, []string{"estimated_usage"}, filter.AnomalyKinds)
				require.Equal(t, 1, *filter.QuotaMin)
				require.Equal(t, 2, *filter.CompletionTokensMin)
				require.Equal(t, 3, *filter.CompletionTokensMax)
				require.Equal(t, 4, *filter.UseTimeMin)
				require.Equal(t, 5, *filter.MinRetryCount)
			case "clear conditions":
				require.Nil(t, filter.Charged)
				require.Nil(t, filter.QuotaMin)
				require.False(t, filter.HasRowFilter())
			}
		})
	}
	var req employeeExportRequest
	require.NoError(t, common.UnmarshalJsonStr(`{"filters":{"charged":true}}`, &req))
	_, err := buildEmployeeExportFilter(req.Filters, []string{"model_name"})
	require.ErrorIs(t, err, model.ErrEmployeeExportInvalid)
}

func TestEmployeeExportTroubleshootingFilterLimits(t *testing.T) {
	withExportSetting(t, func(s *operation_setting.LogExportSetting) { s.MaxFilterValues = 1 })
	for _, body := range []string{
		`{"anomaly_kinds":["retried","estimated_usage"]}`,
		`{"model_name":"` + strings.Repeat("a", 257) + `"}`,
		`{"completion_tokens_min":-1}`, `{"completion_tokens_max":-1}`,
		`{"use_time_min":-1}`, `{"min_retry_count":-1}`,
	} {
		var req employeeExportRequest
		require.NoError(t, common.UnmarshalJsonStr(`{"filters":`+body+`}`, &req))
		_, err := buildEmployeeExportFilter(req.Filters, []string{"anomaly_kinds", "model_name", "completion_tokens_min", "completion_tokens_max", "use_time_min", "min_retry_count"})
		require.Error(t, err, body)
	}
}

func employeeExportFilterTestDB(t *testing.T) {
	t.Helper()
	// Prefer configured project databases; isolate SQLite only when no DB environment is available.
	if model.DB == nil || model.LOG_DB == nil {
		oldDB, oldLogDB := model.DB, model.LOG_DB
		oldType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}, &model.Log{}))
		model.DB, model.LOG_DB = db, db
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
		common.SetLogDatabaseType(common.DatabaseTypeSQLite)
		model.InitColumnNames()
		t.Cleanup(func() {
			model.DB, model.LOG_DB = oldDB, oldLogDB
			common.SetMainDatabaseType(oldType)
			common.SetLogDatabaseType(oldLogType)
			model.InitColumnNames()
			_ = sqlDB.Close()
		})
	}
}

func TestEmployeeExportTroubleshootingEstimateScope(t *testing.T) {
	employeeExportFilterTestDB(t)
	employee, customer, _ := employeeControllerExportFixture(t)
	other := mkUser(t, nil)
	now := time.Now().Unix()
	for _, row := range []model.Log{
		{UserId: customer, Quota: 1, CompletionTokens: 0},
		{UserId: customer, Quota: 0, CompletionTokens: 0},
		{UserId: customer, Quota: 1, CompletionTokens: 5},
		{UserId: other.Id, Quota: 1, CompletionTokens: 0},
	} {
		row.Type, row.CreatedAt, row.RequestId = model.LogTypeConsume, now, uniq("employee-filter")
		require.NoError(t, model.LOG_DB.Create(&row).Error)
		t.Cleanup(func() { model.LOG_DB.Delete(&model.Log{}, row.Id) })
	}
	body := map[string]any{"template_id": model.LogExportTemplateCustomerInvoice, "customer_ids": []int{customer}, "start_timestamp": now - 60, "end_timestamp": now,
		"filters": map[string]any{"charged": true, "completion_tokens_max": 0}}
	ctx, _ := newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	job, err := prepareEmployeeExport(asUser(ctx, employee))
	require.NoError(t, err)
	require.Equal(t, []int{customer}, job.EmployeeScope.CustomerIDs)
	job.Filters.UserId = job.EmployeeScope.CustomerIDs[0]
	rows, capped, err := model.EstimateLogExportRows(context.Background(), job.Filters, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	require.False(t, capped)
	body["filters"] = map[string]any{"charged": false, "completion_tokens_max": 0}
	ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	job, err = prepareEmployeeExport(asUser(ctx, employee))
	require.NoError(t, err)
	job.Filters.UserId = customer
	rows, _, err = model.EstimateLogExportRows(context.Background(), job.Filters, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	body["customer_ids"] = []int{other.Id}
	ctx, _ = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
	_, err = prepareEmployeeExport(asUser(ctx, employee))
	require.ErrorIs(t, err, model.ErrEmployeeExportAccess)
}

func TestEmployeeExportLogTypeEstimateAndDownload(t *testing.T) {
	employeeExportFilterTestDB(t)
	exportManagementFixture(t)
	employee, customer, tpl := employeeControllerExportFixture(t)
	other := mkUser(t, nil)
	tpl.Columns = []string{"model_name", "type"}
	require.NoError(t, model.SaveEmployeeExportTemplate(context.Background(), tpl, employee))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.AuditLog{}))
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs, s.CPUSoftLimit, s.CPUHardLimit = 0, 99, 100
		s.MaxRowsPerSec, s.BatchSize = 100000, 100
		s.OffpeakOnly, s.MinFreeDiskMB = false, 0
	})
	now := time.Now().Unix() - 120
	for _, row := range []model.Log{
		{UserId: customer, Type: model.LogTypeConsume, ModelName: "consume-only", CreatedAt: now},
		{UserId: customer, Type: model.LogTypeError, ModelName: "error-only", CreatedAt: now},
		{UserId: customer, Type: model.LogTypeRefund, ModelName: "refund-only", CreatedAt: now},
		{UserId: other.Id, Type: model.LogTypeError, ModelName: "other-customer", CreatedAt: now},
		{UserId: customer, Type: model.LogTypeError, ModelName: "outside-window", CreatedAt: now - 300},
	} {
		row.RequestId = uniq("employee-log-type")
		require.NoError(t, model.LOG_DB.Create(&row).Error)
		t.Cleanup(func() { model.LOG_DB.Delete(&model.Log{}, row.Id) })
	}
	for _, tc := range []struct {
		name     string
		value    any
		wantType int
		models   []string
	}{
		{"omitted", nil, 0, []string{"consume-only", "error-only", "refund-only"}},
		{"all", 0, 0, []string{"consume-only", "error-only", "refund-only"}},
		{"consume", 2, 2, []string{"consume-only"}},
		{"error", 5, 5, []string{"error-only"}},
		{"refund", 6, 6, []string{"refund-only"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"template_id": tpl.Key, "customer_ids": []int{customer}, "start_timestamp": now - 60, "end_timestamp": now + 60}
			if tc.value != nil {
				body["type"] = tc.value
			}
			ctx, _ := newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
			job, err := prepareEmployeeExport(asUser(ctx, employee))
			require.NoError(t, err)
			require.Equal(t, tc.wantType, job.Filters.LogType)
			ctx, rec := newCtx(t, http.MethodPost, "/api/user/employee/export/estimate", body)
			EmployeeExportEstimate(asUser(ctx, employee))
			response := decodeResp(t, rec)
			require.True(t, response.Success, rec.Body.String())
			var estimate struct {
				Rows      int
				Available bool
			}
			require.NoError(t, common.Unmarshal(response.Data, &estimate))
			require.True(t, estimate.Available)
			require.Equal(t, len(tc.models), estimate.Rows)
			model.ClearLogExportCooldown(employee)
			ctx, rec = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
			EmployeeCreateExport(asUser(ctx, employee))
			response = decodeResp(t, rec)
			require.True(t, response.Success, rec.Body.String())
			var created struct {
				JobID string `json:"job_id"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &created))
			cleanupExportJob(t, created.JobID)
			require.Eventually(t, func() bool {
				job, err = model.GetLogExportJob(created.JobID)
				return err == nil && job != nil && job.IsTerminal()
			}, 15*time.Second, 20*time.Millisecond)
			require.Equal(t, model.LogExportStatusReady, job.Status, job.Error)
			require.Equal(t, tc.wantType, job.Filters.LogType)
			token, err := model.CreateManagedLogExportDownloadToken(job.JobID, employee, 1, "employee")
			require.NoError(t, err)
			ctx, rec = newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
			ctx.Params = gin.Params{{Key: "token", Value: token}}
			DownloadLogExport(ctx)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			compressed, err := gzip.NewReader(rec.Body)
			require.NoError(t, err)
			defer compressed.Close()
			records, err := csv.NewReader(compressed).ReadAll()
			require.NoError(t, err)
			require.Len(t, records, estimate.Rows+1)
			var models []string
			for _, row := range records[1:] {
				models = append(models, row[0])
			}
			require.ElementsMatch(t, tc.models, models)
		})
	}
	for _, template := range []string{tpl.Key, model.LogExportTemplateCustomerInvoice} {
		for _, value := range []any{0, 1, 2, 3, 4, 5, 6, 7, -1, 8, 1.5, "5", true} {
			body := map[string]any{"template_id": template, "customer_ids": []int{customer}, "start_timestamp": now - 60, "end_timestamp": now + 60, "type": value}
			ctx, _ := newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
			job, err := prepareEmployeeExport(asUser(ctx, employee))
			if n, ok := value.(int); ok && n >= 0 && n <= 7 {
				require.NoError(t, err)
				require.Equal(t, n, job.Filters.LogType)
			} else {
				require.Error(t, err)
			}
		}
	}
	for _, tc := range []struct {
		query string
		want  []string
		valid bool
	}{
		{"", []string{"consume-only", "error-only", "refund-only"}, true},
		{"&type=0", []string{"consume-only", "error-only", "refund-only"}, true},
		{"&type=2", []string{"consume-only"}, true},
		{"&type=5", []string{"error-only"}, true},
		{"&type=-1", nil, false}, {"&type=8", nil, false}, {"&type=x", nil, false}, {"&type=1.5", nil, false},
	} {
		path := "/api/user/employee/export/options?field=model_name&template_id=" + tpl.Key + "&customer_ids=" + strconv.Itoa(customer) + "&start_timestamp=" + strconv.FormatInt(now-60, 10) + "&end_timestamp=" + strconv.FormatInt(now+60, 10) + tc.query
		ctx, rec := newCtx(t, http.MethodGet, path, nil)
		GetLogExportFilterOptions(asUser(ctx, employee))
		response := decodeResp(t, rec)
		require.Equal(t, tc.valid, response.Success, path)
		if !tc.valid {
			continue
		}
		var data struct{ Items []model.LogExportFilterOption }
		require.NoError(t, common.Unmarshal(response.Data, &data))
		var values []string
		for _, item := range data.Items {
			values = append(values, item.Value)
		}
		require.ElementsMatch(t, tc.want, values, path)
	}
}

func TestEmployeeExportConsumeDetailAndSummaryDownload(t *testing.T) {
	employeeExportFilterTestDB(t)
	exportManagementFixture(t)
	employee, customer, tpl := employeeControllerExportFixture(t)
	other := mkUser(t, nil)
	tpl.Columns = []string{"username", "created_at", "request_id", "model_name", "token_name", "group", "prompt_tokens", "completion_tokens", "quota"}
	tpl.Mode = model.LogExportModeBoth
	tpl.SummaryDims = []string{"date", "model_name", "token_name", "group"}
	tpl.Options.Timezone = "Asia/Shanghai"
	tpl.AllowedFilters = model.EmployeeBuiltinExportTemplates()[0].AllowedFilters
	require.NoError(t, model.SaveEmployeeExportTemplate(context.Background(), tpl, employee))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.AuditLog{}))
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs, s.CPUSoftLimit, s.CPUHardLimit = 0, 99, 100
		s.MaxRowsPerSec, s.BatchSize = 100000, 100
		s.OffpeakOnly, s.MinFreeDiskMB = false, 0
	})
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	now := time.Date(2026, 11, 20, 23, 59, 50, 0, time.UTC).Unix()
	for _, row := range []model.Log{
		{UserId: customer, Type: model.LogTypeConsume, ModelName: "consume-only", CreatedAt: now, PromptTokens: 100, CompletionTokens: 40, Quota: 500000},
		{UserId: customer, Type: model.LogTypeConsume, ModelName: "consume-only", CreatedAt: now + 1, PromptTokens: 200, CompletionTokens: 80, Quota: 1000000},
		{UserId: customer, Type: model.LogTypeError, ModelName: "error-only", CreatedAt: now},
		{UserId: other.Id, Type: model.LogTypeConsume, ModelName: "other-customer", CreatedAt: now, Quota: 9000000},
		{UserId: customer, Type: model.LogTypeConsume, ModelName: "outside-window", CreatedAt: now - 300, Quota: 9000000},
	} {
		row.TokenName, row.Group, row.RequestId = "customer-key", "default", uniq("employee-summary")
		require.NoError(t, model.LOG_DB.Create(&row).Error)
		t.Cleanup(func() { model.LOG_DB.Delete(&model.Log{}, row.Id) })
	}
	for _, tc := range []struct {
		name    string
		logType int
		models  []string
		summary [][]string
	}{
		{"all", model.LogTypeUnknown, []string{"consume-only", "consume-only", "error-only"}, [][]string{
			{"2026-11-21", "consume-only", "customer-key", "default", "2", "300", "120", "420", "1500000", "3.000000"},
			{"2026-11-21", "error-only", "customer-key", "default", "1", "0", "0", "0", "0", "0.000000"},
		}},
		{"consume", model.LogTypeConsume, []string{"consume-only", "consume-only"}, [][]string{
			{"2026-11-21", "consume-only", "customer-key", "default", "2", "300", "120", "420", "1500000", "3.000000"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"template_id": tpl.Key, "type": tc.logType, "customer_ids": []int{customer}, "start_timestamp": now - 60, "end_timestamp": now + 60, "filters": map[string]any{}}
			ctx, rec := newCtx(t, http.MethodPost, "/api/user/employee/export/estimate", body)
			EmployeeExportEstimate(asUser(ctx, employee))
			response := decodeResp(t, rec)
			require.True(t, response.Success, rec.Body.String())
			var estimate struct {
				Rows      int
				Available bool
			}
			require.NoError(t, common.Unmarshal(response.Data, &estimate))
			require.True(t, estimate.Available)
			require.Equal(t, len(tc.models), estimate.Rows)
			model.ClearLogExportCooldown(employee)
			ctx, rec = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
			EmployeeCreateExport(asUser(ctx, employee))
			response = decodeResp(t, rec)
			require.True(t, response.Success, rec.Body.String())
			var created struct {
				JobID string `json:"job_id"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &created))
			cleanupExportJob(t, created.JobID)
			var job *model.LogExportJob
			require.Eventually(t, func() bool {
				var err error
				job, err = model.GetLogExportJob(created.JobID)
				return err == nil && job != nil && job.IsTerminal()
			}, 15*time.Second, 20*time.Millisecond)
			require.Equal(t, model.LogExportStatusReady, job.Status, job.Error)
			require.Equal(t, tc.logType, job.Filters.LogType)
			require.Len(t, job.Parts, 2)
			require.Equal(t, model.LogExportPartKindSummary, job.Parts[1].Kind)
			ctx, rec = newCtx(t, http.MethodGet, "/api/user/employee/export/jobs/"+job.JobID+"/download-url", nil)
			ctx.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
			GetLogExportDownloadURL(asUser(ctx, employee))
			response = decodeResp(t, rec)
			require.True(t, response.Success, rec.Body.String())
			var download struct{ URL string }
			require.NoError(t, common.Unmarshal(response.Data, &download))
			ctx, rec = newCtx(t, http.MethodGet, download.URL, nil)
			ctx.Params = gin.Params{{Key: "token", Value: strings.TrimPrefix(download.URL, "/dl/log-export/")}}
			DownloadLogExport(ctx)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, "application/zip", rec.Header().Get("Content-Type"))
			archive, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
			require.NoError(t, err)
			require.Len(t, archive.File, 2)
			for index, file := range archive.File {
				part, err := file.Open()
				require.NoError(t, err)
				defer part.Close()
				compressed, err := gzip.NewReader(part)
				require.NoError(t, err)
				defer compressed.Close()
				records, err := csv.NewReader(compressed).ReadAll()
				require.NoError(t, err)
				require.NotEmpty(t, records)
				if index == 1 {
					require.Equal(t, tc.summary, records[1:])
					continue
				}
				require.Len(t, records, estimate.Rows+1)
				var models []string
				for _, row := range records[1:] {
					models = append(models, row[3])
				}
				require.ElementsMatch(t, tc.models, models)
			}
		})
	}
}

func TestEmployeeExportConditionMatrix(t *testing.T) {
	employeeExportFilterTestDB(t)
	exportManagementFixture(t)
	employee, customer, tpl := employeeControllerExportFixture(t)
	second := mkUser(t, func(u *model.User) { u.InviterId = employee })
	outsider := mkUser(t, nil)
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.AuditLog{}))
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSleepMs, s.CPUSoftLimit, s.CPUHardLimit = 0, 99, 100
		s.MaxRowsPerSec, s.BatchSize = 100000, 2 // Exercise keyset pages at the same timestamp.
		s.OffpeakOnly, s.MinFreeDiskMB = false, 0
	})
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	start := time.Date(2026, 11, 20, 15, 59, 59, 0, time.UTC).Unix()
	end := start + 2 // Cross midnight in the template's Asia/Shanghai timezone.
	rows := []model.Log{
		{RequestId: "consume-zero", Type: model.LogTypeConsume, ModelName: "model_a", PromptTokens: 10, Quota: 100, UseTime: 3},
		{RequestId: "consume-estimated", Type: model.LogTypeConsume, ModelName: "model_b", TokenName: "key-b", PromptTokens: 20, CompletionTokens: 4, Quota: 200, UseTime: 5, Other: `{"stream_result":{"usage_source":"estimated","settlement_state":"settled"}}`},
		{RequestId: "consume-stream-error", Type: model.LogTypeConsume, ModelName: "modelXa", Group: "other", PromptTokens: 30, CompletionTokens: 5, UseTime: 7, Other: `{"stream_status":{"status":"error"}}`},
		{RequestId: "error-plain", Type: model.LogTypeError, ModelName: "model_a", CreatedAt: start, UseTime: 2},
		{RequestId: "error-retried", Type: model.LogTypeError, ModelName: "model_a", TokenName: "key-b", UseTime: 5, Other: `{"admin_info":{"use_channel":[1,2,3]},"stream_status":{"error_count":1}}`},
		{RequestId: "error-mixed", Type: model.LogTypeError, ModelName: "model_b", Group: "other", UseTime: 7, Other: `{"stream_result":{"usage_source":"mixed","settlement_state":"pending"}}`},
		{RequestId: "error-no-usage", Type: model.LogTypeError, ModelName: "model_a", CreatedAt: end, UseTime: 8, Other: `{"stream_result":{"usage_source":"none"},"admin_info":{"quota_saturation":{}}}`},
		{RequestId: "error-old", Type: model.LogTypeError, ModelName: "model_c", UseTime: 10, Other: "{"},
		{RequestId: "refund", Type: model.LogTypeRefund, ModelName: "refund", Quota: 50},
		{RequestId: "top-up", Type: model.LogTypeTopup, ModelName: "top-up"},
		{RequestId: "manage", Type: model.LogTypeManage, ModelName: "manage"},
		{RequestId: "system", Type: model.LogTypeSystem, ModelName: "system"},
		{RequestId: "login", Type: model.LogTypeLogin, ModelName: "login"},
		{RequestId: "second-customer", UserId: second.Id, Type: model.LogTypeError, ModelName: "model_a"},
		{RequestId: "employee-self", UserId: employee, Type: model.LogTypeError, ModelName: "model_a"},
		{RequestId: "foreign-customer", UserId: outsider.Id, Type: model.LogTypeError, ModelName: "model_a"},
		{RequestId: "before-range", Type: model.LogTypeError, ModelName: "model_a", CreatedAt: start - 1},
		{RequestId: "after-range", Type: model.LogTypeError, ModelName: "model_a", CreatedAt: end + 1},
	}
	fixture := make(map[string]model.Log, len(rows))
	for _, row := range rows {
		name := row.RequestId
		row.RequestId = uniq(name)
		if row.UserId == 0 {
			row.UserId = customer
		}
		if row.CreatedAt == 0 {
			row.CreatedAt = start + 1
		}
		if row.TokenName == "" {
			row.TokenName = "key-a"
		}
		if row.Group == "" {
			row.Group = "default"
		}
		require.NoError(t, model.LOG_DB.Create(&row).Error)
		t.Cleanup(func() { model.LOG_DB.Delete(&model.Log{}, row.Id) })
		fixture[name] = row
	}
	errors := []string{"error-plain", "error-retried", "error-mixed", "error-no-usage", "error-old"}
	all := append([]string{"consume-zero", "consume-estimated", "consume-stream-error", "refund", "top-up", "manage", "system", "login"}, errors...)
	type condition struct {
		name      string
		logType   int
		filters   map[string]any
		want      []string
		estimate  int
		customers []int
		all       bool
	}
	cases := []condition{
		{name: "all types", want: all, estimate: 13},
		{name: "consume", logType: 2, want: []string{"consume-zero", "consume-estimated", "consume-stream-error"}, estimate: 3},
		{name: "error", logType: 5, want: errors, estimate: 5},
		{name: "top-up", logType: 1, want: []string{"top-up"}, estimate: 1},
		{name: "manage historical", logType: 3, want: []string{"manage"}, estimate: 1},
		{name: "system", logType: 4, want: []string{"system"}, estimate: 1},
		{name: "refund", logType: 6, want: []string{"refund"}, estimate: 1},
		{name: "login historical", logType: 7, want: []string{"login"}, estimate: 1},
		{name: "error model exact", logType: 5, filters: map[string]any{"model_name": "model_a"}, want: []string{"error-plain", "error-retried", "error-no-usage"}, estimate: 3},
		{name: "literal underscore", filters: map[string]any{"model_name": "model_a"}, want: []string{"consume-zero", "error-plain", "error-retried", "error-no-usage"}, estimate: 4},
		{name: "explicit wildcard", logType: 5, filters: map[string]any{"model_name": "model_%"}, want: errors, estimate: 5},
		{name: "error token", logType: 5, filters: map[string]any{"token_name": "key-b"}, want: []string{"error-retried"}, estimate: 1},
		{name: "error group", logType: 5, filters: map[string]any{"group": "other"}, want: []string{"error-mixed"}, estimate: 1},
		{name: "error free zero", logType: 5, filters: map[string]any{"charged": false, "quota_min": 0, "completion_tokens_min": 0, "completion_tokens_max": 0, "use_time_min": 0, "min_retry_count": 0}, want: errors, estimate: 5},
		{name: "error charged is empty", logType: 5, filters: map[string]any{"charged": true}},
		{name: "error positive quota is empty", logType: 5, filters: map[string]any{"quota_min": 1}},
		{name: "error positive output is empty", logType: 5, filters: map[string]any{"completion_tokens_min": 1}},
		{name: "consume charged zero output", logType: 2, filters: map[string]any{"charged": true, "completion_tokens_max": 0}, want: []string{"consume-zero"}, estimate: 1},
		{name: "consume output bounds", logType: 2, filters: map[string]any{"completion_tokens_min": 4, "completion_tokens_max": 4}, want: []string{"consume-estimated"}, estimate: 1},
		{name: "consume quota boundary", logType: 2, filters: map[string]any{"quota_min": 200}, want: []string{"consume-estimated"}, estimate: 1},
		{name: "error duration boundary", logType: 5, filters: map[string]any{"use_time_min": 7}, want: []string{"error-mixed", "error-no-usage", "error-old"}, estimate: 3},
		{name: "error retry boundary", logType: 5, filters: map[string]any{"min_retry_count": 2}, want: []string{"error-retried"}, estimate: 5},
		{name: "error retry empty", logType: 5, filters: map[string]any{"min_retry_count": 3}, estimate: 5},
		{name: "error any anomaly", logType: 5, filters: map[string]any{"anomaly_only": true}, want: []string{"error-retried", "error-mixed", "error-no-usage"}, estimate: 5},
		{name: "error anomaly OR", logType: 5, filters: map[string]any{"anomaly_kinds": []string{"retried", "mixed_usage"}}, want: []string{"error-retried", "error-mixed"}, estimate: 5},
		{name: "error all conditions", logType: 5, filters: map[string]any{"model_name": "model_a", "token_name": "key-b", "group": "default", "charged": false, "quota_min": 0, "completion_tokens_min": 0, "completion_tokens_max": 0, "use_time_min": 5, "min_retry_count": 2, "anomaly_kinds": []string{"stream_error"}}, want: []string{"error-retried"}, estimate: 1},
		{name: "cleared filters", logType: 5, filters: map[string]any{"model_name": "", "token_name": "", "group": "", "charged": nil, "quota_min": nil, "anomaly_only": false, "anomaly_kinds": []string{}, "min_retry_count": nil}, want: errors, estimate: 5},
		{name: "no matching model", logType: 5, filters: map[string]any{"model_name": "missing"}},
		{name: "second customer only", logType: 5, customers: []int{second.Id}, want: []string{"second-customer"}, estimate: 1},
		{name: "selected customers", logType: 5, customers: []int{second.Id, customer}, want: append(slices.Clone(errors), "second-customer"), estimate: 6},
		{name: "all owned customers", logType: 5, all: true, want: append(slices.Clone(errors), "second-customer"), estimate: 6},
	}
	for _, anomaly := range []struct {
		kind string
		want []string
	}{
		{"estimated_usage", []string{"consume-estimated"}},
		{"mixed_usage", []string{"error-mixed"}},
		{"no_usage", []string{"error-no-usage"}},
		{"stream_error", []string{"consume-stream-error", "error-retried"}},
		{"settlement_unsettled", []string{"error-mixed"}},
		{"retried", []string{"error-retried"}},
		{"quota_saturated", []string{"error-no-usage"}},
	} {
		cases = append(cases, condition{name: anomaly.kind, filters: map[string]any{"anomaly_kinds": []string{anomaly.kind}}, want: anomaly.want, estimate: 13})
	}
	for _, mode := range []string{"builtin", model.LogExportModeDetail, model.LogExportModeBoth, model.LogExportModeSummary} {
		t.Run(mode, func(t *testing.T) {
			templateID := model.LogExportTemplateCustomerInvoice
			if mode != "builtin" {
				tpl.Mode, tpl.Columns = mode, []string{"request_id", "quota"}
				tpl.SummaryDims = []string{"date", "model_name", "token_name", "group"}
				tpl.Options.Timezone = "Asia/Shanghai"
				tpl.AllowedFilters = model.EmployeeBuiltinExportTemplates()[0].AllowedFilters
				require.NoError(t, model.SaveEmployeeExportTemplate(context.Background(), tpl, employee))
				templateID = tpl.Key
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ids := tc.customers
					if ids == nil && !tc.all {
						ids = []int{customer}
					}
					body := map[string]any{"template_id": templateID, "type": tc.logType, "customer_ids": ids, "all_customers": tc.all, "start_timestamp": start, "end_timestamp": end, "filters": tc.filters}
					ctx, rec := newCtx(t, http.MethodPost, "/api/user/employee/export/estimate", body)
					EmployeeExportEstimate(asUser(ctx, employee))
					response := decodeResp(t, rec)
					require.True(t, response.Success, rec.Body.String())
					var estimate struct {
						Rows              int
						Available, Capped bool
					}
					require.NoError(t, common.Unmarshal(response.Data, &estimate))
					require.True(t, estimate.Available)
					require.False(t, estimate.Capped)
					require.Equal(t, tc.estimate, estimate.Rows)
					model.ClearLogExportCooldown(employee)
					ctx, rec = newCtx(t, http.MethodPost, "/api/user/employee/export/jobs", body)
					EmployeeCreateExport(asUser(ctx, employee))
					response = decodeResp(t, rec)
					require.True(t, response.Success, rec.Body.String())
					var created struct {
						JobID string `json:"job_id"`
					}
					require.NoError(t, common.Unmarshal(response.Data, &created))
					cleanupExportJob(t, created.JobID)
					var job *model.LogExportJob
					require.Eventually(t, func() bool {
						var err error
						job, err = model.GetLogExportJob(created.JobID)
						return err == nil && job != nil && job.IsTerminal()
					}, 15*time.Second, 20*time.Millisecond)
					require.Equal(t, model.LogExportStatusReady, job.Status, job.Error)
					if mode == model.LogExportModeBoth {
						require.Len(t, job.Parts, 2)
					} else {
						require.Len(t, job.Parts, 1)
					}
					if mode == model.LogExportModeSummary || mode == model.LogExportModeBoth {
						require.Equal(t, model.LogExportPartKindSummary, job.Parts[len(job.Parts)-1].Kind)
					}
					wantIDs := make([]string, 0, len(tc.want))
					// Aggregate the explicitly expected fixture rows, independently of the export scanner.
					groups := map[[4]string][4]int{}
					zone := time.FixedZone("Asia/Shanghai", 8*3600)
					for _, name := range tc.want {
						row := fixture[name]
						wantIDs = append(wantIDs, row.RequestId)
						key := [4]string{time.Unix(row.CreatedAt, 0).In(zone).Format("2006-01-02"), row.ModelName, row.TokenName, row.Group}
						metrics := groups[key]
						metrics[0]++
						metrics[1] += row.PromptTokens
						metrics[2] += row.CompletionTokens
						metrics[3] += row.Quota
						groups[key] = metrics
					}
					if mode == "builtin" && tc.customers == nil && !tc.all {
						listCompatible := true
						for key := range tc.filters {
							if !slices.Contains([]string{"model_name", "token_name", "group"}, key) {
								listCompatible = false
							}
						}
						if listCompatible {
							modelName, _ := tc.filters["model_name"].(string)
							tokenName, _ := tc.filters["token_name"].(string)
							group, _ := tc.filters["group"].(string)
							logs, total, err := model.GetEmployeeCustomerLogsWithContext(context.Background(), model.EmployeeCustomerLogFilter{
								EmployeeUserId: employee, CustomerUserId: customer, LogType: tc.logType,
								StartTimestamp: start, EndTimestamp: end, PageSize: 100,
								ModelName: modelName, TokenName: tokenName, Group: group,
							})
							require.NoError(t, err)
							require.EqualValues(t, len(wantIDs), total)
							var listIDs []string
							for _, row := range logs {
								listIDs = append(listIDs, row.RequestId)
							}
							require.ElementsMatch(t, wantIDs, listIDs, "list and export must share text/type/time semantics")
						}
						if tc.filters == nil {
							for _, field := range []string{"model_name", "token_name", "group"} {
								query := url.Values{
									"field": {field}, "template_id": {templateID}, "type": {strconv.Itoa(tc.logType)},
									"customer_ids": {strconv.Itoa(customer)}, "start_timestamp": {strconv.FormatInt(start, 10)}, "end_timestamp": {strconv.FormatInt(end, 10)},
								}
								ctx, rec = newCtx(t, http.MethodGet, "/api/user/employee/export/options?"+query.Encode(), nil)
								GetLogExportFilterOptions(asUser(ctx, employee))
								response = decodeResp(t, rec)
								require.True(t, response.Success, rec.Body.String())
								var options struct{ Items []model.LogExportFilterOption }
								require.NoError(t, common.Unmarshal(response.Data, &options))
								wantValues := []string{}
								for _, name := range tc.want {
									row := fixture[name]
									value := map[string]string{"model_name": row.ModelName, "token_name": row.TokenName, "group": row.Group}[field]
									if !slices.Contains(wantValues, value) {
										wantValues = append(wantValues, value)
									}
								}
								var actualValues []string
								for _, option := range options.Items {
									actualValues = append(actualValues, option.Value)
								}
								require.ElementsMatch(t, wantValues, actualValues, "candidate field %s", field)
							}
						}
					}
					var actualIDs []string
					for _, part := range job.Parts {
						token, err := model.CreateManagedLogExportDownloadToken(job.JobID, employee, part.Index, "employee")
						require.NoError(t, err)
						ctx, rec = newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
						ctx.Params = gin.Params{{Key: "token", Value: token}}
						DownloadLogExport(ctx)
						require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
						compressed, err := gzip.NewReader(rec.Body)
						require.NoError(t, err)
						records, err := csv.NewReader(compressed).ReadAll()
						require.NoError(t, err)
						require.NoError(t, compressed.Close())
						require.NotEmpty(t, records, "empty results still include the header")
						if part.Kind == model.LogExportPartKindSummary {
							require.Len(t, records[1:], len(groups))
							for _, record := range records[1:] {
								require.Len(t, record, 10)
								key := [4]string{record[0], record[1], record[2], record[3]}
								metrics, ok := groups[key]
								require.True(t, ok, "unexpected summary group: %v", key)
								require.Equal(t, []string{strconv.Itoa(metrics[0]), strconv.Itoa(metrics[1]), strconv.Itoa(metrics[2]), strconv.Itoa(metrics[1] + metrics[2]), strconv.Itoa(metrics[3]), strconv.FormatFloat(float64(metrics[3])/500000, 'f', 6, 64)}, record[4:])
								delete(groups, key)
							}
							require.Empty(t, groups)
							continue
						}
						idColumn := 0
						if mode == "builtin" {
							idColumn = slices.Index(model.EmployeeBuiltinExportTemplates()[0].Columns, "request_id")
							require.NotEqual(t, -1, idColumn)
						}
						for _, record := range records[1:] {
							actualIDs = append(actualIDs, record[idColumn])
						}
					}
					if mode != model.LogExportModeSummary {
						require.ElementsMatch(t, wantIDs, actualIDs)
					}
					if mode == model.LogExportModeSummary || mode == model.LogExportModeBoth {
						require.Empty(t, groups, "summary part must exist even for zero rows")
					}
				})
			}
		})
	}
}
