package claude

import (
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Branch audit of the strict Claude stream's terminal error under the relay
// error display (masking) setting: masked upstream frames are replaced by one
// well-formed Claude error frame, unmasked ones stay byte-identical, and the
// stream's end classification does not depend on the display decision.

func withStrictErrorDisplay(t *testing.T, setting operation_setting.RelayErrorDisplaySetting) {
	t.Helper()
	require.NoError(t, operation_setting.ValidateRelayErrorDisplaySetting(setting))
	previous := operation_setting.GetRelayErrorDisplaySetting()
	operation_setting.ReplaceRelayErrorDisplaySetting(setting)
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
}

const strictAuditUpstreamError = "event: error\r\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"original upstream secret\"}}\r\n\r\n"

// lastErrorFrame returns the data payload of the only "event: error" frame.
func lastErrorFrame(t *testing.T, body string) gjson.Result {
	t.Helper()
	require.Equal(t, 1, strings.Count(body, "event: error"), body)
	tail := body[strings.LastIndex(body, "event: error"):]
	_, data := relaycommon.StreamFramePayload([]byte(tail))
	require.True(t, gjson.ValidBytes(data), "terminal frame data must be valid JSON: %q", data)
	return gjson.ParseBytes(data)
}

func TestStrictBranchAudit_MaskedUpstreamErrorFrame(t *testing.T) {
	// Reference run with masking off: verbatim forward.
	withStrictErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false})
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + strictText + strictAuditUpstreamError)))
	refUsage, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.True(t, strings.HasSuffix(w.Body.String(), strictAuditUpstreamError))
	refReason := info.StreamStatus.EndReason

	withStrictErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: `服务暂时不可用 "retry"`})
	c, w, resp, info = strictTestContext(io.NopCloser(strings.NewReader(strictStart + strictText + strictAuditUpstreamError)))
	usage, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	body := w.Body.String()
	assert.NotContains(t, body, "original upstream secret", "a masked upstream frame is not forwarded")
	assert.NotContains(t, body, "overloaded_error")
	frame := lastErrorFrame(t, body)
	assert.Equal(t, "error", frame.Get("type").String())
	assert.Equal(t, "api_error", frame.Get("error.type").String())
	assert.Equal(t, `服务暂时不可用 "retry"`, frame.Get("error.message").String())
	assert.True(t, strings.HasSuffix(body, "\n\n"), "the replacement is one complete SSE frame")

	// The display decision only changes the client's text.
	assert.Equal(t, refReason, info.StreamStatus.EndReason)
	require.NotNil(t, usage)
	assert.Equal(t, refUsage.CompletionTokens, usage.CompletionTokens)
	assert.Equal(t, refUsage.PromptTokens, usage.PromptTokens)
}

// A keep rule matching the upstream text leaves the frame byte-identical.
func TestStrictBranchAudit_KeepRuleForwardsVerbatim(t *testing.T) {
	rules, err := common.Marshal([]operation_setting.RelayErrorRule{{
		Source: operation_setting.RelayErrorSourceUpstream, Keywords: []string{"original upstream"}, Action: operation_setting.RelayErrorActionKeep,
	}})
	require.NoError(t, err)
	withStrictErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "masked", Rules: string(rules)})
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + strictAuditUpstreamError)))
	_, sErr := strictClaudeStream(c, resp, info)
	require.Nil(t, sErr)
	assert.True(t, strings.HasSuffix(w.Body.String(), strictAuditUpstreamError))
	assert.NotContains(t, w.Body.String(), "masked")
}

// A stream that fails without an upstream error frame (truncated) gets the
// local generic error; with upstream errors hidden it carries the configured text.
func TestStrictBranchAudit_TruncatedStreamUsesDisplayText(t *testing.T) {
	withStrictErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "masked text"})
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + strictText)))
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	frame := lastErrorFrame(t, w.Body.String())
	assert.Equal(t, "masked text", frame.Get("error.message").String())
	assert.Equal(t, "api_error", frame.Get("error.type").String())
	assert.NotContains(t, w.Body.String(), "end_turn")
}

// A completed stream writes no terminal error whatever the display setting.
func TestStrictBranchAudit_CompleteStreamUnaffectedByMasking(t *testing.T) {
	withStrictErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "masked text"})
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + strictText + strictStop)))
	usage, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	assert.NotContains(t, w.Body.String(), "event: error")
	assert.NotContains(t, w.Body.String(), "masked text")
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.Equal(t, 7, usage.CompletionTokens)
}
