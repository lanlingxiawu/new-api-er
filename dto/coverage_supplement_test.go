package dto

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Supplemental cases targeting specific uncovered branches.

// ClaudeMessage.GetStringContent: []any with a non-map item and a text item
// whose "text" is not a string.
func TestClaudeMessage_GetStringContent_EdgeItems(t *testing.T) {
	m := &ClaudeMessage{Content: []any{
		"bare-string",                            // not a map => skipped
		map[string]any{"type": "text", "text": 5},// text not string => skipped
		map[string]any{"type": "text", "text": "kept"},
	}}
	assert.Equal(t, "kept", m.GetStringContent())
}

// ClaudeMediaMessage.GetStringContent same edge items.
func TestClaudeMediaMessage_GetStringContent_EdgeItems(t *testing.T) {
	m := &ClaudeMediaMessage{Content: []any{
		42,
		map[string]any{"type": "text", "text": nil},
		map[string]any{"type": "text", "text": "z"},
	}}
	assert.Equal(t, "z", m.GetStringContent())
}

// GeneralOpenAIRequest.GetTokenCountMeta: message content present but Name nil,
// plus a tool with empty description and nil parameters.
func TestGeneralOpenAIRequest_GetTokenCountMeta_NoNameEmptyTool(t *testing.T) {
	r := &GeneralOpenAIRequest{
		Messages: []Message{
			{Role: "user", Content: "hi"}, // Name nil, content present
		},
		Tools: []ToolCallRequest{
			{Type: "function", Function: FunctionRequest{Name: "f"}}, // no desc, no params
		},
	}
	meta := r.GetTokenCountMeta()
	assert.Equal(t, 0, meta.NameCount)
	assert.Equal(t, 1, meta.ToolsCount)
	assert.Contains(t, meta.CombineText, "hi")
	assert.Contains(t, meta.CombineText, "f")
}

// GeneralOpenAIRequest.GetTokenCountMeta with Input set (embeddings-style body).
func TestGeneralOpenAIRequest_GetTokenCountMeta_Input(t *testing.T) {
	r := &GeneralOpenAIRequest{Input: []any{"ia", "ib"}}
	meta := r.GetTokenCountMeta()
	assert.Contains(t, meta.CombineText, "ia")
	assert.Contains(t, meta.CombineText, "ib")
}

// OpenAIResponsesRequest.ParseInput: array content element that is not a map
// (a bare string) is skipped.
func TestOpenAIResponsesRequest_ParseInput_NonMapArrayItem(t *testing.T) {
	raw := `[{"role":"user","content":["bare-string",{"type":"input_text","text":"kept"}]}]`
	got := (&OpenAIResponsesRequest{Input: json.RawMessage(raw)}).ParseInput()
	require.Len(t, got, 1)
	assert.Equal(t, "kept", got[0].Text)
}

// ImageRequest.UnmarshalJSON: rawMap parses but the typed Alias parse fails
// (n must be *uint but is given a string).
func TestImageRequest_Unmarshal_TypedError(t *testing.T) {
	err := (&ImageRequest{}).UnmarshalJSON([]byte(`{"n":"not-a-number"}`))
	assert.Error(t, err)
}

// ImageRequest.MarshalJSON: a raw-message field holding invalid JSON makes the
// base Marshal fail.
func TestImageRequest_Marshal_InvalidRawMessage(t *testing.T) {
	r := ImageRequest{Model: "m", Prompt: "p", Style: json.RawMessage(`{invalid`)}
	_, err := r.MarshalJSON()
	assert.Error(t, err)
}

// GetJSONFieldNames: anonymous/embedded fields are skipped.
func TestGetJSONFieldNames_AnonymousSkipped(t *testing.T) {
	type Embedded struct {
		Z string `json:"z"`
	}
	type withAnon struct {
		Embedded        // anonymous => skipped
		A        string `json:"a"`
	}
	names := GetJSONFieldNames(reflect.TypeOf(withAnon{}))
	assert.Contains(t, names, "a")
	assert.NotContains(t, names, "z", "anonymous field names are not collected")
}

// matchAdvancedCustomIncomingPathTemplate: configured path with two {model}
// placeholders splits into != 2 parts => no match.
func TestAdvancedCustomConfig_MatchPath_DoublePlaceholder(t *testing.T) {
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/a/{model}/b/{model}"},
	}}
	_, ok := c.MatchPath("/a/x/b/y")
	assert.False(t, ok)
}

// GeminiChatRequest.SetTools with a value that marshals fine (already covered),
// here ensure marshalled bytes are assigned for a multi-tool slice.
func TestGeminiChatRequest_SetTools_MultipleAssigned(t *testing.T) {
	r := &GeminiChatRequest{}
	r.SetTools([]GeminiChatTool{{GoogleSearch: map[string]any{}}, {CodeExecution: map[string]any{}}})
	var parsed []GeminiChatTool
	require.NoError(t, common.Unmarshal(r.Tools, &parsed))
	assert.Len(t, parsed, 2)
}
