package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// strictStart 为合法消息起始夹具，确认输入 100/输出 0；strictText 提供 hello 文本但不结束内容块。
// strictStop 依次结束内容块、报告输出 7 和 end_turn、结束消息，供各测试按需删改以制造边界条件。
const strictStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n"
const strictText = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"
const strictStop = "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

// strictTestContext 构造本地严格流式测试上下文、响应记录器、上游响应和默认中转信息。
// 参数 body：模拟上游响应体，可为 nil 以覆盖缺失响应场景；四个返回值依次供调用、断言输出、输入响应及检查状态使用。
func strictTestContext(body io.ReadCloser) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude"}}
	info.SetEstimatePromptTokens(50)
	return c, w, &http.Response{StatusCode: 200, Body: body, Header: http.Header{"X-Test": []string{"one", "two"}}}, info
}

// TestStrictStreamTermination 覆盖完整结束、截断、只有起始/ping、JSON 错误和块顺序异常，核对终止原因、有效内容及计费来源。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictStreamTermination(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason, source string // 依次为场景名、上游 SSE、预期结束原因、预期计费来源。
		effective                  bool   // 是否预期成功交付有效内容。
		output                     int    // 预期输出 token 数。
	}{
		{"complete", strictStart + strictText + strictStop, "done", "upstream", true, 7},
		{"truncated with text", strictStart + strictText, "upstream_incomplete", "upstream", true, 0},
		{"start only", strictStart, "upstream_incomplete", "none", false, 0},
		{"ping only", "event: ping\ndata: {\"type\":\"ping\"}\n\n", "upstream_incomplete", "none", false, 0},
		{"invalid json", strictStart + "event: message_delta\ndata: {oops}\n\n", "upstream_json_error", "none", false, 0},
		{"missing block stop", strictStart + strictText + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "upstream_protocol_error", "upstream", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(tc.body)))
			usage, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
			require.Equal(t, tc.source, info.ClaudeStream.UsageSource)
			require.Equal(t, tc.effective, info.ClaudeStream.EffectiveContent)
			require.Equal(t, tc.output, usage.CompletionTokens)
			if tc.reason != "done" {
				require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
				require.NotContains(t, w.Body.String(), "end_turn")
				require.NotContains(t, w.Body.String(), "event: message_stop")
			}
		})
	}
}

// TestStrictUpstreamErrorVerbatim 验证上游 error 帧的 CRLF、扩展字段及原始字节被完整透传，且不重复发送。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictUpstreamErrorVerbatim(t *testing.T) {
	raw := "event: error\r\ndata: {\"type\":\"error\",\"extra\":42,\"error\":{\"type\":\"overloaded_error\",\"message\":\"original\"}}\r\n\r\n"
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + raw)))
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.True(t, strings.HasSuffix(w.Body.String(), raw))
	require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
	require.Equal(t, "none", info.ClaudeStream.UsageSource)
}

// TestStrictKnownEventSchemaRegression 验证已知事件缺数据/缺字段时终止，未知扩展和注释帧继续兼容且不污染用量。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictKnownEventSchemaRegression(t *testing.T) {
	end := strings.TrimPrefix(strictStop, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	for _, malformed := range []string{
		"event: content_block_start\ndata:\n\n",
		"event: message_start\ndata:   \n\n",
		"event: ping\ndata:\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
		strings.Split(strictText, "event: content_block_delta")[0] + "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{}}\n\n",
	} {
		c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + malformed + end)))
		u, err := strictClaudeStream(c, resp, info)
		require.Nil(t, err)
		require.Equal(t, "upstream_protocol_error", string(info.StreamStatus.EndReason), malformed)
		require.False(t, info.ClaudeStream.EffectiveContent)
		require.Equal(t, "none", info.ClaudeStream.UsageSource)
		require.Zero(t, u.TotalTokens)
		require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
		require.NotContains(t, w.Body.String(), "event: message_stop")
	}
	// 注释帧和未来扩展事件都不应作为有效内容或上游确认用量。
	prefix := ": keepalive\n\nevent: future_extension\n\nevent: future_extension\ndata: {\"type\":\"future_extension\",\"usage\":{\"output_tokens\":999}}\n\n"
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(prefix + strictStart + strictText + strictStop)))
	u, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.False(t, info.ClaudeStream.Failed)
	require.Equal(t, 7, u.CompletionTokens)
	require.True(t, strings.HasPrefix(w.Body.String(), prefix))
}

// TestStrictNormalPartialUsageRegression 覆盖正常结束缺少最终输出、起始零值、累计下限及显式最终零值，核对混合估算和缓存保留。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictNormalPartialUsageRegression(t *testing.T) {
	missingOutputStop := strings.Replace(strictStop, `,"usage":{"output_tokens":7}`, "", 1)
	for _, tc := range []struct {
		name, start, stop, source string // 场景名、消息起始帧、结束帧及预期用量来源。
		output                    int    // 预期最终计费输出 token 数。
	}{
		{"absent output", strings.Replace(strictStart, `,"output_tokens":0`, "", 1), missingOutputStop, "mixed", service.EstimateTokenByModel("claude", "hello")},
		{"initial zero", strictStart, missingOutputStop, "mixed", service.EstimateTokenByModel("claude", "hello")},
		{"initial cumulative floor", strings.Replace(strictStart, `"output_tokens":0`, `"output_tokens":99`, 1), missingOutputStop, "mixed", 99},
		{"terminal zero", strictStart, strings.Replace(strictStop, `"output_tokens":7`, `"output_tokens":0`, 1), "upstream", 0},
		{"terminal positive", strictStart, strictStop, "upstream", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := strings.Replace(tc.start, `"input_tokens":100`, `"input_tokens":100,"cache_read_input_tokens":200`, 1)
			c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(start + strictText + tc.stop)))
			u, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.False(t, info.ClaudeStream.Failed)
			require.Equal(t, tc.source, info.ClaudeStream.UsageSource)
			require.Equal(t, 100, u.PromptTokens)
			require.Equal(t, 200, u.PromptTokensDetails.CachedTokens)
			require.Equal(t, tc.output, u.CompletionTokens)
			require.Equal(t, tc.output, u.BillingUsage.ClaudeUsage.OutputTokens)
			require.Equal(t, tc.source == "mixed", u.BillingUsage.Estimated)
		})
	}
}

// TestStrictOutputUsageOrdering 覆盖用量报告与停止原因的不同顺序、新内容使旧报告失效及显式零值，并检查普通/全局/渠道透传路径。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictOutputUsageOrdering(t *testing.T) {
	const blockStop = "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	const outputReport = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":1}}\n\n"
	const stopReason = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"
	const messageStop = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	closedText := strictText + blockStop
	secondText := strings.ReplaceAll(closedText, `"index":0`, `"index":1`)
	zeroReport := strings.Replace(outputReport, `"output_tokens":1`, `"output_tokens":0`, 1)
	metadata := ": keepalive\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\nevent: future_extension\ndata: {\"type\":\"future_extension\"}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"input_tokens\":100}}\n\n"
	estimated := service.EstimateTokenByModel("claude", "hello")
	estimatedTwice := service.EstimateTokenByModel("claude", "hellohello")
	old := *model_setting.GetGlobalSettings()
	t.Cleanup(func() { model_setting.ReplaceGlobalSettings(old) })
	for _, mode := range []string{"normal", "global passthrough", "channel passthrough"} {
		t.Run(mode, func(t *testing.T) {
			settings := old
			settings.PassThroughRequestEnabled = mode == "global passthrough"
			model_setting.ReplaceGlobalSettings(settings)
			for _, tc := range []struct {
				name, events, source, reason string // 场景名、起始后事件序列、预期来源和结束原因。
				output, evidence             int    // output 为最终输出 token；evidence 为保留的上游报告值。
				final                        bool   // 是否应将最新输出报告视为完整最终用量。
			}{
				{"before stop", closedText + outputReport + stopReason + messageStop, "upstream", "done", 1, 1, true},
				{"zero before stop", closedText + zeroReport + stopReason + messageStop, "upstream", "done", 0, 0, true},
				{"metadata after report", closedText + outputReport + metadata + stopReason + messageStop, "upstream", "done", 1, 1, true},
				{"with stop", strictText + strings.Replace(strictStop, `"output_tokens":7`, `"output_tokens":1`, 1), "upstream", "done", 1, 1, true},
				{"after stop", closedText + stopReason + outputReport + messageStop, "upstream", "done", 1, 1, true},
				{"latest zero", closedText + outputReport + stopReason + zeroReport + messageStop, "upstream", "done", 0, 0, true},
				{"initial usage only", closedText + stopReason + messageStop, "mixed", "done", estimated, 0, false},
				{"report before content", outputReport + closedText + stopReason + messageStop, "mixed", "done", max(1, estimated), 1, false},
				{"new content after report", closedText + outputReport + secondText + stopReason + messageStop, "mixed", "done", max(1, estimatedTwice), 1, false},
				{"fresh report after new content", closedText + outputReport + secondText + zeroReport + stopReason + messageStop, "upstream", "done", 0, 0, true},
				{"missing message stop", closedText + outputReport + stopReason, "upstream", "upstream_incomplete", 1, 1, false},
				{"missing stop reason", closedText + outputReport + messageStop, "upstream", "upstream_protocol_error", 1, 1, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := strictStart + tc.events
					c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
					info.ChannelSetting.PassThroughBodyEnabled = mode == "channel passthrough"
					u, err := ClaudeStreamHandler(c, resp, info)
					require.Nil(t, err)
					require.NotNil(t, info.ClaudeStream, "passthrough must still use strict response handling")
					require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
					require.Equal(t, tc.source, info.ClaudeStream.UsageSource)
					require.Equal(t, tc.final, info.ClaudeStream.Diagnostic.UsageFinal)
					require.Equal(t, tc.evidence, info.ClaudeStream.Diagnostic.UsageEvidence["output_tokens"])
					require.Equal(t, tc.output, u.CompletionTokens)
					require.Equal(t, 100, u.PromptTokens)
					require.Equal(t, 100+tc.output, u.TotalTokens)
					require.Equal(t, tc.output, u.BillingUsage.ClaudeUsage.OutputTokens)
					require.Equal(t, tc.source == "mixed", u.BillingUsage.Estimated)
					if tc.source != "mixed" {
						require.Empty(t, info.ClaudeStream.Diagnostic.EstimatedUsage)
					}
					if tc.reason == "done" {
						require.NotContains(t, w.Body.String(), "event: error")
						if mode != "normal" {
							require.Equal(t, body, w.Body.String(), "passthrough response bytes must stay unchanged")
						}
					} else {
						require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
					}
				})
			}
		})
	}
}

// TestStrictMessageStartCoreSchema 验证消息起始核心字段错误在状态推进、交付和用量采用前被发现，合法可选字段及无 usage 情况继续通过。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictMessageStartCoreSchema(t *testing.T) {
	_, startData := claudeFramePayload([]byte(strictStart))
	message := gjson.Get(startData, "message").Raw
	end := strings.TrimPrefix(strictStop, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	old := *model_setting.GetGlobalSettings()
	t.Cleanup(func() { model_setting.ReplaceGlobalSettings(old) })
	for _, mode := range []string{"normal", "global passthrough", "channel passthrough"} {
		t.Run(mode, func(t *testing.T) {
			settings := old
			settings.PassThroughRequestEnabled = mode == "global passthrough"
			model_setting.ReplaceGlobalSettings(settings)
			for _, tc := range []struct {
				name, message string // 场景名与被测 message_start.message 原始 JSON。
			}{
				{"empty object", `{}`},
				{"null", `null`},
				{"usage only", `{"usage":{"input_tokens":100,"output_tokens":7}}`},
				{"missing id", strings.Replace(message, `"id":"m",`, "", 1)},
				{"empty id", strings.Replace(message, `"id":"m"`, `"id":""`, 1)},
				{"blank id", strings.Replace(message, `"id":"m"`, `"id":" "`, 1)},
				{"wrong id type", strings.Replace(message, `"id":"m"`, `"id":1`, 1)},
				{"missing model", strings.Replace(message, `"model":"claude",`, "", 1)},
				{"blank model", strings.Replace(message, `"model":"claude"`, `"model":" "`, 1)},
				{"wrong model type", strings.Replace(message, `"model":"claude"`, `"model":false`, 1)},
				{"missing type", strings.Replace(message, `"type":"message",`, "", 1)},
				{"wrong type", strings.Replace(message, `"type":"message"`, `"type":"other"`, 1)},
				{"missing role", strings.Replace(message, `"role":"assistant",`, "", 1)},
				{"wrong role", strings.Replace(message, `"role":"assistant"`, `"role":"user"`, 1)},
				{"missing content", strings.Replace(message, `"content":[],`, "", 1)},
				{"null content", strings.Replace(message, `"content":[]`, `"content":null`, 1)},
				{"object content", strings.Replace(message, `"content":[]`, `"content":{}`, 1)},
				{"populated content", strings.Replace(message, `"content":[]`, `"content":[{"type":"text","text":"untracked"}]`, 1)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					protocol := claudeProtocol{blocks: make(map[int]*claudeOpenBlock)}
					data := `{"type":"message_start","message":` + tc.message + `}`
					_, protocolErr := protocol.accept(gjson.Parse(data))
					require.Error(t, protocolErr)
					require.False(t, protocol.started, "invalid start must not advance protocol state")
					body := "event: message_start\ndata: " + data + "\n\n" + end
					c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
					info.ChannelSetting.PassThroughBodyEnabled = mode == "channel passthrough"
					u, err := ClaudeStreamHandler(c, resp, info)
					require.Nil(t, err)
					require.NotNil(t, info.ClaudeStream)
					require.True(t, info.ClaudeStream.Failed)
					require.False(t, info.ClaudeStream.ClientGone)
					require.False(t, info.ClaudeStream.EffectiveContent)
					require.False(t, info.ClaudeStream.ConfirmedUsage)
					require.Empty(t, info.ClaudeStream.Diagnostic.UsageEvidence)
					require.NotEmpty(t, info.ClaudeStream.Diagnostic.Error)
					require.Equal(t, "none", info.ClaudeStream.UsageSource)
					require.Zero(t, u.TotalTokens)
					require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
					require.NotContains(t, w.Body.String(), "event: message_start")
					require.NotContains(t, w.Body.String(), "event: message_stop")
				})
			}
			// usage/stop 等可选元数据和未来扩展字段应保持兼容，不影响核心字段校验。
			for _, withUsage := range []bool{false, true} {
				t.Run(fmt.Sprintf("valid optional usage %t", withUsage), func(t *testing.T) {
					start := strictStart
					if !withUsage {
						start = strings.Replace(start, `,"usage":{"input_tokens":100,"output_tokens":0}`, "", 1)
					}
					start = strings.Replace(start, `"content":[]`, `"content":[],"stop_reason":null,"stop_sequence":null,"extension":{"a":true}`, 1)
					body := start + strictText + strings.Replace(strictStop, `,"usage":{"output_tokens":7}`, "", 1)
					c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
					info.ChannelSetting.PassThroughBodyEnabled = mode == "channel passthrough"
					u, err := ClaudeStreamHandler(c, resp, info)
					require.Nil(t, err)
					require.NotNil(t, info.ClaudeStream)
					require.False(t, info.ClaudeStream.Failed)
					require.True(t, info.ClaudeStream.EffectiveContent)
					require.Equal(t, service.EstimateTokenByModel("claude", "hello"), u.CompletionTokens)
					require.Equal(t, u.CompletionTokens, u.BillingUsage.ClaudeUsage.OutputTokens)
					if withUsage {
						require.Equal(t, "mixed", info.ClaudeStream.UsageSource)
						require.Equal(t, 100, u.PromptTokens)
					} else {
						require.Equal(t, "estimated", info.ClaudeStream.UsageSource)
						require.Equal(t, 50, u.PromptTokens)
					}
					if mode != "normal" {
						require.Equal(t, body, w.Body.String())
					}
				})
			}
		})
	}
}

// TestStrictPassthroughAdapterHTTPRegression 通过本地 HTTP 适配器验证透传请求字节、最终用量、异常无收费和原始响应采集，不执行真实结算。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictPassthroughAdapterHTTPRegression(t *testing.T) {
	// 仅测试适配器，不重现整条路由中间件链，也不执行真实余额结算。
	const requestBody = " {\n\"model\":\"claude\",\"stream\":true,\"max_tokens\":100,\"messages\":[{\"role\":\"user\",\"content\":\"private-request-sentinel\"}],\"unknown_option\":{\"keep\":true}} "
	const messageStop = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	const finalDeltas = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":1}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"
	closedText := strictText + strings.Split(strictStop, "event: message_delta")[0]
	const upstreamError = "event: error\r\ndata: {\"type\":\"error\",\"extra\":42,\"error\":{\"type\":\"overloaded_error\",\"message\":\"original\"}}\r\n\r\n"
	old := *model_setting.GetGlobalSettings()
	t.Cleanup(func() { model_setting.ReplaceGlobalSettings(old) })
	for _, mode := range []string{"global", "channel"} {
		t.Run(mode, func(t *testing.T) {
			settings := old
			settings.PassThroughRequestEnabled = mode == "global"
			model_setting.ReplaceGlobalSettings(settings)
			for _, tc := range []struct {
				name, body string // 场景名及本地模拟上游返回的 SSE。
				output     int    // 预期最终输出 token 数。
				failed     bool   // 预期是否异常结束。
			}{
				{"usage before stop", strictStart + closedText + finalDeltas + messageStop, 1, false},
				{"zero before stop", strictStart + closedText + strings.Replace(finalDeltas, `"output_tokens":1`, `"output_tokens":0`, 1) + messageStop, 0, false},
				{"empty message start", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n" + finalDeltas + messageStop, 0, true},
				{"upstream error", upstreamError, 0, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					type requestResult struct {
						body []byte // 模拟服务端读到的请求字节，用于断言透传保持原样，不是生产日志采集。
						err  error  // 模拟服务端读取请求的错误。
					}
					received := make(chan requestResult, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := io.ReadAll(r.Body)
						received <- requestResult{body: body, err: err}
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, tc.body)
					}))
					t.Cleanup(server.Close)
					c, w, _, info := strictTestContext(nil)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(requestBody))
					info.ChannelBaseUrl = server.URL
					info.ChannelType = constant.ChannelTypeAnthropic
					info.ChannelSetting.PassThroughBodyEnabled = mode == "channel"
					info.DisablePing = true
					storage, err := common.GetBodyStorage(c)
					require.NoError(t, err)
					t.Cleanup(func() { _ = storage.Close() })
					info.UpstreamRequestBodySize = storage.Size()
					info.ClaudeRequestBody = storage
					adaptor := &Adaptor{}
					response, err := adaptor.DoRequest(c, info, common.ReaderOnly(storage))
					require.NoError(t, err)
					r := <-received
					require.NoError(t, r.err)
					require.Equal(t, requestBody, string(r.body), "unknown fields and whitespace must survive passthrough")
					value, apiErr := adaptor.DoResponse(c, response.(*http.Response), info)
					require.Nil(t, apiErr)
					u := value.(*dto.Usage)
					require.NotNil(t, info.ClaudeStream)
					require.Equal(t, tc.failed, info.ClaudeStream.Failed)
					require.Equal(t, tc.output, u.CompletionTokens)
					if tc.failed {
						require.Equal(t, "none", info.ClaudeStream.UsageSource)
						require.Zero(t, u.TotalTokens)
						require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
					} else {
						require.Equal(t, "upstream", info.ClaudeStream.UsageSource)
						require.Equal(t, tc.output, u.BillingUsage.ClaudeUsage.OutputTokens)
						require.True(t, info.ClaudeStream.Diagnostic.UsageFinal)
					}
					if !tc.failed || tc.body == upstreamError {
						require.Equal(t, tc.body, w.Body.String())
					}
					d := info.ClaudeStream.Diagnostic
					require.Equal(t, tc.body, string(d.BodyHead)+string(d.BodyTail))
					require.NotContains(t, string(d.BodyHead)+string(d.BodyTail), "private-request-sentinel")
				})
			}
		})
	}
}

// TestStrictPolicyStopMarkerRegression 验证上游策略停止原因仍保存到管理员专用上下文，而不误判正常消息结束。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictPolicyStopMarkerRegression(t *testing.T) {
	stop := "ref" + "usal"
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + strictText + strings.Replace(strictStop, "end_turn", stop, 1))))
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.False(t, info.ClaudeStream.Failed)
	require.NotEmpty(t, common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
}

// TestClaudeCaptureAndStrictSwitchesAreIndependent 遍历严格解析与响应采集开关的四种组合，验证采集与计费状态互不绑定。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeCaptureAndStrictSwitchesAreIndependent(t *testing.T) {
	old := *operation_setting.GetClaudeStreamSetting()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_setting", map[string]string{"enabled": fmt.Sprint(old.Enabled), "capture_response": fmt.Sprint(old.CaptureResponse)}))
	})
	for _, strict := range []bool{false, true} {
		for _, capture := range []bool{false, true} {
			require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_setting", map[string]string{"enabled": fmt.Sprint(strict), "capture_response": fmt.Sprint(capture)}))
			body := strictStart + strictText + strictStop
			c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
			if info.CaptureClaudeResponse() {
				info.ClaudeDiagnostic = relaycommon.NewClaudeResponseCapture(1)
				info.ClaudeDiagnostic.Observe(resp)
			}
			_, err := ClaudeStreamHandler(c, resp, info)
			require.Nil(t, err)
			require.Equal(t, strict, info.ClaudeStream != nil)
			if capture {
				d := info.ClaudeDiagnostic.Snapshot()
				require.Equal(t, body, string(d.BodyHead)+string(d.BodyTail))
			} else {
				require.Nil(t, info.ClaudeDiagnostic)
				if strict {
					require.Empty(t, info.ClaudeStream.Diagnostic.BodyHead)
				}
			}
		}
	}
}

// strictReadError 的 Reader 为本地字节源；其 Read 在源结束时注入异常，覆盖部分返回后读取失败。
type strictReadError struct{ io.Reader }

// Read 把底层正常 EOF 替换为读取异常，模拟上游已返回部分内容后断连。
// 接收者 r：持有本地 Reader 的夹具；参数 p：读取目标缓冲；返回实际字节数及替换后的错误。
func (r strictReadError) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, errors.New("fixture read failure")
	}
	return n, err
}

// Close 补齐读取失败夹具的 io.ReadCloser 接口，无外部资源需要释放。
// 接收者 r：本地读取夹具；无参数；返回 nil。
func (r strictReadError) Close() error { return nil }

// TestStrictReadError 模拟上游部分返回后的读取异常，验证私有错误和唯一 error 事件。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictReadError(t *testing.T) {
	c, w, resp, info := strictTestContext(strictReadError{strings.NewReader(strictStart + strictText)})
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.Equal(t, "upstream_read_error", string(info.StreamStatus.EndReason))
	require.Equal(t, "fixture read failure", info.ClaudeStream.Diagnostic.Error)
	require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
}

// TestStrictClientCancellation 验证预先取消的请求不继续发送内容或采用尚未读取的用量。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictClientCancellation(t *testing.T) {
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart)))
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.Equal(t, "client_gone", string(info.StreamStatus.EndReason))
	require.Empty(t, w.Body.String())
	require.Equal(t, "none", info.ClaudeStream.UsageSource)
}

// TestStrictUsagePresenceAndEstimation 区分 usage 缺失和显式零值，验证只有缺少证据且已交付内容时走异常估算。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictUsagePresenceAndEstimation(t *testing.T) {
	noUsage := strings.Replace(strictStart, `,"usage":{"input_tokens":100,"output_tokens":0}`, "", 1)
	for _, tc := range []struct {
		name, body, source string // 场景名、上游响应字节及预期用量来源。
		input              int    // 预期计费输入 token，显式 0 与缺少证据分别测试。
	}{
		{"missing", noUsage + strictText, "estimated", 50},
		{"explicit zero", strings.Replace(strictStart, `"input_tokens":100`, `"input_tokens":0`, 1) + strictText, "upstream", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(tc.body)))
			u, e := strictClaudeStream(c, resp, info)
			require.Nil(t, e)
			require.Equal(t, tc.source, info.ClaudeStream.UsageSource)
			require.Equal(t, tc.input, u.PromptTokens)
			if tc.source == "estimated" {
				require.Positive(t, u.CompletionTokens)
				require.Equal(t, u.CompletionTokens, u.BillingUsage.ClaudeUsage.OutputTokens)
				require.True(t, u.BillingUsage.Estimated)
			} else {
				require.Zero(t, u.CompletionTokens)
			}
		})
	}
}

// TestStrictCumulativeUsage 验证累计用量覆盖、零值保留、缓存保留以及无效字段批次不改变已有证据。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictCumulativeUsage(t *testing.T) {
	e := map[string]int{}
	require.NoError(t, mergeClaudeUsage(e, gjson.Parse(`{"input_tokens":100,"output_tokens":5,"cache_read_input_tokens":200}`)))
	require.NoError(t, mergeClaudeUsage(e, gjson.Parse(`{"output_tokens":9}`)))
	u := confirmedClaudeUsage(e)
	require.Equal(t, 100, u.PromptTokens)
	require.Equal(t, 9, u.CompletionTokens)
	require.Equal(t, 200, u.PromptTokensDetails.CachedTokens)
	require.NoError(t, mergeClaudeUsage(e, gjson.Parse(`{"output_tokens":0}`)))
	require.Zero(t, confirmedClaudeUsage(e).CompletionTokens)
	for _, bad := range []string{`{"input_tokens":-1}`, `{"output_tokens":1.5}`, `{"output_tokens":"2"}`, `{"input_tokens":2,"output_tokens":null}`, `{"input_tokens":999999999999999999999}`} {
		before := e["input_tokens"]
		require.Error(t, mergeClaudeUsage(e, gjson.Parse(bad)))
		require.Equal(t, before, e["input_tokens"])
	}
}

// strictFailWriter 在指定事件处制造下游写失败，验证交付与断开计费判定。
type strictFailWriter struct {
	gin.ResponseWriter                    // 未命中故障条件时使用的原始记录器。
	failOn             string             // 在待写帧中匹配的故障触发子串。
	cancel             context.CancelFunc // 可选请求取消回调，命中故障时先调用。
	short              bool               // true 返回短写且无错误；false 返回断管错误。
}

// Write 在匹配指定帧时制造短写或断管错误，并可同时取消请求。
// 接收者 w：写失败夹具；参数 p：待写帧；返回成功字节数和注入错误，未命中时交给原写入器。
func (w *strictFailWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.failOn) {
		if w.cancel != nil {
			w.cancel()
		}
		if w.short {
			return len(p) - 1, nil
		}
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(p)
}

// TestStrictDisconnectBilling 遍历用户断开前是否有内容和用量的组合，验证客户端断开仅依已确认上游用量收费。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictDisconnectBilling(t *testing.T) {
	noUsage := strings.Replace(strictStart, `,"usage":{"input_tokens":100,"output_tokens":0}`, "", 1)
	for _, confirmed := range []bool{false, true} {
		for _, afterText := range []bool{false, true} {
			start := noUsage
			if confirmed {
				start = strictStart
			}
			c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(start + strictText + strictStop)))
			stop := "message_start"
			if afterText {
				stop = "content_block_stop"
			}
			c.Writer = &strictFailWriter{ResponseWriter: c.Writer, failOn: stop, short: true}
			u, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.Equal(t, "client_gone", string(info.StreamStatus.EndReason))
			require.Equal(t, afterText, info.ClaudeStream.EffectiveContent)
			if confirmed {
				require.Equal(t, 100, u.PromptTokens)
				require.Equal(t, "upstream", info.ClaudeStream.UsageSource)
			} else {
				require.Zero(t, u.TotalTokens)
				require.Equal(t, "none", info.ClaudeStream.UsageSource)
			}
		}
	}
}

// TestStrictSignatureOnly 验证只有 signature 不算有效内容；正常完整结束尊重上游用量，异常且无有效交付时不收费。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictSignatureOnly(t *testing.T) {
	signature := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"encrypted"}}

`
	for _, complete := range []bool{false, true} {
		body := strictStart + signature
		if complete {
			body += strings.Replace(strictStop, `"output_tokens":7`, `"output_tokens":9523`, 1)
		}
		c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
		u, err := strictClaudeStream(c, resp, info)
		require.Nil(t, err)
		require.False(t, info.ClaudeStream.EffectiveContent)
		if complete {
			require.Equal(t, 9523, u.CompletionTokens)
			require.NotContains(t, w.Body.String(), "event: error")
		} else {
			require.Zero(t, u.TotalTokens)
			require.NotContains(t, w.Body.String(), "end_turn")
		}
	}
}

// TestStrictFrameSplittingAndToolArguments 验证单字节短读、CRLF、多行 data 与分段工具 JSON，完整工具参数仅在块结束时计为候选内容。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictFrameSplittingAndToolArguments(t *testing.T) {
	body := strings.ReplaceAll(strictStart+strictText+strictStop, "\n", "\r\n")
	body = "event: ping\r\ndata: {\r\ndata: \"type\":\"ping\"}\r\n\r\n" + body
	c, _, resp, info := strictTestContext(io.NopCloser(&oneByteReader{reader: strings.NewReader(body)}))
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.Equal(t, "done", string(info.StreamStatus.EndReason))
	protocol := claudeProtocol{started: true, blocks: map[int]*claudeOpenBlock{}}
	_, e := protocol.accept(gjson.Parse(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"search","input":{}}}`))
	require.NoError(t, e)
	content, e := protocol.accept(gjson.Parse(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"x\":"}}`))
	require.NoError(t, e)
	require.Empty(t, content)
	_, e = protocol.accept(gjson.Parse(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"1}"}}`))
	require.NoError(t, e)
	content, e = protocol.accept(gjson.Parse(`{"type":"content_block_stop","index":0}`))
	require.NoError(t, e)
	require.Contains(t, content, `{"x":1}`)
}

// oneByteReader 的 reader 为本地字节源，每次最多读一字节，验证分隔符跨读取边界。
type oneByteReader struct{ reader io.Reader }

// Read 把每次底层读取限制为最多一个字节，验证帧拆分器的增量短读处理。
// 接收者 r：持有本地读取器的夹具；参数 p：目标缓冲；原样返回底层读取计数和错误。
func (r *oneByteReader) Read(p []byte) (int, error) { return r.reader.Read(p[:min(1, len(p))]) }

// strictFlushFailure 嵌入正常 Write 记录器，但 FlushError 固定失败，区分记录字节与成功交付。
type strictFlushFailure struct{ *httptest.ResponseRecorder }

// FlushError 始终返回断管错误，验证刷新失败不应被当作有效内容交付。
// 接收者 w：可记录 Write 的测试响应器；无参数；返回 io.ErrClosedPipe。
func (w *strictFlushFailure) FlushError() error { return io.ErrClosedPipe }

// TestStrictFlushFailureDoesNotCountContent 验证 Write 已记录字节但 Flush 失败时仍按下游失败处理，不计有效内容。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictFlushFailureDoesNotCountContent(t *testing.T) {
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + strictText)))
	writer := &strictFlushFailure{httptest.NewRecorder()}
	c, _ = gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.False(t, info.ClaudeStream.EffectiveContent)
	require.True(t, info.ClaudeStream.ClientGone)
}

// strictTimeoutControl 的 writable 标记回调执行期间允许终止错误写入，模拟受管超时控制器。
type strictTimeoutControl struct{ writable bool }

// RelayTimeoutKind 模拟中间件的总时长超时分类，帮助区分超时和用户主动取消。
// 接收者 t：超时控制夹具；无参数；返回 total。
func (t *strictTimeoutControl) RelayTimeoutKind() string { return "total" }

// WriteTerminalError 模拟受管超时后的临时终止错误写入窗口。
// 接收者 t：控制夹具；参数 f：同步错误写出回调；执行期间 writable 为 true，结束后恢复 false。
func (t *strictTimeoutControl) WriteTerminalError(f func()) {
	t.writable = true
	defer func() { t.writable = false }()
	f()
}

// TestStrictManagedTimeoutEmitsError 验证受管超时与用户取消区分，并允许发送流内终止 error。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictManagedTimeoutEmitsError(t *testing.T) {
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader("")))
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	control := &strictTimeoutControl{}
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
	require.True(t, service.IsRelayRequestTimeout(c))
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.Equal(t, "timeout", string(info.StreamStatus.EndReason))
	require.Contains(t, w.Body.String(), "event: error")
}

// TestStrictRawDiagnosticBeforePatching 验证私有快照保存用量改写前的原始响应，响应头快照与后续修改相互独立。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictRawDiagnosticBeforePatching(t *testing.T) {
	body := strictStart + strictText + strictStop
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	diagnostic := info.ClaudeStream.Diagnostic
	require.Equal(t, body, string(diagnostic.BodyHead)+string(diagnostic.BodyTail))
	require.NotEqual(t, body, w.Body.String(), "usage patch affects only the downstream copy")
	require.Equal(t, []string{"one", "two"}, diagnostic.ResponseHeaders["X-Test"])
	resp.Header.Set("X-Test", "mutated")
	require.Equal(t, []string{"one", "two"}, diagnostic.ResponseHeaders["X-Test"])
}

// TestStrictEstimatesActualOutboundRequest 验证缺少用量时优先依据实际出站请求存储估算，而不是旧的请求估算值。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictEstimatesActualOutboundRequest(t *testing.T) {
	noUsage := strings.Replace(strictStart, `,"usage":{"input_tokens":100,"output_tokens":0}`, "", 1)
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(noUsage + strictText)))
	storage, err := common.CreateBodyStorage([]byte(`{"model":"claude","messages":[{"role":"user","content":"upstream changed prompt"}]}`))
	require.NoError(t, err)
	defer storage.Close()
	info.ClaudeRequestBody = storage
	u, apiErr := strictClaudeStream(c, resp, info)
	require.Nil(t, apiErr)
	require.Equal(t, 7, u.PromptTokens) // 包含 Claude 消息角色元数据的 token 估算。
	require.Equal(t, "estimated", info.ClaudeStream.UsageSource)
}

// TestStrictConnectionFailureAndHeaderBound 覆盖无 HTTP 响应的连接失败及响应头超限，检查零用量、错误事件和截断标记。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictConnectionFailureAndHeaderBound(t *testing.T) {
	c, w, _, info := strictTestContext(nil)
	u, err := HandleStreamTransportFailure(c, info, errors.New("connection fixture failed"))
	require.Nil(t, err)
	require.Zero(t, u.TotalTokens)
	require.Contains(t, w.Body.String(), "event: error")
	require.Equal(t, "connection fixture failed", info.ClaudeStream.Diagnostic.Error)
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader("")))
	resp.Header.Set("Large", strings.Repeat("x", 17<<10))
	_, err = strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.True(t, info.ClaudeStream.Diagnostic.HeadersTruncated)
	require.NotContains(t, info.ClaudeStream.Diagnostic.ResponseHeaders, "Large")
}

// strictTransportErrorClient 不访问网络，固定返回连接错误以检验空响应诊断。
type strictTransportErrorClient struct{}

// Do 模拟未取得 HTTP 响应的连接失败，不执行真实网络访问。
// 接收者为无状态 strictTransportErrorClient；未命名 *http.Request 参数仅满足接口，不读取其内容。
// 返回 nil 响应及固定的传输错误。
func (strictTransportErrorClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport fixture")
}

// TestStrictTransportFailureDoesNotCaptureSyntheticResponse 验证内部错误读取器不被重复登记为上游响应，真实传输错误和尝试编号保留。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictTransportFailureDoesNotCaptureSyntheticResponse(t *testing.T) {
	c, _, _, info := strictTestContext(nil)
	info.ClaudeDiagnostic = relaycommon.NewClaudeResponseCapture(2)
	client := relaycommon.ClaudeDiagnosticHTTPClient{Client: strictTransportErrorClient{}, Capture: info.ClaudeDiagnostic}
	_, transportErr := client.Do(c.Request)
	_, err := HandleStreamTransportFailure(c, info, transportErr)
	require.Nil(t, err)
	d := info.ClaudeStream.Diagnostic
	require.Empty(t, d.PreviousResponses, "the synthetic error reader is not another upstream HTTP response")
	require.Equal(t, "transport fixture", d.ReadError)
	require.Zero(t, d.ObservedBytes)
	require.Equal(t, 2, d.Attempt)
}

// TestStrictMultipleMessageDeltasAndFinalUsage 验证多次累计 message_delta、相同/冲突停止原因及初始用量不算最终报告。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictMultipleMessageDeltasAndFinalUsage(t *testing.T) {
	stop := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	partial := strings.TrimSuffix(strictStart+strictText+strictStop, stop)
	for _, tc := range []struct {
		name, suffix, reason string // 场景名、追加的消息 delta 及预期结束原因。
	}{
		{"usage update", "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":12}}\n\n", "done"},
		{"same reason", "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":12}}\n\n", "done"},
		{"conflicting reason", "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n", "upstream_protocol_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(partial + tc.suffix + stop)))
			u, err := strictClaudeStream(c, resp, info)
			require.Nil(t, err)
			require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
			if tc.reason == "done" {
				require.Equal(t, 12, u.CompletionTokens)
				require.True(t, info.ClaudeStream.Diagnostic.UsageFinal)
			}
		})
	}
	body := strings.Replace(strictStart+strictText+strictStop, `,"usage":{"output_tokens":7}`, "", 1)
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
	_, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.False(t, info.ClaudeStream.Diagnostic.UsageFinal, "initial usage is not a final report")
}

// TestStrictNormalEmptyMessageKeepsInputEstimation 验证正常结束的空消息保留既有输入估算，不套用异常无有效交付的免收费规则。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestStrictNormalEmptyMessageKeepsInputEstimation(t *testing.T) {
	start := strings.Replace(strictStart, `,"usage":{"input_tokens":100,"output_tokens":0}`, "", 1)
	body := start + "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
	u, err := strictClaudeStream(c, resp, info)
	require.Nil(t, err)
	require.False(t, info.ClaudeStream.Failed)
	require.False(t, info.ClaudeStream.EffectiveContent)
	require.Equal(t, "estimated", info.ClaudeStream.UsageSource)
	require.Equal(t, 50, u.PromptTokens)
	require.Zero(t, u.CompletionTokens)
}
