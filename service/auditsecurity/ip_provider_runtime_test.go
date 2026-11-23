package auditsecurity

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleProviderCachesAndPreservesUnderlyingSources(t *testing.T) {
	var requests atomic.Int64
	now := time.Now().Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		raw, _ := common.Marshal(map[string]any{"schema_version": 2, "ip": "8.8.8.8", "is_vpn": true,
			"sources":  []IPDataSource{{Source: "upstream_proxycheck", Name: "ProxyCheck", Capability: "vpn", DataUpdatedAt: now, ExpiresAt: now + 900}},
			"evidence": []IPDataEvidence{{Source: "upstream_proxycheck", Capability: "vpn", MatchedNetwork: "8.8.8.8", Reference: "proxycheck_assertion"}}})
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	p := operation_setting.IPIntelligenceProvider{ID: "selfhost", Name: "Selfhost", Type: operation_setting.IPProviderCustomIPAPI, Endpoint: server.URL, Enabled: true, Priority: 100, DailyLimit: 10, Capabilities: []string{"geo", "vpn", "proxy", "tor", "abuse"}, AllowedPrivateHosts: []string{"127.0.0.1"}}
	cfg := operation_setting.DefaultAuditSecuritySetting()
	manager := &ipIntelligenceManager{cache: map[string]ipProviderCacheEntry{}}
	db := newWorkerTestDB(t)
	for i := 0; i < 2; i++ {
		snapshot, calls := manager.lookupProviders(context.Background(), db, "8.8.8.8", cfg, []operation_setting.IPIntelligenceProvider{p}, 5)
		require.Equal(t, 1-i, calls)
		require.Equal(t, []string{"selfhost"}, snapshot.Providers)
		require.True(t, *snapshot.IsVPN)
		require.Equal(t, "upstream_proxycheck", snapshot.Evidence[0].Source)
	}
	require.EqualValues(t, 1, requests.Load())
	var state model.SecurityIPProviderState
	require.NoError(t, db.Where("provider_id = ?", p.ID).Take(&state).Error)
	require.EqualValues(t, 1, state.Used)
	reserved, err := reserveIPProviderCall(context.Background(), db, p, "changed")
	require.NoError(t, err)
	require.True(t, reserved)
	require.NoError(t, db.Where("provider_id = ?", p.ID).Take(&state).Error)
	require.EqualValues(t, 2, state.Used)
}

func TestSingleProviderRejectsMultipleAndDoesNotFallBack(t *testing.T) {
	manager := &ipIntelligenceManager{cache: map[string]ipProviderCacheEntry{}}
	cfg := operation_setting.DefaultAuditSecuritySetting()
	providers := []operation_setting.IPIntelligenceProvider{{ID: "a", Type: operation_setting.IPProviderCustomIPAPI, Enabled: true}, {ID: "b", Type: operation_setting.IPProviderCustomIPAPI, Enabled: true}}
	snapshot, calls := manager.lookupProviders(context.Background(), nil, "8.8.8.8", cfg, providers, 5)
	require.Zero(t, calls)
	require.Empty(t, snapshot.Providers)
	for i := range providers {
		providers[i].Type = operation_setting.IPProviderProxycheck
	}
	snapshot, calls = manager.lookupProviders(context.Background(), nil, "8.8.8.8", cfg, providers, 5)
	require.Zero(t, calls)
	require.Empty(t, snapshot.Providers)
}

func TestIPCacheRemainsAvailableWhenCycleBudgetIsExhausted(t *testing.T) {
	p := operation_setting.IPIntelligenceProvider{ID: "cached", Type: operation_setting.IPProviderCustomIPAPI, Enabled: true, Endpoint: "https://unused.invalid", Capabilities: []string{"abuse"}}
	m := &ipIntelligenceManager{cache: map[string]ipProviderCacheEntry{}}
	abuse := true
	m.store(p.ID+":"+ipProviderConfigHash(p)+":8.8.8.8", IPIntelligenceSnapshot{IsAbuser: &abuse}, time.Now().Unix()+60)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	for _, budget := range []int{0, 1} {
		result, calls := m.lookupProviders(t.Context(), nil, "8.8.8.8", cfg, []operation_setting.IPIntelligenceProvider{p}, budget)
		require.Zero(t, calls)
		require.NotNil(t, result.IsAbuser)
		require.True(t, *result.IsAbuser)
	}
	result, calls := m.lookupProviders(t.Context(), nil, "1.1.1.1", cfg, []operation_setting.IPIntelligenceProvider{p}, 0)
	require.Zero(t, calls)
	require.Empty(t, result.Providers, "an uncached IP must not make a request without budget")
}

func TestIPProviderConfigurationReportedWithoutStoppingOtherRules(t *testing.T) {
	db := newWorkerTestDB(t)
	healthMu.Lock()
	previousHealth := health
	healthMu.Unlock()
	t.Cleanup(func() {
		healthMu.Lock()
		health = previousHealth
		healthMu.Unlock()
	})
	now := time.Now().Unix()
	w := worker{db: db, source: db, owner: "provider-config", sourceStartAt: now}
	state, err := model.ClaimAuditSecurity(t.Context(), db, w.owner)
	require.NoError(t, err)
	entry := model.AuditLog{EventId: "provider-config-session-reuse", CreatedAt: now, UserId: 3, ActorRole: 1, Category: model.AuditCategorySecurity, Action: "auth.session_refresh_reuse"}
	require.NoError(t, db.Create(&entry).Error)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled = true
	provider := operation_setting.IPIntelligenceProvider{ID: "legacy", Name: "Legacy", Type: operation_setting.IPProviderProxycheck, Endpoint: "https://unused.invalid", Enabled: true, Priority: 1, Capabilities: []string{"geo"}}
	raw, err := common.Marshal([]operation_setting.IPIntelligenceProvider{provider})
	require.NoError(t, err)
	cfg.IPIntelligenceProvidersJSON = string(raw)
	require.NoError(t, w.scan(t.Context(), state, cfg))
	require.Equal(t, "invalid_provider_config", Status().ErrorCode)
	var alerts []model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "session_reuse").Find(&alerts).Error)
	require.Len(t, alerts, 1, "a missing IP provider must not stop other security rules")

	provider.Type = operation_setting.IPProviderCustomIPAPI
	raw, err = common.Marshal([]operation_setting.IPIntelligenceProvider{provider})
	require.NoError(t, err)
	cfg.IPIntelligenceProvidersJSON = string(raw)
	require.NoError(t, w.scan(t.Context(), state, cfg))
	require.Empty(t, Status().ErrorCode, "migrating to a callable provider clears the warning")
	cfg.IPIntelligenceProvidersJSON = "[]"
	require.NoError(t, w.scan(t.Context(), state, cfg))
	require.Equal(t, "invalid_provider_config", Status().ErrorCode)
	cfg.IPIntelligenceEnabled = false
	require.NoError(t, w.scan(t.Context(), state, cfg))
	require.Empty(t, Status().ErrorCode, "disabled intelligence does not require a provider")
}
