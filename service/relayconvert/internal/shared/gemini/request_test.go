package gemini

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- settings helpers -------------------------------------------------------

// withGeminiSettings mutates the process-global Gemini settings for the test and
// restores the previous values afterwards. GetGeminiSettings returns a pointer to
// the shared struct.
func withGeminiSettings(t *testing.T, thinkingAdapter bool, funcSig bool, pct float64) {
	t.Helper()
	s := model_setting.GetGeminiSettings()
	prevThinking := s.ThinkingAdapterEnabled
	prevSig := s.FunctionCallThoughtSignatureEnabled
	prevPct := s.ThinkingAdapterBudgetTokensPercentage
	s.ThinkingAdapterEnabled = thinkingAdapter
	s.FunctionCallThoughtSignatureEnabled = funcSig
	s.ThinkingAdapterBudgetTokensPercentage = pct
	t.Cleanup(func() {
		s := model_setting.GetGeminiSettings()
		s.ThinkingAdapterEnabled = prevThinking
		s.FunctionCallThoughtSignatureEnabled = prevSig
		s.ThinkingAdapterBudgetTokensPercentage = prevPct
	})
}

func uintPtr(v uint) *uint { return &v }

func infoWithModel(model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model}}
}

// ---- ShouldAttachThoughtSignature ------------------------------------------

func TestShouldAttachThoughtSignature(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	assert.True(t, ShouldAttachThoughtSignature())
	model_setting.GetGeminiSettings().FunctionCallThoughtSignatureEnabled = false
	assert.False(t, ShouldAttachThoughtSignature())
}

// ---- AttachThoughtSignatureBypass ------------------------------------------

func TestAttachThoughtSignatureBypass_NilPart(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	assert.False(t, AttachThoughtSignatureBypass(nil))
}

func TestAttachThoughtSignatureBypass_AlreadyHasSignature(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	part := &dto.GeminiPart{ThoughtSignature: []byte(`"existing"`)}
	assert.False(t, AttachThoughtSignatureBypass(part))
	assert.Equal(t, `"existing"`, string(part.ThoughtSignature))
}

func TestAttachThoughtSignatureBypass_Disabled(t *testing.T) {
	withGeminiSettings(t, false, false, 0.6) // FunctionCallThoughtSignatureEnabled=false
	part := &dto.GeminiPart{Text: "hi"}
	assert.False(t, AttachThoughtSignatureBypass(part))
	assert.Nil(t, part.ThoughtSignature)
}

func TestAttachThoughtSignatureBypass_Success(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	part := &dto.GeminiPart{Text: "hi"}
	assert.True(t, AttachThoughtSignatureBypass(part))
	assert.Equal(t, strconv.Quote(ThoughtSignatureBypassValue), string(part.ThoughtSignature))
}

// ---- AttachFunctionCallThoughtSignature ------------------------------------

func TestAttachFunctionCallThoughtSignature_NilPart(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	assert.False(t, AttachFunctionCallThoughtSignature(nil))
}

func TestAttachFunctionCallThoughtSignature_NoFunctionCall(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	part := &dto.GeminiPart{Text: "no call"}
	assert.False(t, AttachFunctionCallThoughtSignature(part))
}

func TestAttachFunctionCallThoughtSignature_WithCall(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	part := &dto.GeminiPart{FunctionCall: &dto.FunctionCall{FunctionName: "f"}}
	assert.True(t, AttachFunctionCallThoughtSignature(part))
	assert.NotEmpty(t, part.ThoughtSignature)
}

// ---- AttachFirstTextThoughtSignature ---------------------------------------

func TestAttachFirstTextThoughtSignature_Disabled(t *testing.T) {
	withGeminiSettings(t, false, false, 0.6)
	parts := []dto.GeminiPart{{Text: "a"}}
	assert.False(t, AttachFirstTextThoughtSignature(parts))
}

func TestAttachFirstTextThoughtSignature_FirstTextTagged(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	parts := []dto.GeminiPart{{FunctionCall: &dto.FunctionCall{FunctionName: "f"}}, {Text: "first"}, {Text: "second"}}
	assert.True(t, AttachFirstTextThoughtSignature(parts))
	assert.Empty(t, parts[0].ThoughtSignature)
	assert.NotEmpty(t, parts[1].ThoughtSignature)
	assert.Empty(t, parts[2].ThoughtSignature)
}

func TestAttachFirstTextThoughtSignature_NoTextPart(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	parts := []dto.GeminiPart{{FunctionCall: &dto.FunctionCall{FunctionName: "f"}}}
	assert.False(t, AttachFirstTextThoughtSignature(parts))
}

func TestAttachFirstTextThoughtSignature_TextAlreadyTagged(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	parts := []dto.GeminiPart{{Text: "x", ThoughtSignature: []byte(`"sig"`)}}
	assert.False(t, AttachFirstTextThoughtSignature(parts))
}

// ---- ApplyThinkingConfig ---------------------------------------------------

func TestApplyThinkingConfig_NilRequest(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	ApplyThinkingConfig(nil, infoWithModel("gemini-2.5-flash-thinking")) // must not panic
}

func TestApplyThinkingConfig_NilInfo(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	ApplyThinkingConfig(req, nil)
	assert.Nil(t, req.GenerationConfig.ThinkingConfig)
}

func TestApplyThinkingConfig_AdapterDisabled(t *testing.T) {
	withGeminiSettings(t, false, true, 0.6)
	req := &dto.GeminiChatRequest{}
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash-thinking"))
	assert.Nil(t, req.GenerationConfig.ThinkingConfig)
}

func TestApplyThinkingConfig_ExplicitThinkingBudget(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash-thinking-1000"))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, 1000, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.True(t, req.GenerationConfig.ThinkingConfig.IncludeThoughts)
}

func TestApplyThinkingConfig_ExplicitThinkingBudgetClamped(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	// flash range is [0, 24576]; 99999 clamps down.
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash-thinking-99999"))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, flash25MaxBudget, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

func TestApplyThinkingConfig_ThinkingBudgetUnparseable(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash-thinking-notanumber"))
	// Atoi fails -> no ThinkingConfig produced from this branch.
	assert.Nil(t, req.GenerationConfig.ThinkingConfig)
}

func TestApplyThinkingConfig_ThinkingSuffixUnsupportedModel(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-pro-preview-05-06-thinking"))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	assert.True(t, req.GenerationConfig.ThinkingConfig.IncludeThoughts)
	assert.Nil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

func TestApplyThinkingConfig_ThinkingSuffixWithMaxOutputTokens(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	req.GenerationConfig.MaxOutputTokens = uintPtr(10000)
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash-thinking"))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, 6000, *req.GenerationConfig.ThinkingConfig.ThinkingBudget) // 0.6 * 10000
	assert.True(t, req.GenerationConfig.ThinkingConfig.IncludeThoughts)
}

func TestApplyThinkingConfig_ThinkingSuffixWithEffort(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{} // no MaxOutputTokens
	oai := dto.GeneralOpenAIRequest{ReasoningEffort: "low"}
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash-thinking"), oai)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	// low = flash max * 20% = 24576 * 20 / 100 = 4915
	assert.Equal(t, flash25MaxBudget*20/100, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

func TestApplyThinkingConfig_ThinkingSuffixNoBudgetSource(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{} // no MaxOutputTokens, no oaiRequest
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash-thinking"))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	assert.True(t, req.GenerationConfig.ThinkingConfig.IncludeThoughts)
	assert.Nil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

func TestApplyThinkingConfig_NoThinkingSuffixFlash(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash-nothinking"))
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, 0, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

func TestApplyThinkingConfig_NoThinkingSuffixNew25Pro(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	// new 2.5-pro with -nothinking is a no-op (config stays nil).
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-pro-nothinking"))
	assert.Nil(t, req.GenerationConfig.ThinkingConfig)
}

func TestApplyThinkingConfig_EffortSuffix(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	info := infoWithModel("gemini-2.5-flash-high")
	ApplyThinkingConfig(req, info)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	assert.True(t, req.GenerationConfig.ThinkingConfig.IncludeThoughts)
	assert.Equal(t, "high", req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	assert.Equal(t, "high", info.ReasoningEffort)
}

func TestApplyThinkingConfig_PlainModelNoConfig(t *testing.T) {
	withGeminiSettings(t, true, true, 0.6)
	req := &dto.GeminiChatRequest{}
	ApplyThinkingConfig(req, infoWithModel("gemini-2.5-flash"))
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

// ---- clamp helpers ---------------------------------------------------------

func TestIsNew25ProModel(t *testing.T) {
	assert.True(t, isNew25ProModel("gemini-2.5-pro"))
	assert.True(t, isNew25ProModel("gemini-2.5-pro-preview-06-05"))
	assert.False(t, isNew25ProModel("gemini-2.5-pro-preview-05-06"))
	assert.False(t, isNew25ProModel("gemini-2.5-pro-preview-03-25"))
	assert.False(t, isNew25ProModel("gemini-1.5-pro"))
}

func TestIs25FlashLiteModel(t *testing.T) {
	assert.True(t, is25FlashLiteModel("gemini-2.5-flash-lite"))
	assert.False(t, is25FlashLiteModel("gemini-2.5-flash"))
}

func TestClampThinkingBudget(t *testing.T) {
	// flash-lite range [512, 24576]
	assert.Equal(t, flash25LiteMinBudget, clampThinkingBudget("gemini-2.5-flash-lite", 100))
	assert.Equal(t, flash25LiteMaxBudget, clampThinkingBudget("gemini-2.5-flash-lite", 99999))
	assert.Equal(t, 1000, clampThinkingBudget("gemini-2.5-flash-lite", 1000))
	// new 2.5-pro range [128, 32768]
	assert.Equal(t, pro25MinBudget, clampThinkingBudget("gemini-2.5-pro", 10))
	assert.Equal(t, pro25MaxBudget, clampThinkingBudget("gemini-2.5-pro", 99999))
	assert.Equal(t, 5000, clampThinkingBudget("gemini-2.5-pro", 5000))
	// default (flash) range [0, 24576]
	assert.Equal(t, 0, clampThinkingBudget("gemini-2.5-flash", -5))
	assert.Equal(t, flash25MaxBudget, clampThinkingBudget("gemini-2.5-flash", 99999))
	assert.Equal(t, 1000, clampThinkingBudget("gemini-2.5-flash", 1000))
}

func TestClampThinkingBudgetByEffort(t *testing.T) {
	// new 2.5-pro base max 32768
	assert.Equal(t, pro25MaxBudget*80/100, clampThinkingBudgetByEffort("gemini-2.5-pro", "high"))
	assert.Equal(t, pro25MaxBudget*50/100, clampThinkingBudgetByEffort("gemini-2.5-pro", "medium"))
	// flash base max 24576
	assert.Equal(t, flash25MaxBudget*20/100, clampThinkingBudgetByEffort("gemini-2.5-flash", "low"))
	assert.Equal(t, flash25MaxBudget*5/100, clampThinkingBudgetByEffort("gemini-2.5-flash", "minimal"))
	// unknown effort -> full base max, clamped
	assert.Equal(t, flash25MaxBudget, clampThinkingBudgetByEffort("gemini-2.5-flash", ""))

	// NOTE (latent dead-assignment bug): in clampThinkingBudgetByEffort the
	// `if is25FlashLite { maxBudget = flash25LiteMaxBudget }` line is
	// immediately overwritten by the following `if isNew25Pro {...} else {
	// maxBudget = flash25MaxBudget }`, so flash-lite always uses the plain
	// flash base rather than its own. It has no observable effect today only
	// because flash25LiteMaxBudget == flash25MaxBudget (both 24576). The test
	// below pins the CURRENT behavior: flash-lite effort math equals flash.
	assert.Equal(t,
		clampThinkingBudgetByEffort("gemini-2.5-flash", "high"),
		clampThinkingBudgetByEffort("gemini-2.5-flash-lite", "high"),
	)
}
