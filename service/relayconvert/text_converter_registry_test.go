package relayconvert

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Registry listing + alias lookup
// ---------------------------------------------------------------------------

func TestTextConverterRegistryContents(t *testing.T) {
	tests := []struct {
		id           string
		from         types.RelayFormat
		to           types.RelayFormat
		quality      TextConverterQuality
		reqSteps     []string
		respSteps    []string
		reqDirect    bool
		respDirect   bool
		respAlias    string
		streamDirect bool
	}{
		{id: ConverterClaudeMessagesToOpenAIChat, from: types.RelayFormatClaude, to: types.RelayFormatOpenAI, quality: TextConverterQualityFair, reqDirect: true, respDirect: true, respAlias: ResponseConverterClaudeMessagesToOAIChat},
		{id: ConverterOpenAIChatToClaudeMessages, from: types.RelayFormatOpenAI, to: types.RelayFormatClaude, quality: TextConverterQualityFair, reqDirect: true, respDirect: true, respAlias: ResponseConverterOAIChatToClaudeMessages},
		{id: ConverterGeminiContentToOpenAIChat, from: types.RelayFormatGemini, to: types.RelayFormatOpenAI, quality: TextConverterQualityFair, reqDirect: true, respDirect: true, respAlias: ResponseConverterGeminiChatToOAIChat},
		{id: ConverterOpenAIChatToGeminiContent, from: types.RelayFormatOpenAI, to: types.RelayFormatGemini, quality: TextConverterQualityFair, reqDirect: true, respDirect: true, respAlias: ResponseConverterOAIChatToGeminiChat},
		{id: ConverterOpenAIChatToOpenAIResponses, from: types.RelayFormatOpenAI, to: types.RelayFormatOpenAIResponses, quality: TextConverterQualityGood, reqDirect: true, respDirect: true, respAlias: ResponseConverterOAIChatToOAIResponses, streamDirect: true},
		{id: ConverterOpenAIResponsesToOpenAIChat, from: types.RelayFormatOpenAIResponses, to: types.RelayFormatOpenAI, quality: TextConverterQualityGood, reqDirect: true, respDirect: true, respAlias: ResponseConverterOAIResponsesToOAIChat, streamDirect: true},
		{id: requestConverterClaudeToGemini, from: types.RelayFormatClaude, to: types.RelayFormatGemini, quality: TextConverterQualityDiscouraged, reqSteps: []string{ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToGeminiContent}, respSteps: []string{ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToGeminiContent}, respAlias: responseConverterClaudeToGemini},
		{id: requestConverterClaudeToResponses, from: types.RelayFormatClaude, to: types.RelayFormatOpenAIResponses, quality: TextConverterQualityFair, reqSteps: []string{ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToOpenAIResponses}, respSteps: []string{ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToOpenAIResponses}, respAlias: responseConverterClaudeToResponses},
		{id: requestConverterGeminiToClaude, from: types.RelayFormatGemini, to: types.RelayFormatClaude, quality: TextConverterQualityDiscouraged, reqSteps: []string{ConverterGeminiContentToOpenAIChat, ConverterOpenAIChatToClaudeMessages}, respSteps: []string{ConverterGeminiContentToOpenAIChat, ConverterOpenAIChatToClaudeMessages}, respAlias: responseConverterGeminiToClaude},
		{id: requestConverterGeminiToResponses, from: types.RelayFormatGemini, to: types.RelayFormatOpenAIResponses, quality: TextConverterQualityFair, reqSteps: []string{ConverterGeminiContentToOpenAIChat, ConverterOpenAIChatToOpenAIResponses}, respSteps: []string{ConverterGeminiContentToOpenAIChat, ConverterOpenAIChatToOpenAIResponses}, respAlias: responseConverterGeminiToResponses},
		{id: requestConverterResponsesToClaude, from: types.RelayFormatOpenAIResponses, to: types.RelayFormatClaude, quality: TextConverterQualityFair, reqDirect: true, respSteps: []string{ConverterOpenAIResponsesToOpenAIChat, ConverterOpenAIChatToClaudeMessages}, respAlias: responseConverterResponsesToClaude},
		{id: ConverterOpenAIResponsesToGemini, from: types.RelayFormatOpenAIResponses, to: types.RelayFormatGemini, quality: TextConverterQualityFair, reqDirect: true, respSteps: []string{ConverterOpenAIResponsesToOpenAIChat, ConverterOpenAIChatToGeminiContent}, respAlias: responseConverterResponsesToGemini},
	}

	require.Len(t, textConverters, len(tests))

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			spec, ok := LookupTextConverter(tt.id)
			require.True(t, ok)
			assert.Equal(t, tt.id, spec.ID)
			assert.Equal(t, tt.from, spec.From)
			assert.Equal(t, tt.to, spec.To)
			assert.Equal(t, tt.quality, spec.Quality)
			assert.Equal(t, tt.reqSteps, spec.Req.StepConverters)
			assert.Equal(t, tt.respSteps, spec.Resp.StepConverters)
			assert.Equal(t, tt.reqDirect, spec.Req.Convert != nil)
			assert.Equal(t, tt.respDirect, spec.Resp.Convert != nil)
			assert.Equal(t, tt.streamDirect, spec.Resp.NewStreamState != nil && spec.Resp.ConvertStreamChunk != nil && spec.Resp.FinalizeStream != nil)

			// Alias lookup resolves back to the canonical converter.
			aliasSpec, ok := LookupTextConverter(tt.respAlias)
			require.True(t, ok)
			assert.Equal(t, tt.id, aliasSpec.ID)
		})
	}

	_, ok := LookupTextConverter("missing")
	assert.False(t, ok)

	// Trim + alias resolution.
	spec, ok := LookupTextConverter("  " + ResponseConverterOAIChatToOAIResponses + "  ")
	require.True(t, ok)
	assert.Equal(t, ConverterOpenAIChatToOpenAIResponses, spec.ID)
}

func TestResolveTextConverterID(t *testing.T) {
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, resolveTextConverterID(ResponseConverterClaudeMessagesToOAIChat))
	assert.Equal(t, ConverterOpenAIChatToOpenAIResponses, resolveTextConverterID("  "+ConverterOpenAIChatToOpenAIResponses+"  "))
	assert.Equal(t, "unknown", resolveTextConverterID("unknown"))
}

// ---------------------------------------------------------------------------
// side-configured helpers
// ---------------------------------------------------------------------------

func TestTextRequestSideConfigured(t *testing.T) {
	assert.False(t, textRequestSideConfigured(TextRequestSide{}))
	assert.True(t, textRequestSideConfigured(TextRequestSide{Convert: func(_ *gin.Context, _ *relaycommon.RelayInfo, r any) (any, error) { return r, nil }}))
	assert.True(t, textRequestSideConfigured(TextRequestSide{StepConverters: []string{"x"}}))
}

func TestTextResponseSideConfigured(t *testing.T) {
	assert.False(t, textResponseSideConfigured(TextResponseSide{}))
	assert.True(t, textResponseSideConfigured(TextResponseSide{Convert: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ any) (any, *dto.Usage, error) { return nil, nil, nil }}))
	assert.True(t, textResponseSideConfigured(TextResponseSide{ConvertStream: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ any) (any, *dto.Usage, error) { return nil, nil, nil }}))
	assert.True(t, textResponseSideConfigured(TextResponseSide{NewStreamState: func(_ ResponseStreamOptions) any { return nil }}))
	assert.True(t, textResponseSideConfigured(TextResponseSide{ConvertStreamChunk: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ any, _ any) ([]any, *dto.Usage, error) { return nil, nil, nil }}))
	assert.True(t, textResponseSideConfigured(TextResponseSide{FinalizeStream: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ any) ([]any, *dto.Usage, error) { return nil, nil, nil }}))
	assert.True(t, textResponseSideConfigured(TextResponseSide{StepConverters: []string{"x"}}))
}

// ---------------------------------------------------------------------------
// clone helpers
// ---------------------------------------------------------------------------

func TestCloneTextConverterStrings(t *testing.T) {
	assert.Nil(t, cloneTextConverterStrings(nil))
	assert.Nil(t, cloneTextConverterStrings([]string{}))

	src := []string{"a", "b"}
	out := cloneTextConverterStrings(src)
	require.Equal(t, src, out)
	out[0] = "mutated"
	assert.Equal(t, "a", src[0], "source is not aliased")
}

func TestCloneTextConverterSpecIndependence(t *testing.T) {
	spec, ok := LookupTextConverter(requestConverterClaudeToResponses)
	require.True(t, ok)
	require.NotEmpty(t, spec.Req.StepConverters)
	spec.Req.StepConverters[0] = "mutated"
	spec.Resp.StepConverters[0] = "mutated"

	fresh, ok := LookupTextConverter(requestConverterClaudeToResponses)
	require.True(t, ok)
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, fresh.Req.StepConverters[0])
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, fresh.Resp.StepConverters[0])
}

// ---------------------------------------------------------------------------
// registerBuiltinTextConverter — validation panics
// ---------------------------------------------------------------------------

func TestRegisterBuiltinTextConverterPanics(t *testing.T) {
	reqSide := TextRequestSide{Convert: func(_ *gin.Context, _ *relaycommon.RelayInfo, r any) (any, error) { return r, nil }}
	respSide := TextResponseSide{Convert: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ any) (any, *dto.Usage, error) { return nil, nil, nil }}

	tests := []struct {
		name string
		spec TextConverterSpec
		msg  string
	}{
		{"empty id", TextConverterSpec{}, "ID is required"},
		{"missing from/to", TextConverterSpec{ID: "t1", Quality: TextConverterQualityFair, Req: reqSide, Resp: respSide}, "must declare from and to"},
		{"missing quality", TextConverterSpec{ID: "t2", From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Req: reqSide, Resp: respSide}, "must declare quality"},
		{"missing request side", TextConverterSpec{ID: "t3", From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Quality: TextConverterQualityFair, Resp: respSide}, "must declare request conversion"},
		{"missing response side", TextConverterSpec{ID: "t4", From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Quality: TextConverterQualityFair, Req: reqSide}, "must declare response conversion"},
		{"duplicate id", TextConverterSpec{ID: ConverterClaudeMessagesToOpenAIChat, From: types.RelayFormat("fa"), To: types.RelayFormat("fb"), Quality: TextConverterQualityFair, Req: reqSide, Resp: respSide}, "is already registered"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanicContains(t, tt.msg, func() {
				registerBuiltinTextConverter(tt.spec)
			})
		})
	}
}

// ---------------------------------------------------------------------------
// registerTextConverterAlias — panics + no-op branch
// ---------------------------------------------------------------------------

func TestRegisterTextConverterAliasNoopSameName(t *testing.T) {
	assert.NotPanics(t, func() {
		registerTextConverterAlias(ConverterClaudeMessagesToOpenAIChat, ConverterClaudeMessagesToOpenAIChat)
	})
}

func TestRegisterTextConverterAliasPanics(t *testing.T) {
	tests := []struct {
		name   string
		alias  string
		target string
		msg    string
	}{
		{"empty alias", "", ConverterClaudeMessagesToOpenAIChat, "alias is required"},
		{"empty target", "some_text_alias", "", "target is required"},
		{"alias conflicts with converter", ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToClaudeMessages, "conflicts with registered converter"},
		{"unknown target", "brand_new_text_alias", "not_a_converter", "references unknown converter"},
		{"already registered different", ResponseConverterClaudeMessagesToOAIChat, ConverterOpenAIChatToClaudeMessages, "is already registered for"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanicContains(t, tt.msg, func() {
				registerTextConverterAlias(tt.alias, tt.target)
			})
		})
	}
}
