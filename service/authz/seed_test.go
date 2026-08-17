package authz

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedBuiltInRoles upserts both built-in roles; re-running updates in place
// (idempotent, no duplicates) via the ON CONFLICT(key) clause.
func TestSeedBuiltInRoles_Idempotent(t *testing.T) {
	db := newMigratedDB(t)

	require.NoError(t, seedBuiltInRoles(db))
	require.NoError(t, seedBuiltInRoles(db))

	var roles []model.AuthzRole
	require.NoError(t, db.Order("sort asc").Find(&roles).Error)
	require.Len(t, roles, 2)
	assert.Equal(t, BuiltInRoleRoot, roles[0].Key)
	assert.Equal(t, "Root", roles[0].Name)
	assert.True(t, roles[0].BuiltIn)
	assert.True(t, roles[0].Enabled)
	assert.Equal(t, BuiltInRoleAdmin, roles[1].Key)
	assert.Equal(t, 10, roles[1].Sort)
}

// seedBuiltInRoles surfaces a DB error (missing table).
func TestSeedBuiltInRoles_DBError(t *testing.T) {
	db := newMigratedDB(t)
	require.NoError(t, db.Migrator().DropTable(&model.AuthzRole{}))
	assert.Error(t, seedBuiltInRoles(db))
}

// resetBuiltInRolePolicies deletes only the role-subject policy rows, leaving
// user-subject overrides intact.
func TestResetBuiltInRolePolicies_OnlyRemovesRoleRows(t *testing.T) {
	db := newMigratedDB(t)
	// A role baseline row and a user override row.
	require.NoError(t, db.Create(&model.CasbinRule{
		Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: ResourceChannel, V2: ActionRead, V3: EffectAllow,
	}).Error)
	require.NoError(t, db.Create(&model.CasbinRule{
		Ptype: "p", V0: UserSubject(7), V1: ResourceChannel, V2: ActionWrite, V3: EffectDeny,
	}).Error)

	require.NoError(t, resetBuiltInRolePolicies(db))

	var rules []model.CasbinRule
	require.NoError(t, db.Find(&rules).Error)
	require.Len(t, rules, 1)
	assert.Equal(t, UserSubject(7), rules[0].V0, "user override must survive a role-policy reset")
}

// seedDefaultPolicies writes the admin baseline (read/operate/write) and skips
// the superuser root role (no explicit rows).
func TestSeedDefaultPolicies_WritesAdminBaselineSkipsRoot(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db)) // Init already calls seedDefaultPolicies once.

	// Verify only admin-subject rows exist, one per baseline permission.
	var adminRows int64
	require.NoError(t, db.Model(&model.CasbinRule{}).
		Where("v0 = ?", RoleSubject(BuiltInRoleAdmin)).Count(&adminRows).Error)
	assert.Equal(t, int64(len(PermissionsForRole(BuiltInRoleAdmin))), adminRows)

	var rootRows int64
	require.NoError(t, db.Model(&model.CasbinRule{}).
		Where("v0 = ?", RoleSubject(BuiltInRoleRoot)).Count(&rootRows).Error)
	assert.Equal(t, int64(0), rootRows, "root superuser must have no explicit policy rows")
}

// seedDefaultPolicies errors when the enforcer is not initialized.
func TestSeedDefaultPolicies_NilEnforcer(t *testing.T) {
	setEnforcerNil(t)
	err := seedDefaultPolicies()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestMigrateVeridropMenuDeniesPreservesExistingOverridesAndRunsOnce(t *testing.T) {
	db := newAuthzTestDB(t)
	legacyOnly := newRule("p", []string{UserSubject(101), ResourceAdminMenuChannels, ActionView, EffectDeny})
	legacyWithAllow := newRule("p", []string{UserSubject(102), ResourceAdminMenuChannels, ActionView, EffectDeny})
	existingAllow := newRule("p", []string{UserSubject(102), ResourceAdminMenuVeridropDetection, ActionView, EffectAllow})
	require.NoError(t, db.Create(&[]model.CasbinRule{legacyOnly, legacyWithAllow, existingAllow}).Error)

	require.NoError(t, migrateVeridropMenuDenies(db))

	var migrated model.CasbinRule
	require.NoError(t, db.Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?", "p", UserSubject(101), ResourceAdminMenuVeridropDetection, ActionView,
	).First(&migrated).Error)
	require.Equal(t, EffectDeny, migrated.V3)
	var preserved []model.CasbinRule
	require.NoError(t, db.Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?", "p", UserSubject(102), ResourceAdminMenuVeridropDetection, ActionView,
	).Find(&preserved).Error)
	require.Len(t, preserved, 1)
	require.Equal(t, EffectAllow, preserved[0].V3)

	var markerCount int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p", veridropMenuDenyMigrationKey, ResourceAdminMenuVeridropDetection, ActionView, EffectAllow,
	).Count(&markerCount).Error)
	require.EqualValues(t, 1, markerCount)
	require.NoError(t, db.Where("id = ?", migrated.Id).Delete(&model.CasbinRule{}).Error)
	require.NoError(t, migrateVeridropMenuDenies(db))
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?", "p", UserSubject(101), ResourceAdminMenuVeridropDetection, ActionView,
	).Count(&count).Error)
	require.Zero(t, count)
}

func TestMigrateVeridropMenuDeniesRollsBackWhenMarkerWriteFails(t *testing.T) {
	db := newAuthzTestDB(t)
	legacy := newRule("p", []string{UserSubject(103), ResourceAdminMenuChannels, ActionView, EffectDeny})
	require.NoError(t, db.Create(&legacy).Error)
	callbackName := "test:fail_veridrop_menu_migration_marker"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if rule, ok := tx.Statement.Dest.(*model.CasbinRule); ok && rule.V0 == veridropMenuDenyMigrationKey {
			tx.AddError(errors.New("forced marker write failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callbackName)) })
	require.Error(t, migrateVeridropMenuDenies(db))
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?", "p", UserSubject(103), ResourceAdminMenuVeridropDetection, ActionView,
	).Count(&count).Error)
	require.Zero(t, count)
}

func TestMigratePriceMonitorPermissionsPreservesEffectsAndExistingOverrides(t *testing.T) {
	db := newAuthzTestDB(t)
	legacyResource := SystemSettingsResource("billing.model-pricing")
	rules := []model.CasbinRule{
		newRule("p", []string{UserSubject(201), legacyResource, ActionView, EffectAllow}),
		newRule("p", []string{UserSubject(201), legacyResource, ActionEdit, EffectAllow}),
		newRule("p", []string{UserSubject(202), legacyResource, ActionView, EffectDeny}),
		newRule("p", []string{UserSubject(202), legacyResource, ActionEdit, EffectDeny}),
		newRule("p", []string{UserSubject(203), legacyResource, ActionEdit, EffectAllow}),
		newRule("p", []string{UserSubject(204), legacyResource, ActionView, EffectAllow}),
		newRule("p", []string{UserSubject(204), ResourceAdminMenuPriceMonitor, ActionView, EffectDeny}),
		newRule("p", []string{UserSubject(206), legacyResource, ActionEdit, EffectAllow}),
		newRule("p", []string{UserSubject(206), ResourceAdminMenuPriceMonitor, ActionView, EffectDeny}),
		newRule("p", []string{RoleSubject(BuiltInRoleAdmin), legacyResource, ActionView, EffectAllow}),
	}
	require.NoError(t, db.Create(&rules).Error)

	require.NoError(t, migratePriceMonitorPermissions(db))

	assertPolicyEffect := func(subject string, action string, effect string) {
		t.Helper()
		var rule model.CasbinRule
		require.NoError(t, db.Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?", "p", subject, ResourceAdminMenuPriceMonitor, action,
		).First(&rule).Error)
		assert.Equal(t, effect, rule.V3)
	}
	assertPolicyEffect(UserSubject(201), ActionView, EffectAllow)
	assertPolicyEffect(UserSubject(201), ActionEdit, EffectAllow)
	assertPolicyEffect(UserSubject(202), ActionView, EffectDeny)
	assertPolicyEffect(UserSubject(202), ActionEdit, EffectDeny)
	assertPolicyEffect(UserSubject(203), ActionView, EffectAllow)
	assertPolicyEffect(UserSubject(203), ActionEdit, EffectAllow)
	assertPolicyEffect(UserSubject(204), ActionView, EffectDeny)
	var conflictingEditRows int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?", "p", UserSubject(206), ResourceAdminMenuPriceMonitor, ActionEdit,
	).Count(&conflictingEditRows).Error)
	assert.Zero(t, conflictingEditRows)

	var roleRows int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ?", "p", RoleSubject(BuiltInRoleAdmin), ResourceAdminMenuPriceMonitor,
	).Count(&roleRows).Error)
	assert.Zero(t, roleRows)

	var markerCount int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p", priceMonitorPermissionMigrationKey, ResourceAdminMenuPriceMonitor, ActionView, EffectAllow,
	).Count(&markerCount).Error)
	assert.EqualValues(t, 1, markerCount)

	require.NoError(t, db.Where(
		"ptype = ? AND v0 = ? AND v1 = ?", "p", UserSubject(201), ResourceAdminMenuPriceMonitor,
	).Delete(&model.CasbinRule{}).Error)
	require.NoError(t, migratePriceMonitorPermissions(db))
	var migratedAgain int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ?", "p", UserSubject(201), ResourceAdminMenuPriceMonitor,
	).Count(&migratedAgain).Error)
	assert.Zero(t, migratedAgain)
}

func TestMigratePriceMonitorPermissionsRollsBackWhenMarkerWriteFails(t *testing.T) {
	db := newAuthzTestDB(t)
	legacy := newRule("p", []string{UserSubject(205), SystemSettingsResource("billing.model-pricing"), ActionView, EffectAllow})
	require.NoError(t, db.Create(&legacy).Error)
	callbackName := "test:fail_price_monitor_permission_migration_marker"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if rule, ok := tx.Statement.Dest.(*model.CasbinRule); ok && rule.V0 == priceMonitorPermissionMigrationKey {
			tx.AddError(errors.New("forced marker write failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callbackName)) })

	require.Error(t, migratePriceMonitorPermissions(db))
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ?", "p", UserSubject(205), ResourceAdminMenuPriceMonitor,
	).Count(&count).Error)
	assert.Zero(t, count)
}
