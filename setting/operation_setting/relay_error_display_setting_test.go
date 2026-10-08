package operation_setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rulesJSON(t *testing.T, rules ...RelayErrorRule) string {
	t.Helper()
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	return string(raw)
}

func enabledSetting(t *testing.T, hideUpstream bool, rules ...RelayErrorRule) RelayErrorDisplaySetting {
	t.Helper()
	return RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: hideUpstream, DefaultMessage: "兜底文案", Rules: rulesJSON(t, rules...)}
}

func decide(t *testing.T, s RelayErrorDisplaySetting, in RelayErrorInput) RelayErrorDecision {
	t.Helper()
	d, err := PreviewRelayErrorDecision(s, in)
	require.NoError(t, err)
	return d
}

// ---- 默认与总开关

func TestRelayErrorDisplay_DefaultIsOff(t *testing.T) {
	assert.False(t, relayErrorDisplayDefaults().Enabled, "an unconfigured deployment shows every error unchanged")
	view := compileRelayErrorDisplayParts(relayErrorDisplayDefaults())
	d := view.decide(RelayErrorInput{Upstream: true, StatusCode: 503, Message: "No available channel under group X"})
	assert.False(t, d.Replace)
}

func TestRelayErrorDisplay_DisabledNeverReplaces(t *testing.T) {
	s := enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionReplace, Message: "x"})
	s.Enabled = false
	view := compileRelayErrorDisplayParts(s)
	assert.False(t, view.decide(RelayErrorInput{Upstream: true, StatusCode: 500, Message: "boom"}).Replace)
}

// ---- 兜底：隐藏上游错误

func TestRelayErrorDisplay_HideUpstreamFallback(t *testing.T) {
	s := enabledSetting(t, true)
	d := decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 403, ErrorCode: "pre_consume_token_quota_failed", Message: "token quota is not enough, token remain quota: $0.000002"})
	assert.True(t, d.Replace)
	assert.Equal(t, "兜底文案", d.Message)
	assert.Equal(t, -1, d.RuleIndex)
	assert.Zero(t, d.StatusCode, "the fallback never rewrites the status code")

	local := decide(t, s, RelayErrorInput{Upstream: false, StatusCode: 400, ErrorCode: "invalid_request", Message: "field messages is required"})
	assert.False(t, local.Replace, "local errors are ours and are not hidden by the upstream fallback")

	s.HideUpstreamErrors = false
	assert.False(t, decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 500, Message: "boom"}).Replace)
}

// An empty fallback text means the built-in one, in the user's language: a
// hardcoded default would show Chinese to English users (Rule 13).
func TestRelayErrorDisplay_EmptyDefaultMessageFollowsLanguage(t *testing.T) {
	require.NoError(t, i18n.Init())
	s := enabledSetting(t, true)
	s.DefaultMessage = "   "
	in := RelayErrorInput{Upstream: true, StatusCode: 500, Message: "boom"}

	in.Lang = i18n.LangEn
	assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayErrorDefaultMessage), decide(t, s, in).Message)
	in.Lang = i18n.LangZhCN
	assert.Equal(t, i18n.Translate(i18n.LangZhCN, i18n.MsgRelayErrorDefaultMessage), decide(t, s, in).Message)
	in.Lang = ""
	assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayErrorDefaultMessage), decide(t, s, in).Message,
		"no language falls back to English")

	assert.NotEqual(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayErrorDefaultMessage), i18n.Translate(i18n.LangZhCN, i18n.MsgRelayErrorDefaultMessage))
	assert.NotContains(t, i18n.Translate(i18n.LangEn, i18n.MsgRelayErrorDefaultMessage), "服务")
}

// An admin's own fallback text is used as written, whatever the language.
func TestRelayErrorDisplay_CustomDefaultMessageVerbatim(t *testing.T) {
	s := enabledSetting(t, true)
	d := decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 500, Message: "boom", Lang: i18n.LangEn})
	assert.Equal(t, "兜底文案", d.Message)
}

// Without translations loaded (a failed i18n init is not fatal) the user must
// still get a sentence, never a message key.
func TestRelayErrorDisplay_DefaultMessageWithoutTranslations(t *testing.T) {
	assert.Equal(t, builtInRelayErrorMessage, localizedRelayErrorDefault(func(string, string, ...map[string]any) string {
		return i18n.MsgRelayErrorDefaultMessage
	}, i18n.LangEn))
}

func TestRelayErrorDisplay_DefaultsHaveNoFixedText(t *testing.T) {
	assert.Empty(t, relayErrorDisplayDefaults().DefaultMessage, "the stored default defers to the localized built-in text")
}

// ---- 规则条件

func TestRelayErrorDisplay_RuleConditions(t *testing.T) {
	rule := RelayErrorRule{
		Name: "quota", Source: RelayErrorSourceUpstream, StatusCodes: []int{403, 429},
		ErrorCodes: []string{"pre_consume_token_quota_failed"}, Keywords: []string{"QUOTA", "余额"},
		Action: RelayErrorActionReplace, Message: "服务繁忙", StatusCode: 503,
	}
	s := enabledSetting(t, false, rule)
	base := RelayErrorInput{Upstream: true, StatusCode: 403, ErrorCode: "pre_consume_token_quota_failed", Message: "token quota is not enough"}

	d := decide(t, s, base)
	require.True(t, d.Replace, "all conditions met (keyword is case-insensitive)")
	assert.Equal(t, "服务繁忙", d.Message)
	assert.Equal(t, 503, d.StatusCode)
	assert.Equal(t, 0, d.RuleIndex)
	assert.Equal(t, "quota", d.RuleName)

	for name, in := range map[string]RelayErrorInput{
		"source mismatch":  {Upstream: false, StatusCode: 403, ErrorCode: base.ErrorCode, Message: base.Message},
		"status mismatch":  {Upstream: true, StatusCode: 400, ErrorCode: base.ErrorCode, Message: base.Message},
		"code mismatch":    {Upstream: true, StatusCode: 403, ErrorCode: "other", Message: base.Message},
		"keyword mismatch": {Upstream: true, StatusCode: 403, ErrorCode: base.ErrorCode, Message: "forbidden"},
	} {
		assert.False(t, decide(t, s, in).Replace, name)
	}
	// "或"：同一条件的任一值命中即可。
	assert.True(t, decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 429, ErrorCode: "PRE_CONSUME_TOKEN_QUOTA_FAILED", Message: "账户余额不足"}).Replace)
}

func TestRelayErrorDisplay_EmptyConditionsMatchWholeSource(t *testing.T) {
	s := enabledSetting(t, false, RelayErrorRule{Source: RelayErrorSourceLocal, Action: RelayErrorActionReplace, Message: "本地错误"})
	assert.True(t, decide(t, s, RelayErrorInput{Upstream: false, StatusCode: 503, Message: "No available channel"}).Replace)
	assert.False(t, decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 503, Message: "No available channel"}).Replace)
}

func TestRelayErrorDisplay_FirstMatchWinsAndKeepShortCircuits(t *testing.T) {
	s := enabledSetting(t, true,
		RelayErrorRule{Name: "keep context", Source: RelayErrorSourceUpstream, Keywords: []string{"context length"}, Action: RelayErrorActionKeep},
		RelayErrorRule{Name: "all upstream", Source: RelayErrorSourceUpstream, Action: RelayErrorActionReplace, Message: "上游错误"},
	)
	kept := decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 400, Message: "maximum context length exceeded"})
	assert.False(t, kept.Replace, "keep stops evaluation and also bypasses the fallback")
	assert.Equal(t, 0, kept.RuleIndex)
	assert.Equal(t, "keep context", kept.RuleName)

	other := decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 500, Message: "boom"})
	assert.True(t, other.Replace)
	assert.Equal(t, "上游错误", other.Message)
	assert.Equal(t, 1, other.RuleIndex)
}

// ---- 来源判定

func TestIsUpstreamRelayErrorKind(t *testing.T) {
	for _, errType := range []string{"openai_error", "claude_error", "gemini_error", "rerank_error", "upstream_error"} {
		assert.True(t, IsUpstreamRelayErrorKind(errType, "anything"), errType)
	}
	for _, code := range []string{"bad_response_status_code", "bad_response", "bad_response_body", "empty_response", "do_request_failed", "read_response_body_failed", "aws_invoke_error", "channel:aws_client_error"} {
		assert.True(t, IsUpstreamRelayErrorKind("new_api_error", code), code)
	}
	for _, code := range []string{"relay_timeout", "invalid_request", "model_not_found", "insufficient_user_quota", "get_channel_failed", ""} {
		assert.False(t, IsUpstreamRelayErrorKind("new_api_error", code), code)
	}
}

// ---- 用户日志

// maskLogWith runs view's user-log masking on an error log classified by its
// stored error type and code, as model.maskErrorLogForUser does for a row that
// is not a stream failure.
func maskLogWith(view *relayErrorDisplayView, content, errorType, errorCode string, statusCode int, requestID, lang string) (string, bool) {
	masked, _, replaced := view.maskErrorLogInput(RelayErrorInput{
		Upstream:   IsUpstreamRelayErrorKind(errorType, errorCode),
		StatusCode: statusCode,
		ErrorCode:  errorCode,
		Message:    content,
		Lang:       lang,
	}, requestID)
	return masked, replaced
}

func TestMaskRelayErrorLogForUser(t *testing.T) {
	view := compileRelayErrorDisplayParts(enabledSetting(t, true))
	content, replaced := maskLogWith(view, "upstream said token remain quota $1 (request id: r1)", "openai_error", "pre_consume_token_quota_failed", 403, "r1", "")
	assert.True(t, replaced)
	assert.Equal(t, "兜底文案 (request id: r1)", content)

	local, replaced := maskLogWith(view, "field messages is required (request id: r2)", "new_api_error", "invalid_request", 400, "r2", "")
	assert.False(t, replaced)
	assert.Equal(t, "field messages is required (request id: r2)", local)
}

// ---- 校验

func TestValidateRelayErrorDisplaySetting(t *testing.T) {
	valid := enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionReplace, Message: "m", StatusCode: 503, StatusCodes: []int{400}})
	require.NoError(t, ValidateRelayErrorDisplaySetting(valid))
	require.NoError(t, ValidateRelayErrorDisplaySetting(RelayErrorDisplaySetting{Rules: ""}), "no rules")
	require.NoError(t, ValidateRelayErrorDisplaySetting(RelayErrorDisplaySetting{Rules: "[]"}))

	long := strings.Repeat("x", MaxRelayErrorMessageLen+1)
	bad := map[string]RelayErrorDisplaySetting{
		"not json":            {Rules: "{"},
		"default too long":    {DefaultMessage: long},
		"bad source":          enabledSetting(t, true, RelayErrorRule{Source: "cloud", Action: RelayErrorActionKeep}),
		"bad action":          enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: "drop"}),
		"replace w/o message": enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionReplace}),
		"message too long":    enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionReplace, Message: long}),
		"status out of range": enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionReplace, Message: "m", StatusCode: 200}),
		"match status bad":    enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep, StatusCodes: []int{99}}),
		"empty keyword":       enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep, Keywords: []string{" "}}),
		"keyword too long":    enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep, Keywords: []string{strings.Repeat("k", MaxRelayErrorKeywordLen+1)}}),
	}
	manyKeywords := make([]string, MaxRelayErrorRuleKeywords+1)
	for i := range manyKeywords {
		manyKeywords[i] = "k"
	}
	bad["too many keywords"] = enabledSetting(t, true, RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep, Keywords: manyKeywords})
	manyRules := make([]RelayErrorRule, MaxRelayErrorRules+1)
	for i := range manyRules {
		manyRules[i] = RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep}
	}
	bad["too many rules"] = enabledSetting(t, true, manyRules...)
	for name, s := range bad {
		err := ValidateRelayErrorDisplaySetting(s)
		assert.ErrorIs(t, err, ErrRelayErrorDisplayInvalid, name)
	}
	// 边界：恰好上限可以通过。
	atLimit := make([]RelayErrorRule, MaxRelayErrorRules)
	for i := range atLimit {
		atLimit[i] = RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep}
	}
	require.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true, atLimit...)))

	// Lengths are counted in characters, the same for error codes and
	// keywords (and as the admin UI counts them): 100 CJK characters pass,
	// 101 do not.
	cjk := strings.Repeat("额", MaxRelayErrorKeywordLen)
	require.NoError(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true,
		RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep, ErrorCodes: []string{cjk}, Keywords: []string{cjk}})))
	assert.ErrorIs(t, ValidateRelayErrorDisplaySetting(enabledSetting(t, true,
		RelayErrorRule{Source: RelayErrorSourceAny, Action: RelayErrorActionKeep, ErrorCodes: []string{cjk + "额"}})), ErrRelayErrorDisplayInvalid)
}

// 数据库里的规则损坏时只丢弃规则，隐藏上游错误与兜底文案照常生效，绝不影响请求。
func TestRelayErrorDisplay_CorruptStoredRulesAreSkipped(t *testing.T) {
	view := compileRelayErrorDisplayParts(RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Busy", Rules: "{not json"})
	d := view.decide(RelayErrorInput{Upstream: true, StatusCode: 500, Message: "boom"})
	assert.True(t, d.Replace)
	assert.Equal(t, "Busy", d.Message)
	require.Len(t, view.skipped, 1)
	assert.Equal(t, 0, view.skipped[0].Rule, "the list as a whole")
	assert.False(t, view.decide(RelayErrorInput{StatusCode: 500, Message: "local"}).Replace)
}

func TestRelayErrorPresetRulesAreValid(t *testing.T) {
	require.NoError(t, i18n.Init())
	s := RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: rulesJSON(t, RelayErrorPresetRules(i18n.LangZhCN)...)}
	require.NoError(t, ValidateRelayErrorDisplaySetting(s))
	d := decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 400, Message: "This model's maximum context length is 8192 tokens"})
	assert.False(t, d.Replace, "useful client-side errors are kept by the preset")
	d = decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 403, Message: "token quota is not enough, token remain quota: $0.1"})
	assert.True(t, d.Replace)
	assert.Equal(t, 503, d.StatusCode)
	d = decide(t, s, RelayErrorInput{Upstream: true, StatusCode: 503, Message: "No available channel for model x under group ChatGPT_AZ (distributor)"})
	assert.True(t, d.Replace)
	assert.NotContains(t, d.Message, "ChatGPT_AZ")
}

// Presets are generated in the admin's language; only names and messages
// differ, the matching conditions are the same rule set.
func TestRelayErrorPresetRules_Localized(t *testing.T) {
	require.NoError(t, i18n.Init())
	en, zh := RelayErrorPresetRules(i18n.LangEn), RelayErrorPresetRules(i18n.LangZhCN)
	require.Len(t, en, len(zh))
	for i := range en {
		assert.NotEqual(t, en[i].Name, zh[i].Name, "rule %d", i)
		for _, text := range []string{en[i].Name, en[i].Message} {
			for _, r := range text {
				assert.False(t, r >= 0x4e00 && r <= 0x9fff, "rule %d has CJK text in English: %q", i, text)
			}
			assert.NotContains(t, text, "relay_error.", "rule %d shows a message key", i)
		}
		enRule, zhRule := en[i], zh[i]
		enRule.Name, enRule.Message, zhRule.Name, zhRule.Message = "", "", "", ""
		assert.Equal(t, zhRule, enRule, "rule %d conditions", i)
		if en[i].Action == RelayErrorActionReplace {
			assert.NotEmpty(t, en[i].Message)
		}
	}
	s := RelayErrorDisplaySetting{Enabled: true, Rules: rulesJSON(t, en...)}
	require.NoError(t, ValidateRelayErrorDisplaySetting(s))
}

// Without translations (a failed i18n init is not fatal) presets must not carry
// message keys an admin could save and users would then read. Texts are left
// empty instead: names are optional and a replace rule without a message
// cannot be saved, so the admin has to write one.
func TestRelayErrorPresetRules_NoTranslations(t *testing.T) {
	previous := presetTranslate
	presetTranslate = func(_ string, key string, _ ...map[string]any) string { return key }
	t.Cleanup(func() { presetTranslate = previous })
	for i, rule := range RelayErrorPresetRules(i18n.LangEn) {
		assert.NotContains(t, rule.Name, "relay_error.", "rule %d", i)
		assert.NotContains(t, rule.Message, "relay_error.", "rule %d", i)
	}
}
