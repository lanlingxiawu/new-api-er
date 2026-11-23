package controller

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestSelfLogExportConfiguredRangeThroughDownload(t *testing.T) {
	requireDB(t)
	requireLogDB(t)
	enableRedis(t)
	exportDir := t.TempDir()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, exportDir)
	}
	logSetting := operation_setting.GetLogSetting()
	previousLog := *logSetting
	t.Cleanup(func() { *logSetting = previousLog })
	logSetting.UserExportEnabled = true
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		s.UserMaxRangeSec = 30 * 86400 // The deprecated value must not truncate the actual export.
		s.AdminMaxRangeSec = 86400
		s.BatchSleepMs = 0
		s.MaxRowsPerSec = 100000
		s.CPUSoftLimit, s.CPUHardLimit = 99, 100
		s.OffpeakOnly = false
		s.BatchSize = 2
		s.WindowSec = 86400
		s.RowsPerFile, s.MaxParts = 3, 100
		s.MinFreeDiskMB = 1
		s.TimeoutSec = 30
	})
	user := mkUser(t, nil)
	t.Cleanup(func() {
		model.ClearLogExportCooldown(user.Id)
		require.NoError(t, model.LOG_DB.Where("user_id = ? AND action = ?", user.Id, "log_export.job_create").Delete(&model.AuditLog{}).Error)
	})
	const day = int64(86400)
	end := time.Now().Unix() - 3600
	start := end - 62*day
	stamps := []int64{start, start + day - 1, start + day, start + 30*day, start + 31*day, start + 46*day, end - 7*day, end - 7*day + 1, end}
	var fixtures []*model.Log
	for i, stamp := range stamps {
		fixtures = append(fixtures, mkCtrlExportLog(t, func(l *model.Log) {
			l.UserId, l.Username, l.CreatedAt = user.Id, user.Username, stamp
			l.ModelName, l.TokenName, l.Group = "export-range-model", "export-range-token", "export-range-group"
			l.Content = fmt.Sprintf("included-%d", i)
		}))
	}
	for _, mismatch := range []string{"before", "after", "user", "type", "model", "token", "group"} {
		mkCtrlExportLog(t, func(l *model.Log) {
			*l = *fixtures[len(fixtures)-1]
			l.Id, l.Content = 0, "excluded-"+mismatch
			switch mismatch {
			case "before":
				l.CreatedAt = start - 1
			case "after":
				l.CreatedAt = end + 1
			case "user":
				l.UserId = nextTestID()
			case "type":
				l.Type = model.LogTypeError
			case "model":
				l.ModelName = "other-model"
			case "token":
				l.TokenName = "other-token"
			case "group":
				l.Group = "other-group"
			}
		})
	}
	for _, days := range []int{62, 7} {
		t.Run(fmt.Sprintf("%d_days", days), func(t *testing.T) {
			logSetting.MaxRangeDays = days
			model.ClearLogExportCooldown(user.Id)
			filters := map[string]any{
				"start_timestamp": end - int64(days)*day, "end_timestamp": end + 1,
				"type": model.LogTypeConsume, "model_name": "export-range-model",
				"token_name": "export-range-token", "group": "export-range-group",
			}
			body := map[string]any{"filters": filters, "options": map[string]any{"timezone": "UTC", "header": true, "csv_bom": false}}
			c, rec := newCtx(t, http.MethodPost, "/api/log/self/export/jobs", body)
			CreateSelfLogExportJob(asUser(c, user.Id))
			response := decodeResp(t, rec)
			require.False(t, response.Success)
			require.Equal(t, i18n.T(c, i18n.MsgLogExportRangeTooLong, map[string]any{"Days": int64(days)}), response.Message)

			filters["end_timestamp"] = end
			c, rec = newCtx(t, http.MethodPost, "/api/log/self/export/jobs", body)
			CreateSelfLogExportJob(asUser(c, user.Id))
			response = decodeResp(t, rec)
			require.True(t, response.Success, response.Message)
			var created struct {
				JobID string `json:"job_id"`
			}
			require.NoError(t, common.Unmarshal(response.Data, &created))
			require.NotEmpty(t, created.JobID)
			cleanupExportJob(t, created.JobID)
			// Wait on the serialized state, never on the worker's mutable job pointer.
			var job *model.LogExportJob
			require.Eventually(t, func() bool {
				var err error
				job, err = model.GetLogExportJob(created.JobID)
				return err == nil && job != nil && job.IsTerminal()
			}, 45*time.Second, 50*time.Millisecond)
			require.Equal(t, model.LogExportStatusReady, job.Status, job.Error)
			require.True(t, job.IsSelfExportFor(user.Id))
			require.Equal(t, end-int64(days)*day, job.Filters.StartTimestamp)
			require.Equal(t, end, job.Filters.EndTimestamp)

			var want []string
			for _, row := range fixtures {
				if row.CreatedAt >= job.Filters.StartTimestamp {
					want = append(want, row.Content)
				}
			}
			require.Equal(t, int64(len(want)), job.RowCount)
			require.Equal(t, int64(len(want)), job.ScannedRows)
			require.Len(t, job.Parts, (len(want)+2)/3)
			contentIndex := slices.Index(job.Columns, "content")
			require.NotEqual(t, -1, contentIndex)
			var got []string
			for _, part := range job.Parts {
				c, rec = newCtx(t, http.MethodGet, fmt.Sprintf("/api/log/self/export/jobs/%s/download?part=%d", job.JobID, part.Index), nil)
				c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
				GetLogExportDownloadURL(asUser(c, user.Id))
				response = decodeResp(t, rec)
				require.True(t, response.Success, response.Message)
				var link struct {
					URL string `json:"url"`
				}
				require.NoError(t, common.Unmarshal(response.Data, &link))
				c, rec = newCtx(t, http.MethodGet, link.URL, nil)
				c.Params = gin.Params{{Key: "token", Value: strings.TrimPrefix(link.URL, "/dl/log-export/")}}
				DownloadLogExport(c)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				reader, err := gzip.NewReader(rec.Body)
				require.NoError(t, err)
				rows, err := csv.NewReader(reader).ReadAll()
				require.NoError(t, reader.Close())
				require.NoError(t, err)
				require.Len(t, rows, int(part.Rows)+1)
				for _, row := range rows[1:] {
					got = append(got, row[contentIndex])
				}
			}
			require.Equal(t, want, got, "downloaded rows must preserve the entire requested range and filters without duplicates")
			t.Logf("%d-day limit: downloaded %d matching rows in %d gzip parts; over-limit request rejected", days, len(got), len(job.Parts))
		})
	}
}

func TestSelfLogExportUsesLogMaintenanceRange(t *testing.T) {
	server := miniredis.RunT(t)
	previous, enabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	logSetting := operation_setting.GetLogSetting()
	exportSetting := operation_setting.GetLogExportSetting()
	previousLog, previousExport := *logSetting, *exportSetting
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RDB, common.RedisEnabled = previous, enabled
		*logSetting, *exportSetting = previousLog, previousExport
	})
	logSetting.UserExportEnabled = true
	exportSetting.Enabled = true
	exportSetting.UserMaxRangeSec = 30 * 86400 // Legacy setting must not override the log-maintenance page.
	exportSetting.MaxActiveJobsPerUser = 1
	// An existing task stops accepted requests before disk access or worker startup.
	require.NoError(t, model.CreateLogExportJob(&model.LogExportJob{
		JobID: model.NewLogExportJobID(), UserID: 42, SelfExport: true,
		Filters: model.LogExportFilter{UserId: 42},
	}))
	for _, days := range []int{62, 7} {
		logSetting.MaxRangeDays = days
		c, rec := newCtx(t, http.MethodGet, "/api/log/self/export/jobs", nil)
		c.Set("id", 42)
		c.Set("role", common.RoleCommonUser)
		GetLogExportJobs(c)
		var list struct {
			Success bool `json:"success"`
			Data    struct {
				MaxRangeSec int64 `json:"max_range_sec"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &list))
		require.True(t, list.Success)
		require.Equal(t, int64(days*86400), list.Data.MaxRangeSec)
		for _, extra := range []int64{-1, 0, 1} {
			c, rec := newCtx(t, http.MethodPost, "/api/log/self/export/jobs", map[string]any{
				"filters": map[string]any{"start_timestamp": 1700000000, "end_timestamp": 1700000000 + int64(days*86400) + extra},
			})
			c.Set("id", 42)
			c.Set("role", common.RoleCommonUser)
			CreateSelfLogExportJob(c)
			var response struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
			require.False(t, response.Success)
			if extra > 0 {
				require.Equal(t, i18n.T(c, i18n.MsgLogExportRangeTooLong, map[string]any{"Days": int64(days)}), response.Message)
			} else {
				require.Equal(t, i18n.T(c, i18n.MsgLogExportTooManyJobs, map[string]any{"Max": 1}), response.Message)
			}
		}
	}
}

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
