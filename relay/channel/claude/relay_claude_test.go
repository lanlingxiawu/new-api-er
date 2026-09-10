package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func commonPointer[T any](value T) *T {
	return &value
}

func TestResponseOpenAI2ClaudeToolUseInputIsObject(t *testing.T) {
	tests := []struct {
		name string
		args string
		want map[string]interface{}
	}{
		{name: "object", args: `{"q":"x"}`, want: map[string]interface{}{"q": "x"}},
		{name: "empty", args: "", want: map[string]interface{}{}},
		{name: "invalid", args: "{", want: map[string]interface{}{}},
		{name: "null", args: "null", want: map[string]interface{}{}},
		{name: "array", args: `["x"]`, want: map[string]interface{}{}},
		{name: "string", args: `"x"`, want: map[string]interface{}{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := dto.Message{Role: "assistant"}
			msg.SetToolCalls([]dto.ToolCallRequest{
				{
					ID:   "call_1",
					Type: "function",
					Function: dto.FunctionRequest{
						Name:      "lookup",
						Arguments: tt.args,
					},
				},
			})
			resp := relayconvert.ResponseOpenAI2Claude(&dto.OpenAITextResponse{
				Id:    "chatcmpl_1",
				Model: "gpt-test",
				Choices: []dto.OpenAITextResponseChoice{
					{Message: msg, FinishReason: "tool_calls"},
				},
			}, nil)

			require.Len(t, resp.Content, 1)
			assert.Equal(t, "tool_use", resp.Content[0].Type)
			assert.Equal(t, tt.want, resp.Content[0].Input)
		})
	}
}

func TestFormatClaudeResponseInfo_MessageStart(t *testing.T) {
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{},
	}
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_start",
		Message: &dto.ClaudeMediaMessage{
			Id:    "msg_123",
			Model: "claude-3-5-sonnet",
			Usage: &dto.ClaudeUsage{
				InputTokens:              100,
				OutputTokens:             1,
				CacheCreationInputTokens: 50,
				CacheReadInputTokens:     30,
			},
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedTokens != 30 {
		t.Errorf("CachedTokens = %d, want 30", claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens != 50 {
		t.Errorf("CachedCreationTokens = %d, want 50", claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens)
	}
	if claudeInfo.ResponseId != "msg_123" {
		t.Errorf("ResponseId = %s, want msg_123", claudeInfo.ResponseId)
	}
	if claudeInfo.Model != "claude-3-5-sonnet" {
		t.Errorf("Model = %s, want claude-3-5-sonnet", claudeInfo.Model)
	}
}

func TestFormatClaudeResponseInfo_MessageDelta_FullUsage(t *testing.T) {
	// message_start 先积累 usage
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{
			PromptTokens: 100,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         30,
				CachedCreationTokens: 50,
			},
			CompletionTokens: 1,
		},
	}

	// message_delta 带完整 usage（原生 Anthropic 场景）
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			InputTokens:              100,
			OutputTokens:             200,
			CacheCreationInputTokens: 50,
			CacheReadInputTokens:     30,
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", claudeInfo.Usage.CompletionTokens)
	}
	if claudeInfo.Usage.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", claudeInfo.Usage.TotalTokens)
	}
	if !claudeInfo.Done {
		t.Error("expected Done = true")
	}
}

func TestFormatClaudeResponseInfo_MessageDelta_OnlyOutputTokens(t *testing.T) {
	// 模拟 Bedrock: message_start 已积累 usage
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{
			PromptTokens: 100,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         30,
				CachedCreationTokens: 50,
			},
			CompletionTokens:            1,
			ClaudeCacheCreation5mTokens: 10,
			ClaudeCacheCreation1hTokens: 20,
		},
	}

	// Bedrock 的 message_delta 只有 output_tokens，缺少 input_tokens 和 cache 字段
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			OutputTokens: 200,
			// InputTokens, CacheCreationInputTokens, CacheReadInputTokens 都是 0
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	// PromptTokens 应保持 message_start 的值（因为 message_delta 的 InputTokens=0，不更新）
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", claudeInfo.Usage.CompletionTokens)
	}
	if claudeInfo.Usage.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", claudeInfo.Usage.TotalTokens)
	}
	// cache 字段应保持 message_start 的值
	if claudeInfo.Usage.PromptTokensDetails.CachedTokens != 30 {
		t.Errorf("CachedTokens = %d, want 30", claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens != 50 {
		t.Errorf("CachedCreationTokens = %d, want 50", claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens)
	}
	if claudeInfo.Usage.ClaudeCacheCreation5mTokens != 10 {
		t.Errorf("ClaudeCacheCreation5mTokens = %d, want 10", claudeInfo.Usage.ClaudeCacheCreation5mTokens)
	}
	if claudeInfo.Usage.ClaudeCacheCreation1hTokens != 20 {
		t.Errorf("ClaudeCacheCreation1hTokens = %d, want 20", claudeInfo.Usage.ClaudeCacheCreation1hTokens)
	}
	if !claudeInfo.Done {
		t.Error("expected Done = true")
	}
}

func TestFormatClaudeResponseInfo_NilClaudeInfo(t *testing.T) {
	claudeResponse := &dto.ClaudeResponse{Type: "message_start"}
	ok := FormatClaudeResponseInfo(claudeResponse, nil, nil)
	if ok {
		t.Error("expected false for nil claudeInfo")
	}
}

func TestFormatClaudeResponseInfo_ContentBlockDelta(t *testing.T) {
	text := "hello"
	claudeInfo := &ClaudeResponseInfo{
		Usage:        &dto.Usage{},
		ResponseText: strings.Builder{},
	}
	claudeResponse := &dto.ClaudeResponse{
		Type: "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{
			Text: &text,
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.ResponseText.String() != "hello" {
		t.Errorf("ResponseText = %q, want %q", claudeInfo.ResponseText.String(), "hello")
	}
}

func TestBuildOpenAIStyleUsageFromClaudeUsage(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
		UsageSemantic:               "anthropic",
	}

	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

	if openAIUsage.PromptTokens != 180 {
		t.Fatalf("PromptTokens = %d, want 180", openAIUsage.PromptTokens)
	}
	if openAIUsage.InputTokens != 180 {
		t.Fatalf("InputTokens = %d, want 180", openAIUsage.InputTokens)
	}
	if openAIUsage.TotalTokens != 200 {
		t.Fatalf("TotalTokens = %d, want 200", openAIUsage.TotalTokens)
	}
	if openAIUsage.UsageSemantic != "openai" {
		t.Fatalf("UsageSemantic = %s, want openai", openAIUsage.UsageSemantic)
	}
	if openAIUsage.UsageSource != "anthropic" {
		t.Fatalf("UsageSource = %s, want anthropic", openAIUsage.UsageSource)
	}
}

func TestBuildOpenAIStyleUsageFromClaudeUsagePreservesCacheCreationRemainder(t *testing.T) {
	tests := []struct {
		name                    string
		cachedCreationTokens    int
		cacheCreationTokens5m   int
		cacheCreationTokens1h   int
		expectedTotalInputToken int
	}{
		{
			name:                    "prefers aggregate when it includes remainder",
			cachedCreationTokens:    50,
			cacheCreationTokens5m:   10,
			cacheCreationTokens1h:   20,
			expectedTotalInputToken: 180,
		},
		{
			name:                    "falls back to split tokens when aggregate missing",
			cachedCreationTokens:    0,
			cacheCreationTokens5m:   10,
			cacheCreationTokens1h:   20,
			expectedTotalInputToken: 160,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &dto.Usage{
				PromptTokens:     100,
				CompletionTokens: 20,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens:         30,
					CachedCreationTokens: tt.cachedCreationTokens,
				},
				ClaudeCacheCreation5mTokens: tt.cacheCreationTokens5m,
				ClaudeCacheCreation1hTokens: tt.cacheCreationTokens1h,
				UsageSemantic:               "anthropic",
			}

			openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

			if openAIUsage.PromptTokens != tt.expectedTotalInputToken {
				t.Fatalf("PromptTokens = %d, want %d", openAIUsage.PromptTokens, tt.expectedTotalInputToken)
			}
			if openAIUsage.InputTokens != tt.expectedTotalInputToken {
				t.Fatalf("InputTokens = %d, want %d", openAIUsage.InputTokens, tt.expectedTotalInputToken)
			}
		})
	}
}

func TestBuildOpenAIStyleUsageFromClaudeUsageDefaultsAggregateCacheCreationTo5m(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
		},
		UsageSemantic: "anthropic",
	}

	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

	require.Equal(t, 50, openAIUsage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 0, openAIUsage.ClaudeCacheCreation1hTokens)
}

func newClaudeStreamRecorder() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, recorder
}

func claudeEventTypesFromBody(body string) []string {
	var events []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event: ") {
			events = append(events, strings.TrimPrefix(line, "event: "))
		}
	}
	return events
}

// TestTruncatedClaudeFinalResponseDoesNotManufactureSuccess 验证截断流的收尾函数不伪造 end_turn、内容块结束或消息结束。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestTruncatedClaudeFinalResponseDoesNotManufactureSuccess(t *testing.T) {
	for _, done := range []bool{false, true} {
		c, w := newClaudeStreamRecorder()
		i := 0
		state := &ClaudeResponseInfo{HasMessageStart: true, Done: done, OpenBlockIndex: &i, Usage: &dto.Usage{PromptTokens: 10, CompletionTokens: 2}}
		HandleStreamFinalResponse(c, &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}, state)
		require.Empty(t, w.Body.String())
		require.False(t, state.MessageStopSent)
	}
}

func TestHandleStreamFinalResponseDoesNotDuplicateMessageStop(t *testing.T) {
	c, recorder := newClaudeStreamRecorder()
	claudeInfo := &ClaudeResponseInfo{
		HasMessageStart: true,
		MessageStopSent: true,
		Done:            true,
		Usage:           &dto.Usage{PromptTokens: 10, CompletionTokens: 2},
	}

	HandleStreamFinalResponse(c, &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"},
	}, claudeInfo)

	require.Empty(t, recorder.Body.String())
}

// TestClaudeStreamHandlerPreservesUpstreamError 验证上游 error 事件经流式处理后保留原内容，不重复生成错误。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeStreamHandlerPreservesUpstreamError(t *testing.T) {
	c, w, resp, info := strictTestContext(io.NopCloser(strings.NewReader(strictStart + "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n")))
	usage, err := ClaudeStreamHandler(c, resp, info)
	require.Nil(t, err)
	require.Zero(t, usage.TotalTokens)
	require.Equal(t, []string{"message_start", "error"}, claudeEventTypesFromBody(w.Body.String()))
}

// TestClaudeStreamHandlerBillsEstimateWhenUpstreamSendsNoData 复现线上现象：
//
// 某些流式请求上游返回 HTTP 200（进入流式分支），但整段流里一条 SSE 数据都没
// 发过来（连接挂起 59~96s 后断开 / 首字超时）。此时：
//   - ReceivedResponseCount == 0，SetFirstResponseTime 从未触发 → 面板首字 = -1.0s；
//   - 没有 message_start → claudeInfo.Usage.PromptTokens == 0；
//   - HandleStreamFinalResponse 回退到 GetEstimatePromptTokens()，把「请求体估算」的
//     数百万输入 token 当成真实用量返回，且 ClaudeStreamHandler 返回 err==nil，
//     外层不会退款 → 用户被按数百万输入 token 实扣费，却零输出。
//
// 正确行为应对齐 Gemini 通道（relay-gemini.go: ReceivedResponseCount>0 才估算，
// 否则 usage 归零不计费）。本用例断言「零响应 ⇒ 零用量」，在补上守卫前会失败，
// 即复现该缺陷；修复后转绿并作为回归测试。
func TestClaudeStreamHandlerBillsEstimateWhenUpstreamSendsNoData(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	c, _ := newClaudeStreamRecorder()

	// 上游 200 但流体为空——一条 SSE 数据都没有。
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(""))}

	now := time.Now()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-opus-4-8"},
		// 镜像 GenRelayInfo 的初始化：FirstResponseTime = StartTime - 1s，
		// 只要没收到数据就一直是负数 → 首字 -1.0s。
		StartTime:         now,
		FirstResponseTime: now.Add(-time.Second),
	}
	// 请求体估算出的输入 token（截图里那种数百万级）。
	const estimate = 6_000_000
	info.SetEstimatePromptTokens(estimate)

	usage, apiErr := ClaudeStreamHandler(c, resp, info)

	// 上游确实零响应：一条数据都没收到，首字从未记录。
	require.Equal(t, 0, info.ReceivedResponseCount, "上游未发送任何 SSE 数据")
	require.False(t, info.HasSendResponse(), "首字从未记录（面板显示 -1.0s）")

	// 关键：不带 error 返回，外层 relay.go 的退款 defer 只在 newAPIError!=nil 时触发，
	// 所以这条零响应请求既不报错也不退款，直接按下面的 usage 结算扣费。
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	// —— 期望（修复后）：零响应不应产生任何可计费用量 ——
	// 当前代码会用估算值兜底，导致下面两条断言失败，即复现被错误计费的问题。
	require.Equal(t, 0, usage.PromptTokens,
		"上游零响应不应把请求体估算的输入 token 当成真实用量计费")
	require.Equal(t, 0, usage.CompletionTokens, "零输出")
	require.False(t, service.ValidUsage(usage),
		"零响应的 usage 不应被判定为有效用量（否则会进入结算扣费）")
}

func TestOpenAIChatRequestToClaudeMessages_ClaudeOpus48HighUsesAdaptiveThinking(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:       "claude-opus-4-8-high",
		Temperature: commonPointer(0.7),
		TopP:        commonPointer(0.9),
		TopK:        commonPointer(40),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hello",
			},
		},
	}

	claudeRequest, err := relayconvert.OpenAIChatRequestToClaudeMessages(nil, &relaycommon.RelayInfo{}, request)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-8", claudeRequest.Model)
	require.NotNil(t, claudeRequest.Thinking)
	require.Equal(t, "adaptive", claudeRequest.Thinking.Type)
	require.Equal(t, "summarized", claudeRequest.Thinking.Display)
	require.JSONEq(t, `{"effort":"high"}`, string(claudeRequest.OutputConfig))
	require.Nil(t, claudeRequest.Temperature)
	require.Nil(t, claudeRequest.TopP)
	require.Nil(t, claudeRequest.TopK)
}

func TestOpenAIChatRequestToClaudeMessages_ClaudeOpus48ThinkingUsesAdaptiveHighEffort(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:       "claude-opus-4-8-thinking",
		Temperature: commonPointer(0.7),
		TopP:        commonPointer(0.9),
		TopK:        commonPointer(40),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hello",
			},
		},
	}

	claudeRequest, err := relayconvert.OpenAIChatRequestToClaudeMessages(nil, &relaycommon.RelayInfo{}, request)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-8", claudeRequest.Model)
	require.NotNil(t, claudeRequest.Thinking)
	require.Equal(t, "adaptive", claudeRequest.Thinking.Type)
	require.Equal(t, "summarized", claudeRequest.Thinking.Display)
	require.JSONEq(t, `{"effort":"high"}`, string(claudeRequest.OutputConfig))
	require.Nil(t, claudeRequest.Temperature)
	require.Nil(t, claudeRequest.TopP)
	require.Nil(t, claudeRequest.TopK)
}
