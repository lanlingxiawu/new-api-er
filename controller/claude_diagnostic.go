package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetClaudeStreamDiagnostic 读取超级管理员专用 Claude 响应诊断，限制查询时长并禁止客户端缓存。
// 参数 c：已通过 RootAuth 的上下文；查询参数 request_id 为请求标识，created_at 为 Unix 秒，attempt 为尝试编号（缺省 0）。
// 结果写入 API 响应；访问日志仅记操作者与查询键，不记诊断正文。
func GetClaudeStreamDiagnostic(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	requestID := c.Query("request_id")
	createdAt, err := strconv.ParseInt(c.Query("created_at"), 10, 64)
	attempt, attemptErr := strconv.Atoi(c.DefaultQuery("attempt", "0"))
	if err != nil || attemptErr != nil || attempt < 0 || createdAt <= 0 || requestID == "" || len(requestID) > 64 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	diagnostic, err := model.GetClaudeStreamDiagnostic(ctx, requestID, createdAt, attempt)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogError(c, "Claude diagnostic lookup failed")
		}
		common.ApiErrorI18n(c, i18n.MsgRequestLogNotFound)
		return
	}
	logger.LogInfo(c, fmt.Sprintf("Root diagnostic read: actor=%d request_id=%q created_at=%d attempt=%d", c.GetInt("id"), requestID, createdAt, attempt))
	common.ApiSuccess(c, diagnostic)
}
