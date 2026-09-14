package dify

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// difyTerminalFailureWriter 仅在最终 DONE 注入故障，先前正文仍正常交付，不操作真实资金。
type difyTerminalFailureWriter struct {
	gin.ResponseWriter        // Gin 原写入器，保留正常响应状态及刷新行为。
	failure            string // write/flush 分别注入尾帧写失败和尾帧刷新失败。
	tail               bool   // 已尝试写出 DONE，供刷新阶段定位故障时机。
}

// Write 写出数据 p；只有最终 DONE 命中 write 模式才返回测试错误。
func (w *difyTerminalFailureWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("[DONE]")) {
		w.tail = true
		if w.failure == "write" {
			return 0, errors.New("fixture terminal write failed")
		}
	}
	return w.ResponseWriter.Write(p)
}

// FlushError 在尾帧 flush 模式注入错误；其他刷新正常执行，无参数。
func (w *difyTerminalFailureWriter) FlushError() error {
	if w.tail && w.failure == "flush" {
		return errors.New("fixture terminal flush failed")
	}
	w.ResponseWriter.Flush()
	return nil
}

// TestUnifiedDifyUsageSettlement 使用本地 HTTP fixture 验证原始用量到统一结算选择；t 不连接外部 AI 或资金库。
func TestUnifiedDifyUsageSettlement(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, failure, source string // 用例、上游 metadata、下游故障和期望收费来源。
		content, upstreamBad            bool   // 是否交付正文、用量是否非法。
		input, output                   int    // 非估算分支的预期结算 token。
	}{
		{"normal", `{"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`, "", "upstream", true, false, 12, 3},
		{"tail write", `{"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`, "write", "upstream", true, false, 12, 3},
		{"tail flush", `{"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`, "flush", "upstream", true, false, 12, 3},
		{"client gone without content", `{"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`, "write", "upstream", false, false, 12, 3},
		{"confirmed zero", `{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, "write", "upstream", true, false, 0, 0},
		{"missing usage", `{}`, "write", "none", true, false, 0, 0},
		{"invalid with content", `{"usage":{"prompt_tokens":12,"completion_tokens":-1}}`, "", "estimated", true, true, 0, 0},
		{"invalid without content", `{"usage":{"prompt_tokens":12,"completion_tokens":-1}}`, "", "none", false, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := ""
			if tc.content {
				body = "data: {\"event\":\"message\",\"answer\":\"hello world\"}\n\n"
			}
			body += "data: {\"event\":\"message_end\",\"metadata\":" + tc.metadata + "}\n\n"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(body))
			}))
			defer upstream.Close()
			resp, err := upstream.Client().Get(upstream.URL)
			require.NoError(t, err)
			defer resp.Body.Close()

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Writer = &difyTerminalFailureWriter{ResponseWriter: c.Writer, failure: tc.failure}
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeDify)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			info.SetEstimatePromptTokens(7)
			service.BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, apiErr := difyStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			selected := service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.Equal(t, tc.content, info.StreamResult.EffectiveContent)
			require.Equal(t, tc.failure != "", info.StreamResult.ClientGone)
			require.Equal(t, tc.failure != "" || tc.upstreamBad, info.StreamResult.Failed)
			require.Equal(t, tc.upstreamBad, info.StreamResult.DiagnosticAvailable)
			if tc.source == "estimated" {
				require.Equal(t, 7, selected.PromptTokens)
				require.Positive(t, selected.CompletionTokens)
			} else {
				require.Equal(t, tc.input, selected.PromptTokens)
				require.Equal(t, tc.output, selected.CompletionTokens)
			}
			if tc.upstreamBad {
				require.Contains(t, rec.Body.String(), "upstream_stream_error")
				require.NotContains(t, rec.Body.String(), "[DONE]")
			}
		})
	}
}
