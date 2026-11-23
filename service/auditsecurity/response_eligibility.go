package auditsecurity

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// Eligibility is tied to the original authenticated event, never to a user
// snapshot taken later by the scanner. Enrichment cannot renew this deadline.
func automaticResponseEligibility(event model.SecurityEvent, finding model.SecurityFinding, cfg operation_setting.AuditSecuritySetting, now int64, truncated bool) (Facts, string) {
	var facts Facts
	if common.UnmarshalJsonStr(event.Facts, &facts) != nil || truncated || finding.Truncated || facts.EvidenceIncomplete || event.ActorID <= 0 || facts.Identity == nil || facts.Identity.ActorID != event.ActorID || facts.Identity.Version() <= 0 {
		return facts, "incomplete_evidence"
	}
	if facts.FirstObservedAt <= 0 || facts.FirstObservedAt > now || facts.LivePolicyAt != cfg.AutoBanEffectiveAt || cfg.AutoBanEffectiveAt <= 0 || event.At <= cfg.AutoBanEffectiveAt || event.At < now-120 || event.At > now {
		return facts, "expired"
	}
	for _, evidence := range finding.Evidence {
		var proof Facts
		if common.UnmarshalJsonStr(evidence.Facts, &proof) != nil || proof.Identity == nil || proof.Identity.ActorID != evidence.ActorID || proof.Identity.Version() <= 0 {
			return facts, "incomplete_evidence"
		}
		if evidence.ActorID == event.ActorID && proof.Identity.Version() != facts.Identity.Version() {
			return facts, "superseded"
		}
	}
	if strings.HasPrefix(finding.Rule, "risky_ip_") {
		providers, err := operation_setting.ParseIPIntelligenceProviders(cfg.IPIntelligenceProvidersJSON)
		provider := operation_setting.ActiveIPIntelligenceProvider(providers)
		if err != nil || !cfg.IPIntelligenceEnabled || provider == nil || !slices.Contains(provider.Capabilities, "abuse") || !currentAbuseEvidence(event.IP, facts.IPIntelligence, now) || facts.IPIntelligence.FieldProviders["abuse"] != provider.ID {
			return facts, "incomplete_evidence"
		}
	}
	return facts, ""
}
