// Regression (branch audit L2, docs/design/branch-audit-vs-main.md): masking
// error texts in a user's log view must not re-encode unrelated values of other
// through float64.

package model

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchAuditErrorMaskingPreservesUnrelatedNumbers(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{
		Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable",
	})
	for _, tc := range []struct {
		name  string
		kind  int
		other string
	}{
		{"error", LogTypeError, `{"amount":9007199254740993,"error_type":"openai_error","error_code":"bad_response_status_code","status_code":500}`},
		{"stream", LogTypeConsume, `{"amount":9007199254740993,"stream_status":{"status":"error","end_error":"upstream unavailable","errors":["upstream unavailable"]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &Log{Type: tc.kind, Content: "upstream unavailable", Other: tc.other}
			// Supply language directly: metadata projection does not need a DB read.
			maskProjectedLogForUser(log, nil, func(int) string { return "en" })
			var got map[string]json.RawMessage
			require.NoError(t, common.UnmarshalJsonStr(log.Other, &got))
			assert.Equal(t, "9007199254740993", string(got["amount"]), "masking error text must preserve unrelated exact integers, as main's RawMessage projection does")
		})
	}
}
