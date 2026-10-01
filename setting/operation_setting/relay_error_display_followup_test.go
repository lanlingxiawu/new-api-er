package operation_setting

import (
	"bytes"
	"errors"
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Plain-text steps ignore case exactly as (?i) on the quoted text does, and
// replace leftmost-first without overlap as ReplaceAllLiteralString does.
func TestRelayErrorLiteralEdit_MatchesQuotedRegexp(t *testing.T) {
	cases := []struct{ text, find, replace string }{
		{"Group VIP-a has no channel", "vip-A", "x"},
		{"aaaaa", "aa", "b"},
		{"abcabc", "ABC", ""},
		{"price $5 (a.b*c)", "(A.B*C)", "$1"},
		{"KELVIN K and k and K", "k", "_"},
		{"long ſ and s and S", "S", "z"},
		{"Σσς", "σ", "o"},
		{"İstanbul istanbul ISTANBUL", "istanbul", "city"},
		{"上游分组 vip 下无可用渠道", "无可用渠道", "暂不可用"},
		{"bad \xff byte abc", "ABC", "d"},
		{"bad \xff\xfe byte", "�", "?"},
		{"kKK", "K", "."},
		{"no match here", "missing", "x"},
		{"", "a", "b"},
	}
	for _, tc := range cases {
		want := regexp.MustCompile("(?i)"+regexp.QuoteMeta(tc.find)).ReplaceAllLiteralString(tc.text, tc.replace)
		got := relayErrorLiteralEdit(tc.text, relayErrorFoldText(tc.find), tc.replace)
		assert.Equal(t, want, got, "text %q find %q", tc.text, tc.find)
	}

	// Random texts over an alphabet of case pairs, multi-byte folds and
	// invalid bytes, compared with regexp.
	alphabet := []string{"a", "A", "b", "B", "k", "K", "K", "s", "S", "ſ", "σ", "ς", "Σ", "中", " ", "\xff", "$1"}
	random := rand.New(rand.NewSource(1))
	pick := func(n int, valid bool) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			next := alphabet[random.Intn(len(alphabet))]
			if valid && !utf8.ValidString(next) {
				next = "a"
			}
			b.WriteString(next)
		}
		return b.String()
	}
	for i := 0; i < 3000; i++ {
		// A find is JSON text, so always valid UTF-8.
		text, find := pick(random.Intn(30), false), pick(1+random.Intn(3), true)
		want := regexp.MustCompile("(?i)"+regexp.QuoteMeta(find)).ReplaceAllLiteralString(text, "<$0>")
		got := relayErrorLiteralEdit(text, relayErrorFoldText(find), "<$0>")
		require.Equal(t, want, got, "text %q find %q", text, find)
	}
}

// Regression (review L4a): plain-text steps went through regexp and were not
// counted in the complexity budget; 20 steps of "a"×199+"b" took about 108ms on
// a 4000-character error of a's. They are now a linear substring search.
func TestRelayErrorEdit_PlainTextStepsAreLinear(t *testing.T) {
	edits := make([]RelayErrorEdit, MaxRelayErrorRuleEdits)
	for i := range edits {
		edits[i] = RelayErrorEdit{Find: strings.Repeat("a", MaxRelayErrorEditLen-1) + "b", Replace: "x"}
	}
	setting := enabledSetting(t, true, editRule(edits...))
	view, err := compileRelayErrorDisplay(setting)
	require.NoError(t, err)
	message := strings.Repeat("a", MaxRelayErrorTextLen)
	started := time.Now()
	d := view.decide(RelayErrorInput{Upstream: true, StatusCode: 500, Message: message})
	elapsed := time.Since(started)
	assert.True(t, d.Replace)
	assert.Equal(t, message, d.Message, "nothing matched: the text is unchanged")
	assert.Less(t, elapsed, 20*time.Millisecond)
}

func TestCapRelayErrorText(t *testing.T) {
	assert.Equal(t, "", CapRelayErrorText(""))
	exact := strings.Repeat("a", MaxRelayErrorTextLen)
	assert.Equal(t, exact, CapRelayErrorText(exact), "at the cap nothing is cut")
	assert.Equal(t, "", CapRelayErrorText(exact+"b"), "one token longer than the cap: nothing of it is kept")
	below := strings.Repeat("中", MaxRelayErrorTextLen-1)
	assert.Equal(t, below, CapRelayErrorText(below), "counted in characters, not bytes")
	wide := strings.Repeat("中文 ", MaxRelayErrorTextLen)
	capped := CapRelayErrorText(wide)
	assert.True(t, utf8.ValidString(capped))
	assert.LessOrEqual(t, utf8.RuneCountInString(capped), MaxRelayErrorTextLen)
	assert.True(t, strings.HasSuffix(capped, "中文"), "cut back to the last whole word")
	// The token the cut splits is dropped, up to the last whitespace or delimiter.
	head := strings.Repeat("x ", (MaxRelayErrorTextLen-10)/2)
	assert.Equal(t, head+"key:", CapRelayErrorText(head+"key: sk-abcdefghijklmnopqrstuvwxyz"))
	assert.Equal(t, head+`"key":`, CapRelayErrorText(head+`"key":"sk-abcdefghijklmnopqrstuvwxyz"`))
	assert.Equal(t, head+"at", CapRelayErrorText(head+"at 10.20.30.40:443"))
	// A cut that falls right before a delimiter splits nothing: the last token stays.
	exactWords := strings.Repeat("abcd ", MaxRelayErrorTextLen/5-1) + "last!" + " more words"
	assert.True(t, strings.HasSuffix(CapRelayErrorText(exactWords), "abcd last!"))
	assert.Equal(t, "", CutRelayErrorText("onetoken", 3))
	assert.Equal(t, "one", CutRelayErrorText("one two", 3))
	assert.Equal(t, "one", CutRelayErrorText("one two", 5), "t is split: dropped")
	assert.Equal(t, "one two", CutRelayErrorText("one two", 7))
	assert.Equal(t, `a "b"`, CutRelayErrorText(`a "b",c`, 5))
}

// Regression (review L4b): the live input is err.Error(), which can hold a
// whole upstream body. Edit rules see only the first MaxRelayErrorTextLen
// characters, so the per-rule bound holds whatever the error's size, and an
// edit rule's result is built from that capped text. Keywords look further
// (MaxRelayErrorMatchLen): review item 3 — a replace rule whose keyword sat past
// the edit cap was skipped and a later keep rule sent the full original.
func TestRelayErrorDisplay_TextIsCappedForMatchingAndEditing(t *testing.T) {
	head := strings.Repeat("x ", (MaxRelayErrorTextLen-6)/2)
	s := enabledSetting(t, false,
		RelayErrorRule{Source: RelayErrorSourceAny, Keywords: []string{"late secret"}, Action: RelayErrorActionReplace, Message: "late"},
		RelayErrorRule{Source: RelayErrorSourceAny, Keywords: []string{"early"}, Action: RelayErrorActionEdit, Edits: []RelayErrorEdit{{Find: "early", Replace: "E"}}},
		RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep},
	)
	d := decide(t, s, RelayErrorInput{Message: head + "early" + " late secret"})
	require.True(t, d.Replace)
	assert.Equal(t, 0, d.RuleIndex, "a keyword past the edit cap still matches")
	assert.Equal(t, "late", d.Message)

	near := strings.Repeat("x ", (MaxRelayErrorTextLen-8)/2)
	d = decide(t, s, RelayErrorInput{Message: near + "early sk-secretkey"})
	assert.Equal(t, 1, d.RuleIndex)
	assert.Equal(t, near+"E", d.Message, "the edited result is the capped text, without the token the cut split")

	d = decide(t, s, RelayErrorInput{Message: "early late secret"})
	assert.Equal(t, 0, d.RuleIndex, "within the cap the first rule matches")

	// The worst rule the budget admits, plus plain-text steps, on a 1 MB error.
	var edits []RelayErrorEdit
	size := 0
	for len(edits) < MaxRelayErrorRuleEdits {
		step := RelayErrorEdit{Find: `[^Q]{1,99}Q`, Replace: "x", Regex: true}
		next := relayErrorRegexSize("(?i)" + step.Find)
		if size+next > MaxRelayErrorRuleRegexSize {
			break
		}
		edits, size = append(edits, step), size+next
	}
	for len(edits) < MaxRelayErrorRuleEdits {
		edits = append(edits, RelayErrorEdit{Find: strings.Repeat("a", MaxRelayErrorEditLen-1) + "b"})
	}
	view, err := compileRelayErrorDisplay(enabledSetting(t, true, editRule(edits...)))
	require.NoError(t, err)
	started := time.Now()
	d = view.decide(RelayErrorInput{Upstream: true, StatusCode: 500, Message: strings.Repeat("aaa ", 1<<18)})
	elapsed := time.Since(started)
	assert.True(t, d.Replace)
	assert.LessOrEqual(t, utf8.RuneCountInString(d.Message), MaxRelayErrorTextLen)
	// About 20-50ms (5 steps x 4000 characters); without the cap, 1 MB takes seconds.
	assert.Less(t, elapsed, 250*time.Millisecond, "%d instructions", size)
}

// Review item 3: past MaxRelayErrorMatchLen no keyword is seen, so what a keep
// rule or no match would send unchanged is sent cut where the rules stopped
// looking: the client never gets text that no rule checked.
func TestRelayErrorDisplay_TextPastTheMatchBoundIsNotSent(t *testing.T) {
	head := strings.Repeat("word ", MaxRelayErrorMatchLen/5)
	tail := " late secret sk-abc"
	s := enabledSetting(t, false,
		RelayErrorRule{Source: RelayErrorSourceAny, Keywords: []string{"late secret"}, Action: RelayErrorActionReplace, Message: "late"},
		RelayErrorRule{Source: RelayErrorSourceAny, ErrorCodes: []string{"kept"}, Action: RelayErrorActionKeep},
	)
	for _, tc := range []struct {
		name string
		in   RelayErrorInput
		rule int
	}{
		{"keep", RelayErrorInput{ErrorCode: "kept", Message: head + tail}, 1},
		{"no match", RelayErrorInput{Upstream: true, Message: head + tail}, -1},
	} {
		d := decide(t, s, tc.in)
		require.True(t, d.Replace, tc.name)
		assert.True(t, d.Truncated, tc.name)
		assert.Equal(t, tc.rule, d.RuleIndex, tc.name)
		assert.Equal(t, 0, d.StatusCode, tc.name)
		assert.NotContains(t, d.Message, "secret", tc.name)
		assert.LessOrEqual(t, len(d.Message), MaxRelayErrorMatchLen, tc.name)
		assert.True(t, strings.HasSuffix(d.Message, "word"), "%s: cut at a word", tc.name)
	}

	// Within the bound nothing changes: keep sends the original, no match too.
	d := decide(t, s, RelayErrorInput{ErrorCode: "kept", Message: "short"})
	assert.False(t, d.Replace)
	assert.Equal(t, 1, d.RuleIndex)
	d = decide(t, s, RelayErrorInput{Upstream: true, Message: "short"})
	assert.False(t, d.Replace)

	// An input that says it is only the start of the error (the stream terminal
	// input) is sent as it is instead of the full error.
	d = decide(t, s, RelayErrorInput{ErrorCode: "kept", Message: "start of it", Truncated: true})
	assert.True(t, d.Replace)
	assert.True(t, d.Truncated)
	assert.Equal(t, "start of it", d.Message)
	// A cut that leaves nothing gets the fallback text.
	d = decide(t, s, RelayErrorInput{Upstream: true, Message: strings.Repeat("a", MaxRelayErrorMatchLen+1)})
	assert.True(t, d.Replace)
	assert.Equal(t, "兜底文案", d.Message)
}

// Review item 4: a cut used to leave half a key (which an admin's regex no
// longer matched) or half of "(request id: …" (which was no longer stripped).
// Request ids are stripped before the cap and the split token is dropped.
func TestRelayErrorEdit_CutNeverKeepsAPartialToken(t *testing.T) {
	keyRule := editRule(RelayErrorEdit{Find: `sk-[a-z0-9]{20,}`, Replace: "[key]", Regex: true})
	head := strings.Repeat("x ", (MaxRelayErrorTextLen-10)/2)
	d := decide(t, enabledSetting(t, true, keyRule), RelayErrorInput{Upstream: true, Message: head + "key sk-abcdefghijklmnopqrstuvwxyz0123"})
	require.True(t, d.Replace)
	assert.NotContains(t, d.Message, "sk-", "half a key is not sent")

	idRule := editRule(RelayErrorEdit{Find: "x", Replace: "y"})
	d = decide(t, enabledSetting(t, true, idRule), RelayErrorInput{Upstream: true, Message: head + "failed (request id: upstream-req-0123456789)"})
	require.True(t, d.Replace)
	assert.NotContains(t, d.Message, "request id")
	assert.NotContains(t, d.Message, "upstream-req")
	assert.True(t, strings.HasSuffix(d.Message, "failed"), d.Message[len(d.Message)-20:])
}

// Keywords look at up to MaxRelayErrorMatchLen bytes; the worst the limits
// allow (100 rules of 20 keywords, none matching) stays bounded.
func TestRelayErrorDisplay_KeywordScanIsBounded(t *testing.T) {
	rules := make([]RelayErrorRule, MaxRelayErrorRules)
	for i := range rules {
		keywords := make([]string, MaxRelayErrorRuleKeywords)
		for j := range keywords {
			keywords[j] = strings.Repeat("a", MaxRelayErrorKeywordLen-1) + "b"
		}
		rules[i] = RelayErrorRule{Source: RelayErrorSourceAny, Keywords: keywords, Action: RelayErrorActionReplace, Message: "m"}
	}
	view, err := compileRelayErrorDisplay(enabledSetting(t, false, rules...))
	require.NoError(t, err)
	started := time.Now()
	d := view.decide(RelayErrorInput{Message: strings.Repeat("a", 1<<20)})
	elapsed := time.Since(started)
	assert.True(t, d.Truncated)
	assert.Less(t, elapsed, 250*time.Millisecond)
	t.Logf("2000 keywords on %d bytes: %v", MaxRelayErrorMatchLen, elapsed)
}

// Review item 5: an invalid stored rule is left out on its own; the others,
// hide_upstream_errors and the default message stay in effect, and decisions
// keep naming rules by their stored position.
func TestRelayErrorDisplay_InvalidStoredPartsAreSkippedAlone(t *testing.T) {
	tooComplex := RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionEdit,
		Edits: []RelayErrorEdit{{Find: strings.Repeat(`.{1,999}`, 24) + "Q", Regex: true}}}
	s := enabledSetting(t, true,
		RelayErrorRule{Source: RelayErrorSourceLocal, ErrorCodes: []string{"a"}, Action: RelayErrorActionReplace, Message: "one"},
		tooComplex,
		RelayErrorRule{Source: RelayErrorSourceLocal, ErrorCodes: []string{"c"}, Action: RelayErrorActionReplace, Message: "three"},
	)
	s.DefaultMessage = "fallback"
	require.Error(t, ValidateRelayErrorDisplaySetting(s), "saving still rejects it")

	view := compileRelayErrorDisplayParts(s)
	require.Len(t, view.skipped, 1)
	assert.Equal(t, 2, view.skipped[0].Rule)
	assert.Equal(t, RelayErrorSkippedRule, view.skipped[0].Part)
	var stepErr *RelayErrorEditInvalid
	require.True(t, errors.As(view.skipped[0].Err, &stepErr))
	assert.True(t, stepErr.TooComplex)

	d := view.decide(RelayErrorInput{ErrorCode: "c", Message: "x"})
	assert.Equal(t, "three", d.Message)
	assert.Equal(t, 2, d.RuleIndex, "the stored position, not the position among the rules in effect")
	d = view.decide(RelayErrorInput{Upstream: true, Message: "upstream secret"})
	assert.Equal(t, "fallback", d.Message)

	// A default message over the limit falls back to the built-in text.
	long := RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: strings.Repeat("m", MaxRelayErrorMessageLen+1)}
	view = compileRelayErrorDisplayParts(long)
	require.Len(t, view.skipped, 1)
	assert.Equal(t, 0, view.skipped[0].Rule)
	assert.Equal(t, RelayErrorSkippedDefaultMessage, view.skipped[0].Part)
	d = view.decide(RelayErrorInput{Upstream: true, Message: "upstream secret"})
	assert.True(t, d.Replace)
	assert.Empty(t, view.defaultMessage)
	assert.Equal(t, localizedRelayErrorDefault(i18n.Translate, ""), d.Message, "the built-in text")

	// Rules past the limit are left out; the first MaxRelayErrorRules stay.
	many := make([]RelayErrorRule, MaxRelayErrorRules+1)
	for i := range many {
		many[i] = RelayErrorRule{Source: RelayErrorSourceLocal, Action: RelayErrorActionKeep}
	}
	view = compileRelayErrorDisplayParts(enabledSetting(t, true, many...))
	assert.Len(t, view.rules, MaxRelayErrorRules)
	require.Len(t, view.skipped, 1)
	assert.Equal(t, 0, view.skipped[0].Rule)
	assert.Equal(t, RelayErrorSkippedRuleLimit, view.skipped[0].Part)

	// A valid configuration reports nothing.
	assert.Empty(t, compileRelayErrorDisplayParts(enabledSetting(t, true)).skipped)
}

// Skipped parts are logged when they change, not on every publish (the
// periodic options sync republishes the same configuration).
func TestRelayErrorDisplay_SkippedPartsAreLoggedOnce(t *testing.T) {
	previous := GetRelayErrorDisplaySetting()
	var captured bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &captured
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previousWriter
		common.LogWriterMu.Unlock()
		ReplaceRelayErrorDisplaySetting(previous)
	})
	count := func() int { return strings.Count(captured.String(), "relay error display: stored rule") }

	broken := RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: `[{"source":"nowhere","action":"keep"}]`}
	ReplaceRelayErrorDisplaySetting(broken)
	require.Equal(t, 1, count())
	ReplaceRelayErrorDisplaySetting(broken)
	assert.Equal(t, 1, count(), "the same skipped part is not logged again")

	ReplaceRelayErrorDisplaySetting(RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true})
	ReplaceRelayErrorDisplaySetting(broken)
	assert.Equal(t, 2, count(), "logged again once it comes back after being fixed")
}

// Worst cases of one edit rule on the error path. Run:
// go test ./setting/operation_setting/ -run '^$' -bench RelayErrorDecide_Worst -benchmem
func BenchmarkRelayErrorDecide_WorstLiteral20(b *testing.B) {
	edits := make([]RelayErrorEdit, MaxRelayErrorRuleEdits)
	for i := range edits {
		edits[i] = RelayErrorEdit{Find: strings.Repeat("a", MaxRelayErrorEditLen-1) + "b", Replace: "x"}
	}
	view := benchView(b, RelayErrorRule{Source: RelayErrorSourceUpstream, Action: RelayErrorActionEdit, Edits: edits})
	in := RelayErrorInput{Upstream: true, Message: strings.Repeat("a", MaxRelayErrorTextLen)}
	for b.Loop() {
		_ = view.decide(in)
	}
}

func BenchmarkRelayErrorDecide_WorstRegexOnHugeBody(b *testing.B) {
	var edits []RelayErrorEdit
	size := 0
	for len(edits) < MaxRelayErrorRuleEdits {
		step := RelayErrorEdit{Find: `[^Q]{1,99}Q`, Replace: "x", Regex: true}
		next := relayErrorRegexSize("(?i)" + step.Find)
		if size+next > MaxRelayErrorRuleRegexSize {
			break
		}
		edits, size = append(edits, step), size+next
	}
	view := benchView(b, RelayErrorRule{Source: RelayErrorSourceUpstream, Action: RelayErrorActionEdit, Edits: edits})
	in := RelayErrorInput{Upstream: true, Message: strings.Repeat("a", 1<<20)}
	for b.Loop() {
		_ = view.decide(in)
	}
}
