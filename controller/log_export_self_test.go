package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestSelfLogExportTaskRoutesEnforceScope(t *testing.T) {
	server := miniredis.RunT(t)
	previous, enabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = previous, enabled })
	for _, self := range []bool{false, true} {
		job := &model.LogExportJob{JobID: model.NewLogExportJobID(), UserID: 42, SelfExport: self, Filters: model.LogExportFilter{UserId: 42}}
		require.NoError(t, model.CreateLogExportJob(job))
		for _, viewer := range []int{42, 43} {
			for _, handler := range []gin.HandlerFunc{GetLogExportJob, DeleteLogExportJob, GetLogExportDownloadURL} {
				c, rec := newCtx(t, http.MethodGet, "/api/log/self/export/jobs/"+job.JobID, nil)
				c.Set("id", viewer)
				c.Set("role", common.RoleRootUser) // Even root's self route must not widen scope.
				c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
				handler(c)
				if !self || viewer != 42 {
					require.Equal(t, http.StatusForbidden, rec.Code)
				}
			}
		}
	}
	c, rec := newCtx(t, http.MethodGet, "/api/log/self/export/jobs?all=true", nil)
	c.Set("id", 42)
	c.Set("role", common.RoleRootUser)
	GetLogExportJobs(c)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Jobs []model.LogExportJob `json:"jobs"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Jobs, 1)
	require.True(t, response.Data.Jobs[0].IsSelfExportFor(42))
}

func TestSelfLogExportRejectsPrivilegedInput(t *testing.T) {
	for _, key := range []string{"user_id", "username", "channel", "columns", "template_id", "format", "mode", "summary_dims", "employee_scope"} {
		t.Run(key, func(t *testing.T) {
			c, _ := newCtx(t, http.MethodPost, "/api/log/self/export/jobs", map[string]any{"filters": map[string]any{"start_timestamp": 1, "end_timestamp": 2, key: nil}})
			_, err := parseSelfLogExportRequest(c)
			require.Error(t, err)
		})
	}
	c, _ := newCtx(t, http.MethodPost, "/api/log/self/export/jobs", map[string]any{"filters": map[string]any{"start_timestamp": 1, "end_timestamp": 2, "model_name": "gpt-test"}})
	req, err := parseSelfLogExportRequest(c)
	require.NoError(t, err)
	require.Equal(t, "gpt-test", req.ModelName)
	require.True(t, req.Options.Header)
	require.True(t, req.Options.CSVBOM)
}

func TestSelfLogExportCannotAccessPrivilegedHistory(t *testing.T) {
	job := &model.LogExportJob{UserID: 42, SelfExport: true, Filters: model.LogExportFilter{UserId: 42}}
	require.True(t, job.IsSelfExportFor(42))
	require.False(t, job.IsSelfExportFor(43))
	job.SelfExport = false
	require.False(t, job.IsSelfExportFor(42))
	job.SelfExport = true
	job.Filters.UserId = 0
	require.False(t, job.IsSelfExportFor(42))
	job.Filters.UserId = 42
	job.EmployeeScope = &model.EmployeeExportScope{}
	require.False(t, job.IsSelfExportFor(42))
}

func TestLogExportNormalizesLegacyXlsxTemplate(t *testing.T) {
	c, _ := newCtx(t, http.MethodPost, "/api/log/export/jobs", nil)
	c.Set("role", common.RoleRootUser)
	_, format, _, err := resolveLogExportRequestShape(c, createLogExportJobRequest{Format: model.LogExportFormatXlsx})
	require.NoError(t, err)
	require.Equal(t, model.LogExportFormatCSVGz, format)
}

func TestSelfLogExportDownloadChecksCurrentAccount(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	for _, enabled := range []bool{true, false} {
		user := mkUser(t, func(u *model.User) {
			if !enabled {
				u.Status = common.UserStatusDisabled
			}
		})
		job := mkReadyExportJob(t, user.Id, "compressed-file-fixture")
		job.SelfExport = true
		job.Filters.UserId = user.Id
		model.UpdateLogExportJob(job)
		token, err := model.CreateLogExportDownloadToken(job.JobID, user.Id, 1)
		require.NoError(t, err)
		c, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
		c.Params = gin.Params{{Key: "token", Value: token}}
		DownloadLogExport(c)
		if enabled {
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, "compressed-file-fixture", rec.Body.String())
		} else {
			require.Equal(t, http.StatusForbidden, rec.Code)
		}
	}
}
