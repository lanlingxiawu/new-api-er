package geminichat

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chanTypeGemini 对应主仓库 constant.ChannelTypeGemini；relaykit 不引入渠道常量。
const chanTypeGemini = 24

func infoWith(channelType int, upstreamModel string, isStream bool) convmeta.Meta {
	return &convmeta.Values{
		IsStream:            isStream,
		ChannelMetaAttached: true,
		ChannelType:         channelType,
		UpstreamModelName:   upstreamModel,
	}
}

// --- convertGeminiRoleToOpenAI (decision coverage of switch) ---

func TestConvertRole(t *testing.T) {
	assert.Equal(t, "user", convertGeminiRoleToOpenAI("user"))
	assert.Equal(t, "assistant", convertGeminiRoleToOpenAI("model"))
	assert.Equal(t, "function", convertGeminiRoleToOpenAI("function"))
	assert.Equal(t, "user", convertGeminiRoleToOpenAI("unknown"))
	assert.Equal(t, "user", convertGeminiRoleToOpenAI(""))
}

// --- extractTextFromGeminiParts ---

func TestExtractText(t *testing.T) {
	parts := []dto.GeminiPart{
		{Text: "a"},
		{Text: ""},
		{Text: "b"},
	}
	assert.Equal(t, "a\nb", extractTextFromGeminiParts(parts))
	assert.Equal(t, "", extractTextFromGeminiParts(nil))
}

// --- model / stream propagation ---

func TestReq_ModelAndStreamFromInfo(t *testing.T) {
	out, err := GeminiGenerateContentRequestToOpenAIChat(&dto.GeminiChatRequest{}, infoWith(chanTypeGemini, "gemini-1.5-pro", true))
	require.NoError(t, err)
	assert.Equal(t, "gemini-1.5-pro", out.Model)
	require.NotNil(t, out.Stream)
	assert.True(t, *out.Stream)
}

func TestReq_NilInfoDefaults(t *testing.T) {
	out, err := GeminiGenerateContentRequestToOpenAIChat(&dto.GeminiChatRequest{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "", out.Model)
	require.NotNil(t, out.Stream)
	assert.False(t, *out.Stream)
}

// --- parts: text ---

func TestReq_SingleTextBecomesPlainStringContent(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	assert.Equal(t, "user", out.Messages[0].Role)
	// single text => plain string content
	assert.Equal(t, "hello", out.Messages[0].Content)
}

func TestReq_MultipleTextPartsBecomeMediaContent(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "a"}, {Text: "b"}}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	content := out.Messages[0].ParseContent()
	require.Len(t, content, 2)
	assert.Equal(t, "a", content[0].Text)
	assert.Equal(t, "b", content[1].Text)
}

// --- parts: inlineData / fileData ---

func TestReq_InlineDataImage(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "AAAA"}},
			}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	content := out.Messages[0].ParseContent()
	require.Len(t, content, 1)
	assert.Equal(t, "image_url", content[0].Type)
	img := content[0].GetImageMedia()
	require.NotNil(t, img)
	assert.Equal(t, "data:image/png;base64,AAAA", img.Url)
	assert.Equal(t, "auto", img.Detail)
	assert.Equal(t, "image/png", img.MimeType)
}

func TestReq_FileData(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{
				{FileData: &dto.GeminiFileData{MimeType: "image/jpeg", FileUri: "gs://bucket/x.jpg"}},
			}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	content := out.Messages[0].ParseContent()
	require.Len(t, content, 1)
	img := content[0].GetImageMedia()
	require.NotNil(t, img)
	assert.Equal(t, "gs://bucket/x.jpg", img.Url)
	assert.Equal(t, "image/jpeg", img.MimeType)
}

// --- parts: functionCall / functionResponse ---

func TestReq_FunctionCallBecomesToolCall(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "model", Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "get_weather", Arguments: map[string]any{"city": "SF"}}},
			}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	assert.Equal(t, "assistant", out.Messages[0].Role)
	tcs := out.Messages[0].ParseToolCalls()
	require.Len(t, tcs, 1)
	assert.Equal(t, "call_1", tcs[0].ID)
	assert.Equal(t, "function", tcs[0].Type)
	assert.Equal(t, "get_weather", tcs[0].Function.Name)
	assert.JSONEq(t, `{"city":"SF"}`, tcs[0].Function.Arguments)
}

func TestReq_MultipleFunctionCallsIncrementIds(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "model", Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "f1", Arguments: map[string]any{}}},
				{FunctionCall: &dto.FunctionCall{FunctionName: "f2", Arguments: map[string]any{}}},
			}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	tcs := out.Messages[0].ParseToolCalls()
	require.Len(t, tcs, 2)
	assert.Equal(t, "call_1", tcs[0].ID)
	assert.Equal(t, "call_2", tcs[1].ID)
}

func TestReq_FunctionResponseBecomesToolMessage(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "function", Parts: []dto.GeminiPart{
				{FunctionResponse: &dto.GeminiFunctionResponse{Name: "get_weather", Response: map[string]any{"temp": 20.0}}},
			}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	msg := out.Messages[0]
	assert.Equal(t, "tool", msg.Role)
	assert.Equal(t, "call_0", msg.ToolCallId) // len(toolCalls)==0 at emit time
	assert.JSONEq(t, `{"temp":20}`, msg.StringContent())
}

// --- append guard: empty content dropped ---

func TestReq_EmptyPartsMessageDropped(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Len(t, out.Messages, 0)
}

func TestReq_EmptyContents(t *testing.T) {
	out, err := GeminiGenerateContentRequestToOpenAIChat(&dto.GeminiChatRequest{}, nil)
	require.NoError(t, err)
	assert.Len(t, out.Messages, 0)
}

// --- generationConfig mapping ---

func TestReq_GenerationConfigFull(t *testing.T) {
	req := &dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{
			Temperature:     kitutil.GetPointer(0.7),
			TopP:            kitutil.GetPointer(0.9),
			TopK:            kitutil.GetPointer(40.0),
			MaxOutputTokens: kitutil.GetPointer(uint(256)),
			CandidateCount:  kitutil.GetPointer(2),
			StopSequences:   []string{"a", "b", "c", "d", "e"},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.NotNil(t, out.Temperature)
	assert.Equal(t, 0.7, *out.Temperature)
	require.NotNil(t, out.TopP)
	assert.Equal(t, 0.9, *out.TopP)
	require.NotNil(t, out.TopK)
	assert.Equal(t, 40, *out.TopK)
	require.NotNil(t, out.MaxTokens)
	assert.Equal(t, uint(256), *out.MaxTokens)
	require.NotNil(t, out.N)
	assert.Equal(t, 2, *out.N)
	// stop sequences capped at 4
	assert.Equal(t, []string{"a", "b", "c", "d"}, out.Stop)
}

func TestReq_GenerationConfigTemperatureZeroPreserved(t *testing.T) {
	// Temperature assigned directly (no >0 guard) => explicit 0 preserved.
	req := &dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{Temperature: kitutil.GetPointer(0.0)},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.NotNil(t, out.Temperature)
	assert.Equal(t, 0.0, *out.Temperature)
}

func TestReq_GenerationConfigNonPositiveGuardsDrop(t *testing.T) {
	// TopP/TopK/MaxOutputTokens/CandidateCount <= 0 are dropped by their >0 guards.
	req := &dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{
			TopP:            kitutil.GetPointer(0.0),
			TopK:            kitutil.GetPointer(0.0),
			MaxOutputTokens: kitutil.GetPointer(uint(0)),
			CandidateCount:  kitutil.GetPointer(0),
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Nil(t, out.TopP)
	assert.Nil(t, out.TopK)
	assert.Nil(t, out.MaxTokens)
	assert.Nil(t, out.N)
}

func TestReq_StopSequencesUnderFourNotTruncated(t *testing.T) {
	req := &dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{StopSequences: []string{"x", "y"}},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"x", "y"}, out.Stop)
}

// --- tools / functionDeclarations ---

func TestReq_ToolsFunctionDeclarations(t *testing.T) {
	toolsJSON := `[{"functionDeclarations":[{"name":"get_weather","description":"d","parameters":{"type":"object"}}]}]`
	req := &dto.GeminiChatRequest{Tools: json.RawMessage(toolsJSON)}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Tools, 1)
	assert.Equal(t, "function", out.Tools[0].Type)
	assert.Equal(t, "get_weather", out.Tools[0].Function.Name)
	assert.Equal(t, "d", out.Tools[0].Function.Description)
	assert.NotNil(t, out.Tools[0].Function.Parameters)
}

func TestReq_ToolsWithoutFunctionDeclarationsSkipped(t *testing.T) {
	// googleSearch tool has no functionDeclarations => skipped, no tools set.
	req := &dto.GeminiChatRequest{Tools: json.RawMessage(`[{"googleSearch":{}}]`)}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Nil(t, out.Tools)
}

func TestReq_ToolsSingleObjectForm(t *testing.T) {
	req := &dto.GeminiChatRequest{Tools: json.RawMessage(`{"functionDeclarations":[{"name":"f"}]}`)}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Tools, 1)
	assert.Equal(t, "f", out.Tools[0].Function.Name)
}

func TestReq_NoTools(t *testing.T) {
	out, err := GeminiGenerateContentRequestToOpenAIChat(&dto.GeminiChatRequest{}, nil)
	require.NoError(t, err)
	assert.Nil(t, out.Tools)
}

func TestReq_ToolsMalformedFunctionDeclarationsSkipped(t *testing.T) {
	// functionDeclarations is a string, not an array => Any2Type parse error =>
	// SysError + continue, no tools produced.
	req := &dto.GeminiChatRequest{Tools: json.RawMessage(`[{"functionDeclarations":"not_an_array"}]`)}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	assert.Nil(t, out.Tools)
}

// --- system instruction prepended ---

func TestReq_SystemInstructionPrepended(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hi"}}},
		},
		SystemInstructions: &dto.GeminiChatContent{
			Parts: []dto.GeminiPart{{Text: "be terse"}, {Text: "and kind"}},
		},
	}
	out, err := GeminiGenerateContentRequestToOpenAIChat(req, nil)
	require.NoError(t, err)
	require.Len(t, out.Messages, 2)
	assert.Equal(t, "system", out.Messages[0].Role)
	assert.Equal(t, "be terse\nand kind", out.Messages[0].Content)
	assert.Equal(t, "user", out.Messages[1].Role)
}
