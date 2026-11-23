package model

import (
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuditIdentity is server-owned event provenance, separate from operation input.
// EffectiveVersion is set only by the code which committed an identity change.
type AuditIdentity struct {
	ActorID          int    `json:"actor_id"`
	Role             int    `json:"role"`
	AuthVersion      int64  `json:"auth_version"`
	EffectiveVersion int64  `json:"effective_version,omitempty"`
	SessionRef       string `json:"session_ref,omitempty"`
	AuthMethod       string `json:"auth_method,omitempty"`
	CredentialKind   string `json:"credential_kind,omitempty"`
	CredentialRef    string `json:"credential_ref,omitempty"`
	CredentialGrant  string `json:"credential_grant,omitempty"`
}

func (identity AuditIdentity) Version() int64 {
	if identity.EffectiveVersion > 0 {
		return identity.EffectiveVersion
	}
	return identity.AuthVersion
}

// BindAuditIdentity also supports a newly issued login before auth middleware
// has populated the context. Never use current DB state to reconstruct history.
func BindAuditIdentity(c *gin.Context, actor, role int, version int64, sessionID string) {
	identity := AuditIdentity{ActorID: actor, Role: role, AuthVersion: version, AuthMethod: "session"}
	if sessionID != "" {
		identity.SessionRef = strings.TrimPrefix(SecurityAccountRef("audit-session:"+sessionID), "account:")
	}
	if c.GetBool("use_access_token") {
		identity.AuthMethod = "access_token"
	}
	c.Set("audit_identity", identity)
}

func SetAuditEffectiveVersion(c *gin.Context, version int64) {
	if version > 0 {
		c.Set("audit_effective_version", version)
	}
}

func captureAuditIdentity(c *gin.Context, entry *AuditLog) {
	if c == nil {
		return
	}
	actor := entry.UserId
	if entry.Other.AdminInfo != nil && entry.Other.AdminInfo.AdminID > 0 {
		actor = entry.Other.AdminInfo.AdminID
	}
	value, _ := c.Get("audit_identity")
	identity, ok := value.(AuditIdentity)
	if !ok {
		BindAuditIdentity(c, c.GetInt("id"), c.GetInt("role"), c.GetInt64("auth_version"), c.GetString("session_id"))
		value, _ = c.Get("audit_identity")
		identity, _ = value.(AuditIdentity)
	}
	if actor <= 0 || identity.ActorID != actor || identity.AuthVersion <= 0 {
		return
	}
	identity.EffectiveVersion = c.GetInt64("audit_effective_version")
	identity.CredentialKind, identity.CredentialRef, identity.CredentialGrant = "", "", ""
	if c.GetBool("use_access_token") && identity.AuthMethod == "access_token" && identity.ActorID == c.GetInt("id") && identity.Role == c.GetInt("role") {
		ref := c.GetString("access_token_ref")
		decoded, err := hex.DecodeString(ref)
		if err == nil && len(decoded) == 32 {
			entry.TokenRef = ref
			if c.GetBool("access_token_legacy") {
				identity.CredentialKind, identity.CredentialRef = "legacy", ref
				identity.CredentialGrant = AccessTokenFingerprint("audit-credential-grant-v1:legacy")
			} else if tokenID := c.GetInt("access_token_id"); tokenID > 0 {
				value, _ := c.Get("access_token_scopes")
				if scopes, ok := value.([]string); ok && len(scopes) > 0 && len(scopes) <= 128 {
					scopes = slices.Clone(scopes)
					slices.Sort(scopes)
					scopes = slices.Compact(scopes)
					identity.CredentialKind, identity.CredentialRef = "pat", ref
					identity.CredentialGrant = AccessTokenFingerprint("audit-credential-grant-v1:pat:" + strconv.Itoa(tokenID) + "\x00" + strings.Join(scopes, "\x00"))
				}
			}
		}
	}
	op := AuditOperation{Action: entry.Action}
	if entry.Other.Op != nil {
		op = *entry.Other.Op
	}
	op.Identity = &identity
	entry.Other.Op = &op
}
