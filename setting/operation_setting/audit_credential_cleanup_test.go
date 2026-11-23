package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestAutomaticCredentialActionCombinations(t *testing.T) {
	for ruleIndex := range 4 {
		for mask := range 8 {
			s := DefaultAuditSecuritySetting()
			s.AutoBanEnabled, s.CaptureEnabled = true, true
			var rules []map[string]any
			require.NoError(t, common.UnmarshalJsonStr(s.AutoBanRulesJSON, &rules))
			for i := range rules {
				rules[i]["selected"] = i == ruleIndex && mask&1 != 0
				rules[i]["clear_api_keys"] = i == ruleIndex && mask&2 != 0
				rules[i]["clear_access_tokens"] = i == ruleIndex && mask&4 != 0
			}
			raw, err := common.Marshal(rules)
			require.NoError(t, err)
			s.AutoBanRulesJSON = string(raw)
			if mask == 0 {
				require.Error(t, ValidateAuditSecuritySetting(s))
			} else {
				require.NoError(t, ValidateAuditSecuritySetting(s), "rule %d mask %d", ruleIndex, mask)
			}
			if ruleIndex == 3 && mask != 0 {
				s.CaptureEnabled = false
				require.Error(t, ValidateAuditSecuritySetting(s))
			}
		}
	}
}

func TestAutomaticCredentialFieldsRejectAmbiguousValues(t *testing.T) {
	for _, name := range []string{"clear_api_keys", "clear_access_tokens"} {
		for _, value := range []any{nil, "true", 1, []bool{true}} {
			var rules []map[string]any
			require.NoError(t, common.UnmarshalJsonStr(defaultAutoBanRulesJSON, &rules))
			rules[0][name] = value
			raw, err := common.Marshal(rules)
			require.NoError(t, err)
			_, err = ParseAutoBanRules(string(raw))
			require.Error(t, err, "%s must reject %#v", name, value)
		}
	}
	_, err := ParseAutoBanRules(defaultAutoBanRulesJSON)
	require.NoError(t, err, "legacy configurations remain valid")
}
