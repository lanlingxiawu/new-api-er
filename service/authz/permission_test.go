package authz

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// UserSubject / RoleSubject are the casbin subject-string builders. Equivalence
// partitioning across sign of userID and content of roleKey.
func TestUserSubject(t *testing.T) {
	cases := []struct {
		name   string
		userID int
		want   string
	}{
		{"zero", 0, "user:0"},
		{"positive", 42, "user:42"},
		{"negative", -1, "user:-1"},
		{"large", 2147483647, "user:2147483647"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, UserSubject(tc.userID))
		})
	}
}

func TestRoleSubject(t *testing.T) {
	cases := []struct {
		name    string
		roleKey string
		want    string
	}{
		{"root", BuiltInRoleRoot, "role:root"},
		{"admin", BuiltInRoleAdmin, "role:admin"},
		{"empty", "", "role:"},
		{"custom", "auditor", "role:auditor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RoleSubject(tc.roleKey))
		})
	}
}

// UserSubject and RoleSubject must never collide for the same numeric/string —
// distinct namespaces guard against a user impersonating a role and vice versa.
func TestSubjectNamespacesDoNotCollide(t *testing.T) {
	assert.NotEqual(t, UserSubject(1), RoleSubject("1"))
	assert.NotEqual(t, UserSubject(0), RoleSubject(""))
}
