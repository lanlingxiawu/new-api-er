package operation_setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// Cost of deciding one client-facing error on the error path, per rule action.
// Run: go test ./setting/operation_setting/ -run '^$' -bench RelayErrorDecide -benchmem

func benchView(b *testing.B, rules ...RelayErrorRule) *relayErrorDisplayView {
	b.Helper()
	raw, err := common.Marshal(rules)
	if err != nil {
		b.Fatal(err)
	}
	view, err := compileRelayErrorDisplay(RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "fallback", Rules: string(raw)})
	if err != nil {
		b.Fatal(err)
	}
	return view
}

var benchInput = RelayErrorInput{
	Upstream: true, StatusCode: 400,
	Message: "Your organization must be verified to stream this model. Group vip-A under model gpt-5 has no available channel (request id: 2026092812abcdef)",
}

func BenchmarkRelayErrorDecide_Fallback(b *testing.B) {
	view := benchView(b)
	for b.Loop() {
		_ = view.decide(benchInput)
	}
}

func BenchmarkRelayErrorDecide_Replace(b *testing.B) {
	view := benchView(b, RelayErrorRule{Source: RelayErrorSourceUpstream, StatusCodes: []int{400}, Action: RelayErrorActionReplace, Message: "busy"})
	for b.Loop() {
		_ = view.decide(benchInput)
	}
}

func BenchmarkRelayErrorDecide_EditLiteral3(b *testing.B) {
	view := benchView(b, RelayErrorRule{Source: RelayErrorSourceUpstream, Action: RelayErrorActionEdit, Edits: []RelayErrorEdit{
		{Find: "your organization"}, {Find: "no available channel", Replace: "is busy"}, {Find: "vip-a"},
	}})
	for b.Loop() {
		_ = view.decide(benchInput)
	}
}

func BenchmarkRelayErrorDecide_EditRegex3(b *testing.B) {
	view := benchView(b, RelayErrorRule{Source: RelayErrorSourceUpstream, Action: RelayErrorActionEdit, Edits: []RelayErrorEdit{
		{Find: `must be verified to (\w+) this model`, Replace: "cannot $1", Regex: true},
		{Find: `group \S+ under`, Replace: "for", Regex: true},
		{Find: `no available channel`, Replace: "is busy"},
	}})
	for b.Loop() {
		_ = view.decide(benchInput)
	}
}

// The worst allowed rule: 20 regex steps over a 4000-character upstream message.
func BenchmarkRelayErrorDecide_EditRegex20Long(b *testing.B) {
	edits := make([]RelayErrorEdit, MaxRelayErrorRuleEdits)
	for i := range edits {
		edits[i] = RelayErrorEdit{Find: `(?:token|quota|group)\s+\S+`, Replace: "x", Regex: true}
	}
	view := benchView(b, RelayErrorRule{Source: RelayErrorSourceUpstream, Action: RelayErrorActionEdit, Edits: edits})
	long := benchInput
	long.Message = strings.Repeat("upstream said token abc quota 12 group vip and more text. ", 70)
	for b.Loop() {
		_ = view.decide(long)
	}
}
