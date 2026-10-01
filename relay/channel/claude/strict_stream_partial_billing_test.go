package claude

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// 回归：测试报告 claude-format S01/X01/O1 与 openai-format DC1/DC3。异常结束（上游中途报错、
// 用户断开）时只按 message_start 的 output_tokens（Anthropic 恒为 1）收费，已交付/已接收的
// 大段内容被低计；首字节前断开则完全不收费，而上游已按输入计费。主分支在两种情况下都按
// max(确认值, 本地估算) 与估算输入收费。

// strictStartOne 是真实 Anthropic 的 message_start：输出计数从 1 开始。
const strictStartOne = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n"

const strictBlockStart = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"

// strictDeltas 构造 n 个文本增量帧，并返回它们拼接后的全文。
func strictDeltas(t *testing.T, n int) (string, string) {
	var frames, text strings.Builder
	for i := 0; i < n; i++ {
		piece := "The quick brown fox jumps over the lazy dog number " + strings.Repeat("x", i%3) + ". "
		payload, err := common.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": piece}})
		require.NoError(t, err)
		frames.WriteString("event: content_block_delta\ndata: " + string(payload) + "\n\n")
		text.WriteString(piece)
	}
	return frames.String(), text.String()
}

// TestStrictMidStreamErrorBillsDeliveredOverMessageStart S01：五个增量后上游 event:error。
// 输出按已交付内容的估算计（远大于 message_start 的 1），输入保留上游确认值 100。
func TestStrictMidStreamErrorBillsDeliveredOverMessageStart(t *testing.T) {
	deltas, text := strictDeltas(t, 5)
	upstreamError := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStartOne + strictBlockStart + deltas + upstreamError)))
	usage, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.Contains(t, w.Body.String(), "overloaded_error")
	want := service.EstimateTokenByModel("claude", text)
	require.Greater(t, want, 1)
	require.Equal(t, "mixed", info.StreamResult.UsageSource)
	require.Equal(t, 100, usage.PromptTokens)
	require.Equal(t, want, usage.CompletionTokens)
	require.Equal(t, want, usage.BillingUsage.ClaudeUsage.OutputTokens, "the nested Claude usage is what billing reads")
	require.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
	require.True(t, usage.BillingUsage.Estimated)
}

// TestStrictClientGoneBillsReceivedOverMessageStart X01：客户端在第 4 个增量写出时断开。
// 输出按已接收内容估算（主分支 ResponseText 口径），不再停在 message_start 的 1。
func TestStrictClientGoneBillsReceivedOverMessageStart(t *testing.T) {
	deltas, _ := strictDeltas(t, 6)
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStartOne + strictBlockStart + deltas + strictStop)))
	c.Writer = &strictFailWriter{ResponseWriter: c.Writer, failOn: "number xx.", short: true}
	usage, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.True(t, info.StreamResult.ClientGone)
	require.Equal(t, 100, usage.PromptTokens)
	require.Greater(t, usage.CompletionTokens, 1)
	require.Equal(t, "mixed", info.StreamResult.UsageSource)
	require.Equal(t, usage.CompletionTokens, info.StreamResult.Diagnostic.EstimatedUsage["output_tokens"])
}

// TestStrictCurrentReportIsKeptWhenStreamBreaks 最后一段内容之后的 message_delta 输出计数是上游对全部
// 内容的计量：流随后没有 message_stop 就断开，也照收该计数，即使它低于本地估算。
func TestStrictCurrentReportIsKeptWhenStreamBreaks(t *testing.T) {
	deltas, text := strictDeltas(t, 5)
	tail := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n"
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStartOne + strictBlockStart + deltas + tail)))
	usage, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.Equal(t, "upstream_incomplete", string(info.StreamStatus.EndReason))
	require.Greater(t, service.EstimateTokenByModel("claude", text), 3)
	require.Equal(t, "upstream", info.StreamResult.UsageSource)
	require.Equal(t, 3, usage.CompletionTokens)
}

// TestStrictCompleteStreamTrustsConfirmedOutput 正常结束时上游的最终输出是完整计量，
// 即使低于本地估算也不上调（估算只补异常结束的中途值）。
func TestStrictCompleteStreamTrustsConfirmedOutput(t *testing.T) {
	deltas, text := strictDeltas(t, 5)
	stop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStartOne + strictBlockStart + deltas + stop)))
	usage, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.Greater(t, service.EstimateTokenByModel("claude", text), 3)
	require.Equal(t, "upstream", info.StreamResult.UsageSource)
	require.Equal(t, 3, usage.CompletionTokens)
}

// TestStrictClientGoneBeforeFirstByteBillsPromptEstimate DC1/DC3：上游已返回 200、一帧未读客户端即断开。
// 上游已按输入计费，按估算输入收费而不是释放预扣。
func TestStrictClientGoneBeforeFirstByteBillsPromptEstimate(t *testing.T) {
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStartOne + strictBlockStart)))
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	usage, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.Empty(t, w.Body.String())
	require.True(t, info.StreamResult.ClientGone)
	require.Equal(t, "estimated", info.StreamResult.UsageSource)
	require.Equal(t, 50, usage.PromptTokens)
	require.Zero(t, usage.CompletionTokens)
	require.Equal(t, 50, usage.BillingUsage.ClaudeUsage.InputTokens)
}

// TestStrictOwnTimeoutTerminalFrameSaysTimeout 中继控制 D-3：我方总时长切断流时，终止帧说明是超时
// （timeout_error 与带秒数的超时文案），不说成上游失败。
func TestStrictOwnTimeoutTerminalFrameSaysTimeout(t *testing.T) {
	require.NoError(t, i18n.Init())
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader("")))
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &strictTimeoutControl{})
	common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, 4)
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	body := w.Body.String()
	require.Contains(t, body, `"type":"timeout_error"`)
	require.Contains(t, body, "The request exceeded the configured 4 second time limit.")
	require.NotContains(t, body, "Upstream stream failed")
}

// TestStrictOwnTimeoutWithoutDeliveryFollowsTimeoutBilling 我方时限在首个内容前切断原生 Claude 流：
// charge 用户按 message_start 的确认输入收费，refund 用户不收费。
func TestStrictOwnTimeoutWithoutDeliveryFollowsTimeoutBilling(t *testing.T) {
	for _, tc := range []struct {
		billing, source string
		input           int
	}{
		{service.NonStreamTimeoutBillingCharge, "upstream", 100},
		{service.NonStreamTimeoutBillingRefund, "none", 0},
	} {
		t.Run(tc.billing, func(t *testing.T) {
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = writer.Close() })
			c, _, resp, info := strictTestContext(reader)
			ctx, cancel := context.WithCancel(c.Request.Context())
			c.Request = c.Request.WithContext(ctx)
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, tc.billing)
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &strictTimeoutControl{})
			go func() {
				_, _ = io.WriteString(writer, strictStartOne)
				time.Sleep(50 * time.Millisecond)
				cancel() // 我方时限到期
			}()
			usage, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.Equal(t, "timeout", string(info.StreamStatus.EndReason))
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.Equal(t, tc.input, usage.PromptTokens)
		})
	}
}

// TestStrictAbnormalEndWithoutInputTokensEstimatesInput 审查 L1：原生 Claude 流异常结束、上游没报过
// input_tokens（只有输出计数）时，输入曾按 0 计；现与通用流一致按估算输入补齐，显式 0 保留。
func TestStrictAbnormalEndWithoutInputTokensEstimatesInput(t *testing.T) {
	for _, tc := range []struct {
		name, start string
		input       int
	}{
		{"missing input", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"output_tokens\":1}}}\n\n", 50},
		{"explicit zero input", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":1}}}\n\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deltas, _ := strictDeltas(t, 3)
			upstreamError := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
			c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(tc.start + strictBlockStart + deltas + upstreamError)))
			usage, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.Equal(t, tc.input, usage.PromptTokens)
			require.Equal(t, tc.input, usage.BillingUsage.ClaudeUsage.InputTokens, "the nested Claude usage is what billing reads")
		})
	}
}
