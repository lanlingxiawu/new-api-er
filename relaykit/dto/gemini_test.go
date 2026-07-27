package dto

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IsStream 接收 *http.Request，本包不依赖 gin。
func reqWithTarget(method, target string) *http.Request {
	return httptest.NewRequest(method, target, nil)
}

// ---------------------------------------------------------------------------
// GeminiChatRequest.UnmarshalJSON (snake_case / camelCase for systemInstruction)
// ---------------------------------------------------------------------------

func TestGeminiChatRequest_Unmarshal_SystemInstruction(t *testing.T) {
	// camelCase
	var r GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"contents":[],"systemInstruction":{"role":"system","parts":[{"text":"a"}]}}`), &r))
	require.NotNil(t, r.SystemInstructions)
	assert.Equal(t, "a", r.SystemInstructions.Parts[0].Text)

	// snake_case wins
	var r2 GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"contents":[],"system_instruction":{"role":"system","parts":[{"text":"b"}]}}`), &r2))
	require.NotNil(t, r2.SystemInstructions)
	assert.Equal(t, "b", r2.SystemInstructions.Parts[0].Text)

	// invalid JSON => error
	assert.Error(t, (&GeminiChatRequest{}).UnmarshalJSON([]byte(`{`)))
}

func TestGeminiChatRequest_IsStream(t *testing.T) {
	assert.True(t, (&GeminiChatRequest{}).IsStream(reqWithTarget("POST", "/v1beta/models/x:generateContent?alt=sse")))
	assert.True(t, (&GeminiChatRequest{}).IsStream(reqWithTarget("POST", "/v1beta/models/x:streamGenerateContent")))
	assert.False(t, (&GeminiChatRequest{}).IsStream(reqWithTarget("POST", "/v1beta/models/x:generateContent")))
}

func TestGeminiChatRequest_SetModelName_NoOp(t *testing.T) {
	r := &GeminiChatRequest{}
	r.SetModelName("anything") // no model field; must not panic
}

func TestGeminiChatRequest_GetTokenCountMeta(t *testing.T) {
	maxTok := uint(128)
	r := &GeminiChatRequest{
		GenerationConfig: GeminiChatGenerationConfig{MaxOutputTokens: &maxTok},
		Contents: []GeminiChatContent{
			{Role: "user", Parts: []GeminiPart{
				{Text: "hello"},
				{InlineData: &GeminiInlineData{MimeType: "image/png", Data: "AAAA"}},
				{InlineData: &GeminiInlineData{MimeType: "audio/wav", Data: "BBBB"}},
				{InlineData: &GeminiInlineData{MimeType: "video/mp4", Data: "CCCC"}},
				{InlineData: &GeminiInlineData{MimeType: "application/pdf", Data: "DDDD"}},
			}},
		},
	}
	meta := r.GetTokenCountMeta()
	assert.Equal(t, 128, meta.MaxTokens)
	assert.Contains(t, meta.CombineText, "hello")
	require.Len(t, meta.Files, 4)
	assert.Equal(t, "image", string(meta.Files[0].FileType))
	assert.Equal(t, "audio", string(meta.Files[1].FileType))
	assert.Equal(t, "video", string(meta.Files[2].FileType))
	assert.Equal(t, "file", string(meta.Files[3].FileType))
}

func TestGeminiChatRequest_GetTokenCountMeta_ZeroMaxTokens(t *testing.T) {
	zero := uint(0)
	r := &GeminiChatRequest{GenerationConfig: GeminiChatGenerationConfig{MaxOutputTokens: &zero}}
	assert.Equal(t, 0, r.GetTokenCountMeta().MaxTokens)
	// nil max tokens
	assert.Equal(t, 0, (&GeminiChatRequest{}).GetTokenCountMeta().MaxTokens)
}

func TestGeminiChatRequest_GetTools(t *testing.T) {
	// array form
	r := &GeminiChatRequest{Tools: json.RawMessage(`[{"googleSearch":{}},{"codeExecution":{}}]`)}
	tools := r.GetTools()
	require.Len(t, tools, 2)
	// object form => single tool
	r2 := &GeminiChatRequest{Tools: json.RawMessage(`{"googleSearch":{}}`)}
	require.Len(t, r2.GetTools(), 1)
	// empty => nil
	assert.Nil(t, (&GeminiChatRequest{}).GetTools())
	// invalid array => nil
	assert.Nil(t, (&GeminiChatRequest{Tools: json.RawMessage(`[not json`)}).GetTools())
	// invalid object => nil
	assert.Nil(t, (&GeminiChatRequest{Tools: json.RawMessage(`{not json`)}).GetTools())
}

func TestGeminiChatRequest_SetTools(t *testing.T) {
	r := &GeminiChatRequest{}
	// empty => "[]"
	r.SetTools(nil)
	assert.JSONEq(t, "[]", string(r.Tools))
	// non-empty
	r.SetTools([]GeminiChatTool{{GoogleSearch: map[string]any{}}})
	assert.Contains(t, string(r.Tools), "googleSearch")
}

// ---------------------------------------------------------------------------
// GeminiThinkingConfig.UnmarshalJSON + SetThinkingBudget
// ---------------------------------------------------------------------------

func TestGeminiThinkingConfig_Unmarshal(t *testing.T) {
	// camelCase
	var c GeminiThinkingConfig
	require.NoError(t, kitutil.Unmarshal([]byte(`{"includeThoughts":true,"thinkingBudget":100,"thinkingLevel":"high"}`), &c))
	assert.True(t, c.IncludeThoughts)
	require.NotNil(t, c.ThinkingBudget)
	assert.Equal(t, 100, *c.ThinkingBudget)
	assert.Equal(t, "high", c.ThinkingLevel)

	// snake_case overrides
	var c2 GeminiThinkingConfig
	require.NoError(t, kitutil.Unmarshal([]byte(`{"include_thoughts":true,"thinking_budget":50,"thinking_level":"low"}`), &c2))
	assert.True(t, c2.IncludeThoughts)
	require.NotNil(t, c2.ThinkingBudget)
	assert.Equal(t, 50, *c2.ThinkingBudget)
	assert.Equal(t, "low", c2.ThinkingLevel)

	assert.Error(t, (&GeminiThinkingConfig{}).UnmarshalJSON([]byte(`{`)))
}

func TestGeminiThinkingConfig_SetThinkingBudget(t *testing.T) {
	c := &GeminiThinkingConfig{}
	c.SetThinkingBudget(256)
	require.NotNil(t, c.ThinkingBudget)
	assert.Equal(t, 256, *c.ThinkingBudget)
}

// ---------------------------------------------------------------------------
// GeminiInlineData
// ---------------------------------------------------------------------------

func TestGeminiInlineData_ToFileSource(t *testing.T) {
	var nilData *GeminiInlineData
	assert.Nil(t, nilData.ToFileSource())
	assert.Nil(t, (&GeminiInlineData{}).ToFileSource(), "empty data => nil")
	src := (&GeminiInlineData{MimeType: "image/png", Data: "AAAA"}).ToFileSource()
	require.NotNil(t, src)
	assert.False(t, src.IsURL())
}

func TestGeminiInlineData_Unmarshal(t *testing.T) {
	// camelCase
	var d GeminiInlineData
	require.NoError(t, kitutil.Unmarshal([]byte(`{"mimeType":"image/png","data":"AAAA"}`), &d))
	assert.Equal(t, "image/png", d.MimeType)
	assert.Equal(t, "AAAA", d.Data)

	// snake_case wins
	var d2 GeminiInlineData
	require.NoError(t, kitutil.Unmarshal([]byte(`{"mime_type":"audio/wav","data":"BBBB"}`), &d2))
	assert.Equal(t, "audio/wav", d2.MimeType)

	assert.Error(t, (&GeminiInlineData{}).UnmarshalJSON([]byte(`{`)))
}

// ---------------------------------------------------------------------------
// GeminiPart.UnmarshalJSON
// ---------------------------------------------------------------------------

func TestGeminiPart_Unmarshal(t *testing.T) {
	// camelCase inlineData
	var p GeminiPart
	require.NoError(t, kitutil.Unmarshal([]byte(`{"text":"t","inlineData":{"mimeType":"image/png","data":"AAAA"}}`), &p))
	assert.Equal(t, "t", p.Text)
	require.NotNil(t, p.InlineData)
	assert.Equal(t, "image/png", p.InlineData.MimeType)

	// snake_case inline_data wins
	var p2 GeminiPart
	require.NoError(t, kitutil.Unmarshal([]byte(`{"inline_data":{"mime_type":"audio/wav","data":"BBBB"}}`), &p2))
	require.NotNil(t, p2.InlineData)
	assert.Equal(t, "audio/wav", p2.InlineData.MimeType)

	assert.Error(t, (&GeminiPart{}).UnmarshalJSON([]byte(`{`)))
}

// ---------------------------------------------------------------------------
// GeminiChatGenerationConfig.UnmarshalJSON snake/camel matrix
// ---------------------------------------------------------------------------

func TestGeminiChatGenerationConfig_Unmarshal_CamelCase(t *testing.T) {
	raw := `{
		"temperature":0.5,"topP":0.9,"topK":40,"maxOutputTokens":100,
		"candidateCount":2,"stopSequences":["x"],"responseMimeType":"application/json",
		"responseSchema":{"type":"object"},"presencePenalty":0.1,"frequencyPenalty":0.2,
		"responseLogprobs":true,"enableEnhancedCivicAnswers":true,"mediaResolution":"MEDIUM",
		"responseModalities":["TEXT"],"thinkingConfig":{"thinkingBudget":10}
	}`
	var c GeminiChatGenerationConfig
	require.NoError(t, kitutil.Unmarshal([]byte(raw), &c))
	require.NotNil(t, c.TopP)
	assert.Equal(t, 0.9, *c.TopP)
	require.NotNil(t, c.TopK)
	require.NotNil(t, c.MaxOutputTokens)
	assert.Equal(t, uint(100), *c.MaxOutputTokens)
	require.NotNil(t, c.CandidateCount)
	assert.Equal(t, []string{"x"}, c.StopSequences)
	assert.Equal(t, "application/json", c.ResponseMimeType)
	assert.NotNil(t, c.ResponseSchema)
	require.NotNil(t, c.PresencePenalty)
	require.NotNil(t, c.FrequencyPenalty)
	require.NotNil(t, c.ResponseLogprobs)
	require.NotNil(t, c.EnableEnhancedCivicAnswers)
	assert.Equal(t, MediaResolution("MEDIUM"), c.MediaResolution)
	assert.Equal(t, []string{"TEXT"}, c.ResponseModalities)
	require.NotNil(t, c.ThinkingConfig)
}

func TestGeminiChatGenerationConfig_Unmarshal_SnakeCaseOverrides(t *testing.T) {
	raw := `{
		"top_p":0.1,"top_k":5,"max_output_tokens":22,"candidate_count":3,
		"stop_sequences":["y"],"response_mime_type":"text/plain",
		"response_schema":{"a":1},"response_json_schema":{"b":2},
		"presence_penalty":0.3,"frequency_penalty":0.4,"response_logprobs":false,
		"enable_enhanced_civic_answers":false,"media_resolution":"LOW",
		"response_modalities":["AUDIO"],"thinking_config":{"thinkingBudget":9},
		"speech_config":{"x":1},"image_config":{"y":2}
	}`
	var c GeminiChatGenerationConfig
	require.NoError(t, kitutil.Unmarshal([]byte(raw), &c))
	require.NotNil(t, c.TopP)
	assert.Equal(t, 0.1, *c.TopP)
	require.NotNil(t, c.TopK)
	assert.Equal(t, 5.0, *c.TopK)
	require.NotNil(t, c.MaxOutputTokens)
	assert.Equal(t, uint(22), *c.MaxOutputTokens)
	require.NotNil(t, c.CandidateCount)
	assert.Equal(t, 3, *c.CandidateCount)
	assert.Equal(t, []string{"y"}, c.StopSequences)
	assert.Equal(t, "text/plain", c.ResponseMimeType)
	assert.NotNil(t, c.ResponseSchema)
	assert.NotEmpty(t, c.ResponseJsonSchema)
	require.NotNil(t, c.PresencePenalty)
	require.NotNil(t, c.FrequencyPenalty)
	require.NotNil(t, c.ResponseLogprobs)
	assert.False(t, *c.ResponseLogprobs)
	require.NotNil(t, c.EnableEnhancedCivicAnswers)
	assert.Equal(t, MediaResolution("LOW"), c.MediaResolution)
	assert.Equal(t, []string{"AUDIO"}, c.ResponseModalities)
	require.NotNil(t, c.ThinkingConfig)
	assert.NotEmpty(t, c.SpeechConfig)
	assert.NotEmpty(t, c.ImageConfig)
}

func TestGeminiChatGenerationConfig_Unmarshal_Error(t *testing.T) {
	assert.Error(t, (&GeminiChatGenerationConfig{}).UnmarshalJSON([]byte(`{`)))
}

// ---------------------------------------------------------------------------
// GeminiChatResponse.UnmarshalJSON + GetUsageMetadata
// ---------------------------------------------------------------------------

func TestGeminiChatResponse_Unmarshal_WithUsage(t *testing.T) {
	var r GeminiChatResponse
	require.NoError(t, kitutil.Unmarshal([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":5}}`), &r))
	assert.True(t, r.HasUsageMetadata)
	assert.Equal(t, 5, r.UsageMetadata.PromptTokenCount)

	md := r.GetUsageMetadata()
	require.NotNil(t, md)
	assert.Equal(t, 5, md.PromptTokenCount)
}

func TestGeminiChatResponse_Unmarshal_WithoutUsage(t *testing.T) {
	var r GeminiChatResponse
	require.NoError(t, kitutil.Unmarshal([]byte(`{"candidates":[]}`), &r))
	assert.False(t, r.HasUsageMetadata)
	assert.Nil(t, r.GetUsageMetadata(), "no usage metadata => nil")

	// present but all-zero => HasUsageMetadata true still returns pointer
	var r2 GeminiChatResponse
	require.NoError(t, kitutil.Unmarshal([]byte(`{"candidates":[],"usageMetadata":{}}`), &r2))
	assert.True(t, r2.HasUsageMetadata)
	assert.NotNil(t, r2.GetUsageMetadata())

	// error path
	assert.Error(t, (&GeminiChatResponse{}).UnmarshalJSON([]byte(`{`)))
}

func TestGeminiChatResponse_GetUsageMetadata_NilReceiver(t *testing.T) {
	var r *GeminiChatResponse
	assert.Nil(t, r.GetUsageMetadata())
}

// ---------------------------------------------------------------------------
// GeminiEmbeddingRequest / GeminiBatchEmbeddingRequest
// ---------------------------------------------------------------------------

func TestGeminiEmbeddingRequest(t *testing.T) {
	r := &GeminiEmbeddingRequest{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "a"}, {Text: "b"}, {Text: ""}}}}
	assert.False(t, r.IsStream(nil))
	meta := r.GetTokenCountMeta()
	assert.Equal(t, "a\nb", meta.CombineText)

	r.SetModelName("")
	assert.Equal(t, "", r.Model)
	r.SetModelName("emb")
	assert.Equal(t, "emb", r.Model)
}

func TestGeminiBatchEmbeddingRequest(t *testing.T) {
	r := &GeminiBatchEmbeddingRequest{Requests: []*GeminiEmbeddingRequest{
		{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "x"}}}},
		{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "y"}}}},
	}}
	assert.False(t, r.IsStream(nil))
	meta := r.GetTokenCountMeta()
	assert.Equal(t, "x\ny", meta.CombineText)

	r.SetModelName("m")
	for _, req := range r.Requests {
		assert.Equal(t, "m", req.Model)
	}
	// empty model name => no change
	r.SetModelName("")
	assert.Equal(t, "m", r.Requests[0].Model)
}
