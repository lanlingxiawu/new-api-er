package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Can — the security-critical allow/deny matrix.
// ---------------------------------------------------------------------------

// The role-level allow/deny matrix after a clean Init (no per-user overrides):
//
//	action           | common | admin | root
//	-----------------+--------+-------+-----
//	read             | deny   | ALLOW | ALLOW
//	operate          | deny   | ALLOW | ALLOW
//	write            | deny   | ALLOW | ALLOW
//	sensitive_write  | deny   | deny  | ALLOW
//	secret_view      | deny   | deny  | ALLOW
func TestCan_RoleMatrix(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	type row struct {
		perm  Permission
		guest bool
		commn bool
		admin bool
		root  bool
	}
	matrix := []row{
		{ChannelRead, false, false, true, true},
		{ChannelOperate, false, false, true, true},
		{ChannelWrite, false, false, true, true},
		{ChannelSensitiveWrite, false, false, false, true},
		{ChannelSecretView, false, false, false, true},
	}
	for _, r := range matrix {
		t.Run(r.perm.Action, func(t *testing.T) {
			assert.Equal(t, r.guest, Can(1, common.RoleGuestUser, r.perm), "guest")
			assert.Equal(t, r.commn, Can(1, common.RoleCommonUser, r.perm), "common")
			assert.Equal(t, r.admin, Can(1, common.RoleAdminUser, r.perm), "admin")
			assert.Equal(t, r.root, Can(1, common.RoleRootUser, r.perm), "root")
		})
	}
}

// Deny-by-default: a subject with no resolved roles (common/guest) is denied
// everything, even a known, baseline-granted permission.
func TestCan_NoRolesDeniedEverything(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	assert.False(t, Can(1, common.RoleCommonUser, ChannelRead))
	assert.False(t, Can(1, common.RoleGuestUser, ChannelRead))
}

// SECURITY: a superuser (root) short-circuits to allow BEFORE the
// isKnownPermission check, so root is allowed even an unregistered permission.
// A non-superuser (admin) is denied any unknown permission (fail closed).
func TestCan_UnknownPermission(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	unknown := Permission{Resource: "nonexistent", Action: "do"}
	assert.True(t, Can(1, common.RoleRootUser, unknown), "root superuser short-circuits before known-permission check")
	assert.False(t, Can(1, common.RoleAdminUser, unknown), "admin must be denied an unknown permission")
	assert.False(t, Can(1, common.RoleCommonUser, unknown))
}

// Nil enforcer: every non-superuser decision must fail closed. (A superuser
// short-circuits before the enforcer is consulted, which is verified separately.)
func TestCan_NilEnforcerFailsClosed(t *testing.T) {
	setEnforcerNil(t)
	assert.False(t, Can(1, common.RoleAdminUser, ChannelRead), "admin denied when enforcer uninitialized")
}

// A superuser is allowed even when the enforcer is nil, because the superuser
// check precedes the enforcer lookup.
func TestCan_SuperuserAllowedWithNilEnforcer(t *testing.T) {
	setEnforcerNil(t)
	assert.True(t, Can(1, common.RoleRootUser, ChannelSensitiveWrite))
	assert.True(t, Can(1, common.RoleRootUser, Permission{Resource: "x", Action: "y"}))
}

// Per-user override ALLOW beats the missing admin baseline (admin normally lacks
// sensitive_write).
func TestCan_OverrideAllowBeatsBaselineAbsence(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	assert.False(t, Can(50, common.RoleAdminUser, ChannelSensitiveWrite))
	require.NoError(t, SetUserPermissions(50, PermissionsMap{
		ResourceChannel: {ActionSensitiveWrite: true},
	}))
	assert.True(t, Can(50, common.RoleAdminUser, ChannelSensitiveWrite), "override allow must grant access")
}

// Per-user override DENY beats the admin baseline ALLOW (admin normally has
// write). This is the revocation path.
func TestCan_OverrideDenyBeatsBaselineAllow(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	assert.True(t, Can(51, common.RoleAdminUser, ChannelWrite))
	require.NoError(t, SetUserPermissions(51, PermissionsMap{
		ResourceChannel: {ActionWrite: false},
	}))
	assert.False(t, Can(51, common.RoleAdminUser, ChannelWrite), "override deny must revoke a baseline grant")
}

// A per-user override applies only to that user; a different user keeps the
// baseline.
func TestCan_OverrideIsPerUser(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, SetUserPermissions(60, PermissionsMap{
		ResourceChannel: {ActionWrite: false},
	}))
	assert.False(t, Can(60, common.RoleAdminUser, ChannelWrite))
	assert.True(t, Can(61, common.RoleAdminUser, ChannelWrite), "other admins keep baseline")
}

// A root override cannot matter: the superuser short-circuit means root is
// always allowed regardless of any stored deny override.
func TestCan_RootIgnoresDenyOverride(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	// Store a deny override for user 70 (relative to admin baseline), then check
	// as root: still allowed.
	require.NoError(t, SetUserPermissions(70, PermissionsMap{
		ResourceChannel: {ActionWrite: false},
	}))
	assert.False(t, Can(70, common.RoleAdminUser, ChannelWrite))
	assert.True(t, Can(70, common.RoleRootUser, ChannelWrite), "root superuser ignores overrides")
}

// ---------------------------------------------------------------------------
// Capabilities — full matrix shape.
// ---------------------------------------------------------------------------

func TestCapabilities_AdminShape(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	caps := Capabilities(7, common.RoleAdminUser)
	require.Contains(t, caps, ResourceChannel)
	assert.True(t, caps[ResourceChannel][ActionRead])
	assert.True(t, caps[ResourceChannel][ActionOperate])
	assert.True(t, caps[ResourceChannel][ActionWrite])
	assert.False(t, caps[ResourceChannel][ActionSensitiveWrite])
	assert.False(t, caps[ResourceChannel][ActionSecretView])
	// Every registered action must have an entry.
	assert.Len(t, caps[ResourceChannel], 5)
}

func TestCapabilities_RootAllTrue(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	caps := Capabilities(7, common.RoleRootUser)
	for action, allowed := range caps[ResourceChannel] {
		assert.True(t, allowed, "root must be allowed %s", action)
	}
}

func TestCapabilities_CommonAllFalse(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	caps := Capabilities(7, common.RoleCommonUser)
	for action, allowed := range caps[ResourceChannel] {
		assert.False(t, allowed, "common user must be denied %s", action)
	}
}

// ---------------------------------------------------------------------------
// roleBaselineAllows / explicitSubjectEffect / policyEffect — white box.
// ---------------------------------------------------------------------------

func TestRoleBaselineAllows(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	e := currentEnforcer()
	require.NotNil(t, e)

	assert.True(t, roleBaselineAllows(e, BuiltInRoleAdmin, ChannelRead))
	assert.False(t, roleBaselineAllows(e, BuiltInRoleAdmin, ChannelSensitiveWrite))
	// Root has no explicit baseline rows (superuser is implicit), so a direct
	// baseline lookup returns false.
	assert.False(t, roleBaselineAllows(e, BuiltInRoleRoot, ChannelRead))
	// Unknown role subject -> no policy -> false.
	assert.False(t, roleBaselineAllows(e, "ghost", ChannelRead))
}

// explicitSubjectEffect: deny wins over allow when a subject has conflicting
// rows for the same resource/action.
func TestExplicitSubjectEffect_DenyWinsOverAllow(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	e := currentEnforcer()
	require.NotNil(t, e)

	subject := UserSubject(999)
	_, err := e.AddPolicy(subject, ResourceChannel, ActionWrite, EffectAllow)
	require.NoError(t, err)
	_, err = e.AddPolicy(subject, ResourceChannel, ActionWrite, EffectDeny)
	require.NoError(t, err)

	effect, ok := explicitSubjectEffect(e, subject, ChannelWrite)
	require.True(t, ok)
	assert.Equal(t, EffectDeny, effect, "deny must win when both allow and deny are present")
}

func TestExplicitSubjectEffect_AllowOnly(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	e := currentEnforcer()

	subject := UserSubject(1001)
	_, err := e.AddPolicy(subject, ResourceChannel, ActionSecretView, EffectAllow)
	require.NoError(t, err)

	effect, ok := explicitSubjectEffect(e, subject, ChannelSecretView)
	require.True(t, ok)
	assert.Equal(t, EffectAllow, effect)
}

func TestExplicitSubjectEffect_NoPolicy(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	e := currentEnforcer()

	_, ok := explicitSubjectEffect(e, UserSubject(123456), ChannelSecretView)
	assert.False(t, ok, "absent policy must report not-found")
}

// policyEffect: an effect column that is missing (len<4) or empty defaults to
// "allow" (matching how ruleToLine backfills legacy p rows); an explicit value
// is returned verbatim.
func TestPolicyEffect(t *testing.T) {
	assert.Equal(t, EffectAllow, policyEffect([]string{"s", "o", "a"}), "len<4 defaults to allow")
	assert.Equal(t, EffectAllow, policyEffect([]string{"s", "o", "a", ""}), "empty effect defaults to allow")
	assert.Equal(t, EffectDeny, policyEffect([]string{"s", "o", "a", EffectDeny}))
	assert.Equal(t, EffectAllow, policyEffect([]string{"s", "o", "a", EffectAllow}))
	assert.Equal(t, "custom", policyEffect([]string{"s", "o", "a", "custom"}), "explicit value returned verbatim")
}
