package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestLogExportAdminCooldownOption(t *testing.T) {
	requireDB(t)
	const key = "log_export_setting.admin_cooldown_sec"
	s := operation_setting.GetLogExportSetting()
	previous := *s
	var old []model.Option
	require.NoError(t, model.DB.Where(&model.Option{Key: key}).Find(&old).Error)
	common.OptionMapRWMutex.Lock()
	oldMap, existed := common.OptionMap[key]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		if len(old) != 0 {
			require.NoError(t, model.DB.Save(&old[0]).Error)
		} else {
			require.NoError(t, model.DB.Where(&model.Option{Key: key}).Delete(&model.Option{}).Error)
		}
		common.OptionMapRWMutex.Lock()
		if existed {
			common.OptionMap[key] = oldMap
		} else {
			delete(common.OptionMap, key)
		}
		common.OptionMapRWMutex.Unlock()
		*s = previous
	})
	for _, tc := range []struct {
		value any
		want  int
	}{
		{-1, -1}, {0, 0}, {1, 1}, {30, 30}, {86400, 86400},
		{" -1 ", -1}, {" 0 ", 0}, {" 30 ", 30},
	} {
		t.Run(fmt.Sprint(tc.value), func(t *testing.T) {
			c, rec := newCtx(t, http.MethodPut, "/api/option/", map[string]any{
				"scope": "system-tuning.log-export", "key": key, "value": tc.value,
			})
			asAdmin(c, nextTestID())
			c.Set(middleware.SystemSettingsScopeContextKey, "system-tuning.log-export")
			UpdateOption(c)
			require.True(t, decodeResp(t, rec).Success, rec.Body.String())
			require.Equal(t, tc.want, s.AdminCooldownSec)
			var saved model.Option
			require.NoError(t, model.DB.Where(&model.Option{Key: key}).First(&saved).Error)
			require.Equal(t, strconv.Itoa(tc.want), saved.Value)
			s.AdminCooldownSec = 19
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{key: saved.Value}))
			require.Equal(t, tc.want, s.AdminCooldownSec, "persisted values survive reload")
		})
	}
	for _, value := range []any{-2, 86401, 1.5, "abc", "", true, nil} {
		t.Run(fmt.Sprint("invalid_", value), func(t *testing.T) {
			before := s.AdminCooldownSec
			c, rec := newCtx(t, http.MethodPut, "/api/option/", map[string]any{"key": key, "value": value})
			UpdateOption(asRoot(c, nextTestID()))
			require.False(t, decodeResp(t, rec).Success)
			require.Equal(t, i18n.T(c, i18n.MsgInvalidParams), decodeResp(t, rec).Message)
			require.Equal(t, before, s.AdminCooldownSec)
		})
	}
}

func TestLogExportCooldownByCreatorRole(t *testing.T) {
	requireDB(t)
	requireLogDB(t)
	enableRedis(t)
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, t.TempDir())
	}
	logSetting := operation_setting.GetLogSetting()
	previousLog := *logSetting
	t.Cleanup(func() { *logSetting = previousLog })
	logSetting.UserExportEnabled = true
	for _, tc := range []struct {
		name, route       string
		role, admin, want int
	}{
		{"admin", "admin", common.RoleAdminUser, 30, 30},
		{"root", "admin", common.RoleRootUser, 30, 30},
		{"admin_self", "self", common.RoleAdminUser, 30, 30},
		{"user_self", "self", common.RoleCommonUser, 30, 300},
		{"employee", "employee", common.RoleCommonUser, 30, 300},
		{"admin_employee", "employee", common.RoleAdminUser, 30, 30},
		{"inherit", "admin", common.RoleAdminUser, -1, 300},
		{"disabled", "admin", common.RoleAdminUser, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withExportSetting(t, func(s *operation_setting.LogExportSetting) {
				s.Enabled, s.OffpeakOnly = true, false
				s.UserCooldownSec, s.AdminCooldownSec = 300, tc.admin
				s.MaxConcurrentJobs, s.MaxActiveJobsPerUser = 4, 3
				s.MinFreeDiskMB, s.BatchSleepMs = 1, 0
				s.CPUSoftLimit, s.CPUHardLimit, s.TimeoutSec = 99, 100, 30
			})
			userID := nextTestID()
			body := map[string]any{"start_timestamp": 1, "end_timestamp": 2, "columns": []string{"created_at"}}
			path := "/api/log/export/jobs"
			if tc.route == "self" {
				path = "/api/log/self/export/jobs"
				body = map[string]any{"filters": map[string]any{"start_timestamp": 1, "end_timestamp": 2}}
			}
			if tc.route == "employee" {
				var customer int
				var tpl *model.EmployeeExportTemplate
				userID, customer, tpl = employeeControllerExportFixture(t)
				path = "/api/user/employee/export/jobs"
				body = map[string]any{"start_timestamp": 1, "end_timestamp": 2, "template_id": tpl.Key, "customer_ids": []int{customer}}
			}
			key := "logexport:cooldown:" + strconv.Itoa(userID)
			model.ClearLogExportCooldown(userID)
			t.Cleanup(func() {
				model.ClearLogExportCooldown(userID)
				require.NoError(t, model.LOG_DB.Where("user_id = ? AND action = ?", userID, "log_export.job_create").Delete(&model.AuditLog{}).Error)
			})
			for attempt := range 2 {
				if attempt == 1 && tc.name == "admin" {
					// The same administrator cannot bypass cooldown via self export.
					c, rec := newCtx(t, http.MethodPost, "/api/log/self/export/jobs", map[string]any{
						"filters": map[string]any{"start_timestamp": 1, "end_timestamp": 2},
					})
					CreateSelfLogExportJob(asAdmin(c, userID))
					require.False(t, decodeResp(t, rec).Success)
					require.Equal(t, i18n.T(c, i18n.MsgLogExportCooldown, map[string]any{"Minutes": 1}), decodeResp(t, rec).Message)
				}
				c, rec := newCtx(t, http.MethodPost, path, body)
				asUser(c, userID).Set("role", tc.role)
				switch tc.route {
				case "self":
					CreateSelfLogExportJob(c)
				case "employee":
					EmployeeCreateExport(c)
				default:
					CreateLogExportJob(c)
				}
				resp := decodeResp(t, rec)
				if attempt == 1 && tc.want > 0 {
					require.False(t, resp.Success)
					require.Equal(t, i18n.T(c, i18n.MsgLogExportCooldown, map[string]any{"Minutes": (tc.want + 59) / 60}), resp.Message)
					continue
				}
				require.True(t, resp.Success, rec.Body.String())
				var created struct {
					JobID string `json:"job_id"`
				}
				require.NoError(t, common.Unmarshal(resp.Data, &created))
				cleanupExportJob(t, created.JobID)
				ttl, err := common.RDB.TTL(context.Background(), key).Result()
				require.NoError(t, err)
				if tc.want == 0 {
					require.Equal(t, time.Duration(-2), ttl)
				} else {
					require.Greater(t, ttl, time.Duration(tc.want-10)*time.Second)
					require.LessOrEqual(t, ttl, time.Duration(tc.want)*time.Second)
				}
				var job *model.LogExportJob
				require.Eventually(t, func() bool {
					job, err = model.GetLogExportJob(created.JobID)
					return err == nil && job != nil && job.IsTerminal()
				}, 35*time.Second, 50*time.Millisecond)
				require.Equal(t, model.LogExportStatusReady, job.Status, job.Error)
			}
		})
	}
}

// Fail only metadata writes, leaving the real Redis cooldown and slots observable.
type logExportSaveFailure struct{ jobID string }

func (h *logExportSaveFailure) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "set" && len(cmd.Args()) > 1 {
		if id, ok := strings.CutPrefix(fmt.Sprint(cmd.Args()[1]), "logexport:job:"); ok {
			h.jobID = id
			return ctx, errors.New("injected export metadata write failure")
		}
	}
	return ctx, nil
}
func (*logExportSaveFailure) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (*logExportSaveFailure) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (*logExportSaveFailure) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func TestLogExportCooldownCreateFailure(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	for _, route := range []string{"admin", "employee"} {
		for _, cooldown := range []int{0, 30} {
			t.Run(fmt.Sprintf("%s_%d", route, cooldown), func(t *testing.T) {
				withExportSetting(t, func(s *operation_setting.LogExportSetting) {
					s.Enabled, s.AdminCooldownSec = true, cooldown
					s.MaxConcurrentJobs, s.MaxActiveJobsPerUser, s.MinFreeDiskMB = 4, 3, 1
				})
				userID := nextTestID()
				body := map[string]any{"start_timestamp": 1, "end_timestamp": 2, "columns": []string{"created_at"}}
				if route == "employee" {
					var customer int
					var tpl *model.EmployeeExportTemplate
					userID, customer, tpl = employeeControllerExportFixture(t)
					body = map[string]any{"start_timestamp": 1, "end_timestamp": 2, "template_id": tpl.Key, "customer_ids": []int{customer}}
				}
				previous := common.RDB
				client := redis.NewClient(previous.Options())
				hook := &logExportSaveFailure{}
				client.AddHook(hook)
				common.RDB = client
				t.Cleanup(func() { _ = client.Close(); common.RDB = previous })
				key := "logexport:cooldown:" + strconv.Itoa(userID)
				model.ClearLogExportCooldown(userID)
				t.Cleanup(func() { model.ClearLogExportCooldown(userID) })
				if cooldown == 0 {
					require.NoError(t, client.Set(context.Background(), key, "existing", time.Minute).Err())
				}
				c, rec := newCtx(t, http.MethodPost, "/api/log/export/jobs", body)
				asAdmin(c, userID)
				if route == "employee" {
					EmployeeCreateExport(c)
				} else {
					CreateLogExportJob(c)
				}
				require.False(t, decodeResp(t, rec).Success)
				require.NotEmpty(t, hook.jobID, "failure must occur after cooldown acquisition")
				require.ErrorIs(t, client.ZScore(context.Background(), "logexport:slots", hook.jobID).Err(), redis.Nil)
				if cooldown == 0 {
					value, err := client.Get(context.Background(), key).Result()
					require.NoError(t, err)
					require.Equal(t, "existing", value, "disabled path must preserve an earlier cooldown")
				} else {
					require.ErrorIs(t, client.Get(context.Background(), key).Err(), redis.Nil)
				}
			})
		}
	}
}
