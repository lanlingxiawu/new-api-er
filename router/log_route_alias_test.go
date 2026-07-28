package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 列表接口的前端调用点是 /api/log（无尾斜杠，见
// web/src/features/usage-logs/api.ts 的 buildApiPath）。若只注册 "/"，
// Gin 的 RedirectTrailingSlash 会先回一个 301，每次列表都白付一个 RTT。
// 这里锁住「两条路径都直达同一 handler、不产生重定向」这个契约。
func TestLogListRouteHasNoTrailingSlashRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{}, len(engine.Routes()))
	handlers := make(map[string]string, len(engine.Routes()))
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		routes[key] = struct{}{}
		handlers[key] = route.Handler
	}

	_, hasBare := routes[http.MethodGet+" /api/log"]
	_, hasSlash := routes[http.MethodGet+" /api/log/"]
	assert.True(t, hasBare, "GET /api/log 必须注册，否则前端调用会吃到 301")
	assert.True(t, hasSlash, "GET /api/log/ 必须保留，兼容既有调用方")
	assert.Equal(t,
		handlers[http.MethodGet+" /api/log/"],
		handlers[http.MethodGet+" /api/log"],
		"两条路径必须指向同一个 handler")
}

// 实际发一个请求，确认无尾斜杠的路径不会被重定向。
// 不带凭证时预期停在鉴权中间件（401）——能走到鉴权就说明路由已直接命中，
// 而不是先被 RedirectTrailingSlash 弹去 /api/log/。
func TestLogListRouteServesBothPathsWithoutRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	codes := make(map[string]int, 2)
	for _, path := range []string{"/api/log", "/api/log/"} {
		req := httptest.NewRequest(http.MethodGet, path+"?p=1&page_size=10", nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		codes[path] = rec.Code

		require.NotEqual(t, http.StatusMovedPermanently, rec.Code, "%s 不应触发 301", path)
		require.NotEqual(t, http.StatusPermanentRedirect, rec.Code, "%s 不应触发 308", path)
		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"%s 应直达鉴权中间件并因缺少凭证返回 401，实际 %d", path, rec.Code)
	}
	assert.Equal(t, codes["/api/log/"], codes["/api/log"], "两条路径的响应必须一致")
}
