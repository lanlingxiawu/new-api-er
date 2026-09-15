package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestClaudeRequestLoggingCapturesRequests 验证 Messages 与其他路径一样遵循原请求采集规则。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeRequestLoggingCapturesRequests(t *testing.T) {
	old := common.RequestLogEnabled
	common.RequestLogEnabled = true
	t.Cleanup(func() { common.RequestLogEnabled = old })
	router := gin.New()
	router.Use(RequestResponseLogger())
	router.POST("/v1/messages", func(c *gin.Context) {
		_, capturing := c.Writer.(*responseBodyWriter)
		require.True(t, capturing)
		c.Status(200)
	})
	request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"private":"request"}`))
	request.Header.Set("Authorization", "Bearer private")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	require.Equal(t, 200, w.Code)
}
