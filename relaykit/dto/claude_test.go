package dto

import (
	"encoding/json"
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ClaudeMediaMessage
// ---------------------------------------------------------------------------

func TestClaudeMediaMessage_Text(t *testing.T) {
	m := &ClaudeMediaMessage{}
	assert.Equal(t, "", m.GetText())
	m.SetText("hi")
	require.NotNil(t, m.Text)
	assert.Equal(t, "hi", m.GetText())
}

func TestClaudeMediaMessage_IsStringContent(t *testing.T) {
	assert.False(t, (&ClaudeMediaMessage{}).IsStringContent(), "nil content => false")
	assert.True(t, (&ClaudeMediaMessage{Content: "s"}).IsStringContent())
	assert.False(t, (&ClaudeMediaMessage{Content: []any{}}).IsStringContent())
}

func TestClaudeMediaMessage_GetStringContent(t *testing.T) {
	assert.Equal(t, "", (&ClaudeMediaMessage{}).GetStringContent())
	assert.Equal(t, "s", (&ClaudeMediaMessage{Content: "s"}).GetStringContent())
	m := &ClaudeMediaMessage{Content: []any{
		map[string]any{"type": "text", "text": "a"},
		map[string]any{"type": "text", "text": "b"},
		map[string]any{"type": "image"}, // ignored
		"str",                           // ignored
	}}
	assert.Equal(t, "ab", m.GetStringContent())
	// unsupported content type
	assert.Equal(t, "", (&ClaudeMediaMessage{Content: 5}).GetStringContent())
}

func TestClaudeMediaMessage_SetContentAndParse(t *testing.T) {
	m := &ClaudeMediaMessage{}
	m.SetContent([]map[string]any{{"type": "text", "text": "x"}})
	parsed := m.ParseMediaContent()
	require.Len(t, parsed, 1)
	assert.Equal(t, "text", parsed[0].Type)
	assert.Equal(t, "x", parsed[0].GetText())
}

func TestClaudeMediaMessage_GetJsonRowString(t *testing.T) {
	m := &ClaudeMediaMessage{Type: "text"}
	m.SetText("hi")
	s := m.GetJsonRowString()
	assert.Contains(t, s, `"text":"hi"`)
	assert.Contains(t, s, `"type":"text"`)
}

func TestClaudeMediaMessage_ToFileSource(t *testing.T) {
	// nil source
	assert.Nil(t, (&ClaudeMediaMessage{}).ToFileSource())
	// url source
	src := (&ClaudeMediaMessage{Source: &ClaudeMessageSource{Url: "https://x/a.png", MediaType: "image/png"}}).ToFileSource()
	require.NotNil(t, src)
	assert.True(t, src.IsURL())
	// data source (base64)
	src = (&ClaudeMediaMessage{Source: &ClaudeMessageSource{Data: "AAAA", MediaType: "image/png"}}).ToFileSource()
	require.NotNil(t, src)
	assert.False(t, src.IsURL())
	// empty (no url, no data) => nil
	assert.Nil(t, (&ClaudeMediaMessage{Source: &ClaudeMessageSource{}}).ToFileSource())
}

// ---------------------------------------------------------------------------
// ClaudeMessage
// ---------------------------------------------------------------------------

func TestClaudeMessage_StringContent(t *testing.T) {
	assert.False(t, (&ClaudeMessage{}).IsStringContent())
	assert.True(t, (&ClaudeMessage{Content: "s"}).IsStringContent())

	assert.Equal(t, "", (&ClaudeMessage{}).GetStringContent())
	assert.Equal(t, "hi", (&ClaudeMessage{Content: "hi"}).GetStringContent())
	m := &ClaudeMessage{Content: []any{
		map[string]any{"type": "text", "text": "a"},
		map[string]any{"type": "text", "text": "b"},
	}}
	assert.Equal(t, "ab", m.GetStringContent())
	assert.Equal(t, "", (&ClaudeMessage{Content: 1}).GetStringContent())
}

func TestClaudeMessage_SetContent(t *testing.T) {
	m := &ClaudeMessage{}
	m.SetStringContent("x")
	assert.Equal(t, "x", m.Content)
	m.SetContent([]any{"y"})
	assert.Equal(t, []any{"y"}, m.Content)
}

func TestClaudeMessage_ParseContent(t *testing.T) {
	m := &ClaudeMessage{Content: []map[string]any{
		{"type": "text", "text": "hello"},
		{"type": "image", "source": map[string]any{"type": "base64", "data": "AAAA", "media_type": "image/png"}},
	}}
	content, err := m.ParseContent()
	require.NoError(t, err)
	require.Len(t, content, 2)
	assert.Equal(t, "hello", content[0].GetText())
	assert.Equal(t, "image", content[1].Type)
}

// ---------------------------------------------------------------------------
// ClaudeRequest
// ---------------------------------------------------------------------------

func TestClaudeRequest_IsStream(t *testing.T) {
	tr := true
	fa := false
	assert.False(t, (&ClaudeRequest{}).IsStream(nil))
	assert.True(t, (&ClaudeRequest{Stream: &tr}).IsStream(nil))
	assert.False(t, (&ClaudeRequest{Stream: &fa}).IsStream(nil))
}

func TestClaudeRequest_SetModelName(t *testing.T) {
	r := &ClaudeRequest{Model: "a"}
	r.SetModelName("")
	assert.Equal(t, "a", r.Model)
	r.SetModelName("b")
	assert.Equal(t, "b", r.Model)
}

func TestClaudeRequest_Rule5Pointers(t *testing.T) {
	// absent optional scalars => nil
	var r ClaudeRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"model":"claude"}`), &r))
	assert.Nil(t, r.MaxTokens)
	assert.Nil(t, r.Temperature)
	assert.Nil(t, r.TopP)
	assert.Nil(t, r.TopK)
	assert.Nil(t, r.Stream)

	// explicit zero => non-nil, sent on marshal
	var r2 ClaudeRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"model":"claude","max_tokens":0,"temperature":0,"top_k":0,"stream":false}`), &r2))
	require.NotNil(t, r2.MaxTokens)
	assert.Equal(t, uint(0), *r2.MaxTokens)
	require.NotNil(t, r2.Temperature)
	require.NotNil(t, r2.TopK)
	require.NotNil(t, r2.Stream)

	out, err := kitutil.Marshal(&r2)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, kitutil.Unmarshal(out, &m))
	assert.JSONEq(t, "0", string(m["max_tokens"]))
	assert.JSONEq(t, "0", string(m["temperature"]))
	assert.JSONEq(t, "false", string(m["stream"]))
}

func TestClaudeRequest_System(t *testing.T) {
	// string system
	r := &ClaudeRequest{}
	assert.False(t, r.IsStringSystem())
	assert.Equal(t, "", r.GetStringSystem())
	r.SetStringSystem("you are helpful")
	assert.True(t, r.IsStringSystem())
	assert.Equal(t, "you are helpful", r.GetStringSystem())

	// array system => ParseSystem
	r2 := &ClaudeRequest{System: []map[string]any{{"type": "text", "text": "sys"}}}
	assert.False(t, r2.IsStringSystem())
	parsed := r2.ParseSystem()
	require.Len(t, parsed, 1)
	assert.Equal(t, "sys", parsed[0].GetText())
}

func TestClaudeRequest_ToolsAddGet(t *testing.T) {
	r := &ClaudeRequest{}
	assert.Nil(t, r.GetTools())
	r.AddTool(Tool{Name: "t1"})
	r.AddTool(Tool{Name: "t2"})
	tools := r.GetTools()
	require.Len(t, tools, 2)

	// If Tools is a non-[]any type, AddTool re-initializes
	r2 := &ClaudeRequest{Tools: "not-a-slice"}
	assert.Nil(t, r2.GetTools(), "non-slice => nil")
	r2.AddTool(Tool{Name: "x"})
	require.Len(t, r2.GetTools(), 1)
}

func TestClaudeRequest_GetEfforts(t *testing.T) {
	// valid output config
	r := &ClaudeRequest{OutputConfig: json.RawMessage(`{"effort":"high"}`)}
	assert.Equal(t, "high", r.GetEfforts())
	// empty output config => "" (unmarshal of empty fails silently)
	assert.Equal(t, "", (&ClaudeRequest{}).GetEfforts())
	// object without effort
	assert.Equal(t, "", (&ClaudeRequest{OutputConfig: json.RawMessage(`{"x":1}`)}).GetEfforts())
}

func TestClaudeRequest_SearchToolNameByToolCallId(t *testing.T) {
	r := &ClaudeRequest{Messages: []ClaudeMessage{
		{Role: "assistant", Content: []map[string]any{
			{"type": "tool_use", "id": "call_1", "name": "search"},
		}},
	}}
	assert.Equal(t, "search", r.SearchToolNameByToolCallId("call_1"))
	assert.Equal(t, "", r.SearchToolNameByToolCallId("missing"))
}

func TestClaudeRequest_GetTokenCountMeta(t *testing.T) {
	maxTok := uint(200)
	r := &ClaudeRequest{
		MaxTokens: &maxTok,
		System:    "system prompt",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "plain text"},
			{Role: "user", Content: []map[string]any{
				{"type": "text", "text": "detail"},
				{"type": "image", "source": map[string]any{"type": "base64", "data": "AAAA", "media_type": "image/png"}},
				{"type": "tool_use", "name": "calc", "input": map[string]any{"x": 1}},
				{"type": "tool_result", "content": "42"},
			}},
		},
		Tools: []any{
			Tool{Name: "calc", Description: "calculator", InputSchema: map[string]any{"type": "object"}},
			ClaudeWebSearchTool{Type: "web_search", Name: "web", UserLocation: &ClaudeWebSearchUserLocation{Type: "approximate", City: "NY"}},
		},
	}
	meta := r.GetTokenCountMeta()
	assert.Equal(t, types.TokenTypeTokenizer, meta.TokenType)
	assert.Equal(t, 200, meta.MaxTokens)
	assert.Equal(t, 2, meta.MessagesCount)
	assert.Equal(t, 2, meta.ToolsCount)
	assert.Contains(t, meta.CombineText, "system prompt")
	assert.Contains(t, meta.CombineText, "plain text")
	assert.Contains(t, meta.CombineText, "detail")
	assert.Contains(t, meta.CombineText, "calc")
	assert.Contains(t, meta.CombineText, "calculator")
	assert.Contains(t, meta.CombineText, "web")
	assert.Len(t, meta.Files, 1) // one image
}

func TestClaudeRequest_GetTokenCountMeta_ArraySystem(t *testing.T) {
	r := &ClaudeRequest{
		System: []map[string]any{
			{"type": "text", "text": "sys-text"},
			{"type": "image", "source": map[string]any{"type": "base64", "data": "AAAA", "media_type": "image/png"}},
		},
	}
	meta := r.GetTokenCountMeta()
	assert.Contains(t, meta.CombineText, "sys-text")
	assert.Len(t, meta.Files, 1)
}

func TestClaudeRequest_GetTokenCountMeta_NilMaxTokens(t *testing.T) {
	meta := (&ClaudeRequest{}).GetTokenCountMeta()
	assert.Equal(t, 0, meta.MaxTokens)
}

// ---------------------------------------------------------------------------
// Thinking
// ---------------------------------------------------------------------------

func TestThinking_GetBudgetTokens(t *testing.T) {
	assert.Equal(t, 0, (&Thinking{}).GetBudgetTokens())
	b := 512
	assert.Equal(t, 512, (&Thinking{BudgetTokens: &b}).GetBudgetTokens())
}

// ---------------------------------------------------------------------------
// ProcessTools
// ---------------------------------------------------------------------------

func TestProcessTools(t *testing.T) {
	tools := []any{
		&Tool{Name: "ptr-tool"},
		Tool{Name: "val-tool"},
		&ClaudeWebSearchTool{Name: "ptr-web"},
		ClaudeWebSearchTool{Name: "val-web"},
		"unknown", // skipped
		42,        // skipped
	}
	normal, web := ProcessTools(tools)
	require.Len(t, normal, 2)
	require.Len(t, web, 2)
	assert.Equal(t, "ptr-tool", normal[0].Name)
	assert.Equal(t, "val-tool", normal[1].Name)
	assert.Equal(t, "ptr-web", web[0].Name)
	assert.Equal(t, "val-web", web[1].Name)

	// empty
	n2, w2 := ProcessTools(nil)
	assert.Nil(t, n2)
	assert.Nil(t, w2)
}

// ---------------------------------------------------------------------------
// ClaudeResponse
// ---------------------------------------------------------------------------

func TestClaudeResponse_Index(t *testing.T) {
	c := &ClaudeResponse{}
	assert.Equal(t, 0, c.GetIndex(), "nil => 0")
	c.SetIndex(4)
	require.NotNil(t, c.Index)
	assert.Equal(t, 4, c.GetIndex())
}

func TestClaudeResponse_GetClaudeError(t *testing.T) {
	// nil
	assert.Nil(t, (&ClaudeResponse{}).GetClaudeError())
	// value
	e := (&ClaudeResponse{Error: types.ClaudeError{Type: "t", Message: "m"}}).GetClaudeError()
	require.NotNil(t, e)
	assert.Equal(t, "t", e.Type)
	// pointer
	pe := &types.ClaudeError{Type: "pt"}
	assert.Equal(t, pe, (&ClaudeResponse{Error: pe}).GetClaudeError())
	// map form
	m := (&ClaudeResponse{Error: map[string]interface{}{"type": "overloaded_error", "message": "busy"}}).GetClaudeError()
	require.NotNil(t, m)
	assert.Equal(t, "overloaded_error", m.Type)
	assert.Equal(t, "busy", m.Message)
	// string form
	s := (&ClaudeResponse{Error: "raw"}).GetClaudeError()
	require.NotNil(t, s)
	assert.Equal(t, "upstream_error", s.Type)
	assert.Equal(t, "raw", s.Message)
	// default branch
	u := (&ClaudeResponse{Error: 99}).GetClaudeError()
	require.NotNil(t, u)
	assert.Equal(t, "unknown_upstream_error", u.Type)
	assert.Contains(t, u.Message, "99")
}

// ---------------------------------------------------------------------------
// ClaudeUsage cache-creation helpers
// ---------------------------------------------------------------------------

func TestClaudeUsage_CacheCreationGetters(t *testing.T) {
	// nil receiver / nil CacheCreation
	var nilU *ClaudeUsage
	assert.Equal(t, 0, nilU.GetCacheCreation5mTokens())
	assert.Equal(t, 0, nilU.GetCacheCreation1hTokens())
	assert.Equal(t, 0, nilU.GetCacheCreationTotalTokens())

	u := &ClaudeUsage{}
	assert.Equal(t, 0, u.GetCacheCreation5mTokens())
	assert.Equal(t, 0, u.GetCacheCreation1hTokens())

	u.CacheCreation = &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 3, Ephemeral1hInputTokens: 7}
	assert.Equal(t, 3, u.GetCacheCreation5mTokens())
	assert.Equal(t, 7, u.GetCacheCreation1hTokens())
	// total falls back to 5m+1h when CacheCreationInputTokens==0
	assert.Equal(t, 10, u.GetCacheCreationTotalTokens())

	// CacheCreationInputTokens takes precedence when > 0
	u.CacheCreationInputTokens = 20
	assert.Equal(t, 20, u.GetCacheCreationTotalTokens())
}
