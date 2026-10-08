package operation_setting

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each step can multiply the length; a short error must not become a huge one.
func TestRelayErrorEdit_GrowthIsBounded(t *testing.T) {
	grow := RelayErrorEdit{Find: "a", Replace: strings.Repeat("a", MaxRelayErrorEditLen)}
	d := edited(t, strings.Repeat("a", 100), grow, grow, grow)
	assert.Equal(t, "兜底文案", d.Message, "about 400 MB would come out otherwise")

	// A zero-width pattern passes the empty-text check but inserts text; still bounded.
	boundary := edited(t, "hello World", RelayErrorEdit{Find: `\b`, Replace: "X", Regex: true})
	assert.Equal(t, "XhelloX XWorldX", boundary.Message, "small growth is allowed")
}

// Go expands a reference to a missing group into nothing: "$5 credit" would
// lose "$5". Such a replacement is refused, with the rule and step named.
func TestRelayErrorEdit_ReplacementReferences(t *testing.T) {
	refused := func(replace string) {
		t.Helper()
		err := ValidateRelayErrorDisplaySetting(enabledSetting(t, true,
			RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep},
			editRule(RelayErrorEdit{Find: "x"}, RelayErrorEdit{Find: `balance (\d+)`, Replace: replace, Regex: true}),
		))
		var stepErr *RelayErrorEditInvalid
		require.True(t, errors.As(err, &stepErr), "%q: %v", replace, err)
		assert.Equal(t, 2, stepErr.Rule)
		assert.Equal(t, 2, stepErr.Step)
		assert.ErrorIs(t, err, ErrRelayErrorDisplayInvalid)
	}
	refused("$5 credit")
	refused("USD$credit")
	refused("${2}")
	refused("$1x") // Go reads the name "1x", not group 1

	allowed := func(message, replace, want string) {
		t.Helper()
		d := edited(t, message, RelayErrorEdit{Find: `balance (?P<amount>\d+)`, Replace: replace, Regex: true})
		assert.Equal(t, want, d.Message, replace)
	}
	allowed("balance 12 left", "$$5 credit", "$5 credit left")
	allowed("balance 12 left", "credit $1", "credit 12 left")
	allowed("balance 12 left", "credit ${1}0", "credit 120 left")
	allowed("balance 12 left", "credit ${amount}", "credit 12 left")
	allowed("balance 12 left", "cost $", "cost $ left")
	allowed("balance 12 left", "cost $ each", "cost $ each left")

	literal := edited(t, "balance 12", RelayErrorEdit{Find: "balance", Replace: "$5"})
	assert.Equal(t, "$5 12", literal.Message, "plain text replacements never expand $")
}

// A step must not mangle a request id into something that is no longer removed:
// "(request id: up-1)" with "request id" deleted would leave "(: up-1)", and the
// upstream's id would reach the client.
func TestRelayErrorEdit_RequestIdsAreRemovedBeforeEditing(t *testing.T) {
	d := edited(t, "upstream failed (request id: up-1)", RelayErrorEdit{Find: "request id"})
	assert.Equal(t, "upstream failed", d.Message)

	s := enabledSetting(t, true, editRule(RelayErrorEdit{Find: "request id"}))
	view := compileRelayErrorDisplayParts(s)
	content, replaced := maskLogWith(view, "upstream failed (request id: up-1) (request id: r1)", "openai_error", "", 503, "r1", "")
	assert.True(t, replaced)
	assert.Equal(t, "upstream failed (request id: r1)", content, "only our own id, once")

	relayErrorDisplaySnapshot.Publish(*view)
	t.Cleanup(func() { relayErrorDisplaySnapshot.Publish(relayErrorDisplayView{}) })
	message, _ := MaskRelayStreamErrorForUser("stream cut (request id: up-2)", "")
	assert.Equal(t, "stream cut", message, "the stream frame carries no upstream id either")
}

// A message the steps do not touch goes out exactly as it was.
func TestRelayErrorEdit_UntouchedMessageIsUnchanged(t *testing.T) {
	d := edited(t, "a   b\tc", RelayErrorEdit{Find: "missing"})
	assert.Equal(t, "a   b\tc", d.Message)
}
