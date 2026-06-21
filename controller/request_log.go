package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
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
func GetRequestLogDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "invalid id",
		})
		return
	}
	log, err := model.GetRequestLogById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, log)
}

// DeleteHistoryRequestLogs 删除指定时间戳之前的请求日志。
func DeleteHistoryRequestLogs(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target_timestamp is required",
		})
		return
	}
	count, err := model.DeleteOldRequestLog(targetTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, count)
}

// ClearAllRequestLogs 清除存储的全部请求日志（仅超级管理员）。
func ClearAllRequestLogs(c *gin.Context) {
	count, err := model.ClearAllRequestLogs()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, count)
}
