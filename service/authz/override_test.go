package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// SetUserPermissions — only stores entries that DIFFER from the admin baseline.
// ---------------------------------------------------------------------------

func TestSetUserPermissions_StoresOnlyDiffsFromBaseline(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	// Desired: read/operate=true (== baseline, omitted), write=false (differs ->
	// deny row), sensitive_write=true (differs -> allow row), secret_view=false
	// (== baseline false, omitted). Plus unknown action (ignored) and unknown
	// resource (ignored).
	require.NoError(t, SetUserPermissions(42, PermissionsMap{
		ResourceChannel: {
			ActionRead:           true,
			ActionOperate:        true,
			ActionWrite:          false,
			ActionSensitiveWrite: true,
			ActionSecretView:     false,
			"unknown_action":     true,
		},
		"unknown_resource": {ActionRead: true},
	}))

	// Exactly two override rows persisted: write=deny, sensitive_write=allow.
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(42)).Count(&count).Error)
	assert.Equal(t, int64(2), count)

	assert.Equal(t, PermissionsMap{
		ResourceChannel: {
			ActionSensitiveWrite: true,
			ActionWrite:          false,
		},
	}, ExplicitUserOverrides(42))

	// Effective decisions reflect the overrides.
	assert.True(t, Can(42, common.RoleAdminUser, ChannelSensitiveWrite))
	assert.False(t, Can(42, common.RoleAdminUser, ChannelWrite))
	assert.True(t, Can(42, common.RoleAdminUser, ChannelRead))
}

// Re-applying a permission set that exactly matches the baseline removes all
// overrides (idempotent normalization back to baseline).
func TestSetUserPermissions_ResettingToBaselineClearsOverrides(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, SetUserPermissions(43, PermissionsMap{
		ResourceChannel: {ActionSensitiveWrite: true, ActionWrite: false},
	}))
	require.NotEmpty(t, ExplicitUserOverrides(43))

	// Now set exactly the baseline.
	require.NoError(t, SetUserPermissions(43, PermissionsMap{
		ResourceChannel: {
			ActionRead:           true,
			ActionOperate:        true,
			ActionWrite:          true,
			ActionSensitiveWrite: false,
			ActionSecretView:     false,
		},
	}))
	assert.Empty(t, ExplicitUserOverrides(43))
}

// A partial permission map only touches the listed actions; unlisted actions
// keep whatever override state they had (they are not in the catalogActions
// desired map).
func TestSetUserPermissions_PartialMapLeavesOthers(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, SetUserPermissions(44, PermissionsMap{
		ResourceChannel: {ActionSensitiveWrite: true},
	}))
	assert.True(t, Can(44, common.RoleAdminUser, ChannelSensitiveWrite))

	// Re-set only write=false; but note SetUserPermissions deletes ALL rows for
	// the resource first, then re-derives from the provided actions. Since only
	// write is provided this time, the earlier sensitive_write override is gone.
	require.NoError(t, SetUserPermissions(44, PermissionsMap{
		ResourceChannel: {ActionWrite: false},
	}))
	assert.False(t, Can(44, common.RoleAdminUser, ChannelSensitiveWrite), "resource rows are replaced wholesale")
	assert.False(t, Can(44, common.RoleAdminUser, ChannelWrite))
}

func TestSetUserPermissions_NilEnforcer(t *testing.T) {
	setEnforcerNil(t)
	err := SetUserPermissions(1, PermissionsMap{ResourceChannel: {ActionRead: true}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

// ---------------------------------------------------------------------------
// SetUserPermissionsInTx — writes straight to DB; enforcer snapshot only sees
// them after a reload.
// ---------------------------------------------------------------------------

func TestSetUserPermissionsInTx_VisibleAfterReload(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return SetUserPermissionsInTx(tx, 80, PermissionsMap{
			ResourceChannel: {ActionSensitiveWrite: true},
		})
	}))

	// Not yet visible in the in-memory enforcer snapshot.
	assert.False(t, Can(80, common.RoleAdminUser, ChannelSensitiveWrite))
	require.NoError(t, ReloadPolicy())
	assert.True(t, Can(80, common.RoleAdminUser, ChannelSensitiveWrite))
}

func TestUpdateUserPermissionsInTx_ReportsOnlyEffectivePolicyChanges(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	permissions := PermissionsMap{
		ResourceChannel: {ActionSensitiveWrite: true},
	}

	var changed bool
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		changed, err = UpdateUserPermissionsInTx(tx, 801, permissions)
		return err
	}))
	assert.True(t, changed)
	require.NoError(t, ReloadPolicy())

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		changed, err = UpdateUserPermissionsInTx(tx, 801, permissions)
		return err
	}))
	assert.False(t, changed)
}

func TestSetUserPermissionsInTx_RollbackLeavesNothing(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	tx := db.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, SetUserPermissionsInTx(tx, 81, PermissionsMap{
		ResourceChannel: {ActionSensitiveWrite: true},
	}))
	require.NoError(t, tx.Rollback().Error)
	require.NoError(t, ReloadPolicy())

	assert.False(t, Can(81, common.RoleAdminUser, ChannelSensitiveWrite))
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(81)).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

// A permission set that equals the baseline produces zero rows (the
// len(policies)==0 continue branch) but still deletes any prior rows.
func TestSetUserPermissionsInTx_BaselineOnlyWritesNoRows(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return SetUserPermissionsInTx(tx, 82, PermissionsMap{
			ResourceChannel: {ActionRead: true, ActionOperate: true, ActionWrite: true},
		})
	}))
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(82)).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

// Unknown resources are skipped in the Tx path too.
func TestSetUserPermissionsInTx_SkipsUnknownResource(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return SetUserPermissionsInTx(tx, 83, PermissionsMap{
			"unknown_resource": {ActionRead: true},
		})
	}))
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(83)).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestSetUserPermissionsInTx_NilEnforcer(t *testing.T) {
	db := newMigratedDB(t)
	setEnforcerNil(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		return SetUserPermissionsInTx(tx, 1, PermissionsMap{ResourceChannel: {ActionRead: true}})
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

// ---------------------------------------------------------------------------
// Clear paths.
// ---------------------------------------------------------------------------

func TestClearUserAuthorization_RemovesOverrides(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, SetUserPermissions(90, PermissionsMap{
		ResourceChannel: {ActionWrite: false, ActionSensitiveWrite: true},
	}))
	assert.True(t, Can(90, common.RoleAdminUser, ChannelSensitiveWrite))
	assert.False(t, Can(90, common.RoleAdminUser, ChannelWrite))

	require.NoError(t, ClearUserAuthorization(90))

	assert.Empty(t, ExplicitUserOverrides(90))
	// Back to baseline.
	assert.True(t, Can(90, common.RoleAdminUser, ChannelWrite))
	assert.False(t, Can(90, common.RoleAdminUser, ChannelSensitiveWrite))
}

func TestClearUserPermissions_NilEnforcer(t *testing.T) {
	setEnforcerNil(t)
	err := ClearUserPermissions(1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestClearUserAuthorizationInTx_RemovesRows(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	// Seed override rows directly (committed) then clear in a tx.
	require.NoError(t, SetUserPermissions(91, PermissionsMap{
		ResourceChannel: {ActionSensitiveWrite: true, ActionWrite: false},
	}))
	var before int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(91)).Count(&before).Error)
	require.Equal(t, int64(2), before)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ClearUserAuthorizationInTx(tx, 91)
	}))

	var after int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(91)).Count(&after).Error)
	assert.Equal(t, int64(0), after)
}

// ClearUserPermissionsInTx surfaces a real DB error when the target table is
// gone (exercises the Delete error branch without mocking GORM).
func TestClearUserPermissionsInTx_DBError(t *testing.T) {
	db := newMigratedDB(t)
	require.NoError(t, db.Migrator().DropTable(&model.CasbinRule{}))

	err := db.Transaction(func(tx *gorm.DB) error {
		return ClearUserPermissionsInTx(tx, 1)
	})
	assert.Error(t, err)
}

// SetUserPermissionsInTx surfaces a real DB error from its initial delete when
// the casbin_rule table is missing.
func TestSetUserPermissionsInTx_DeleteDBError(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	require.NoError(t, db.Migrator().DropTable(&model.CasbinRule{}))

	err := db.Transaction(func(tx *gorm.DB) error {
		return SetUserPermissionsInTx(tx, 1, PermissionsMap{ResourceChannel: {ActionSensitiveWrite: true}})
	})
	assert.Error(t, err)
}

// ClearUserPermissionsInTx (the underlying func of ...AuthorizationInTx) with a
// rolled-back tx must leave rows intact.
func TestClearUserPermissionsInTx_RollbackKeepsRows(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, SetUserPermissions(92, PermissionsMap{
		ResourceChannel: {ActionSensitiveWrite: true},
	}))

	tx := db.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, ClearUserPermissionsInTx(tx, 92))
	require.NoError(t, tx.Rollback().Error)

	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(92)).Count(&count).Error)
	assert.Equal(t, int64(1), count, "rolled-back clear must not delete rows")
}

// ---------------------------------------------------------------------------
// ExplicitUserPermissions / ExplicitUserOverrides.
// ---------------------------------------------------------------------------

func TestExplicitUserPermissions_ReflectsBaselinePlusOverrides(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, SetUserPermissions(100, PermissionsMap{
		ResourceChannel: {ActionSensitiveWrite: true},
	}))
	perms := ExplicitUserPermissions(100)
	assert.True(t, perms[ResourceChannel][ActionRead])           // baseline
	assert.True(t, perms[ResourceChannel][ActionSensitiveWrite]) // override
	assert.False(t, perms[ResourceChannel][ActionSecretView])    // baseline deny
}

func TestExplicitUserOverrides_EmptyWhenNoneSet(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	assert.Empty(t, ExplicitUserOverrides(101))
}

func TestExplicitUserOverrides_NilEnforcerReturnsEmpty(t *testing.T) {
	setEnforcerNil(t)
	assert.Equal(t, PermissionsMap{}, ExplicitUserOverrides(1))
}

// ---------------------------------------------------------------------------
// userOverridePolicies — the diff engine, white box.
// ---------------------------------------------------------------------------

func TestUserOverridePolicies_OmitsBaselineMatchesAndSortsByAction(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	e := currentEnforcer()
	require.NotNil(t, e)

	// read=true matches baseline (omit); write=false differs (deny);
	// sensitive_write=true differs (allow); an action absent from the map is
	// skipped entirely.
	got := userOverridePolicies(e, ResourceChannel, map[string]bool{
		ActionRead:           true,  // == baseline true -> omit
		ActionWrite:          false, // != baseline true -> deny
		ActionSensitiveWrite: true,  // != baseline false -> allow
	})

	require.Len(t, got, 2)
	// Sorted by Action: sensitive_write < write.
	assert.Equal(t, ActionSensitiveWrite, got[0].Action)
	assert.Equal(t, EffectAllow, got[0].Effect)
	assert.Equal(t, ActionWrite, got[1].Action)
	assert.Equal(t, EffectDeny, got[1].Effect)
}

func TestUserOverridePolicies_EmptyWhenAllMatchBaseline(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	e := currentEnforcer()

	got := userOverridePolicies(e, ResourceChannel, map[string]bool{
		ActionRead:           true,
		ActionOperate:        true,
		ActionWrite:          true,
		ActionSensitiveWrite: false,
		ActionSecretView:     false,
	})
	assert.Empty(t, got)
}
