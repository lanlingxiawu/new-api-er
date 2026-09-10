package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestClaudeRequestLoggingDoesNotCaptureRequests 验证 Claude Messages 绕过旧请求日志采集，开启全局请求日志也不采集该请求的头和正文。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeRequestLoggingDoesNotCaptureRequests(t *testing.T) {
	old := common.RequestLogEnabled
	common.RequestLogEnabled = true
	t.Cleanup(func() { common.RequestLogEnabled = old })
	router := gin.New()
	router.Use(RequestResponseLogger())
	router.POST("/v1/messages", func(c *gin.Context) {
		_, capturing := c.Writer.(*responseBodyWriter)
		require.False(t, capturing)
		c.Status(200)
	})
	request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"private":"request"}`))
	request.Header.Set("Authorization", "Bearer private")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	require.Equal(t, 200, w.Code)
}
