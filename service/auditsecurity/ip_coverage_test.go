package auditsecurity

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestIPIntelligenceCoversRecordedSecurityEvents(t *testing.T) {
	for _, tc := range []struct {
		action, reason string
		success, want  bool
		fields         model.AuditFields
	}{
		{"auth.login_failed", "invalid_credentials", false, true, nil},
		{"auth.login_failed", "invalid_code", false, true, nil},
		{"auth.login_failed", "service_error", false, false, nil},
		{"auth.login_failed", "expired", false, false, nil},
		{"auth.login_failed", "invalid_credentials", true, false, nil},
		{"auth.session_refresh_reuse", "", false, true, nil},
		{"auth.access_denied", "permission_denied", false, true, nil},
		{"auth.access_denied", "service_error", false, false, nil},
		{"auth.recovery_requested", "", true, true, nil},
		{"auth.recovery_requested", "service_error", false, false, nil},
		{"auth.password_reset_committed", "", true, true, nil},
		{"auth.password_reset_cleanup_failed", "service_error", true, true, nil},
		{"auth.login_locked", "failure_limit", false, false, nil},
		{"login", "", true, true, nil},
		{"logout", "", true, false, nil},
		{"user.update", "", true, true, model.AuditFields{"target_user_id": 2, "admin_permissions_updated": true}},
		{"channel.create", "", true, true, model.AuditFields{"id": 3}},
		{"channel.update", "", true, false, model.AuditFields{"id": 3, "changed_fields": []string{"name"}}},
		{"token.key_view", "", true, true, model.AuditFields{"id": 4}},
		{"token.key_view", "", false, false, model.AuditFields{"id": 4}},
		{"access_token.update", "", true, true, model.AuditFields{"id": 5, "scopes": []string{"channels:read", "channels:write"}, "previous_scopes": []string{"channels:read"}}},
		{"access_token.update", "", true, false, model.AuditFields{"id": 5, "scopes": []string{"channels:read"}, "previous_scopes": []string{"channels:read"}}},
	} {
		t.Run(fmt.Sprintf("%s/%s/%t", tc.action, tc.reason, tc.success), func(t *testing.T) {
			fields := tc.fields
			if fields == nil {
				fields = model.AuditFields{}
			}
			fields["reason"] = tc.reason
			e := Normalize(model.AuditLog{EventId: "candidate", UserId: 1, ActorRole: 1, Action: tc.action, Ip: "204.1.132.33", Success: tc.success, Other: model.AuditOther{Op: &model.AuditOperation{Params: fields}}})
			require.Equal(t, tc.want, ipIntelligenceCandidate(e))
			e.IP = "127.0.0.1"
			require.False(t, ipIntelligenceCandidate(e))
		})
	}
}

func TestFailedLoginIPRetryAddsRiskWithoutRecountingFailures(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "failed-ip-coverage")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.FailureThreshold = true, 2
	provider := operation_setting.IPIntelligenceProvider{ID: "cached", Name: "Cached", Priority: 100, Type: operation_setting.IPProviderCustomIPAPI, Endpoint: "https://unused.invalid", Enabled: true, Capabilities: []string{"abuse"}}
	raw, err := common.Marshal([]operation_setting.IPIntelligenceProvider{provider})
	require.NoError(t, err)
	cfg.IPIntelligenceProvidersJSON = string(raw)
	now := time.Now().Unix()
	events := []model.SecurityEvent{
		{EventID: "failed-first", RequestID: "request-first", At: now - 2, Visibility: 100, Action: "auth.login_failed", Reason: "invalid_credentials", SubjectRef: "account:root", IP: "204.1.132.33", Facts: "{}"},
		{EventID: "failed-second", RequestID: "request-second", At: now - 1, Visibility: 100, Action: "auth.login_failed", Reason: "invalid_credentials", SubjectRef: "account:root", IP: "204.1.132.33", Facts: "{}"},
	}
	require.NoError(t, Apply(t.Context(), db, state, events, state, cfg))
	var failed model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "login_failures").Take(&failed).Error)
	detail, err := GetAlert(t.Context(), db, 100, failed.ID)
	require.NoError(t, err)
	require.Len(t, detail.IPMarkers, 2)
	require.Equal(t, "attention", detail.IPMarkers[0].Level)
	require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id IN ?", []string{events[0].EventID, events[1].EventID}).Update("ip_next_attempt_at", now-1).Error)
	previous := globalIPIntelligence
	globalIPIntelligence = &ipIntelligenceManager{cache: map[string]ipProviderCacheEntry{}}
	t.Cleanup(func() { globalIPIntelligence = previous })
	yes := true
	globalIPIntelligence.store(provider.ID+":"+ipProviderConfigHash(provider)+":"+events[0].IP, IPIntelligenceSnapshot{IsAbuser: &yes}, now+60)
	w := worker{db: db}
	remaining := 0
	require.NoError(t, w.retryIPIntelligence(t.Context(), state, cfg, &remaining))
	require.NoError(t, w.retryIPIntelligence(t.Context(), state, cfg, &remaining))
	require.NoError(t, Apply(t.Context(), db, state, events, state, cfg))
	var receipt model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", events[1].EventID).Take(&receipt).Error)
	require.Equal(t, "done", receipt.IPCheckState)
	var facts Facts
	require.NoError(t, common.UnmarshalJsonStr(receipt.Facts, &facts))
	require.NotNil(t, facts.IPIntelligence)
	require.Equal(t, &yes, facts.IPIntelligence.IsAbuser)
	detail, err = GetAlert(t.Context(), db, 100, failed.ID)
	require.NoError(t, err)
	require.Equal(t, failed.Occurrences, detail.Alert.Occurrences)
	require.Equal(t, failed.MatchedCount, detail.Alert.MatchedCount)
	for _, marker := range detail.IPMarkers {
		require.Equal(t, "high", marker.Level)
	}
	var count int64
	require.NoError(t, db.Model(&model.SecurityFindingSnapshot{}).Where("rule = ?", "ip_abuse").Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, db.Model(&model.SecurityFindingSnapshot{}).Where("rule IN ?", []string{"risky_ip_login", "risky_ip_sensitive_operation"}).Count(&count).Error)
	require.Zero(t, count, "a failed attempt must not become a successful compromised login")
	_, reason := automaticResponseEligibility(receipt, model.SecurityFinding{Rule: "ip_abuse"}, cfg, now, false)
	require.Equal(t, "incomplete_evidence", reason, "the attempted account is not an authenticated actor")
	legacy := events[0]
	legacy.EventID = "legacy-failed"
	require.NoError(t, db.Create(&legacy).Error)
	page := []model.SecurityEvent{legacy}
	require.NoError(t, enrichSecurityEvents(t.Context(), db, page, cfg, &remaining))
	require.Empty(t, page[0].IPCheckState, "old receipts must not be implicitly enriched")
	require.Equal(t, "{}", page[0].Facts)
}

func TestIPIntelligenceFindingDoesNotMarkOtherSources(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "separate-ip-evidence")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	now := time.Now().Unix()
	events := []model.SecurityEvent{
		{EventID: "clean-source", RequestID: "first-request", At: now - 1, ActorID: 7, ActorRole: 1, Visibility: 10, SubjectRef: "actor:7", ResourceType: "account", Action: "access_token.generate", Success: true, Count: 1, IP: "1.1.1.1", Facts: "{}"},
		{EventID: "abuse-source", RequestID: "second-request", At: now, ActorID: 7, ActorRole: 1, Visibility: 10, SubjectRef: "actor:7", ResourceType: "account", Action: "access_token.generate", Success: true, Count: 1, IP: "8.8.8.8", Facts: `{"ip_intelligence":{"is_abuser":true,"is_tor":true}}`},
	}
	require.NoError(t, Apply(t.Context(), db, state, events, state, cfg))
	for _, rule := range []string{"ip_abuse", "anonymous_network"} {
		var alert model.SecurityAlert
		require.NoError(t, db.Where("rule = ?", rule).Take(&alert).Error)
		detail, err := GetAlert(t.Context(), db, 100, alert.ID)
		require.NoError(t, err)
		require.Len(t, detail.Events, 1)
		require.Equal(t, "abuse-source", detail.Events[0].EventID)
	}
	markers, err := RiskMarkers(t.Context(), db, []*model.AuditLog{{EventId: events[0].EventID, CreatedAt: events[0].At, Ip: events[0].IP}}, 100)
	require.NoError(t, err)
	require.Equal(t, "none", markers[0].State)
}

func TestCorrelatedIPMarkersMatchDetailsFiltersAndVisibility(t *testing.T) {
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	var highIDs, attentionIDs []string
	for i, tc := range []struct{ rule, level string }{
		{"multi_account_key_access", "high"}, {"multi_source_key_access", "high"},
		{"new_source_channel_rewrite", "high"}, {"new_source_sensitive_operation", "high"},
		{"recovery_requests", "attention"}, {"permission_failures", "attention"},
		{"sensitive_operation", ""}, {"bulk_sensitive_operations", ""}, {"bulk_key_access", ""},
	} {
		event := model.SecurityEvent{EventID: fmt.Sprintf("marker-%d", i), IP: "8.8.8.8", At: now - int64(i), Action: "login", Facts: "{}"}
		raw, err := common.Marshal([]model.SecurityEvent{event})
		require.NoError(t, err)
		alert := model.SecurityAlert{Rule: tc.rule, Visibility: 100, Status: "open", Evidence: string(raw), FirstSeen: event.At, LastSeen: event.At}
		require.NoError(t, db.Create(&alert).Error)
		require.NoError(t, StoreRiskEvidence(db, alert, []model.SecurityEvent{event}))
		require.NoError(t, db.Create(&model.AuditLog{EventId: event.EventID, Ip: event.IP, CreatedAt: event.At, ActorRole: 100}).Error)
		detail, err := GetAlert(t.Context(), db, 100, alert.ID)
		require.NoError(t, err)
		require.Len(t, detail.IPMarkers, 1)
		require.Equal(t, tc.level, detail.IPMarkers[0].Level, tc.rule)
		if tc.level == "high" {
			highIDs = append(highIDs, event.EventID)
		}
		if tc.level == "attention" {
			attentionIDs = append(attentionIDs, event.EventID)
		}
		markers, err := RiskMarkers(t.Context(), db, []*model.AuditLog{{EventId: event.EventID, Ip: event.IP, CreatedAt: event.At}}, 10)
		require.NoError(t, err)
		require.Equal(t, "none", markers[0].State)
		markers, err = RiskMarkers(t.Context(), db, []*model.AuditLog{{EventId: event.EventID, Ip: "1.1.1.1", CreatedAt: event.At}}, 100)
		require.NoError(t, err)
		require.Equal(t, "none", markers[0].State, "same event ID cannot borrow another IP's risk")
	}
	for level, want := range map[string][]string{"high": highIDs, "attention": attentionIDs} {
		page, err := QueryRiskRecords(t.Context(), db, db, model.AuditLogFilter{StartTimestamp: now - 60, EndTimestamp: now + 1}, 100, 1, level, "", 20)
		require.NoError(t, err)
		var got []string
		for _, row := range page.Items {
			got = append(got, row.EventId)
		}
		require.ElementsMatch(t, want, got, level)
	}
}

func TestAuthenticationCorrelationsCountDistinctRequests(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	for _, tc := range []struct {
		action, rule string
		threshold    int
	}{
		{"auth.login_failed", "login_failures", cfg.FailureThreshold},
		{"auth.recovery_requested", "recovery_requests", 3},
		{"auth.access_denied", "permission_failures", 10},
		{"login", "login_after_failures", cfg.FailureThreshold + 1},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			e := model.SecurityEvent{EventID: "current", RequestID: "current-request", Action: tc.action, Reason: "invalid_credentials", SubjectRef: "account:a", At: 1000, Visibility: 100, Success: tc.action == "login", IP: "8.8.8.8"}
			history := make([]model.SecurityEvent, tc.threshold-1)
			for i := range history {
				history[i] = e
				history[i].EventID = fmt.Sprint("event-", i)
				history[i].RequestID = "duplicate-request"
				history[i].At = 990
				if e.Action == "login" {
					history[i].Action, history[i].Success = "auth.login_failed", false
				}
			}
			require.Empty(t, Evaluate(e, history, cfg), "repeated capture of one request cannot reach the threshold")
			for i := range history {
				history[i].RequestID = fmt.Sprint("request-", i)
			}
			findings := Evaluate(e, history, cfg)
			require.Len(t, findings, 1)
			require.Equal(t, tc.rule, findings[0].Rule)
			history = append(history, history...)
			require.Equal(t, findings, Evaluate(e, history, cfg), "duplicate event receipts cannot increase the count")
		})
	}
}

func TestPasswordSprayRequiresIndependentAttemptsAcrossAccounts(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	e := model.SecurityEvent{EventID: "current", RequestID: "current-request", Action: "auth.login_failed", Reason: "invalid_credentials", SubjectRef: "account:0", At: 1000, Visibility: 100, IP: "8.8.8.8"}
	history := make([]model.SecurityEvent, 19)
	for i := range history {
		history[i] = e
		history[i].EventID = fmt.Sprint("event-", i)
		history[i].RequestID = "duplicate-request"
		history[i].SubjectRef = fmt.Sprint("account:", (i+1)%5)
	}
	require.Empty(t, Evaluate(e, history, cfg))
	for i := range history {
		history[i].RequestID = fmt.Sprint("request-", i)
	}
	findings := Evaluate(e, history, cfg)
	require.Len(t, findings, 1)
	require.Equal(t, "password_spray", findings[0].Rule)
	require.Equal(t, 20, findings[0].Count)
	completeFindingMetadata(findings, e, append(history, history...), cfg)
	require.Equal(t, 5, findings[0].Dimensions["accounts"])
	history[0].Reason = "service_error"
	require.Empty(t, Evaluate(e, history, cfg))
}
