package oaichat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
)

// geminiInfo 打开 thoughtSignature 附加开关——转换器不再读进程级全局配置，
// 该行为由 Options 按次传入。
func geminiInfo() convmeta.Meta {
	return &convmeta.Values{Options: &convmeta.Options{
		Gemini: convmeta.GeminiOptions{
			FunctionCallThoughtSignatureEnabled: true,
			// 安全阈值原先取自全局 GeminiSettings 的 "OFF" 默认值。
			SafetySetting: func(string) string { return "OFF" },
		},
	}}
}

func TestGeminiReq_BasicGenerationConfigAndZeroPreservation(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:       "gemini-1.5-flash",
		Temperature: ptr(0.0), // Rule 5: explicit zero temperature preserved
		TopP:        ptr(0.8),
		MaxTokens:   ptr(uint(256)),
		Seed:        ptr(1234.0),
		Messages:    []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	require.NotNil(t, got.GenerationConfig.Temperature)
	assert.Equal(t, 0.0, *got.GenerationConfig.Temperature)
	require.NotNil(t, got.GenerationConfig.TopP)
	assert.Equal(t, 0.8, *got.GenerationConfig.TopP)
	require.NotNil(t, got.GenerationConfig.MaxOutputTokens)
	assert.Equal(t, uint(256), *got.GenerationConfig.MaxOutputTokens)
	require.NotNil(t, got.GenerationConfig.Seed)
	assert.Equal(t, int64(1234), *got.GenerationConfig.Seed)
	// Safety settings always populated (one per category).
	assert.Len(t, got.SafetySettings, 4)
}

func TestGeminiReq_TopPZeroIsDropped(t *testing.T) {
	// NOTE: TopP is only forwarded when *TopP > 0, so an explicit top_p=0 is
	// intentionally dropped (Gemini rejects topP=0). Documented deviation from
	// the general zero-preservation guidance.
	req := dto.GeneralOpenAIRequest{
		Model:    "gemini-1.5-flash",
		TopP:     ptr(0.0),
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	assert.Nil(t, got.GenerationConfig.TopP)
}

func TestGeminiReq_StopSequencesTruncatedToFive(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:    "gemini-1.5-flash",
		Stop:     []interface{}{"a", "b", "c", "d", "e", "f", "g"},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, got.GenerationConfig.StopSequences)
}

func TestGeminiReq_ExtraBodyThinkingConfig(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:     "gemini-2.5-flash",
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_budget":512,"include_thoughts":true,"thinking_level":"high"}}}`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	tc := got.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc)
	require.NotNil(t, tc.ThinkingBudget)
	assert.Equal(t, 512, *tc.ThinkingBudget)
	assert.True(t, tc.IncludeThoughts)
	assert.Equal(t, "high", tc.ThinkingLevel)
}

func TestGeminiReq_ExtraBodyThinkingBudgetZeroDisablesThoughts(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:     "gemini-2.5-flash",
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_budget":0}}}`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	tc := got.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc)
	assert.False(t, tc.IncludeThoughts) // budget 0 -> IncludeThoughts=false
}

func TestGeminiReq_ExtraBodyErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"camelCase thinkingConfig", `{"google":{"thinkingConfig":{}}}`},
		{"camelCase thinkingBudget", `{"google":{"thinking_config":{"thinkingBudget":1}}}`},
		{"budget not int", `{"google":{"thinking_config":{"thinking_budget":"x"}}}`},
		{"include_thoughts not bool", `{"google":{"thinking_config":{"include_thoughts":"yes"}}}`},
		{"thinking_level not string", `{"google":{"thinking_config":{"thinking_level":5}}}`},
		{"camelCase imageConfig", `{"google":{"imageConfig":{}}}`},
		{"camelCase aspectRatio", `{"google":{"image_config":{"aspectRatio":"1:1"}}}`},
		{"camelCase imageSize", `{"google":{"image_config":{"imageSize":"1K"}}}`},
		{"invalid json", `{not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := dto.GeneralOpenAIRequest{
				Model:     "gemini-2.5-flash",
				ExtraBody: json.RawMessage(tc.body),
				Messages:  []dto.Message{{Role: "user", Content: "hi"}},
			}
			_, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
			require.Error(t, err)
		})
	}
}

func TestGeminiReq_ExtraBodyImageConfig(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:     "gemini-2.5-flash-image",
		ExtraBody: json.RawMessage(`{"google":{"image_config":{"aspect_ratio":"16:9","image_size":"2K"}}}`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	require.NotEmpty(t, got.GenerationConfig.ImageConfig)
	assert.Contains(t, string(got.GenerationConfig.ImageConfig), "aspectRatio")
	assert.Contains(t, string(got.GenerationConfig.ImageConfig), "imageSize")
}

func TestGeminiReq_ExtraBodyNothinkingModelSkipsThinking(t *testing.T) {
	// With a -nothinking suffix, the google thinking branch is skipped and the
	// request stays adaptorWithExtraBody=false path but no thinking config set.
	req := dto.GeneralOpenAIRequest{
		Model:     "gemini-2.5-flash-nothinking",
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_budget":512}}}`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	// info has empty upstream model name -> uses textRequest.Model for suffix check.
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	assert.Nil(t, got.GenerationConfig.ThinkingConfig)
}

func TestGeminiReq_ToolsSpecialAndFunctions(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Tools: []dto.ToolCallRequest{
			{Type: "function", Function: dto.FunctionRequest{Name: "googleSearch"}},
			{Type: "function", Function: dto.FunctionRequest{Name: "codeExecution"}},
			{Type: "function", Function: dto.FunctionRequest{Name: "urlContext"}},
			{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"q": map[string]any{"type": "string"}},
			}}},
			// empty-properties function -> Parameters nil'd out.
			{Type: "function", Function: dto.FunctionRequest{Name: "noparams", Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}}},
		},
		ToolChoice: "auto",
		Messages:   []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	tools := got.GetTools()
	// codeExecution, googleSearch, urlContext, functionDeclarations => 4 tools.
	require.Len(t, tools, 4)
	assert.NotNil(t, got.ToolConfig)
}

func TestGeminiReq_ResponseFormatJSONSchema(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		ResponseFormat: &dto.ResponseFormat{
			Type:       "json_schema",
			JsonSchema: json.RawMessage(`{"schema":{"type":"object","properties":{"a":{"type":"string"}}}}`),
		},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	assert.Equal(t, "application/json", got.GenerationConfig.ResponseMimeType)
	assert.NotNil(t, got.GenerationConfig.ResponseSchema)
}

func TestGeminiReq_ResponseFormatJSONObject(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:          "gemini-1.5-pro",
		ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
		Messages:       []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	assert.Equal(t, "application/json", got.GenerationConfig.ResponseMimeType)
}

func TestGeminiReq_SystemAndDeveloperMessages(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "system", Content: "rule one"},
			{Role: "developer", Content: "rule two"},
			{Role: "user", Content: "hi"},
		},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	require.NotNil(t, got.SystemInstructions)
	require.Len(t, got.SystemInstructions.Parts, 1)
	assert.Equal(t, "rule one\nrule two", got.SystemInstructions.Parts[0].Text)
}

func TestGeminiReq_ToolMessageContentVariants(t *testing.T) {
	tests := []struct {
		name    string
		content string
		check   func(t *testing.T, resp map[string]interface{})
	}{
		{
			name:    "json object",
			content: `{"k":"v"}`,
			check: func(t *testing.T, resp map[string]interface{}) {
				assert.Equal(t, "v", resp["k"])
			},
		},
		{
			name:    "json array",
			content: `[1,2,3]`,
			check: func(t *testing.T, resp map[string]interface{}) {
				assert.Contains(t, resp, "result")
			},
		},
		{
			name:    "plain string",
			content: "just text",
			check: func(t *testing.T, resp map[string]interface{}) {
				assert.Equal(t, "just text", resp["content"])
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := "lookup"
			req := dto.GeneralOpenAIRequest{
				Model: "gemini-1.5-pro",
				Messages: []dto.Message{
					{Role: "user", Content: "hi"},
					{Role: "tool", ToolCallId: "call_1", Name: &name, Content: tt.content},
				},
			}
			got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
			require.NoError(t, err)
			// last content should carry a functionResponse part.
			last := got.Contents[len(got.Contents)-1]
			var fr *dto.GeminiFunctionResponse
			for _, p := range last.Parts {
				if p.FunctionResponse != nil {
					fr = p.FunctionResponse
				}
			}
			require.NotNil(t, fr)
			assert.Equal(t, "lookup", fr.Name)
			tt.check(t, fr.Response)
		})
	}
}

func TestGeminiReq_ToolMessageAfterModelInsertsUser(t *testing.T) {
	// assistant(model) message then a tool message -> a new user content is
	// created to hold the function response.
	amsg := dto.Message{Role: "assistant", Content: "calling"}
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
			amsg,
			{Role: "tool", ToolCallId: "call_1", Content: `{"ok":true}`},
		},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	last := got.Contents[len(got.Contents)-1]
	assert.Equal(t, "user", last.Role)
}

func TestGeminiReq_AssistantToolCallsBecomeFunctionCalls(t *testing.T) {
	amsg := dto.Message{Role: "assistant", Content: "calling"}
	amsg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "c1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`}},
	})
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
			amsg,
		},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	modelContent := got.Contents[len(got.Contents)-1]
	assert.Equal(t, "model", modelContent.Role)
	var fc *dto.FunctionCall
	for _, p := range modelContent.Parts {
		if p.FunctionCall != nil {
			fc = p.FunctionCall
		}
	}
	require.NotNil(t, fc)
	assert.Equal(t, "lookup", fc.FunctionName)
	assert.Equal(t, map[string]interface{}{"q": "x"}, fc.Arguments)
}

func TestGeminiReq_AssistantToolCallInvalidArgsError(t *testing.T) {
	amsg := dto.Message{Role: "assistant", Content: "calling"}
	amsg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "c1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{bad`}},
	})
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
			amsg,
		},
	}
	_, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid arguments")
}

func TestGeminiReq_TextContentAndImagePart(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "look"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x.test/pic.png"}},
			}},
		},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	parts := got.Contents[len(got.Contents)-1].Parts
	require.Len(t, parts, 2)
	assert.Equal(t, "look", parts[0].Text)
	require.NotNil(t, parts[1].InlineData)
	assert.Equal(t, "image/png", parts[1].InlineData.MimeType)
}

func TestGeminiReq_UnsupportedMimeTypeError(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x.test/bad-file"}},
			}},
		},
	}
	_, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported by Gemini")
}

func TestGeminiReq_MediaResolveError(t *testing.T) {
	setMediaResolver(t, media_resolverErr())
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x.test/pic.png"}},
			}},
		},
	}
	_, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.Error(t, err)
}

func TestGeminiReq_MarkdownInlineImage(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "before ![alt](data:image/png;base64,AAAA) after"},
			}},
		},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	parts := got.Contents[len(got.Contents)-1].Parts
	// "before " text, inline image, then remaining " after" is not re-added as a
	// separate part in this branch (the loop continues from the close index).
	var haveInline bool
	for _, p := range parts {
		if p.InlineData != nil {
			haveInline = true
		}
	}
	assert.True(t, haveInline)
}

func TestGeminiReq_MarkdownInlineImageDecodeError(t *testing.T) {
	setMediaResolver(t, media_resolverErr())
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "x ![a](data:image/png;base64,AAAA) y"},
			}},
		},
	}
	_, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "markdown base64 image")
}

func TestGeminiReq_EmptyTextPartSkipped(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": ""},
				map[string]any{"type": "text", "text": "kept"},
			}},
		},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	parts := got.Contents[len(got.Contents)-1].Parts
	require.Len(t, parts, 1)
	assert.Equal(t, "kept", parts[0].Text)
}

// --- supplementary branch coverage -----------------------------------------

func TestGeminiReq_UpstreamModelNameOverride(t *testing.T) {
	// When the RelayInfo carries an upstream model name, it is used in place of
	// textRequest.Model for the -nothinking suffix check.
	info := &convmeta.Values{
		ChannelMetaAttached: true,
		UpstreamModelName:   "gemini-2.5-flash-nothinking",
	}
	req := dto.GeneralOpenAIRequest{
		Model:     "gemini-2.5-flash", // no suffix here
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_budget":512}}}`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, info)
	require.NoError(t, err)
	// upstream name ends with -nothinking -> the google thinking branch is skipped.
	assert.Nil(t, got.GenerationConfig.ThinkingConfig)
}

func TestGeminiReq_ToolMessageNameFromToolCallIDs(t *testing.T) {
	// An assistant tool_call records call.ID -> name in toolCallIDs; a later tool
	// message without an explicit Name resolves its name from that map.
	amsg := dto.Message{Role: "assistant", Content: "calling"}
	amsg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "resolved_name", Arguments: `{"q":"x"}`}},
	})
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-1.5-pro",
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
			amsg,
			{Role: "tool", ToolCallId: "call_1", Content: `{"ok":true}`}, // no Name
		},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	last := got.Contents[len(got.Contents)-1]
	var fr *dto.GeminiFunctionResponse
	for _, p := range last.Parts {
		if p.FunctionResponse != nil {
			fr = p.FunctionResponse
		}
	}
	require.NotNil(t, fr)
	assert.Equal(t, "resolved_name", fr.Name)
}

func TestGeminiReq_NonTextPartNilSourceSkipped(t *testing.T) {
	// A media part whose ToFileSource() is nil (image_url with empty url) is
	// skipped without error.
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeText, Text: "keep"},
		{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: ""}}, // nil source
	})
	req := dto.GeneralOpenAIRequest{
		Model:    "gemini-1.5-pro",
		Messages: []dto.Message{msg},
	}
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), req, geminiInfo())
	require.NoError(t, err)
	parts := got.Contents[len(got.Contents)-1].Parts
	require.Len(t, parts, 1)
	assert.Equal(t, "keep", parts[0].Text)
}

func TestGeminiReq_MarkdownEdgeCasesNoImage(t *testing.T) {
	cases := map[string]string{
		"bracket without data": "look ![alt](http://x/y.png) here",
		"data without close":   "look ![alt](data:image/png;base64,AAAA here",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			msg := dto.Message{Role: "user"}
			msg.SetMediaContent([]dto.MediaContent{{Type: dto.ContentTypeText, Text: text}})
			got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), dto.GeneralOpenAIRequest{
				Model:    "gemini-1.5-pro",
				Messages: []dto.Message{msg},
			}, geminiInfo())
			require.NoError(t, err)
			parts := got.Contents[len(got.Contents)-1].Parts
			// No inline image extracted; the whole text is kept as a single part.
			require.Len(t, parts, 1)
			assert.Equal(t, text, parts[0].Text)
		})
	}
}

func TestGeminiReq_AssistantMarkdownImageAttachesThoughtSignature(t *testing.T) {
	// Assistant role + markdown inline image: with the (default-on) thought
	// signature setting, the inline image part gets a thought-signature bypass.
	msg := dto.Message{Role: "assistant"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeText, Text: "here ![a](data:image/png;base64,AAAA) done"},
	})
	got, err := OpenAIChatRequestToGeminiGenerateContent(newGinCtx(), dto.GeneralOpenAIRequest{
		Model:    "gemini-1.5-pro",
		Messages: []dto.Message{msg},
	}, geminiInfo())
	require.NoError(t, err)
	model := got.Contents[len(got.Contents)-1]
	assert.Equal(t, "model", model.Role)
	var haveInlineSig bool
	for _, p := range model.Parts {
		if p.InlineData != nil && len(p.ThoughtSignature) > 0 {
			haveInlineSig = true
		}
	}
	assert.True(t, haveInlineSig)
}
