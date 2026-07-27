package controller

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ── 列目录 ───────────────────────────────────────────────────────

type logExportColumnDTO struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Group     string `json:"group"`
	AdminOnly bool   `json:"admin_only"`
}

// GetLogExportColumns 返回可导出的列目录与内置模板。
func GetLogExportColumns(c *gin.Context) {
	columns := model.LogExportColumns()
	out := make([]logExportColumnDTO, 0, len(columns))
	for i := range columns {
		out = append(out, logExportColumnDTO{
			Key:       columns[i].Key,
			Label:     columns[i].Label,
			Group:     columns[i].Group,
			AdminOnly: columns[i].AdminOnly,
		})
	}
	setting := operation_setting.GetLogExportSetting()
	common.ApiSuccess(c, gin.H{
		"columns":           out,
		"builtin_templates": model.BuiltinLogExportTemplates(),
		"default_template":  model.LogExportTemplateAsDisplayed,
		"max_columns":       model.LogExportMaxColumns,
		// 下发给前端做可行性判断与预估提示，避免前端硬编码这些阈值。
		"xlsx_max_rows": setting.GetXlsxMaxRows(),
		"rows_per_file": setting.GetRowsPerFile(),
		"max_range_sec": setting.GetAdminMaxRangeSec(),
	})
}

// ── 模板 ─────────────────────────────────────────────────────────

type logExportTemplateRequest struct {
	Name     string                 `json:"name"`
	Columns  []string               `json:"columns"`
	Format   string                 `json:"format"`
	Options  model.LogExportOptions `json:"options"`
	IsShared bool                   `json:"is_shared"`
}

func GetLogExportTemplates(c *gin.Context) {
	templates, err := model.ListLogExportTemplates(c.GetInt("id"))
	if err != nil {
		common.SysError("log export: list templates failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	common.ApiSuccess(c, templates)
}

func CreateLogExportTemplate(c *gin.Context) {
	var req logExportTemplateRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	tpl := &model.LogExportTemplate{
		UserId:     c.GetInt("id"),
		Name:       req.Name,
		Format:     req.Format,
		IsShared:   req.IsShared && isRootRequest(c),
		ColumnKeys: req.Columns,
		Opts:       req.Options,
	}
	if err := model.CreateLogExportTemplate(tpl); err != nil {
		respondLogExportTemplateError(c, err)
		return
	}
	common.ApiSuccess(c, tpl)
}

func UpdateLogExportTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidId)
		return
	}
	tpl, err := model.GetLogExportTemplate(id)
	if err != nil {
		common.SysError("log export: get template failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if tpl == nil {
		common.ApiErrorI18n(c, i18n.MsgLogExportTemplateNotFound)
		return
	}
	if !model.CanWriteLogExportTemplate(tpl, c.GetInt("id"), isRootRequest(c)) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgForbidden)})
		return
	}
	var req logExportTemplateRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	tpl.Name = req.Name
	tpl.Format = req.Format
	tpl.ColumnKeys = req.Columns
	tpl.Opts = req.Options
	tpl.IsShared = req.IsShared && isRootRequest(c)
	if err := model.UpdateLogExportTemplate(tpl); err != nil {
		respondLogExportTemplateError(c, err)
		return
	}
	common.ApiSuccess(c, tpl)
}

func DeleteLogExportTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidId)
		return
	}
	tpl, err := model.GetLogExportTemplate(id)
	if err != nil {
		common.SysError("log export: get template failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if tpl == nil {
		common.ApiErrorI18n(c, i18n.MsgLogExportTemplateNotFound)
		return
	}
	if !model.CanWriteLogExportTemplate(tpl, c.GetInt("id"), isRootRequest(c)) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgForbidden)})
		return
	}
	if err := model.DeleteLogExportTemplate(id); err != nil {
		common.SysError("log export: delete template failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	common.ApiSuccess(c, nil)
}

func respondLogExportTemplateError(c *gin.Context, err error) {
	var unknownCol *model.UnknownLogExportColumnError
	switch {
	case errors.As(err, &unknownCol):
		common.ApiErrorI18n(c, i18n.MsgLogExportInvalidColumn, map[string]any{"Key": unknownCol.Key})
	case errors.Is(err, model.ErrLogExportTooManyColumns):
		common.ApiErrorI18n(c, i18n.MsgLogExportTooManyColumns, map[string]any{"Max": model.LogExportMaxColumns})
	case errors.Is(err, model.ErrLogExportNoColumns):
		common.ApiErrorI18n(c, i18n.MsgLogExportNoColumns)
	case errors.Is(err, model.ErrLogExportTemplateNameEmpty):
		common.ApiErrorI18n(c, i18n.MsgLogExportTemplateNameEmpty)
	case errors.Is(err, model.ErrLogExportTemplateNameLong):
		common.ApiErrorI18n(c, i18n.MsgLogExportTemplateNameLong)
	case errors.Is(err, model.ErrLogExportTemplateExists):
		common.ApiErrorI18n(c, i18n.MsgLogExportTemplateExists)
	case errors.Is(err, model.ErrLogExportTemplateLimit):
		common.ApiErrorI18n(c, i18n.MsgLogExportTemplateLimit,
			map[string]any{"Max": operation_setting.GetLogExportSetting().GetMaxTemplatesPerUser()})
	default:
		common.SysError("log export: template operation failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
	}
}

func isRootRequest(c *gin.Context) bool {
	return c.GetInt("role") >= common.RoleRootUser
}

// logExportEstimateTimeout 估算查询的硬超时。
//
// 不做成配置项：这是交互提示的响应预算，不是运营旋钮；调大它只会让最坏情况
// （过滤条件无匹配 → 扫完整段时间范围）在日志大表上跑得更久。
const logExportEstimateTimeout = 3 * time.Second

// GetLogExportEstimate 估算某组筛选条件命中的行数，供新建导出弹窗判断
// xlsx 是否可行、预计几个分片。
//
// 走有界计数（数到上限即止），不复用日志列表接口——那个接口每次都会对
// 命中集做完整 COUNT，而这个估算会随用户敲筛选条件被反复触发。
func GetLogExportEstimate(c *gin.Context) {
	setting := operation_setting.GetLogExportSetting()

	start, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if start <= 0 || end <= 0 {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeRequired)
		return
	}
	if end < start {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeInvalid)
		return
	}
	maxRange := setting.GetAdminMaxRangeSec()
	if end-start > maxRange {
		// 超限的范围后端本来就会拒绝导出，没必要再为它扫库。
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeTooLong,
			map[string]any{"Days": maxRange / 86400})
		return
	}

	logType, _ := strconv.Atoi(c.Query("type"))
	channel, _ := strconv.Atoi(c.Query("channel"))
	filter := model.LogExportFilter{
		LogType:        logType,
		StartTimestamp: start,
		EndTimestamp:   end,
		ModelName:      c.Query("model_name"),
		Username:       c.Query("username"),
		TokenName:      c.Query("token_name"),
		ChannelId:      channel,
		Group:          c.Query("group"),
	}

	// 数到「xlsx 上限 + 1」就够：再多也只是用来判断超没超限。
	limit := setting.GetXlsxMaxRows() + 1
	// 超时刻意取得很短：LIMIT 限住的是结果不是工作量，过滤条件无匹配时
	// 数据库要扫完整段范围才知道凑不满。估算只是提示，宁可放弃也不该让它
	// 在日志大表上跑几十秒。
	ctx, cancel := context.WithTimeout(c.Request.Context(), logExportEstimateTimeout)
	defer cancel()

	rows, capped, err := model.EstimateLogExportRows(ctx, filter, limit)
	if err != nil {
		// 估算只影响提示，失败不该挡住导出。
		common.SysError("log export: estimate failed: " + err.Error())
		common.ApiSuccess(c, gin.H{"rows": 0, "capped": false, "available": false})
		return
	}
	common.ApiSuccess(c, gin.H{"rows": rows, "capped": capped, "available": true})
}

// ── 任务 ─────────────────────────────────────────────────────────

type createLogExportJobRequest struct {
	StartTimestamp int64                  `json:"start_timestamp"`
	EndTimestamp   int64                  `json:"end_timestamp"`
	LogType        int                    `json:"type"`
	ModelName      string                 `json:"model_name"`
	Username       string                 `json:"username"`
	TokenName      string                 `json:"token_name"`
	Channel        int                    `json:"channel"`
	Group          string                 `json:"group"`
	Columns        []string               `json:"columns"`
	TemplateId     string                 `json:"template_id"`
	Format         string                 `json:"format"`
	EstRows        int64                  `json:"est_rows"`
	Options        model.LogExportOptions `json:"options"`
}

// CreateLogExportJob 创建后台导出任务（管理员）。
func CreateLogExportJob(c *gin.Context) {
	setting := operation_setting.GetLogExportSetting()
	if !setting.Enabled {
		common.ApiErrorI18n(c, i18n.MsgLogExportDisabled)
		return
	}
	if !model.LogExportAvailable() {
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}

	var req createLogExportJobRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	// 时间范围必填且不得超过配置的最大跨度。
	if req.StartTimestamp <= 0 || req.EndTimestamp <= 0 {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeRequired)
		return
	}
	if req.EndTimestamp < req.StartTimestamp {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeInvalid)
		return
	}
	maxRange := setting.GetAdminMaxRangeSec()
	if req.EndTimestamp-req.StartTimestamp > maxRange {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeTooLong,
			map[string]any{"Days": maxRange / 86400})
		return
	}

	columns, format, options, err := resolveLogExportRequestShape(c, req)
	if err != nil {
		respondLogExportTemplateError(c, err)
		return
	}

	userId := c.GetInt("id")
	if model.CountActiveLogExportJobs(userId) >= setting.GetMaxActiveJobsPerUser() {
		common.ApiErrorI18n(c, i18n.MsgLogExportTooManyJobs,
			map[string]any{"Max": setting.GetMaxActiveJobsPerUser()})
		return
	}
	if err := model.CheckLogExportDiskSpace(); err != nil {
		common.ApiErrorI18n(c, i18n.MsgLogExportDiskFull)
		return
	}

	jobID := model.NewLogExportJobID()
	// 先抢槽位再计冷却：系统正忙时不该白白消耗掉用户的一次导出机会。
	slotTTL := time.Duration(setting.GetTimeoutSec()) * time.Second
	if !model.AcquireLogExportSlot(jobID, setting.GetMaxConcurrentJobs(), slotTTL) {
		common.ApiErrorI18n(c, i18n.MsgLogExportBusy)
		return
	}
	if model.CheckAndSetLogExportCooldown(userId) {
		model.ReleaseLogExportSlot(jobID)
		common.ApiErrorI18n(c, i18n.MsgLogExportCooldown,
			map[string]any{"Minutes": (setting.GetUserCooldownSec() + 59) / 60})
		return
	}

	// 被动清理孤儿文件，放 goroutine 里避免慢 IO 拖住这次请求。
	go model.CleanupOrphanLogExportFiles()

	job := &model.LogExportJob{
		JobID:    jobID,
		UserID:   userId,
		Username: c.GetString("username"),
		Format:   format,
		Columns:  columns,
		Options:  options,
		Lang:     i18n.GetLangFromContext(c),
		Filters: model.LogExportFilter{
			LogType:        req.LogType,
			StartTimestamp: req.StartTimestamp,
			EndTimestamp:   req.EndTimestamp,
			ModelName:      req.ModelName,
			Username:       req.Username,
			TokenName:      req.TokenName,
			ChannelId:      req.Channel,
			Group:          req.Group,
		},
	}
	if err := model.CreateLogExportJob(job); err != nil {
		model.ReleaseLogExportSlot(jobID)
		model.ClearLogExportCooldown(userId)
		common.SysError("log export: create job failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgLogExportCreateFailed)
		return
	}

	model.StartLogExport(job)
	auditLogExportJobCreated(c, job)
	common.ApiSuccess(c, gin.H{"job_id": job.JobID})
}

// auditLogExportJobCreated 手动埋点：中间件兜底只会记下「POST 了哪个路由」，
// 而导出的关键信息是「谁、取了哪段时间、哪些筛选条件、多少列」。
// 设置 ContextKeyAuditLogged 以避免与兜底记录重复。
func auditLogExportJobCreated(c *gin.Context, job *model.LogExportJob) {
	common.SetContextKey(c, constant.ContextKeyAuditLogged, true)
	params := map[string]interface{}{
		"job_id":  job.JobID,
		"start":   job.Filters.StartTimestamp,
		"end":     job.Filters.EndTimestamp,
		"columns": len(job.Columns),
		"format":  job.Format,
	}
	if scope := logExportFilterScope(job.Filters); scope != "" {
		params["filters"] = scope
	}
	recordLogExportAudit(c, "log_export.job_create",
		"created log export job "+job.JobID, params)
}

// logExportFilterScope 把筛选条件压成一行可读摘要，便于事后追查「导了什么」。
func logExportFilterScope(filter model.LogExportFilter) string {
	parts := make([]string, 0, 6)
	if filter.LogType != 0 {
		parts = append(parts, "type="+strconv.Itoa(filter.LogType))
	}
	if filter.Username != "" {
		parts = append(parts, "user="+filter.Username)
	}
	if filter.ModelName != "" {
		parts = append(parts, "model="+filter.ModelName)
	}
	if filter.TokenName != "" {
		parts = append(parts, "token="+filter.TokenName)
	}
	if filter.Group != "" {
		parts = append(parts, "group="+filter.Group)
	}
	if filter.ChannelId != 0 {
		parts = append(parts, "channel="+strconv.Itoa(filter.ChannelId))
	}
	return strings.Join(parts, " ")
}

// recordLogExportAudit 异步写一条管理员操作审计日志，失败不影响主流程。
func recordLogExportAudit(c *gin.Context, action, content string, params map[string]interface{}) {
	operatorID := c.GetInt("id")
	adminInfo := map[string]interface{}{
		"admin_id":       operatorID,
		"admin_username": c.GetString("username"),
		"admin_role":     c.GetInt("role"),
	}
	auditInfo := map[string]interface{}{
		"method": c.Request.Method,
		"route":  c.FullPath(),
		"path":   c.Request.URL.Path,
	}
	ip := c.ClientIP()
	gopool.Go(func() {
		model.RecordOperationAuditLog(operatorID, content, ip, action, params, adminInfo, auditInfo)
	})
}

// resolveLogExportRequestShape 归并 columns / template_id / format / options。
// columns 优先于 template_id；两者都为空时回落默认模板（页面所见）。
func resolveLogExportRequestShape(c *gin.Context, req createLogExportJobRequest) (
	columns []string, format string, options model.LogExportOptions, err error,
) {
	columns = req.Columns
	format = req.Format
	options = req.Options

	if len(columns) == 0 && req.TemplateId != "" {
		if builtin, ok := model.LookupBuiltinLogExportTemplate(req.TemplateId); ok {
			columns = builtin.Columns
		} else {
			id, convErr := strconv.Atoi(req.TemplateId)
			if convErr != nil {
				return nil, "", options, model.ErrLogExportTemplateNotFound
			}
			tpl, dbErr := model.GetLogExportTemplate(id)
			if dbErr != nil {
				return nil, "", options, dbErr
			}
			if tpl == nil || !model.CanReadLogExportTemplate(tpl, c.GetInt("id")) {
				return nil, "", options, model.ErrLogExportTemplateNotFound
			}
			columns = tpl.ColumnKeys
			if format == "" {
				format = tpl.Format
			}
		}
	}

	set, err := model.ResolveLogExportColumns(columns, true)
	if err != nil {
		return nil, "", options, err
	}
	columns = set.Keys

	if !model.ValidLogExportFormat(format) {
		format = model.LogExportFormatCSVGz
	}
	// xlsx 只服务小结果集：预估行数超阈值时直接落到 csv.gz，避免白跑一趟再降级。
	if format == model.LogExportFormatXlsx {
		if req.EstRows > int64(operation_setting.GetLogExportSetting().GetXlsxMaxRows()) {
			format = model.LogExportFormatCSVGz
		}
	}
	return columns, format, options, nil
}

// GetLogExportJobs 列出任务。Root 可用 ?all=true 查看所有人的任务。
func GetLogExportJobs(c *gin.Context) {
	if !model.LogExportAvailable() {
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	all := c.Query("all") == "true" && isRootRequest(c)
	jobs, err := model.ListLogExportJobs(c.GetInt("id"), all, 20)
	if err != nil {
		common.SysError("log export: list jobs failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	views := make([]*model.LogExportJob, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, job.PublicView())
	}
	common.ApiSuccess(c, gin.H{
		"jobs":    views,
		"enabled": operation_setting.GetLogExportSetting().Enabled,
	})
}

// loadOwnedLogExportJob 读取任务并校验归属。Root 可访问所有人的任务。
func loadOwnedLogExportJob(c *gin.Context) (*model.LogExportJob, bool) {
	job, err := model.GetLogExportJob(c.Param("job_id"))
	if err != nil {
		common.SysError("log export: get job failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return nil, false
	}
	if job == nil {
		common.ApiErrorI18n(c, i18n.MsgLogExportJobNotFound)
		return nil, false
	}
	if job.UserID != c.GetInt("id") && !isRootRequest(c) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgForbidden)})
		return nil, false
	}
	return job, true
}

func GetLogExportJob(c *gin.Context) {
	if !model.LogExportAvailable() {
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	job, ok := loadOwnedLogExportJob(c)
	if !ok {
		return
	}
	common.ApiSuccess(c, job.PublicView())
}

// DeleteLogExportJob 取消运行中的任务，或删除已结束的任务及其文件。
func DeleteLogExportJob(c *gin.Context) {
	if !model.LogExportAvailable() {
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	job, ok := loadOwnedLogExportJob(c)
	if !ok {
		return
	}
	if !job.IsTerminal() {
		// 运行中：置取消标志，goroutine 在下一批检测到后退出并清理半成品。
		model.CancelLogExportJob(job.JobID)
		common.ApiSuccess(c, gin.H{"canceled": true})
		return
	}
	model.RemoveLogExportJobFiles(job)
	model.DeleteLogExportJob(job)
	common.ApiSuccess(c, gin.H{"deleted": true})
}

// GetLogExportDownloadURL 签发一次性下载链接。part 为空或 "all" 时打包全部分片。
func GetLogExportDownloadURL(c *gin.Context) {
	if !model.LogExportAvailable() {
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	job, ok := loadOwnedLogExportJob(c)
	if !ok {
		return
	}
	if job.Status != model.LogExportStatusReady {
		common.ApiErrorI18n(c, i18n.MsgLogExportJobNotReady)
		return
	}

	part := 0
	if raw := c.Query("part"); raw != "" && raw != "all" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		part = parsed
	}
	if part > 0 && findLogExportPart(job, part) == nil {
		common.ApiErrorI18n(c, i18n.MsgLogExportPartNotFound)
		return
	}

	token, err := model.CreateLogExportDownloadToken(job.JobID, job.UserID, part)
	if err != nil {
		common.SysError("log export: create download token failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	common.ApiSuccess(c, gin.H{
		"url":        "/dl/log-export/" + token,
		"expires_in": int(operation_setting.GetLogExportSetting().GetDownloadTokenTTL().Seconds()),
	})
}

func findLogExportPart(job *model.LogExportJob, index int) *model.LogExportPart {
	for i := range job.Parts {
		if job.Parts[i].Index == index {
			return &job.Parts[i]
		}
	}
	return nil
}

// ── 下载 ─────────────────────────────────────────────────────────

// 单实例部署，用进程内计数限制每用户并发下载即可，无需 Redis 往返。
// 若将来横向扩容，这里需要换成 Redis 计数。
var logExportDownloads = struct {
	sync.Mutex
	byUser map[int]int
}{byUser: make(map[int]int)}

func acquireLogExportDownloadSlot(userID int) bool {
	limit := operation_setting.GetLogExportSetting().GetMaxConcurrentDownloadsPerUser()
	logExportDownloads.Lock()
	defer logExportDownloads.Unlock()
	if logExportDownloads.byUser[userID] >= limit {
		return false
	}
	logExportDownloads.byUser[userID]++
	return true
}

func releaseLogExportDownloadSlot(userID int) {
	logExportDownloads.Lock()
	defer logExportDownloads.Unlock()
	if logExportDownloads.byUser[userID] <= 1 {
		delete(logExportDownloads.byUser, userID)
		return
	}
	logExportDownloads.byUser[userID]--
}

// DownloadLogExport 提供分片下载。
//
// 挂在鉴权组与 gzip 中间件之外：凭证是 URL 里的一次性令牌，浏览器可直接打开
// 无需自定义头；内容已 gzip 压缩，绕过 gzip 中间件避免二次压缩浪费 CPU。
// 单分片走 http.ServeContent，自动支持 Range / 206 / If-Range 断点续传。
func DownloadLogExport(c *gin.Context) {
	if !model.LogExportAvailable() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "export service unavailable"})
		return
	}
	jobID, userID, part, err := model.ConsumeLogExportDownloadToken(c.Param("token"))
	if err != nil {
		common.SysError("log export: consume download token failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "download failed"})
		return
	}
	if jobID == "" {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "download link expired"})
		return
	}

	// 令牌不经过鉴权中间件，这里必须自己复核当前身份：管理员被降权或被封禁后，
	// 已签发的链接要立刻失效（否则续传窗口内仍能取数）。
	if !logExportDownloaderAllowed(userID) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "access denied"})
		return
	}

	job, err := model.GetLogExportJob(jobID)
	if err != nil || job == nil || job.UserID != userID || job.Status != model.LogExportStatusReady {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "export not available"})
		return
	}
	if len(job.Parts) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "export not available"})
		return
	}

	if !acquireLogExportDownloadSlot(userID) {
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "too many concurrent downloads"})
		return
	}
	defer releaseLogExportDownloadSlot(userID)

	// 这里不写审计日志：审计日志落在 logs 表，而下载是断点续传（同一分片会发多次
	// Range 请求），给正在保护的大表持续加写入不划算。谁在什么时间、按什么条件
	// 导出了哪段数据，已经由创建任务时的 log_export.job_create 记下。
	c.Header("Cache-Control", "no-store")
	if part > 0 {
		serveLogExportPart(c, job, part)
		return
	}
	serveLogExportZip(c, job)
}

// logExportDownloaderAllowed 复核下载者当前仍是启用状态的管理员。
// model.IsAdmin 只看角色，封禁用户同样需要挡住。
func logExportDownloaderAllowed(userID int) bool {
	if userID <= 0 {
		return false
	}
	user, err := model.GetUserCache(userID)
	if err != nil || user == nil {
		return false
	}
	return user.Role >= common.RoleAdminUser && user.Status == common.UserStatusEnabled
}

func logExportPartFileName(job *model.LogExportJob, index int) string {
	ext := ".csv.gz"
	if job.Format == model.LogExportFormatXlsx {
		ext = ".xlsx"
	}
	stamp := time.Unix(job.CreatedAt, 0).Format("20060102-150405")
	return fmt.Sprintf("log-export-%s-part-%04d%s", stamp, index, ext)
}

func serveLogExportPart(c *gin.Context, job *model.LogExportJob, index int) {
	part := findLogExportPart(job, index)
	if part == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "part not found"})
		return
	}
	f, err := os.Open(part.Path)
	if err != nil {
		common.SysError("log export: open part failed: " + err.Error())
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "export file unavailable"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "export file unavailable"})
		return
	}

	filename := logExportPartFileName(job, index)
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	if job.Format == model.LogExportFormatXlsx {
		c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	} else {
		c.Header("Content-Type", "application/gzip")
	}
	// ServeContent 负责 Range/206/If-Range，断点续传由标准库处理。
	http.ServeContent(c.Writer, c.Request, filename, info.ModTime(), f)
}

func serveLogExportZip(c *gin.Context, job *model.LogExportJob) {
	stamp := time.Unix(job.CreatedAt, 0).Format("20060102-150405")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="log-export-%s.zip"`, stamp))
	c.Header("Content-Type", "application/zip")

	zw := zip.NewWriter(c.Writer)
	defer zw.Close()
	for _, part := range job.Parts {
		f, err := os.Open(part.Path)
		if err != nil {
			common.SysError("log export: open part failed: " + err.Error())
			return
		}
		// 分片已是 gz/xlsx（内部即 zip），Store 不做二次压缩，省 CPU。
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:   logExportPartFileName(job, part.Index),
			Method: zip.Store,
		})
		if err != nil {
			_ = f.Close()
			return
		}
		if _, err := io.Copy(w, f); err != nil {
			_ = f.Close()
			return
		}
		_ = f.Close()
	}
}
