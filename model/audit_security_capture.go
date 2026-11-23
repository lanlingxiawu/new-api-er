package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

var securityCaptureQueue = make(chan AuditLog, 1024)
var SecurityCaptureDropped atomic.Uint64

// AuditLoginTarget identifies the account actually found by the login flow.
// It is never an authenticated actor or authorization to change that account.
type AuditLoginTarget struct {
	UserID int `json:"user_id"`
	Role   int `json:"role"`
}

func (target AuditLoginTarget) Valid() bool {
	return target.UserID > 0 && (target.Role == common.RoleCommonUser || target.Role == common.RoleAdminUser || target.Role == common.RoleRootUser)
}

// BindAuditLoginTarget accepts only server-resolved login state, never request
// fields. The value copy cannot outlive or change the authentication decision.
func BindAuditLoginTarget(c *gin.Context, userID, role int) {
	target := AuditLoginTarget{UserID: userID, Role: role}
	if target.Valid() {
		c.Set("audit_login_target", target)
	}
}

// SecurityAccountRef is purpose-separated from credentials and preserves the
// authentication layer's case semantics. It never returns the input identifier.
func SecurityAccountRef(identifier string) string {
	h := hmac.New(sha256.New, []byte(common.SessionSecret))
	_, _ = h.Write([]byte("audit-security-account-v1\x00" + identifier))
	return "account:" + hex.EncodeToString(h.Sum(nil))
}

// SecurityAttemptedAccount bounds untrusted display evidence. It is never an
// authenticated identity or a replacement for the original correlation digest.
func SecurityAttemptedAccount(identifier string) string {
	var result strings.Builder
	result.Grow(min(len(identifier), 512))
	count := 0
	for _, char := range identifier {
		if count == 128 {
			break
		}
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) || char == '\u2028' || char == '\u2029' || char == utf8.RuneError {
			char = ' '
		}
		result.WriteRune(char)
		count++
	}
	return strings.TrimSpace(result.String())
}

// CaptureSecurityEvent is only used by dashboard authentication handlers. It
// copies bounded facts, never retains gin.Context and never waits for I/O.
func CaptureSecurityEvent(c *gin.Context, action, identifier, reason string, success bool) {
	cfg := operation_setting.GetAuditSecuritySetting()
	if !cfg.CaptureEnabled || cfg.Suspended {
		return
	}
	e := AuditLog{EventId: common.NewRequestId(), CreatedAt: common.GetTimestamp(), Category: AuditCategorySecurity, Action: action, Success: success, UserId: c.GetInt("id"), ActorRole: c.GetInt("role"), RequestId: c.GetString(common.RequestIdKey), Ip: c.ClientIP(), Method: c.Request.Method, Route: c.FullPath(), Status: c.Writer.Status(), AuthMethod: "session"}
	params := AuditFields{"reason": reason}
	if identifier != "" {
		params["subject_ref"] = SecurityAccountRef(identifier)
		// Recovery keeps the submitted address as display evidence. It is not an
		// authenticated actor, and the public response stays the same either way.
		retainAccount := !success && (action == "auth.login_failed" || action == "auth.login_locked")
		retainAccount = retainAccount || action == "auth.recovery_requested" || action == "auth.recovery_rejected"
		if retainAccount {
			if attempted := SecurityAttemptedAccount(identifier); attempted != "" {
				params["attempted_account"] = attempted
			}
		}
	}
	e.Other = AuditOther{Op: &AuditOperation{Action: action, Params: params}}
	if !success && (action == "auth.login_failed" || action == "auth.login_locked") {
		if value, ok := c.Get("audit_login_target"); ok {
			if target, ok := value.(AuditLoginTarget); ok && target.Valid() {
				e.Other.Op.LoginTarget = &target
			}
		}
	}
	captureAuditIdentity(c, &e)
	QueueSecurityAudit(e)
}

// Password recovery has no authenticated caller. Only the committed target
// returned by the transaction is retained; it cannot authorize account actions.
func CapturePasswordRecoveryOutcome(c *gin.Context, identity AuditIdentity, cleanupFailed bool) {
	if identity.ActorID <= 0 || identity.EffectiveVersion <= 0 {
		return
	}
	action, reason := "auth.password_reset_committed", ""
	if cleanupFailed {
		action, reason = "auth.password_reset_cleanup_failed", "service_error"
	}
	e := AuditLog{UserId: identity.ActorID, ActorRole: 0, Action: action, Category: AuditCategorySecurity, Success: true, Ip: c.ClientIP(), RequestId: c.GetString(common.RequestIdKey), Method: c.Request.Method, Route: c.FullPath(), Status: c.Writer.Status(), AuthMethod: "recovery"}
	e.Other.Op = &AuditOperation{Action: action, Identity: &identity, Params: AuditFields{"reason": reason}}
	QueueSecurityAudit(e)
}

// QueueSecurityAudit accepts server-built safe facts from authentication code.
// Callers must never include request bodies or credentials in the entry.
func QueueSecurityAudit(e AuditLog) {
	cfg := operation_setting.GetAuditSecuritySetting()
	if !cfg.CaptureEnabled || cfg.Suspended {
		return
	}
	if e.EventId == "" {
		e.EventId = common.NewRequestId()
	}
	if e.CreatedAt == 0 {
		e.CreatedAt = common.GetTimestamp()
	}
	select {
	case securityCaptureQueue <- e:
	default:
		SecurityCaptureDropped.Add(1)
	}
}

func TakeSecurityCapture() (AuditLog, bool) {
	select {
	case e := <-securityCaptureQueue:
		return e, true
	default:
		return AuditLog{}, false
	}
}
