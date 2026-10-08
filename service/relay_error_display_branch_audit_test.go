package service

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
)

// Branch audit: the two halves of one managed-stream failure. The terminal
// frame the client receives is decided as an upstream error and masked, while
// the text the settlement puts into an error log (StreamFailureLogMessage) is
// the upstream's own error.message, kept for admins. The user's log view
// decides that row the same way as the frame (see
// TestStreamFailureMessagesReachSettlementLogs and
// model/log_branch_audit_regression_test.go).
func TestBranchAuditStreamFailure_LiveFrameMaskedLogTextIsUpstream(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	payload := []byte(`{"type":"error","error":{"type":"billing_error","message":"Your credit balance is too low (org vip-azure-3)"}}`)
	frame := []byte("event: error\ndata: " + string(payload) + "\n\n")

	message, forward := PresentStreamTerminalMessage(errorDisplayCtx(), payload, "generic")
	assert.Equal(t, "Unavailable", message)
	assert.False(t, forward, "the live client never sees the upstream text")

	logText := StreamFailureLogMessage(relaycommon.StreamSnapshot{ErrorFrame: frame})
	assert.Contains(t, logText, "credit balance", "the usage-log text is the upstream's own message")
}

// The same stream text decided through the user-log entry point for
// stream_status texts is masked too.
func TestBranchAuditStreamFailure_StreamStatusEntryPointMasks(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	masked, replaced := operation_setting.MaskRelayStreamErrorForUser("Your credit balance is too low (org vip-azure-3)", "en")
	assert.True(t, replaced)
	assert.Equal(t, "Unavailable", masked)
}
