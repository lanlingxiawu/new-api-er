package operation_setting

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression (branch audit L4): RE2 is linear in the text, but with a constant
// equal to the number of program states that can be active at once. The
// 200-character limit alone admitted `.{1,999}`×24+`Q`, which compiles to about
// 48k instructions and took about a second per step on a 4000-character error,
// synchronously on the relay goroutine (decide() via PresentRelayError). Such a
// step is now refused at save time, naming the rule and step.
func TestBranchAuditRegression_AcceptedRegexStepCanStallErrorPath(t *testing.T) {
	pattern := strings.Repeat(`.{1,999}`, 24) + "Q"
	require.LessOrEqual(t, len(pattern), MaxRelayErrorEditLen, "the length limit alone does not catch it")
	setting := enabledSetting(t, true,
		RelayErrorRule{Source: RelayErrorSourceLocal, Action: RelayErrorActionKeep},
		RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionEdit,
			Edits: []RelayErrorEdit{{Find: `\(request id: [^)]*\)`, Regex: true}, {Find: pattern, Replace: "x", Regex: true}}})

	err := ValidateRelayErrorDisplaySetting(setting)
	var stepErr *RelayErrorEditInvalid
	require.True(t, errors.As(err, &stepErr), "%v", err)
	assert.ErrorIs(t, err, ErrRelayErrorDisplayInvalid)
	assert.Equal(t, 2, stepErr.Rule)
	assert.Equal(t, 2, stepErr.Step)
	assert.True(t, stepErr.TooComplex, "the admin is told to simplify, not that the syntax is wrong")

	_, err = PreviewRelayErrorDecision(setting, RelayErrorInput{Upstream: true, Message: "x"})
	assert.True(t, errors.As(err, &stepErr), "preview refuses it the same way")

	// A stored configuration saved before the bound existed loses only that
	// rule: the error path never runs the pattern, and the rest (rule 1, the
	// upstream fallback) stays in effect.
	view := compileRelayErrorDisplayParts(setting)
	started := time.Now()
	d := view.decide(RelayErrorInput{Upstream: true, StatusCode: 500, Message: strings.Repeat("abcdefghij ", 364)})
	assert.True(t, d.Replace)
	assert.Equal(t, -1, d.RuleIndex, "the skipped rule does not match; hidden by the upstream fallback")
	assert.Less(t, time.Since(started), 100*time.Millisecond)
}

// The budget counts compiled instructions of a rule's regular expressions
// together: exactly at the budget is accepted, one more is refused at the step
// that crosses it.
func TestRelayErrorEdit_RegexBudgetBoundary(t *testing.T) {
	// (?i)x{n} compiles to n instructions (one folded class per copy) plus a
	// fixed overhead.
	overhead := relayErrorRegexSize("(?i)x{1}") - 1
	exact := fmt.Sprintf("x{%d}", MaxRelayErrorRuleRegexSize-overhead)
	require.Equal(t, MaxRelayErrorRuleRegexSize, relayErrorRegexSize("(?i)"+exact))
	over := fmt.Sprintf("x{%d}", MaxRelayErrorRuleRegexSize-overhead+1)

	require.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true, editRule(RelayErrorEdit{Find: exact, Regex: true}))))

	err := ValidateRelayErrorDisplaySetting(enabledSetting(t, true, editRule(RelayErrorEdit{Find: over, Regex: true})))
	var stepErr *RelayErrorEditInvalid
	require.True(t, errors.As(err, &stepErr), "%v", err)
	assert.True(t, stepErr.TooComplex)
	assert.Equal(t, 1, stepErr.Step)

	// Two steps each within the budget but over it together: the second is named.
	half := fmt.Sprintf("y{%d}", MaxRelayErrorRuleRegexSize/2)
	err = ValidateRelayErrorDisplaySetting(enabledSetting(t, true, editRule(RelayErrorEdit{Find: half, Regex: true}, RelayErrorEdit{Find: half, Regex: true})))
	require.True(t, errors.As(err, &stepErr), "%v", err)
	assert.True(t, stepErr.TooComplex)
	assert.Equal(t, 2, stepErr.Step)

	// The budget is per rule: the same steps split over two rules are accepted.
	require.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true,
		editRule(RelayErrorEdit{Find: half, Regex: true}), editRule(RelayErrorEdit{Find: half, Regex: true}))))

	// Other invalid steps are not reported as too complex.
	err = ValidateRelayErrorDisplaySetting(enabledSetting(t, true, editRule(RelayErrorEdit{Find: "(", Regex: true})))
	require.True(t, errors.As(err, &stepErr), "%v", err)
	assert.False(t, stepErr.TooComplex)
}

// Plain-text steps are not counted: a rule of twenty 200-character texts is an
// ordinary rule. Ordinary patterns, including the design document's examples,
// stay well within the budget.
func TestRelayErrorEdit_RegexBudgetKeepsOrdinaryRules(t *testing.T) {
	literal := make([]RelayErrorEdit, MaxRelayErrorRuleEdits)
	for i := range literal {
		literal[i] = RelayErrorEdit{Find: strings.Repeat(fmt.Sprintf("上游分组%02d ", i), MaxRelayErrorEditLen/7)} // 196 characters
	}
	require.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true, editRule(literal...))))

	ordinary := []RelayErrorEdit{
		{Find: `当前分组 \S+ 下`, Regex: true},
		{Find: `\(request id: [^)]*\)`, Regex: true},
		{Find: `(org|group|分组)[-_ ]?[a-z0-9-]{3,40}`, Regex: true},
		{Find: `https?://[^\s]+`, Regex: true},
		{Find: `sk-[A-Za-z0-9]{20,64}`, Regex: true},
		{Find: `(?P<name>[a-z]+)-\d{1,6}`, Replace: "${name}", Regex: true},
		{Find: `[\p{Han}]+渠道`, Regex: true},
		{Find: `req_[a-zA-Z0-9]{24}`, Regex: true},
	}
	require.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true, editRule(ordinary...))))
}

// The worst rule the budget admits (every state active on every character)
// stays far from stalling the error path.
func TestRelayErrorEdit_WorstAcceptedRuleIsFast(t *testing.T) {
	var edits []RelayErrorEdit
	size := 0
	for {
		step := RelayErrorEdit{Find: `.{1,99}Q`, Replace: "x", Regex: true}
		next := relayErrorRegexSize("(?i)" + step.Find)
		if size+next > MaxRelayErrorRuleRegexSize || len(edits) == MaxRelayErrorRuleEdits {
			break
		}
		edits, size = append(edits, step), size+next
	}
	require.NotEmpty(t, edits)
	setting := enabledSetting(t, true, editRule(edits...))
	require.NoError(t, ValidateRelayErrorDisplaySetting(setting))
	view, err := compileRelayErrorDisplay(setting)
	require.NoError(t, err)
	started := time.Now()
	view.decide(RelayErrorInput{Upstream: true, StatusCode: 500, Message: strings.Repeat("abcdefghij ", 364)})
	assert.Less(t, time.Since(started), 100*time.Millisecond, "%d steps, %d instructions", len(edits), size)
}
