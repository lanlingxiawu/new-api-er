package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminMenuResources_RegisteredWithAdminBaseline(t *testing.T) {
	resources := AdminMenuResources()
	require.Len(t, resources, 11)
	assert.Equal(t, []string{
		ResourceAdminMenuChannels,
		ResourceAdminMenuModels,
		ResourceAdminMenuUsers,
		ResourceAdminMenuRedemptionCodes,
		ResourceAdminMenuSubscriptions,
		ResourceAdminMenuVeridropDetection,
		ResourceAdminMenuPriceMonitor,
		ResourceAdminMenuEmployees,
		ResourceAdminMenuBusinessOverview,
		ResourceAdminMenuRequestLogs,
		ResourceAdminMenuSystemInfo,
	}, resources)

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
		ResourceAdminMenuRequestLogs:  true,
		ResourceAdminMenuSystemInfo:   true,
		ResourceAdminMenuPriceMonitor: true,
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

	actions := catalogActions(ResourceAdminMenuPriceMonitor)
	require.Len(t, actions, 2)
	assert.Equal(t, ActionView, actions[0].Action)
	assert.Equal(t, ActionEdit, actions[1].Action)
	assert.False(t, adminGrants[ResourceAdminMenuPriceMonitor][ActionEdit])
	assert.True(t, rootGrants[ResourceAdminMenuPriceMonitor][ActionEdit])

	for _, resource := range Catalog() {
		if resource.Resource == ResourceAdminMenuVeridropDetection {
			assert.Equal(t, "Authenticity Detection", resource.LabelKey)
			assert.Equal(t, 6, resource.Sort)
		}
	}
}

func TestNormalizePriceMonitorMenuActions(t *testing.T) {
	assert.Equal(t, map[string]bool{ActionView: true, ActionEdit: true},
		normalizePermissionActions(ResourceAdminMenuPriceMonitor, map[string]bool{ActionEdit: true}))
	assert.Equal(t, map[string]bool{ActionView: false, ActionEdit: false},
		normalizePermissionActions(ResourceAdminMenuPriceMonitor, map[string]bool{ActionView: false, ActionEdit: true}))
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
