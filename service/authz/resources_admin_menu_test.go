package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminMenuResources_RegisteredWithAdminBaseline(t *testing.T) {
	resources := AdminMenuResources()
	require.Len(t, resources, 9)

	admin, ok := roleSpec(BuiltInRoleAdmin)
	require.True(t, ok)
	root, ok := roleSpec(BuiltInRoleRoot)
	require.True(t, ok)
	adminGrants := roleGrants(admin)
	rootGrants := roleGrants(root)

	// Request logs and system info default OFF for ordinary administrators —
	// they must be granted per user by root. Every other menu resource keeps
	// its historical baseline of ON for ordinary administrators.
	adminBaselineOff := map[string]bool{
		ResourceAdminMenuRequestLogs: true,
		ResourceAdminMenuSystemInfo:  true,
	}

	for _, resource := range resources {
		permission := Permission{Resource: resource, Action: ActionView}
		assert.True(t, isKnownPermission(permission))
		assert.True(t, rootGrants[resource][ActionView], "root must always be granted %s", resource)
		if adminBaselineOff[resource] {
			assert.False(t, adminGrants[resource][ActionView], "%s must default off for ordinary administrators", resource)
		} else {
			assert.True(t, adminGrants[resource][ActionView], "%s must default on for ordinary administrators", resource)
		}
	}
}

func TestAdminMenuPermissions_UserDenyAndRootBypass(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	const userID = 92001

	assert.True(t, Can(userID, common.RoleAdminUser, AdminMenuChannelsView))
	require.NoError(t, SetUserPermissions(userID, PermissionsMap{
		ResourceAdminMenuChannels: {ActionView: false},
	}))
	assert.False(t, Can(userID, common.RoleAdminUser, AdminMenuChannelsView))
	assert.True(t, Can(userID, common.RoleRootUser, AdminMenuChannelsView))
	assert.False(t, Can(userID, common.RoleCommonUser, AdminMenuChannelsView))
}

func TestAdminMenuRequestLogsAndSystemInfo_DefaultOffWithPerUserGrant(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	const userID = 92002

	// Default OFF for ordinary administrators; root always bypasses via the
	// superuser role regardless of the per-user policy.
	assert.False(t, Can(userID, common.RoleAdminUser, AdminMenuRequestLogsView))
	assert.False(t, Can(userID, common.RoleAdminUser, AdminMenuSystemInfoView))
	assert.True(t, Can(userID, common.RoleRootUser, AdminMenuRequestLogsView))
	assert.True(t, Can(userID, common.RoleRootUser, AdminMenuSystemInfoView))
	assert.False(t, Can(userID, common.RoleCommonUser, AdminMenuRequestLogsView))

	// Root grants only request_logs to this administrator; system_info stays off.
	require.NoError(t, SetUserPermissions(userID, PermissionsMap{
		ResourceAdminMenuRequestLogs: {ActionView: true},
	}))
	assert.True(t, Can(userID, common.RoleAdminUser, AdminMenuRequestLogsView))
	assert.False(t, Can(userID, common.RoleAdminUser, AdminMenuSystemInfoView))

	// Revoking returns to the default-off baseline.
	require.NoError(t, SetUserPermissions(userID, PermissionsMap{
		ResourceAdminMenuRequestLogs: {ActionView: false},
	}))
	assert.False(t, Can(userID, common.RoleAdminUser, AdminMenuRequestLogsView))
}
