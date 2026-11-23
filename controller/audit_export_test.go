package controller

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditexport"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuditExportRequestContract(t *testing.T) {
	for _, tc := range []struct {
		body        string
		self, valid bool
	}{
		{`{"filters":{"start_timestamp":1,"end_timestamp":3456001,"success":false},"options":{"csv_bom":true}}`, false, true},
		{`{"filters":{"start_timestamp":1,"end_timestamp":2,"success":"false"}}`, false, false},
		{`{"filters":{"start_timestamp":1,"end_timestamp":2,"username":"other"}}`, true, false},
		{`{"filters":{"start_timestamp":1,"end_timestamp":2,"ip_risk":"high"}}`, true, false},
		{`{"filters":{"start_timestamp":1,"end_timestamp":2},"options":{"bom":true}}`, false, false},
		{`{"data_source":"usage","filters":{"start_timestamp":1,"end_timestamp":2}}`, false, false},
		{`{"filters":{"start_timestamp":1,"end_timestamp":2,"cursor":"x"}}`, false, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/api/audit/export/jobs", strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", 42)
		filter, opts, err := parseAuditExportRequest(c, tc.self)
		if !tc.valid {
			require.Error(t, err, tc.body)
			continue
		}
		require.NoError(t, err)
		require.NotNil(t, filter.Success)
		require.False(t, *filter.Success)
		require.True(t, opts.CSVBOM)
	}
}

func TestAuditExportDowngradeRejectsExistingDownload(t *testing.T) {
	requireDB(t)
	exportManagementFixture(t)
	require.NoError(t, model.InitAuditExportPools(t.Context()))
	t.Cleanup(model.CloseAuditExportPools)
	user := mkUser(t, func(u *model.User) { u.Role = common.RoleRootUser })
	job := mkReadyExportJob(t, user.Id, "higher-role-audit-data")
	job.DataSource, job.SelfExport = model.LogExportDataAudit, true
	job.CreatorRole, job.AuditProjectionVersion = common.RoleRootUser, model.AuditActivityProjectionVersion
	job.Filters.UserId = user.Id
	job.AuditFilters = &model.AuditExportFilter{AuditLogFilter: model.AuditLogFilter{UserId: user.Id, SelfView: true}}
	model.UpdateLogExportJob(job)
	tokens := []string{}
	for range 2 {
		token, err := model.CreateManagedLogExportDownloadToken(job.JobID, user.Id, 1, "audit_self")
		require.NoError(t, err)
		tokens = append(tokens, token)
	}
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleCommonUser).Error)
	for i, token := range tokens {
		c, rec := newCtx(t, "GET", "/dl/log-export/"+token, nil)
		c.Params = gin.Params{{Key: "token", Value: token}}
		if i == 1 {
			c.Request.Header.Set("Range", "bytes=0-5")
		}
		DownloadLogExport(c)
		require.Equal(t, http.StatusForbidden, rec.Code)
		var response struct{ Success bool }
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
		require.False(t, response.Success)
		require.NotContains(t, rec.Body.String(), "higher-role-audit-data")
	}
}

func TestAuditExportConfiguredRangeAndDownload(t *testing.T) {
	requireDB(t)
	requireLogDB(t)
	enableRedis(t)
	require.NoError(t, model.DB.AutoMigrate(&model.CasbinRule{}))
	require.NoError(t, model.InitAuditExportPools(t.Context()))
	t.Cleanup(model.CloseAuditExportPools)
	dir := t.TempDir()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, dir)
	}
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled, s.OffpeakOnly = true, false
		s.AdminMaxRangeSec, s.AdminCooldownSec = 90*86400, 1
		s.BatchSize, s.WindowSec, s.RowsPerFile, s.MaxParts = 2, 86400, 2, 100
		s.BatchSleepMs, s.MaxRowsPerSec, s.MinFreeDiskMB, s.TimeoutSec = 0, 1000000, 1, 60
		s.CPUSoftLimit, s.CPUHardLimit = 99, 100
	})
	user := mkUser(t, func(u *model.User) { u.Role = common.RoleRootUser })
	end := time.Now().Unix() - 3600
	start := end - 40*86400
	rows := []model.AuditLog{}
	for i, at := range []int64{start, start + 86400 - 1, start + 86400, end, end} {
		rows = append(rows, model.AuditLog{EventId: fmt.Sprintf("%s-%d", user.Username, i), Username: user.Username, UserId: user.Id, ActorRole: common.RoleRootUser, Category: "operation", CreatedAt: at, Content: "=test", Other: model.AuditOther{RootInfo: model.AuditFields{"large": int64(9007199254740993)}}})
	}
	require.NoError(t, model.LOG_DB.Create(&rows).Error)
	t.Cleanup(func() {
		model.ClearLogExportCooldown(user.Id)
		model.LOG_DB.Where("user_id = ?", user.Id).Delete(&model.AuditLog{})
	})
	access, err := auditexport.CurrentAccess(context.Background(), user.Id, "audit_admin")
	require.NoError(t, err)
	c, rec := newCtx(t, "POST", "/api/audit/export/jobs", map[string]any{"filters": map[string]any{"start_timestamp": start, "end_timestamp": end, "username": user.Username}, "options": map[string]any{"header": true, "csv_bom": false, "timezone": "UTC"}})
	asRoot(c, user.Id)
	c.Set("log_export_access", access)
	CreateAuditLogExportJob(c)
	response := decodeResp(t, rec)
	require.True(t, response.Success, response.Message)
	var created struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &created))
	cleanupExportJob(t, created.JobID)
	var job *model.LogExportJob
	require.Eventually(t, func() bool {
		job, err = model.GetLogExportJob(created.JobID)
		return err == nil && job != nil && job.IsTerminal()
	}, 70*time.Second, 50*time.Millisecond)
	require.Equal(t, model.LogExportStatusReady, job.Status, job.Error)
	require.EqualValues(t, 5, job.RowCount)
	require.EqualValues(t, 5, job.ScannedRows)
	require.Len(t, job.Parts, 3)
	var events []string
	for _, part := range job.Parts {
		require.LessOrEqual(t, part.StartTime, part.EndTime)
		token, err := model.CreateManagedLogExportDownloadToken(job.JobID, user.Id, part.Index, "audit_admin")
		require.NoError(t, err)
		c, rec = newCtx(t, "GET", "/dl/log-export/"+token, nil)
		c.Params = gin.Params{{Key: "token", Value: token}}
		DownloadLogExport(c)
		require.Equal(t, http.StatusOK, rec.Code)
		gz, err := gzip.NewReader(rec.Body)
		require.NoError(t, err)
		csvRows, err := csv.NewReader(gz).ReadAll()
		require.NoError(t, err)
		require.NoError(t, gz.Close())
		require.Equal(t, "Event ID", csvRows[0][0])
		for _, row := range csvRows[1:] {
			events = append(events, row[0])
			require.Equal(t, "'=test", row[17])
			require.Contains(t, row[18], "9007199254740993")
		}
		c, rec = newCtx(t, "GET", "/dl/log-export/"+token, nil)
		c.Params = gin.Params{{Key: "token", Value: token}}
		c.Request.Header.Set("Range", "bytes=0-9")
		DownloadLogExport(c)
		require.Equal(t, http.StatusPartialContent, rec.Code)
		require.Len(t, rec.Body.Bytes(), 10)
	}
	require.Equal(t, []string{rows[4].EventId, rows[3].EventId, rows[2].EventId, rows[1].EventId, rows[0].EventId}, events)
	// Usage management cannot enumerate or operate on the audit job.
	c, rec = newCtx(t, "GET", "/api/log/export/jobs/"+job.JobID, nil)
	asRoot(c, user.Id)
	c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
	GetLogExportJob(c)
	require.Equal(t, http.StatusForbidden, rec.Code)
	legacy, err := model.CreateManagedLogExportDownloadToken(job.JobID, user.Id, 1, "admin")
	require.NoError(t, err)
	c, rec = newCtx(t, "GET", "/dl/log-export/"+legacy, nil)
	c.Params = gin.Params{{Key: "token", Value: legacy}}
	DownloadLogExport(c)
	require.Equal(t, http.StatusForbidden, rec.Code)
	token, err := model.CreateManagedLogExportDownloadToken(job.JobID, user.Id, 1, "audit_admin")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleAdminUser).Error)
	c, rec = newCtx(t, "GET", "/dl/log-export/"+token, nil)
	c.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(c)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAuditExportScopeRequiresDashboardSession(t *testing.T) {
	for _, self := range []bool{true, false} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(method, "/api/audit/export/jobs", nil)
			c.Set("id", 42)
			c.Set("role", 100)
			c.Set("use_access_token", true)
			AuditExportScope(self)(c)
			require.Equal(t, 403, response.Code)
			require.True(t, c.IsAborted())
		}
	}
}
