package auditsecurity

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// Facts is a strict allowlist. Never retain arbitrary operation metadata,
// display names, request bodies, URLs, or any credential material. The bounded
// attempted account is unverified display evidence for failed sign-ins and
// password-recovery requests. It never authorizes the account.
type Facts struct {
	LoginTarget         *model.AuditLoginTarget `json:"login_target,omitempty"`
	AttemptedAccount    string                  `json:"attempted_account,omitempty"`
	NewManagementSource bool                    `json:"new_management_source,omitempty"`
	SourceBaseline      *sourceBaselineEvidence `json:"source_baseline,omitempty"`
	Identity            *model.AuditIdentity    `json:"identity,omitempty"`
	TokenID             int                     `json:"token_id,omitempty"`
	LivePolicyAt        int64                   `json:"live_policy_at,omitempty"`
	FirstObservedAt     int64                   `json:"first_observed_at,omitempty"`
	ChangedFields       []string                `json:"changed_fields,omitempty"`
	SubmittedFields     []string                `json:"submitted_fields,omitempty"`
	Changed             *bool                   `json:"changed,omitempty"`
	Count               *int                    `json:"count,omitempty"`
	From                *int64                  `json:"from,omitempty"`
	To                  *int64                  `json:"to,omitempty"`
	Quota               *int64                  `json:"quota,omitempty"`
	ID                  int                     `json:"id,omitempty"`
	IDs                 []int                   `json:"ids,omitempty"`
	ReturnedIDs         []int                   `json:"returned_ids,omitempty"`
	ClientRef           string                  `json:"client_ref,omitempty"`
	TargetUserID        int                     `json:"target_user_id,omitempty"`
	TargetRole          int                     `json:"target_role,omitempty"`
	Role                int                     `json:"role,omitempty"`
	Action              string                  `json:"action,omitempty"`
	PasswordReset       bool                    `json:"password_reset,omitempty"`
	PermissionsUpdated  bool                    `json:"admin_permissions_updated,omitempty"`
	PermissionIncrease  bool                    `json:"permission_increase,omitempty"`
	SubjectRef          string                  `json:"subject_ref,omitempty"`
	Reason              string                  `json:"reason,omitempty"`
	FollowupFailed      bool                    `json:"followup_failed,omitempty"`
	EvidenceIncomplete  bool                    `json:"evidence_incomplete,omitempty"`
	IPIntelligence      *IPIntelligenceSnapshot `json:"ip_intelligence,omitempty"`
}

var sensitiveFields = map[string]bool{
	"key": true, "base_url": true, "models": true, "group": true, "type": true, "status": true,
	"priority": true, "weight": true, "model_mapping": true, "status_code_mapping": true,
	"param_override": true, "header_override": true, "setting": true, "settings": true,
	"auto_ban": true, "other": true, "proxy": true, "daily_limit": true, "tag": true,
	"role": true, "password": true, "email": true, "quota": true, "permissions": true, "group_ratios": true,
}

func Normalize(a model.AuditLog) model.SecurityEvent {
	var f Facts
	if a.Other.Op != nil {
		b, err := common.Marshal(a.Other.Op.Params)
		if err == nil {
			_ = common.Unmarshal(b, &f)
			f.PermissionIncrease = false
			f.NewManagementSource = false
			f.IPIntelligence = nil
			if a.Action == "access_token.update" {
				var grants struct {
					Scopes         []string `json:"scopes"`
					PreviousScopes []string `json:"previous_scopes"`
				}
				if common.Unmarshal(b, &grants) == nil && grants.Scopes != nil && grants.PreviousScopes != nil {
					slices.Sort(grants.Scopes)
					slices.Sort(grants.PreviousScopes)
					changed := !slices.Equal(slices.Compact(grants.Scopes), slices.Compact(grants.PreviousScopes))
					f.Changed = &changed
					f.PermissionIncrease = false
					for _, scope := range grants.Scopes {
						if !slices.Contains(grants.PreviousScopes, scope) {
							f.PermissionIncrease = true
						}
					}
				}
			}
		}
	}
	// These facts come only from server-recorded request metadata.
	f.Identity, f.LivePolicyAt, f.FirstObservedAt = nil, 0, 0
	f.SourceBaseline = nil
	f.LoginTarget = nil
	if a.Action == "login" || strings.HasPrefix(a.Action, "auth.") {
		f.TargetUserID, f.TargetRole = 0, 0
	}
	recovery := a.Action == "auth.recovery_requested" || a.Action == "auth.recovery_rejected"
	loginFailure := !a.Success && (a.Action == "auth.login_failed" || a.Action == "auth.login_locked")
	if recovery || loginFailure {
		f.AttemptedAccount = model.SecurityAttemptedAccount(f.AttemptedAccount)
	} else {
		f.AttemptedAccount = ""
	}
	f.ClientRef = ""
	if a.UserAgent != "" && utf8.RuneCountInString(a.UserAgent) < 512 {
		f.ClientRef = strings.TrimPrefix(model.SecurityAccountRef("client:"+a.UserAgent), "account:")
	}
	if address, err := netip.ParseAddr(a.Ip); err == nil {
		a.Ip = address.Unmap().String()
	}
	if a.Action == "token.key_view_batch" {
		ids := make([]int, 0, min(100, len(f.ReturnedIDs)))
		for _, id := range f.ReturnedIDs {
			if id > 0 && !slices.Contains(ids, id) {
				if len(ids) == 100 {
					f.EvidenceIncomplete = true
					break
				}
				ids = append(ids, id)
			}
		}
		f.ReturnedIDs = ids
	} else {
		f.ReturnedIDs = nil
	}
	for _, source := range []*[]string{&f.ChangedFields, &f.SubmittedFields} {
		if *source == nil {
			continue
		}
		fields := make([]string, 0, min(len(*source), 32))
		for _, field := range *source {
			if (sensitiveFields[field] || field == "name") && !slices.Contains(fields, field) {
				fields = append(fields, field)
			}
			if len(fields) == 32 {
				break
			}
		}
		// Distinguish absent evidence from a known, non-sensitive change.
		if len(fields) == 0 {
			fields = []string{"other_display_field"}
		}
		*source = fields
	}
	switch f.Action {
	case "promote", "demote", "disable", "enable", "delete", "disable_key", "enable_key", "enable_all_keys", "disable_all_keys", "delete_key", "delete_disabled_keys":
	default:
		f.Action = ""
	}
	if f.Reason != "invalid_credentials" && f.Reason != "invalid_code" && f.Reason != "expired" && f.Reason != "service_error" && f.Reason != "permission_denied" {
		f.Reason = ""
	}
	if !strings.HasPrefix(f.SubjectRef, "account:") || len(f.SubjectRef) != 72 {
		f.SubjectRef = ""
	}
	e := model.SecurityEvent{EventID: a.EventId, At: a.CreatedAt, ActorID: a.UserId, ActorRole: a.ActorRole, Action: a.Action, IP: a.Ip, RequestID: a.RequestId, Success: a.Success, Visibility: common.RoleAdminUser, TargetID: f.TargetUserID, Count: 1, Reason: f.Reason}
	if a.Other.AdminInfo != nil && a.Other.AdminInfo.AdminID > 0 {
		e.ActorID = a.Other.AdminInfo.AdminID
		e.ActorRole = a.Other.AdminInfo.AdminRole
	}
	if a.Other.Op != nil && a.Other.Op.Identity != nil && a.Other.Op.Identity.ActorID == e.ActorID && a.Other.Op.Identity.AuthVersion > 0 {
		identity := *a.Other.Op.Identity
		f.Identity = &identity
		e.SessionRef = identity.SessionRef
	}
	e.TokenRef = a.TokenRef
	if e.ActorRole != common.RoleCommonUser && e.ActorRole != common.RoleAdminUser || f.TargetRole >= common.RoleRootUser {
		e.Visibility = common.RoleRootUser
	}
	e.ResourceType = "account"
	if strings.HasPrefix(a.Action, "channel.") {
		e.ResourceType = "channel"
		e.TargetID = f.ID
	}
	if strings.HasPrefix(a.Action, "user.") {
		e.ResourceType = "user"
		if f.TargetUserID > 0 && f.TargetRole != common.RoleCommonUser && f.TargetRole != common.RoleAdminUser {
			e.Visibility = common.RoleRootUser
		}
	}
	e.SubjectRef = "actor:" + strconv.Itoa(e.ActorID)
	if e.Action == "relay.ip_observed" {
		e.SubjectRef = "relay:" + strconv.Itoa(e.ActorID)
	}
	if e.ActorID == 0 {
		e.SubjectRef = "source:" + e.IP
	}
	if f.SubjectRef != "" {
		e.SubjectRef = f.SubjectRef
	}
	if !e.Success && e.ActorID == 0 && e.ActorRole == 0 && (e.Action == "auth.login_failed" || e.Action == "auth.login_locked") && a.Other.Op != nil && a.Other.Op.LoginTarget != nil && a.Other.Op.LoginTarget.Valid() {
		target := *a.Other.Op.LoginTarget
		f.LoginTarget = &target
		e.TargetID = target.UserID
		// Keep the namespace outside the HMAC so a raw attempted account such
		// as "login-target:3" cannot match a trusted account's correlation key.
		e.SubjectRef = "login-target:" + model.SecurityAccountRef(strconv.Itoa(target.UserID))
	}
	if e.Action == "login" && e.Success && (model.AuditLoginTarget{UserID: e.ActorID, Role: e.ActorRole}).Valid() {
		e.SubjectRef = "login-target:" + model.SecurityAccountRef(strconv.Itoa(e.ActorID))
	}
	// Authentication correlations can include anonymous attempts, which are
	// root-only in the source audit. Keep the whole correlation root-only.
	if e.Action == "login" || strings.HasPrefix(e.Action, "auth.") {
		e.Visibility = common.RoleRootUser
	}
	if a.Category == model.AuditCategoryAccessToken && a.Status == 403 && !a.Success && a.TokenRef != "" {
		e.Action = "auth.access_denied"
		e.SubjectRef = "token:" + a.TokenRef
		e.Reason = "permission_denied"
	}
	if e.IP != "" {
		e.SourceRef = strings.TrimPrefix(model.SecurityAccountRef("source:"+e.IP), "account:")
	}
	if f.Count != nil {
		e.Count = max(0, min(*f.Count, 1000000))
	}
	if e.Action == "channel.token_update" && f.IDs != nil {
		ids := make([]int, 0, min(len(f.IDs), 100))
		seen := make(map[int]bool, cap(ids))
		for _, id := range f.IDs {
			if id <= 0 || seen[id] {
				continue
			}
			if len(ids) == 100 {
				f.EvidenceIncomplete = true
				break
			}
			seen[id] = true
			ids = append(ids, id)
		}
		slices.Sort(ids)
		f.IDs = ids
		e.Count = len(ids)
		if f.Count != nil {
			e.Count = min(e.Count, max(0, *f.Count))
		}
		e.TargetID = 0
		if len(ids) == 1 {
			e.TargetID = ids[0]
		}
	} else {
		f.IDs = nil
	}
	f.SubjectRef = "" // Already stored in the dedicated column.
	b, _ := common.Marshal(f)
	e.Facts = string(b)
	return e
}

// Evaluate uses event time and request deduplication; infrastructure failures do
// not count as wrong credentials. An alert is evidence for review, not a verdict.
func Evaluate(e model.SecurityEvent, history []model.SecurityEvent, cfg operation_setting.AuditSecuritySetting) []model.SecurityFinding {
	if e.Action == "login" || strings.HasPrefix(e.Action, "auth.") {
		history = uniqueAuthenticationHistory(e, history)
	}
	var out []model.SecurityFinding
	var f Facts
	_ = common.UnmarshalJsonStr(e.Facts, &f)
	high := highRisk(e, f)
	if high > 0 {
		// highRisk keeps the event in correlation. A zero standalone grade only
		// skips this operation's own alert.
		if severity := standaloneSeverity(e, f); severity > 0 {
			out = append(out, model.SecurityFinding{Rule: "sensitive_operation", SubjectRef: e.SubjectRef, Severity: severity, Count: e.Count})
		}
		count := e.Count
		customerVersion := customerSensitiveEventVersion(e)
		resources := map[int]bool{}
		if e.TargetID > 0 {
			resources[e.TargetID] = true
		}
		seen := map[string]bool{}
		if e.RequestID != "" {
			seen[e.RequestID] = true
		}
		elevated := elevatedBulkImpact(e, f)
		for _, h := range history {
			if h.At < e.At-300 || h.At > e.At || h.ActorID != e.ActorID || h.Visibility != e.Visibility || h.ResourceType != e.ResourceType || h.EventID == e.EventID {
				continue
			}
			if customerVersion != 0 && customerSensitiveEventVersion(h) != customerVersion {
				continue
			}
			if h.RequestID != "" && seen[h.RequestID] {
				continue
			}
			var hf Facts
			_ = common.UnmarshalJsonStr(h.Facts, &hf)
			if highRisk(h, hf) == 0 {
				continue
			}
			if h.RequestID != "" {
				seen[h.RequestID] = true
			}
			count += h.Count
			if elevatedBulkImpact(h, hf) {
				elevated = true
			}
			if h.Action == "channel.key_view" && h.TargetID > 0 {
				resources[h.TargetID] = true
			}
		}
		if e.Action == "channel.key_view" {
			if len(resources) >= cfg.KeyViewThreshold {
				out = append(out, model.SecurityFinding{Rule: "bulk_key_access", SubjectRef: e.SubjectRef, Severity: 3, Count: len(resources)})
			}
		} else if count >= cfg.BatchThreshold {
			severity := 2
			if elevated {
				severity = 3
			}
			out = append(out, model.SecurityFinding{Rule: "bulk_sensitive_operations", SubjectRef: e.SubjectRef, Severity: severity, Count: count})
		}
	}
	if e.Action == "auth.login_failed" && !e.Success && (e.Reason == "invalid_credentials" || e.Reason == "invalid_code") {
		failures, sourceFailures := 1, 1
		accounts := map[string]bool{e.SubjectRef: true}
		for _, h := range history {
			if h.EventID == e.EventID || h.At < e.At-300 || h.At > e.At || h.Action != e.Action || (h.Reason != "invalid_credentials" && h.Reason != "invalid_code") || h.Visibility != e.Visibility {
				continue
			}
			if h.SubjectRef == e.SubjectRef && sameLoginTarget(e, h) {
				failures++
			}
			if h.IP == e.IP {
				sourceFailures++
				accounts[h.SubjectRef] = true
			}
		}
		if failures >= cfg.FailureThreshold {
			out = append(out, model.SecurityFinding{Rule: "login_failures", SubjectRef: e.SubjectRef, Severity: 1, Count: failures})
		}
		if e.IP != "" && sourceFailures >= 20 && len(accounts) >= 5 {
			out = append(out, model.SecurityFinding{Rule: "password_spray", SubjectRef: "ip:" + e.IP, Severity: 2, Count: sourceFailures})
		}
	}
	if e.Action == "login" && e.Success {
		failures := 0
		for _, h := range history {
			if h.At >= e.At-900 && h.At <= e.At && h.SubjectRef == e.SubjectRef && h.Action == "auth.login_failed" && (h.Reason == "invalid_credentials" || h.Reason == "invalid_code") {
				if target := eventLoginTarget(h); target != nil && (target.UserID != e.ActorID || target.Role != e.ActorRole) {
					continue
				}
				failures++
			}
		}
		if failures >= cfg.FailureThreshold {
			out = append(out, model.SecurityFinding{Rule: "login_after_failures", SubjectRef: e.SubjectRef, Severity: 2, Count: failures})
		}
	}
	if e.Action == "auth.recovery_requested" || e.Action == "auth.access_denied" {
		count := 1
		window, threshold := int64(600), 3
		if e.Action == "auth.access_denied" {
			window = 300
			threshold = 10
		}
		for _, h := range history {
			if h.EventID != e.EventID && h.At >= e.At-window && h.At <= e.At && h.Action == e.Action && h.SubjectRef == e.SubjectRef {
				count++
			}
		}
		if count >= threshold {
			rule := "recovery_requests"
			if e.Action == "auth.access_denied" {
				rule = "permission_failures"
			}
			out = append(out, model.SecurityFinding{Rule: rule, SubjectRef: e.SubjectRef, Severity: 1, Count: count})
		}
	}
	if e.Action == "auth.session_refresh_reuse" {
		out = append(out, model.SecurityFinding{Rule: "session_reuse", SubjectRef: e.SubjectRef, Severity: 3, Count: 1})
	}
	return out
}

// One authentication request may be captured more than once by related entry
// points. Count that attempt once, while keeping distinct requests independent.
func uniqueAuthenticationHistory(e model.SecurityEvent, history []model.SecurityEvent) []model.SecurityEvent {
	seenEvents := map[string]bool{}
	seenRequests := map[string]bool{}
	if e.EventID != "" {
		seenEvents[e.EventID] = true
	}
	if e.RequestID != "" {
		seenRequests[e.Action+":"+e.RequestID] = true
	}
	unique := make([]model.SecurityEvent, 0, len(history))
	for _, h := range history {
		if h.Visibility != e.Visibility || h.At > e.At || h.EventID != "" && seenEvents[h.EventID] || h.RequestID != "" && seenRequests[h.Action+":"+h.RequestID] {
			continue
		}
		if h.Action == "auth.login_failed" && (h.Success || h.Reason != "invalid_credentials" && h.Reason != "invalid_code") {
			continue
		}
		if h.EventID != "" {
			seenEvents[h.EventID] = true
		}
		if h.RequestID != "" {
			seenRequests[h.Action+":"+h.RequestID] = true
		}
		unique = append(unique, h)
	}
	return unique
}

func highRisk(e model.SecurityEvent, f Facts) int {
	if !e.Success || e.Count == 0 || f.Changed != nil && !*f.Changed {
		return 0
	}
	if strings.HasPrefix(e.Action, "user.quota_") && f.From != nil && f.To != nil && *f.From == *f.To {
		return 0
	}
	if e.ResourceType == "channel" {
		switch e.Action {
		case "channel.update", "channel.token_update", "channel.tag_edit":
			fields := f.ChangedFields
			if e.Action == "channel.tag_edit" {
				// Tag edits record submitted fields without reading every channel.
				fields = f.SubmittedFields
			}
			if fields != nil {
				for _, v := range fields {
					// Routine channel maintenance does not change credential targets.
					// Keep the shared allowlist intact for user group changes.
					if v == "models" || v == "group" || v == "priority" || v == "weight" {
						continue
					}
					if sensitiveFields[v] {
						return 2
					}
				}
				return 0
			}
			return 2
		case "channel.upstream_apply", "channel.upstream_apply_all":
			// These actions only apply model-list changes and discovery state.
			return 0
		case "channel.create", "channel.copy", "channel.delete", "channel.delete_batch", "channel.delete_disabled", "channel.key_view", "channel.status_update", "channel.status_update_batch", "channel.tag_disable", "channel.tag_enable", "channel.tag_batch_set", "channel.multi_key_manage", "channel.batch_daily_limit":
			return 2
		}
	}
	switch e.Action {
	case "user.manage":
		if f.Action == "promote" {
			return 3
		}
		return 2
	case "user.create":
		if f.Role >= common.RoleAdminUser {
			return 3
		}
		return 0
	case "user.update":
		if f.PermissionsUpdated {
			return 3
		}
		if f.PasswordReset {
			return 2
		}
		if f.ChangedFields == nil {
			return 2
		}
		for _, v := range f.ChangedFields {
			if sensitiveFields[v] {
				return 2
			}
		}
	case "access_token.generate", "access_token.update":
		// Role alone is not a critical grade. Standalone alerting decides whether
		// an ordinary create or a known permission shrink stays record-only.
		return 2
	case "user.quota_add", "user.quota_subtract", "user.quota_override", "user.delete", "user.binding_clear", "user.2fa_disable", "user.reset_passkey", "user.oauth_unbind", "user.password_change", "user.2fa_disable_self", "user.passkey_delete", "user.binding_unbind", "user.2fa_backup_codes", "user.passkey_register", "user.binding_bind", "auth.password_reset_committed", "auth.password_reset_cleanup_failed":
		return 2
	}
	return 0
}

// standaloneSeverity is the alert grade for this operation by itself.
// Zero means the audit row stays, without a sensitive_operation alert.
func standaloneSeverity(e model.SecurityEvent, f Facts) int {
	risk := highRisk(e, f)
	if risk == 0 {
		return 0
	}
	switch e.Action {
	case "access_token.generate":
		if e.ActorRole < common.RoleAdminUser {
			return 0
		}
		return 2
	case "access_token.update":
		if f.Changed != nil && !f.PermissionIncrease {
			return 0
		}
		return 2
	case "channel.status_update", "channel.status_update_batch", "channel.tag_disable", "channel.tag_enable":
		return 1
	case "user.manage":
		if f.Action == "promote" {
			return 3
		}
		return 2
	case "user.update":
		if f.PermissionsUpdated {
			return 2
		}
	}
	if statusOnlyChange(e, f) {
		return 1
	}
	return risk
}

func statusOnlyChange(e model.SecurityEvent, f Facts) bool {
	if e.Action != "channel.update" && e.Action != "channel.token_update" && e.Action != "channel.tag_edit" {
		return false
	}
	fields := f.ChangedFields
	if e.Action == "channel.tag_edit" {
		fields = f.SubmittedFields
	}
	if len(fields) == 0 {
		return false
	}
	sawStatus := false
	for _, field := range fields {
		switch field {
		case "models", "group", "priority", "weight", "name", "other_display_field":
			continue
		case "status":
			sawStatus = true
		default:
			return false
		}
	}
	return sawStatus
}

// elevatedBulkImpact is the part of a window that can still be critical:
// credential export, target rewrite, destruction, quota, security-factor
// removal, or a permission increase. Ordinary creates and start/stop stay high.
func elevatedBulkImpact(e model.SecurityEvent, f Facts) bool {
	if highRisk(e, f) == 0 {
		return false
	}
	switch e.Action {
	case "channel.key_view", "channel.delete", "channel.delete_batch", "channel.delete_disabled", "channel.multi_key_manage", "user.delete":
		return true
	case "user.quota_add", "user.quota_subtract", "user.quota_override":
		return true
	case "user.binding_clear", "user.2fa_disable", "user.reset_passkey", "user.oauth_unbind", "user.2fa_disable_self", "user.passkey_delete", "user.binding_unbind", "user.2fa_backup_codes":
		return true
	case "user.manage":
		return f.Action == "promote"
	case "user.create":
		return f.Role >= common.RoleAdminUser
	case "user.update":
		return f.PermissionsUpdated
	case "access_token.update":
		return f.PermissionIncrease || f.Changed == nil
	case "channel.update", "channel.token_update", "channel.tag_edit":
		return channelCredentialRewrite(f, e.Action)
	default:
		return false
	}
}

func channelCredentialRewrite(f Facts, action string) bool {
	fields := f.ChangedFields
	if action == "channel.tag_edit" {
		fields = f.SubmittedFields
	}
	if fields == nil {
		return true
	}
	for _, field := range fields {
		switch field {
		case "key", "base_url", "proxy", "header_override", "param_override", "setting", "settings", "model_mapping":
			return true
		}
	}
	return false
}

// credentialOrPrivilege is the combination that keeps an abuse-IP operation critical.
func credentialOrPrivilege(e model.SecurityEvent, f Facts) bool {
	if successfulAccountKeyAccess(e, f) || e.Action == "channel.key_view" || e.Action == "channel.multi_key_manage" {
		return true
	}
	if e.Action == "channel.update" || e.Action == "channel.token_update" || e.Action == "channel.tag_edit" {
		return channelCredentialRewrite(f, e.Action)
	}
	if e.Action == "access_token.update" && (f.PermissionIncrease || f.Changed == nil) {
		return true
	}
	if e.Action == "user.manage" && f.Action == "promote" {
		return true
	}
	if e.Action == "user.create" && f.Role >= common.RoleAdminUser {
		return true
	}
	return e.Action == "user.update" && f.PermissionsUpdated
}
