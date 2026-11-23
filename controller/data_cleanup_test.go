package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDataCleanupRejectsFinancialAccessBeforeDatabase(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", 1)
		c.Set("role", role)
		c.Request = httptest.NewRequest("POST", "/api/data-cleanup/preview", strings.NewReader(`{"kind":"ledger","before":1}`))
		PreviewDataCleanup(c)
		require.Equal(t, 403, w.Code)
	}
}

func TestErrorLogCleanupIsHiddenFromGenericTaskAPI(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("role", common.RoleRootUser)
	c.Request = httptest.NewRequest("GET", "/api/system-task/current?type="+model.SystemTaskTypeErrorLogCleanup, nil)
	GetCurrentSystemTask(c)
	require.Equal(t, 403, w.Code)
}

func TestDataCleanupRejectsInvalidPreview(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("role", common.RoleRootUser)
	c.Request = httptest.NewRequest("POST", "/api/data-cleanup", strings.NewReader(`{"preview_id":""}`))
	CreateDataCleanup(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"success":false`)
}

func TestDataCleanupDirectStartValidation(t *testing.T) {
	for _, body := range []string{
		`{"kind":"ledger","before":1}`,
		`{"kind":"error_logs","before":1,"resume":true}`,
		`{"kind":"error_logs","before":1,"preview_id":"01234567890123456789012345678901"}`,
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", 1)
		c.Set("role", common.RoleRootUser)
		c.Request = httptest.NewRequest("POST", "/api/data-cleanup", strings.NewReader(body))
		CreateDataCleanup(c)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), `"success":false`)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 1)
	c.Set("role", common.RoleCommonUser)
	c.Request = httptest.NewRequest("POST", "/api/data-cleanup", strings.NewReader(`{"kind":"error_logs","before":1}`))
	CreateDataCleanup(c)
	require.Equal(t, 403, w.Code)
}

func TestErrorLogCleanupContinuationEligibility(t *testing.T) {
	requireDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	owner := nextTestID()
	id, err := common.GenerateRandomCharsKey(32)
	require.NoError(t, err)
	taskID, err := model.GenerateSystemTaskID()
	require.NoError(t, err)
	task := model.SystemTask{TaskID: taskID, Type: model.SystemTaskTypeErrorLogCleanup, Status: model.SystemTaskStatusFailed}
	require.NoError(t, model.DB.Create(&task).Error)
	run := model.DataCleanupRun{ID: id, UserID: owner, Kind: model.CleanupErrorLogs, Status: "failed", TaskID: &taskID}
	require.NoError(t, model.DB.Create(&run).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id = ?", id).Delete(&model.DataCleanupRun{}).Error)
		require.NoError(t, model.DB.Where("task_id = ?", taskID).Delete(&model.SystemTask{}).Error)
	})
	for _, test := range []struct {
		name      string
		userID    int
		status    model.SystemTaskStatus
		errorKey  string
		canResume bool
	}{
		{"owner", owner, model.SystemTaskStatusFailed, "cleanup.failed", true},
		{"other administrator", owner + 1, model.SystemTaskStatusFailed, "cleanup.failed", false},
		{"missing saved index", owner, model.SystemTaskStatusFailed, "cleanup.index_missing", false},
		{"worker still running", owner, model.SystemTaskStatusRunning, "cleanup.failed", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, model.DB.Model(&task).Update("status", test.status).Error)
			require.NoError(t, model.DB.Model(&run).Update("error_key", test.errorKey).Error)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("id", test.userID)
			c.Set("role", common.RoleRootUser)
			c.Request = httptest.NewRequest("GET", "/api/data-cleanup?kind=error_logs&id="+id, nil)
			GetDataCleanup(c)
			require.Equal(t, 200, w.Code)
			var response struct {
				Success bool
				Data    model.DataCleanupRun
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			require.True(t, response.Success)
			require.Equal(t, test.canResume, response.Data.CanResume)
			require.Equal(t, string(test.status), response.Data.Status)
			require.Equal(t, test.errorKey, response.Data.ErrorKey)
			var progressResponse struct {
				Data struct {
					Progress struct {
						Phase      string
						LeaseValid bool `json:"lease_valid"`
					}
				}
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &progressResponse))
			require.False(t, progressResponse.Data.Progress.LeaseValid, "a running row without a lease is not a live worker")
		})
	}
}
