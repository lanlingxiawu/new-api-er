package i18n

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountResponseMessagesResolveWithEvidence(t *testing.T) {
	require.NoError(t, Init())
	for _, lang := range SupportedLanguages() {
		for _, ruleKey := range []string{MsgAuditSecurityRuleMultiAccountKeyAccess, MsgAuditSecurityRuleMultiSourceKeyAccess, MsgAuditSecurityRuleNewSourceChannelRewrite, MsgAuditSecurityRuleSessionReuse} {
			rule := Translate(lang, ruleKey)
			require.NotEmpty(t, rule)
			require.NotEqual(t, ruleKey, rule)
			message := Translate(lang, MsgAuditSecurityBanSucceeded, map[string]any{"id": 731, "rule": rule})
			require.Contains(t, message, "731", lang)
			require.Contains(t, message, rule, lang)
			require.NotContains(t, message, "{{", lang)
		}
		for _, key := range []string{MsgAuditSecurityBanFailed, MsgAuditSecurityBanUnconfirmed} {
			message := Translate(lang, key, map[string]any{"id": 731})
			require.Contains(t, message, "731", lang)
			require.NotContains(t, message, "{{", lang)
		}
		for _, key := range []string{MsgAuditSecurityResponseInvalidTarget, MsgAuditSecurityResponseConflict} {
			require.NotEqual(t, key, Translate(lang, key), lang)
		}
	}
}
