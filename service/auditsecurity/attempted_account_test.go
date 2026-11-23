package auditsecurity

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAttemptedAccountCaptureAndNormalization(t *testing.T) {
	previous := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "true", "suspended": "false"}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": fmt.Sprint(previous.CaptureEnabled), "suspended": fmt.Sprint(previous.Suspended)}))
	})
	for {
		if _, ok := model.TakeSecurityCapture(); !ok {
			break
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/user/login", strings.NewReader(`{"password":"never-log-password","code":"never-log-code"}`))
	for _, test := range []struct {
		action, input, want string
		success             bool
	}{
		{"auth.login_failed", "missing-user", "missing-user", false},
		{"auth.login_locked", "用户@example.com", "用户@example.com", false},
		{"auth.login_failed", "  alice\r\nroot\x00\u202E\u2028\u2029\xff  ", "alice  root", false},
		{"auth.login_failed", strings.Repeat("界", 129), strings.Repeat("界", 128), false},
		{"auth.login_failed", "", "", false},
		{"auth.login_failed", "not-a-failure", "", true},
		{"auth.recovery_rejected", "recovery@example.com", "recovery@example.com", false},
		{"auth.recovery_requested", "recovery@example.com", "recovery@example.com", true},
	} {
		t.Run(test.action+"/"+fmt.Sprint(len(test.input), test.success), func(t *testing.T) {
			model.CaptureSecurityEvent(c, test.action, test.input, "invalid_credentials", test.success)
			entry, ok := model.TakeSecurityCapture()
			require.True(t, ok)
			require.Zero(t, entry.UserId)
			require.Empty(t, entry.Username)
			require.Nil(t, entry.Other.Op.Identity)
			if test.want == "" {
				require.NotContains(t, entry.Other.Op.Params, "attempted_account")
			} else {
				require.Equal(t, test.want, entry.Other.Op.Params["attempted_account"])
			}
			if test.input != "" {
				require.Equal(t, model.SecurityAccountRef(test.input), entry.Other.Op.Params["subject_ref"])
			}
			encoded, err := common.Marshal(entry)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "never-log-")
			event := Normalize(entry)
			var facts Facts
			require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
			require.Equal(t, test.want, facts.AttemptedAccount)
			require.Zero(t, event.ActorID)
			require.Nil(t, facts.Identity)
			require.Equal(t, common.RoleRootUser, event.Visibility)
			// Source metadata is independently bounded and action-allowlisted.
			entry.Other.Op.Params["attempted_account"] = strings.Repeat("a", 130)
			event = Normalize(entry)
			require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
			retain := !test.success && (test.action == "auth.login_failed" || test.action == "auth.login_locked")
			retain = retain || test.action == "auth.recovery_requested" || test.action == "auth.recovery_rejected"
			if retain {
				require.Equal(t, strings.Repeat("a", 128), facts.AttemptedAccount)
			} else {
				require.Empty(t, facts.AttemptedAccount)
			}
		})
	}
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "false"}))
	model.CaptureSecurityEvent(c, "auth.login_failed", "disabled", "invalid_credentials", false)
	_, ok := model.TakeSecurityCapture()
	require.False(t, ok)
}

func TestAttemptedAccountPersistsAcrossAlertReads(t *testing.T) {
	db := newWorkerTestDB(t)
	previous := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "true", "suspended": "false"}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": fmt.Sprint(previous.CaptureEnabled), "suspended": fmt.Sprint(previous.Suspended)}))
	})
	for {
		if _, ok := model.TakeSecurityCapture(); !ok {
			break
		}
	}
	state, err := model.ClaimAuditSecurity(t.Context(), db, "attempted-account")
	require.NoError(t, err)
	now := time.Now().Unix()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/user/login", nil)
	for range 6 {
		model.CaptureSecurityEvent(c, "auth.login_failed", "missing-user", "invalid_credentials", false)
	}
	w := worker{db: db, source: db, writer: db, owner: "attempted-account", sourceStartAt: now}
	require.NoError(t, w.capture(t.Context()))
	var source []model.AuditLog
	require.NoError(t, db.Find(&source).Error)
	require.Len(t, source, 6)
	for _, entry := range source {
		// Exercise AuditOther's DB JSON Scanner, then the real scanner projection.
		encoded, err := common.Marshal(entry.Other)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"attempted_account":"missing-user"`)
		require.Zero(t, entry.UserId)
	}
	require.NoError(t, w.scan(t.Context(), state, operation_setting.DefaultAuditSecuritySetting()))
	filter := AlertFilter{Start: now - 1, End: now + 10, Limit: 20}
	page, err := ListAlerts(t.Context(), db, 100, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	alert := page.Items[0]
	require.Equal(t, "missing-user", alert.AttemptedAccount)
	require.Zero(t, alert.ActorID)
	require.Empty(t, alert.ActorUsername)
	detail, err := GetAlert(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Findings, 2)
	for _, finding := range detail.Findings {
		require.Equal(t, "missing-user", finding.AttemptedAccount)
		require.Zero(t, finding.ActorID)
	}
	for _, event := range detail.Events {
		require.Contains(t, event.Facts, `"attempted_account":"missing-user"`)
	}
	related, err := GetAlertRelations(t.Context(), db, 100, 1, alert.ID, "account", "", 20, int(now-1), int(now+10))
	require.NoError(t, err)
	require.Len(t, related.Items, 2)
	require.Equal(t, "missing-user", related.Items[0].AttemptedAccount)
	export, err := ExportAlerts(t.Context(), db, 100, filter, 20)
	require.NoError(t, err)
	require.Len(t, export.Items, 1)
	require.Equal(t, "missing-user", export.Items[0].Alert.AttemptedAccount)
	page, err = ListAlerts(t.Context(), db, 10, filter)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	_, err = GetAlert(t.Context(), db, 10, alert.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	var actions int64
	require.NoError(t, db.Model(&model.SecurityAction{}).Count(&actions).Error)
	require.Zero(t, actions, "an attempted name must not authorize an account response")
}

func TestAttemptedAccountMigrationFreshUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t)
	for _, upgrade := range []bool{false, true} {
		if upgrade {
			// Only these two columns in this fixture's disposable DB are removed.
			require.NoError(t, db.Migrator().DropColumn(&model.SecurityAlert{}, "attempted_account"))
			require.NoError(t, db.Migrator().DropColumn(&model.SecurityFindingSnapshot{}, "attempted_account"))
			require.NoError(t, db.Omit("attempted_account").Create(&model.SecurityAlert{ID: 45, SubjectRef: "account:legacy", Evidence: "[]"}).Error)
			require.NoError(t, db.Omit("attempted_account").Create(&model.SecurityFindingSnapshot{ID: 45, AlertID: 45, SubjectRef: "account:legacy"}).Error)
			require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
			var alert model.SecurityAlert
			require.NoError(t, db.Select("id,subject_ref,attempted_account").First(&alert, 45).Error)
			require.Empty(t, alert.AttemptedAccount)
			require.Equal(t, "account:legacy", alert.SubjectRef)
		}
		for _, record := range []any{&model.SecurityAlert{}, &model.SecurityFindingSnapshot{}} {
			require.True(t, db.Migrator().HasColumn(record, "attempted_account"))
		}
		ddl := &migrationDDLLog{Interface: logger.Default.LogMode(logger.Silent)}
		require.NoError(t, model.MigrateAuditSecurity(t.Context(), db.Session(&gorm.Session{Logger: ddl})))
		require.Empty(t, ddl.statements)
		name := strings.Repeat("界😀", 64)
		alert := model.SecurityAlert{AttemptedAccount: name}
		require.NoError(t, db.Create(&alert).Error)
		finding := model.SecurityFindingSnapshot{AlertID: alert.ID, AttemptedAccount: name}
		require.NoError(t, db.Create(&finding).Error)
		var saved model.SecurityFindingSnapshot
		require.NoError(t, db.Select("id,attempted_account").First(&saved, finding.ID).Error)
		require.Equal(t, name, saved.AttemptedAccount)
		var savedAlert model.SecurityAlert
		require.NoError(t, db.Select("id,attempted_account").First(&savedAlert, alert.ID).Error)
		require.Equal(t, name, savedAlert.AttemptedAccount)
	}
}
