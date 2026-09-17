package gemini

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- settings helpers -------------------------------------------------------

// withGeminiSettings 组装本次测试用的 Gemini 选项。转换器不再读进程级全局配置，
// 选项由调用方按次传入。
func withGeminiSettings(t *testing.T, thinkingAdapter bool, funcSig bool, pct float64) *convmeta.Options {
	t.Helper()
	return &convmeta.Options{Gemini: convmeta.GeminiOptions{
		ThinkingAdapterEnabled:                thinkingAdapter,
		FunctionCallThoughtSignatureEnabled:   funcSig,
		ThinkingAdapterBudgetTokensPercentage: pct,
	}}
}

func uintPtr(v uint) *uint { return &v }

func infoWithModel(opts *convmeta.Options, model string) convmeta.Meta {
	return &convmeta.Values{ChannelMetaAttached: true, UpstreamModelName: model, Options: opts}
}

// ---- ShouldAttachThoughtSignature ------------------------------------------

func TestShouldAttachThoughtSignature(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	assert.True(t, ShouldAttachThoughtSignature(opts))
	opts.Gemini.FunctionCallThoughtSignatureEnabled = false
	assert.False(t, ShouldAttachThoughtSignature(opts))
}

// ---- AttachThoughtSignatureBypass ------------------------------------------

func TestAttachThoughtSignatureBypass_NilPart(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	assert.False(t, AttachThoughtSignatureBypass(opts, nil))
}

func TestAttachThoughtSignatureBypass_AlreadyHasSignature(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	part := &dto.GeminiPart{ThoughtSignature: []byte(`"existing"`)}
	assert.False(t, AttachThoughtSignatureBypass(opts, part))
	assert.Equal(t, `"existing"`, string(part.ThoughtSignature))
}

func TestAttachThoughtSignatureBypass_Disabled(t *testing.T) {
	opts := withGeminiSettings(t, false, false, 0.6) // FunctionCallThoughtSignatureEnabled=false
	part := &dto.GeminiPart{Text: "hi"}
	assert.False(t, AttachThoughtSignatureBypass(opts, part))
	assert.Nil(t, part.ThoughtSignature)
}

func TestAttachThoughtSignatureBypass_Success(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	part := &dto.GeminiPart{Text: "hi"}
	assert.True(t, AttachThoughtSignatureBypass(opts, part))
	assert.Equal(t, strconv.Quote(ThoughtSignatureBypassValue), string(part.ThoughtSignature))
}

// ---- AttachFunctionCallThoughtSignature ------------------------------------

func TestAttachFunctionCallThoughtSignature_NilPart(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	assert.False(t, AttachFunctionCallThoughtSignature(opts, nil))
}

func TestAttachFunctionCallThoughtSignature_NoFunctionCall(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	part := &dto.GeminiPart{Text: "no call"}
	assert.False(t, AttachFunctionCallThoughtSignature(opts, part))
}

func TestAttachFunctionCallThoughtSignature_WithCall(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	part := &dto.GeminiPart{FunctionCall: &dto.FunctionCall{FunctionName: "f"}}
	assert.True(t, AttachFunctionCallThoughtSignature(opts, part))
	assert.NotEmpty(t, part.ThoughtSignature)
}

// ---- AttachFirstTextThoughtSignature ---------------------------------------

func TestAttachFirstTextThoughtSignature_Disabled(t *testing.T) {
	opts := withGeminiSettings(t, false, false, 0.6)
	parts := []dto.GeminiPart{{Text: "a"}}
	assert.False(t, AttachFirstTextThoughtSignature(opts, parts))
}

func TestAttachFirstTextThoughtSignature_FirstTextTagged(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	parts := []dto.GeminiPart{{FunctionCall: &dto.FunctionCall{FunctionName: "f"}}, {Text: "first"}, {Text: "second"}}
	assert.True(t, AttachFirstTextThoughtSignature(opts, parts))
	assert.Empty(t, parts[0].ThoughtSignature)
	assert.NotEmpty(t, parts[1].ThoughtSignature)
	assert.Empty(t, parts[2].ThoughtSignature)
}

func TestAttachFirstTextThoughtSignature_NoTextPart(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	parts := []dto.GeminiPart{{FunctionCall: &dto.FunctionCall{FunctionName: "f"}}}
	assert.False(t, AttachFirstTextThoughtSignature(opts, parts))
}

func TestAttachFirstTextThoughtSignature_TextAlreadyTagged(t *testing.T) {
	opts := withGeminiSettings(t, false, true, 0.6)
	parts := []dto.GeminiPart{{Text: "x", ThoughtSignature: []byte(`"sig"`)}}
	assert.False(t, AttachFirstTextThoughtSignature(opts, parts))
}

// ---- ApplyThinkingConfig ---------------------------------------------------
//
// Reasoning suffixes are parsed upstream of the converter and arrive through
// the conversion state (convmeta.Values.ReasoningConversion); the model name
// itself is never re-parsed here.

func infoWithState(opts *convmeta.Options, model string, state *dto.ReasoningConversionState) *convmeta.Values {
	return &convmeta.Values{ChannelMetaAttached: true, UpstreamModelName: model, ReasoningConversion: state, Options: opts}
}

func TestApplyThinkingConfig_NilRequest(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	require.NoError(t, ApplyThinkingConfig(nil, infoWithModel(opts, "gemini-2.5-flash")))
}

func TestApplyThinkingConfig_NilInfoNativeRequestUntouched(t *testing.T) {
	req := &dto.GeminiChatRequest{}
	require.NoError(t, ApplyThinkingConfig(req, nil))
	assert.Nil(t, req.GenerationConfig.ThinkingConfig)
}

func TestApplyThinkingConfig_SuffixInModelNameIsNotReparsed(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	require.NoError(t, ApplyThinkingConfig(req, infoWithModel(opts, "gemini-2.5-flash-thinking-1000")))
	assert.Nil(t, req.GenerationConfig.ThinkingConfig)
}

func TestApplyThinkingConfig_NativeLevelRecordsCanonicalEffort(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	req.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{ThinkingLevel: "HIGH"}
	info := infoWithModel(opts, "gemini-3-pro-preview")
	require.NoError(t, ApplyThinkingConfig(req, info))
	assert.Equal(t, "HIGH", req.GenerationConfig.ThinkingConfig.ThinkingLevel, "native controls are forwarded as sent")
	assert.Equal(t, "high", info.GetReasoningEffort())
}

func TestApplyThinkingConfig_NativeBudgetRecordsEffort(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	budget := 1000
	req := &dto.GeminiChatRequest{}
	req.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{ThinkingBudget: &budget}
	info := infoWithModel(opts, "gemini-2.5-flash")
	require.NoError(t, ApplyThinkingConfig(req, info))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, 1000, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, string(reasoning.EffortFromBudget(1000)), info.GetReasoningEffort())
}

func TestApplyThinkingConfig_SuffixDisabledOnBudgetModel(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	info := infoWithState(opts, "gemini-2.5-flash", &dto.ReasoningConversionState{Mode: "disabled"})
	require.NoError(t, ApplyThinkingConfig(req, info))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, 0, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, "none", info.GetReasoningEffort())
}

func TestApplyThinkingConfig_SuffixDisabledUnsupportedModelErrors(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	err := ApplyThinkingConfig(req, infoWithState(opts, "gemini-2.5-pro", &dto.ReasoningConversionState{Mode: "disabled"}))
	require.ErrorIs(t, err, reasoning.ErrThinkingNotDisabled)
	assert.Nil(t, req.GenerationConfig.ThinkingConfig)
}

func TestApplyThinkingConfig_SuffixEnabledUsesDynamicBudget(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	info := infoWithState(opts, "gemini-2.5-flash", &dto.ReasoningConversionState{Mode: "enabled"})
	require.NoError(t, ApplyThinkingConfig(req, info))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, -1, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

func TestApplyThinkingConfig_SuffixEnabledWithMaxOutputTokensUsesAdapterPercentage(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	req.GenerationConfig.MaxOutputTokens = uintPtr(10000)
	info := infoWithState(opts, "gemini-2.5-flash", &dto.ReasoningConversionState{Mode: "enabled"})
	require.NoError(t, ApplyThinkingConfig(req, info))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, 6000, *req.GenerationConfig.ThinkingConfig.ThinkingBudget) // 0.6 * 10000
}

func TestApplyThinkingConfig_SuffixEffortOnLevelModel(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	includeThoughts := true
	info := infoWithState(opts, "gemini-3-pro-preview", &dto.ReasoningConversionState{Effort: "high", IncludeThoughts: &includeThoughts})
	require.NoError(t, ApplyThinkingConfig(req, info))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	assert.Equal(t, "high", req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.IncludeThoughts)
	assert.True(t, *req.GenerationConfig.ThinkingConfig.IncludeThoughts)
	assert.Equal(t, "high", info.GetReasoningEffort())
}

func TestApplyThinkingConfig_CrossProtocolEffort(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	info := infoWithModel(opts, "gemini-3-pro-preview")
	require.NoError(t, ApplyThinkingConfig(req, info, dto.GeneralOpenAIRequest{ReasoningEffort: "low"}))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	assert.Equal(t, "low", req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	assert.Equal(t, "low", info.GetReasoningEffort())
}

func TestApplyThinkingConfig_PlainModelNoConfig(t *testing.T) {
	opts := withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	require.NoError(t, ApplyThinkingConfig(req, infoWithModel(opts, "gemini-2.5-flash")))
	assert.Nil(t, req.GenerationConfig.ThinkingConfig)
}

// ---- ParseStopSequences ----------------------------------------------------

func TestParseStopSequences(t *testing.T) {
	assert.Nil(t, ParseStopSequences(nil))
	assert.Nil(t, ParseStopSequences(""))
	assert.Equal(t, []string{"stop"}, ParseStopSequences("stop"))
	assert.Equal(t, []string{"a", "b"}, ParseStopSequences([]string{"a", "b"}))
	assert.Equal(t, []string{"a", "b"}, ParseStopSequences([]interface{}{"a", 1, "", "b"}))
	assert.Equal(t, []string{}, ParseStopSequences([]interface{}{}))
	assert.Nil(t, ParseStopSequences(42))
}

// ---- HasFunctionCallContent ------------------------------------------------

func TestHasFunctionCallContent(t *testing.T) {
	assert.False(t, HasFunctionCallContent(nil))
	assert.True(t, HasFunctionCallContent(&dto.FunctionCall{FunctionName: "f"}))
	assert.True(t, HasFunctionCallContent(&dto.FunctionCall{FunctionName: "  f  "}))
	assert.False(t, HasFunctionCallContent(&dto.FunctionCall{FunctionName: "   "}))
	assert.False(t, HasFunctionCallContent(&dto.FunctionCall{Arguments: nil}))
	assert.False(t, HasFunctionCallContent(&dto.FunctionCall{Arguments: "   "}))
	assert.True(t, HasFunctionCallContent(&dto.FunctionCall{Arguments: "x"}))
	assert.False(t, HasFunctionCallContent(&dto.FunctionCall{Arguments: map[string]interface{}{}}))
	assert.True(t, HasFunctionCallContent(&dto.FunctionCall{Arguments: map[string]interface{}{"k": 1}}))
	assert.False(t, HasFunctionCallContent(&dto.FunctionCall{Arguments: []interface{}{}}))
	assert.True(t, HasFunctionCallContent(&dto.FunctionCall{Arguments: []interface{}{1}}))
	assert.True(t, HasFunctionCallContent(&dto.FunctionCall{Arguments: 42}))
}

// ---- SupportedMimeTypesList ------------------------------------------------

func TestSupportedMimeTypesList(t *testing.T) {
	list := SupportedMimeTypesList()
	assert.Len(t, list, len(SupportedMimeTypes))
	assert.Contains(t, list, "application/pdf")
	assert.Contains(t, list, "image/png")
}
