package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// audit.go covers the language-neutral audit-content template rendering and the
// operator-info extraction helpers. All are pure (no DB) except the context reads.

func TestAuditContentEN_KnownTemplateExpands(t *testing.T) {
	got := auditContentEN("user.create", map[string]interface{}{
		"username": "alice",
		"role":     3,
	})
	assert.Equal(t, "Created user alice (role 3)", got)
}

func TestAuditContentEN_MissingParamRendersEmpty(t *testing.T) {
	// ${id} absent from params -> os.Expand yields empty string for it.
	got := auditContentEN("user.update", map[string]interface{}{
		"username": "bob",
	})
	assert.Equal(t, "Updated user bob (ID: )", got)
}

func TestAuditContentEN_UnknownActionReturnsActionItself(t *testing.T) {
	got := auditContentEN("totally.unregistered.action", map[string]interface{}{"x": 1})
	assert.Equal(t, "totally.unregistered.action", got)
}

func TestAuditContentEN_NilParams(t *testing.T) {
	// nil params must not panic; placeholders resolve to empty.
	got := auditContentEN("redemption.create", nil)
	assert.Equal(t, "Created  redemption codes named  ( each)", got)
}

func TestAuditAuthMethod_AccessTokenVsSession(t *testing.T) {
	ctx, _ := newCtx(t, "GET", "/x", nil)
	assert.Equal(t, "session", auditAuthMethod(ctx))
	ctx.Set("use_access_token", true)
	assert.Equal(t, "access_token", auditAuthMethod(ctx))
}

func TestAuditOperatorInfo_PopulatesFromContext(t *testing.T) {
	ctx, _ := newCtx(t, "GET", "/x", nil)
	ctx.Set("id", 4242)
	ctx.Set("username", "admin1")
	ctx.Set("role", common.RoleAdminUser)
	ctx.Set("use_access_token", true)

	info := auditOperatorInfo(ctx)
	require.NotNil(t, info)
	assert.Equal(t, 4242, info["admin_id"])
	assert.Equal(t, "admin1", info["admin_username"])
	assert.Equal(t, common.RoleAdminUser, info["admin_role"])
	assert.Equal(t, "access_token", info["auth_method"])
}
