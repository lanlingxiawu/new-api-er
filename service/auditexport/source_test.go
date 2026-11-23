package auditexport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAuditExportReaderLifecycle(t *testing.T) {
	dialects := map[string]gorm.Dialector{}
	if dsn := os.Getenv("AUDIT_TEST_MYSQL_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		dialects["mysql"] = mysql.Open(dsn)
	}
	if dsn := os.Getenv("AUDIT_TEST_PG_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		dialects["postgres"] = postgres.Open(dsn)
	}
	if len(dialects) == 0 {
		dialects["sqlite"] = sqlite.Open(filepath.Join(t.TempDir(), "export.db"))
	}
	for name, dialect := range dialects {
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = pool.Close() })
			pool.SetMaxOpenConns(1)
			require.NoError(t, db.AutoMigrate(&model.AuditLog{}, &model.User{}, &model.CasbinRule{}))
			ctx := context.Background()
			require.NoError(t, model.MigrateAuditSecurity(ctx, db))
			prefix := common.NewRequestId()
			user := model.User{Username: prefix, Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			t.Cleanup(func() { db.Where("id = ?", user.Id).Delete(&model.User{}) })
			for _, p := range []authz.Permission{authz.AuditRead, authz.AuditExport, authz.AuditSecurityRead, authz.AuditSecurityExport} {
				rule := model.CasbinRule{Ptype: "p", V0: authz.UserSubject(user.Id), V1: p.Resource, V2: p.Action, V3: "allow"}
				require.NoError(t, db.Create(&rule).Error)
			}
			t.Cleanup(func() { db.Where("v0 = ?", authz.UserSubject(user.Id)).Delete(&model.CasbinRule{}) })
			no := false
			rows := []model.AuditLog{
				{EventId: prefix + "a", UserId: user.Id, Username: prefix, ActorRole: 1, CreatedAt: 100, ActorIsEmployee: &no},
				{EventId: prefix + "b", UserId: user.Id, Username: prefix, ActorRole: 10, CreatedAt: 100},
				{EventId: prefix + "c", UserId: user.Id, Username: prefix, ActorRole: 100, CreatedAt: 101},
				{EventId: prefix + "d", UserId: user.Id, Username: prefix, ActorRole: 0, CreatedAt: 102},
			}
			for i := range rows {
				rows[i].Other = model.AuditOther{RootInfo: model.AuditFields{"hidden": true}, AdminInfo: &model.AuditAdminInfo{AdminID: 77}, Op: &model.AuditOperation{Action: "export", Params: model.AuditFields{"large": int64(9007199254740993)}}}
			}
			require.NoError(t, db.Create(&rows).Error)
			t.Cleanup(func() { db.Where("username = ?", prefix).Delete(&model.AuditLog{}) })
			job := &model.LogExportJob{DataSource: "audit", UserID: user.Id, CreatorRole: 10, AuditFilters: &model.AuditExportFilter{AuditLogFilter: model.AuditLogFilter{Username: prefix, StartTimestamp: 1, EndTimestamp: 40 * 86400}}, Filters: model.LogExportFilter{StartTimestamp: 1, EndTimestamp: 40 * 86400}}
			r := &Reader{job: job, main: db, logs: db, loc: time.UTC}
			require.NoError(t, r.Validate(ctx, true))
			first, err := r.Read(ctx, 1, 40*86400, nil, 1)
			require.NoError(t, err)
			require.False(t, first.Done)
			require.Equal(t, prefix+"b", first.Rows[0].Values[0])
			require.Contains(t, first.Rows[0].Values[18], "9007199254740993")
			require.NotContains(t, first.Rows[0].Values[18], "root_info")
			require.Contains(t, first.Rows[0].Values[18], "admin_info")
			second, err := r.Read(ctx, 1, 40*86400, first.Cursor, 2)
			require.NoError(t, err)
			require.True(t, second.Done)
			require.Equal(t, prefix+"a", second.Rows[0].Values[0])
			require.Equal(t, "false", second.Rows[0].Values[5])
			job.SelfExport, job.Filters.UserId, job.AuditFilters.UserId, job.AuditFilters.SelfView = true, user.Id, user.Id, true
			require.ErrorIs(t, r.Validate(ctx, false), model.ErrLogExportAccessChanged)
			_, err = r.Read(ctx, 1, 40*86400, nil, 100)
			require.ErrorIs(t, err, model.ErrLogExportAccessChanged)
			job.AuditProjectionVersion = model.AuditActivityProjectionVersion
			require.NoError(t, r.Validate(ctx, true))
			self, err := r.Read(ctx, 1, 40*86400, nil, 100)
			require.NoError(t, err)
			require.Len(t, self.Rows[0].Values, 14)
			require.NotContains(t, fmt.Sprint(self.Rows[0].Values), "admin_info")
			require.NotContains(t, fmt.Sprint(self.Rows[0].Values), "9007199254740993")
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleCommonUser).Error)
			require.ErrorIs(t, r.Validate(ctx, true), model.ErrLogExportAccessChanged, "demotion invalidates a pending higher-role self export")
			job.CreatorRole = common.RoleCommonUser
			require.NoError(t, r.Validate(ctx, true))
			current, err := r.Read(ctx, 1, 40*86400, nil, 100)
			require.NoError(t, err)
			require.Len(t, current.Rows, 1, "new self exports exclude historical administrator events")
			require.Equal(t, prefix+"a", current.Rows[0].Values[0])
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleAdminUser).Error)
			job.CreatorRole = common.RoleAdminUser
			job.SelfExport, job.Filters.UserId, job.AuditFilters.UserId, job.AuditFilters.SelfView = false, 0, 0, false
			job.AuditFilters.IPRisk = "high"
			alert := model.SecurityAlert{Rule: "password_spray", SubjectRef: prefix, Visibility: 10, Status: "open", Version: 1}
			require.NoError(t, db.Create(&alert).Error)
			t.Cleanup(func() {
				db.Where("alert_id = ?", alert.ID).Delete(&model.SecurityAlertEvent{})
				db.Where("alert_id = ?", alert.ID).Delete(&model.SecurityAction{})
				db.Where("id = ?", alert.ID).Delete(&model.SecurityAlert{})
			})
			links := make([]model.SecurityAlertEvent, 0, 101)
			for i := range 100 {
				links = append(links, model.SecurityAlertEvent{EventID: fmt.Sprintf("%s-missing-%03d", prefix, i), AlertID: alert.ID, EventAt: 200})
			}
			links = append(links, model.SecurityAlertEvent{EventID: prefix + "a", AlertID: alert.ID, EventAt: 100})
			require.NoError(t, db.CreateInBatches(&links, 100).Error)
			require.NoError(t, r.Validate(ctx, true))
			empty, err := r.Read(ctx, 1, 300, nil, 100)
			require.NoError(t, err)
			require.Empty(t, empty.Rows)
			require.False(t, empty.Done)
			require.Equal(t, 100, empty.Scanned)
			match, err := r.Read(ctx, 1, 300, empty.Cursor, 100)
			require.NoError(t, err)
			require.True(t, match.Done)
			require.Equal(t, 1, match.Scanned)
			require.Len(t, match.Rows, 1)
			_, err = auditsecurity.UpdateAlertInvestigation(ctx, db, 10, user.Id, auditsecurity.AlertInvestigation{Version: 1, Status: "resolved"}, alert.ID)
			require.NoError(t, err)
			require.ErrorIs(t, r.Validate(ctx, true), model.ErrLogExportRiskChanged)
			_, err = r.Read(ctx, 1, 300, nil, 100)
			require.ErrorIs(t, err, model.ErrLogExportRiskChanged)
			job.AuditFilters.IPRisk = ""
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v1 = ? AND v2 = ?", authz.UserSubject(user.Id), "audit", "export").Update("v3", "deny").Error)
			require.ErrorIs(t, r.Validate(ctx, true), model.ErrLogExportAccessChanged)
			_, err = currentAccess(ctx, db, user.Id, "audit_admin")
			require.ErrorIs(t, err, model.ErrEmployeeExportAccess, "download and task APIs share current export authorization")
			_, err = currentAccess(ctx, db, user.Id, "audit_self")
			require.NoError(t, err, "self export is independent of administrator export grants")
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v1 = ?", authz.UserSubject(user.Id), "audit").Update("v3", "deny").Error)
			require.ErrorIs(t, r.Validate(ctx, true), model.ErrLogExportAccessChanged)
			// Waiting for the single connection consumes the supplied deadline.
			tx := db.Begin()
			require.NoError(t, tx.Error)
			limited, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			_, err = currentAccess(limited, db, user.Id, "audit_self")
			cancel()
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NoError(t, tx.Rollback().Error)
		})
	}
}
