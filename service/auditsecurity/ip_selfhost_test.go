package auditsecurity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestSelfHostedEvidenceExpiryAndCapabilities(t *testing.T) {
	now := time.Now().Unix()
	response := map[string]any{
		"schema_version": 2, "ip": "8.8.8.8", "is_abuser": true,
		"sources":  []IPDataSource{{Source: "drop_v4", Name: "Spamhaus DROP IPv4", Capability: "abuse", DataUpdatedAt: now - 60, ExpiresAt: now + 300}},
		"evidence": []IPDataEvidence{{Source: "drop_v4", Capability: "abuse", MatchedNetwork: "8.8.8.0/24", Reference: "SBL123"}, {Source: "drop_v4", Capability: "abuse", MatchedNetwork: "1.1.1.0/24"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := common.Marshal(response)
		require.NoError(t, err)
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	provider := operation_setting.IPIntelligenceProvider{ID: "local", Type: operation_setting.IPProviderCustomIPAPI, Endpoint: server.URL, AllowedPrivateHosts: []string{"127.0.0.1"}}
	snapshot, failure, _ := callIPProvider(context.Background(), provider, "8.8.8.8", 2)
	require.Empty(t, failure)
	require.True(t, *snapshot.IsAbuser)
	require.Len(t, snapshot.Evidence, 1)
	require.Equal(t, now-60, snapshot.Evidence[0].DataUpdatedAt)
	require.LessOrEqual(t, ipProviderCacheTTLSeconds(provider, snapshot, operation_setting.DefaultAuditSecuritySetting()), int64(300))
	filtered := filterIPIntelligenceCapabilities(snapshot, []string{"geo"})
	require.Nil(t, filtered.IsAbuser)
	require.Empty(t, filtered.Sources)
	require.Empty(t, filtered.Evidence)
	require.Len(t, snapshot.Evidence, 1, "filter must not mutate cached source slices")
	merged := IPIntelligenceSnapshot{FieldProviders: map[string]string{}}
	mergeIPIntelligence(&merged, snapshot, "local")
	require.Equal(t, "local", merged.Evidence[0].ProviderID)
	response["sources"] = []IPDataSource{{Source: "drop_v4", Capability: "abuse", DataUpdatedAt: now - 100, ExpiresAt: now - 1}}
	stale, failure, _ := callIPProvider(context.Background(), provider, "8.8.8.8", 2)
	require.Empty(t, failure)
	require.Nil(t, stale.IsAbuser)
	require.Empty(t, stale.Evidence)
}

func TestSelfHostedASNAndEvidenceDoNotOverrideSelectedVerdict(t *testing.T) {
	no, yes := false, true
	target := IPIntelligenceSnapshot{FieldProviders: map[string]string{"abuse": "first"}, IsAbuser: &no}
	source := IPIntelligenceSnapshot{IsAbuser: &yes, ASNNumber: 15169, ASNOrganization: "Google", Evidence: []IPDataEvidence{{Source: "drop_v4", Capability: "abuse", MatchedNetwork: "8.8.8.0/24"}}}
	mergeIPIntelligence(&target, source, "second")
	require.False(t, *target.IsAbuser)
	require.Empty(t, target.Evidence)
	require.EqualValues(t, 15169, target.ASNNumber)
	require.Equal(t, "second", target.FieldProviders["asn"])
	filtered := filterIPIntelligenceCapabilities(target, []string{"tor"})
	require.Zero(t, filtered.ASNNumber)
}

func TestSelfHostedEvidenceSurvivesAlertStorage(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	ctx := context.Background()
	now := time.Now().Unix()
	yes := true
	snapshot := IPIntelligenceSnapshot{
		ObservedAt: now, IsAbuser: &yes, ASNNumber: 15169, ASNOrganization: "Example network",
		FieldProviders: map[string]string{},
	}
	applySelfHostedIPMetadata(&snapshot,
		[]IPDataSource{{Source: "drop_v4", Name: "Spamhaus DROP IPv4", Capability: "abuse", DataUpdatedAt: now - 60, ExpiresAt: now + 3600}},
		[]IPDataEvidence{{Source: "drop_v4", Capability: "abuse", MatchedNetwork: "8.8.8.0/24", Reference: "SBL123"}}, "8.8.8.8")
	merged := IPIntelligenceSnapshot{FieldProviders: map[string]string{}}
	mergeIPIntelligence(&merged, snapshot, "selfhost")
	facts, err := common.Marshal(Facts{IPIntelligence: &merged})
	require.NoError(t, err)
	state, err := model.ClaimAuditSecurity(ctx, db, "selfhost-evidence")
	require.NoError(t, err)
	event := model.SecurityEvent{EventID: "selfhost-login", At: now, Action: "login", Success: true, ActorID: 9, ActorRole: 1, Visibility: 100, SubjectRef: "account:9", ResourceType: "account", IP: "8.8.8.8", Facts: string(facts)}
	next := state
	next.CursorAt, next.CursorEvent = now, event.EventID
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, next, operation_setting.DefaultAuditSecuritySetting()))
	var alert model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "ip_abuse").Take(&alert).Error)
	detail, err := GetAlert(ctx, db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 1)
	var stored Facts
	require.NoError(t, common.UnmarshalJsonStr(detail.Events[0].Facts, &stored))
	require.Len(t, stored.IPIntelligence.Evidence, 1)
	require.Equal(t, "8.8.8.0/24", stored.IPIntelligence.Evidence[0].MatchedNetwork)
	require.Equal(t, "SBL123", stored.IPIntelligence.Evidence[0].Reference)
	require.Equal(t, "selfhost", stored.IPIntelligence.Evidence[0].ProviderID)
	require.Len(t, detail.Findings, 1)
}
