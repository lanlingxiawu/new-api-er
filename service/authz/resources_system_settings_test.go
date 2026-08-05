package authz

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemSettingsResources_AllScopesRegistered(t *testing.T) {
	scopes := SystemSettingsScopes()
	require.Len(t, scopes, 53)

	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		_, duplicated := seen[scope]
		assert.False(t, duplicated, "scope %s must only be registered once", scope)
		seen[scope] = struct{}{}

		resource := SystemSettingsResource(scope)
		assert.True(t, strings.HasPrefix(resource, SystemSettingsResourcePrefix))
		assert.True(t, isKnownPermission(SystemSettingsView(scope)))
		assert.True(t, isKnownPermission(SystemSettingsEdit(scope)))

		actions := catalogActions(resource)
		require.Len(t, actions, 2)
		assert.Equal(t, ActionView, actions[0].Action)
		assert.Equal(t, ActionEdit, actions[1].Action)
		assert.Empty(t, actions[0].DefaultRoles)
		assert.Empty(t, actions[1].DefaultRoles)
	}
}

func TestSystemSettingsResources_CatalogMetadata(t *testing.T) {
	resourceName := SystemSettingsResource("operations.performance")
	for _, resource := range Catalog() {
		if resource.Resource != resourceName {
			continue
		}
		assert.Equal(t, "operations", resource.Group)
		assert.Equal(t, "Operations", resource.GroupLabelKey)
		assert.Equal(t, "Performance", resource.LabelKey)
		assert.Greater(t, resource.Sort, 0)
		return
	}
	require.FailNowf(t, "resource not found", "resource %s not found", resourceName)
}

func TestSystemSettingsPermissions_AdminBaselineDeniedRootGranted(t *testing.T) {
	admin, ok := roleSpec(BuiltInRoleAdmin)
	require.True(t, ok)
	root, ok := roleSpec(BuiltInRoleRoot)
	require.True(t, ok)

	adminGrants := roleGrants(admin)
	rootGrants := roleGrants(root)
	for _, scope := range SystemSettingsScopes() {
		resource := SystemSettingsResource(scope)
		assert.False(t, adminGrants[resource][ActionView])
		assert.False(t, adminGrants[resource][ActionEdit])
		assert.True(t, rootGrants[resource][ActionView])
		assert.True(t, rootGrants[resource][ActionEdit])
	}
}

func TestNormalizeSystemSettingsActions(t *testing.T) {
	resource := SystemSettingsResource("site.notice")

	assert.Equal(t, map[string]bool{ActionView: true, ActionEdit: true},
		normalizePermissionActions(resource, map[string]bool{ActionEdit: true}))
	assert.Equal(t, map[string]bool{ActionView: false, ActionEdit: false},
		normalizePermissionActions(resource, map[string]bool{ActionView: false, ActionEdit: true}))
	assert.Equal(t, map[string]bool{ActionView: true, ActionEdit: false},
		normalizePermissionActions(resource, map[string]bool{ActionView: true, ActionEdit: false}))

	channel := map[string]bool{ActionRead: true}
	assert.Equal(t, channel, normalizePermissionActions(ResourceChannel, channel))
}
