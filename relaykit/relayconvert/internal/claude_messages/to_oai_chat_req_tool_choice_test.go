package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Claude tool_choice must survive the Claude -> OpenAI Chat conversion: a
// forced tool call ("any" / "tool") that arrives upstream as the default
// "auto" lets the model answer without calling the tool.

func weatherTool() []dto.Tool {
	return []dto.Tool{{Name: "get_weather", Description: "d", InputSchema: map[string]any{"type": "object"}}}
}

// convertClaudeJSON parses a raw client body the way the relay does and
// returns the upstream OpenAI body as a generic map.
func convertClaudeJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var req dto.ClaudeRequest
	require.NoError(t, kitutil.Unmarshal([]byte(body), &req))
	out, err := ClaudeMessagesRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	raw, err := kitutil.Marshal(out)
	require.NoError(t, err)
	var upstream map[string]any
	require.NoError(t, kitutil.Unmarshal(raw, &upstream))
	return upstream
}

const toolsJSON = `"tools":[{"name":"get_weather","description":"d","input_schema":{"type":"object"}}]`

func TestReq_ToolChoiceJSONMapping(t *testing.T) {
	cases := []struct {
		name         string
		toolChoice   string
		wantChoice   any // nil => tool_choice must be absent
		wantParallel any // nil => parallel_tool_calls must be absent
	}{
		{name: "auto", toolChoice: `{"type":"auto"}`, wantChoice: "auto"},
		{name: "any", toolChoice: `{"type":"any"}`, wantChoice: "required"},
		{name: "none", toolChoice: `{"type":"none"}`, wantChoice: "none"},
		{name: "tool", toolChoice: `{"type":"tool","name":"get_weather"}`,
			wantChoice: map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}},
		{name: "auto disable parallel", toolChoice: `{"type":"auto","disable_parallel_tool_use":true}`,
			wantChoice: "auto", wantParallel: false},
		{name: "any disable parallel", toolChoice: `{"type":"any","disable_parallel_tool_use":true}`,
			wantChoice: "required", wantParallel: false},
		{name: "tool disable parallel", toolChoice: `{"type":"tool","name":"get_weather","disable_parallel_tool_use":true}`,
			wantChoice: map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}, wantParallel: false},
		{name: "explicit parallel allowed stays default", toolChoice: `{"type":"any","disable_parallel_tool_use":false}`,
			wantChoice: "required"},
		// "none" forbids tool calls, so a parallel limit is meaningless.
		{name: "none ignores disable parallel", toolChoice: `{"type":"none","disable_parallel_tool_use":true}`,
			wantChoice: "none"},
		{name: "tool without name dropped", toolChoice: `{"type":"tool"}`},
		{name: "unknown type dropped", toolChoice: `{"type":"sometimes"}`},
		{name: "non-object dropped", toolChoice: `"auto"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := convertClaudeJSON(t, `{"model":"m","max_tokens":10,`+toolsJSON+`,"tool_choice":`+tc.toolChoice+`,"messages":[{"role":"user","content":"hi"}]}`)
			choice, hasChoice := upstream["tool_choice"]
			if tc.wantChoice == nil {
				assert.False(t, hasChoice, "tool_choice must be absent, got %v", choice)
			} else {
				assert.Equal(t, tc.wantChoice, choice)
			}
			parallel, hasParallel := upstream["parallel_tool_calls"]
			if tc.wantParallel == nil {
				assert.False(t, hasParallel, "parallel_tool_calls must be absent, got %v", parallel)
			} else {
				assert.Equal(t, tc.wantParallel, parallel)
			}
		})
	}
}

func TestReq_ToolChoiceAbsentStaysAbsent(t *testing.T) {
	upstream := convertClaudeJSON(t, `{"model":"m","max_tokens":10,`+toolsJSON+`,"messages":[{"role":"user","content":"hi"}]}`)
	assert.NotContains(t, upstream, "tool_choice")
	assert.NotContains(t, upstream, "parallel_tool_calls")
	assert.Contains(t, upstream, "tools")
}

// OpenAI rejects tool_choice / parallel_tool_calls without tools, so a
// tool_choice that has nothing to choose from is not forwarded.
func TestReq_ToolChoiceWithoutToolsDropped(t *testing.T) {
	upstream := convertClaudeJSON(t, `{"model":"m","max_tokens":10,"tool_choice":{"type":"any","disable_parallel_tool_use":true},"messages":[{"role":"user","content":"hi"}]}`)
	assert.NotContains(t, upstream, "tool_choice")
	assert.NotContains(t, upstream, "parallel_tool_calls")
	assert.NotContains(t, upstream, "tools")
}

// In-process callers may build the request with the typed struct instead of a
// parsed JSON map.
func TestReq_ToolChoiceTypedStruct(t *testing.T) {
	for _, choice := range []any{
		&dto.ClaudeToolChoice{Type: "tool", Name: "get_weather", DisableParallelToolUse: true},
		dto.ClaudeToolChoice{Type: "tool", Name: "get_weather", DisableParallelToolUse: true},
	} {
		out, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{Model: "m", Tools: weatherTool(), ToolChoice: choice}, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}, out.ToolChoice)
		require.NotNil(t, out.ParallelTooCalls)
		assert.False(t, *out.ParallelTooCalls)
	}
}
