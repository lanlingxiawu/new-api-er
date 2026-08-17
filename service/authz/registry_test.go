package authz

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The channel resource is registered by resources_channel.go's init(). These
// tests treat that as the fixed baseline registry.

func TestRegistry_ChannelResourceRegistered(t *testing.T) {
	require.True(t, isKnownResource(ResourceChannel))
	actions := catalogActions(ResourceChannel)
	require.Len(t, actions, 5)

	got := make(map[string]bool)
	for _, a := range actions {
		got[a.Action] = true
	}
	for _, want := range []string{ActionRead, ActionOperate, ActionWrite, ActionSensitiveWrite, ActionSecretView} {
		assert.True(t, got[want], "expected action %s registered", want)
	}
}

// RegisterResource appends to the global registry. Snapshot/restore so we don't
// pollute sibling tests.
func TestRegisterResource_Appends(t *testing.T) {
	snapshotRegistry(t)
	before := len(registry)

	RegisterResource(ResourceDefinition{
		Resource: "widget",
		LabelKey: "Widget",
		Actions: []ActionDefinition{
			{Action: "read", DefaultRoles: []string{BuiltInRoleAdmin}},
		},
	})

	assert.Len(t, registry, before+1)
	assert.True(t, isKnownResource("widget"))
	assert.True(t, isKnownPermission(Permission{Resource: "widget", Action: "read"}))
}

// Catalog returns a deep copy — mutating the returned slice/actions must not
// affect the underlying registry.
func TestCatalog_ReturnsDeepCopy(t *testing.T) {
	catalog := Catalog()
	require.NotEmpty(t, catalog)

	// Locate channel entry.
	var idx = -1
	for i, r := range catalog {
		if r.Resource == ResourceChannel {
			idx = i
			break
		}
	}
	require.GreaterOrEqual(t, idx, 0)

	// Mutate the copy.
	origLen := len(catalog[idx].Actions)
	catalog[idx].Actions = append(catalog[idx].Actions, ActionDefinition{Action: "tampered"})
	catalog[idx].LabelKey = "tampered-label"
	catalog[idx].Group = "tampered-group"

	// Registry must be untouched.
	assert.Len(t, catalogActions(ResourceChannel), origLen)
	assert.False(t, isKnownPermission(Permission{Resource: ResourceChannel, Action: "tampered"}))
}

func TestCatalog_ActionSliceIsIndependent(t *testing.T) {
	catalog := Catalog()
	// Mutate an action element in the copy.
	for i := range catalog {
		if catalog[i].Resource == ResourceChannel {
			catalog[i].Actions[0].Action = "mutated"
		}
	}
	// Original registry still has the real action name.
	found := false
	for _, a := range catalogActions(ResourceChannel) {
		if a.Action == "mutated" {
			found = true
		}
	}
	assert.False(t, found, "Catalog's action copy leaked back into the registry")
}

// AllPermissions must enumerate every registered permission exactly once.
func TestAllPermissions_EnumeratesEveryAction(t *testing.T) {
	perms := AllPermissions()
	// Count expected from registry.
	expected := 0
	for _, r := range registry {
		expected += len(r.Actions)
	}
	assert.Len(t, perms, expected)

	// Every channel action present.
	set := make(map[Permission]bool)
	for _, p := range perms {
		set[p] = true
	}
	assert.True(t, set[ChannelRead])
	assert.True(t, set[ChannelOperate])
	assert.True(t, set[ChannelWrite])
	assert.True(t, set[ChannelSensitiveWrite])
	assert.True(t, set[ChannelSecretView])
}

// PermissionsForRole filters actions by their DefaultRoles. Admin gets the
// channel operational baseline plus the currently assignable admin menus; the
// sensitive channel actions are NOT baseline for admin.
func TestPermissionsForRole_AdminBaseline(t *testing.T) {
	perms := PermissionsForRole(BuiltInRoleAdmin)

	set := make(map[Permission]bool)
	for _, p := range perms {
		set[p] = true
	}
	assert.True(t, set[ChannelRead], "admin baseline must include read")
	assert.True(t, set[ChannelOperate], "admin baseline must include operate")
	assert.True(t, set[ChannelWrite], "admin baseline must include write")
	// SECURITY: these must NOT be part of the admin baseline.
	assert.False(t, set[ChannelSensitiveWrite], "admin baseline must NOT include sensitive_write")
	assert.False(t, set[ChannelSecretView], "admin baseline must NOT include secret_view")
	// Request logs and system info are sensitive enough that they are NOT part
	// of the ordinary-administrator baseline; root must grant them per user.
	notBaselineForAdmin := map[string]bool{
		ResourceAdminMenuPriceMonitor: true,
		ResourceAdminMenuRequestLogs:  true,
		ResourceAdminMenuSystemInfo:   true,
	}
	baselineMenuCount := 0
	for _, resource := range AdminMenuResources() {
		permission := Permission{Resource: resource, Action: ActionView}
		if notBaselineForAdmin[resource] {
			assert.False(t, set[permission], "%s must NOT be part of the admin baseline", resource)
			continue
		}
		assert.True(t, set[permission], "%s must be part of the admin baseline", resource)
		baselineMenuCount++
	}
	assert.Len(t, perms, 3+baselineMenuCount)
}

// root is a superuser and receives NO explicit DefaultRoles entries — its access
// is granted implicitly by the superuser flag, not by baseline policy rows.
func TestPermissionsForRole_RootHasNoExplicitBaseline(t *testing.T) {
	perms := PermissionsForRole(BuiltInRoleRoot)
	assert.Empty(t, perms, "root must have no explicit baseline (superuser is implicit)")
}

// An unknown role key matches nothing (deny-by-default).
func TestPermissionsForRole_UnknownRoleEmpty(t *testing.T) {
	assert.Empty(t, PermissionsForRole("nonexistent-role"))
	assert.Empty(t, PermissionsForRole(""))
}

// actionHasRole condition coverage: present, absent, empty DefaultRoles.
func TestActionHasRole(t *testing.T) {
	withAdmin := ActionDefinition{Action: "x", DefaultRoles: []string{BuiltInRoleAdmin}}
	multi := ActionDefinition{Action: "y", DefaultRoles: []string{"a", "b", BuiltInRoleAdmin}}
	none := ActionDefinition{Action: "z"}

	assert.True(t, actionHasRole(withAdmin, BuiltInRoleAdmin))
	assert.False(t, actionHasRole(withAdmin, BuiltInRoleRoot))
	assert.True(t, actionHasRole(multi, BuiltInRoleAdmin), "match at non-first index")
	assert.False(t, actionHasRole(multi, "c"))
	assert.False(t, actionHasRole(none, BuiltInRoleAdmin), "empty DefaultRoles never matches")
}

// isKnownResource decision coverage.
func TestIsKnownResource(t *testing.T) {
	assert.True(t, isKnownResource(ResourceChannel))
	assert.False(t, isKnownResource("unknown"))
	assert.False(t, isKnownResource(""))
}

// catalogActions: known resource returns actions, unknown returns nil.
func TestCatalogActions(t *testing.T) {
	assert.NotEmpty(t, catalogActions(ResourceChannel))
	assert.Nil(t, catalogActions("unknown"))
	assert.Nil(t, catalogActions(""))
}

// isKnownPermission: full decision/condition coverage — resource+action match,
// resource match but action mismatch, resource mismatch.
func TestIsKnownPermission(t *testing.T) {
	assert.True(t, isKnownPermission(ChannelRead))
	assert.True(t, isKnownPermission(ChannelSecretView))
	// Resource matches, action does not.
	assert.False(t, isKnownPermission(Permission{Resource: ResourceChannel, Action: "bogus"}))
	// Resource does not match at all.
	assert.False(t, isKnownPermission(Permission{Resource: "bogus", Action: ActionRead}))
	// Both empty.
	assert.False(t, isKnownPermission(Permission{}))
}
