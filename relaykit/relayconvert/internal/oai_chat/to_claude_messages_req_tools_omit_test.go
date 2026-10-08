package oaichat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/relaykit/dto"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

// A request without tools must not reach Claude with "tools": [] — Tools is an
// interface field, so omitempty only drops it when it is nil.

func claudeBodyJSON(t *testing.T, req dto.GeneralOpenAIRequest) map[string]any {
	t.Helper()
	got, err := OpenAIChatRequestToClaudeMessages(newGinCtx(), claudeMeta(), req)
	require.NoError(t, err)
	raw, err := kitutil.Marshal(got)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, kitutil.Unmarshal(raw, &body))
	return body
}

func TestClaudeReq_NoToolsOmitsToolsField(t *testing.T) {
	body := claudeBodyJSON(t, dto.GeneralOpenAIRequest{
		Model:    "claude-3",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	})
	assert.NotContains(t, body, "tools")
}

func TestClaudeReq_ToolsFieldKeptWhenPresent(t *testing.T) {
	body := claudeBodyJSON(t, dto.GeneralOpenAIRequest{
		Model:    "claude-3",
		Tools:    []dto.ToolCallRequest{toolWithParams("lookup", "d", map[string]any{"type": "object"})},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	})
	tools, ok := body["tools"].([]any)
	require.True(t, ok)
	assert.Len(t, tools, 1)
}

func TestClaudeReq_WebSearchOnlyKeepsToolsField(t *testing.T) {
	body := claudeBodyJSON(t, dto.GeneralOpenAIRequest{
		Model:            "claude-3",
		WebSearchOptions: &dto.WebSearchOptions{},
		Messages:         []dto.Message{{Role: "user", Content: "hi"}},
	})
	tools, ok := body["tools"].([]any)
	require.True(t, ok)
	assert.Len(t, tools, 1)
}

// Anthropic rejects tool_choice without tools, so neither tool_choice nor
// parallel_tool_calls is forwarded when no tool survives the conversion.
func TestClaudeReq_ToolChoiceWithoutToolsOmitted(t *testing.T) {
	for name, req := range map[string]dto.GeneralOpenAIRequest{
		"no tools": {
			Model: "claude-3", ToolChoice: "required", ParallelTooCalls: ptr(false),
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		},
		"parallel only": {
			Model: "claude-3", ParallelTooCalls: ptr(false),
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := claudeBodyJSON(t, req)
			assert.NotContains(t, body, "tools")
			assert.NotContains(t, body, "tool_choice")
		})
	}
}

func TestClaudeReq_ToolChoiceKeptWithWebSearchOnly(t *testing.T) {
	body := claudeBodyJSON(t, dto.GeneralOpenAIRequest{
		Model: "claude-3", ToolChoice: "auto",
		WebSearchOptions: &dto.WebSearchOptions{},
		Messages:         []dto.Message{{Role: "user", Content: "hi"}},
	})
	assert.Equal(t, map[string]any{"type": "auto"}, body["tool_choice"])
}
