package service

import (
	"context"
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

func TestStreamPolicyDisabledPreservesWireUsageAndCapture(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini, types.RelayFormatOpenAIResponses} {
		t.Run(string(format), func(t *testing.T) {
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest("POST", "/fixture", nil)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{StreamProtocolValidation: common.GetPointer(false)})
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: format}
			BeginStreamAttempt(c, info)
			wire := "data: {invalid}\n\ndata: [DONE]\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, wire, string(body))
			_, err = c.Writer.Write(body)
			require.NoError(t, err)
			c.Writer.Flush()
			require.Equal(t, wire, r.Body.String())
			usage := &dto.Usage{PromptTokens: 2, CompletionTokens: 32000}
			require.Same(t, usage, FinalizeStreamUsage(c, info, usage))
			require.Nil(t, info.StreamResult)
			other := model.NewLogOther()
			AppendStreamLogInfo(info, other)
			require.NotEqual(t, true, other.Snapshot()["stream_diagnostic_available"], "unknown completion is not an error")
			require.NotNil(t, info.StreamDiagnostic)
		})
	}
}

func TestRelayRetryStopsAfterResponseCommitOrCancellation(t *testing.T) {
	for _, phase := range []string{"body", "headers", "cancelled", "before_response"} {
		t.Run(phase, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/fixture", nil)
			switch phase {
			case "body":
				_, _ = c.Writer.Write([]byte("partial"))
			case "headers":
				c.Writer.WriteHeaderNow()
			case "cancelled":
				ctx, cancel := context.WithCancel(c.Request.Context())
				c.Request = c.Request.WithContext(ctx)
				cancel()
			}
			err := types.NewErrorWithStatusCode(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse, 503)
			decision := DecideRelayRetry(c, err, 1)
			if phase == "before_response" {
				require.Equal(t, "retry", decision.Action)
			} else {
				require.Equal(t, "stop", decision.Action)
			}
		})
	}
}

func TestStreamPolicyMainFallbackAndErrorEvidence(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/fixture", nil)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{StreamProtocolValidation: common.GetPointer(false)})
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	usage := ResponseText2UsageFromStream(c, "", "gpt-4o", 123, 0)
	require.Equal(t, 123, usage.PromptTokens, "disabled mode retains main's missing-usage estimate")
	info.StreamSession.Fail("upstream_json_error", io.ErrUnexpectedEOF)
	require.True(t, info.StreamStatus.OutcomeSnapshot().HasErrors)
	other := model.NewLogOther()
	AppendStreamLogInfo(info, other)
	require.Equal(t, true, other.Snapshot()["stream_diagnostic_available"])
	raw, err := common.Marshal(other.Snapshot()["stream_diagnostic"])
	require.NoError(t, err)
	require.Contains(t, string(raw), "unexpected EOF")
	require.NotContains(t, string(raw), "protocol_complete")
	require.NotContains(t, string(raw), "usage_final")
	require.Nil(t, info.StreamResult)
}

func TestStreamPolicyNonStreamAndUnknownLog(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/fixture", nil)
		info := &relaycommon.RelayInfo{IsStream: streaming, RelayFormat: types.RelayFormatOpenAI, UserSetting: dto.UserSetting{StreamProtocolValidation: common.GetPointer(false)}}
		BeginStreamAttempt(c, info)
		require.Equal(t, streaming, info.MainStreamMode())
		require.Nil(t, info.StreamWriter)
		if streaming {
			other := model.NewLogOther()
			appendStreamStatus(info, other)
			status := other.Snapshot()["stream_status"].(map[string]any)
			require.Equal(t, "ok", status["status"])
			require.NotContains(t, status, "end_reason")
		}
	}
}

func TestLegacyClaudeEnabledReleasesPassiveErrorOwnership(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{StreamProtocolValidation: common.GetPointer(false)})
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude,
		UserSetting: dto.UserSetting{ClaudeStreamValidation: common.GetPointer(true)}}
	BeginStreamAttempt(c, info)
	require.NotNil(t, info.StreamDiagnostic.PassiveStatus)
	info.ApplyClaudeStreamPolicy()
	require.True(t, info.StreamSession.ValidationEnabled())
	require.Nil(t, info.StreamDiagnostic.PassiveStatus, "strict writer owns errors after the legacy Claude override applies")
}

func TestStreamPolicyClientErrorDoesNotExpandRawRetention(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/fixture", nil)
	info := &relaycommon.RelayInfo{IsStream: true, UserSetting: dto.UserSetting{StreamProtocolValidation: common.GetPointer(false)}}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	info.StreamDiagnostic.WriteStreamPayload([]byte("private sample"))
	info.StreamDiagnostic.Termination.Observe("downstream", "write", io.ErrClosedPipe)
	info.StreamDiagnostic.SetError(io.ErrClosedPipe)
	info.StreamFinalUsage = &dto.Usage{PromptTokens: 2, CompletionTokens: 3}
	other := model.NewLogOther()
	AppendStreamLogInfo(info, other)
	require.NotEqual(t, true, other.Snapshot()["stream_diagnostic_available"])
	raw, err := common.Marshal(other.Snapshot()["stream_diagnostic"])
	require.NoError(t, err)
	require.Contains(t, string(raw), "closed pipe")
	require.Nil(t, other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic).BodyHead)
}

func TestRealtimeMainSettlementOnlyOnce(t *testing.T) {
	const uid, tid, chid = 873205, 873205, 873275
	seedUser(t, uid, 1_000_000)
	seedToken(t, tid, uid, "protocol-wss-token", 1_000_000)
	seedChannel(t, chid)
	info := finalizeRelayInfo(uid, tid, chid, "protocol-wss-token")
	info.IsStream = true
	info.RelayFormat = types.RelayFormatOpenAIRealtime
	info.UserSetting.StreamProtocolValidation = common.GetPointer(false)
	info.FreezeStreamValidation(dto.ChannelSettings{})
	info.ForcePreConsume = true
	c := finalizeCtx(t)
	c.Request = httptest.NewRequest("GET", "/v1/realtime", nil)
	billing, apiErr := NewBillingSession(c, info, 10000)
	require.Nil(t, apiErr)
	info.Billing = billing
	usage := &dto.RealtimeUsage{InputTokens: 1000, OutputTokens: 500, TotalTokens: 1500, InputTokenDetails: dto.InputTokenDetails{TextTokens: 1000}, OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 500}}
	require.NoError(t, PreWssConsumeQuota(c, info, usage))
	roundDebit := 1_000_000 - 10000 - getUserQuota(t, uid)
	require.Positive(t, roundDebit)
	PostWssConsumeQuota(c, info, "gpt-4o-realtime", usage, nil)
	PostWssConsumeQuota(c, info, "gpt-4o-realtime", usage, nil)
	drainRelayLogs()
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ?", uid).Find(&logs).Error)
	t.Cleanup(func() { model.LOG_DB.Where("user_id = ?", uid).Delete(&model.Log{}) })
	require.Len(t, logs, 1)
	require.Positive(t, logs[0].Quota)
	require.Equal(t, 1_000_000-roundDebit-logs[0].Quota, getUserQuota(t, uid))
	var token model.Token
	require.NoError(t, model.DB.First(&token, tid).Error)
	require.Equal(t, 1_000_000-roundDebit-logs[0].Quota, token.RemainQuota)
	require.Nil(t, info.StreamResult)
}
