package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"

	"gorm.io/gorm"
)

func applyExplicitLogTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if strings.Contains(value, "%") {
		condition, pattern, err := buildLogLikeCondition(column, value)
		if err != nil {
			return nil, err
		}
		return tx.Where(condition, pattern), nil
	}
	return tx.Where(column+" = ?", value), nil
}

func buildLogLikeCondition(column string, value string) (string, string, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		pattern, err := sanitizeClickHouseLikePattern(value)
		if err != nil {
			return "", "", err
		}
		return column + " LIKE ?", pattern, nil
	}

	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return "", "", err
	}
	return column + " LIKE ? ESCAPE '!'", pattern, nil
}

func sanitizeClickHouseLikePattern(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, `\\`)
	input = strings.ReplaceAll(input, `_`, `\_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}
	return input, nil
}

type Log struct {
	Id                int    `json:"id" gorm:"index:idx_created_at_id,priority:2;index:idx_user_id_id,priority:2;index:idx_log_export_user_time,priority:3"`
	UserId            int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1;index:idx_log_export_user_time,priority:1"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:1;index:idx_created_at_type;index:idx_log_export_user_time,priority:2"`
	Type              int    `json:"type" gorm:"index:idx_created_at_type"`
	Content           string `json:"content"`
	Username          string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName         string `json:"token_name" gorm:"index;default:''"`
	ModelName         string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota             int    `json:"quota" gorm:"default:0"`
	PromptTokens      int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens  int    `json:"completion_tokens" gorm:"default:0"`
	UseTime           int    `json:"use_time" gorm:"default:0"`
	IsStream          bool   `json:"is_stream"`
	ChannelId         int    `json:"channel" gorm:"index"`
	ChannelName       string `json:"channel_name" gorm:"->"`
	TokenId           int    `json:"token_id" gorm:"default:0;index"`
	Group             string `json:"group" gorm:"index"`
	Ip                string `json:"ip" gorm:"index;default:''"`
	RequestId         string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);index:idx_logs_upstream_request_id;default:''"`
	Other             string `json:"other"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
	LogTypeLogin   = 7
)

func ensureLogRequestId(log *Log) {
	if log != nil && log.RequestId == "" {
		log.RequestId = common.NewRequestId()
	}
}

func createLog(log *Log) error {
	ensureLogRequestId(log)
	return LOG_DB.Create(log).Error
}

func clickHouseLogOrder(prefix string) string {
	return prefix + "created_at desc, " + prefix + "request_id desc"
}

func assignDisplayLogIds(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].Id = startIdx + i + 1
	}
}

// formatUserLogs 生成日志所有者可见的视图：other 按上游的用户可见性裁剪（admin/root/audit 信息与
// channel_id/channel_name/channel_type/reject_reason 等历史敏感键一律去掉），再按错误提示配置改写。
// 参数 logs：待原地更新的日志切片；startIdx：当前页起始序号，用于生成展示用日志编号。
func formatUserLogs(logs []*Log, startIdx int) {
	masking := operation_setting.RelayErrorDisplayEnabled()
	// The built-in fallback text follows the user's language. All logs here
	// belong to one user; resolved once, on the first log that is checked.
	lang, langLoaded := "", false
	langOf := func(userId int) string {
		if !langLoaded {
			lang, langLoaded = userLogLanguage(userId), true
		}
		return lang
	}
	for i := range logs {
		logs[i].ChannelName = ""
		// Read before the projection removes admin_info, where it is kept.
		var streamInput *operation_setting.RelayErrorInput
		if masking {
			streamInput = storedStreamTerminalInput(logs[i])
		}
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityUser)
		if masking {
			maskProjectedLogForUser(logs[i], streamInput, langOf)
		}
	}
	assignDisplayLogIds(logs, startIdx)
}

// maskProjectedLogForUser applies the relay error display configuration to one
// log that has already been projected for its owner. streamInput is the
// terminal-frame input a stream-failure error log recorded
// (storedStreamTerminalInput, read from the log before projection; nil when it
// has none). other is re-encoded only when a value in it was rewritten, and
// then only that value changes: the others keep their raw JSON.
func maskProjectedLogForUser(log *Log, streamInput *operation_setting.RelayErrorInput, langOf func(userId int) string) {
	if log.Type != LogTypeError && !strings.Contains(log.Other, `"stream_status"`) {
		return
	}
	fields := parseLogOtherFields(log.Other)
	changed := false
	if log.Type == LogTypeError {
		changed = maskErrorLogForUser(log, fields, streamInput, langOf)
	} else {
		changed = maskStreamStatusForUser(fields, log.UserId, langOf)
	}
	if changed {
		log.Other = fields.encode()
	}
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	order := "id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order(order).Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

// logTraceMaxDuplicates bounds how many rows sharing one request ID are read.
// Request IDs are expected to be unique; this only caps legacy duplicates.
const logTraceMaxDuplicates = 50

// logTraceByRequestIdQuery 不在数据库端排序：PostgreSQL 面对
// `WHERE request_id = ? ORDER BY created_at DESC LIMIT 1` 会倒序扫描 idx_created_at_type
// 并逐行过滤（240 万行实测约 40s），而不走 idx_logs_request_id（约 2ms）。
func logTraceByRequestIdQuery(db *gorm.DB, requestId string) *gorm.DB {
	return db.Model(&Log{}).
		Select("id, request_id, upstream_request_id, channel_id, type, other, created_at").
		Where("request_id = ?", requestId).
		Limit(logTraceMaxDuplicates)
}

// GetLogTraceByRequestId returns only the trusted routing fields needed to
// resolve an administrator-initiated upstream log lookup. A retried request
// has one row per failed attempt plus its final row; the trace must resolve to
// the final attempt: newest created_at, then the consume row, then id.
//
// Id order alone does not follow response order: under backlog the async
// pipeline flushes consume logs before error logs, and retry-buffer and
// fallback-replay inserts land late too, so an earlier attempt's error log can
// get a higher id than the final consume log within the same second. Only the
// final attempt writes a consume log, so it wins such a tie. Rows written after
// that consume log (a relay-timeout error log for the same attempt, a
// violation-fee consume log) carry the same channel, so either choice routes
// the lookup to the right upstream.
func GetLogTraceByRequestId(requestId string) (*Log, error) {
	var rows []Log
	if err := logTraceByRequestIdQuery(LOG_DB, requestId).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	latest := rows[0]
	for _, row := range rows[1:] {
		if logTraceNewer(row, latest) {
			latest = row
		}
	}
	return &latest, nil
}

func logTraceNewer(a, b Log) bool {
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt > b.CreatedAt
	}
	aConsume, bConsume := a.Type == LogTypeConsume, b.Type == LogTypeConsume
	if aConsume != bConsume {
		return aConsume
	}
	return a.Id > b.Id
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// RecordLogWithAdminInfo 记录操作日志，并将管理员相关信息存入 Other.admin_info，
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo map[string]interface{}) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if len(adminInfo) > 0 {
		other := map[string]interface{}{
			"admin_info": adminInfo,
		}
		log.Other = common.MapToJsonStr(other)
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// buildOpField 构建语言无关的操作描述（写入 Other.op）。
// 前端依据 action(稳定操作标识) + params(结构化参数) 在渲染期用 i18n 本地化展示，
// 因此不在数据库中存储自然语言句子。
func buildOpField(action string, params map[string]interface{}) map[string]interface{} {
	op := map[string]interface{}{
		"action": action,
	}
	if len(params) > 0 {
		op["params"] = params
	}
	return op
}

// RecordLoginLog 记录用户登录成功的审计日志（type=LogTypeLogin）。
// username 由调用方传入（登录流程已持有用户对象），避免额外的数据库查询。
// content 为英文兜底文本（用于导出）；action+params 供前端本地化渲染。
// extra 可携带 login_method、user_agent 等附加信息（普通用户可见）。
func RecordLoginLog(userId int, username string, content string, ip string, action string, params map[string]interface{}, extra map[string]interface{}) {
	other := map[string]interface{}{}
	for k, v := range extra {
		other[k] = v
	}
	other["op"] = buildOpField(action, params)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeLogin,
		Content:   content,
		Ip:        ip,
		Other:     common.MapToJsonStr(other),
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record login log: " + err.Error())
	}
}

// RecordOperationAuditLog 记录管理/高危操作审计日志（type=LogTypeManage）。
// logUserId 为日志归属者，管理审计日志应归属实际操作者；目标资源/用户放入
// action params。username 内部按 logUserId 查询。content 为英文兜底文本（供导出使用）。
// action+params 写入 Other.op，供前端本地化渲染（普通用户可见，不含敏感信息）。
// adminInfo 存放操作者身份（写入 Other.admin_info，普通用户查询时剥离）；
// auditInfo 存放路由/方法/结果等中间件兜底信息（写入 Other.audit_info，普通用户查询时剥离）。
func RecordOperationAuditLog(logUserId int, content string, ip string, action string, params map[string]interface{}, adminInfo map[string]interface{}, auditInfo map[string]interface{}) {
	username, _ := GetUsernameById(logUserId, false)
	other := map[string]interface{}{
		"op": buildOpField(action, params),
	}
	if len(adminInfo) > 0 {
		other["admin_info"] = adminInfo
	}
	if len(auditInfo) > 0 {
		other["audit_info"] = auditInfo
	}
	log := &Log{
		UserId:    logUserId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeManage,
		Content:   content,
		Ip:        ip,
		Other:     common.MapToJsonStr(other),
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record operation audit log: " + err.Error())
	}
}

func RecordTopupLog(userId int, content string, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	adminInfo := map[string]interface{}{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	}
	other := map[string]interface{}{
		"admin_info": adminInfo,
	}
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Ip:        callerIp,
		Other:     common.MapToJsonStr(other),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record topup log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, common.LocalLogPreview(content)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := common.MapToJsonStr(other)
	// 判断是否需要记录 IP
	needRecordIp := contextWantsIPLog(c)
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	_ = enqueueAsyncRelayLog(relayLogKindError, log, nil, nil)
}

// EnqueueConsumeLog builds and non-blockingly appends a consume log to the
// bounded relay-log buffer. continuation is executed by a fixed background
// worker after the INSERT has produced the existing logs.id.
func EnqueueConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams, accounting *RelayLogAccountingPayload) bool {
	if !common.LogConsumeEnabled {
		dispatchRelayLogContinuation(&relayLogEvent{Accounting: accounting}, 0)
		return true
	}
	logEntry := buildConsumeLog(c, userId, params)
	dataExport := common.DataExportEnabled
	quotaData := QuotaDataLogParams{
		UserID: userId, Username: logEntry.Username, ModelName: params.ModelName, Quota: params.Quota,
		CreatedAt: logEntry.CreatedAt, TokenUsed: params.PromptTokens + params.CompletionTokens,
		UseGroup: params.Group, TokenID: params.TokenId, ChannelID: params.ChannelId, NodeName: common.NodeName,
	}
	var quotaDataPtr *QuotaDataLogParams
	if dataExport {
		quotaDataPtr = &quotaData
	}
	return enqueueAsyncRelayLog(relayLogKindConsume, logEntry, accounting, quotaDataPtr)
}

type RecordConsumeLogParams struct {
	ChannelId        int                    `json:"channel_id"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	Quota            int                    `json:"quota"`
	Content          string                 `json:"content"`
	TokenId          int                    `json:"token_id"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	Other            map[string]interface{} `json:"other"`
	CreatedAt        int64                  `json:"created_at"`
}

// RecordConsumeLog 记录消费日志，返回插入的 log.Id（失败时返回 0）。
func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) int {
	if !common.LogConsumeEnabled {
		return 0
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	logEntry := buildConsumeLog(c, userId, params)
	username := logEntry.Username
	createdAt := logEntry.CreatedAt
	err := createLog(logEntry)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
		return 0
	}
	if common.DataExportEnabled {
		LogQuotaData(QuotaDataLogParams{
			UserID: userId, Username: username, ModelName: params.ModelName, Quota: params.Quota,
			CreatedAt: createdAt, TokenUsed: params.PromptTokens + params.CompletionTokens,
			UseGroup: params.Group, TokenID: params.TokenId, ChannelID: params.ChannelId, NodeName: common.NodeName,
		})
	}
	return logEntry.Id
}

func buildConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) *Log {
	createdAt := params.CreatedAt
	if createdAt == 0 {
		createdAt = common.GetTimestamp()
	}
	needRecordIp := contextWantsIPLog(c)
	return &Log{
		UserId:           userId,
		Username:         c.GetString("username"),
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         c.GetString(common.RequestIdKey),
		UpstreamRequestId: c.GetString(common.UpstreamRequestIdKey),
		Other:             common.MapToJsonStr(params.Other),
	}
}

// contextWantsIPLog uses only the user-setting snapshot installed by auth
// middleware. It must never fall back to the main database on the relay path.
func contextWantsIPLog(c *gin.Context) bool {
	if c == nil {
		return false
	}
	settingMap, ok := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting)
	return ok && settingMap.RecordIpLog
}

func GetChannelNameSnapshotsFromLogs(ids []int) map[int]string {
	return GetChannelNameSnapshotsFromLogsWithContext(context.Background(), ids)
}

func GetChannelNameSnapshotsFromLogsWithContext(ctx context.Context, ids []int) map[int]string {
	result := make(map[int]string, len(ids))
	uniq := make([]int, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return result
	}

	var logs []Log
	err := LOG_DB.WithContext(safeDBContext(ctx)).Model(&Log{}).
		Select("channel_id, other").
		Where("channel_id IN ? AND other LIKE ?", uniq, "%channel_name%").
		Order("created_at desc, id desc").
		Limit(len(uniq) * 20).
		Find(&logs).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to get channel name snapshots from logs: channel_ids=%v, error=%v", uniq, err))
		return result
	}
	for _, log := range logs {
		if result[log.ChannelId] != "" {
			continue
		}
		otherMap, _ := common.StrToMap(log.Other)
		if otherMap == nil {
			continue
		}
		if name, ok := otherMap["channel_name"].(string); ok {
			name = strings.TrimSpace(name)
			if name != "" {
				result[log.ChannelId] = name
			}
		}
	}
	return result
}

type RecordTaskBillingLogParams struct {
	UserId    int
	LogType   int
	Content   string
	ChannelId int
	ModelName string
	Quota     int
	TokenId   int
	Group     string
	Other     map[string]interface{}
	NodeName  string // 任务发起节点；为空时回退当前节点
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) int {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return 0
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	createdAt := common.GetTimestamp()
	log := &Log{
		UserId:    params.UserId,
		Username:  username,
		CreatedAt: createdAt,
		Type:      params.LogType,
		Content:   params.Content,
		TokenName: tokenName,
		ModelName: params.ModelName,
		Quota:     params.Quota,
		ChannelId: params.ChannelId,
		TokenId:   params.TokenId,
		Group:     params.Group,
		Other:     common.MapToJsonStr(params.Other),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record task billing log: " + err.Error())
		return 0
	}
	if params.LogType == LogTypeConsume && common.DataExportEnabled {
		nodeName := params.NodeName
		if nodeName == "" {
			nodeName = common.NodeName
		}
		LogQuotaData(QuotaDataLogParams{
			UserID:    params.UserId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  nodeName,
		})
	}
	return log.Id
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, logId int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("logs.type = ?", logType)
	}

	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, 0, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.username", username); err != nil {
		return nil, 0, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if logId > 0 {
		tx = tx.Where("logs.id = ?", logId)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	// 无筛选（类型除外）时走小时缓存分解：命中后只需扫两端各 ≤1 小时。
	// 总数仍然精确，翻页深度不受影响。带筛选时回落原查询。
	hasNonTypeFilter := modelName != "" || username != "" || tokenName != "" ||
		channel != 0 || logId > 0 || group != "" || requestId != "" || upstreamRequestId != ""
	total, err = countLogsWithHourCache(logType, startTimestamp, endTimestamp, hasNonTypeFilter,
		func() *gorm.DB { return tx.Model(&Log{}) })
	if err != nil {
		return nil, 0, err
	}
	order := "logs.created_at desc, logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, startIdx)
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

// ExportLogs 流式导出日志：按时间段及过滤条件分批回调，避免一次性加载全部数据进内存。
// userId > 0 时只导出该用户的日志（普通用户自助导出场景）。
// fn 在每批数据上被调用，返回 error 会中断后续查询。
func ExportLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string,
	tokenName string, channel int, group string, userId int, fn func(batch []*Log) error) error {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("logs.type = ?", logType)
	}

	var err error
	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.username", username); err != nil {
		return err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	if userId > 0 {
		tx = tx.Where("logs.user_id = ?", userId)
	}

	var batch []*Log
	// FindInBatches 跨 SQLite/MySQL/PostgreSQL 通用，分批查询避免内存溢出。
	// 注意：不能附加自定义 Order——FindInBatches 内部按主键升序做游标翻页
	// （WHERE id > 上一批末行主键）。自定义排序会破坏游标导致漏数据/重复数据。
	// 主键 id 自增，升序即近似时间顺序（旧→新）。
	return tx.Model(&Log{}).
		FindInBatches(&batch, 1000, func(_ *gorm.DB, _ int) error {
			return fn(batch)
		}).Error
}

type EmployeeCustomerLogFilter struct {
	EmployeeUserId    int
	CustomerUserId    int
	LogType           int
	StartTimestamp    int64
	EndTimestamp      int64
	ModelName         string
	Username          string
	TokenName         string
	Channel           int
	LogId             int
	Group             string
	RequestId         string
	UpstreamRequestId string
	StartIdx          int
	PageSize          int
}

func getEmployeeCustomerLogUserIds(employeeUserId, customerUserId int) ([]int, error) {
	if employeeUserId <= 0 {
		return nil, nil
	}
	userIds := make([]int, 0, 8)
	if customerUserId <= 0 {
		userIds = append(userIds, employeeUserId)
	}
	customerUserIds := make([]int, 0)
	tx := DB.Model(&User{}).
		Where("inviter_id = ? AND role = ?", employeeUserId, common.RoleCommonUser).
		Where("id NOT IN (?)", DB.Model(&EmployeeProfile{}).Select("user_id").Where("status = ?", CustomerStatusEnabled))
	if customerUserId > 0 {
		tx = tx.Where("id = ?", customerUserId)
	}
	if err := tx.Pluck("id", &customerUserIds).Error; err != nil {
		return nil, err
	}
	userIds = append(userIds, customerUserIds...)
	return userIds, nil
}

func applyEmployeeCustomerLogScope(tx *gorm.DB, employeeUserId, customerUserId int) *gorm.DB {
	if customerUserId <= 0 {
		return tx.Joins("LEFT JOIN users ON users.id = logs.user_id").
			Joins("LEFT JOIN employee_profiles ep ON ep.user_id = users.id AND ep.status = ?", CustomerStatusEnabled).
			Where("logs.user_id = ? OR (users.inviter_id = ? AND users.role = ? AND users.deleted_at IS NULL AND ep.user_id IS NULL)", employeeUserId, employeeUserId, common.RoleCommonUser)
	}
	tx = tx.Joins("JOIN users ON users.id = logs.user_id").
		Joins("LEFT JOIN employee_profiles ep ON ep.user_id = users.id AND ep.status = ?", CustomerStatusEnabled).
		Where("users.inviter_id = ? AND users.role = ? AND users.deleted_at IS NULL AND ep.user_id IS NULL", employeeUserId, common.RoleCommonUser)
	return tx.Where("logs.user_id = ?", customerUserId)
}

func applyEmployeeCustomerLogUserScope(tx *gorm.DB, filter EmployeeCustomerLogFilter) (*gorm.DB, bool, error) {
	if LOG_DB == DB {
		return applyEmployeeCustomerLogScope(tx, filter.EmployeeUserId, filter.CustomerUserId), false, nil
	}
	userIds, err := getEmployeeCustomerLogUserIds(filter.EmployeeUserId, filter.CustomerUserId)
	if err != nil {
		return nil, false, err
	}
	if len(userIds) == 0 {
		return tx, true, nil
	}
	return tx.Where("logs.user_id IN ?", userIds), false, nil
}

func applyEmployeeCustomerLogFilters(tx *gorm.DB, filter EmployeeCustomerLogFilter) (*gorm.DB, error) {
	var err error
	if filter.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", filter.LogType)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", filter.ModelName); err != nil {
		return nil, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.username", filter.Username); err != nil {
		return nil, err
	}
	if filter.TokenName != "" {
		tx = tx.Where("logs.token_name = ?", filter.TokenName)
	}
	if filter.RequestId != "" {
		tx = tx.Where("logs.request_id = ?", filter.RequestId)
	}
	if filter.UpstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", filter.UpstreamRequestId)
	}
	if filter.StartTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", filter.EndTimestamp)
	}
	if filter.Channel != 0 {
		tx = tx.Where("logs.channel_id = ?", filter.Channel)
	}
	if filter.LogId > 0 {
		tx = tx.Where("logs.id = ?", filter.LogId)
	}
	if filter.Group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", filter.Group)
	}
	return tx, nil
}

// employeeHiddenLogOtherKeys are the top-level other keys the employee view of
// customer logs removes: the channel name (error logs written before it left
// other still carry it) and the root-only and audit scopes. admin_info stays:
// the employee log page shows the admin fields (retry chain, channel affinity,
// multi-key index), and employees read errors unmasked by design, which
// admin_info.stream_error* only repeats.
var employeeHiddenLogOtherKeys = []string{"channel_name", logOtherRootInfoKey, logOtherAuditInfoKey}

// employeeHiddenAdminInfoKeys are admin_info keys the employee view removes
// although it keeps admin_info: timeout_absorbed is the platform's cost of a
// timed-out request, an internal figure (relay-timeout-cost-bearing.md §4).
var employeeHiddenAdminInfoKeys = []string{"timeout_absorbed"}

// employeeHiddenLogOtherMarkers is the cheap substring pre-check for both lists.
var employeeHiddenLogOtherMarkers = append(append([]string{}, employeeHiddenLogOtherKeys...), employeeHiddenAdminInfoKeys...)

// formatEmployeeLogs 生成员工查看客户日志的视图：抹掉渠道名称（员工只看渠道编号定位问题，
// 供应渠道的名称属于内部信息）、仅 root / 审计可见的 root_info、audit_info，以及 admin_info 里的
// 平台成本字段（timeout_absorbed）。
// 其余 other 值保持原始 JSON；无须去掉任何键时 other 原样返回，无法解析且含这些键时返回 {}。
func formatEmployeeLogs(logs []*Log) {
	for i := range logs {
		logs[i].ChannelName = ""
		hidden := false
		for _, key := range employeeHiddenLogOtherMarkers {
			if strings.Contains(logs[i].Other, `"`+key+`"`) {
				hidden = true
				break
			}
		}
		if !hidden {
			continue
		}
		fields := parseLogOtherFields(logs[i].Other)
		if fields == nil {
			logs[i].Other = "{}"
			continue
		}
		changed := false
		for _, key := range employeeHiddenLogOtherKeys {
			if _, ok := fields[key]; ok {
				delete(fields, key)
				changed = true
			}
		}
		if adminInfo := fields.object(logOtherAdminInfoKey); adminInfo != nil {
			adminChanged := false
			for _, key := range employeeHiddenAdminInfoKeys {
				if _, ok := adminInfo[key]; ok {
					delete(adminInfo, key)
					adminChanged = true
				}
			}
			if adminChanged {
				if !fields.set(logOtherAdminInfoKey, adminInfo) {
					delete(fields, logOtherAdminInfoKey)
				}
				changed = true
			}
		}
		if changed {
			logs[i].Other = fields.encode()
		}
	}
}

func GetEmployeeCustomerLogs(filter EmployeeCustomerLogFilter) (logs []*Log, total int64, err error) {
	tx, empty, err := applyEmployeeCustomerLogUserScope(LOG_DB.Model(&Log{}), filter)
	if err != nil {
		return nil, 0, err
	}
	if empty {
		return []*Log{}, 0, nil
	}
	if tx, err = applyEmployeeCustomerLogFilters(tx, filter); err != nil {
		return nil, 0, err
	}
	if err = tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = tx.Order("logs.created_at desc, logs.id desc").Limit(filter.PageSize).Offset(filter.StartIdx).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	formatEmployeeLogs(logs)
	return logs, total, nil
}

const logSearchCountLimit = 10000

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, logId int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("logs.user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("logs.user_id = ? and logs.type = ?", userId, logType)
	}

	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, 0, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if logId > 0 {
		tx = tx.Where("logs.id = ?", logId)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError("failed to count user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError("failed to search user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}

	formatUserLogs(logs, startIdx)
	return logs, total, err
}

type Stat struct {
	Quota int `json:"quota"`
	Rpm   int `json:"rpm"`
	Tpm   int `json:"tpm"`
}

// ChannelGroupConsumption 按「渠道 + 分组」聚合的消费额度，用于平台级成本/利润估算。
type ChannelGroupConsumption struct {
	ChannelId int    `json:"channel_id" gorm:"column:channel_id"`
	GroupName string `json:"group_name" gorm:"column:group_name"`
	Quota     int64  `json:"quota" gorm:"column:quota"`
}

// GetConsumptionByChannelGroup 返回时间范围内、按渠道与分组聚合的消费额度（仅消费类日志）。
// 跨库安全：仅使用标准 SUM/GROUP BY，分组列通过 logGroupCol 处理保留字引号差异。
func GetConsumptionByChannelGroup(startTimestamp, endTimestamp int64) ([]ChannelGroupConsumption, error) {
	var rows []ChannelGroupConsumption
	tx := LOG_DB.Table("logs").
		Select("channel_id as channel_id, "+logGroupCol+" as group_name, COALESCE(SUM(quota),0) as quota").
		Where("type = ?", LogTypeConsume)
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	tx = tx.Group("channel_id, " + logGroupCol)
	err := tx.Scan(&rows).Error
	return rows, err
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (stat Stat, err error) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(quota), 0) quota")

	// 为rpm和tpm创建单独的查询
	rpmTpmQuery := LOG_DB.Table("logs").Select("count(*) rpm, COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0) tpm")

	if tx, err = applyExplicitLogTextFilter(tx, "username", username); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "username", username); err != nil {
		return stat, err
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
		rpmTpmQuery = rpmTpmQuery.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "model_name", modelName); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "model_name", modelName); err != nil {
		return stat, err
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
		rpmTpmQuery = rpmTpmQuery.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	tx = tx.Where("type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 额度是整个区间的聚合，无筛选时走小时缓存分解：命中后只需扫两端各 ≤1 小时。
	// rpm/tpm 只看最近 60 秒，本来就便宜，维持实时查询。
	// 注意这里不看 logType：本函数历来恒按 LogTypeConsume 统计（见下方 where），
	// 而缓存里的 Quota 也只累加消费类日志，两者口径一致。
	hasNonTypeFilter := username != "" || tokenName != "" || modelName != "" ||
		channel != 0 || group != ""
	cachedQuota, quotaFromCache := sumConsumeQuotaWithHourCache(startTimestamp, endTimestamp, hasNonTypeFilter)

	// 执行查询。GORM v1.25.12 起 Scan 会清零目标结构体中未匹配的字段，
	// 因此 rpm/tpm 必须扫进独立结构体再合并，否则会把已取到的 quota 覆盖为 0。
	// 回落到整段 SUM 时同样挂超时：那条查询要扫全区间，是最可能长时间占住
	// LOG_DB 连接的一条，而连接占用正是本次改造要解决的问题。
	statCtx, cancelStat := logStatQueryContext()
	defer cancelStat()
	if quotaFromCache {
		stat.Quota = int(cachedQuota)
	} else if err := tx.WithContext(statCtx).Scan(&stat).Error; err != nil {
		common.SysError("failed to query log stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	var rpmTpm Stat
	if err := rpmTpmQuery.Scan(&rpmTpm).Error; err != nil {
		common.SysError("failed to query rpm/tpm stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	stat.Rpm = rpmTpm.Rpm
	stat.Tpm = rpmTpm.Tpm

	return stat, nil
}

func SumEmployeeCustomerUsedQuota(filter EmployeeCustomerLogFilter) (stat Stat, err error) {
	tx, empty, err := applyEmployeeCustomerLogUserScope(LOG_DB.Table("logs").Select("sum(logs.quota) quota"), filter)
	if err != nil {
		return stat, err
	}
	if empty {
		return stat, nil
	}
	rpmTpmQuery, empty, err := applyEmployeeCustomerLogUserScope(LOG_DB.Table("logs").Select("count(*) rpm, sum(logs.prompt_tokens) + sum(logs.completion_tokens) tpm"), filter)
	if err != nil {
		return stat, err
	}
	if empty {
		return stat, nil
	}

	filter.LogType = LogTypeUnknown
	if tx, err = applyEmployeeCustomerLogFilters(tx, filter); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyEmployeeCustomerLogFilters(rpmTpmQuery, filter); err != nil {
		return stat, err
	}

	tx = tx.Where("logs.type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("logs.type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("logs.created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	if err := tx.Scan(&stat).Error; err != nil {
		common.SysError("failed to query employee customer log stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	// GORM v1.25.12 起 Scan 会清零未匹配字段，rpm/tpm 需扫进独立结构体再合并。
	var rpmTpm Stat
	if err := rpmTpmQuery.Scan(&rpmTpm).Error; err != nil {
		common.SysError("failed to query employee customer rpm/tpm stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	stat.Rpm = rpmTpm.Rpm
	stat.Tpm = rpmTpm.Tpm
	return stat, nil
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func CountOldLog(ctx context.Context, targetTimestamp int64) (int64, error) {
	var total int64
	if err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func DeleteOldLogBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if nil != ctx.Err() {
		return 0, ctx.Err()
	}

	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// ClickHouse DELETE is a heavy mutation that rewrites data parts, so
		// per-batch mutations would be pathologically slow. Remove all matching
		// rows in a single synchronous mutation regardless of limit; the reported
		// count lets the caller's progress loop complete in one pass.
		total, err := CountOldLog(ctx, targetTimestamp)
		if err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, nil
		}
		if err := LOG_DB.WithContext(ctx).Exec(
			"ALTER TABLE logs DELETE WHERE created_at < ? SETTINGS mutations_sync = 1",
			targetTimestamp,
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	result := LOG_DB.WithContext(ctx).Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
	if nil != result.Error {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// userLogLanguage is the user's language for the fallback text; a seam for tests.
var userLogLanguage = GetUserLanguage

// MaskErrorLogContentForUser returns an error log's content as its owner may
// see it, for views outside formatUserLogs (the self-service export). lang is
// the viewer's language, for the built-in fallback text.
func MaskErrorLogContentForUser(log *Log, lang string) string {
	if log == nil || log.Type != LogTypeError || !operation_setting.RelayErrorDisplayEnabled() {
		if log == nil {
			return ""
		}
		return log.Content
	}
	masked := *log
	maskErrorLogForUser(&masked, parseLogOtherFields(log.Other), storedStreamTerminalInput(log), func(int) string { return lang })
	return masked.Content
}

// storedStreamTerminalInput returns the error code and text the terminal frame
// of a failed stream was decided with, which its settlement error log keeps in
// admin_info (service.FinalizeConsumptionSettlement); nil for any other log and
// for rows written before it was recorded. log.Other must not be projected yet:
// the user projection removes admin_info.
func storedStreamTerminalInput(log *Log) *operation_setting.RelayErrorInput {
	if log == nil || log.Type != LogTypeError || !strings.Contains(log.Other, `"`+operation_setting.RelayStreamMatchCodeKey+`"`) {
		return nil
	}
	adminInfo := parseLogOtherFields(log.Other).object(logOtherAdminInfoKey)
	if adminInfo == nil {
		return nil
	}
	code, codeOK := rawJSONString(adminInfo[operation_setting.RelayStreamMatchCodeKey])
	message, messageOK := rawJSONString(adminInfo[operation_setting.RelayStreamMatchMessageKey])
	if !codeOK || !messageOK {
		return nil
	}
	truncated := string(adminInfo[operation_setting.RelayStreamMatchTruncatedKey]) == "true"
	return &operation_setting.RelayErrorInput{ErrorCode: code, Message: message, Truncated: truncated}
}

// isStreamFailureErrorLog reports whether an error log is the settlement of a
// stream that failed after its headers went out with nothing to charge
// (service.FinalizeConsumptionSettlement). Such a row has no error type; the
// settlement records error_code upstream_stream_error, or relay_timeout when our
// own time limit cut the stream. Rows written before it recorded a code are
// recognised by a stream_result that failed for a reason other than the client
// leaving.
func isStreamFailureErrorLog(fields logOtherFields, errorType, errorCode string) bool {
	if errorType != "" {
		return false
	}
	switch errorCode {
	case operation_setting.RelayStreamErrorCode, operation_setting.RelayTimeoutErrorCode:
		return true
	case "":
	default:
		return false
	}
	var result struct {
		Failed     bool `json:"failed"`
		ClientGone bool `json:"client_gone"`
	}
	raw, ok := fields["stream_result"]
	if !ok || common.GetJsonType(raw) != "object" || common.Unmarshal(raw, &result) != nil {
		return false
	}
	return result.Failed && !result.ClientGone
}

// maskErrorLogForUser applies the relay error display configuration to an error
// log a user is about to see. The stored row keeps the original for admins; it
// is rewritten at read time so rule changes also cover past logs. streamInput
// is the stream-failure row's recorded terminal-frame input
// (storedStreamTerminalInput), or nil. Reports whether fields was changed.
func maskErrorLogForUser(log *Log, fields logOtherFields, streamInput *operation_setting.RelayErrorInput, langOf func(userId int) string) bool {
	errorType, errorCode := fields.str("error_type"), fields.str("error_code")
	in := operation_setting.RelayErrorInput{
		Upstream:   operation_setting.IsUpstreamRelayErrorKind(errorType, errorCode),
		StatusCode: fields.integer("status_code"),
		ErrorCode:  errorCode,
		Message:    log.Content,
	}
	streamFailure := isStreamFailureErrorLog(fields, errorType, errorCode)
	if streamFailure {
		// Decided as the stream's terminal frame was: the upstream's error, or our
		// own time limit (error_code relay_timeout). The status was already sent,
		// so a rule's status override did not reach the client and is not shown.
		in = operation_setting.StreamFailureRelayErrorInput(errorCode == operation_setting.RelayTimeoutErrorCode, log.Content)
		if streamInput != nil {
			// The frame's own error code and text, so rules keyed on them decide
			// the log exactly as they decided the frame. Older rows match with
			// the stream-failure code and the whole content.
			in.ErrorCode, in.Message, in.Truncated = streamInput.ErrorCode, streamInput.Message, streamInput.Truncated
		}
	}
	in.Lang = langOf(log.UserId)
	content, statusCode, replaced := operation_setting.MaskRelayErrorLogInputForUser(in, log.RequestId)
	if !replaced {
		return false
	}
	log.Content = content
	if fields == nil {
		return false
	}
	changed := false
	// The client got the rule's status; the upstream's own one is part of what
	// the override hides.
	if _, ok := fields["status_code"]; ok && statusCode > 0 && !streamFailure {
		changed = fields.set("status_code", statusCode) || changed
	}
	if in.Upstream && !streamFailure {
		// The upstream's own code would still tell the user what the upstream is.
		// A stream failure's code is ours.
		changed = fields.set("error_type", "new_api_error") || changed
		changed = fields.set("error_code", "upstream_error") || changed
	}
	return changed
}

// maskStreamStatusForUser applies the same configuration to the error texts a
// consume log keeps in stream_status: a stream that failed after output is
// settled, not logged as an error, and the legacy stream path records the
// upstream's text there. The status and end_reason classification is ours and
// stays. Only string texts are rewritten; other values keep their raw JSON.
// Reports whether any text was rewritten.
func maskStreamStatusForUser(fields logOtherFields, userId int, langOf func(userId int) string) bool {
	status := fields.object("stream_status")
	if status == nil {
		return false
	}
	mask := func(raw json.RawMessage) (json.RawMessage, bool) {
		text, ok := rawJSONString(raw)
		if !ok {
			return raw, false
		}
		masked, replaced := operation_setting.MaskRelayStreamErrorForUser(text, langOf(userId))
		if !replaced {
			return raw, false
		}
		encoded, err := common.Marshal(masked)
		if err != nil {
			return raw, false
		}
		return encoded, true
	}
	changed := false
	if raw, ok := status["end_error"]; ok {
		if masked, replaced := mask(raw); replaced {
			status["end_error"], changed = masked, true
		}
	}
	var messages []json.RawMessage
	if raw, ok := status["errors"]; ok && common.GetJsonType(raw) == "array" && common.Unmarshal(raw, &messages) == nil {
		messagesChanged := false
		for i, raw := range messages {
			if masked, replaced := mask(raw); replaced {
				messages[i], messagesChanged = masked, true
			}
		}
		if messagesChanged && status.set("errors", messages) {
			changed = true
		}
	}
	return changed && fields.set("stream_status", status)
}
