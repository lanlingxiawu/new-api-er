package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

// resolveSubjectRoles maps a system role integer to authz role keys. This is the
// role-hierarchy gate: common(1) < admin(10) < root(100). Boundary-value
// analysis around each threshold, plus the guest(0) lower edge.
func TestResolveSubjectRoles_HierarchyBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		systemRole int
		want       []string
	}{
		{"guest_0", common.RoleGuestUser, nil},          // 0  -> no roles (deny)
		{"common_1", common.RoleCommonUser, nil},        // 1  -> no roles (deny)
		{"just_below_admin_9", 9, nil},                  // 9  -> still below admin
		{"admin_exact_10", common.RoleAdminUser, []string{BuiltInRoleAdmin}}, // 10 -> admin
		{"between_admin_root_11", 11, []string{BuiltInRoleAdmin}},
		{"just_below_root_99", 99, []string{BuiltInRoleAdmin}}, // 99 -> still admin
		{"root_exact_100", common.RoleRootUser, []string{BuiltInRoleRoot}},   // 100 -> root
		{"above_root_101", 101, []string{BuiltInRoleRoot}},
		{"negative", -5, nil}, // below everything -> deny
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSubjectRoles(0, tc.systemRole)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The ordering of the switch matters: root must be evaluated before admin,
// otherwise a root user would be down-graded to admin. This locks in that a
// value >= root threshold never resolves to admin.
func TestResolveSubjectRoles_RootIsNotDowngradedToAdmin(t *testing.T) {
	got := resolveSubjectRoles(1, common.RoleRootUser)
	assert.Equal(t, []string{BuiltInRoleRoot}, got)
	assert.NotContains(t, got, BuiltInRoleAdmin)
}

// managedRoleKey is the baseline that per-user overrides are expressed against;
// it must be the admin role.
func TestManagedRoleKeyIsAdmin(t *testing.T) {
	assert.Equal(t, BuiltInRoleAdmin, managedRoleKey)
}
