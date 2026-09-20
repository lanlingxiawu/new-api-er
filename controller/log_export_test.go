package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

// 估算接口必须自己挡住超限范围：那种范围导出本来就会被拒，没必要为它扫库。
func TestGetLogExportEstimate_ValidatesRange(t *testing.T) {
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.AdminMaxRangeSec = 86400
	})
	now := time.Now().Unix()

	cases := []struct {
		name  string
		query string
	}{
		{"missing_range", ""},
		{"reversed", fmt.Sprintf("?start_timestamp=%d&end_timestamp=%d", now, now-100)},
		{"too_long", fmt.Sprintf("?start_timestamp=%d&end_timestamp=%d", now-200000, now)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/estimate"+tc.query, nil)
			GetLogExportEstimate(asAdmin(ctx, nextTestID()))
			resp := decodeResp(t, rec)
			assert.False(t, resp.Success)
			assert.NotEmpty(t, resp.Message)
		})
	}
}

func TestGetLogExportEstimate_ReturnsBoundedCount(t *testing.T) {
	requireLogDB(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.AdminMaxRangeSec = 86400
		// 上限取 xlsx_max_rows + 1，这里压到 2 让 capped 可观察。
		s.XlsxMaxRows = 2
	})

	username := uniq("est_api")
	base := time.Now().Unix() - 3600
	for i := 0; i < 5; i++ {
		i := i
		mkCtrlExportLog(t, func(l *model.Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
		})
	}

	target := fmt.Sprintf("/api/log/export/estimate?start_timestamp=%d&end_timestamp=%d&username=%s",
		base-10, base+100, username)
	ctx, rec := newCtx(t, http.MethodGet, target, nil)
	GetLogExportEstimate(asAdmin(ctx, nextTestID()))

	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var data struct {
		Rows      int64 `json:"rows"`
		Capped    bool  `json:"capped"`
		Available bool  `json:"available"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	assert.True(t, data.Available)
	assert.Equal(t, int64(3), data.Rows, "counting stops at xlsx_max_rows + 1")
	assert.True(t, data.Capped)
}

// mkCtrlExportLog 往 LOG_DB 插一条日志并注册清理。
func mkCtrlExportLog(t *testing.T, mut func(l *model.Log)) *model.Log {
	t.Helper()
	l := &model.Log{
		UserId:    nextTestID(),
		CreatedAt: time.Now().Unix(),
		Type:      model.LogTypeConsume,
		Username:  uniq("ctrlexp"),
		ModelName: "gpt-4o",
	}
	if mut != nil {
		mut(l)
	}
	require.NoError(t, model.LOG_DB.Create(l).Error)
	id := l.Id
	t.Cleanup(func() { model.LOG_DB.Unscoped().Delete(&model.Log{}, id) })
	return l
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
	// 上一轮测试进程被杀时留下的孤儿槽位会让本用例的第一次创建就失败。
	// 回收一次，拿到确定的起点——这正是 RecoverStaleLogExportJobs 该做的事。
	model.RecoverStaleLogExportJobs()
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

	// 并发槽只有 1 个，占满时另一个用户应被挡住（且不消耗他的冷却）。
	//
	// 这里显式占槽，而不是指望上面那个任务「还在跑」：闸门改成按实际行数计费之后，
	// 小数据量的导出几乎瞬间完成并释放槽位，靠任务耗时来制造竞争的写法会随机失败。
	// 上限传 2 而不是 1：上面那个任务是异步跑的，此刻可能还占着唯一的槽位，
	// 传 1 会让这次占槽失败。传 2 则无论它是否已释放都能占到，而下面的请求
	// 走的是配置里的 max_concurrent_jobs=1，两种情况下都必然被挡住。
	const slotHolder = "test-slot-holder"
	require.True(t, model.AcquireLogExportSlot(slotHolder, 2, time.Minute))
	t.Cleanup(func() { model.ReleaseLogExportSlot(slotHolder) })

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

// ── 新增筛选条件的接口层校验 ──────────────────────────────────────

func TestCreateLogExportJob_ValidatesNewFilters(t *testing.T) {
	enableRedis(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		s.MaxFilterValues = 3
	})

	cases := []struct {
		name     string
		mutate   func(body map[string]any)
		contains string
	}{
		{
			name:     "用量来源取值非法",
			mutate:   func(b map[string]any) { b["usage_source"] = []string{"guessed"} },
			contains: "guessed",
		},
		{
			name:     "异常种类取值非法",
			mutate:   func(b map[string]any) { b["anomaly_kinds"] = []string{"made_up"} },
			contains: "made_up",
		},
		{
			name: "单个条件取值过多",
			mutate: func(b map[string]any) {
				b["stream_end_reason"] = []string{"a", "b", "c", "d"}
			},
			contains: "3",
		},
		{
			name: "最小值大于最大值",
			mutate: func(b map[string]any) {
				b["quota_min"] = 100
				b["quota_max"] = 10
			},
		},
		{
			name: "耗时区间反了",
			mutate: func(b map[string]any) {
				b["use_time_min"] = 60
				b["use_time_max"] = 10
			},
		},
		{
			name:   "重试次数为负",
			mutate: func(b map[string]any) { b["min_retry_count"] = -1 },
		},
		{
			name:   "聚合模式未选维度",
			mutate: func(b map[string]any) { b["mode"] = "summary" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := validExportJobBody()
			tc.mutate(body)
			ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", body)
			CreateLogExportJob(asAdmin(ctx, nextTestID()))
			resp := decodeResp(t, rec)
			require.False(t, resp.Success, "非法条件必须被拒绝")
			if tc.contains != "" {
				assert.Contains(t, resp.Message, tc.contains)
			}
		})
	}
}

// 结束原因与结算状态刻意不做白名单：取值随协议演进增加，
// 白名单只会挡住刚出现的那种异常——而那正是最需要被查出来的。
func TestCreateLogExportJob_DoesNotWhitelistEvolvingFilterValues(t *testing.T) {
	enableRedis(t)
	model.RecoverStaleLogExportJobs()
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		// 孤儿槽位与上一轮进程留下的冷却键都不该让这条用例失败。
		s.MaxConcurrentJobs = 20
		s.TimeoutSec = 30
	})

	// UserCooldownSec 置 0 不会关闭冷却（getter 对 <=0 回落到默认 300 秒），
	// 而 nextTestID 跨进程重复，上一轮留下的冷却键仍然有效。显式清掉。
	userID := nextTestID()
	model.ClearLogExportCooldown(userID)
	t.Cleanup(func() { model.ClearLogExportCooldown(userID) })

	body := validExportJobBody()
	body["stream_end_reason"] = []string{"some_brand_new_reason"}
	body["settlement_state"] = []string{"whatever_comes_next"}
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", body)
	CreateLogExportJob(asAdmin(ctx, userID))
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, "未知的结束原因不该被拒绝: %s", resp.Message)
	var created struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &created))
	cleanupExportJob(t, created.JobID)
}

// 数值条件里 0 是有意义的取值（quota_max=0 = 只看零费用的行），
// 必须真的传到筛选条件里，不能被当成「未设置」丢掉（Rule 5）。
func TestCreateLogExportJob_ZeroValuedNumericFilterIsPreserved(t *testing.T) {
	enableRedis(t)
	model.RecoverStaleLogExportJobs()
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		// 孤儿槽位与上一轮进程留下的冷却键都不该让这条用例失败。
		s.MaxConcurrentJobs = 20
		s.TimeoutSec = 30
	})

	userID := nextTestID()
	model.ClearLogExportCooldown(userID)
	t.Cleanup(func() { model.ClearLogExportCooldown(userID) })

	body := validExportJobBody()
	body["quota_max"] = 0
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", body)
	CreateLogExportJob(asAdmin(ctx, userID))
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)

	var created struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, common.Unmarshal(resp.Data, &created))
	require.NotEmpty(t, created.JobID)
	cleanupExportJob(t, created.JobID)

	job, err := model.GetLogExportJob(created.JobID)
	require.NoError(t, err)
	require.NotNil(t, job)
	require.NotNil(t, job.Filters.QuotaMax, "显式传入的 0 必须保留为非 nil 指针")
	assert.Equal(t, 0, *job.Filters.QuotaMax)
	assert.Nil(t, job.Filters.QuotaMin, "未传的条件仍应为 nil")
}

// 列目录要把受众、异常种类、聚合维度一并下发——前端据此渲染，
// 硬编码一份必然与后端漂移。
func TestGetLogExportColumns_ExposesAudienceAndFilterVocabulary(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/log/export/columns", nil)
	GetLogExportColumns(asAdmin(ctx, nextTestID()))
	resp := decodeResp(t, rec)
	require.True(t, resp.Success)

	var data map[string]any
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	require.NotEmpty(t, data["anomaly_kinds"])
	require.NotEmpty(t, data["summary_dimensions"])
	assert.NotNil(t, data["max_filter_values"])

	columns, ok := data["columns"].([]any)
	require.True(t, ok)
	audiences := map[string]bool{}
	for _, raw := range columns {
		col, ok := raw.(map[string]any)
		require.True(t, ok)
		audience, _ := col["audience"].(string)
		require.Contains(t, []string{"internal", "customer"}, audience,
			"列 %v 的受众取值非法", col["key"])
		audiences[audience] = true
	}
	assert.True(t, audiences["customer"], "必须有可发给客户的列")
	assert.True(t, audiences["internal"], "必须有仅内部的列")

	templates, ok := data["builtin_templates"].([]any)
	require.True(t, ok)
	var customerTemplates int
	for _, raw := range templates {
		tpl, _ := raw.(map[string]any)
		require.NotEmpty(t, tpl["purpose"], "模板 %v 缺少用途", tpl["id"])
		if tpl["audience"] == "customer" {
			customerTemplates++
		}
	}
	assert.Equal(t, 1, customerTemplates, "只应有「客户对账单」一个面向客户的模板")
}

// 「明细 + 汇总」模式下两种分片同在一个压缩包里，下载文件名必须能区分，
// 否则拿到包的人得逐个解开才知道哪个是明细、哪个是汇总。
func TestLogExportPartFileName_DistinguishesSummary(t *testing.T) {
	job := &model.LogExportJob{
		CreatedAt: time.Date(2026, 9, 20, 15, 30, 27, 0, time.Local).Unix(),
		Format:    model.LogExportFormatCSVGz,
	}
	detail := logExportPartFileName(job, &model.LogExportPart{Index: 1})
	summary := logExportPartFileName(job, &model.LogExportPart{
		Index: 2, Kind: model.LogExportPartKindSummary,
	})
	assert.Contains(t, detail, "-part-0001.csv.gz")
	assert.Contains(t, summary, "-summary-0002.csv.gz")
	assert.NotEqual(t, detail, summary)

	// 空 Kind 按明细处理，升级前创建的任务文件名不变。
	legacy := logExportPartFileName(job, &model.LogExportPart{Index: 1, Kind: ""})
	assert.Equal(t, detail, legacy)

	job.Format = model.LogExportFormatXlsx
	assert.Contains(t, logExportPartFileName(job, &model.LogExportPart{
		Index: 3, Kind: model.LogExportPartKindSummary,
	}), "-summary-0003.xlsx")
}

// mode=both 必须带着聚合维度落库。
//
// 这条用例对应一个真实发生的缺陷：前端校验改成了「非明细模式都要有维度」，
// 但组装请求体那行仍是「只有纯汇总才发维度」，于是 both 模式带着空维度提交，
// 前端校验通过、后端却回「请至少选择一个聚合维度」。后端这侧必须把两种模式
// 一视同仁地校验并保存，才能在前端再犯同类错误时立刻暴露。
func TestCreateLogExportJob_BothModeRequiresAndKeepsDimensions(t *testing.T) {
	enableRedis(t)
	model.RecoverStaleLogExportJobs()
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.Enabled = true
		s.MaxConcurrentJobs = 20
		s.TimeoutSec = 30
	})

	// 缺维度必须被拒——与纯汇总模式同样对待。
	body := validExportJobBody()
	body["mode"] = "both"
	ctx, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", body)
	CreateLogExportJob(asAdmin(ctx, nextTestID()))
	require.False(t, decodeResp(t, rec).Success, "both 模式缺维度必须被拒绝")

	// 带了维度就要原样存进任务，且 Mode 保持 both（不能被写成 summary）。
	userID := nextTestID()
	model.ClearLogExportCooldown(userID)
	t.Cleanup(func() { model.ClearLogExportCooldown(userID) })

	body = validExportJobBody()
	body["mode"] = "both"
	body["summary_dims"] = []string{"model_name", "date"}
	ctx, rec = newCtx(t, http.MethodPost, "/api/log/export/jobs", body)
	CreateLogExportJob(asAdmin(ctx, userID))
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
	assert.Equal(t, model.LogExportModeBoth, job.Mode, "Mode 必须保持 both")
	assert.Equal(t, []string{"date", "model_name"}, job.SummaryDims, "维度按固定顺序规整后保存")
	assert.NotEmpty(t, job.Columns, "both 模式同时需要明细列")
}

// 估算必须把能下推 SQL 的数值条件算进去。
//
// 否则设了「最多输出 tokens = 0」（实际只会导出个位数行），弹窗却笃定地
// 报出整段时间范围的行数，管理员据此判断 xlsx 可行性会被彻底误导。
func TestGetLogExportEstimate_HonoursNumericFilters(t *testing.T) {
	requireLogDB(t)
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.AdminMaxRangeSec = 86400
		s.XlsxMaxRows = 1000
	})

	username := uniq("est_num")
	base := time.Now().Unix() - 3600
	// 3 条有输出，1 条输出为 0 且计费。
	for i := 0; i < 3; i++ {
		i := i
		mkCtrlExportLog(t, func(l *model.Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
			l.CompletionTokens = 20
			l.Quota = 100
		})
	}
	mkCtrlExportLog(t, func(l *model.Log) {
		l.Username = username
		l.CreatedAt = base + 5
		l.CompletionTokens = 0
		l.Quota = 100
	})

	estimate := func(extra string) int64 {
		target := fmt.Sprintf(
			"/api/log/export/estimate?start_timestamp=%d&end_timestamp=%d&username=%s%s",
			base-10, base+100, username, extra)
		ctx, rec := newCtx(t, http.MethodGet, target, nil)
		GetLogExportEstimate(asAdmin(ctx, nextTestID()))
		resp := decodeResp(t, rec)
		require.True(t, resp.Success, resp.Message)
		var data struct {
			Rows int64 `json:"rows"`
		}
		require.NoError(t, common.Unmarshal(resp.Data, &data))
		return data.Rows
	}

	assert.EqualValues(t, 4, estimate(""), "无数值条件时计入全部")
	assert.EqualValues(t, 1, estimate("&completion_tokens_max=0"),
		"completion_tokens_max=0 必须参与估算，且 0 不能被当成未设置")
	assert.EqualValues(t, 1, estimate("&completion_tokens_max=0&charged=true"),
		"charged 同样要参与估算")
	assert.EqualValues(t, 0, estimate("&charged=false"), "全部都计费了，未计费应为 0")
}
