package controller

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func exportManagementFixture(t *testing.T) {
	t.Helper()
	if model.DB == nil {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		model.DB = db
		t.Cleanup(func() { _ = sqlDB.Close(); model.DB = nil })
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
		var version string
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
		t.Log("SQLite fallback version:", version)
		var plan []struct{ Detail string }
		require.NoError(t, db.Raw("EXPLAIN QUERY PLAN SELECT id FROM users WHERE id IN (1,2,3) AND inviter_id = 7 AND role = 1 AND deleted_at IS NULL").Scan(&plan).Error)
		t.Log("Customer ownership query:", plan)
	}
	server := miniredis.RunT(t)
	previous, enabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = previous, enabled })
	withExportSetting(t, func(s *operation_setting.LogExportSetting) {
		s.EmployeeExportEnabled = true
		s.EmployeeCustomerManageEnabled = false
	})
}

func TestExportManagementDownloadAndRevocation(t *testing.T) {
	exportManagementFixture(t)
	employee := mkControllerExportEmployee(t)
	customer := mkUser(t, func(u *model.User) { u.InviterId = employee.Id })
	admin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	job := mkReadyExportJob(t, customer.Id, "customer-file")
	job.SelfExport = true
	job.Filters.UserId = customer.Id
	model.UpdateLogExportJob(job)

	for _, manage := range []bool{false, true} {
		operation_setting.GetLogExportSetting().EmployeeCustomerManageEnabled = manage
		for _, viewer := range []*model.User{employee, admin} {
			base := "/api/user/employee/export/jobs/"
			if viewer.Id == admin.Id {
				base = "/api/log/export/jobs/"
			}
			c, rec := newCtx(t, http.MethodGet, base+job.JobID+"/download-url", nil)
			c.Set("id", viewer.Id)
			c.Set("role", viewer.Role)
			c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
			GetLogExportDownloadURL(c)
			if viewer.Id == employee.Id && !manage {
				require.Equal(t, http.StatusForbidden, rec.Code)
				continue
			}
			var response struct {
				Success bool
				Data    struct {
					URL string `json:"url"`
				}
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
			require.True(t, response.Success, rec.Body.String())
			token := strings.TrimPrefix(response.Data.URL, "/dl/log-export/")
			grant, err := model.ConsumeManagedLogExportDownloadToken(token)
			require.NoError(t, err)
			require.Equal(t, viewer.Id, grant.UserID)
			c, rec = newCtx(t, http.MethodGet, response.Data.URL, nil)
			c.Params = gin.Params{{Key: "token", Value: token}}
			DownloadLogExport(c)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, "customer-file", rec.Body.String())
			if viewer.Id == employee.Id {
				operation_setting.GetLogExportSetting().EmployeeCustomerManageEnabled = false
			} else {
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", viewer.Id).Update("role", common.RoleCommonUser).Error)
			}
			c, rec = newCtx(t, http.MethodGet, response.Data.URL, nil)
			c.Request.Header.Set("Range", "bytes=0-2")
			c.Params = gin.Params{{Key: "token", Value: token}}
			DownloadLogExport(c)
			require.Equal(t, http.StatusForbidden, rec.Code)
			require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
			operation_setting.GetLogExportSetting().EmployeeCustomerManageEnabled = manage
		}
	}
}

func TestExportManagementCustomerTransferAndActions(t *testing.T) {
	exportManagementFixture(t)
	employee := mkControllerExportEmployee(t)
	customer := mkUser(t, func(u *model.User) { u.InviterId = employee.Id })
	job := mkReadyExportJob(t, customer.Id, "customer-file")
	job.SelfExport = true
	job.Filters.UserId = customer.Id
	model.UpdateLogExportJob(job)
	base := "/api/user/employee/export/jobs"
	for _, manage := range []bool{false, true} {
		operation_setting.GetLogExportSetting().EmployeeCustomerManageEnabled = manage
		c, rec := newCtx(t, http.MethodGet, base+"?customer_id="+strconv.Itoa(customer.Id), nil)
		c.Set("id", employee.Id)
		c.Set("role", employee.Role)
		GetLogExportJobs(c)
		var response struct {
			Success bool
			Data    model.LogExportJobPage
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
		require.True(t, response.Success, rec.Body.String())
		require.Len(t, response.Data.Jobs, 1)
		require.Equal(t, manage, response.Data.Jobs[0].CanDelete)
		c, rec = newCtx(t, http.MethodDelete, base+"/"+job.JobID, nil)
		c.Set("id", employee.Id)
		c.Set("role", employee.Role)
		c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
		DeleteLogExportJob(c)
		if !manage {
			require.Equal(t, http.StatusForbidden, rec.Code)
		} else {
			require.Contains(t, rec.Body.String(), `"deleted":true`)
		}
	}
	job = mkReadyExportJob(t, customer.Id, "another-file")
	job.SelfExport = true
	job.Filters.UserId = customer.Id
	model.UpdateLogExportJob(job)
	token, err := model.CreateManagedLogExportDownloadToken(job.JobID, employee.Id, 1, "employee")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", customer.Id).Update("inviter_id", 0).Error)
	for _, handler := range []gin.HandlerFunc{GetLogExportJob, DeleteLogExportJob, GetLogExportDownloadURL} {
		c, rec := newCtx(t, http.MethodGet, base+"/"+job.JobID, nil)
		c.Set("id", employee.Id)
		c.Set("role", employee.Role)
		c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
		handler(c)
		require.Equal(t, http.StatusForbidden, rec.Code)
	}
	c, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	c.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(c)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestExportManagementAdminCanManageAllTasks(t *testing.T) {
	exportManagementFixture(t)
	admin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	owner := mkUser(t, func(u *model.User) { u.Role = common.RoleRootUser; u.Status = common.UserStatusDisabled })
	job := mkReadyExportJob(t, owner.Id, "root-export-file")
	job.CreatorRole = common.RoleRootUser
	job.Filters.TokenName = "private-filter"
	model.UpdateLogExportJob(job)
	for _, suffix := range []string{"", "/download-url"} {
		c, rec := newCtx(t, http.MethodGet, "/api/log/export/jobs/"+job.JobID+suffix, nil)
		c.Set("id", admin.Id)
		c.Set("role", admin.Role)
		c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
		if suffix == "" {
			GetLogExportJob(c)
			require.NotContains(t, rec.Body.String(), "private-filter")
			require.NotContains(t, rec.Body.String(), job.Parts[0].Path)
		} else {
			GetLogExportDownloadURL(c)
		}
		require.True(t, decodeResp(t, rec).Success, rec.Body.String())
	}
	token, err := model.CreateManagedLogExportDownloadToken(job.JobID, admin.Id, 1, "admin")
	require.NoError(t, err)
	c, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	c.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(c)
	require.Equal(t, "root-export-file", rec.Body.String())
	for _, status := range []string{model.LogExportStatusRunning, model.LogExportStatusReady} {
		job.Status = status
		model.UpdateLogExportJob(job)
		c, rec = newCtx(t, http.MethodDelete, "/api/log/export/jobs/"+job.JobID, nil)
		c.Set("id", admin.Id)
		c.Set("role", admin.Role)
		c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
		DeleteLogExportJob(c)
		require.True(t, decodeResp(t, rec).Success, rec.Body.String())
		if status == model.LogExportStatusRunning {
			require.Contains(t, rec.Body.String(), `"canceled":true`)
			value, err := common.RedisGet("logexport:cancel:" + job.JobID)
			require.NoError(t, err)
			require.Equal(t, "1", value)
		} else {
			deleted, err := model.GetLogExportJob(job.JobID)
			require.NoError(t, err)
			require.Nil(t, deleted)
			_, err = os.Stat(job.Parts[0].Path)
			require.True(t, os.IsNotExist(err))
		}
	}
}
