package claude

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// 设计 relay-timeout-cost-bearing.md §3.2：原生 Claude 流被我方时限在首个有效内容前切断时，
// input 档只收确认输入（没有确认输入时收估算输入），输出为 0——message_start 的 output_tokens=1
// 与已接收未交付的内容都不收；按次计费模型在 input 档退款。
func TestStrictOwnTimeoutInputOnly(t *testing.T) {
	// 已接收、但因我方时限先到而没有交付的一段内容（写出失败前被切断）。
	startNoInput := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"output_tokens\":1}}}\n\n"
	for _, tc := range []struct {
		name, frames, source string
		perCall              bool
		input                int
	}{
		{"confirmed input", strictStartOne, "upstream", false, 100},
		{"missing input is estimated", startNoInput, "upstream", false, 50},
		{"nothing confirmed", "", "estimated", false, 50},
		{"per call refunds", strictStartOne, "none", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = writer.Close() })
			c, _, resp, info := strictTestContext(reader)
			if tc.perCall {
				info.PriceData.UsePrice = true
				info.PriceData.ModelPrice = 0.01
			}
			ctx, cancel := context.WithCancel(c.Request.Context())
			c.Request = c.Request.WithContext(ctx)
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, service.NonStreamTimeoutBillingInput)
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &strictTimeoutControl{})
			go func() {
				_, _ = io.WriteString(writer, tc.frames)
				time.Sleep(50 * time.Millisecond)
				cancel() // 我方时限到期
			}()
			usage, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.Equal(t, "timeout", string(info.StreamStatus.EndReason))
			require.False(t, info.StreamResult.EffectiveContent)
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.Equal(t, tc.input, usage.PromptTokens)
			require.Zero(t, usage.CompletionTokens, "output is never charged in the input mode")
			if usage.BillingUsage != nil && usage.BillingUsage.ClaudeUsage != nil {
				require.Equal(t, tc.input, usage.BillingUsage.ClaudeUsage.InputTokens, "the nested Claude usage is what billing reads")
				require.Zero(t, usage.BillingUsage.ClaudeUsage.OutputTokens)
			}
		})
	}
}
