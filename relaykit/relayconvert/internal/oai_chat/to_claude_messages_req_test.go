package oaichat

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// 转换器现在接收 context.Context + convmeta.Meta。
func newGinCtx() context.Context {
	return context.Background()
}

func toolWithParams(name, desc string, params any) dto.ToolCallRequest {
	return dto.ToolCallRequest{
		Type: "function",
		Function: dto.FunctionRequest{
			Name:        name,
			Description: desc,
			Parameters:  params,
		},
	}
}

func TestClaudeReq_ToolsSchemaTranslation(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Tools: []dto.ToolCallRequest{
			toolWithParams("lookup", "look things up", map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"q": map[string]any{"type": "string"}},
				"required":             []any{"q"},
				"additionalProperties": false,
			}),
			// Non-function tools without map parameters must be skipped entirely.
			{Type: "custom", Function: dto.FunctionRequest{Name: "skipme", Parameters: "not-a-map"}},
		},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}

	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)

	tools, ok := got.Tools.([]any)
	require.True(t, ok)
	require.Len(t, tools, 1) // only the map-param tool survives
	tool := tools[0].(*dto.Tool)
	assert.Equal(t, "lookup", tool.Name)
	assert.Equal(t, "object", tool.InputSchema["type"])
	assert.NotNil(t, tool.InputSchema["properties"])
	assert.NotNil(t, tool.InputSchema["required"])
	// Extra keys beyond type/properties/required are copied through.
	assert.Equal(t, false, tool.InputSchema["additionalProperties"])
}

func TestClaudeReq_ToolWithoutTypeKey(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Tools: []dto.ToolCallRequest{
			toolWithParams("lookup", "desc", map[string]any{
				"properties": map[string]any{"q": map[string]any{"type": "string"}},
			}),
		},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	tool := got.Tools.([]any)[0].(*dto.Tool)
	// "type" absent -> defaults to object.
	assert.Equal(t, "object", tool.InputSchema["type"])
}

func TestClaudeReq_WebSearchOptionsWithUserLocationAndContextSizes(t *testing.T) {
	// search_context_size does not map to Claude max_uses; the hosted tool keeps its own limit.
	sizes := map[string]int{
		"low":     0,
		"medium":  0,
		"high":    0,
		"unknown": 0,
	}
	for size, wantMax := range sizes {
		t.Run(size, func(t *testing.T) {
			ul := json.RawMessage(`{"approximate":{"timezone":"America/New_York","country":"US","region":"NY","city":"New York"}}`)
			req := dto.GeneralOpenAIRequest{
				Model: "gpt-4",
				WebSearchOptions: &dto.WebSearchOptions{
					SearchContextSize: size,
					UserLocation:      ul,
				},
				Messages: []dto.Message{{Role: "user", Content: "hi"}},
			}
			got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
			require.NoError(t, err)
			tools := got.Tools.([]any)
			require.Len(t, tools, 1)
			ws := tools[0].(*dto.ClaudeWebSearchTool)
			assert.Equal(t, "web_search", ws.Name)
			assert.Equal(t, wantMax, ws.MaxUses)
			require.NotNil(t, ws.UserLocation)
			assert.Equal(t, "America/New_York", ws.UserLocation.Timezone)
			assert.Equal(t, "US", ws.UserLocation.Country)
			assert.Equal(t, "NY", ws.UserLocation.Region)
			assert.Equal(t, "New York", ws.UserLocation.City)
		})
	}
}

func TestClaudeReq_WebSearchWithoutUserLocation(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:            "gpt-4",
		WebSearchOptions: &dto.WebSearchOptions{SearchContextSize: "low"},
		Messages:         []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	ws := got.Tools.([]any)[0].(*dto.ClaudeWebSearchTool)
	assert.Nil(t, ws.UserLocation)
}

func TestClaudeReq_ScalarParamsAndZeroValuePreservation(t *testing.T) {
	// Rule 5: an explicit temperature=0 must survive into the converted request.
	req := dto.GeneralOpenAIRequest{
		Model:       "gpt-4",
		Temperature: ptr(0.0),
		TopP:        ptr(0.9),
		TopK:        ptr(7),
		MaxTokens:   ptr(uint(4096)),
		Stream:      ptr(true),
		Messages:    []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)

	require.NotNil(t, got.Temperature)
	assert.Equal(t, 0.0, *got.Temperature)
	require.NotNil(t, got.TopP)
	assert.Equal(t, 0.9, *got.TopP)
	require.NotNil(t, got.TopK)
	assert.Equal(t, 7, *got.TopK)
	require.NotNil(t, got.MaxTokens)
	assert.Equal(t, uint(4096), *got.MaxTokens)
	require.NotNil(t, got.Stream)
	assert.True(t, *got.Stream)
}

func TestClaudeReq_DefaultMaxTokensAppliedWhenAbsent(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:    "gpt-4",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.NotNil(t, got.MaxTokens)
	assert.Greater(t, int(*got.MaxTokens), 0)
}

func TestClaudeReq_ToolChoiceAndParallel(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:            "gpt-4",
		ToolChoice:       "auto",
		ParallelTooCalls: ptr(false),
		Messages:         []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.NotNil(t, got.ToolChoice)
	tc := got.ToolChoice.(*dto.ClaudeToolChoice)
	assert.Equal(t, "auto", tc.Type)
	assert.True(t, tc.DisableParallelToolUse)
}

// 模型名推理后缀由 relay/helper.ApplyReasoningModelSuffix 在转换前处理，转换器原样保留模型名。
func TestClaudeReq_ModelSuffixNotInterpretedByConverter(t *testing.T) {
	for _, model := range []string{"claude-opus-4-6-high", "claude-opus-4-7-low", "claude-3-5-sonnet-thinking"} {
		t.Run(model, func(t *testing.T) {
			req := dto.GeneralOpenAIRequest{
				Model:     model,
				MaxTokens: ptr(uint(100)),
				Messages:  []dto.Message{{Role: "user", Content: "hi"}},
			}
			got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
			require.NoError(t, err)
			assert.Equal(t, model, got.Model)
			assert.Nil(t, got.Thinking)
			require.NotNil(t, got.MaxTokens)
			assert.Equal(t, uint(100), *got.MaxTokens)
		})
	}
}

func TestClaudeReq_ReasoningEffortLevels(t *testing.T) {
	cases := map[string]int{"low": 1638, "medium": 4096, "high": 6553}
	for effort, wantBudget := range cases {
		t.Run(effort, func(t *testing.T) {
			req := dto.GeneralOpenAIRequest{
				Model:           "gpt-4",
				ReasoningEffort: effort,
				Messages:        []dto.Message{{Role: "user", Content: "hi"}},
			}
			got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
			require.NoError(t, err)
			require.NotNil(t, got.Thinking)
			assert.Equal(t, "enabled", got.Thinking.Type)
			require.NotNil(t, got.Thinking.BudgetTokens)
			assert.Equal(t, wantBudget, *got.Thinking.BudgetTokens)
		})
	}
}

func TestClaudeReq_OpenRouterReasoning(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:     "gpt-4",
		Reasoning: json.RawMessage(`{"enabled":true,"max_tokens":3000}`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.NotNil(t, got.Thinking)
	require.NotNil(t, got.Thinking.BudgetTokens)
	assert.Equal(t, 3000, *got.Thinking.BudgetTokens)
}

func TestClaudeReq_OpenRouterReasoningInvalidJSON(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:     "gpt-4",
		Reasoning: json.RawMessage(`{bad`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	_, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.Error(t, err)
}

func TestClaudeReq_OpenRouterReasoningZeroBudgetConflict(t *testing.T) {
	// enabled=true 与 max_tokens=0（关闭思考）互相矛盾，按客户端参数错误拒绝。
	req := dto.GeneralOpenAIRequest{
		Model:     "gpt-4",
		Reasoning: json.RawMessage(`{"enabled":true,"max_tokens":0}`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	_, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.Error(t, err)
}

func TestClaudeReq_StopSequences(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model:    "gpt-4",
			Stop:     "STOP",
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		}
		got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
		require.NoError(t, err)
		assert.Equal(t, []string{"STOP"}, got.StopSequences)
	})
	t.Run("array", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model:    "gpt-4",
			Stop:     []interface{}{"a", "b"},
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		}
		got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, got.StopSequences)
	})
}

func TestClaudeReq_SystemMessagesStringAndParts(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "system", Content: "be terse"},
			{Role: "system", Content: []any{
				map[string]any{"type": "text", "text": "and precise"},
				map[string]any{"type": "text", "text": ""}, // empty text skipped
			}},
			{Role: "user", Content: "hi"},
		},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	sys, ok := got.System.([]dto.ClaudeMediaMessage)
	require.True(t, ok)
	require.Len(t, sys, 2)
	assert.Equal(t, "be terse", *sys[0].Text)
	assert.Equal(t, "and precise", *sys[1].Text)
}

func TestClaudeReq_FirstMessageNotUserInjectsUser(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "assistant", Content: "I am first"},
		},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(got.Messages), 2)
	assert.Equal(t, "user", got.Messages[0].Role)
	assert.Equal(t, "assistant", got.Messages[1].Role)
}

func TestClaudeReq_EmptyRoleDefaultsToUser(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:    "gpt-4",
		Messages: []dto.Message{{Role: "", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	// The "" -> "user" default reaches the emitted message, so no synthetic
	// leading user message is injected.
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "user", got.Messages[0].Role)
	assert.Equal(t, "hi", got.Messages[0].Content)
}

func TestClaudeReq_ConsecutiveSameRoleMerged(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
			{Role: "user", Content: "world"},
		},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "hello world", got.Messages[0].Content)
}

func TestClaudeReq_EmptyContentBecomesEllipsis(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:    "gpt-4",
		Messages: []dto.Message{{Role: "user", Content: ""}},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "...", got.Messages[0].Content)
}

func TestClaudeReq_ToolRoleMergedIntoPrecedingUser(t *testing.T) {
	// A user string message followed by a tool result: the tool_result is
	// appended to the just-emitted user message content array.
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: "please run"},
			{Role: "tool", ToolCallId: "call_1", Content: "result payload"},
		},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	content := got.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.Len(t, content, 2)
	assert.Equal(t, "text", content[0].Type)
	assert.Equal(t, "tool_result", content[1].Type)
	assert.Equal(t, "call_1", content[1].ToolUseId)
}

func TestClaudeReq_ToolRoleFirstBecomesUserToolResult(t *testing.T) {
	// A leading tool message (no preceding user) becomes its own user message
	// carrying a tool_result block. The first-message guard also injects a
	// synthetic user "..." before it.
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "tool", ToolCallId: "call_9", Content: "orphan result"},
		},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	// The first-message guard injects a synthetic user "..." message; then the
	// tool handling sees a preceding user message and appends the tool_result to
	// it rather than creating a second message. Net: a single user message
	// containing [text "...", tool_result].
	require.Len(t, got.Messages, 1)
	last := got.Messages[0]
	assert.Equal(t, "user", last.Role)
	content := last.Content.([]dto.ClaudeMediaMessage)
	require.Len(t, content, 2)
	assert.Equal(t, "text", content[0].Type)
	assert.Equal(t, "tool_result", content[1].Type)
	assert.Equal(t, "call_9", content[1].ToolUseId)
}

func TestClaudeReq_AssistantToolCallsInContent(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: []any{
		map[string]any{"type": "text", "text": "calling"},
	}}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`}},
		{ID: "call_2", Type: "function", Function: dto.FunctionRequest{Name: "noargs", Arguments: ""}},
		{ID: "call_3", Type: "function", Function: dto.FunctionRequest{Name: "badargs", Arguments: "{bad"}},
	})
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
			msg,
		},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.Len(t, got.Messages, 2)
	content := got.Messages[1].Content.([]dto.ClaudeMediaMessage)
	// text + 3 tool_use blocks
	require.Len(t, content, 4)
	assert.Equal(t, "text", content[0].Type)
	assert.Equal(t, "tool_use", content[1].Type)
	assert.Equal(t, map[string]any{"q": "x"}, content[1].Input)
	assert.Equal(t, "tool_use", content[2].Type)
	assert.Equal(t, map[string]any{}, content[2].Input) // empty args -> empty object
	assert.Equal(t, map[string]any{}, content[3].Input) // invalid args -> empty object
}

func TestClaudeReq_MediaImageAndDocument(t *testing.T) {
	// image path
	imgReq := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "look"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x.test/pic.png"}},
			}},
		},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), imgReq)
	require.NoError(t, err)
	content := got.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.Len(t, content, 2)
	assert.Equal(t, "image", content[1].Type)
	require.NotNil(t, content[1].Source)
	assert.Equal(t, "base64", content[1].Source.Type)
	assert.Equal(t, "image/png", content[1].Source.MediaType)

	// pdf/document path (identifier carries "pdf" so the fake resolver returns application/pdf)
	pdfReq := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "file", "file": map[string]any{"file_data": "data-pdf-here", "filename": "doc.pdf"}},
			}},
		},
	}
	got2, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), pdfReq)
	require.NoError(t, err)
	content2 := got2.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.Len(t, content2, 1)
	assert.Equal(t, "document", content2[0].Type)
	assert.Equal(t, "application/pdf", content2[0].Source.MediaType)
}

func TestClaudeReq_MediaResolveError(t *testing.T) {
	setMediaResolver(t, media_resolverErr())
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x.test/pic.png"}},
			}},
		},
	}
	_, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get file data failed")
}

func TestClaudeReq_ToolAfterAssistantCreatesNewUserMessage(t *testing.T) {
	// user -> assistant -> tool: the tool sees the last Claude message is an
	// assistant (not a user), so it emits its own user message holding the
	// tool_result block.
	req := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: "run it"},
			{Role: "assistant", Content: "on it"},
			{Role: "tool", ToolCallId: "call_5", Content: "the result"},
		},
	}
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	require.Len(t, got.Messages, 3)
	last := got.Messages[2]
	assert.Equal(t, "user", last.Role)
	content := last.Content.([]dto.ClaudeMediaMessage)
	require.Len(t, content, 1)
	assert.Equal(t, "tool_result", content[0].Type)
	assert.Equal(t, "call_5", content[0].ToolUseId)
}

func TestClaudeReq_MediaPartNilSourceSkipped(t *testing.T) {
	// A non-text media part whose ToFileSource() is nil (image_url with empty
	// url) is skipped without error.
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "keep"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": ""}},
			}},
		},
	})
	require.NoError(t, err)
	content := got.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.Len(t, content, 1)
	assert.Equal(t, "text", content[0].Type)
}

func TestOpenAIChatRequestToClaudeMessagesNormalizesToolInputSchema(t *testing.T) {
	tests := []struct {
		name       string
		parameters any
		wantSchema map[string]any
	}{
		{
			name:       "omitted parameters",
			parameters: nil,
			wantSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			name: "missing type and properties",
			parameters: map[string]any{
				"additionalProperties": false,
			},
			wantSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			name: "non-string type",
			parameters: map[string]any{
				"type":       123,
				"properties": map[string]any{},
			},
			wantSchema: map[string]any{
				"type":       123,
				"properties": map[string]any{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxTokens := uint(1024)
			got, err := OpenAIChatRequestToClaudeMessages(context.Background(), nil, dto.GeneralOpenAIRequest{
				Model:     "claude-test",
				MaxTokens: &maxTokens,
				Messages: []dto.Message{
					{Role: "user", Content: "Call the tool."},
				},
				Tools: []dto.ToolCallRequest{
					{
						Type: "function",
						Function: dto.FunctionRequest{
							Name:        "get_current_time",
							Description: "Get the current time",
							Parameters:  tt.parameters,
						},
					},
				},
			})

			require.NoError(t, err)
			tools, ok := got.Tools.([]any)
			require.True(t, ok)
			require.Len(t, tools, 1)
			tool, ok := tools[0].(*dto.Tool)
			require.True(t, ok)
			assert.Equal(t, "get_current_time", tool.Name)
			assert.Equal(t, tt.wantSchema, tool.InputSchema)
		})
	}
}
