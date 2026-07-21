package claudemessages

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

func strptr(s string) *string { return &s }

func openRouterInfo(upstreamModel string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: upstreamModel,
		},
	}
}

func genericInfo(channelType int, upstreamModel, originModel string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: originModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       channelType,
			UpstreamModelName: upstreamModel,
		},
	}
}

// --- scalar / pointer field mapping (Rule 5: preserve explicit zero values) ---

func TestReq_ScalarPointersPreserveExplicitZero(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:       "claude-3",
		Temperature: common.GetPointer(0.0),
		MaxTokens:   common.GetPointer(uint(0)),
		TopP:        common.GetPointer(0.0),
		TopK:        common.GetPointer(0),
		Stream:      common.GetPointer(false),
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.NotNil(t, out.Temperature)
	assert.Equal(t, 0.0, *out.Temperature)
	require.NotNil(t, out.MaxTokens)
	assert.Equal(t, uint(0), *out.MaxTokens)
	require.NotNil(t, out.TopP)
	assert.Equal(t, 0.0, *out.TopP)
	require.NotNil(t, out.TopK)
	assert.Equal(t, 0, *out.TopK)
	require.NotNil(t, out.Stream)
	assert.False(t, *out.Stream)
	assert.Equal(t, "claude-3", out.Model)
}

func TestReq_NilOptionalScalarsRemainNil(t *testing.T) {
	req := dto.ClaudeRequest{Model: "claude-3"}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Nil(t, out.Temperature)
	assert.Nil(t, out.MaxTokens)
	assert.Nil(t, out.TopP)
	assert.Nil(t, out.TopK)
	assert.Nil(t, out.Stream)
}

func TestReq_NonZeroScalars(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:     "claude-3",
		MaxTokens: common.GetPointer(uint(128)),
		TopP:      common.GetPointer(0.9),
		TopK:      common.GetPointer(40),
		Stream:    common.GetPointer(true),
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Equal(t, uint(128), *out.MaxTokens)
	assert.Equal(t, 0.9, *out.TopP)
	assert.Equal(t, 40, *out.TopK)
	assert.True(t, *out.Stream)
}

// --- stop sequences: 0 / 1 / >1 (boundary) ---

func TestReq_StopSequencesNone(t *testing.T) {
	out, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{Model: "m"}, nil)
	require.NoError(t, err)
	assert.Nil(t, out.Stop)
}

func TestReq_StopSequencesSingleBecomesString(t *testing.T) {
	req := dto.ClaudeRequest{Model: "m", StopSequences: []string{"STOP"}}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Equal(t, "STOP", out.Stop)
}

func TestReq_StopSequencesMultipleBecomesSlice(t *testing.T) {
	req := dto.ClaudeRequest{Model: "m", StopSequences: []string{"A", "B"}}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B"}, out.Stop)
}

// --- tools ---

func TestReq_ToolsConverted(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Tools: []dto.Tool{
			{Name: "get_weather", Description: "d", InputSchema: map[string]interface{}{"type": "object"}},
		},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Tools, 1)
	assert.Equal(t, "function", out.Tools[0].Type)
	assert.Equal(t, "get_weather", out.Tools[0].Function.Name)
	assert.Equal(t, "d", out.Tools[0].Function.Description)
	assert.Equal(t, map[string]interface{}{"type": "object"}, out.Tools[0].Function.Parameters)
}

func TestReq_ToolsEmptyYieldsEmptySlice(t *testing.T) {
	out, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{Model: "m"}, nil)
	require.NoError(t, err)
	assert.NotNil(t, out.Tools)
	assert.Len(t, out.Tools, 0)
}

// --- system prompt variants ---

func TestReq_StringSystem(t *testing.T) {
	req := dto.ClaudeRequest{Model: "m", System: "you are helpful"}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	assert.Equal(t, "system", out.Messages[0].Role)
	assert.Equal(t, "you are helpful", out.Messages[0].StringContent())
}

func TestReq_EmptyStringSystemSkipped(t *testing.T) {
	req := dto.ClaudeRequest{Model: "m", System: ""}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Len(t, out.Messages, 0)
}

func TestReq_ArraySystemConcatenatedNonOpenRouter(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		System: []dto.ClaudeMediaMessage{
			{Type: "text", Text: strptr("part1 ")},
			{Type: "text", Text: strptr("part2")},
		},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	assert.Equal(t, "system", out.Messages[0].Role)
	assert.Equal(t, "part1 part2", out.Messages[0].StringContent())
}

func TestReq_ArraySystemEmptyListSkipped(t *testing.T) {
	// A non-string, non-array-parseable system that parses to empty.
	req := dto.ClaudeRequest{Model: "m", System: []dto.ClaudeMediaMessage{}}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Len(t, out.Messages, 0)
}

func TestReq_ArraySystemOpenRouterClaudeUsesMediaContent(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		System: []dto.ClaudeMediaMessage{
			{Type: "text", Text: strptr("sys A"), CacheControl: json.RawMessage(`{"type":"ephemeral"}`)},
			{Type: "text", Text: strptr("sys B")},
		},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, openRouterInfo("anthropic/claude-3.5"))
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	content := out.Messages[0].ParseContent()
	require.Len(t, content, 2)
	assert.Equal(t, "text", content[0].Type)
	assert.Equal(t, "sys A", content[0].Text)
	assert.JSONEq(t, `{"type":"ephemeral"}`, string(content[0].CacheControl))
	assert.Equal(t, "sys B", content[1].Text)
}

func TestReq_ArraySystemOpenRouterNonClaudeUsesString(t *testing.T) {
	// OpenRouter but non-anthropic upstream => concatenated string, not media.
	req := dto.ClaudeRequest{
		Model: "m",
		System: []dto.ClaudeMediaMessage{
			{Type: "text", Text: strptr("X")},
			{Type: "text", Text: strptr("Y")},
		},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, openRouterInfo("openai/gpt-4o"))
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	assert.Equal(t, "XY", out.Messages[0].StringContent())
}

// --- OpenRouter reasoning / verbosity ---

func TestReq_OpenRouterVerbosityFromEffort(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:        "m",
		OutputConfig: json.RawMessage(`{"effort":"high"}`),
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, openRouterInfo("anthropic/claude-3"))
	require.NoError(t, err)
	assert.JSONEq(t, `"high"`, string(out.Verbosity))
}

func TestReq_OpenRouterNoEffortNoVerbosity(t *testing.T) {
	req := dto.ClaudeRequest{Model: "m"}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, openRouterInfo("anthropic/claude-3"))
	require.NoError(t, err)
	assert.Nil(t, out.Verbosity)
}

func TestReq_OpenRouterThinkingEnabled(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:    "m",
		Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: common.GetPointer(2048)},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, openRouterInfo("anthropic/claude-3"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"enabled":true,"max_tokens":2048}`, string(out.Reasoning))
}

func TestReq_OpenRouterThinkingAdaptive(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:    "m",
		Thinking: &dto.Thinking{Type: "adaptive"},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, openRouterInfo("anthropic/claude-3"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"enabled":true}`, string(out.Reasoning))
}

func TestReq_OpenRouterThinkingOtherTypeStillMarshalsDisabled(t *testing.T) {
	// Type neither "enabled" nor "adaptive": reasoningConfig stays zero-value
	// and is still marshaled (enabled:false).
	req := dto.ClaudeRequest{
		Model:    "m",
		Thinking: &dto.Thinking{Type: "disabled"},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, openRouterInfo("anthropic/claude-3"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"enabled":false}`, string(out.Reasoning))
}

// --- non-OpenRouter -thinking suffix logic ---

func TestReq_ThinkingSuffixAppendedWhenOriginHasSuffix(t *testing.T) {
	info := genericInfo(constant.ChannelTypeAnthropic, "claude-3", "claude-3-thinking")
	req := dto.ClaudeRequest{Model: "claude-3"}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, info)
	require.NoError(t, err)
	assert.Equal(t, "claude-3-thinking", out.Model)
}

func TestReq_ThinkingSuffixNotDoubledWhenModelAlreadyHasSuffix(t *testing.T) {
	info := genericInfo(constant.ChannelTypeAnthropic, "x", "claude-3-thinking")
	req := dto.ClaudeRequest{Model: "claude-3-thinking"}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, info)
	require.NoError(t, err)
	assert.Equal(t, "claude-3-thinking", out.Model)
}

func TestReq_ThinkingSuffixNotAppliedWhenOriginLacksSuffix(t *testing.T) {
	info := genericInfo(constant.ChannelTypeAnthropic, "x", "claude-3")
	req := dto.ClaudeRequest{Model: "claude-3"}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, info)
	require.NoError(t, err)
	assert.Equal(t, "claude-3", out.Model)
}

// --- message content: string vs blocks ---

func TestReq_StringMessageContent(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:    "m",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	assert.Equal(t, "user", out.Messages[0].Role)
	assert.Equal(t, "hello", out.Messages[0].StringContent())
}

func TestReq_TextAndInputTextBlocks(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{
				{Type: "text", Text: strptr("a")},
				{Type: "input_text", Text: strptr("b")},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	content := out.Messages[0].ParseContent()
	require.Len(t, content, 2)
	assert.Equal(t, "text", content[0].Type)
	assert.Equal(t, "a", content[0].Text)
	assert.Equal(t, "text", content[1].Type)
	assert.Equal(t, "b", content[1].Text)
}

func TestReq_ImageBlock(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{
				{Type: "image", Source: &dto.ClaudeMessageSource{Type: "base64", MediaType: "image/png", Data: "AAAA"}},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	content := out.Messages[0].ParseContent()
	require.Len(t, content, 1)
	assert.Equal(t, "image_url", content[0].Type)
	img := content[0].GetImageMedia()
	require.NotNil(t, img)
	assert.Equal(t, "data:image/png;base64,AAAA", img.Url)
}

func TestReq_ToolUseBlockBecomesToolCalls(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "assistant",
			Content: []dto.ClaudeMediaMessage{
				{Type: "tool_use", Id: "toolu_1", Name: "get_weather", Input: map[string]any{"city": "SF"}},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	tcs := out.Messages[0].ParseToolCalls()
	require.Len(t, tcs, 1)
	assert.Equal(t, "toolu_1", tcs[0].ID)
	assert.Equal(t, "function", tcs[0].Type)
	assert.Equal(t, "get_weather", tcs[0].Function.Name)
	assert.JSONEq(t, `{"city":"SF"}`, tcs[0].Function.Arguments)
}

func TestReq_ToolUseWithNilInputYieldsNullArgs(t *testing.T) {
	// requestToJSONString(nil) marshals to the JSON literal null.
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "assistant",
			Content: []dto.ClaudeMediaMessage{
				{Type: "tool_use", Id: "toolu_1", Name: "noargs"},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	tcs := out.Messages[0].ParseToolCalls()
	require.Len(t, tcs, 1)
	assert.Equal(t, "null", tcs[0].Function.Arguments)
}

func TestReq_ToolResultStringContent(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{
				{Type: "tool_result", ToolUseId: "toolu_1", Name: "get_weather", Content: "sunny"},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	// The tool result becomes its own "tool" message; the outer user message is
	// empty and dropped.
	require.Len(t, out.Messages, 1)
	msg := out.Messages[0]
	assert.Equal(t, "tool", msg.Role)
	require.NotNil(t, msg.Name)
	assert.Equal(t, "get_weather", *msg.Name)
	assert.Equal(t, "toolu_1", msg.ToolCallId)
	assert.Equal(t, "sunny", msg.StringContent())
}

func TestReq_ToolResultNameResolvedFromPriorToolUse(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{
			{
				Role: "assistant",
				Content: []dto.ClaudeMediaMessage{
					{Type: "tool_use", Id: "toolu_9", Name: "lookup", Input: map[string]any{}},
				},
			},
			{
				Role: "user",
				Content: []dto.ClaudeMediaMessage{
					{Type: "tool_result", ToolUseId: "toolu_9", Content: "done"},
				},
			},
		},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	// [assistant(tool_calls), tool]
	require.Len(t, out.Messages, 2)
	toolMsg := out.Messages[1]
	assert.Equal(t, "tool", toolMsg.Role)
	require.NotNil(t, toolMsg.Name)
	assert.Equal(t, "lookup", *toolMsg.Name)
}

func TestReq_ToolResultArrayContentMarshaledToJSON(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{
				{Type: "tool_result", ToolUseId: "toolu_1", Name: "t", Content: []any{
					map[string]any{"type": "text", "text": "hi"},
				}},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	// String content is the JSON-encoded media content array.
	var decoded []dto.ClaudeMediaMessage
	require.NoError(t, json.Unmarshal([]byte(out.Messages[0].StringContent()), &decoded))
	require.Len(t, decoded, 1)
	assert.Equal(t, "text", decoded[0].Type)
	assert.Equal(t, "hi", decoded[0].GetText())
}

func TestReq_ToolUseAndTextInSameMessageDropsText(t *testing.T) {
	// When tool_use present, mediaMessages (the text) are NOT attached because
	// the guard requires len(toolCalls)==0. Documents current behavior.
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "assistant",
			Content: []dto.ClaudeMediaMessage{
				{Type: "text", Text: strptr("thinking out loud")},
				{Type: "tool_use", Id: "toolu_1", Name: "t", Input: map[string]any{}},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	tcs := out.Messages[0].ParseToolCalls()
	require.Len(t, tcs, 1)
	// The text content is dropped.
	assert.Empty(t, out.Messages[0].ParseContent())
}

func TestReq_UnknownBlockTypeProducesEmptyDroppedMessage(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{
				{Type: "some_unknown_type", Text: strptr("ignored")},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	// No media, no tool calls => message dropped.
	assert.Len(t, out.Messages, 0)
}

func TestReq_EmptyMessagesList(t *testing.T) {
	out, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{Model: "m"}, nil)
	require.NoError(t, err)
	assert.Len(t, out.Messages, 0)
}

func TestReq_MediaOnlyMessageUsesSetMediaContent(t *testing.T) {
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{
				{Type: "text", Text: strptr("only text block")},
			},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	content := out.Messages[0].ParseContent()
	require.Len(t, content, 1)
	assert.Equal(t, "only text block", content[0].Text)
}

// requestToJSONString is exercised indirectly, but cover the error/default path.
func TestRequestToJSONString_Unmarshalable(t *testing.T) {
	// channels cannot be marshaled => "{}" fallback.
	assert.Equal(t, "{}", requestToJSONString(make(chan int)))
	assert.Equal(t, `{"a":1}`, requestToJSONString(map[string]int{"a": 1}))
}

func TestReq_ParseContentErrorPropagates(t *testing.T) {
	// Content is a non-string JSON object => not string content, and Any2Type
	// fails to unmarshal an object into []ClaudeMediaMessage => error returned.
	req := dto.ClaudeRequest{
		Model: "m",
		Messages: []dto.ClaudeMessage{{
			Role:    "user",
			Content: map[string]any{"unexpected": "object"},
		}},
	}
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.Error(t, err)
	assert.Nil(t, out)
}
