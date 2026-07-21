package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
