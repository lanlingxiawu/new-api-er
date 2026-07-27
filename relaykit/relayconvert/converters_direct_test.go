package relayconvert

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Stream orchestration error / empty-value propagation.
//
// These drive fabricated ResponseStreamState values (white-box) so we can
// exercise the multi-step chunk/finalize orchestration branches that builtin
// converters never trigger.
// ---------------------------------------------------------------------------

func TestStreamOrchestrationErrorAndEmptyPaths(t *testing.T) {
	fakeTo := types.RelayFormat("fake-target")
	mid := types.RelayFormat("fake-mid")
	sentinel := errors.New("chunk fail")

	// ConvertStreamResponseChunk propagates a chunk-step error.
	failState := &ResponseStreamState{
		From: types.RelayFormatOpenAI, To: fakeTo,
		specs: []ResponseConverterSpec{{
			ID: "fail", From: types.RelayFormatOpenAI, To: fakeTo,
			ConvertStreamChunk: func(_ context.Context, _ convmeta.Meta, _ any, _ any) ([]any, *dto.Usage, error) {
				return nil, nil, sentinel
			},
		}},
		stepStates: []any{nil},
	}
	_, err := ConvertStreamResponseChunk(nil, nil, failState, &dto.ChatCompletionsStreamResponse{})
	require.ErrorIs(t, err, sentinel)

	// executeResponseStreamSteps returns early when an intermediate step yields
	// no values; the subsequent step must not run.
	emptyState := &ResponseStreamState{
		From: types.RelayFormatOpenAI, To: fakeTo,
		specs: []ResponseConverterSpec{
			{ID: "empty", From: types.RelayFormatOpenAI, To: mid, ConvertStreamChunk: func(_ context.Context, _ convmeta.Meta, _ any, _ any) ([]any, *dto.Usage, error) {
				return nil, nil, nil
			}},
			{ID: "never", From: mid, To: fakeTo, ConvertStreamChunk: func(_ context.Context, _ convmeta.Meta, _ any, _ any) ([]any, *dto.Usage, error) {
				t.Fatal("second step should not run after empty output")
				return nil, nil, nil
			}},
		},
		stepStates: []any{nil, nil},
	}
	results, err := ConvertStreamResponseChunk(nil, nil, emptyState, &dto.ChatCompletionsStreamResponse{})
	require.NoError(t, err)
	assert.Empty(t, results)

	// FinalizeStreamResponse propagates a finalize error.
	finErrState := &ResponseStreamState{
		From: types.RelayFormatOpenAI, To: fakeTo,
		specs: []ResponseConverterSpec{{ID: "fin", From: types.RelayFormatOpenAI, To: fakeTo, FinalizeStream: func(_ context.Context, _ convmeta.Meta, _ any) ([]any, *dto.Usage, error) {
			return nil, nil, sentinel
		}}},
		stepStates: []any{nil},
	}
	_, err = FinalizeStreamResponse(nil, nil, finErrState)
	require.ErrorIs(t, err, sentinel)

	// FinalizeStreamResponse emits finalize output (and remembers its usage).
	finState := &ResponseStreamState{
		From: types.RelayFormatOpenAI, To: fakeTo,
		specs: []ResponseConverterSpec{{ID: "fin2", From: types.RelayFormatOpenAI, To: fakeTo, FinalizeStream: func(_ context.Context, _ convmeta.Meta, _ any) ([]any, *dto.Usage, error) {
			return []any{"done"}, &dto.Usage{TotalTokens: 3}, nil
		}}},
		stepStates: []any{nil},
	}
	results, err = FinalizeStreamResponse(nil, nil, finState)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "done", results[0].Value)
	require.NotNil(t, finState.Usage())
	assert.Equal(t, 3, finState.Usage().TotalTokens)

	// executeResponseSteps (non-stream) propagates a step error.
	_, err = executeResponseSteps(nil, nil, types.RelayFormatOpenAI, fakeTo, &dto.OpenAITextResponse{}, "", ResponseConverterQualityFair,
		[]ResponseConverterSpec{{ID: "f", From: types.RelayFormatOpenAI, To: fakeTo, Convert: func(_ context.Context, _ convmeta.Meta, _ any) (any, *dto.Usage, error) {
			return nil, nil, sentinel
		}}})
	require.ErrorIs(t, err, sentinel)

	// executeResponseStreamStep propagates a ConvertStream error (non-chunk path).
	_, _, err = executeResponseStreamStep(nil, nil, ResponseConverterSpec{ID: "s", ConvertStream: func(_ context.Context, _ convmeta.Meta, _ any) (any, *dto.Usage, error) {
		return nil, nil, sentinel
	}}, nil, &dto.OpenAITextResponse{})
	require.ErrorIs(t, err, sentinel)

	// FinalizeStreamResponse propagates an error from a later chunk step that
	// consumes the finalize output.
	finLaterErr := &ResponseStreamState{
		From: types.RelayFormatOpenAI, To: fakeTo,
		specs: []ResponseConverterSpec{
			{ID: "fin", From: types.RelayFormatOpenAI, To: mid, FinalizeStream: func(_ context.Context, _ convmeta.Meta, _ any) ([]any, *dto.Usage, error) {
				return []any{"x"}, nil, nil
			}},
			{ID: "chunkfail", From: mid, To: fakeTo, ConvertStreamChunk: func(_ context.Context, _ convmeta.Meta, _ any, _ any) ([]any, *dto.Usage, error) {
				return nil, nil, sentinel
			}},
		},
		stepStates: []any{nil, nil},
	}
	_, err = FinalizeStreamResponse(nil, nil, finLaterErr)
	require.ErrorIs(t, err, sentinel)
}

// ---------------------------------------------------------------------------
// Request converter functions: value-type acceptance + wrong-type error.
// ---------------------------------------------------------------------------

func TestRequestConverterFuncValueAndErrorBranches(t *testing.T) {
	c := context.Background()
	// OpenAI→Claude 需要 max_tokens；源请求没带时由 DefaultMaxTokens 钩子注入。
	info := &convmeta.Values{Options: &convmeta.Options{
		Claude: convmeta.ClaudeOptions{DefaultMaxTokens: func(string) int { return 8192 }},
	}}

	type reqFn func(context.Context, convmeta.Meta, any) (any, error)
	cases := []struct {
		name   string
		fn     reqFn
		value  any // value-typed (non-pointer) valid request
		badMsg string
	}{
		{"chatToResponses", convertChatRequestToResponses, *newOpenAIRequest(), "expected OpenAI chat completions request"},
		{"claudeToOpenAI", convertClaudeRequestToOpenAI, *newClaudeRequest(), "expected Anthropic Messages request"},
		{"openAIToClaude", convertOpenAIRequestToClaude, *newOpenAIRequest(), "expected OpenAI chat completions request"},
		{"geminiToOpenAI", convertGeminiRequestToOpenAI, *newGeminiRequest(), "expected Gemini generateContent request"},
		{"openAIToGemini", convertOpenAIRequestToGemini, *newOpenAIRequest(), "expected OpenAI chat completions request"},
		{"responsesToChat", convertResponsesRequestToChat, *newResponsesRequest(t), "expected OpenAI responses request"},
	}
	for _, tt := range cases {
		t.Run(tt.name+"/value", func(t *testing.T) {
			out, err := tt.fn(c, info, tt.value)
			require.NoError(t, err)
			require.NotNil(t, out)
		})
		t.Run(tt.name+"/wrongType", func(t *testing.T) {
			_, err := tt.fn(c, info, 12345)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.badMsg)
		})
	}
}

// OpenAIResponsesRequestFromAny-based converters surface a parse error on a
// wrong input type.
func TestResponsesRequestConvertersErrorOnBadType(t *testing.T) {
	c := context.Background()
	info := &convmeta.Values{}

	_, err := convertOpenAIResponsesRequestToClaudeMessages(c, info, 999)
	require.Error(t, err)

	_, err = convertOpenAIResponsesRequestToGeminiChat(c, info, 999)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Response converter functions: wrong-type error branch (value already covered
// through the registry routes and the as* helper tests).
// ---------------------------------------------------------------------------

func TestResponseConverterFuncErrorBranches(t *testing.T) {
	c := context.Background()
	info := &convmeta.Values{}

	type respFn func(context.Context, convmeta.Meta, any) (any, *dto.Usage, error)
	fns := map[string]respFn{
		"oaiChatToResponses":    convertOAIChatResponseToOAIResponses,
		"oaiResponsesToChat":    convertOAIResponsesResponseToOAIChat,
		"oaiChatToClaude":       convertOAIChatResponseToClaudeMessages,
		"oaiChatStreamToClaude": convertOAIChatStreamResponseToClaudeMessages,
		"oaiChatToGemini":       convertOAIChatResponseToGeminiChat,
		"oaiChatStreamToGemini": convertOAIChatStreamResponseToGeminiChat,
		"claudeToOaiChat":       convertClaudeMessagesResponseToOAIChat,
		"claudeStreamToOaiChat": convertClaudeMessagesStreamResponseToOAIChat,
		"geminiToOaiChat":       convertGeminiChatResponseToOAIChat,
		"geminiStreamToOaiChat": convertGeminiChatStreamResponseToOAIChat,
	}
	for name, fn := range fns {
		t.Run(name, func(t *testing.T) {
			_, _, err := fn(c, info, "wrong-type")
			require.Error(t, err)
		})
	}
}

// Non-stream chunk converters error when given the wrong stream-state type.
func TestStreamChunkConvertersRejectBadState(t *testing.T) {
	c := context.Background()
	info := &convmeta.Values{}

	_, _, err := convertOAIChatStreamResponseToOAIResponses(c, info, &dto.ChatCompletionsStreamResponse{}, "bad-state")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream state is required")

	_, _, err = finalizeOAIChatStreamResponseToOAIResponses(c, info, "bad-state")
	require.Error(t, err)

	_, _, err = convertOAIResponsesStreamResponseToOAIChat(c, info, &dto.ResponsesStreamResponse{}, "bad-state")
	require.Error(t, err)

	_, _, err = finalizeOAIResponsesStreamResponseToOAIChat(c, info, "bad-state")
	require.Error(t, err)
}

// Chunk converters also error when handed the wrong response type.
func TestStreamChunkConvertersRejectBadResponse(t *testing.T) {
	c := context.Background()
	info := &convmeta.Values{}

	chatState := NewChatToResponsesStreamState("resp_1", "gpt-test")
	_, _, err := convertOAIChatStreamResponseToOAIResponses(c, info, "wrong", chatState)
	require.Error(t, err)

	respState := NewResponsesToChatStreamState("gpt-test", false)
	_, _, err = convertOAIResponsesStreamResponseToOAIChat(c, info, "wrong", respState)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Stream-state factory branches: empty ID => generated, Created override.
// ---------------------------------------------------------------------------

func TestChatToResponsesStreamStateFactoryBranches(t *testing.T) {
	// Empty ID triggers UUID generation; Created override is applied.
	state, err := NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, ResponseStreamOptions{
		ID:      "",
		Model:   "gpt-test",
		Created: 123,
	})
	require.NoError(t, err)
	chatState, ok := state.stepStates[0].(*ChatToResponsesStreamState)
	require.True(t, ok)
	assert.NotEmpty(t, chatState.ID)
	assert.Equal(t, int64(123), chatState.Created)
}

func TestResponsesToChatStreamStateFactoryBranches(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, ResponseStreamOptions{
		ID:           "chatcmpl_1",
		Model:        "gpt-test",
		Created:      456,
		IncludeUsage: true,
	})
	require.NoError(t, err)
	respState, ok := state.stepStates[0].(*ResponsesToChatStreamState)
	require.True(t, ok)
	assert.Equal(t, "chatcmpl_1", respState.ID)
	assert.Equal(t, int64(456), respState.Created)
}

// convertOAIChatResponseToOAIResponses / convertOAIResponsesResponseToOAIChat
// generate an ID when the source response carries none.
func TestNonStreamResponseConvertersGenerateID(t *testing.T) {
	c := context.Background()
	info := &convmeta.Values{}

	chat := textRegistryChatResponse()
	chat.Id = "" // force generated resp_ id
	out, _, err := convertOAIChatResponseToOAIResponses(c, info, chat)
	require.NoError(t, err)
	respOut, ok := out.(*dto.OpenAIResponsesResponse)
	require.True(t, ok)
	assert.Contains(t, respOut.ID, "resp_")

	responses := textRegistryResponsesResponse()
	responses.ID = "" // force generated chatcmpl- id
	out, _, err = convertOAIResponsesResponseToOAIChat(c, info, responses)
	require.NoError(t, err)
	chatOut, ok := out.(*dto.OpenAITextResponse)
	require.True(t, ok)
	assert.Contains(t, chatOut.Id, "chatcmpl-")
}
