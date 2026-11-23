package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSystemTaskListFiltersAndPaginationResponse(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}))
	require.NoError(t, db.Create(&[]model.SystemTask{
		{TaskID: "older", Type: model.SystemTaskTypeModelUpdate, Status: model.SystemTaskStatusFailed},
		{TaskID: "newer", Type: model.SystemTaskTypeModelUpdate, Status: model.SystemTaskStatusFailed},
		{TaskID: "other-status", Type: model.SystemTaskTypeModelUpdate, Status: model.SystemTaskStatusSucceeded},
		{TaskID: "other-type", Type: model.SystemTaskTypeChannelTest, Status: model.SystemTaskStatusFailed},
	}).Error)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/system-task/list?scope=history&type=model_update&status=failed&offset=1&limit=1", nil)
	ListSystemTasks(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool                       `json:"success"`
		Total   int64                      `json:"total"`
		Data    []model.SystemTaskResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.EqualValues(t, 2, response.Total)
	require.Len(t, response.Data, 1)
	assert.Equal(t, "older", response.Data[0].TaskID)
}

func TestSystemTaskInvalidFiltersAreRejected(t *testing.T) {
	for _, tc := range []struct {
		query   string
		handler gin.HandlerFunc
	}{
		{"?scope=invalid", ListSystemTasks},
		{"?status=unknown", ListSystemTasks},
		{"?offset=-1", ListSystemTasks},
		{"?limit=invalid", ListSystemTasks},
		{"?status=unknown", DeleteSystemTaskHistory},
		{"?scope=invalid", DeleteSystemTaskHistory},
	} {
		t.Run(tc.query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/"+tc.query, nil)
			tc.handler(c)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.JSONEq(t, `{"success":false,"message":"invalid system task filters"}`, recorder.Body.String())
		})
	}
}

func TestCreateLogCleanupSystemTaskSharedConflict(t *testing.T) {
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}))
	for _, taskType := range []string{model.SystemTaskTypeDataCleanup, model.SystemTaskTypeErrorLogCleanup} {
		t.Run(taskType, func(t *testing.T) {
			taskID, err := model.GenerateSystemTaskID()
			require.NoError(t, err)
			activeKey := model.SystemTaskTypeLogCleanup
			task := model.SystemTask{
				TaskID: taskID, Type: taskType, ActiveKey: &activeKey,
				Status: model.SystemTaskStatusPending, Payload: `{"run_id":"private-cleanup-receipt"}`,
			}
			require.NoError(t, model.DB.Create(&task).Error)
			t.Cleanup(func() { require.NoError(t, model.DB.Where("task_id = ?", taskID).Delete(&model.SystemTask{}).Error) })
			for _, lang := range []string{"en", "zh-CN", "zh-TW"} {
				t.Run(lang, func(t *testing.T) {
					w := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(w)
					c.Request = httptest.NewRequest(http.MethodPost, "/api/system-task/log-cleanup?target_timestamp=1234", nil)
					c.Request.Header.Set("Accept-Language", lang)
					CreateLogCleanupSystemTask(c)
					require.Equal(t, http.StatusOK, w.Code)
					var response struct {
						Success bool
						Message string
						Data    *model.SystemTaskResponse
					}
					require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
					require.False(t, response.Success)
					require.Equal(t, common.TranslateMessage(c, "cleanup.busy"), response.Message)
					require.NotEqual(t, "cleanup.busy", response.Message)
					require.Nil(t, response.Data)
					require.NotContains(t, w.Body.String(), "private-cleanup-receipt")
					require.NotContains(t, w.Body.String(), "system_tasks")
				})
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/system-task/current?type=log_cleanup", nil)
			GetCurrentSystemTask(c)
			require.JSONEq(t, `{"success":true,"message":"","data":null}`, w.Body.String())
		})
	}
}

func TestCreateLogCleanupSystemTaskSanitizesDatabaseError(t *testing.T) {
	requireDB(t)
	previousDB := model.DB
	model.DB = model.DB.Session(&gorm.Session{NewDB: true}).Table("missing_cleanup_table")
	t.Cleanup(func() { model.DB = previousDB })
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/system-task/log-cleanup?target_timestamp=1234", nil)
	CreateLogCleanupSystemTask(c)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	var response struct {
		Success bool
		Message string
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, common.TranslateMessage(c, "cleanup.failed"), response.Message)
	require.NotContains(t, w.Body.String(), "missing_cleanup_table")
}

func TestCreateLogCleanupSystemTaskInvalidTimestamp(t *testing.T) {
	requireDB(t)
	previousDB := model.DB
	model.DB = model.DB.Session(&gorm.Session{NewDB: true}).Table("missing_cleanup_table")
	t.Cleanup(func() { model.DB = previousDB })
	for _, value := range []string{"", "0", "-1", "invalid", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/system-task/log-cleanup?target_timestamp="+value, nil)
			CreateLogCleanupSystemTask(c)
			require.Equal(t, http.StatusOK, w.Code)
			var response struct {
				Success bool
				Message string
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			require.False(t, response.Success)
			require.Equal(t, common.TranslateMessage(c, "cleanup.invalid_date"), response.Message)
		})
	}
}

func TestCreateAuditLogCleanupRejectsAmbiguousRequests(t *testing.T) {
	for _, query := range []string{
		"scope=other&target_timestamp=1234",
		"scope=audit&target_timestamp=1234",
		"scope=audit&target_timestamp=0",
		"scope=audit&target_timestamp=0&audit_target_timestamp=invalid",
		"scope=audit&target_timestamp=0&audit_target_timestamp=9223372036854775807",
		// An older server ignores the audit fields and must reject the zero legacy timestamp.
		"target_timestamp=0&audit_target_timestamp=1234",
	} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/system-task/log-cleanup?"+query, nil)
			CreateLogCleanupSystemTask(c)
			require.Equal(t, http.StatusOK, w.Code)
			var response struct {
				Success bool
				Message string
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			require.False(t, response.Success)
			require.NotEmpty(t, response.Message)
			require.NotContains(t, response.Message, "cleanup.")
		})
	}
}

func TestCreateAuditLogCleanupUsesExistingTask(t *testing.T) {
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/system-task/log-cleanup?scope=audit&target_timestamp=0&audit_target_timestamp=1234", nil)
	CreateLogCleanupSystemTask(c)
	require.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Success bool
		Data    struct {
			TaskID  string `json:"task_id"`
			Type    string
			Payload service.LogCleanupPayload
		}
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("task_id = ?", response.Data.TaskID).Delete(&model.SystemTask{}).Error)
	})
	require.Equal(t, model.SystemTaskTypeLogCleanup, response.Data.Type)
	require.Equal(t, "audit", response.Data.Payload.Scope)
	require.Zero(t, response.Data.Payload.TargetTimestamp)
	require.EqualValues(t, 1234, response.Data.Payload.AuditTargetTimestamp)
}
