package authz

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Roles returns descriptors for both built-in roles with their baseline grant
// matrices. This is the canonical allow/deny matrix at the role level.
func TestRoles_DescriptorsAndGrantMatrix(t *testing.T) {
	roles := Roles()
	require.Len(t, roles, 2)

	byKey := make(map[string]RoleDescriptor)
	for _, r := range roles {
		byKey[r.Key] = r
	}

	root, ok := byKey[BuiltInRoleRoot]
	require.True(t, ok)
	admin, ok := byKey[BuiltInRoleAdmin]
	require.True(t, ok)

	assert.True(t, root.BuiltIn)
	assert.True(t, root.Superuser)
	assert.True(t, admin.BuiltIn)
	assert.False(t, admin.Superuser)

	// Root (superuser) is granted EVERY channel action.
	assert.True(t, root.Grants[ResourceChannel][ActionRead])
	assert.True(t, root.Grants[ResourceChannel][ActionOperate])
	assert.True(t, root.Grants[ResourceChannel][ActionWrite])
	assert.True(t, root.Grants[ResourceChannel][ActionSensitiveWrite])
	assert.True(t, root.Grants[ResourceChannel][ActionSecretView])

	// Admin is granted only the baseline (read/operate/write).
	assert.True(t, admin.Grants[ResourceChannel][ActionRead])
	assert.True(t, admin.Grants[ResourceChannel][ActionOperate])
	assert.True(t, admin.Grants[ResourceChannel][ActionWrite])
	// SECURITY: admin must be DENIED the sensitive actions at baseline.
	assert.False(t, admin.Grants[ResourceChannel][ActionSensitiveWrite])
	assert.False(t, admin.Grants[ResourceChannel][ActionSecretView])
}

// roleGrants: superuser sets every action true regardless of DefaultRoles;
// non-superuser follows DefaultRoles membership.
func TestRoleGrants_SuperuserVsBaseline(t *testing.T) {
	superSpec := RoleSpec{Key: "super", Superuser: true}
	grants := roleGrants(superSpec)
	for _, a := range catalogActions(ResourceChannel) {
		assert.True(t, grants[ResourceChannel][a.Action], "superuser must be granted %s", a.Action)
	}

	adminSpec, ok := roleSpec(BuiltInRoleAdmin)
	require.True(t, ok)
	adminGrants := roleGrants(adminSpec)
	assert.True(t, adminGrants[ResourceChannel][ActionRead])
	assert.False(t, adminGrants[ResourceChannel][ActionSensitiveWrite])

	// A non-superuser role with a key that matches no DefaultRoles gets all false.
	strangerGrants := roleGrants(RoleSpec{Key: "stranger", Superuser: false})
	for _, a := range catalogActions(ResourceChannel) {
		assert.False(t, strangerGrants[ResourceChannel][a.Action], "stranger must be denied %s", a.Action)
	}
}

// roleSpec lookup: found (both built-ins) and not-found.
func TestRoleSpec(t *testing.T) {
	root, ok := roleSpec(BuiltInRoleRoot)
	require.True(t, ok)
	assert.Equal(t, BuiltInRoleRoot, root.Key)
	assert.True(t, root.Superuser)

	admin, ok := roleSpec(BuiltInRoleAdmin)
	require.True(t, ok)
	assert.Equal(t, BuiltInRoleAdmin, admin.Key)

	_, ok = roleSpec("nope")
	assert.False(t, ok)
	_, ok = roleSpec("")
	assert.False(t, ok)
}

// isSuperuserRole: root true, admin false, unknown false (fail closed).
func TestIsSuperuserRole(t *testing.T) {
	assert.True(t, isSuperuserRole(BuiltInRoleRoot))
	assert.False(t, isSuperuserRole(BuiltInRoleAdmin))
	assert.False(t, isSuperuserRole("unknown"), "unknown role must not be a superuser")
	assert.False(t, isSuperuserRole(""))
}
