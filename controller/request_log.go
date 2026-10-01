package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetAllRequestLogs 分页查询请求日志（列表不含请求/响应头与正文）。
func GetAllRequestLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	requestId := c.Query("request_id")
	statusCode, _ := strconv.Atoi(c.Query("status_code"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)

	logs, total, err := model.GetAllRequestLogs(username, modelName, channel, requestId, statusCode,
		startTimestamp, endTimestamp, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// requestLogDetailView is the detail response. HeadersWithheld tells the
// dialog that a header block was cut at the size limit and could not be
// masked, so it was left out rather than shown with a partial credential.
type requestLogDetailView struct {
	*model.RequestLog
	HeadersWithheld bool `json:"headers_withheld,omitempty"`
}

// GetRequestLogDetail 读取已保存的请求日志详情（正文可能受采集预算截断），返回禁止缓存的响应。
// 路由层要求 admin_menu.request_logs 的 view_detail 权限；超级管理员看到原始请求头，
// 其他管理员看到的凭据类请求/响应头被替换为 ***，无法可靠打码（被截断）的头部整段不返回。
// 参数 c：含日志路径参数 id 及已认证身份的 Gin 上下文，结果写入其响应。
func GetRequestLogDetail(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidId)
		return
	}
	log, err := model.GetRequestLogById(id)
	if err != nil {
		// 索引被淘汰或正文文件已被清理都归为"不可用"。底层错误不外泄，
		// 也不把 go-redis / os 的错误串直接当成用户可读信息。
		common.ApiErrorI18n(c, i18n.MsgRequestLogNotFound)
		return
	}
	view := requestLogDetailView{RequestLog: log}
	if c.GetInt("role") < common.RoleRootUser {
		var requestOK, responseOK bool
		log.RequestHeaders, requestOK = common.RedactCredentialHeadersJSON(log.RequestHeaders)
		log.ResponseHeaders, responseOK = common.RedactCredentialHeadersJSON(log.ResponseHeaders)
		view.HeadersWithheld = !requestOK || !responseOK
		// A client that put its token into the path (model%3Fkey=<token>)
		// gets it echoed in error bodies; mask credential assignments there too.
		log.RequestBody = common.RedactTextCredentials(log.RequestBody)
		log.ResponseBody = common.RedactTextCredentials(log.ResponseBody)
	}
	common.ApiSuccess(c, view)
}

// DeleteHistoryRequestLogs 删除指定时间戳之前的请求日志。
func DeleteHistoryRequestLogs(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		common.ApiErrorI18n(c, i18n.MsgRequestLogTimestampRequired)
		return
	}
	count, err := model.DeleteOldRequestLog(targetTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, count)
}

// ClearAllRequestLogs 清除存储的全部请求日志索引（仅超级管理员）。
// 返回被清除的条目数；对应的正文文件由后台清理协程回收。
func ClearAllRequestLogs(c *gin.Context) {
	count, err := model.ClearAllRequestLogs()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, count)
}
