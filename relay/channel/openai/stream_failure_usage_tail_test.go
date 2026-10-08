package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 回归：relay-control D-2。流式失败时，错误帧前多发一条合成的 include_usage 尾帧（id:""、created:0、
// 带内部字段，prompt_tokens 与实际结算不符）。失败流的最后一帧必须是终止错误帧；成功流照常补发用量帧。
func TestStreamFailureDoesNotEmitSyntheticUsageTail(t *testing.T) {
	role := "data: {\"id\":\"chatcmpl-test\",\"created\":1,\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n"
	text := "data: {\"id\":\"chatcmpl-test\",\"created\":1,\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"
	stop := "data: {\"id\":\"chatcmpl-test\",\"created\":1,\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	upstreamError := "event: error\ndata: {\"error\":{\"message\":\"upstream-provided\",\"type\":\"server_error\"}}\n\n"
	for _, tc := range []struct {
		name, body string
		failed     bool
	}{
		{"error as first frame", upstreamError, true},
		{"role frame then EOF", role, true},
		{"content then EOF", role + text, true},
		{"content then upstream error", role + text + upstreamError, true},
		{"normal without upstream usage", role + text + stop, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ShouldIncludeUsage: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, FinalRequestRelayFormat: types.RelayFormatOpenAI}
			info.SetEstimatePromptTokens(12)
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, err := OaiStreamHandler(c, info, resp)
			require.Nil(t, err)
			service.FinalizeStreamUsage(c, info, usage)
			body := rec.Body.String()
			require.Equal(t, tc.failed, info.StreamResult.Failed)
			if tc.failed {
				require.NotContains(t, body, `"choices":[]`, "no usage tail on a failed stream")
				require.NotContains(t, body, `"created":0`)
				require.NotContains(t, body, "claude_cache_creation")
				require.NotContains(t, body, "[DONE]")
				frames := strings.Split(strings.TrimSpace(body), "\n\n")
				require.Contains(t, frames[len(frames)-1], `"error"`, "the terminal error frame must be the last frame")
				return
			}
			require.Contains(t, body, `"choices":[]`, "a successful stream still gets its include_usage frame")
			require.Contains(t, body, "[DONE]")
		})
	}
}

// usageTailTimeoutControl 模拟已到期的请求级超时控制器（总时长）。
type usageTailTimeoutControl struct{}

func (usageTailTimeoutControl) RelayTimeoutKind() string        { return "total_timeout" }
func (usageTailTimeoutControl) WriteTerminalError(write func()) { write() }

// TestStreamOwnTimeoutDoesNotEmitSyntheticUsageTail 我方时限切断的流同样不补发用量尾帧，
// 最后一帧是 relay_timeout 终止错误帧。
func TestStreamOwnTimeoutDoesNotEmitSyntheticUsageTail(t *testing.T) {
	text := "data: {\"id\":\"chatcmpl-test\",\"created\":1,\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ShouldIncludeUsage: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, FinalRequestRelayFormat: types.RelayFormatOpenAI}
	info.SetEstimatePromptTokens(12)
	service.BeginStreamAttempt(c, info)
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(text))}
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	info.StreamSession.ObserveHTTP(resp)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, usageTailTimeoutControl{})
	cancel() // 我方时限到期：控制器取消请求 context
	usage, err := OaiStreamHandler(c, info, resp)
	require.Nil(t, err)
	service.FinalizeStreamUsage(c, info, usage)
	body := rec.Body.String()
	require.NotContains(t, body, `"choices":[]`)
	frames := strings.Split(strings.TrimSpace(body), "\n\n")
	require.Contains(t, frames[len(frames)-1], `"code":"relay_timeout"`)
}
