package dto

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GetOpenAIError — every dynamic-error branch
// ---------------------------------------------------------------------------

func TestGetOpenAIError_AllBranches(t *testing.T) {
	// nil => nil
	assert.Nil(t, GetOpenAIError(nil))

	// value type
	e := GetOpenAIError(types.OpenAIError{Type: "t", Message: "m"})
	require.NotNil(t, e)
	assert.Equal(t, "t", e.Type)
	assert.Equal(t, "m", e.Message)

	// pointer type
	pe := &types.OpenAIError{Type: "pt", Message: "pm"}
	assert.Equal(t, pe, GetOpenAIError(pe))

	// map form (from JSON decode)
	m := GetOpenAIError(map[string]interface{}{
		"type": "invalid_request_error", "message": "bad", "param": "model", "code": "c1",
	})
	require.NotNil(t, m)
	assert.Equal(t, "invalid_request_error", m.Type)
	assert.Equal(t, "bad", m.Message)
	assert.Equal(t, "model", m.Param)
	assert.Equal(t, "c1", m.Code)

	// map form with missing/wrong-typed fields => zero values, no panic
	m2 := GetOpenAIError(map[string]interface{}{"type": 5})
	require.NotNil(t, m2)
	assert.Equal(t, "", m2.Type)

	// string form
	s := GetOpenAIError("plain error")
	require.NotNil(t, s)
	assert.Equal(t, "error", s.Type)
	assert.Equal(t, "plain error", s.Message)

	// unknown type (default branch)
	u := GetOpenAIError(12345)
	require.NotNil(t, u)
	assert.Equal(t, "unknown_error", u.Type)
	assert.Contains(t, u.Message, "12345")
}

func TestSimpleResponse_GetOpenAIError(t *testing.T) {
	s := &SimpleResponse{Error: "boom"}
	e := s.GetOpenAIError()
	require.NotNil(t, e)
	assert.Equal(t, "boom", e.Message)
	assert.Nil(t, (&SimpleResponse{}).GetOpenAIError())
}

func TestOpenAITextResponse_GetOpenAIError(t *testing.T) {
	o := &OpenAITextResponse{Error: map[string]interface{}{"message": "x"}}
	require.NotNil(t, o.GetOpenAIError())
	assert.Nil(t, (&OpenAITextResponse{}).GetOpenAIError())
}

// ---------------------------------------------------------------------------
// ChatCompletionsStreamResponseChoiceDelta
// ---------------------------------------------------------------------------

func TestStreamDelta_ContentString(t *testing.T) {
	d := &ChatCompletionsStreamResponseChoiceDelta{}
	assert.Equal(t, "", d.GetContentString(), "nil => empty")
	d.SetContentString("hi")
	require.NotNil(t, d.Content)
	assert.Equal(t, "hi", d.GetContentString())
}

func TestStreamDelta_ReasoningContent(t *testing.T) {
	d := &ChatCompletionsStreamResponseChoiceDelta{}
	assert.Equal(t, "", d.GetReasoningContent())
	d.SetReasoningContent("r")
	require.NotNil(t, d.ReasoningContent)
	assert.Equal(t, "r", d.GetReasoningContent())

	// Reasoning fallback when only Reasoning set
	rs := "fallback"
	d2 := &ChatCompletionsStreamResponseChoiceDelta{Reasoning: &rs}
	assert.Equal(t, "fallback", d2.GetReasoningContent())
}

func TestToolCallResponse_SetIndex(t *testing.T) {
	tc := &ToolCallResponse{}
	assert.Nil(t, tc.Index)
	tc.SetIndex(3)
	require.NotNil(t, tc.Index)
	assert.Equal(t, 3, *tc.Index)
}

// ---------------------------------------------------------------------------
// ChatCompletionsStreamResponse
// ---------------------------------------------------------------------------

func newStreamResp(finish *string, toolCalls []ToolCallResponse) *ChatCompletionsStreamResponse {
	return &ChatCompletionsStreamResponse{
		Choices: []ChatCompletionsStreamResponseChoice{
			{
				FinishReason: finish,
				Delta:        ChatCompletionsStreamResponseChoiceDelta{ToolCalls: toolCalls},
			},
		},
	}
}

func TestStreamResponse_IsFinished(t *testing.T) {
	// no choices
	assert.False(t, (&ChatCompletionsStreamResponse{}).IsFinished())
	// finish reason nil
	assert.False(t, newStreamResp(nil, nil).IsFinished())
	// finish reason empty string
	empty := ""
	assert.False(t, newStreamResp(&empty, nil).IsFinished())
	// finish reason set
	stop := "stop"
	assert.True(t, newStreamResp(&stop, nil).IsFinished())
}

func TestStreamResponse_IsToolCall_GetFirst(t *testing.T) {
	// no choices
	assert.False(t, (&ChatCompletionsStreamResponse{}).IsToolCall())
	assert.Nil(t, (&ChatCompletionsStreamResponse{}).GetFirstToolCall())
	// choices but no tool calls
	assert.False(t, newStreamResp(nil, nil).IsToolCall())
	assert.Nil(t, newStreamResp(nil, nil).GetFirstToolCall())
	// with tool calls
	r := newStreamResp(nil, []ToolCallResponse{{ID: "id1", Function: FunctionResponse{Name: "fn"}}})
	assert.True(t, r.IsToolCall())
	first := r.GetFirstToolCall()
	require.NotNil(t, first)
	assert.Equal(t, "id1", first.ID)
}

func TestStreamResponse_ClearToolCalls(t *testing.T) {
	// no tool calls => no-op, no panic
	r0 := newStreamResp(nil, nil)
	r0.ClearToolCalls()

	idx := 2
	r := newStreamResp(nil, []ToolCallResponse{
		{Index: &idx, ID: "id", Type: "function", Function: FunctionResponse{Name: "fn", Arguments: "{}"}},
	})
	r.ClearToolCalls()
	tc := r.Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "", tc.ID)
	assert.Nil(t, tc.Type)
	assert.Equal(t, "", tc.Function.Name)
	// index and arguments preserved
	require.NotNil(t, tc.Index)
	assert.Equal(t, "{}", tc.Function.Arguments)
}

func TestStreamResponse_Copy(t *testing.T) {
	fp := "fp"
	u := &Usage{PromptTokens: 3}
	stop := "stop"
	orig := &ChatCompletionsStreamResponse{
		Id: "i", Object: "o", Created: 1, Model: "m",
		SystemFingerprint: &fp,
		Choices:           []ChatCompletionsStreamResponseChoice{{FinishReason: &stop}},
		Usage:             u,
	}
	cp := orig.Copy()
	assert.Equal(t, orig.Id, cp.Id)
	assert.Equal(t, orig.Model, cp.Model)
	assert.Len(t, cp.Choices, 1)
	assert.Same(t, orig.Usage, cp.Usage, "usage pointer shared by design")
	// mutating copied slice does not affect original slice element identity
	newStop := "length"
	cp.Choices[0].FinishReason = &newStop
	assert.Equal(t, "stop", *orig.Choices[0].FinishReason)
}

func TestStreamResponse_SystemFingerprint(t *testing.T) {
	r := &ChatCompletionsStreamResponse{}
	assert.Equal(t, "", r.GetSystemFingerprint(), "nil => empty")
	r.SetSystemFingerprint("abc")
	require.NotNil(t, r.SystemFingerprint)
	assert.Equal(t, "abc", r.GetSystemFingerprint())
}

// ---------------------------------------------------------------------------
// InputTokenDetails.CacheCreationTokensTotal
// ---------------------------------------------------------------------------

func TestInputTokenDetails_CacheCreationTokensTotal(t *testing.T) {
	// both zero
	assert.Equal(t, 0, InputTokenDetails{}.CacheCreationTokensTotal())
	// CachedCreationTokens only
	assert.Equal(t, 5, InputTokenDetails{CachedCreationTokens: 5}.CacheCreationTokensTotal())
	// CacheWriteTokens larger => wins
	assert.Equal(t, 9, InputTokenDetails{CachedCreationTokens: 4, CacheWriteTokens: 9}.CacheCreationTokensTotal())
	// CachedCreationTokens larger => wins
	assert.Equal(t, 7, InputTokenDetails{CachedCreationTokens: 7, CacheWriteTokens: 2}.CacheCreationTokensTotal())
	// negative clamps to 0
	assert.Equal(t, 0, InputTokenDetails{CachedCreationTokens: -3}.CacheCreationTokensTotal())
	assert.Equal(t, 0, InputTokenDetails{CachedCreationTokens: -3, CacheWriteTokens: -1}.CacheCreationTokensTotal())
}

// ---------------------------------------------------------------------------
// OpenAIResponsesResponse
// ---------------------------------------------------------------------------

func TestOpenAIResponsesResponse_GetOpenAIError(t *testing.T) {
	o := &OpenAIResponsesResponse{Error: "err"}
	require.NotNil(t, o.GetOpenAIError())
	assert.Nil(t, (&OpenAIResponsesResponse{}).GetOpenAIError())
}

// ---------------------------------------------------------------------------
// ResponsesOutput.ArgumentsString / ResponsesArgumentsString
// ---------------------------------------------------------------------------

func TestResponsesArgumentsString(t *testing.T) {
	// nil receiver
	var nilOut *ResponsesOutput
	assert.Equal(t, "", nilOut.ArgumentsString())

	// JSON string value decoded to its content
	out := &ResponsesOutput{Arguments: json.RawMessage(`"{\"a\":1}"`)}
	assert.Equal(t, `{"a":1}`, out.ArgumentsString())

	// raw object returned as text
	out2 := &ResponsesOutput{Arguments: json.RawMessage(`{"a":1}`)}
	assert.Equal(t, `{"a":1}`, out2.ArgumentsString())

	// empty / null
	assert.Equal(t, "", ResponsesArgumentsString(nil))
	assert.Equal(t, "", ResponsesArgumentsString(json.RawMessage(`null`)))
}
