package auditsecurity

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Execution provenance is internal even for Root. Dashboard evidence retains
// operation facts and provider proof, never identity/session recovery metadata.
func publicSecurityEvent(event model.SecurityEvent) model.SecurityEvent {
	var facts Facts
	if common.UnmarshalJsonStr(event.Facts, &facts) != nil {
		event.Facts = ""
		return event
	}
	facts.Identity = nil
	if target := eventLoginTarget(event); target != nil {
		event.TargetRole = target.Role
	}
	facts.LoginTarget = nil
	facts.LivePolicyAt = 0
	facts.FirstObservedAt = 0
	facts.NewManagementSource = false
	facts.SourceBaseline = nil
	raw, err := common.Marshal(facts)
	if err != nil {
		event.Facts = ""
	} else {
		event.Facts = string(raw)
	}
	return event
}
