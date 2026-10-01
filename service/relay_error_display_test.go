package service

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withRelayErrorDisplay(t *testing.T, setting operation_setting.RelayErrorDisplaySetting) {
	t.Helper()
	require.NoError(t, operation_setting.ValidateRelayErrorDisplaySetting(setting))
	previous := operation_setting.GetRelayErrorDisplaySetting()
	operation_setting.ReplaceRelayErrorDisplaySetting(setting)
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
}

func displayRules(t *testing.T, rules ...operation_setting.RelayErrorRule) string {
	t.Helper()
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	return string(raw)
}

func errorDisplayCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// An upstream new-api answering "no channel" — the exact leak seen against a real upstream.
func upstreamNoChannelError() *types.NewAPIError {
	return types.WithOpenAIError(types.OpenAIError{
		Message: "No available channel for model x under group ChatGPT_AZ (distributor)",
		Type:    "new_api_error",
		Code:    "model_not_found",
		Param:   "model",
	}, http.StatusServiceUnavailable)
}

func TestPresentRelayError_DisabledIsUnchanged(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	original := upstreamNoChannelError()
	got, message := PresentRelayError(errorDisplayCtx(), original, "orig (request id: r1)", "r1")
	assert.Same(t, original, got)
	assert.Equal(t, "orig (request id: r1)", message)
}

func TestPresentRelayError_UpstreamFallbackStripsUpstreamFields(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	got, message := PresentRelayError(errorDisplayCtx(), upstreamNoChannelError(), "leaky (request id: r1)", "r1")
	require.NotNil(t, got)
	assert.Equal(t, "服务暂时不可用 (request id: r1)", message)
	assert.Equal(t, http.StatusServiceUnavailable, got.StatusCode, "the fallback keeps the status")
	got.SetMessage(message)
	oai := got.ToOpenAIError()
	assert.Equal(t, "服务暂时不可用 (request id: r1)", oai.Message)
	assert.Equal(t, "new_api_error", oai.Type)
	assert.Equal(t, "upstream_error", fmt.Sprint(oai.Code), "upstream codes never reach the client once masked")
	assert.Empty(t, oai.Param)
	assert.Nil(t, oai.Metadata)
	claude := got.ToClaudeError()
	assert.Equal(t, "服务暂时不可用 (request id: r1)", claude.Message)
	assert.NotContains(t, claude.Type, "model_not_found")
}

func TestPresentRelayError_RuleStatusOverride(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, Rules: displayRules(t, operation_setting.RelayErrorRule{
		Source: operation_setting.RelayErrorSourceUpstream, Keywords: []string{"quota"},
		Action: operation_setting.RelayErrorActionReplace, Message: "服务繁忙", StatusCode: 503,
	})})
	quota := types.WithOpenAIError(types.OpenAIError{Message: "token quota is not enough, token remain quota: $0.1", Code: "pre_consume_token_quota_failed"}, http.StatusForbidden)
	got, message := PresentRelayError(errorDisplayCtx(), quota, "x", "r2")
	assert.Equal(t, http.StatusServiceUnavailable, got.StatusCode)
	assert.Equal(t, "服务繁忙 (request id: r2)", message)
}

func TestPresentRelayError_LocalKeepsLocalCode(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: displayRules(t, operation_setting.RelayErrorRule{
		Source: operation_setting.RelayErrorSourceLocal, ErrorCodes: []string{"relay_timeout"},
		Action: operation_setting.RelayErrorActionReplace, Message: "请求处理超时",
	})})
	timeout := types.NewErrorWithStatusCode(errors.New("The request exceeded the configured 6 second time limit."), types.ErrorCodeRelayTimeout, http.StatusGatewayTimeout)
	got, message := PresentRelayError(errorDisplayCtx(), timeout, "x", "r3")
	assert.Equal(t, types.ErrorCodeRelayTimeout, got.GetErrorCode(), "the timeout writer keys off this code")
	assert.Equal(t, http.StatusGatewayTimeout, got.StatusCode)
	assert.Equal(t, "请求处理超时 (request id: r3)", message)

	validation := types.NewErrorWithStatusCode(errors.New("field messages is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest)
	same, msg := PresentRelayError(errorDisplayCtx(), validation, "field messages is required (request id: r4)", "r4")
	assert.Same(t, validation, same, "a local error no rule matches is untouched, even with the upstream fallback on")
	assert.Equal(t, "field messages is required (request id: r4)", msg)
}

func TestPresentRelayError_NilIsNil(t *testing.T) {
	got, message := PresentRelayError(errorDisplayCtx(), nil, "m", "r")
	assert.Nil(t, got)
	assert.Equal(t, "m", message)
}

func TestIsUpstreamRelayError(t *testing.T) {
	assert.True(t, IsUpstreamRelayError(upstreamNoChannelError()))
	assert.True(t, IsUpstreamRelayError(types.NewOpenAIError(errors.New("x"), types.ErrorCodeBadResponseStatusCode, 500)))
	assert.False(t, IsUpstreamRelayError(types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeInvalidRequest, 400)))
	assert.False(t, IsUpstreamRelayError(nil))
}

func TestPresentLocalRelayAbort(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, Rules: displayRules(t, operation_setting.RelayErrorRule{
		Source: operation_setting.RelayErrorSourceLocal, Keywords: []string{"no available channel"},
		Action: operation_setting.RelayErrorActionReplace, Message: "当前模型暂不可用", StatusCode: 404,
	})})
	message := "No available channel for model x under group vip (distributor)"
	status, msg, code := PresentLocalRelayAbort(errorDisplayCtx(), true, http.StatusServiceUnavailable, message, "model_not_found")
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "当前模型暂不可用", msg)
	assert.Equal(t, "model_not_found", code)

	status, msg, code = PresentLocalRelayAbort(errorDisplayCtx(), false, http.StatusServiceUnavailable, message, "model_not_found")
	assert.Equal(t, http.StatusServiceUnavailable, status, "dashboard routes are not relay responses")
	assert.Equal(t, message, msg)
	assert.Equal(t, "model_not_found", code)
}

func TestPresentStreamTerminalMessage(t *testing.T) {
	const generic = "Upstream stream failed. Please retry."
	upstreamFrame := []byte(`{"error":{"message":"token quota is not enough, remain $0.1","type":"new_api_error"}}`)

	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false})
	msg, forward := PresentStreamTerminalMessage(errorDisplayCtx(), upstreamFrame, generic)
	assert.Equal(t, generic, msg)
	assert.True(t, forward, "disabled: upstream frames are forwarded as before")

	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	msg, forward = PresentStreamTerminalMessage(errorDisplayCtx(), upstreamFrame, generic)
	assert.Equal(t, "服务暂时不可用", msg)
	assert.False(t, forward, "a masked upstream frame must not be forwarded verbatim")

	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: displayRules(t, operation_setting.RelayErrorRule{
		Source: operation_setting.RelayErrorSourceUpstream, Keywords: []string{"quota"}, Action: operation_setting.RelayErrorActionKeep,
	})})
	msg, forward = PresentStreamTerminalMessage(errorDisplayCtx(), upstreamFrame, generic)
	assert.Equal(t, generic, msg)
	assert.True(t, forward, "keep leaves the original behaviour")
}

// With masking on, a same-protocol upstream error frame — forwarded verbatim
// today — is replaced by our own frame carrying the configured text.
func TestStreamTerminalError_MaskedUpstreamFrameNotForwarded(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	for _, tc := range []struct {
		name    string
		format  types.RelayFormat
		payload string
	}{
		{"openai", types.RelayFormatOpenAI, `{"error":{"message":"original upstream secret"},"usage":"invalid"}`},
		{"claude", types.RelayFormatClaude, `{"type":"error","error":{"message":"original upstream secret"},"usage":"invalid"}`},
		{"gemini", types.RelayFormatGemini, `{"error":{"code":500,"message":"original upstream secret"},"usage":"invalid"}`},
		{"responses", types.RelayFormatOpenAIResponses, `{"type":"response.failed","response":{"error":{"message":"original upstream secret"}},"usage":"invalid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/fixture", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: tc.format, FinalRequestRelayFormat: tc.format, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
			require.Error(t, info.StreamSession.ObserveEvent("", []byte(tc.payload)))
			FinalizeStreamUsage(c, info, nil)
			body := rec.Body.String()
			assert.NotContains(t, body, "original upstream secret")
			assert.Contains(t, body, "服务暂时不可用")
		})
	}
}

// A panic while deciding must send the original error. Before, the unnamed
// results came back as zero values: status 0 goes out as HTTP 200 with an
// empty message.
func TestPresentLocalRelayAbort_PanicKeepsOriginal(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true})
	previous := decideRelayError
	decideRelayError = func(operation_setting.RelayErrorInput) operation_setting.RelayErrorDecision { panic("boom") }
	t.Cleanup(func() { decideRelayError = previous })

	status, msg, code := PresentLocalRelayAbort(errorDisplayCtx(), true, http.StatusServiceUnavailable, "no channel", "model_not_found")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "no channel", msg)
	assert.Equal(t, "model_not_found", code)
}
