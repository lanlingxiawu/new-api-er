package authz

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Init on a master node seeds the built-in roles and writes only the admin
// baseline policy rows (root is a superuser and gets no explicit rows). Running
// twice is idempotent.
func TestInit_MasterSeedsRolesAndBaselineIdempotently(t *testing.T) {
	db := newAuthzTestDB(t)

	require.NoError(t, Init(db))
	require.NoError(t, Init(db))

	var policyCount int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Count(&policyCount).Error)
	assert.Equal(t, int64(len(PermissionsForRole(BuiltInRoleAdmin))), policyCount)

	var roles []model.AuthzRole
	require.NoError(t, db.Order("sort asc").Find(&roles).Error)
	require.Len(t, roles, 2)
	assert.Equal(t, BuiltInRoleRoot, roles[0].Key)
	assert.Equal(t, BuiltInRoleAdmin, roles[1].Key)

	assert.True(t, Can(1, common.RoleRootUser, ChannelSensitiveWrite))
	assert.True(t, Can(2, common.RoleAdminUser, ChannelRead))
	assert.False(t, Can(2, common.RoleAdminUser, ChannelSensitiveWrite))
}

// Init on a slave (non-master) node only loads existing policies; it does not
// seed roles or write baseline rows.
func TestInit_SlaveOnlyLoadsPolicies(t *testing.T) {
	snapshotMasterNode(t)
	common.IsMasterNode = false
	snapshotEnforcer(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))

	require.NoError(t, Init(db))

	var roleCount int64
	require.NoError(t, db.Model(&model.AuthzRole{}).Count(&roleCount).Error)
	assert.Equal(t, int64(0), roleCount)
	var policyCount int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Count(&policyCount).Error)
	assert.Equal(t, int64(0), policyCount)
	assert.False(t, Can(2, common.RoleAdminUser, ChannelRead))
}

// A slave node that boots against a DB already seeded by a master picks up the
// admin baseline via LoadPolicy.
func TestInit_SlaveLoadsPreSeededPolicies(t *testing.T) {
	// First, seed as master.
	db := newAuthzTestDB(t) // sets master=true + snapshots enforcer
	require.NoError(t, Init(db))

	// Now re-init the SAME db as a slave; must load the baseline rows.
	common.IsMasterNode = false
	require.NoError(t, Init(db))
	assert.True(t, Can(2, common.RoleAdminUser, ChannelRead))
	assert.False(t, Can(2, common.RoleAdminUser, ChannelSensitiveWrite))
}

// Init returns an error when the underlying enforcer cannot load its policy
// (missing casbin_rule table). Guard the master seed by running as slave so the
// failure comes from NewSyncedEnforcer -> LoadPolicy.
func TestInit_ErrorWhenPolicyTableMissing(t *testing.T) {
	snapshotMasterNode(t)
	common.IsMasterNode = false
	snapshotEnforcer(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	// Intentionally do NOT migrate casbin_rule.

	assert.Error(t, Init(db))
}

// Init on a master node surfaces a seeding error (authz_roles table missing)
// before the enforcer is built.
func TestInit_MasterSeedError(t *testing.T) {
	snapshotMasterNode(t)
	common.IsMasterNode = true
	snapshotEnforcer(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	// Migrate only casbin_rule; leave authz_roles absent so seedBuiltInRoles fails.
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}))

	assert.Error(t, Init(db))
}

// currentEnforcer returns the live enforcer after Init and nil when unset.
func TestCurrentEnforcer(t *testing.T) {
	setEnforcerNil(t)
	assert.Nil(t, currentEnforcer())

	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	assert.NotNil(t, currentEnforcer())
}

// ReloadPolicy errors when the enforcer is nil.
func TestReloadPolicy_NilEnforcer(t *testing.T) {
	setEnforcerNil(t)
	err := ReloadPolicy()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

// ReloadPolicy re-reads rows written to the DB out-of-band (e.g. by another
// node) into the in-memory snapshot.
func TestReloadPolicy_PicksUpExternalRows(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	assert.False(t, Can(200, common.RoleAdminUser, ChannelSensitiveWrite))
	// Write an allow override straight to the DB.
	rule := newRule("p", []string{UserSubject(200), ResourceChannel, ActionSensitiveWrite, EffectAllow})
	require.NoError(t, db.Create(&rule).Error)
	require.NoError(t, ReloadPolicy())
	assert.True(t, Can(200, common.RoleAdminUser, ChannelSensitiveWrite))
}

// StartPolicySync returns immediately when frequency <= 0 (boundary values).
func TestStartPolicySync_NonPositiveFrequencyReturns(t *testing.T) {
	done := make(chan struct{})
	go func() {
		StartPolicySync(0)
		StartPolicySync(-1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartPolicySync did not return for non-positive frequency")
	}
}

// NOTE: The body of StartPolicySync's infinite for-loop (Sleep -> ReloadPolicy
// -> SysError-on-error) is intentionally left uncovered. StartPolicySync has no
// stop channel or context, so any test that entered the loop would leak a
// goroutine that keeps locking enforcerMu and calling ReloadPolicy on the shared
// global enforcer, racing every subsequent test in this package. Covering it
// safely would require a source change (a stop signal), which is out of scope
// for a tests-only task. The guard (frequency <= 0) is fully covered above.
