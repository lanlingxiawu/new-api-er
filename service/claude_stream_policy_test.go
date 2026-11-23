package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudePolicyKeepsOtherProtocolSessions(t *testing.T) {
	for _, format := range []types.RelayFormat{
		types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini,
		types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction,
		types.RelayFormatOpenAIAlphaSearch, types.RelayFormatDeepSeekTools,
		types.RelayFormatOpenAIAudio, types.RelayFormatOpenAIImage, types.RelayFormatOpenAIRealtime,
		types.RelayFormatRerank, types.RelayFormatEmbedding, types.RelayFormatTask, types.RelayFormatMjProxy,
	} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/claude_validation=%t", format, enabled), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/local-protocol-fixture", nil)
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{ClaudeStreamValidation: &enabled})
				info := &relaycommon.RelayInfo{RelayFormat: format, IsStream: true}
				BeginStreamAttempt(c, info)
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(""))}
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				defer resp.Body.Close()
				require.True(t, info.StreamSession.Active(), "configuration alone must not disable a non-Claude upstream")
				require.False(t, info.StreamSession.RequireClaudeProtocol, "only the actual Claude handler may require its schema")
				require.False(t, info.ClaudeStreamPolicyApplied)
				require.False(t, info.ClaudeStreamMainMode)
				info.StreamSession.Fail("upstream_read_error", io.ErrUnexpectedEOF)
				usage := FinalizeStreamUsage(c, info, nil)
				require.NotNil(t, usage)
				require.Zero(t, usage.TotalTokens)
				require.True(t, info.StreamResult.Failed)
				require.False(t, info.StreamResult.Diagnostic.UsageFinal)
			})
		}
	}
}

func TestClaudeStreamPolicyRefreshesFromAttemptChannel(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{ClaudeStreamValidation: common.GetPointer(true)}}}
	for _, value := range []bool{false, true} {
		common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{ClaudeStreamValidation: &value})
		BeginStreamAttempt(c, info)
		require.Equal(t, value, info.ClaudeStreamValidationEnabled())
		require.Equal(t, "channel", info.ClaudeStreamValidationSource)
	}
}

func TestClaudeMainDiagnosticCannotChangeUsage(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude}
	BeginStreamAttempt(c, info)
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("private incomplete stream"))}
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamSession.Disable()
	info.StreamDiagnostic.Observe(resp)
	_, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	info.ClaudeStreamMainMode, info.ClaudeStreamPolicyApplied = true, true
	info.ClaudeStreamValidationSource = "user"
	info.StreamStatus.RequireTerminal()
	usage := &dto.Usage{PromptTokens: 2, CompletionTokens: 32000}
	require.Same(t, usage, FinalizeStreamUsage(c, info, usage))
	other := model.NewLogOther()
	AppendStreamLogInfo(info, other)
	require.Equal(t, false, logOtherAdmin(other)["claude_stream_validation"])
	require.Equal(t, "user", logOtherAdmin(other)["claude_stream_validation_source"])
	require.NotEqual(t, true, other.Snapshot()["stream_diagnostic_available"], "unmeasured completion must not manufacture an error")
	require.Nil(t, info.StreamResult)
	require.Equal(t, 32000, usage.CompletionTokens)
}
