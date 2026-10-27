package controller

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"net/http"
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
