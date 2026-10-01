package operation_setting

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/config"
)

// 中转错误提示的可配置替换：让客户端看到管理员配置的文案，而不是上游的真实情况
// （上游分组名、令牌余额、内部报错等）。只作用于"交给客户端"的最后一步与用户侧的
// 错误日志展示；重试判断、自动禁用、管理员日志仍基于原文。设计见
// docs/design/relay-error-message-masking.md。

const (
	relayErrorDisplayConfigName = "relay_error_display_setting"

	MaxRelayErrorRules        = 100
	MaxRelayErrorRuleKeywords = 20
	MaxRelayErrorRuleCodes    = 20
	MaxRelayErrorKeywordLen   = 100
	MaxRelayErrorMessageLen   = 500
	MaxRelayErrorRuleNameLen  = 100
	MaxRelayErrorRuleEdits    = 20
	MaxRelayErrorEditLen      = 200
	// MaxRelayErrorRuleRegexSize bounds the compiled size (RE2 program
	// instructions) of all regular-expression steps of one edit rule together.
	// RE2 is linear in the text, but the constant is the number of program
	// states that can be active at once, and counted repetition multiplies
	// them: `.{1,999}` alone is 2000 instructions, and 24 of them in a
	// 200-character pattern took about a second per step on a 4000-character
	// error, synchronously on the relay goroutine. In the worst case (every
	// state active) a step costs about 5-10ns per instruction per character, so
	// this budget keeps a whole rule within about 20-50ms on a
	// MaxRelayErrorTextLen error (BenchmarkRelayErrorDecide_WorstRegexOnHugeBody).
	// Ordinary patterns are far below it (`\(request id: [^)]*\)` is 18,
	// `[a-z0-9-]{3,40}` about 90). Plain-text steps are not regular
	// expressions: a case-folded substring search whose cost is linear in the
	// text whatever the find is (relayErrorLiteralEdit), so they are not counted.
	MaxRelayErrorRuleRegexSize = 1000
	// MaxRelayErrorTextLen caps, in characters, the error text that edit rules
	// run on. The live error can hold a whole upstream body (err.Error() shows
	// it on failure), and the per-rule time bound above is per character, so an
	// edit rule's result is built from the capped text only. The admin preview
	// accepts samples of at most this length.
	MaxRelayErrorTextLen = 4000
	// MaxRelayErrorMatchLen caps, in bytes, the error text that keywords are
	// matched against. Keywords are plain substrings (linear in the text), so
	// they look much further than edits do. When the error is longer still, or
	// the input says it is only the start of the error (RelayErrorInput.Truncated),
	// what would go out unchanged (keep, no match) goes out cut where the rules
	// stopped looking instead, so the client never gets text no rule has seen.
	MaxRelayErrorMatchLen = 16 << 10

	RelayErrorSourceUpstream = "upstream"
	RelayErrorSourceLocal    = "local"
	RelayErrorSourceAny      = "any"

	RelayErrorActionReplace = "replace"
	RelayErrorActionKeep    = "keep"
	// RelayErrorActionEdit rewrites parts of the original message (find and
	// replace, in order) instead of replacing the whole text.
	RelayErrorActionEdit = "edit"

	// RelayStreamErrorCode is the error code an upstream error that ended a
	// stream is matched with when the frame carries no code of its own.
	RelayStreamErrorCode = "upstream_stream_error"

	// RelayTimeoutErrorCode is our own time limit's error code
	// (types.ErrorCodeRelayTimeout; this package cannot import relaykit/types).
	RelayTimeoutErrorCode = "relay_timeout"

	// RelayStreamMatchCodeKey and RelayStreamMatchMessageKey are the
	// admin_info keys of a stream-failure error log that keep the error code and
	// text its terminal frame was decided with, so the user's view of the log is
	// decided with the same input. admin_info never reaches users.
	RelayStreamMatchCodeKey    = "stream_error_code"
	RelayStreamMatchMessageKey = "stream_error_message"
	// RelayStreamMatchTruncatedKey is set (true) when the recorded text is only
	// the start of the frame's error text (RelayErrorInput.Truncated).
	RelayStreamMatchTruncatedKey = "stream_error_truncated"

	// builtInRelayErrorMessage is the fallback text for when translations are
	// not loaded; normally the localized i18n.MsgRelayErrorDefaultMessage is used.
	builtInRelayErrorMessage = "Service temporarily unavailable, please try again later"
)

// ErrRelayErrorDisplayInvalid marks a configuration that cannot be saved; the
// wrapped text says which field, for the server log.
var ErrRelayErrorDisplayInvalid = errors.New("invalid relay error display setting")

// RelayErrorDisplaySetting is the stored configuration. Rules is a JSON array of
// RelayErrorRule kept as one option value, like GroupRetryTimes.
type RelayErrorDisplaySetting struct {
	Enabled            bool   `json:"enabled"`
	HideUpstreamErrors bool   `json:"hide_upstream_errors"`
	DefaultMessage     string `json:"default_message"`
	Rules              string `json:"rules"`
}

// RelayErrorRule matches when every condition that is set holds (and), each
// condition matching any of its values (or). Keywords are case-insensitive
// substrings, not regular expressions.
type RelayErrorRule struct {
	Name        string           `json:"name,omitempty"`
	Source      string           `json:"source"`
	StatusCodes []int            `json:"status_codes,omitempty"`
	ErrorCodes  []string         `json:"error_codes,omitempty"`
	Keywords    []string         `json:"keywords,omitempty"`
	Action      string           `json:"action"`
	Message     string           `json:"message,omitempty"`
	Edits       []RelayErrorEdit `json:"edits,omitempty"`
	StatusCode  int              `json:"status_code,omitempty"`
}

// RelayErrorEdit is one find-and-replace step of an edit rule. Find is plain
// text unless Regex is set; either way it ignores case. An empty Replace
// deletes the match. Regular expressions are Go's RE2, whose running time is
// linear in the text; MaxRelayErrorRuleRegexSize bounds the per-character cost,
// so a pattern cannot stall the error path.
type RelayErrorEdit struct {
	Find    string `json:"find"`
	Replace string `json:"replace,omitempty"`
	Regex   bool   `json:"regex,omitempty"`
}

// RelayErrorInput describes one client-facing error.
type RelayErrorInput struct {
	Upstream   bool
	StatusCode int
	ErrorCode  string
	Message    string
	// Lang is the user's language, for the built-in fallback text. Empty means
	// the i18n default (English).
	Lang string
	// Truncated reports that Message is only the start of the error text (the
	// stream terminal input keeps at most MaxRelayErrorTextLen characters): if
	// no rule replaces it, Message goes out instead of the full error.
	Truncated bool
}

// RelayErrorDecision is what the client should see. Replace=false means the
// original error goes out unchanged. StatusCode 0 keeps the original status.
// RuleIndex is the matched rule, or -1 for the upstream fallback / no match.
// Truncated marks a Replace that no rule asked for (a keep rule or no match):
// the error was longer than the rules looked at, and Message is the part they
// saw.
type RelayErrorDecision struct {
	Replace    bool
	Message    string
	StatusCode int
	RuleIndex  int
	RuleName   string
	Truncated  bool
}

type compiledRelayErrorRule struct {
	// index is the rule's position in the stored list, which decisions report;
	// it differs from the position in view.rules when an earlier stored rule
	// was skipped as invalid.
	index       int
	name        string
	source      string
	statusCodes map[int]struct{}
	errorCodes  map[string]struct{}
	keywords    []string
	action      string
	message     string
	edits       []compiledRelayErrorEdit
	statusCode  int
}

// compiledRelayErrorEdit is one step: a regular expression (pattern), or plain
// text (pattern nil) searched as foldedFind, the find text folded by
// relayErrorFoldText. Plain-text replacements never expand $1.
type compiledRelayErrorEdit struct {
	pattern    *regexp.Regexp
	foldedFind string
	replace    string
}

// relayErrorDisplayView is the published, pre-parsed form read on the error path.
type relayErrorDisplayView struct {
	enabled        bool
	hideUpstream   bool
	defaultMessage string
	rules          []compiledRelayErrorRule
	// skipped lists the stored parts left out because they no longer validate.
	skipped []RelayErrorSkipped
}

// RelayErrorSkipped is a part of the stored configuration that no longer
// validates (a rule saved before a limit existed, a hand-edited row) and is
// left out of the live configuration while the rest stays in effect. Part says
// which part (RelayErrorSkipped*); Rule is the 1-based stored rule number for
// RelayErrorSkippedRule, else 0. Err is what ValidateRelayErrorDisplaySetting
// reports for it.
type RelayErrorSkipped struct {
	Part string
	Rule int
	Err  error
}

// The parts of a stored configuration that can be skipped.
const (
	// RelayErrorSkippedRule: one invalid rule is left out.
	RelayErrorSkippedRule = "rule"
	// RelayErrorSkippedDefaultMessage: the default message is too long; the
	// built-in text is used instead.
	RelayErrorSkippedDefaultMessage = "default_message"
	// RelayErrorSkippedRules: the rules list cannot be read; no rule applies.
	RelayErrorSkippedRules = "rules"
	// RelayErrorSkippedRuleLimit: rules past MaxRelayErrorRules are left out.
	RelayErrorSkippedRuleLimit = "rule_limit"
)

func relayErrorDisplayDefaults() RelayErrorDisplaySetting {
	return RelayErrorDisplaySetting{
		Enabled:            false,
		HideUpstreamErrors: true,
		DefaultMessage:     "",
		Rules:              "",
	}
}

var relayErrorDisplaySetting = relayErrorDisplayDefaults()

var relayErrorDisplaySnapshot config.Snapshot[relayErrorDisplayView]

func init() {
	config.GlobalConfig.RegisterSnapshot(relayErrorDisplayConfigName, &relayErrorDisplaySetting, publishRelayErrorDisplaySetting)
}

// relayErrorDisplayLoggedSkips is what the last log of skipped parts said;
// guarded by the config draft lock that publishRelayErrorDisplaySetting runs
// under.
var relayErrorDisplayLoggedSkips string

// publishRelayErrorDisplaySetting runs while the config draft lock is held. It
// also runs on every periodic options sync, so skipped parts are logged only
// when they change, not on every publish.
//
// The stored configuration is compiled with compileRelayErrorDisplayParts,
// which never fails and never turns the feature off: a stored part that no
// longer passes validation (hand-edited DB row, older build, a rule saved
// before a limit such as MaxRelayErrorRuleRegexSize existed) is left out, so
// the error path never runs it. The other rules, hide_upstream_errors and the
// default message stay in effect, so one stale rule cannot expose raw upstream
// errors to every user. RelayErrorDisplaySkipped reports what was left out,
// for the settings page.
func publishRelayErrorDisplaySetting() {
	view := compileRelayErrorDisplayParts(relayErrorDisplaySetting)
	var summary strings.Builder
	for _, skipped := range view.skipped {
		fmt.Fprintf(&summary, "stored %s %d skipped: %v; ", skipped.Part, skipped.Rule, skipped.Err)
	}
	if text := summary.String(); text != relayErrorDisplayLoggedSkips {
		relayErrorDisplayLoggedSkips = text
		if text != "" {
			common.SysError("relay error display: " + text + "the rest of the setting stays in effect")
		}
	}
	relayErrorDisplaySnapshot.Publish(*view)
}

// RelayErrorDisplaySkipped lists the stored parts the live configuration left
// out because they no longer validate; nil when everything is in effect.
func RelayErrorDisplaySkipped() []RelayErrorSkipped {
	v := relayErrorDisplaySnapshot.Load()
	if v == nil || len(v.skipped) == 0 {
		return nil
	}
	return append([]RelayErrorSkipped(nil), v.skipped...)
}

func GetRelayErrorDisplaySetting() RelayErrorDisplaySetting {
	var out RelayErrorDisplaySetting
	config.WithConfigDraft(func() { out = relayErrorDisplaySetting })
	return out
}

func ReplaceRelayErrorDisplaySetting(setting RelayErrorDisplaySetting) {
	config.WithConfigDraft(func() {
		relayErrorDisplaySetting = setting
		publishRelayErrorDisplaySetting()
	})
}

func ValidateRelayErrorDisplaySetting(setting RelayErrorDisplaySetting) error {
	_, err := compileRelayErrorDisplay(setting)
	return err
}

// RelayErrorEditInvalid names the rule and find-and-replace step that cannot be
// saved, so the admin is told exactly what to fix. It is an ErrRelayErrorDisplayInvalid.
// TooComplex marks the step at which the rule's regular expressions exceed
// MaxRelayErrorRuleRegexSize, which needs its own advice.
type RelayErrorEditInvalid struct {
	Rule       int
	Step       int
	Reason     string
	TooComplex bool
}

func (e *RelayErrorEditInvalid) Error() string {
	return fmt.Sprintf("%v: rule %d step %d: %s", ErrRelayErrorDisplayInvalid, e.Rule, e.Step, e.Reason)
}

func (e *RelayErrorEditInvalid) Unwrap() error { return ErrRelayErrorDisplayInvalid }

// relayErrorStepError is a step-level failure before the rule number is known.
type relayErrorStepError struct {
	step       int
	reason     string
	tooComplex bool
}

func (e *relayErrorStepError) Error() string {
	return fmt.Sprintf("step %d: %s", e.step, e.reason)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRelayErrorDisplayInvalid, fmt.Sprintf(format, args...))
}

// compileRelayErrorDisplay is the strict form used to save and preview: any
// invalid part rejects the whole configuration, reported as the first problem.
func compileRelayErrorDisplay(setting RelayErrorDisplaySetting) (*relayErrorDisplayView, error) {
	view := compileRelayErrorDisplayParts(setting)
	if len(view.skipped) > 0 {
		return nil, view.skipped[0].Err
	}
	return view, nil
}

// compileRelayErrorDisplayParts compiles every part that validates and lists
// the others in view.skipped, in the order a strict check meets them: a too
// long default message falls back to the built-in text, an unreadable rules
// list to no rules, rules past the first MaxRelayErrorRules and invalid rules
// are left out.
func compileRelayErrorDisplayParts(setting RelayErrorDisplaySetting) *relayErrorDisplayView {
	view := &relayErrorDisplayView{
		enabled:        setting.Enabled,
		hideUpstream:   setting.HideUpstreamErrors,
		defaultMessage: strings.TrimSpace(setting.DefaultMessage),
	}
	skip := func(part string, rule int, err error) {
		view.skipped = append(view.skipped, RelayErrorSkipped{Part: part, Rule: rule, Err: err})
	}
	if utf8.RuneCountInString(setting.DefaultMessage) > MaxRelayErrorMessageLen {
		skip(RelayErrorSkippedDefaultMessage, 0, invalid("default message longer than %d characters", MaxRelayErrorMessageLen))
		view.defaultMessage = ""
	}
	raw := strings.TrimSpace(setting.Rules)
	if raw == "" {
		return view
	}
	var rules []RelayErrorRule
	if err := common.UnmarshalJsonStr(raw, &rules); err != nil {
		skip(RelayErrorSkippedRules, 0, invalid("rules are not a JSON array of rules"))
		return view
	}
	if len(rules) > MaxRelayErrorRules {
		skip(RelayErrorSkippedRuleLimit, 0, invalid("more than %d rules", MaxRelayErrorRules))
		rules = rules[:MaxRelayErrorRules]
	}
	for i, rule := range rules {
		compiled, err := compileRelayErrorRule(rule)
		if err != nil {
			var stepErr *relayErrorStepError
			if errors.As(err, &stepErr) {
				skip(RelayErrorSkippedRule, i+1, &RelayErrorEditInvalid{Rule: i + 1, Step: stepErr.step, Reason: stepErr.reason, TooComplex: stepErr.tooComplex})
			} else {
				skip(RelayErrorSkippedRule, i+1, invalid("rule %d: %v", i+1, err))
			}
			continue
		}
		compiled.index = i
		view.rules = append(view.rules, compiled)
	}
	return view
}

func compileRelayErrorRule(rule RelayErrorRule) (compiledRelayErrorRule, error) {
	out := compiledRelayErrorRule{
		name:       strings.TrimSpace(rule.Name),
		source:     strings.TrimSpace(rule.Source),
		action:     strings.TrimSpace(rule.Action),
		message:    strings.TrimSpace(rule.Message),
		statusCode: rule.StatusCode,
	}
	if utf8.RuneCountInString(out.name) > MaxRelayErrorRuleNameLen {
		return out, fmt.Errorf("name longer than %d characters", MaxRelayErrorRuleNameLen)
	}
	switch out.source {
	case RelayErrorSourceUpstream, RelayErrorSourceLocal, RelayErrorSourceAny:
	default:
		return out, fmt.Errorf("source must be upstream, local or any")
	}
	switch out.action {
	case RelayErrorActionKeep:
	case RelayErrorActionReplace:
		if out.message == "" {
			return out, fmt.Errorf("a replace rule needs a message")
		}
	case RelayErrorActionEdit:
		edits, err := compileRelayErrorEdits(rule.Edits)
		if err != nil {
			return out, err
		}
		out.edits = edits
	default:
		return out, fmt.Errorf("action must be replace, keep or edit")
	}
	if out.action != RelayErrorActionEdit && len(rule.Edits) > 0 {
		return out, fmt.Errorf("only an edit rule has find-and-replace steps")
	}
	if utf8.RuneCountInString(out.message) > MaxRelayErrorMessageLen {
		return out, fmt.Errorf("message longer than %d characters", MaxRelayErrorMessageLen)
	}
	if out.statusCode != 0 && (out.statusCode < 400 || out.statusCode > 599) {
		return out, fmt.Errorf("status code override must be between 400 and 599")
	}
	if len(rule.StatusCodes) > 0 {
		out.statusCodes = make(map[int]struct{}, len(rule.StatusCodes))
		for _, code := range rule.StatusCodes {
			if code < 100 || code > 599 {
				return out, fmt.Errorf("matched status codes must be between 100 and 599")
			}
			out.statusCodes[code] = struct{}{}
		}
	}
	if len(rule.ErrorCodes) > MaxRelayErrorRuleCodes {
		return out, fmt.Errorf("more than %d error codes", MaxRelayErrorRuleCodes)
	}
	if len(rule.ErrorCodes) > 0 {
		out.errorCodes = make(map[string]struct{}, len(rule.ErrorCodes))
		for _, code := range rule.ErrorCodes {
			code = strings.ToLower(strings.TrimSpace(code))
			if code == "" || utf8.RuneCountInString(code) > MaxRelayErrorKeywordLen {
				return out, fmt.Errorf("error codes must be 1 to %d characters", MaxRelayErrorKeywordLen)
			}
			out.errorCodes[code] = struct{}{}
		}
	}
	if len(rule.Keywords) > MaxRelayErrorRuleKeywords {
		return out, fmt.Errorf("more than %d keywords", MaxRelayErrorRuleKeywords)
	}
	for _, keyword := range rule.Keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" || utf8.RuneCountInString(keyword) > MaxRelayErrorKeywordLen {
			return out, fmt.Errorf("keywords must be 1 to %d characters", MaxRelayErrorKeywordLen)
		}
		out.keywords = append(out.keywords, keyword)
	}
	return out, nil
}

// compileRelayErrorEdits validates and pre-compiles an edit rule's steps, so
// the error path never compiles a pattern.
func compileRelayErrorEdits(edits []RelayErrorEdit) ([]compiledRelayErrorEdit, error) {
	if len(edits) == 0 {
		return nil, fmt.Errorf("an edit rule needs at least one find-and-replace step")
	}
	if len(edits) > MaxRelayErrorRuleEdits {
		return nil, fmt.Errorf("more than %d find-and-replace steps", MaxRelayErrorRuleEdits)
	}
	out := make([]compiledRelayErrorEdit, 0, len(edits))
	regexSize := 0
	for i, edit := range edits {
		step := func(reason string) error { return &relayErrorStepError{step: i + 1, reason: reason} }
		if strings.TrimSpace(edit.Find) == "" {
			return nil, step("the text to find is empty")
		}
		if utf8.RuneCountInString(edit.Find) > MaxRelayErrorEditLen || utf8.RuneCountInString(edit.Replace) > MaxRelayErrorEditLen {
			return nil, step(fmt.Sprintf("find and replace can be at most %d characters", MaxRelayErrorEditLen))
		}
		if !edit.Regex {
			// Plain text never becomes a regular expression: relayErrorLiteralEdit.
			out = append(out, compiledRelayErrorEdit{foldedFind: relayErrorFoldText(edit.Find), replace: edit.Replace})
			continue
		}
		pattern, err := regexp.Compile("(?i)" + edit.Find)
		if err != nil {
			return nil, step("invalid regular expression")
		}
		regexSize += relayErrorRegexSize("(?i)" + edit.Find)
		if regexSize > MaxRelayErrorRuleRegexSize {
			return nil, &relayErrorStepError{step: i + 1, tooComplex: true,
				reason: fmt.Sprintf("the regular expressions of this rule compile to more than %d instructions together", MaxRelayErrorRuleRegexSize)}
		}
		// A pattern that matches empty text (a*) would insert the replacement
		// between every character.
		if pattern.MatchString("") {
			return nil, step("the regular expression can match empty text")
		}
		if !relayErrorReplacementRefsExist(edit.Replace, pattern) {
			return nil, step("the replacement refers to a group that does not exist; write $$ for a literal $")
		}
		out = append(out, compiledRelayErrorEdit{pattern: pattern, replace: edit.Replace})
	}
	return out, nil
}

// relayErrorFoldRune maps r to one fixed member (the smallest) of its simple
// case-folding orbit, so runes that (?i) treats as equal map to the same rune:
// a/A, k/K/U+212A (Kelvin sign), s/S/U+017F (long s).
func relayErrorFoldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if 'a' <= r && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}
	smallest := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < smallest {
			smallest = f
		}
	}
	return smallest
}

// relayErrorFoldText folds every rune of s with relayErrorFoldRune.
func relayErrorFoldText(s string) string {
	folded, _ := relayErrorFoldTextASCII(s)
	return folded
}

// relayErrorFoldTextASCII is relayErrorFoldText that also reports whether s was
// pure ASCII, in which case byte offsets in the result are byte offsets in s.
// s is read the way regexp reads it (an invalid byte is one U+FFFD) and the
// result is always valid UTF-8, so a match of a folded find in it starts and
// ends on rune boundaries.
func relayErrorFoldTextASCII(s string) (folded string, ascii bool) {
	ascii = true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToUpper(s), true
	}
	buf := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		buf = utf8.AppendRune(buf, relayErrorFoldRune(r))
		i += width
	}
	return string(buf), false
}

// relayErrorLiteralEdit replaces every occurrence of a plain-text find in text,
// ignoring case exactly as (?i) on the quoted text does, leftmost first and
// without overlap as ReplaceAllLiteralString does. It searches the folded text
// with strings.Index, linear in the text whatever the find is. The quoted
// literal run through regexp is not: all 200 states of "aa…ab" stay active on
// a run of a's, about 5ms per step on a MaxRelayErrorTextLen error, 100ms for
// a rule of 20 such steps.
func relayErrorLiteralEdit(text, foldedFind, replace string) string {
	folded, ascii := relayErrorFoldTextASCII(text)
	at := strings.Index(folded, foldedFind)
	if at < 0 {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	// fp and tp are aligned cursors in folded and in text; copied is where the
	// part of text not yet written starts.
	fp, tp, copied := 0, 0, 0
	advance := func(target int) {
		if ascii {
			fp, tp = target, target
			return
		}
		for fp < target {
			_, textWidth := utf8.DecodeRuneInString(text[tp:])
			_, foldedWidth := utf8.DecodeRuneInString(folded[fp:])
			tp += textWidth
			fp += foldedWidth
		}
	}
	for at >= 0 {
		advance(fp + at)
		out.WriteString(text[copied:tp])
		out.WriteString(replace)
		advance(fp + len(foldedFind))
		copied = tp
		at = strings.Index(folded[fp:], foldedFind)
	}
	out.WriteString(text[copied:])
	return out.String()
}

// relayErrorRegexSize is the number of instructions of expr's RE2 program,
// compiled as regexp.Compile does. expr has already compiled, so a failure here
// is unexpected; it counts as over the budget rather than as free.
func relayErrorRegexSize(expr string) int {
	parsed, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		return MaxRelayErrorRuleRegexSize + 1
	}
	program, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return MaxRelayErrorRuleRegexSize + 1
	}
	return len(program.Inst)
}

// relayErrorReplacementRefsExist reports whether every $name / ${name} in a
// regex replacement names a group of pattern. Go expands a reference to a
// missing group into nothing, so "$5 credit" would silently lose "$5". The
// scan mirrors regexp.Regexp.Expand: $$ is a literal $, and a $ not followed
// by a name is kept as is.
func relayErrorReplacementRefsExist(replace string, pattern *regexp.Regexp) bool {
	names := map[string]bool{}
	for _, name := range pattern.SubexpNames() {
		if name != "" {
			names[name] = true
		}
	}
	isNameChar := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	for i := 0; i < len(replace); i++ {
		if replace[i] != '$' || i+1 >= len(replace) {
			continue
		}
		rest := replace[i+1:]
		if rest[0] == '$' {
			i++
			continue
		}
		name := ""
		if rest[0] == '{' {
			end := strings.IndexByte(rest, '}')
			if end < 0 {
				continue
			}
			name = rest[1:end]
			valid := name != ""
			for j := 0; j < len(name) && valid; j++ {
				valid = isNameChar(name[j])
			}
			if !valid {
				continue
			}
			i += end + 1
		} else {
			end := 0
			for end < len(rest) && isNameChar(rest[end]) {
				end++
			}
			if end == 0 {
				continue
			}
			name = rest[:end]
			i += end
		}
		if number, err := strconv.Atoi(name); err == nil {
			if number > pattern.NumSubexp() {
				return false
			}
			continue
		}
		if !names[name] {
			return false
		}
	}
	return true
}

// relayErrorExtraSpaces collapses the gaps a deletion leaves behind.
var relayErrorExtraSpaces = regexp.MustCompile(`[ \t]{2,}`)

// relayErrorEditGrowthAllowance bounds how much the steps may lengthen a
// message. Each step can multiply the length (a one-character find with a
// 200-character replacement, a zero-width match such as \b), so without a
// bound a few steps turn a short error into hundreds of megabytes.
const relayErrorEditGrowthAllowance = 8 << 10

// apply runs the steps in order over message; each works on the previous result.
// A message the steps did not touch comes back unchanged. An empty result (or
// one grown past the bound) tells the caller to use the fallback text.
func (r *compiledRelayErrorRule) apply(original string) string {
	limit := 4*len(original) + relayErrorEditGrowthAllowance
	message := original
	for _, edit := range r.edits {
		if edit.pattern == nil {
			message = relayErrorLiteralEdit(message, edit.foldedFind, edit.replace)
		} else {
			message = edit.pattern.ReplaceAllString(message, edit.replace)
		}
		if len(message) > limit {
			return ""
		}
	}
	if message == original {
		return original
	}
	return strings.TrimSpace(relayErrorExtraSpaces.ReplaceAllString(message, " "))
}

func (r *compiledRelayErrorRule) matches(in RelayErrorInput, lowerMessage string) bool {
	switch r.source {
	case RelayErrorSourceUpstream:
		if !in.Upstream {
			return false
		}
	case RelayErrorSourceLocal:
		if in.Upstream {
			return false
		}
	}
	if r.statusCodes != nil {
		if _, ok := r.statusCodes[in.StatusCode]; !ok {
			return false
		}
	}
	if r.errorCodes != nil {
		if _, ok := r.errorCodes[strings.ToLower(in.ErrorCode)]; !ok {
			return false
		}
	}
	if len(r.keywords) > 0 {
		hit := false
		for _, keyword := range r.keywords {
			if strings.Contains(lowerMessage, keyword) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func (v *relayErrorDisplayView) decide(in RelayErrorInput) RelayErrorDecision {
	none := RelayErrorDecision{RuleIndex: -1}
	if v == nil || !v.enabled {
		return none
	}
	// Keywords see up to MaxRelayErrorMatchLen bytes, edits up to
	// MaxRelayErrorTextLen characters: both bound the time a rule can take.
	scanned, cut := RelayErrorMatchPrefix(in.Message)
	truncated := cut || in.Truncated
	lower := strings.ToLower(scanned)
	if cut {
		// A token cut in half (a key, an address) is kept out of what is
		// edited or sent; it still took part in keyword matching above.
		scanned = CutRelayErrorText(in.Message, len(scanned))
	}
	// Request ids are taken out before capping and editing: a cut or a step
	// must not leave one in a shape that is no longer recognised and removed
	// (it would reach the client), and the live error and the user log (which
	// carries our id) must be edited the same way. Callers append our own id.
	stripped := common.StripRequestIds(scanned)
	for i := range v.rules {
		rule := &v.rules[i]
		if !rule.matches(in, lower) {
			continue
		}
		switch rule.action {
		case RelayErrorActionKeep:
			if truncated {
				return v.truncatedDecision(stripped, in.Lang, rule.index, rule.name)
			}
			return RelayErrorDecision{RuleIndex: rule.index, RuleName: rule.name}
		case RelayErrorActionEdit:
			message := rule.apply(CapRelayErrorText(stripped))
			if strings.TrimSpace(message) == "" {
				message = v.fallbackMessage(in.Lang)
			}
			return RelayErrorDecision{Replace: true, Message: message, StatusCode: rule.statusCode, RuleIndex: rule.index, RuleName: rule.name}
		}
		return RelayErrorDecision{Replace: true, Message: rule.message, StatusCode: rule.statusCode, RuleIndex: rule.index, RuleName: rule.name}
	}
	if v.hideUpstream && in.Upstream {
		return RelayErrorDecision{Replace: true, Message: v.fallbackMessage(in.Lang), RuleIndex: -1}
	}
	if truncated {
		return v.truncatedDecision(stripped, in.Lang, -1, "")
	}
	return none
}

// truncatedDecision sends the part of an over-long error the rules saw in place
// of the full error, which a keep rule or no match would otherwise send.
func (v *relayErrorDisplayView) truncatedDecision(seen, lang string, index int, name string) RelayErrorDecision {
	if strings.TrimSpace(seen) == "" {
		seen = v.fallbackMessage(lang)
	}
	return RelayErrorDecision{Replace: true, Message: seen, RuleIndex: index, RuleName: name, Truncated: true}
}

// RelayErrorMatchPrefix returns the first MaxRelayErrorMatchLen bytes of text,
// cut on a character boundary, and whether anything was cut.
func RelayErrorMatchPrefix(text string) (string, bool) {
	if len(text) <= MaxRelayErrorMatchLen {
		return text, false
	}
	end := MaxRelayErrorMatchLen
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}

// CapRelayErrorText returns text cut to its first MaxRelayErrorTextLen
// characters; shorter text is returned as is. A cut never keeps part of a
// token (CutRelayErrorText), so half a key, address or request id never
// remains for rules to miss.
func CapRelayErrorText(text string) string {
	if len(text) <= MaxRelayErrorTextLen {
		return text
	}
	count := 0
	for i := range text {
		if count == MaxRelayErrorTextLen {
			return CutRelayErrorText(text, i)
		}
		count++
	}
	return text
}

// CutRelayErrorText returns text[:at] without a token the cut splits: when the
// character at at is not a delimiter, what follows the last delimiter before at
// is dropped too, then trailing whitespace. A cut inside a text without any
// delimiter leaves nothing. Characters that occur inside keys, addresses and
// ids (- _ . / : = +) are not delimiters. at must be a character boundary.
func CutRelayErrorText(text string, at int) string {
	if at >= len(text) {
		return text
	}
	head := text[:at]
	if next, _ := utf8.DecodeRuneInString(text[at:]); !isRelayErrorDelimiter(next) {
		end := strings.LastIndexFunc(head, isRelayErrorDelimiter)
		if end < 0 {
			return ""
		}
		head = head[:end]
	}
	return strings.TrimRightFunc(head, unicode.IsSpace)
}

func isRelayErrorDelimiter(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(",;()[]{}<>\"'`|", r)
}

// fallbackMessage is the configured default text, or the built-in one in lang.
func (v *relayErrorDisplayView) fallbackMessage(lang string) string {
	if v.defaultMessage != "" {
		return v.defaultMessage
	}
	return localizedRelayErrorDefault(i18n.Translate, lang)
}

// localizedRelayErrorDefault is the built-in fallback text in lang. translate
// returns the key itself when translations are not loaded, which must never
// reach a user.
func localizedRelayErrorDefault(translate func(lang, key string, args ...map[string]any) string, lang string) string {
	if lang == "" {
		lang = i18n.DefaultLang
	}
	if text := translate(lang, i18n.MsgRelayErrorDefaultMessage); text != "" && text != i18n.MsgRelayErrorDefaultMessage {
		return text
	}
	return builtInRelayErrorMessage
}

// RelayErrorDisplayEnabled reports whether the live configuration is on, so
// callers can skip preparing an input (the user's language) when it is off.
func RelayErrorDisplayEnabled() bool {
	v := relayErrorDisplaySnapshot.Load()
	return v != nil && v.enabled
}

// DecideRelayError evaluates the live configuration.
func DecideRelayError(in RelayErrorInput) RelayErrorDecision {
	return relayErrorDisplaySnapshot.Load().decide(in)
}

// PreviewRelayErrorDecision evaluates a draft configuration as if it were
// enabled, so an admin can see the effect before saving or switching it on.
func PreviewRelayErrorDecision(setting RelayErrorDisplaySetting, in RelayErrorInput) (RelayErrorDecision, error) {
	view, err := compileRelayErrorDisplay(setting)
	if err != nil {
		return RelayErrorDecision{RuleIndex: -1}, err
	}
	view.enabled = true
	return view.decide(in), nil
}

// upstreamErrorTypes are the error types only ever built from an upstream
// response body; upstreamErrorCodes are local codes that describe an upstream
// response or the call to it.
var upstreamErrorTypes = map[string]struct{}{
	"openai_error": {}, "claude_error": {}, "gemini_error": {}, "rerank_error": {}, "upstream_error": {},
}

var upstreamErrorCodes = map[string]struct{}{
	"bad_response_status_code": {}, "bad_response": {}, "bad_response_body": {}, "empty_response": {},
	"do_request_failed": {}, "read_response_body_failed": {}, "aws_invoke_error": {}, "channel:aws_client_error": {},
}

// IsUpstreamRelayErrorKind classifies an error by its type and code.
func IsUpstreamRelayErrorKind(errorType, errorCode string) bool {
	if _, ok := upstreamErrorTypes[errorType]; ok {
		return true
	}
	_, ok := upstreamErrorCodes[errorCode]
	return ok
}

// MaskRelayErrorLogInputForUser rewrites an error log's content for the
// user-facing log view with the live configuration; the caller classifies the
// error (in.Message is the text matched). The stored row keeps the original for
// admins; rewriting at read time makes rule changes apply to history too.
// statusCode is the rule's status override, 0 when the status was not changed.
func MaskRelayErrorLogInputForUser(in RelayErrorInput, requestID string) (content string, statusCode int, replaced bool) {
	return relayErrorDisplaySnapshot.Load().maskErrorLogInput(in, requestID)
}

func (v *relayErrorDisplayView) maskErrorLogInput(in RelayErrorInput, requestID string) (string, int, bool) {
	d := v.decide(in)
	if !d.Replace {
		return in.Message, 0, false
	}
	if requestID == "" {
		return d.Message, d.StatusCode, true
	}
	return common.MessageWithRequestId(d.Message, requestID), d.StatusCode, true
}

// StreamFailureRelayErrorInput classifies an error that ended a stream after
// its headers went out: the upstream's error (code upstream_stream_error), or
// our own time limit when timedOut (relay_timeout, 504). The live terminal frame
// and the user's error log of the same stream are both decided with it, so they
// read the same way.
func StreamFailureRelayErrorInput(timedOut bool, message string) RelayErrorInput {
	if timedOut {
		return RelayErrorInput{ErrorCode: RelayTimeoutErrorCode, StatusCode: 504, Message: message}
	}
	return RelayErrorInput{Upstream: true, ErrorCode: RelayStreamErrorCode, Message: message}
}

// MaskRelayStreamErrorForUser rewrites one error text recorded in a consume
// log's stream_status for the user-facing log view. The legacy stream path
// records whatever ended the stream, typically the upstream's own text, so it is
// matched as an upstream stream error — the same input the terminal frame of
// that stream was decided with.
func MaskRelayStreamErrorForUser(message, lang string) (string, bool) {
	in := StreamFailureRelayErrorInput(false, message)
	in.Lang = lang
	d := relayErrorDisplaySnapshot.Load().decide(in)
	if !d.Replace {
		return message, false
	}
	return d.Message, true
}

// presetTranslate is i18n.Translate; a seam for tests.
var presetTranslate = i18n.Translate

// RelayErrorPresetRules is the starting rule set offered in the admin UI, with
// names and messages in lang (the admin's UI language).
func RelayErrorPresetRules(lang string) []RelayErrorRule {
	// Without translations Translate returns the key, which must never become a
	// message users read: leave the text empty so the admin has to write it (a
	// replace rule without a message cannot be saved).
	t := func(key string) string {
		if text := presetTranslate(lang, key); text != key {
			return text
		}
		return ""
	}
	modelUnavailable := t(i18n.MsgRelayErrorPresetModelMessage)
	return []RelayErrorRule{
		{
			Name: t(i18n.MsgRelayErrorPresetKeepParamsName), Source: RelayErrorSourceUpstream, StatusCodes: []int{400, 422},
			Keywords: []string{"context length", "maximum context", "context_length", "temperature", "max_tokens", "invalid"},
			Action:   RelayErrorActionKeep,
		},
		{
			Name: t(i18n.MsgRelayErrorPresetQuotaName), Source: RelayErrorSourceUpstream,
			Keywords: []string{"quota", "balance", "余额", "额度"},
			Action:   RelayErrorActionReplace, Message: t(i18n.MsgRelayErrorPresetQuotaMessage), StatusCode: 503,
		},
		{
			Name: t(i18n.MsgRelayErrorPresetRateLimitName), Source: RelayErrorSourceUpstream, StatusCodes: []int{429},
			Action: RelayErrorActionReplace, Message: t(i18n.MsgRelayErrorPresetRateLimitMessage),
		},
		// Two rules: conditions inside one rule are "and", this needs "or".
		{
			Name: t(i18n.MsgRelayErrorPresetModelCodeName), Source: RelayErrorSourceAny, ErrorCodes: []string{"model_not_found"},
			Action: RelayErrorActionReplace, Message: modelUnavailable,
		},
		{
			Name: t(i18n.MsgRelayErrorPresetModelChannelName), Source: RelayErrorSourceAny, Keywords: []string{"no available channel"},
			Action: RelayErrorActionReplace, Message: modelUnavailable,
		},
		{
			Name: t(i18n.MsgRelayErrorPresetTimeoutName), Source: RelayErrorSourceLocal, ErrorCodes: []string{"relay_timeout"},
			Action: RelayErrorActionReplace, Message: t(i18n.MsgRelayErrorPresetTimeoutMessage),
		},
	}
}
