package operation_setting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Branch audit: boundary and failure-mode contracts of the relay error display
// configuration not pinned by the existing tests.

func TestBranchAuditRelayErrorDisplay_NullRulesAndBlankDefault(t *testing.T) {
	require.NoError(t, ValidateRelayErrorDisplaySetting(RelayErrorDisplaySetting{Rules: "null"}), "a JSON null rule list means no rules")
	require.NoError(t, ValidateRelayErrorDisplaySetting(RelayErrorDisplaySetting{Rules: "  "}))

	// A whitespace-only default message counts as unset: the built-in text is used,
	// never an empty message.
	view, err := compileRelayErrorDisplay(RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "   "})
	require.NoError(t, err)
	d := view.decide(RelayErrorInput{Upstream: true, StatusCode: 500, Message: "boom"})
	assert.True(t, d.Replace)
	assert.NotEmpty(t, strings.TrimSpace(d.Message))
	assert.Equal(t, -1, d.RuleIndex)
}

func TestBranchAuditRelayErrorDisplay_StatusBoundaries(t *testing.T) {
	ok := func(rule RelayErrorRule) {
		t.Helper()
		assert.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true, rule)))
	}
	bad := func(rule RelayErrorRule) {
		t.Helper()
		assert.ErrorIs(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true, rule)), ErrRelayErrorDisplayInvalid)
	}
	replace := func(status int, match ...int) RelayErrorRule {
		return RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionReplace, Message: "m", StatusCode: status, StatusCodes: match}
	}
	ok(replace(0))
	ok(replace(400))
	ok(replace(599))
	bad(replace(399))
	bad(replace(600))
	bad(replace(-1))
	ok(replace(0, 100, 599))
	bad(replace(0, 600))
	bad(replace(0, 0))

	// Default message and rule message are counted in characters, not bytes.
	cjk := strings.Repeat("额", MaxRelayErrorMessageLen)
	assert.NoError(t, ValidateRelayErrorDisplaySetting(RelayErrorDisplaySetting{DefaultMessage: cjk}))
	assert.ErrorIs(t, ValidateRelayErrorDisplaySetting(RelayErrorDisplaySetting{DefaultMessage: cjk + "额"}), ErrRelayErrorDisplayInvalid)
	ok(RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionReplace, Message: cjk})
}

// Error codes and keywords ignore case on both sides, including non-ASCII.
func TestBranchAuditRelayErrorDisplay_CaseInsensitiveMatching(t *testing.T) {
	s := enabledSetting(t, false,
		RelayErrorRule{Source: RelayErrorSourceAny, ErrorCodes: []string{" Model_Not_Found "}, Action: RelayErrorActionReplace, Message: "code"},
		RelayErrorRule{Source: RelayErrorSourceAny, Keywords: []string{"ÄRGER"}, Action: RelayErrorActionReplace, Message: "keyword"},
	)
	assert.Equal(t, "code", decide(t, s, RelayErrorInput{ErrorCode: "MODEL_NOT_FOUND", Message: "x"}).Message)
	assert.Equal(t, "keyword", decide(t, s, RelayErrorInput{Message: "großer ärger upstream"}).Message)
	assert.False(t, decide(t, s, RelayErrorInput{Upstream: true, Message: "unrelated"}).Replace, "hide-upstream off and no rule: unchanged")
}

// One stored rule that no longer compiles (hand-edited row, older build) is
// skipped; the feature stays on and the upstream fallback still hides upstream
// errors, live and in the user's log. It is reported for the settings page.
func TestBranchAuditRelayErrorDisplay_InvalidStoredRuleIsSkipped(t *testing.T) {
	previous := GetRelayErrorDisplaySetting()
	t.Cleanup(func() { ReplaceRelayErrorDisplaySetting(previous) })

	broken := RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "fallback",
		Rules: `[{"source":"any","action":"edit","edits":[{"find":"(unclosed","regex":true}]}]`}
	require.Error(t, ValidateRelayErrorDisplaySetting(broken))
	ReplaceRelayErrorDisplaySetting(broken)
	assert.True(t, RelayErrorDisplayEnabled())
	d := DecideRelayError(RelayErrorInput{Upstream: true, StatusCode: 500, Message: "upstream secret"})
	assert.True(t, d.Replace)
	assert.Equal(t, "fallback", d.Message)
	content, _, replaced := MaskRelayErrorLogInputForUser(RelayErrorInput{Upstream: true, StatusCode: 500, Message: "upstream secret", Lang: "en"}, "r")
	assert.True(t, replaced)
	assert.Equal(t, "fallback (request id: r)", content)
	skipped := RelayErrorDisplaySkipped()
	require.Len(t, skipped, 1)
	assert.Equal(t, 1, skipped[0].Rule)
	var stepErr *RelayErrorEditInvalid
	assert.ErrorAs(t, skipped[0].Err, &stepErr)
}

// The user-log entry point appends our request id only when there is one.
func TestBranchAuditRelayErrorDisplay_MaskErrorLogRequestId(t *testing.T) {
	view := compileRelayErrorDisplayParts(RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "fallback"})
	content, replaced := maskLogWith(view, "upstream secret (request id: up-9)", "claude_error", "", 529, "", "en")
	assert.True(t, replaced)
	assert.Equal(t, "fallback", content)
	content, _ = maskLogWith(view, "upstream secret", "", "do_request_failed", 0, "r-1", "en")
	assert.Equal(t, "fallback (request id: r-1)", content, "an upstream code alone classifies the error as upstream")
	content, replaced = maskLogWith(view, "local", "new_api_error", "invalid_request", 400, "r-1", "en")
	assert.False(t, replaced)
	assert.Equal(t, "local", content)
}
