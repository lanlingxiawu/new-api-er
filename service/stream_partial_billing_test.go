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
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 回归：测试报告 openai-format D-3（DC1/DC3）、claude-format O1/O8（S01/S13/X01）、relay-control D-3。

// partialBillingInfo 构造一次已取得上游 200 的受管流式尝试。
func partialBillingInfo(t *testing.T, format types.RelayFormat) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, context.CancelFunc) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	info.SetEstimatePromptTokens(23)
	info.PriceData.ModelRatio = 1
	info.PriceData.CompletionRatio = 1
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	return c, recorder, info, cancel
}

// deliver 让一帧既被接收（接收侧估算）又成功交付（有效内容与交付侧估算）。
func deliver(t *testing.T, info *relaycommon.RelayInfo, frame string, estimated int) {
	t.Helper()
	require.NoError(t, info.StreamSession.ObserveEvent("", []byte(frame)))
	info.StreamSession.CommitDelivery([]byte(frame))
	info.StreamSession.AddEstimatedOutput(estimated)
}

// TestStreamClientGoneBeforeFirstByteSettlesPromptEstimate DC1/DC3：上游已返回 200，客户端在首个业务帧前断开。
// 修复前按 none 释放预扣（收 0），而上游（nexaxis）按 19/44 计费；现在按估算输入结算，与主分支一致。
func TestStreamClientGoneBeforeFirstByteSettlesPromptEstimate(t *testing.T) {
	c, _, info, cancel := partialBillingInfo(t, types.RelayFormatOpenAI)
	funding := &claudeSettlementFixture{}
	info.Billing = funding
	cancel()
	u := FinalizeStreamUsage(c, info, nil)
	require.True(t, info.StreamResult.ClientGone)
	require.False(t, info.StreamResult.EffectiveContent)
	require.Equal(t, "estimated", info.StreamResult.UsageSource)
	require.Equal(t, 23, u.PromptTokens)
	require.Zero(t, u.CompletionTokens)

	summary := calculateTextQuotaSummary(c, info, u)
	require.Positive(t, summary.Quota)
	params := ConsumptionSettlementParams{Quota: summary.Quota, LedgerQuota: summary.LedgerQuota, CountUsage: summary.hasBillableUsage()}
	require.True(t, settleStreamQuota(c, info, &params))
	require.Equal(t, "settled", info.StreamResult.SettlementState)
	require.Equal(t, summary.Quota, funding.actual)
}

// TestStreamRealtimeClientGoneBeforeAnyRoundIsFree Realtime 按轮次计费：握手后没开始任何轮次就断开不收费。
func TestStreamRealtimeClientGoneBeforeAnyRoundIsFree(t *testing.T) {
	c, _, info, cancel := partialBillingInfo(t, types.RelayFormatOpenAIRealtime)
	cancel()
	u := FinalizeStreamUsage(c, info, nil)
	require.Equal(t, "none", info.StreamResult.UsageSource)
	require.Zero(t, u.TotalTokens)
}

const (
	claudeStartOne = `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude","content":[],"usage":{"input_tokens":50,"output_tokens":1}}}`
	claudeBlock    = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	claudeDelta    = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The quick brown fox jumps over the lazy dog again and again and again."}}`
	claudeStop     = `{"type":"content_block_stop","index":0}`
)

// TestStreamAbnormalEndBillsMaxOfConfirmedAndEstimate S13/O8 与 OpenAI 路径的同类问题：异常结束时
// 输出取确认值与本地估算的较大者，但最后一段内容之后的上游计数照收；正常结束不改动。
func TestStreamAbnormalEndBillsMaxOfConfirmedAndEstimate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format types.RelayFormat
		run    func(t *testing.T, info *relaycommon.RelayInfo, cancel context.CancelFunc)
		source string
		input  int
		output func(snapshot relaycommon.StreamSnapshot) int
	}{
		{
			name: "claude upstream error after message_start only", format: types.RelayFormatOpenAI,
			run: func(t *testing.T, info *relaycommon.RelayInfo, _ context.CancelFunc) {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeStartOne)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeBlock)))
				deliver(t, info, claudeDelta, 40)
				info.StreamSession.Fail("upstream_error", io.ErrUnexpectedEOF)
			},
			source: "mixed", input: 50, output: func(relaycommon.StreamSnapshot) int { return 40 },
		},
		{
			name: "claude client gone after message_start only", format: types.RelayFormatOpenAI,
			run: func(t *testing.T, info *relaycommon.RelayInfo, cancel context.CancelFunc) {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeStartOne)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeBlock)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeDelta)))
				cancel()
			},
			source: "mixed", input: 50, output: func(s relaycommon.StreamSnapshot) int { return s.ReceivedOutput },
		},
		{
			name: "claude report after all content is kept", format: types.RelayFormatOpenAI,
			run: func(t *testing.T, info *relaycommon.RelayInfo, _ context.CancelFunc) {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeStartOne)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeBlock)))
				deliver(t, info, claudeDelta, 40)
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeStop)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`)))
				info.StreamSession.EndRead(io.EOF)
			},
			source: "upstream", input: 50, output: func(relaycommon.StreamSnapshot) int { return 3 },
		},
		{
			name: "openai stale per-chunk usage", format: types.RelayFormatOpenAI,
			run: func(t *testing.T, info *relaycommon.RelayInfo, _ context.CancelFunc) {
				deliver(t, info, `{"choices":[{"index":0,"delta":{"content":"a"}}],"usage":{"prompt_tokens":12,"completion_tokens":1}}`, 1)
				deliver(t, info, `{"choices":[{"index":0,"delta":{"content":"more text arriving after the last usage report"}}]}`, 29)
				info.StreamSession.Fail("upstream_error", io.ErrUnexpectedEOF)
			},
			source: "mixed", input: 12, output: func(relaycommon.StreamSnapshot) int { return 30 },
		},
		{
			name: "missing input field is estimated", format: types.RelayFormatOpenAI,
			run: func(t *testing.T, info *relaycommon.RelayInfo, _ context.CancelFunc) {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"completion_tokens":2}}`)))
				deliver(t, info, `{"choices":[{"index":0,"delta":{"content":"partial answer"}}]}`, 9)
				info.StreamSession.Fail("upstream_error", io.ErrUnexpectedEOF)
			},
			source: "mixed", input: 23, output: func(relaycommon.StreamSnapshot) int { return 9 },
		},
		{
			name: "claude missing input fills the nested billing object", format: types.RelayFormatOpenAI,
			run: func(t *testing.T, info *relaycommon.RelayInfo, _ context.CancelFunc) {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude","content":[]}}`)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeBlock)))
				deliver(t, info, claudeDelta, 2)
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeStop)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`)))
				info.StreamSession.EndRead(io.EOF)
			},
			source: "upstream", input: 23, output: func(relaycommon.StreamSnapshot) int { return 2 },
		},
		{
			name: "complete stream trusts the final count", format: types.RelayFormatOpenAI,
			run: func(t *testing.T, info *relaycommon.RelayInfo, _ context.CancelFunc) {
				deliver(t, info, `{"choices":[{"index":0,"delta":{"content":"a long answer"}}]}`, 30)
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3}}`)))
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte("[DONE]")))
			},
			source: "upstream", input: 12, output: func(relaycommon.StreamSnapshot) int { return 3 },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, info, cancel := partialBillingInfo(t, tc.format)
			tc.run(t, info, cancel)
			snapshot := info.StreamSession.Snapshot()
			u := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 12, CompletionTokens: 3})
			want := tc.output(snapshot)
			require.Greater(t, want, 0)
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.Equal(t, tc.input, u.PromptTokens)
			require.Equal(t, want, u.CompletionTokens)
			billed := effectiveBillingUsage(u)
			require.Equal(t, want, billed.CompletionTokens, "the usage billing reads must carry the same output")
			require.Equal(t, tc.input, billed.PromptTokens)
		})
	}
}

// TestStreamOwnTimeoutTerminalFrame relay-control D-3：我方流式总时长到期时，终止帧的错误码与文案
// 说明是我方超时（与消费日志、非流式 504 一致），不说成上游流式失败；各下游协议分别检查。
func TestStreamOwnTimeoutTerminalFrame(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, tc := range []struct {
		format types.RelayFormat
		want   []string
	}{
		{types.RelayFormatOpenAI, []string{`"code":"relay_timeout"`}},
		{types.RelayFormatOpenAIResponses, []string{`"code":"relay_timeout"`}},
		{types.RelayFormatClaude, []string{`"type":"timeout_error"`}},
		{types.RelayFormatGemini, []string{`"code":504`, `"status":"DEADLINE_EXCEEDED"`}},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			control := &streamTerminalControl{}
			w := &terminalDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), control: control}
			c, _ := gin.CreateTestContext(w)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: tc.format, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
			common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, 4)
			cancel()
			FinalizeStreamUsage(c, info, nil)
			body := w.Body.String()
			require.Contains(t, body, "event: error")
			for _, want := range tc.want {
				require.Contains(t, body, want)
			}
			require.Contains(t, body, "The request exceeded the configured 4 second time limit.")
			require.NotContains(t, body, "upstream_stream_error")
			require.NotContains(t, body, "Upstream stream failed")
		})
	}
}

// TestStreamUpstreamFailureTerminalFrameUnchanged 上游导致的失败仍是 upstream_stream_error 与通用文案。
func TestStreamUpstreamFailureTerminalFrameUnchanged(t *testing.T) {
	require.NoError(t, i18n.Init())
	c, recorder, info, _ := partialBillingInfo(t, types.RelayFormatOpenAI)
	info.StreamSession.Fail("upstream_read_error", io.ErrUnexpectedEOF)
	FinalizeStreamUsage(c, info, nil)
	body := recorder.Body.String()
	require.Contains(t, body, `"code":"upstream_stream_error"`)
	require.True(t, strings.Contains(body, "Upstream stream failed"), body)
	require.NotContains(t, body, "relay_timeout")
}

// TestStreamOwnTimeoutWithoutDeliveryFollowsTimeoutBilling 我方时限切断、一段内容都没交付的流：
// 超时计费方式为 charge 的用户至少按主分支口径收（有确认用量用确认用量，否则估算输入），
// refund（默认）用户保持不收费。
func TestStreamOwnTimeoutWithoutDeliveryFollowsTimeoutBilling(t *testing.T) {
	for _, tc := range []struct {
		name, billing string
		confirmed     bool
		source        string
		input         int
	}{
		{"charge with message_start usage", NonStreamTimeoutBillingCharge, true, "upstream", 50},
		{"charge without usage", NonStreamTimeoutBillingCharge, false, "estimated", 23},
		{"refund with message_start usage", NonStreamTimeoutBillingRefund, true, "none", 0},
		{"default without usage", "", false, "none", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, info, cancel := partialBillingInfo(t, types.RelayFormatOpenAI)
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, tc.billing)
			if tc.confirmed {
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(claudeStartOne)))
			}
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &streamTerminalControl{})
			cancel()
			u := FinalizeStreamUsage(c, info, nil)
			require.False(t, info.StreamResult.EffectiveContent)
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.Equal(t, tc.input, effectiveBillingUsage(u).PromptTokens)
		})
	}
}
