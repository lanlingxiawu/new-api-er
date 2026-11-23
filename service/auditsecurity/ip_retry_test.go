package auditsecurity

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestIPRetryCompletesOnlyIPRulesAndPreservesCursor(t *testing.T) {
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	state, err := model.ClaimAuditSecurity(t.Context(), db, "ip-retry")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.FailureThreshold = true, 1
	p := operation_setting.IPIntelligenceProvider{ID: "cached", Name: "Cached", Priority: 100, Type: operation_setting.IPProviderCustomIPAPI, Endpoint: "https://unused.invalid", Enabled: true, Capabilities: []string{"abuse"}}
	raw, err := common.Marshal([]operation_setting.IPIntelligenceProvider{p})
	require.NoError(t, err)
	cfg.IPIntelligenceProvidersJSON = string(raw)
	failed := model.SecurityEvent{EventID: "failed", At: now - 1, Visibility: 100, Action: "auth.login_failed", Reason: "invalid_credentials", SubjectRef: "account:test", IP: "8.8.8.8"}
	login := model.SecurityEvent{EventID: "success", At: now, ActorID: 1, Visibility: 100, Action: "login", Success: true, SubjectRef: failed.SubjectRef, IP: failed.IP}
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{failed, login}, state, cfg))
	var base model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "login_after_failures").Take(&base).Error)
	// Durable pending receipts survive restarting the worker; no audit-log rescan is needed.
	require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id = ?", login.EventID).Update("ip_next_attempt_at", now-1).Error)
	var cursor model.SecurityState
	require.NoError(t, db.Where("name = ?", "scanner").Take(&cursor).Error)
	previous := globalIPIntelligence
	globalIPIntelligence = &ipIntelligenceManager{cache: map[string]ipProviderCacheEntry{}}
	t.Cleanup(func() { globalIPIntelligence = previous })
	abuse := true
	globalIPIntelligence.store(p.ID+":"+ipProviderConfigHash(p)+":"+login.IP, IPIntelligenceSnapshot{IsAbuser: &abuse}, now+60)
	w := worker{db: db}
	remaining := 0
	renewed, err := model.ClaimAuditSecurity(t.Context(), db, state.Owner)
	require.NoError(t, err)
	require.ErrorIs(t, w.retryIPIntelligence(t.Context(), state, cfg, &remaining), model.ErrSecurityConflict)
	var pending model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", login.EventID).Take(&pending).Error)
	require.Equal(t, "pending", pending.IPCheckState, "an expired worker must not complete IP checks")
	state = renewed
	require.NoError(t, w.retryIPIntelligence(t.Context(), state, cfg, &remaining))
	require.NoError(t, w.retryIPIntelligence(t.Context(), state, cfg, &remaining))
	// Overlapping source scans must not count either rule again.
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{login}, cursor, cfg))
	var risk model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "ip_abuse").Take(&risk).Error)
	require.EqualValues(t, 1, risk.Occurrences)
	var after model.SecurityAlert
	require.NoError(t, db.First(&after, base.ID).Error)
	require.Equal(t, base.Version, after.Version)
	require.Equal(t, base.Occurrences, after.Occurrences)
	var receipt model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", login.EventID).Take(&receipt).Error)
	require.Equal(t, "done", receipt.IPCheckState)
	var saved model.SecurityState
	require.NoError(t, db.Where("name = ?", "scanner").Take(&saved).Error)
	require.Equal(t, cursor.CursorAt, saved.CursorAt)
	require.Equal(t, cursor.CursorEvent, saved.CursorEvent)
	var count int64
	require.NoError(t, db.Model(&model.SecurityFindingSnapshot{}).Where("rule = ?", "ip_abuse").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestOrdinaryProxyObservationsDoNotCreateAlerts(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "proxy-noise")
	require.NoError(t, err)
	yes, no := true, false
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.RelayIPEnabled = true
	for caseIndex, tc := range []struct {
		name  string
		intel IPIntelligenceSnapshot
		rules []string
	}{
		{"vpn", IPIntelligenceSnapshot{IsVPN: &yes, IsAbuser: &no}, nil},
		{"proxy", IPIntelligenceSnapshot{IsProxy: &yes}, nil},
		{"unknown", IPIntelligenceSnapshot{}, nil},
		{"tor", IPIntelligenceSnapshot{IsVPN: &yes, IsTor: &yes}, []string{"anonymous_network"}},
		{"abuse", IPIntelligenceSnapshot{IsProxy: &yes, IsAbuser: &yes}, []string{"ip_abuse"}},
		{"tor-abuse", IPIntelligenceSnapshot{IsVPN: &yes, IsTor: &yes, IsAbuser: &yes}, []string{"anonymous_network", "ip_abuse"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for actionIndex, action := range []string{"relay.ip_observed", "login", "access_token.generate", "auth.login_failed"} {
				facts, err := common.Marshal(Facts{IPIntelligence: &tc.intel})
				require.NoError(t, err)
				event := model.SecurityEvent{EventID: tc.name + "-" + action, Action: action, At: time.Now().Unix(), ActorID: 123 + caseIndex*4 + actionIndex, ActorRole: 1, Visibility: 100, Success: true, Count: 1, SubjectRef: tc.name + "-" + action, ResourceType: "account", IP: "8.8.8.8", Facts: string(facts)}
				if action == "auth.login_failed" {
					event.ActorID, event.Success, event.Reason = 0, false, "invalid_credentials"
				}
				require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{event}, state, cfg))
				var persisted model.SecurityEvent
				require.NoError(t, db.Where("event_id = ?", event.EventID).Take(&persisted).Error)
				var persistedFacts Facts
				require.NoError(t, common.UnmarshalJsonStr(persisted.Facts, &persistedFacts))
				require.Equal(t, &tc.intel, persistedFacts.IPIntelligence, "network facts must remain available without a proxy alert")
				var snapshots []model.SecurityFindingSnapshot
				require.NoError(t, db.Where("trigger_event_id = ?", event.EventID).Find(&snapshots).Error)
				var rules []string
				for _, snapshot := range snapshots {
					rules = append(rules, snapshot.Rule)
				}
				require.ElementsMatch(t, tc.rules, rules)
			}
		})
	}
}

func TestIPRetryBudgetExpiryAndDeferredRequests(t *testing.T) {
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	state, err := model.ClaimAuditSecurity(t.Context(), db, "ip-budgets")
	require.NoError(t, err)
	var calls atomic.Int64
	server, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		for {
			conn, err := server.Accept()
			if err != nil {
				return
			}
			calls.Add(1)
			_ = conn.Close() // Deterministic TLS/connection failure, without external traffic.
		}
	}()
	p := operation_setting.IPIntelligenceProvider{ID: "retry", Name: "Retry", Priority: 100, Type: operation_setting.IPProviderCustomIPAPI, Endpoint: "https://" + server.Addr().String(), Enabled: true, Capabilities: []string{"abuse"}, AllowedPrivateHosts: []string{"127.0.0.1"}}
	raw, err := common.Marshal([]operation_setting.IPIntelligenceProvider{p})
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.IPIntelligenceProvidersJSON = true, string(raw)
	previous := globalIPIntelligence
	globalIPIntelligence = &ipIntelligenceManager{cache: map[string]ipProviderCacheEntry{}}
	t.Cleanup(func() { globalIPIntelligence = previous })
	events := []model.SecurityEvent{
		{EventID: "due", At: now, ActorID: 1, Visibility: 100, Action: "login", Success: true, IP: "8.8.8.8", IPCheckState: "pending", IPNextAttemptAt: now - 1},
		{EventID: "later", At: now, ActorID: 2, Visibility: 100, Action: "login", Success: true, IP: "1.1.1.1", IPCheckState: "pending", IPNextAttemptAt: now + 600},
		{EventID: "expired", At: now - 86401, ActorID: 3, Visibility: 100, Action: "login", Success: true, IP: "9.9.9.9", IPCheckState: "pending", IPNextAttemptAt: now - 2},
		{EventID: "legacy", At: now, ActorID: 4, Visibility: 100, Action: "login", Success: true, IP: "8.8.4.4"},
	}
	require.NoError(t, db.Create(&events).Error)
	w := worker{db: db}
	remaining := 1
	require.NoError(t, w.retryIPIntelligence(t.Context(), state, cfg, &remaining))
	require.Zero(t, remaining)
	require.EqualValues(t, 1, calls.Load())
	require.NoError(t, w.retryIPIntelligence(t.Context(), state, cfg, &remaining))
	require.EqualValues(t, 1, calls.Load(), "a deferred request must not be retried immediately")
	var due, expired, legacy model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", "due").Take(&due).Error)
	require.Equal(t, "pending", due.IPCheckState)
	require.GreaterOrEqual(t, due.IPNextAttemptAt, now+60)
	require.NoError(t, db.Where("event_id = ?", "expired").Take(&expired).Error)
	require.Equal(t, "skipped", expired.IPCheckState)
	require.NoError(t, db.Where("event_id = ?", "legacy").Take(&legacy).Error)
	require.Empty(t, legacy.IPCheckState)
	// A separate page shares the same exhausted budget, rather than resetting it.
	page := []model.SecurityEvent{{EventID: "another-page", At: now, Action: "login", Success: true, IP: "4.2.2.2"}}
	require.NoError(t, enrichSecurityEvents(t.Context(), db, page, cfg, &remaining))
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, "pending", page[0].IPCheckState)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, w.retryIPIntelligence(ctx, state, cfg, &remaining))
}
