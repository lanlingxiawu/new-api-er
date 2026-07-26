package common

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- validate.go ----

func TestValidateInitialized(t *testing.T) {
	require.NotNil(t, Validate)
	type form struct {
		Email string `validate:"required,email"`
	}
	assert.Error(t, Validate.Struct(form{Email: "not-an-email"}))
	assert.NoError(t, Validate.Struct(form{Email: "a@b.com"}))
}

// ---- constants.go helpers ----

func TestIsValidateRole(t *testing.T) {
	assert.True(t, IsValidateRole(RoleGuestUser))
	assert.True(t, IsValidateRole(RoleCommonUser))
	assert.True(t, IsValidateRole(RoleAdminUser))
	assert.True(t, IsValidateRole(RoleRootUser))
	assert.False(t, IsValidateRole(5))
	assert.False(t, IsValidateRole(-1))
}

// ---- database.go ----

func TestDatabaseTypeAccessors(t *testing.T) {
	mainOrig, logOrig := MainDatabaseType(), LogDatabaseType()
	t.Cleanup(func() { SetDatabaseTypes(mainOrig, logOrig) })

	SetMainDatabaseType(DatabaseTypeMySQL)
	assert.Equal(t, DatabaseTypeMySQL, MainDatabaseType())
	assert.True(t, UsingMainDatabase(DatabaseTypeMySQL))
	assert.False(t, UsingMainDatabase(DatabaseTypePostgreSQL))

	SetLogDatabaseType(DatabaseTypePostgreSQL)
	assert.Equal(t, DatabaseTypePostgreSQL, LogDatabaseType())
	assert.True(t, UsingLogDatabase(DatabaseTypePostgreSQL))
	assert.False(t, UsingLogDatabase(DatabaseTypeSQLite))

	SetDatabaseTypes(DatabaseTypeSQLite, DatabaseTypeClickHouse)
	assert.Equal(t, DatabaseTypeSQLite, MainDatabaseType())
	assert.Equal(t, DatabaseTypeClickHouse, LogDatabaseType())
}

// ---- node_identity.go ----

func TestNodeIdentity(t *testing.T) {
	nameOrig, srcOrig, manOrig := NodeName, NodeNameSource, NodeNameManuallyConfigured
	envOrig, hadEnv := os.LookupEnv("NODE_NAME")
	t.Cleanup(func() {
		NodeName, NodeNameSource, NodeNameManuallyConfigured = nameOrig, srcOrig, manOrig
		if hadEnv {
			_ = os.Setenv("NODE_NAME", envOrig)
		} else {
			_ = os.Unsetenv("NODE_NAME")
		}
	})

	// explicit NODE_NAME env => manual source
	require.NoError(t, os.Setenv("NODE_NAME", "node-a"))
	initNodeNameIdentity()
	id := GetNodeIdentity()
	assert.Equal(t, "node-a", id.Name)
	assert.Equal(t, NodeNameSourceManual, id.Source)
	assert.True(t, id.ManuallyConfigured)
	assert.False(t, id.ShouldConfigureManually)

	// unset => hostname fallback, should configure manually
	require.NoError(t, os.Unsetenv("NODE_NAME"))
	initNodeNameIdentity()
	id = GetNodeIdentity()
	assert.Equal(t, NodeNameSourceHostname, id.Source)
	assert.False(t, id.ManuallyConfigured)
	assert.True(t, id.ShouldConfigureManually)
}

// ---- performance_config.go ----

func TestPerformanceMonitorConfig(t *testing.T) {
	orig := GetPerformanceMonitorConfig()
	t.Cleanup(func() { SetPerformanceMonitorConfig(orig) })

	cfg := PerformanceMonitorConfig{Enabled: false, CPUThreshold: 50, MemoryThreshold: 60, DiskThreshold: 70}
	SetPerformanceMonitorConfig(cfg)
	assert.Equal(t, cfg, GetPerformanceMonitorConfig())
}

// ---- request_body_limit.go ----

func TestGetAnonymousRequestBodyLimitBytes(t *testing.T) {
	orig := constant.AnonymousRequestBodyLimitKB
	t.Cleanup(func() { constant.AnonymousRequestBodyLimitKB = orig })

	constant.AnonymousRequestBodyLimitKB = 4
	assert.Equal(t, int64(4)<<10, GetAnonymousRequestBodyLimitBytes())

	// zero is a valid explicit configured value (>= 0)
	constant.AnonymousRequestBodyLimitKB = 0
	assert.Equal(t, int64(0), GetAnonymousRequestBodyLimitBytes())

	// negative falls back to the default
	constant.AnonymousRequestBodyLimitKB = -1
	assert.Equal(t, int64(defaultAnonymousRequestBodyLimitKB)<<10, GetAnonymousRequestBodyLimitBytes())
}
