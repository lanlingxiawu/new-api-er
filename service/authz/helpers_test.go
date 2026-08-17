package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newAuthzTestDB returns a fresh in-memory SQLite database with the authz
// tables migrated. It also flips common.IsMasterNode to true (restored on
// cleanup) so Init seeds roles/policies, and snapshots the global enforcer so a
// test that calls Init cannot leak its enforcer into a later test.
//
// SQLite :memory: is used as the CI fallback (Rule 15.5). The raw SQL executed
// by this package is limited to equality predicates on ptype/v0/v1/v2/v3 and a
// `v0 IN (?)` clause — all standard and identical across SQLite/MySQL/PostgreSQL
// (no JSON operators, no dialect-specific functions), so the SQLite fallback
// exercises the same code paths a real MySQL/PG run would.
func newAuthzTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	snapshotMasterNode(t)
	common.IsMasterNode = true
	snapshotEnforcer(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// Pin to a single connection so the in-memory schema is not lost when the
	// pool recycles connections.
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	return db
}

// newMigratedDB returns an in-memory SQLite DB with the authz tables migrated
// but WITHOUT touching IsMasterNode or the global enforcer. Use it for adapter
// unit tests that operate on a *gormAdapter directly.
func newMigratedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}, &model.Option{}))
	return db
}

// snapshotEnforcer captures the current global enforcer and restores it on
// cleanup. Every test that mutates the enforcer (via Init or by setting it nil)
// must call this to keep the sequential tests isolated from one another.
func snapshotEnforcer(t *testing.T) {
	t.Helper()
	enforcerMu.RLock()
	saved := enforcer
	enforcerMu.RUnlock()
	t.Cleanup(func() {
		enforcerMu.Lock()
		enforcer = saved
		enforcerMu.Unlock()
	})
}

// setEnforcerNil forces the global enforcer to nil (after snapshotting) so a
// test can exercise the "enforcer not initialized" defensive branches.
func setEnforcerNil(t *testing.T) {
	t.Helper()
	snapshotEnforcer(t)
	enforcerMu.Lock()
	enforcer = nil
	enforcerMu.Unlock()
}

// snapshotRegistry captures the package-global resource registry and restores
// it on cleanup. Required for any test that calls RegisterResource, since the
// registry is process-global and would otherwise pollute sibling tests.
func snapshotRegistry(t *testing.T) {
	t.Helper()
	saved := append([]ResourceDefinition(nil), registry...)
	t.Cleanup(func() {
		registry = saved
	})
}

// snapshotMasterNode restores common.IsMasterNode on cleanup.
func snapshotMasterNode(t *testing.T) {
	t.Helper()
	was := common.IsMasterNode
	t.Cleanup(func() {
		common.IsMasterNode = was
	})
}

// snapshotResolveSubjectRoles restores the resolveSubjectRoles hook on cleanup.
func snapshotResolveSubjectRoles(t *testing.T) {
	t.Helper()
	saved := resolveSubjectRoles
	t.Cleanup(func() {
		resolveSubjectRoles = saved
	})
}
