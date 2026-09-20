package controller

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
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
	// Audience "customer" 表示该列可以出现在发给客户的文件里。
	// 前端据此在列选择器里给内部列打角标——靠标注提醒，不阻断选择。
	Audience string `json:"audience"`
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
			Audience:  columns[i].Audience.String(),
		})
	}
	setting := operation_setting.GetLogExportSetting()
	common.ApiSuccess(c, gin.H{
		"columns":           out,
		"builtin_templates": model.BuiltinLogExportTemplates(),
		"default_template":  model.LogExportTemplateAsDisplayed,
		"max_columns":       model.LogExportMaxColumns,
		// 异常种类由后端下发而不是前端硬编码：判定规则住在后端，
		// 前端另写一份迟早会与 logAnomalyFlags 漂移。
		"anomaly_kinds":     model.LogExportAnomalyKinds(),
		"max_filter_values": setting.GetMaxFilterValues(),
		// 聚合汇总可选维度。时间粒度只有「天」——小时会把组合数乘 24。
		"summary_dimensions": model.LogSummaryDimensions(),
		// 额度→美元的换算率。quota 是整数额度列，而界面上花费一栏显示的是美元；
		// 不把换算率下发，数值筛选框的单位就只能靠使用者猜，必然填错数量级。
		"quota_per_unit": common.QuotaPerUnit,
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
	// 估算只覆盖能下推 SQL 的条件。行级条件（异常、用量来源等）没有索引可走，
	// 无法在一次有界计数里算出来，因此带这些条件时估算结果是**上限**而非预测值。
	// 前端据此把「约 N 行」改成「最多 N 行」——这个方向是保守的：
	// 可能把实际能用 xlsx 的判成要降级，但绝不会反过来。
	upperBound := c.Query("has_row_filter") == "true"

	// 数值条件同样下推 SQL，必须计入估算——否则设了「最多输出 tokens=0」
	// 明明只会导出几行，弹窗却笃定地显示「预计 15 万行」。
	filter.Charged = queryBool(c, "charged")
	filter.QuotaMin = queryInt(c, "quota_min")
	filter.QuotaMax = queryInt(c, "quota_max")
	filter.PromptTokensMin = queryInt(c, "prompt_tokens_min")
	filter.PromptTokensMax = queryInt(c, "prompt_tokens_max")
	filter.CompletionTokensMin = queryInt(c, "completion_tokens_min")
	filter.CompletionTokensMax = queryInt(c, "completion_tokens_max")
	filter.UseTimeMin = queryInt(c, "use_time_min")
	filter.UseTimeMax = queryInt(c, "use_time_max")

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
		common.ApiSuccess(c, gin.H{"rows": 0, "capped": false, "available": false, "upper_bound": upperBound})
		return
	}
	common.ApiSuccess(c, gin.H{"rows": rows, "capped": capped, "available": true, "upper_bound": upperBound})
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

	// 数值条件：指针类型，0 是有意义的取值（Rule 5）。
	Charged             *bool `json:"charged"`
	QuotaMin            *int  `json:"quota_min"`
	QuotaMax            *int  `json:"quota_max"`
	PromptTokensMin     *int  `json:"prompt_tokens_min"`
	PromptTokensMax     *int  `json:"prompt_tokens_max"`
	CompletionTokensMin *int  `json:"completion_tokens_min"`
	CompletionTokensMax *int  `json:"completion_tokens_max"`
	UseTimeMin          *int  `json:"use_time_min"`
	UseTimeMax          *int  `json:"use_time_max"`
	IsStream            *bool `json:"is_stream"`

	// Mode "detail"（默认）或 "summary"；summary 时 Columns 被忽略，按 SummaryDims 聚合。
	Mode        string   `json:"mode"`
	SummaryDims []string `json:"summary_dims"`

	// 异常条件。
	AnomalyOnly     bool     `json:"anomaly_only"`
	AnomalyKinds    []string `json:"anomaly_kinds"`
	UsageSource     []string `json:"usage_source"`
	StreamEndReason []string `json:"stream_end_reason"`
	SettlementState []string `json:"settlement_state"`
	MinRetryCount   *int     `json:"min_retry_count"`
}

// logExportUsageSources 是 stream_result.usage_source 的闭集，可以校验。
// stream_end_reason / settlement_state 刻意**不校验**——它们随协议演进增加，
// 白名单只会挡住刚出现的那种异常，而那正是最需要被查出来的。
var logExportUsageSources = []string{"upstream", "estimated", "mixed", "none"}

// buildLogExportFilter 把请求体翻译成筛选条件，并做取值校验。
func buildLogExportFilter(req createLogExportJobRequest) (model.LogExportFilter, error) {
	maxValues := operation_setting.GetLogExportSetting().GetMaxFilterValues()
	checkList := func(name string, values []string, allowed []string) error {
		if len(values) > maxValues {
			return &logExportFilterError{key: i18n.MsgLogExportTooManyFilterValues,
				args: map[string]any{"Max": maxValues}}
		}
		if allowed == nil {
			return nil
		}
		for _, v := range values {
			if !slices.Contains(allowed, v) {
				return &logExportFilterError{key: i18n.MsgLogExportInvalidFilterValue,
					args: map[string]any{"Field": name, "Value": v,
						"Allowed": strings.Join(allowed, ", ")}}
			}
		}
		return nil
	}
	if err := checkList("usage_source", req.UsageSource, logExportUsageSources); err != nil {
		return model.LogExportFilter{}, err
	}
	if err := checkList("stream_end_reason", req.StreamEndReason, nil); err != nil {
		return model.LogExportFilter{}, err
	}
	if err := checkList("settlement_state", req.SettlementState, nil); err != nil {
		return model.LogExportFilter{}, err
	}
	if err := checkList("anomaly_kinds", req.AnomalyKinds, model.LogExportAnomalyKinds()); err != nil {
		return model.LogExportFilter{}, err
	}
	if req.QuotaMin != nil && req.QuotaMax != nil && *req.QuotaMin > *req.QuotaMax {
		return model.LogExportFilter{}, &logExportFilterError{key: i18n.MsgLogExportRangeMinGtMax}
	}
	if req.UseTimeMin != nil && req.UseTimeMax != nil && *req.UseTimeMin > *req.UseTimeMax {
		return model.LogExportFilter{}, &logExportFilterError{key: i18n.MsgLogExportRangeMinGtMax}
	}
	if req.PromptTokensMin != nil && req.PromptTokensMax != nil && *req.PromptTokensMin > *req.PromptTokensMax {
		return model.LogExportFilter{}, &logExportFilterError{key: i18n.MsgLogExportRangeMinGtMax}
	}
	if req.CompletionTokensMin != nil && req.CompletionTokensMax != nil && *req.CompletionTokensMin > *req.CompletionTokensMax {
		return model.LogExportFilter{}, &logExportFilterError{key: i18n.MsgLogExportRangeMinGtMax}
	}
	if req.MinRetryCount != nil && *req.MinRetryCount < 0 {
		return model.LogExportFilter{}, &logExportFilterError{key: i18n.MsgLogExportInvalidFilterValue,
			args: map[string]any{"Field": "min_retry_count", "Value": strconv.Itoa(*req.MinRetryCount),
				"Allowed": ">= 0"}}
	}

	return model.LogExportFilter{
		LogType:             req.LogType,
		StartTimestamp:      req.StartTimestamp,
		EndTimestamp:        req.EndTimestamp,
		ModelName:           req.ModelName,
		Username:            req.Username,
		TokenName:           req.TokenName,
		ChannelId:           req.Channel,
		Group:               req.Group,
		Charged:             req.Charged,
		QuotaMin:            req.QuotaMin,
		QuotaMax:            req.QuotaMax,
		PromptTokensMin:     req.PromptTokensMin,
		PromptTokensMax:     req.PromptTokensMax,
		CompletionTokensMin: req.CompletionTokensMin,
		CompletionTokensMax: req.CompletionTokensMax,
		UseTimeMin:          req.UseTimeMin,
		UseTimeMax:          req.UseTimeMax,
		IsStream:            req.IsStream,
		AnomalyOnly:         req.AnomalyOnly,
		AnomalyKinds:        req.AnomalyKinds,
		UsageSource:         req.UsageSource,
		StreamEndReason:     req.StreamEndReason,
		SettlementState:     req.SettlementState,
		MinRetryCount:       req.MinRetryCount,
	}, nil
}

// logExportFilterError 携带 i18n key 与参数的筛选条件校验错误。
type logExportFilterError struct {
	key  string
	args map[string]any
}

func (e *logExportFilterError) Error() string { return e.key }

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

	filters, err := buildLogExportFilter(req)
	if err != nil {
		var filterErr *logExportFilterError
		if errors.As(err, &filterErr) {
			common.ApiErrorI18n(c, filterErr.key, filterErr.args)
			return
		}
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	// 聚合模式走维度校验，不解析列集合——两者是互斥的两种产出形态。
	// 聚合模式（纯汇总或明细+汇总）都要校验维度；both 还会照常解析明细列集。
	needsSummary := req.Mode == model.LogExportModeSummary || req.Mode == model.LogExportModeBoth
	var summaryDims []string
	if needsSummary {
		summaryDims = model.NormalizeLogSummaryDimensions(req.SummaryDims, true)
		if len(summaryDims) == 0 {
			common.ApiErrorI18n(c, i18n.MsgLogExportSummaryNoDimensions)
			return
		}
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
		Filters:  filters,
	}
	if needsSummary {
		job.Mode = req.Mode
		job.SummaryDims = summaryDims
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
	// 异常与数值条件同样要留痕：审计记录的价值就是回答「谁导了什么」，
	// 只记前六个字段的话，最关键的筛选范围恰好是缺的那部分。
	addInt := func(name string, v *int) {
		if v != nil {
			parts = append(parts, name+"="+strconv.Itoa(*v))
		}
	}
	addInt("quota_min", filter.QuotaMin)
	addInt("quota_max", filter.QuotaMax)
	addInt("prompt_tokens_min", filter.PromptTokensMin)
	addInt("completion_tokens_min", filter.CompletionTokensMin)
	addInt("completion_tokens_max", filter.CompletionTokensMax)
	addInt("prompt_tokens_max", filter.PromptTokensMax)
	addInt("use_time_min", filter.UseTimeMin)
	addInt("use_time_max", filter.UseTimeMax)
	addInt("min_retry_count", filter.MinRetryCount)
	if filter.IsStream != nil {
		parts = append(parts, "is_stream="+strconv.FormatBool(*filter.IsStream))
	}
	if filter.Charged != nil {
		parts = append(parts, "charged="+strconv.FormatBool(*filter.Charged))
	}
	if filter.AnomalyOnly {
		parts = append(parts, "anomaly=any")
	}
	addList := func(name string, values []string) {
		if len(values) > 0 {
			parts = append(parts, name+"="+strings.Join(values, "|"))
		}
	}
	addList("anomaly", filter.AnomalyKinds)
	addList("usage_source", filter.UsageSource)
	addList("stream_end_reason", filter.StreamEndReason)
	addList("settlement_state", filter.SettlementState)
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
	//
	// 聚合模式不参与这个判断：est_rows 估的是**要扫描的日志行数**，而聚合的产出是
	// 维度组合数——扫 100 万行可能只出 50 行汇总。拿扫描量去否决 xlsx，
	// 会让所有大范围的汇总导出都拿不到 Excel 文件，而那恰恰是最该给 Excel 的场景。
	if format == model.LogExportFormatXlsx && req.Mode != model.LogExportModeSummary {
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

// logExportPartFileName 生成下载时呈现给用户的文件名。
//
// 汇总分片必须用不同的名字：「明细 + 汇总」模式下两种分片同在一个压缩包里，
// 全叫 part-000N 的话，拿到文件的人得逐个解开才知道哪个是哪个。
func logExportPartFileName(job *model.LogExportJob, part *model.LogExportPart) string {
	ext := ".csv.gz"
	if job.Format == model.LogExportFormatXlsx {
		ext = ".xlsx"
	}
	kind := "part"
	if part.Kind == model.LogExportPartKindSummary {
		kind = "summary"
	}
	stamp := time.Unix(job.CreatedAt, 0).Format("20060102-150405")
	return fmt.Sprintf("log-export-%s-%s-%04d%s", stamp, kind, part.Index, ext)
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

	filename := logExportPartFileName(job, part)
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
			Name:   logExportPartFileName(job, &part),
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

// queryInt 读取可选的整数查询参数。返回 nil 表示「未传」——不能退化成 0，
// 0 在这些条件里是有意义的取值（quota_max=0 就是「只看零费用的行」）。
func queryInt(c *gin.Context, key string) *int {
	raw := c.Query(key)
	if raw == "" {
		return nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &v
}

// queryBool 读取可选的布尔查询参数，语义同 queryInt。
func queryBool(c *gin.Context, key string) *bool {
	raw := c.Query(key)
	if raw == "" {
		return nil
	}
	v := raw == "true" || raw == "1"
	return &v
}
