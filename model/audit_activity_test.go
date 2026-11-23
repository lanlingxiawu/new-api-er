package model

import (
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestAuditActivityProjection(t *testing.T) {
	changes := NewAuditChanges()
	changes.Add(SelfAuditTarget("token", 86, 3, "My key"),
		AuditFields{"name": "before", "group": "internal-before", "remain_quota": 10, "status": 1},
		AuditFields{"name": "after", "group": "internal-after", "remain_quota": 20, "status": 2})
	changes.Add(SelfAuditTarget("token", 87, 4, "foreign-target"), nil, AuditFields{"name": "foreign-value"})
	entry := &AuditLog{UserId: 3, ActorRole: 1, EventId: "event", Action: "token.update", Category: "operation", Success: true, Ip: "192.0.2.1", RequestId: "request", UserAgent: "Mozilla/5.0 Windows Chrome/155.0 Edg/155.0 secret-UA", TokenRef: "private-fingerprint", Content: "private-content", Route: "/internal/route", Other: AuditOther{Changes: changes, Op: &AuditOperation{Action: "token.update", Params: AuditFields{"subject_ref": "private-subject", "password": "private-password"}}, RootInfo: AuditFields{"private": true}}}
	before, err := common.Marshal(entry)
	require.NoError(t, err)
	activity := ProjectAuditActivity(entry)
	require.Equal(t, "Edge · Windows", activity.Client)
	require.Equal(t, "My key", activity.Target.Name)
	require.Equal(t, 4, activity.ChangeSummary.Total)
	require.Len(t, activity.ChangeSummary.Fields, 3)
	encoded, err := common.Marshal(activity)
	require.NoError(t, err)
	for _, forbidden := range []string{"other", "old", "new", "params", "actor_role", "token_ref", "route", "user_agent", "private-", "internal-", "secret-UA", "foreign-", "before", "after"} {
		require.NotContains(t, string(encoded), forbidden)
	}
	after, err := common.Marshal(entry)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "projection must not mutate retained evidence")
}

func TestAuditActivityKnownOperationNames(t *testing.T) {
	for _, action := range []string{
		"channel.update", "channel.status_update_batch", "channel.key_view", "channel.priority_reorder",
		"channel.veridrop_detect", "channel.veridrop_detect_batch", "channel.veridrop_detect_enabled",
		"user.update", "user.quota_add", "user.access_tokens.delete_all", "user.topup_complete",
		"option.update", "option.group.update", "option.passkey_domains_blocked",
		"custom_oauth.update", "performance.clear_disk_cache", "redemption.delete_batch",
		"prefill_group.create", "vendor.metadata.save", "model.pricing.update", "deployment.delete",
		"subscription.plan_reset", "employee.performance_adjust", "customer.bind_blocked",
		"audit_security.status", "price_monitor.price.apply", "employee_export.template_save",
		"log.cleanup_start", "log_export.job_create", "log_export.job_cancel", "logout", "user.session_revoke",
	} {
		t.Run(action, func(t *testing.T) {
			entry := &AuditLog{UserId: 3, ActorRole: common.RoleRootUser, Action: action, Category: AuditCategoryOperation,
				Content: "private-content", Route: "/private-route", TokenRef: "private-fingerprint",
				Other: AuditOther{Op: &AuditOperation{Action: action, Params: AuditFields{"name": "private-target", "key": "private-key"}}, Changes: NewAuditChanges()}}
			entry.Other.Changes.Add(AuditTarget("channel", 86, "private-target"), AuditFields{"name": "private-before"}, AuditFields{"name": "private-after"})
			before, err := common.Marshal(entry)
			require.NoError(t, err)
			activity := ProjectAuditActivity(entry)
			require.Equal(t, action, activity.Action)
			require.Nil(t, activity.Target)
			require.Nil(t, activity.ChangeSummary)
			encoded, err := common.Marshal(activity)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private-")
			after, err := common.Marshal(entry)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "naming must not rewrite retained evidence")
		})
	}
}

func TestAuditActivityUnknownOperationNames(t *testing.T) {
	for _, action := range []string{"generic", "channel.private_action", "user.update.private_value", "option.private_key", "log_export.private_job"} {
		activity := ProjectAuditActivity(&AuditLog{Action: action, Content: "private-content", Route: "/private-route",
			Other: AuditOther{Op: &AuditOperation{Action: "channel.update", Params: AuditFields{"name": "private-target"}}}})
		require.Equal(t, "operation", activity.Action, action)
		encoded, err := common.Marshal(activity)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "private-")
	}
}

func TestAuditActivityRecordedActionFallbacks(t *testing.T) {
	for _, test := range []struct {
		name, category, action, nestedAction, want string
	}{
		{"subscription reset receipt", AuditCategoryOperation, "", "subscription.user_quota_reset", "subscription.user_quota_reset"},
		{"access request without descriptor", AuditCategoryAccessToken, "", "", "access_token.request"},
		{"explicit action wins", AuditCategoryAccessToken, "access_token.denied", "access_token.request", "access_token.denied"},
		{"unknown nested action", AuditCategoryOperation, "", "private.action", "operation"},
		{"unknown access action", AuditCategoryAccessToken, "", "private.action", "operation"},
		{"no action evidence", AuditCategoryOperation, "", "", "operation"},
	} {
		for _, success := range []bool{true, false} {
			t.Run(test.name+"/"+strconv.FormatBool(success), func(t *testing.T) {
				entry := &AuditLog{Category: test.category, Action: test.action, Success: success, Other: AuditOther{Op: &AuditOperation{Action: test.nestedAction, Params: AuditFields{"name": "private-target"}}}}
				before, err := common.Marshal(entry)
				require.NoError(t, err)
				// Exercise the stored representation as well as the absent Op path.
				require.NoError(t, common.Unmarshal(before, entry))
				activity := ProjectAuditActivity(entry)
				require.Equal(t, test.want, activity.Action)
				require.Equal(t, success, activity.Success)
				require.Nil(t, activity.Target)
				after, err := common.Marshal(entry)
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(after))
			})
		}
	}
	require.Equal(t, "access_token.request", ProjectAuditActivity(&AuditLog{Category: AuditCategoryAccessToken}).Action)
}

func TestAuditActivityCreateAndLegacyParams(t *testing.T) {
	for _, stored := range []bool{false, true} {
		entry := &AuditLog{UserId: 3, Action: "token.create", Category: "operation", Other: AuditOther{Op: &AuditOperation{Action: "token.create", Params: AuditFields{"id": 86, "name": "My key", "group": "secret-group"}}, Changes: NewAuditChanges()}}
		entry.Other.Changes.Add(SelfAuditTarget("token", 86, 3, "My key"), nil, AuditFields{"name": "My key", "group": "secret-group"})
		if stored {
			data, err := common.Marshal(entry)
			require.NoError(t, err)
			require.NoError(t, common.Unmarshal(data, entry))
		}
		activity := ProjectAuditActivity(entry)
		require.Equal(t, "86", activity.Target.ID)
		require.Nil(t, activity.ChangeSummary, "creation must not show an initial configuration dump")
		entry.Other.Changes = nil
		activity = ProjectAuditActivity(entry)
		require.Nil(t, activity.Target, "legacy parameters cannot prove target ownership")
	}
	entry := &AuditLog{Action: "unknown.private", Other: AuditOther{Op: &AuditOperation{Params: AuditFields{"name": "secret"}}}, UserAgent: "private-agent", AuthMethod: "internal", Content: "secret"}
	activity := ProjectAuditActivity(entry)
	require.Equal(t, "operation", activity.Action)
	require.Nil(t, activity.Target)
	require.Equal(t, "Other client", activity.Client)
	require.Equal(t, "unknown", activity.Source)
	entry.Action = "login"
	entry.Other.Op.Params["method"] = "oauth:" + strings.Repeat("private", 10)
	require.Equal(t, "oauth", ProjectAuditActivity(entry).LoginMethod)
	data, err := common.Marshal(entry)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(data, entry))
	require.Equal(t, "oauth", ProjectAuditActivity(entry).LoginMethod, "stored RawMessage parameters are supported")
}

func TestAuditActivityFilteredReceiptCannotUseLegacyTarget(t *testing.T) {
	entry := &AuditLog{UserId: 3, Action: "token.update", Other: AuditOther{Changes: NewAuditChanges(), Op: &AuditOperation{Params: AuditFields{"name": "foreign-secret", "id": 99}}}}
	entry.Other.Changes.Add(SelfAuditTarget("token", 99, 4, "foreign-secret"), AuditFields{"name": "old"}, AuditFields{"name": "new"})
	ProjectAuditLogs([]*AuditLog{entry}, true, common.RoleRootUser)
	activity := ProjectAuditActivity(entry)
	require.Nil(t, activity.Target)
	require.Nil(t, activity.ChangeSummary)
}

func TestAuditSelfExportProjectionVersion(t *testing.T) {
	job := &LogExportJob{DataSource: LogExportDataAudit, SelfExport: true, UserID: 3, Filters: LogExportFilter{UserId: 3}, AuditFilters: &AuditExportFilter{AuditLogFilter: AuditLogFilter{UserId: 3, SelfView: true, TokenRef: "private-ref", Route: "/private"}}, Status: LogExportStatusReady, Parts: []LogExportPart{{Index: 1, Path: "private-path"}}}
	access := LogExportAccess{UserID: 3, Role: 1, Scope: "audit_self"}
	require.False(t, job.HasCurrentAuditProjection())
	public := access.PublicJob(job)
	require.NotNil(t, public)
	require.False(t, public.CanDownload)
	require.True(t, public.CanDelete)
	job.AuditProjectionVersion = AuditActivityProjectionVersion
	require.True(t, job.HasCurrentAuditProjection())
	require.True(t, access.PublicJob(job).CanDownload)
	require.Empty(t, public.AuditFilters.TokenRef)
	require.Empty(t, public.AuditFilters.Route)
	job.SelfExport = false
	job.AuditProjectionVersion = 0
	require.True(t, job.HasCurrentAuditProjection(), "administrator exports retain their contract")
}
