package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
)

func TestAuditIdentityIsBoundToAuthenticatedActorAndCommit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("id", 7)
	c.Set("role", common.RoleCommonUser)
	c.Set("auth_version", int64(4))
	c.Set("session_id", "private-session")
	entry := AuditLog{UserId: 7, Other: AuditOther{Op: &AuditOperation{Action: "user.password_change"}}}
	SetAuditEffectiveVersion(c, 5)
	captureAuditIdentity(c, &entry)
	require.EqualValues(t, 4, entry.Other.Op.Identity.AuthVersion)
	require.EqualValues(t, 5, entry.Other.Op.Identity.Version())
	require.NotContains(t, entry.Other.Op.Identity.SessionRef, "private-session")
	other := AuditLog{UserId: 8}
	captureAuditIdentity(c, &other)
	require.Nil(t, other.Other.Op)
}

func TestAuditIdentityCapturesRequestCredential(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("id", 7)
		c.Set("role", common.RoleAdminUser)
		c.Set("auth_version", int64(4))
		c.Set("use_access_token", true)
		c.Set("access_token_legacy", legacy)
		c.Set("access_token_id", 18)
		c.Set("access_token_scopes", []string{"user:write", "user:read"})
		ref := AccessTokenFingerprint("synthetic-request-credential")
		c.Set("access_token_ref", ref)
		entry := AuditLog{UserId: 7, Other: AuditOther{Op: &AuditOperation{Params: AuditFields{"token_ref": "spoofed-target", "token_id": 999}}}}
		captureAuditIdentity(c, &entry)
		require.Equal(t, ref, entry.TokenRef)
		raw, err := common.Marshal(entry.Other.Op.Identity)
		require.NoError(t, err)
		var identity map[string]any
		require.NoError(t, common.Unmarshal(raw, &identity))
		require.Equal(t, ref, identity["credential_ref"])
		require.NotContains(t, string(raw), "synthetic-request-credential")
		require.NotContains(t, string(raw), "spoofed-target")
		kind := "pat"
		if legacy {
			kind = "legacy"
		}
		require.Equal(t, kind, identity["credential_kind"])
		require.NotEmpty(t, identity["credential_grant"])
		c.Set("access_token_scopes", []string{"user:read", "user:write", "user:read"})
		captureAuditIdentity(c, &entry)
		reordered, err := common.Marshal(entry.Other.Op.Identity)
		require.NoError(t, err)
		require.JSONEq(t, string(raw), string(reordered))
		if !legacy {
			c.Set("access_token_scopes", []string{"user:read"})
			captureAuditIdentity(c, &entry)
			require.NotEqual(t, identity["credential_grant"], entry.Other.Op.Identity.CredentialGrant)
			c.Set("access_token_ref", "not-a-fingerprint")
			invalid := AuditLog{UserId: 7}
			captureAuditIdentity(c, &invalid)
			require.Empty(t, invalid.TokenRef)
			require.Empty(t, invalid.Other.Op.Identity.CredentialKind)
		}
		c.Set("use_access_token", false)
		c.Set("audit_identity", AuditIdentity{ActorID: 7, Role: common.RoleAdminUser, AuthVersion: 4, AuthMethod: "session"})
		browser := AuditLog{UserId: 7}
		captureAuditIdentity(c, &browser)
		require.Empty(t, browser.TokenRef, "stale context fields cannot label browser authentication as a token")
	}
}

func TestAuditCommittedVersionSurvivesCachePublishFailure(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	twofa := TwoFA{UserId: user.Id, IsEnabled: true}
	require.NoError(t, DB.Create(&twofa).Error)
	t.Cleanup(func() { require.NoError(t, DB.Unscoped().Where("user_id = ?", user.Id).Delete(&TwoFA{}).Error) })
	server := useUserCacheMiniRedis(t)
	// Let the required pre-commit deny fence succeed. The outage starts after
	// the final SQL mutation, so only post-commit cache publication fails.
	require.NoError(t, DB.Callback().Delete().After("gorm:delete").Register("test:audit-publish-outage", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "two_fas" {
			server.SetError("fixture outage")
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Delete().Remove("test:audit-publish-outage")) })
	version, err := disableTwoFACommitted(user.Id, nil)
	require.Error(t, err)
	require.Equal(t, user.AuthVersion+1, version)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, version, stored.AuthVersion)
	version, err = disableTwoFACommitted(user.Id, nil)
	require.ErrorIs(t, err, ErrTwoFANotEnabled)
	require.Zero(t, version, "a rejected change must not claim a commit")
}
