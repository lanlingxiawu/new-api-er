package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withExportSetting 临时改导出配置并还原。
func withExportSetting(t *testing.T, mut func(s *operation_setting.LogExportSetting)) {
	t.Helper()
	s := operation_setting.GetLogExportSetting()
	prev := *s
	mut(s)
	t.Cleanup(func() { *operation_setting.GetLogExportSetting() = prev })
}

// validExportJobBody 返回一个通过所有校验的建任务请求体。
func validExportJobBody() map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"start_timestamp": now - 3600,
		"end_timestamp":   now,
		"columns":         []string{"created_at", "quota"},
		"format":          "csv_gz",
	}
}

// cleanupExportJob 结束后取消并删除任务，避免残留 goroutine 与文件。
func cleanupExportJob(t *testing.T, jobID string) {
	t.Helper()
	t.Cleanup(func() {
		model.CancelLogExportJob(jobID)
		if job, err := model.GetLogExportJob(jobID); err == nil && job != nil {
			model.RemoveLogExportJobFiles(job)
			model.DeleteLogExportJob(job)
		}
		model.ReleaseLogExportSlot(jobID)
	})
}

func TestGetLogExportColumns_ReturnsCatalogAndDefaultTemplate(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/columns", nil)
	GetLogExportColumns(asAdmin(ctx, 1))

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var data struct {
		Columns []struct {
			Key       string `json:"key"`
			Label     string `json:"label"`
			Group     string `json:"group"`
			AdminOnly bool   `json:"admin_only"`
		} `json:"columns"`
		BuiltinTemplates []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Columns   []string `json:"columns"`
			IsDefault bool     `json:"is_default"`
		} `json:"builtin_templates"`
		DefaultTemplate string `json:"default_template"`
		MaxColumns      int    `json:"max_columns"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &data))

	assert.NotEmpty(t, data.Columns)
	assert.Equal(t, model.LogExportTemplateAsDisplayed, data.DefaultTemplate)
	assert.Equal(t, model.LogExportMaxColumns, data.MaxColumns)

	// 默认模板必须排在首位且标记为默认。
	require.NotEmpty(t, data.BuiltinTemplates)
	assert.Equal(t, model.LogExportTemplateAsDisplayed, data.BuiltinTemplates[0].ID)
	assert.True(t, data.BuiltinTemplates[0].IsDefault)

	// 列目录里必须能看到管理员专属标记，前端据此提示权限差异。
	hasAdminOnly := false
	for _, col := range data.Columns {
		assert.NotEmpty(t, col.Label)
		assert.NotEmpty(t, col.Group)
		if col.AdminOnly {
			hasAdminOnly = true
		}
	}
	assert.True(t, hasAdminOnly)
}

func TestCreateLogExportJob_RejectsWhenDisabled(t *testing.T) {
	withExportSetting(t, func(s *operation_setting.LogExportSetting) { s.Enabled = false })
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", validExportJobBody())
	CreateLogExportJob(asAdmin(ctx, 1))

	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.Message)
}

// Redis 未启用时后台任务不可用，必须给出可操作的提示而不是 500。
func TestCreateLogExportJob_RequiresRedis(t *testing.T) {
	withExportSetting(t, func(s *operation_setting.LogExportSetting) { s.Enabled = true })
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })

	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", validExportJobBody())
	CreateLogExportJob(asAdmin(ctx, 1))

	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.Message)
}

func TestCreateLogExportJob_ValidatesTimeRange(t *testing.T) {
	enableRedis(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		s.AdminMaxRangeSec = 86400
	})
	now := time.Now().Unix()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing_range", map[string]any{"columns": []string{"quota"}}},
		{"missing_end", map[string]any{"start_timestamp": now - 10, "columns": []string{"quota"}}},
		{"reversed", map[string]any{"start_timestamp": now, "end_timestamp": now - 100, "columns": []string{"quota"}}},
		{"too_long", map[string]any{"start_timestamp": now - 200000, "end_timestamp": now, "columns": []string{"quota"}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", tc.body)
			CreateLogExportJob(asAdmin(ctx, nextTestID()))
			resp := decodeResp(t, rec)
			assert.False(t, resp.Success)
			assert.NotEmpty(t, resp.Message)
		})
	}
}

func TestCreateLogExportJob_RejectsUnknownColumnAndTemplate(t *testing.T) {
	enableRedis(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) { s.Enabled = true })

	body := validExportJobBody()
	body["columns"] = []string{"created_at", "made_up_column"}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", body)
	CreateLogExportJob(asAdmin(ctx, nextTestID()))
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "made_up_column")

	tplBody := validExportJobBody()
	delete(tplBody, "columns")
	tplBody["template_id"] = "builtin:does_not_exist"
	ctx, rec = newCtx(t, http.MethodPost, "/api/log/export/jobs", tplBody)
	CreateLogExportJob(asAdmin(ctx, nextTestID()))
	resp = decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestCreateLogExportJob_CooldownAndSlotAreEnforced(t *testing.T) {
	enableRedis(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		s.MaxConcurrentJobs = 1
		s.UserCooldownSec = 300
		s.AdminMaxRangeSec = 86400
	})
	userA := nextTestID()
	userB := nextTestID()
	t.Cleanup(func() {
		model.ClearLogExportCooldown(userA)
		model.ClearLogExportCooldown(userB)
	})

	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", validExportJobBody())
	CreateLogExportJob(asAdmin(ctx, userA))
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var created struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &created))
	require.NotEmpty(t, created.JobID)
	cleanupExportJob(t, created.JobID)

	// 并发槽只有 1 个，另一个用户此刻应被挡住（且不消耗他的冷却）。
	ctx, rec = newCtx(t, http.MethodPost, "/api/log/export/jobs", validExportJobBody())
	CreateLogExportJob(asAdmin(ctx, userB))
	resp = decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.False(t, model.CheckAndSetLogExportCooldown(userB),
		"a rejected request must not burn the user's cooldown")
}

func TestCreateLogExportJob_DowngradesXlsxWhenEstimateTooLarge(t *testing.T) {
	enableRedis(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		s.MaxConcurrentJobs = 5
		s.UserCooldownSec = 1
		s.XlsxMaxRows = 1000
		s.AdminMaxRangeSec = 86400
	})
	user := nextTestID()
	t.Cleanup(func() { model.ClearLogExportCooldown(user) })

	body := validExportJobBody()
	body["format"] = "xlsx"
	body["est_rows"] = 50000

	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", body)
	CreateLogExportJob(asAdmin(ctx, user))
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var created struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &created))
	cleanupExportJob(t, created.JobID)

	job, err := model.GetLogExportJob(created.JobID)
	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Equal(t, model.LogExportFormatCSVGz, job.Format,
		"an estimate above the xlsx limit must fall back to csv.gz up front")
}

func TestGetLogExportJob_OwnershipEnforced(t *testing.T) {
	enableRedis(t)
	owner := nextTestID()
	stranger := nextTestID()

	job := &model.LogExportJob{
		JobID: model.NewLogExportJobID(), UserID: owner,
		Format: model.LogExportFormatCSVGz, Columns: []string{"created_at"},
	}
	require.NoError(t, model.CreateLogExportJob(job))
	t.Cleanup(func() { model.DeleteLogExportJob(job) })

	// 属主可读
	ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/jobs/"+job.JobID, nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
	GetLogExportJob(asAdmin(ctx, owner))
	assert.True(t, decodeResp(t, rec).Success)

	// 非属主的管理员被拒
	ctx, rec = newCtx(t, http.MethodGet, "/api/log/export/jobs/"+job.JobID, nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
	GetLogExportJob(asAdmin(ctx, stranger))
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Root 可以看所有人的任务
	ctx, rec = newCtx(t, http.MethodGet, "/api/log/export/jobs/"+job.JobID, nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
	GetLogExportJob(asRoot(ctx, stranger))
	assert.True(t, decodeResp(t, rec).Success)
}

func TestGetLogExportJob_NotFound(t *testing.T) {
	enableRedis(t)
	ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/jobs/none", nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: "none"}}
	GetLogExportJob(asAdmin(ctx, 1))
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestDeleteLogExportJob_CancelsRunningAndDeletesFinished(t *testing.T) {
	enableRedis(t)
	user := nextTestID()

	running := &model.LogExportJob{JobID: model.NewLogExportJobID(), UserID: user, Status: model.LogExportStatusRunning}
	require.NoError(t, model.CreateLogExportJob(running))
	running.Status = model.LogExportStatusRunning
	model.UpdateLogExportJob(running)
	t.Cleanup(func() { model.DeleteLogExportJob(running) })

	ctx, rec := newCtx(t, http.MethodDelete, "/api/log/export/jobs/"+running.JobID, nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: running.JobID}}
	DeleteLogExportJob(asAdmin(ctx, user))
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)
	assert.Contains(t, string(resp.Data), "canceled")

	// 已完成的任务：连同文件一起删除。
	finished := &model.LogExportJob{JobID: model.NewLogExportJobID(), UserID: user}
	require.NoError(t, model.CreateLogExportJob(finished))
	partPath := model.LogExportPartFilePath(finished.JobID, 1, model.LogExportFormatCSVGz)
	require.NoError(t, os.WriteFile(partPath, []byte("data"), 0o600))
	finished.Status = model.LogExportStatusReady
	finished.Parts = []model.LogExportPart{{Index: 1, Path: partPath, Rows: 1, Bytes: 4}}
	model.UpdateLogExportJob(finished)
	t.Cleanup(func() { _ = os.Remove(partPath) })

	ctx, rec = newCtx(t, http.MethodDelete, "/api/log/export/jobs/"+finished.JobID, nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: finished.JobID}}
	DeleteLogExportJob(asAdmin(ctx, user))
	require.True(t, decodeResp(t, rec).Success)

	_, err := os.Stat(partPath)
	assert.True(t, os.IsNotExist(err), "part file must be removed with the job")
	gone, err := model.GetLogExportJob(finished.JobID)
	require.NoError(t, err)
	assert.Nil(t, gone)
}

// 建一个 ready 状态的任务 + 一个真实分片文件，供下载相关用例复用。
func mkReadyExportJob(t *testing.T, userID int, content string) *model.LogExportJob {
	t.Helper()
	job := &model.LogExportJob{
		JobID: model.NewLogExportJobID(), UserID: userID,
		Format: model.LogExportFormatCSVGz, Columns: []string{"created_at"},
	}
	require.NoError(t, model.CreateLogExportJob(job))
	partPath := model.LogExportPartFilePath(job.JobID, 1, model.LogExportFormatCSVGz)
	require.NoError(t, os.WriteFile(partPath, []byte(content), 0o600))
	job.Status = model.LogExportStatusReady
	job.RowCount = 1
	job.Parts = []model.LogExportPart{{Index: 1, Path: partPath, Rows: 1, Bytes: int64(len(content))}}
	model.UpdateLogExportJob(job)
	t.Cleanup(func() {
		_ = os.Remove(partPath)
		model.DeleteLogExportJob(job)
	})
	return job
}

func TestGetLogExportDownloadURL(t *testing.T) {
	enableRedis(t)
	user := nextTestID()
	job := mkReadyExportJob(t, user, "hello")

	ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/jobs/"+job.JobID+"/download-url?part=1", nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
	GetLogExportDownloadURL(asAdmin(ctx, user))

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var data struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	assert.Contains(t, data.URL, "/dl/log-export/")
	assert.Greater(t, data.ExpiresIn, 0)
}

func TestGetLogExportDownloadURL_RejectsUnfinishedAndMissingPart(t *testing.T) {
	enableRedis(t)
	user := nextTestID()

	pending := &model.LogExportJob{JobID: model.NewLogExportJobID(), UserID: user}
	require.NoError(t, model.CreateLogExportJob(pending))
	t.Cleanup(func() { model.DeleteLogExportJob(pending) })

	ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/jobs/"+pending.JobID+"/download-url", nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: pending.JobID}}
	GetLogExportDownloadURL(asAdmin(ctx, user))
	assert.False(t, decodeResp(t, rec).Success, "cannot download a job that is not ready")

	ready := mkReadyExportJob(t, user, "hello")
	ctx, rec = newCtx(t, http.MethodGet, "/api/log/export/jobs/"+ready.JobID+"/download-url?part=99", nil)
	ctx.Params = gin.Params{{Key: "job_id", Value: ready.JobID}}
	GetLogExportDownloadURL(asAdmin(ctx, user))
	assert.False(t, decodeResp(t, rec).Success, "unknown part must be rejected")
}

// 下载端点不经过鉴权中间件，凭证是 URL 里的令牌。
func TestDownloadLogExport_TokenFlowAndRange(t *testing.T) {
	enableRedis(t)
	requireDB(t)

	admin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	content := "0123456789"
	job := mkReadyExportJob(t, admin.Id, content)

	newDownloadCtx := func(token, rangeHeader string) (*gin.Context, *httptest.ResponseRecorder) {
		ctx, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
		ctx.Params = gin.Params{{Key: "token", Value: token}}
		if rangeHeader != "" {
			ctx.Request.Header.Set("Range", rangeHeader)
		}
		return ctx, rec
	}

	token, err := model.CreateLogExportDownloadToken(job.JobID, admin.Id, 1)
	require.NoError(t, err)

	// 完整下载
	ctx, rec := newDownloadCtx(token, "")
	DownloadLogExport(ctx)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, content, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")

	// 断点续传：同一令牌在续传窗口内仍然可用，返回 206 + Content-Range。
	ctx, rec = newDownloadCtx(token, "bytes=4-")
	DownloadLogExport(ctx)
	require.Equal(t, http.StatusPartialContent, rec.Code, rec.Body.String())
	assert.Equal(t, "456789", rec.Body.String())
	assert.Equal(t, "bytes 4-9/10", rec.Header().Get("Content-Range"))

	// 非法 Range
	ctx, rec = newDownloadCtx(token, "bytes=999-1000")
	DownloadLogExport(ctx)
	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, rec.Code)

	// 未知令牌
	ctx, rec = newDownloadCtx("not-a-token", "")
	DownloadLogExport(ctx)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// 管理员被降权后，已签发的下载链接必须立即失效。
func TestDownloadLogExport_RejectsDemotedUser(t *testing.T) {
	enableRedis(t)
	requireDB(t)

	commonUser := mkUser(t, nil) // 普通角色
	job := mkReadyExportJob(t, commonUser.Id, "data")
	token, err := model.CreateLogExportDownloadToken(job.JobID, commonUser.Id, 1)
	require.NoError(t, err)

	ctx, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	ctx.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(ctx)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// 封禁的管理员同样要被挡住：下载端点不经过鉴权中间件，只能自己复核状态。
func TestDownloadLogExport_RejectsBannedAdmin(t *testing.T) {
	enableRedis(t)
	requireDB(t)

	banned := mkUser(t, func(u *model.User) {
		u.Role = common.RoleAdminUser
		u.Status = common.UserStatusDisabled
	})
	job := mkReadyExportJob(t, banned.Id, "data")
	token, err := model.CreateLogExportDownloadToken(job.JobID, banned.Id, 1)
	require.NoError(t, err)

	ctx, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	ctx.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(ctx)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// 下载端点是 GET 且不经过鉴权中间件，兜底审计覆盖不到它——
// 取走全站日志这种动作必须留痕，所以 handler 内手动记录。
func TestDownloadLogExport_WritesAuditLog(t *testing.T) {
	enableRedis(t)
	requireDB(t)
	requireLogDB(t)

	admin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	job := mkReadyExportJob(t, admin.Id, "payload")
	token, err := model.CreateLogExportDownloadToken(job.JobID, admin.Id, 1)
	require.NoError(t, err)

	ctx, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	ctx.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(ctx)
	require.Equal(t, http.StatusOK, rec.Code)

	// 审计写入是异步的（gopool），轮询等待落库。
	// 必须按本次的 job_id 精确匹配：测试 id 生成器每个进程从头计数，
	// 只按「用户 + 动作」筛会命中上一轮遗留的审计行。
	var found *model.Log
	for i := 0; i < 60 && found == nil; i++ {
		time.Sleep(150 * time.Millisecond)
		var logs []*model.Log
		require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?",
			admin.Id, model.LogTypeManage).Find(&logs).Error)
		for _, l := range logs {
			if strings.Contains(l.Other, job.JobID) {
				found = l
				break
			}
		}
	}
	require.NotNil(t, found, "download must leave an audit trail")
	assert.Contains(t, found.Other, "log_export.download")
	t.Cleanup(func() { model.LOG_DB.Unscoped().Delete(&model.Log{}, found.Id) })
}

func TestDownloadLogExport_ZipAllParts(t *testing.T) {
	enableRedis(t)
	requireDB(t)

	admin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	job := mkReadyExportJob(t, admin.Id, "part-one")

	token, err := model.CreateLogExportDownloadToken(job.JobID, admin.Id, 0)
	require.NoError(t, err)

	ctx, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	ctx.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/zip", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), ".zip")
	assert.Greater(t, rec.Body.Len(), 0)
}

func TestLogExportDownloadSlots_LimitConcurrentDownloadsPerUser(t *testing.T) {
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.MaxConcurrentDownloadsPerUser = 2
	})
	user := nextTestID()

	assert.True(t, acquireLogExportDownloadSlot(user))
	assert.True(t, acquireLogExportDownloadSlot(user))
	assert.False(t, acquireLogExportDownloadSlot(user), "third concurrent download must be rejected")

	releaseLogExportDownloadSlot(user)
	assert.True(t, acquireLogExportDownloadSlot(user))

	releaseLogExportDownloadSlot(user)
	releaseLogExportDownloadSlot(user)
	// 计数归零后不应留下残留条目。
	logExportDownloads.Lock()
	_, exists := logExportDownloads.byUser[user]
	logExportDownloads.Unlock()
	assert.False(t, exists)
}

// ── 模板接口 ─────────────────────────────────────────────────────

func TestLogExportTemplateAPI_CRUD(t *testing.T) {
	requireDB(t)
	user := nextTestID()

	body := map[string]any{
		"name":    "tpl-" + strconv.Itoa(user),
		"columns": []string{"created_at", "quota"},
		"format":  "csv_gz",
		"options": map[string]any{"csv_bom": true, "header": true},
	}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/templates", body)
	CreateLogExportTemplate(asAdmin(ctx, user))
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var created model.LogExportTemplate
	require.NoError(t, common.Unmarshal(resp.Data, &created))
	require.NotZero(t, created.Id)
	t.Cleanup(func() { _ = model.DeleteLogExportTemplate(created.Id) })
	assert.Equal(t, []string{"created_at", "quota"}, created.ColumnKeys)

	// 列表可见
	ctx, rec = newCtx(t, http.MethodGet, "/api/log/export/templates", nil)
	GetLogExportTemplates(asAdmin(ctx, user))
	resp = decodeResp(t, rec)
	require.True(t, resp.Success)
	var list []model.LogExportTemplate
	require.NoError(t, common.Unmarshal(resp.Data, &list))
	assert.NotEmpty(t, list)

	// 更新
	updateBody := map[string]any{
		"name":    created.Name,
		"columns": []string{"created_at", "username", "content"},
		"format":  "csv_gz",
	}
	ctx, rec = newCtx(t, http.MethodPut, "/api/log/export/templates/"+strconv.Itoa(created.Id), updateBody)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(created.Id)}}
	UpdateLogExportTemplate(asAdmin(ctx, user))
	resp = decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	reloaded, err := model.GetLogExportTemplate(created.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{"created_at", "username", "content"}, reloaded.ColumnKeys)

	// 删除
	ctx, rec = newCtx(t, http.MethodDelete, "/api/log/export/templates/"+strconv.Itoa(created.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(created.Id)}}
	DeleteLogExportTemplate(asAdmin(ctx, user))
	require.True(t, decodeResp(t, rec).Success)

	gone, err := model.GetLogExportTemplate(created.Id)
	require.NoError(t, err)
	assert.Nil(t, gone)
}

// 共享标记只有 Root 能设置，普通管理员提交 is_shared 会被忽略。
func TestLogExportTemplateAPI_SharedFlagRequiresRoot(t *testing.T) {
	requireDB(t)
	user := nextTestID()
	body := map[string]any{
		"name":      "shared-" + strconv.Itoa(user),
		"columns":   []string{"created_at"},
		"is_shared": true,
	}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/templates", body)
	CreateLogExportTemplate(asAdmin(ctx, user))
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var created model.LogExportTemplate
	require.NoError(t, common.Unmarshal(resp.Data, &created))
	t.Cleanup(func() { _ = model.DeleteLogExportTemplate(created.Id) })
	assert.False(t, created.IsShared, "non-root admins must not publish shared templates")
}

func TestLogExportTemplateAPI_NonOwnerCannotModify(t *testing.T) {
	requireDB(t)
	owner := nextTestID()
	stranger := nextTestID()

	tpl := &model.LogExportTemplate{
		UserId: owner, Name: "own-" + strconv.Itoa(owner),
		ColumnKeys: []string{"created_at"}, IsShared: true,
	}
	require.NoError(t, model.CreateLogExportTemplate(tpl))
	t.Cleanup(func() { _ = model.DeleteLogExportTemplate(tpl.Id) })

	ctx, rec := newCtx(t, http.MethodDelete, "/api/log/export/templates/"+strconv.Itoa(tpl.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tpl.Id)}}
	DeleteLogExportTemplate(asAdmin(ctx, stranger))
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Root 可以管理任何模板。
	ctx, rec = newCtx(t, http.MethodDelete, "/api/log/export/templates/"+strconv.Itoa(tpl.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tpl.Id)}}
	DeleteLogExportTemplate(asRoot(ctx, stranger))
	assert.True(t, decodeResp(t, rec).Success)
}

func TestLogExportTemplateAPI_InvalidId(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodPut, "/api/log/export/templates/abc", map[string]any{})
	ctx.Params = gin.Params{{Key: "id", Value: "abc"}}
	UpdateLogExportTemplate(asAdmin(ctx, 1))
	assert.False(t, decodeResp(t, rec).Success)
}
