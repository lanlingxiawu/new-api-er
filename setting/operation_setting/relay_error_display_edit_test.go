package operation_setting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 局部修改（action=edit）：在原文上按顺序查找替换，只改掉需要隐藏的部分。
// 设计见 docs/design/relay-error-message-masking.md §3.5。

func editRule(edits ...RelayErrorEdit) RelayErrorRule {
	return RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionEdit, Edits: edits}
}

func edited(t *testing.T, message string, edits ...RelayErrorEdit) RelayErrorDecision {
	t.Helper()
	return decide(t, enabledSetting(t, true, editRule(edits...)), RelayErrorInput{Upstream: true, StatusCode: 503, Message: message})
}

func TestRelayErrorEdit_DesignExample(t *testing.T) {
	d := edited(t, "当前分组 vip-A 下对于模型 gpt-5 无可用渠道 (request id: 2026092812abcd)",
		RelayErrorEdit{Find: `当前分组 \S+ 下`, Regex: true},
		RelayErrorEdit{Find: `\(request id: [^)]*\)`, Regex: true},
		RelayErrorEdit{Find: "无可用渠道", Replace: "暂时不可用，请稍后再试"},
	)
	require.True(t, d.Replace)
	assert.Equal(t, "对于模型 gpt-5 暂时不可用，请稍后再试", d.Message)
	assert.Equal(t, 0, d.RuleIndex)
}

func TestRelayErrorEdit_LiteralIsCaseInsensitiveAndNotARegex(t *testing.T) {
	d := edited(t, "Upstream NEWAPI.example (a.b) failed; newapi.EXAMPLE again",
		RelayErrorEdit{Find: "newapi.example", Replace: "service"},
		RelayErrorEdit{Find: "(a.b)"},
	)
	assert.Equal(t, "Upstream service failed; service again", d.Message, "every occurrence, any case; '.' and '(' are literal")

	literalDot := edited(t, "axb a.b", RelayErrorEdit{Find: "a.b", Replace: "X"})
	assert.Equal(t, "axb X", literalDot.Message)
}

func TestRelayErrorEdit_RegexGroupsAndCase(t *testing.T) {
	d := edited(t, "Model GPT-5 in GROUP vip is overloaded",
		RelayErrorEdit{Find: `in group (\S+) `, Replace: "", Regex: true},
		RelayErrorEdit{Find: `model (\S+)`, Replace: "model “$1”", Regex: true},
	)
	// The match ignores case; the replacement text is written as configured.
	assert.Equal(t, "model “GPT-5” is overloaded", d.Message)
}

// Each edit works on the previous one's result.
func TestRelayErrorEdit_RunsInOrder(t *testing.T) {
	d := edited(t, "alpha", RelayErrorEdit{Find: "alpha", Replace: "beta"}, RelayErrorEdit{Find: "beta", Replace: "gamma"})
	assert.Equal(t, "gamma", d.Message)
}

func TestRelayErrorEdit_CollapsesLeftoverSpaces(t *testing.T) {
	d := edited(t, "  keep   this \t part  ", RelayErrorEdit{Find: "this"})
	assert.Equal(t, "keep part", d.Message)
}

// Nothing left (a request id alone does not count): the fallback text, never an empty error.
func TestRelayErrorEdit_EmptyResultFallsBack(t *testing.T) {
	d := edited(t, "secret group vip (request id: up-1)", RelayErrorEdit{Find: "secret group vip"})
	require.True(t, d.Replace)
	assert.Equal(t, "兜底文案", d.Message)
}

func TestRelayErrorEdit_StatusCodeAndFirstMatch(t *testing.T) {
	s := enabledSetting(t, true,
		RelayErrorRule{Source: RelayErrorSourceUpstream, Keywords: []string{"quota"}, Action: RelayErrorActionEdit, StatusCode: 503, Edits: []RelayErrorEdit{{Find: "quota", Replace: "capacity"}}},
		RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionReplace, Message: "later rule"},
	)
	d := decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 403, Message: "upstream quota exceeded"})
	assert.Equal(t, "upstream capacity exceeded", d.Message)
	assert.Equal(t, 503, d.StatusCode)
	assert.Equal(t, 0, d.RuleIndex)

	other := decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 500, Message: "boom"})
	assert.Equal(t, "later rule", other.Message, "an edit rule that does not match lets later rules decide")
}

// A condition-less edit rule placed last catches what earlier rules did not,
// instead of the whole-message fallback.
func TestRelayErrorEdit_CatchAllReplacesFallback(t *testing.T) {
	s := enabledSetting(t, true, editRule(RelayErrorEdit{Find: `\(request id: [^)]*\)`, Regex: true}))
	d := decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 400, Message: "context length exceeded (request id: up-9)"})
	assert.Equal(t, "context length exceeded", d.Message)
	assert.Equal(t, 0, d.RuleIndex)
}

func TestRelayErrorEdit_UserLogAndStream(t *testing.T) {
	view := compileRelayErrorDisplayParts(enabledSetting(t, true, editRule(RelayErrorEdit{Find: "group vip", Replace: "group"})))
	content, replaced := maskLogWith(view, "no channel for group vip (request id: r1)", "openai_error", "", 503, "r1", "")
	assert.True(t, replaced)
	assert.Equal(t, "no channel for group (request id: r1)", content, "our request id is kept once")

	relayErrorDisplaySnapshot.Publish(*view)
	t.Cleanup(func() { relayErrorDisplaySnapshot.Publish(relayErrorDisplayView{}) })
	message, replaced := MaskRelayStreamErrorForUser("stream cut in group vip", "")
	assert.True(t, replaced)
	assert.Equal(t, "stream cut in group", message)
}

func TestRelayErrorEdit_Validation(t *testing.T) {
	long := strings.Repeat("字", MaxRelayErrorEditLen+1)
	tooMany := make([]RelayErrorEdit, MaxRelayErrorRuleEdits+1)
	for i := range tooMany {
		tooMany[i] = RelayErrorEdit{Find: "x"}
	}
	cases := map[string]RelayErrorRule{
		"no edits":                 editRule(),
		"empty find":               editRule(RelayErrorEdit{Find: "  "}),
		"find too long":            editRule(RelayErrorEdit{Find: long}),
		"replace too long":         editRule(RelayErrorEdit{Find: "x", Replace: long}),
		"too many edits":           editRule(tooMany...),
		"regex does not compile":   editRule(RelayErrorEdit{Find: "(unclosed", Regex: true}),
		"lookahead is not RE2":     editRule(RelayErrorEdit{Find: "a(?=b)", Regex: true}),
		"regex matches empty text": editRule(RelayErrorEdit{Find: "a*", Regex: true}),
		"edits on a replace rule":  {Source: RelayErrorSourceAny, Action: RelayErrorActionReplace, Message: "m", Edits: []RelayErrorEdit{{Find: "x"}}},
		"status code out of range": {Source: RelayErrorSourceAny, Action: RelayErrorActionEdit, StatusCode: 200, Edits: []RelayErrorEdit{{Find: "x"}}},
	}
	for name, rule := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateRelayErrorDisplaySetting(enabledSetting(t, true, rule))
			assert.ErrorIs(t, err, ErrRelayErrorDisplayInvalid)
		})
	}

	exact := make([]RelayErrorEdit, MaxRelayErrorRuleEdits)
	for i := range exact {
		exact[i] = RelayErrorEdit{Find: strings.Repeat("字", MaxRelayErrorEditLen), Replace: strings.Repeat("字", MaxRelayErrorEditLen)}
	}
	assert.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true, editRule(exact...))), "the limits themselves are allowed")
	assert.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true, editRule(RelayErrorEdit{Find: " keep spaces ", Replace: ""}))))
}
