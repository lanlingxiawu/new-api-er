package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestClaudeDiagnosticRootBoundary 验证未登录、普通用户、管理员及超级管理员的权限边界，并检查尝试选择和禁止缓存响应头。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeDiagnosticRootBoundary(t *testing.T) {
	require.NoError(t, i18n.Init())
	oldDB, oldLog, oldType, oldRedis := model.DB, model.LOG_DB, common.MainDatabaseType(), common.RedisEnabled
	db := model.DB
	if db == nil {
		var err error
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	}
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	model.InitColumnNames()
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLog
		common.RedisEnabled = oldRedis
		common.SetMainDatabaseType(oldType)
		model.InitColumnNames()
	})
	row := model.Log{RequestId: common.NewRequestId(), CreatedAt: common.GetTimestamp(), Other: `{"claude_diagnostic":{"error":"root-private"}}`}
	require.NoError(t, db.Create(&row).Error)
	require.NoError(t, db.Create(&model.Log{RequestId: row.RequestId, CreatedAt: row.CreatedAt, Other: `{"claude_diagnostic":{"attempt":2,"error":"second-private"},"claude_diagnostic_attempt":2}`}).Error)
	t.Cleanup(func() { db.Where("request_id = ?", row.RequestId).Delete(&model.Log{}) })
	router := gin.New()
	router.GET("/diagnostic", middleware.RootAuth(), GetClaudeStreamDiagnostic)
	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest("GET", "/diagnostic", nil))
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code)
	require.NotContains(t, unauthenticated.Body.String(), "root-private")
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		token := common.NewRequestId()
		user := model.User{Username: "diag-" + token, Password: "fixture", Role: role, Status: common.UserStatusEnabled, Group: "default", AccessToken: &token, AffCode: token}
		require.NoError(t, db.Create(&user).Error)
		t.Cleanup(func() { db.Unscoped().Where("id = ?", user.Id).Delete(&model.User{}) })
		request := httptest.NewRequest("GET", fmt.Sprintf("/diagnostic?request_id=%s&created_at=%d", row.RequestId, row.CreatedAt), nil)
		request.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		if role == common.RoleRootUser {
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), "root-private")
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			for _, attempt := range []string{"2", "3", "-1", "invalid"} {
				r := httptest.NewRequest("GET", fmt.Sprintf("/diagnostic?request_id=%s&created_at=%d&attempt=%s", row.RequestId, row.CreatedAt, attempt), nil)
				r.Header.Set("Authorization", "Bearer "+token)
				result := httptest.NewRecorder()
				router.ServeHTTP(result, r)
				if attempt == "2" {
					require.Contains(t, result.Body.String(), "second-private")
					require.NotContains(t, result.Body.String(), "root-private")
				} else {
					require.Contains(t, result.Body.String(), `"success":false`)
					require.NotContains(t, result.Body.String(), "private")
				}
			}
		} else {
			require.Equal(t, http.StatusForbidden, w.Code)
			require.NotContains(t, w.Body.String(), "root-private")
		}
	}
}
