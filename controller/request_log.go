package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetAllRequestLogs 分页查询请求日志（仅超级管理员）。
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

// GetRequestLogDetail 获取单条请求日志详情（含完整请求/返回体）。
// GetRequestLogDetail 读取请求日志原始详情并返回禁止缓存的响应，由路由层限制为超级管理员访问。
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
	common.ApiSuccess(c, log)
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
