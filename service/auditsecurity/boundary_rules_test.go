package auditsecurity

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestSingleSensitiveOperationsDoNotRequireMultipleKeys(t *testing.T) {
	one := 1
	for _, action := range []string{"token.create", "token.key_view", "token.key_view_batch", "access_token.generate", "user.password_change", "user.2fa_enable", "user.2fa_disable_self", "user.passkey_register", "user.binding_unbind"} {
		t.Run(action, func(t *testing.T) {
			e := compromiseEvent("one-operation", 7, 1000, "8.8.8.8", action)
			f := Facts{ID: 1, TokenID: 1, Count: &one, ReturnedIDs: []int{1}}
			require.True(t, successfulSensitiveOperation(e, f))
			e.Success = false
			require.False(t, successfulSensitiveOperation(e, f))
			e.Success = true
			f.EvidenceIncomplete = true
			require.False(t, successfulSensitiveOperation(e, f))
		})
	}
	for _, action := range []string{"token.create", "token.key_view", "token.key_view_batch", "access_token.generate", "channel.update", "access_token.update", "user.profile_update"} {
		require.False(t, successfulSensitiveOperation(compromiseEvent(action, 7, 1000, "8.8.8.8", action), Facts{}), action)
	}
}

func TestResponseEligibilityNeverRenewsOriginalEventDeadline(t *testing.T) {
	now := int64(10000)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.AutoBanEffectiveAt = 9000
	for _, age := range []int64{-1, 0, 119, 120, 121, 600} {
		t.Run(fmt.Sprint(age), func(t *testing.T) {
			event := compromiseEvent("deadline", 7, now-age, "8.8.8.8", "auth.session_refresh_reuse")
			var facts Facts
			require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
			facts.LivePolicyAt = 9000
			facts.FirstObservedAt = now
			raw, err := common.Marshal(facts)
			require.NoError(t, err)
			event.Facts = string(raw)
			_, reason := automaticResponseEligibility(event, model.SecurityFinding{Rule: "session_reuse"}, cfg, now, false)
			if age < 0 || age > 120 {
				require.Equal(t, "expired", reason)
			} else {
				require.Empty(t, reason)
			}
			facts.LivePolicyAt = 8999
			raw, err = common.Marshal(facts)
			require.NoError(t, err)
			event.Facts = string(raw)
			_, reason = automaticResponseEligibility(event, model.SecurityFinding{Rule: "session_reuse"}, cfg, now, false)
			require.Equal(t, "expired", reason)
		})
	}
}

func TestIPResponseRequiresExactCurrentAbuseEvidence(t *testing.T) {
	now := int64(1100)
	yes, no := true, false
	e := compromiseEvent("one-key", 7, 1090, "8.8.8.8", "token.create")
	base := IPIntelligenceSnapshot{IsAbuser: &yes, ObservedAt: 1095, Providers: []string{"selfhost"}, FieldProviders: map[string]string{"abuse": "selfhost"}, ExpiresAt: 1200,
		Evidence: []IPDataEvidence{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", MatchedNetwork: "8.8.8.8/32", DataUpdatedAt: 1000, ExpiresAt: 1200}},
		Sources:  []IPDataSource{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", DataUpdatedAt: 1000, ExpiresAt: 1200}}}
	for _, tc := range []struct {
		name   string
		change func(*IPIntelligenceSnapshot)
		want   bool
	}{
		{"exact", func(*IPIntelligenceSnapshot) {}, true},
		{"legacy boolean", func(s *IPIntelligenceSnapshot) { s.Evidence = nil }, false},
		{"broad network", func(s *IPIntelligenceSnapshot) { s.Evidence[0].MatchedNetwork = "8.8.8.0/24" }, false},
		{"other address", func(s *IPIntelligenceSnapshot) { s.Evidence[0].MatchedNetwork = "1.1.1.1" }, false},
		{"expired", func(s *IPIntelligenceSnapshot) { s.Evidence[0].ExpiresAt = now }, false},
		{"future update", func(s *IPIntelligenceSnapshot) { s.Evidence[0].DataUpdatedAt = now + 1 }, false},
		{"unknown", func(s *IPIntelligenceSnapshot) { s.IsAbuser = nil }, false},
		{"proxy only", func(s *IPIntelligenceSnapshot) { s.IsAbuser = &no; s.IsProxy = &yes }, false},
		{"no source", func(s *IPIntelligenceSnapshot) { s.Sources = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			s.Evidence = append([]IPDataEvidence(nil), base.Evidence...)
			tc.change(&s)
			require.Equal(t, tc.want, currentAbuseEvidence(e.IP, &s, now))
		})
	}
}

func TestNormalizeUsesServerIdentityInsteadOfOperationParameters(t *testing.T) {
	a := model.AuditLog{UserId: 7, ActorRole: common.RoleCommonUser, Action: "token.create", Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"id": 1, "identity": model.AuditIdentity{ActorID: 999, AuthVersion: 999}}}}}
	var f Facts
	require.NoError(t, common.UnmarshalJsonStr(Normalize(a).Facts, &f))
	require.Nil(t, f.Identity)
	a.Other.Op.Identity = &model.AuditIdentity{ActorID: 7, Role: common.RoleCommonUser, AuthVersion: 4}
	require.NoError(t, common.UnmarshalJsonStr(Normalize(a).Facts, &f))
	require.EqualValues(t, 4, f.Identity.AuthVersion)
	a.Other.Op.Identity.ActorID = 8
	f = Facts{}
	require.NoError(t, common.UnmarshalJsonStr(Normalize(a).Facts, &f))
	require.Nil(t, f.Identity)
}
