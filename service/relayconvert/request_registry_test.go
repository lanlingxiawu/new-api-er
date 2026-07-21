package relayconvert

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newOpenAIRequest() *dto.GeneralOpenAIRequest {
	return &dto.GeneralOpenAIRequest{
		Model:    "gpt-test",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}
}

func newClaudeRequest() *dto.ClaudeRequest {
	return &dto.ClaudeRequest{
		Model:    "claude-test",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}
}

func newGeminiRequest() *dto.GeminiChatRequest {
	return &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
	}
}

func newResponsesRequest(t *testing.T) *dto.OpenAIResponsesRequest {
	return &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustRawMessage(t, []map[string]any{
			{"role": "user", "content": "hello"},
		}),
	}
}

func infoFor(format types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:            format,
		RequestConversionChain: []types.RelayFormat{format},
		ChannelMeta:            &relaycommon.ChannelMeta{UpstreamModelName: "upstream-test"},
	}
}

// ---------------------------------------------------------------------------
// Registry listing
// ---------------------------------------------------------------------------

func TestRequestConverterRegistryContents(t *testing.T) {
	tests := []struct {
		converter      string
		from           types.RelayFormat
		to             types.RelayFormat
		quality        RequestConverterQuality
		stepConverters []string
	}{
		{converter: ConverterClaudeMessagesToOpenAIChat, from: types.RelayFormatClaude, to: types.RelayFormatOpenAI, quality: RequestConverterQualityFair},
		{converter: ConverterGeminiContentToOpenAIChat, from: types.RelayFormatGemini, to: types.RelayFormatOpenAI, quality: RequestConverterQualityFair},
		{converter: ConverterOpenAIChatToClaudeMessages, from: types.RelayFormatOpenAI, to: types.RelayFormatClaude, quality: RequestConverterQualityFair},
		{converter: ConverterOpenAIChatToGeminiContent, from: types.RelayFormatOpenAI, to: types.RelayFormatGemini, quality: RequestConverterQualityFair},
		{converter: ConverterOpenAIChatToOpenAIResponses, from: types.RelayFormatOpenAI, to: types.RelayFormatOpenAIResponses, quality: RequestConverterQualityGood},
		{converter: ConverterOpenAIResponsesToOpenAIChat, from: types.RelayFormatOpenAIResponses, to: types.RelayFormatOpenAI, quality: RequestConverterQualityGood},
		{converter: requestConverterClaudeToGemini, from: types.RelayFormatClaude, to: types.RelayFormatGemini, quality: RequestConverterQualityDiscouraged, stepConverters: []string{ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToGeminiContent}},
		{converter: requestConverterClaudeToResponses, from: types.RelayFormatClaude, to: types.RelayFormatOpenAIResponses, quality: RequestConverterQualityFair, stepConverters: []string{ConverterClaudeMessagesToOpenAIChat, ConverterOpenAIChatToOpenAIResponses}},
		{converter: requestConverterGeminiToClaude, from: types.RelayFormatGemini, to: types.RelayFormatClaude, quality: RequestConverterQualityDiscouraged, stepConverters: []string{ConverterGeminiContentToOpenAIChat, ConverterOpenAIChatToClaudeMessages}},
		{converter: requestConverterGeminiToResponses, from: types.RelayFormatGemini, to: types.RelayFormatOpenAIResponses, quality: RequestConverterQualityFair, stepConverters: []string{ConverterGeminiContentToOpenAIChat, ConverterOpenAIChatToOpenAIResponses}},
		{converter: requestConverterResponsesToClaude, from: types.RelayFormatOpenAIResponses, to: types.RelayFormatClaude, quality: RequestConverterQualityFair},
		{converter: ConverterOpenAIResponsesToGemini, from: types.RelayFormatOpenAIResponses, to: types.RelayFormatGemini, quality: RequestConverterQualityFair},
	}

	require.Len(t, requestConverters, len(tests))

	for _, tt := range tests {
		t.Run(tt.converter, func(t *testing.T) {
			spec, ok := LookupRequestConverter(tt.converter)
			require.True(t, ok)
			assert.Equal(t, tt.converter, spec.ID)
			assert.Equal(t, tt.from, spec.From)
			assert.Equal(t, tt.to, spec.To)
			assert.Equal(t, tt.quality, spec.Quality)
			assert.Equal(t, tt.stepConverters, spec.StepConverters)
			if len(tt.stepConverters) == 0 {
				assert.NotNil(t, spec.Convert)
			} else {
				assert.Nil(t, spec.Convert)
			}
		})
	}
}

func TestLookupRequestConverterTrimAndMiss(t *testing.T) {
	spec, ok := LookupRequestConverter("  " + ConverterClaudeMessagesToOpenAIChat + "  ")
	require.True(t, ok)
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, spec.ID)

	_, ok = LookupRequestConverter("does_not_exist")
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// ConvertRequest
// ---------------------------------------------------------------------------

func TestConvertRequestSameFormatPassthrough(t *testing.T) {
	req := newOpenAIRequest()
	result, err := ConvertRequest(nil, infoFor(types.RelayFormatOpenAI), types.RelayFormatOpenAI, req)
	require.NoError(t, err)
	assert.Same(t, req, result.Value)
	assert.Equal(t, types.RelayFormatOpenAI, result.From)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), result.To)
	assert.Empty(t, result.Converter)
	assert.Empty(t, result.Steps)
}

func TestConvertRequestEmptyTarget(t *testing.T) {
	_, err := ConvertRequest(nil, infoFor(types.RelayFormatOpenAI), "", newOpenAIRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target relay format is required")
}

func TestConvertRequestNilRequest(t *testing.T) {
	_, err := ConvertRequest(nil, &relaycommon.RelayInfo{}, types.RelayFormatOpenAIResponses, (*dto.GeneralOpenAIRequest)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is nil")
}

func TestConvertRequestUnsupportedType(t *testing.T) {
	_, err := ConvertRequest(nil, &relaycommon.RelayInfo{}, types.RelayFormatOpenAI, 12345)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported request type")
}

func TestConvertRequestUnregisteredRoute(t *testing.T) {
	_, err := ConvertRequest(nil, &relaycommon.RelayInfo{}, types.RelayFormatEmbedding, newClaudeRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "from claude to embedding is not registered")
}

// Every registered single-hop request route, executed end to end.
func TestConvertRequestSingleHopRoutes(t *testing.T) {
	tests := []struct {
		name      string
		from      types.RelayFormat
		to        types.RelayFormat
		request   any
		converter string
		wantType  any
	}{
		{"claude->openai", types.RelayFormatClaude, types.RelayFormatOpenAI, newClaudeRequest(), ConverterClaudeMessagesToOpenAIChat, &dto.GeneralOpenAIRequest{}},
		{"openai->claude", types.RelayFormatOpenAI, types.RelayFormatClaude, newOpenAIRequest(), ConverterOpenAIChatToClaudeMessages, &dto.ClaudeRequest{}},
		{"gemini->openai", types.RelayFormatGemini, types.RelayFormatOpenAI, newGeminiRequest(), ConverterGeminiContentToOpenAIChat, &dto.GeneralOpenAIRequest{}},
		{"openai->gemini", types.RelayFormatOpenAI, types.RelayFormatGemini, newOpenAIRequest(), ConverterOpenAIChatToGeminiContent, &dto.GeminiChatRequest{}},
		{"openai->responses", types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, newOpenAIRequest(), ConverterOpenAIChatToOpenAIResponses, &dto.OpenAIResponsesRequest{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := infoFor(tt.from)
			result, err := ConvertRequest(nil, info, tt.to, tt.request)
			require.NoError(t, err)
			assert.IsType(t, tt.wantType, result.Value)
			assert.Equal(t, tt.from, result.From)
			assert.Equal(t, tt.to, result.To)
			assert.Equal(t, tt.converter, result.Converter)
			require.Len(t, result.Steps, 1)
			assert.Equal(t, tt.converter, result.Steps[0].Converter)
			assert.Equal(t, []types.RelayFormat{tt.from, tt.to}, info.RequestConversionChain)
		})
	}
}

// responses->openai needs a gin context for media resolution; run separately.
func TestConvertRequestResponsesToOpenAI(t *testing.T) {
	info := infoFor(types.RelayFormatOpenAIResponses)
	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAI, newResponsesRequest(t))
	require.NoError(t, err)
	assert.IsType(t, &dto.GeneralOpenAIRequest{}, result.Value)
	assert.Equal(t, ConverterOpenAIResponsesToOpenAIChat, result.Converter)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI}, info.RequestConversionChain)
}

// Chained (multi-step) request routes.
func TestConvertRequestChainedRoutes(t *testing.T) {
	tests := []struct {
		name      string
		from      types.RelayFormat
		to        types.RelayFormat
		request   any
		converter string
		steps     []RequestStep
	}{
		{
			name: "claude->gemini", from: types.RelayFormatClaude, to: types.RelayFormatGemini,
			request: newClaudeRequest(), converter: requestConverterClaudeToGemini,
			steps: []RequestStep{
				{Converter: ConverterClaudeMessagesToOpenAIChat, From: types.RelayFormatClaude, To: types.RelayFormatOpenAI},
				{Converter: ConverterOpenAIChatToGeminiContent, From: types.RelayFormatOpenAI, To: types.RelayFormatGemini},
			},
		},
		{
			name: "claude->responses", from: types.RelayFormatClaude, to: types.RelayFormatOpenAIResponses,
			request: newClaudeRequest(), converter: requestConverterClaudeToResponses,
			steps: []RequestStep{
				{Converter: ConverterClaudeMessagesToOpenAIChat, From: types.RelayFormatClaude, To: types.RelayFormatOpenAI},
				{Converter: ConverterOpenAIChatToOpenAIResponses, From: types.RelayFormatOpenAI, To: types.RelayFormatOpenAIResponses},
			},
		},
		{
			name: "gemini->claude", from: types.RelayFormatGemini, to: types.RelayFormatClaude,
			request: newGeminiRequest(), converter: requestConverterGeminiToClaude,
			steps: []RequestStep{
				{Converter: ConverterGeminiContentToOpenAIChat, From: types.RelayFormatGemini, To: types.RelayFormatOpenAI},
				{Converter: ConverterOpenAIChatToClaudeMessages, From: types.RelayFormatOpenAI, To: types.RelayFormatClaude},
			},
		},
		{
			name: "gemini->responses", from: types.RelayFormatGemini, to: types.RelayFormatOpenAIResponses,
			request: newGeminiRequest(), converter: requestConverterGeminiToResponses,
			steps: []RequestStep{
				{Converter: ConverterGeminiContentToOpenAIChat, From: types.RelayFormatGemini, To: types.RelayFormatOpenAI},
				{Converter: ConverterOpenAIChatToOpenAIResponses, From: types.RelayFormatOpenAI, To: types.RelayFormatOpenAIResponses},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := infoFor(tt.from)
			result, err := ConvertRequest(nil, info, tt.to, tt.request)
			require.NoError(t, err)
			assert.Equal(t, tt.converter, result.Converter)
			assert.Equal(t, tt.steps, result.Steps)
			assert.Equal(t, tt.to, result.To)
			assert.Equal(t, []types.RelayFormat{tt.from, types.RelayFormatOpenAI, tt.to}, info.RequestConversionChain)
		})
	}
}

// responses->claude and responses->gemini use DIRECT converters (not chained
// step converters) even though a step alias exists on the response side.
func TestConvertRequestResponsesToClaudeDirect(t *testing.T) {
	info := infoFor(types.RelayFormatOpenAIResponses)
	result, err := ConvertRequest(nil, info, types.RelayFormatClaude, newResponsesRequest(t))
	require.NoError(t, err)
	assert.IsType(t, &dto.ClaudeRequest{}, result.Value)
	assert.Equal(t, requestConverterResponsesToClaude, result.Converter)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, requestConverterResponsesToClaude, result.Steps[0].Converter)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatClaude}, info.RequestConversionChain)
}

func TestConvertRequestResponsesToGeminiDirect(t *testing.T) {
	info := infoFor(types.RelayFormatOpenAIResponses)
	result, err := ConvertRequest(nil, info, types.RelayFormatGemini, newResponsesRequest(t))
	require.NoError(t, err)
	geminiReq, ok := result.Value.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Len(t, geminiReq.Contents, 1)
	assert.Equal(t, "hello", geminiReq.Contents[0].Parts[0].Text)
	assert.Equal(t, ConverterOpenAIResponsesToGemini, result.Converter)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, types.RelayFormat(types.RelayFormatGemini), result.Steps[0].To)
}

// ---------------------------------------------------------------------------
// ConvertRequestVia
// ---------------------------------------------------------------------------

func TestConvertRequestViaMultiHop(t *testing.T) {
	info := infoFor(types.RelayFormatOpenAIResponses)
	result, err := ConvertRequestVia(nil, info, newResponsesRequest(t), types.RelayFormatOpenAI, types.RelayFormatGemini)
	require.NoError(t, err)
	assert.IsType(t, &dto.GeminiChatRequest{}, result.Value)
	assert.Equal(t, ConverterOpenAIResponsesToOpenAIChat+","+ConverterOpenAIChatToGeminiContent, result.Converter)
	assert.Equal(t, []RequestStep{
		{Converter: ConverterOpenAIResponsesToOpenAIChat, From: types.RelayFormatOpenAIResponses, To: types.RelayFormatOpenAI},
		{Converter: ConverterOpenAIChatToGeminiContent, From: types.RelayFormatOpenAI, To: types.RelayFormatGemini},
	}, result.Steps)
	assert.Equal(t, types.RelayFormat(types.RelayFormatGemini), result.To)
}

func TestConvertRequestViaSingleHop(t *testing.T) {
	info := infoFor(types.RelayFormatOpenAI)
	result, err := ConvertRequestVia(nil, info, newOpenAIRequest(), types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses)
	require.NoError(t, err)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, ConverterOpenAIChatToOpenAIResponses, result.Steps[0].Converter)
	assert.Empty(t, result.Quality, "Via does not carry a spec-level quality")
}

// First path element == from is trimmed, leaving only a single actual hop.
func TestConvertRequestViaTrimsLeadingFromElement(t *testing.T) {
	info := infoFor(types.RelayFormatOpenAI)
	result, err := ConvertRequestVia(nil, info, newOpenAIRequest(), types.RelayFormatOpenAI, types.RelayFormatClaude)
	require.NoError(t, err)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, ConverterOpenAIChatToClaudeMessages, result.Steps[0].Converter)
}

// After trimming, path is empty => passthrough result.
func TestConvertRequestViaTrimToEmptyPassthrough(t *testing.T) {
	req := newOpenAIRequest()
	result, err := ConvertRequestVia(nil, infoFor(types.RelayFormatOpenAI), req, types.RelayFormatOpenAI)
	require.NoError(t, err)
	assert.Same(t, req, result.Value)
	assert.Equal(t, types.RelayFormatOpenAI, result.From)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), result.To)
	assert.Empty(t, result.Steps)
}

func TestConvertRequestViaEmptyPath(t *testing.T) {
	_, err := ConvertRequestVia(nil, infoFor(types.RelayFormatOpenAI), newOpenAIRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conversion path is required")
}

func TestConvertRequestViaEmptyFormatInPath(t *testing.T) {
	_, err := ConvertRequestVia(nil, infoFor(types.RelayFormatOpenAI), newOpenAIRequest(), types.RelayFormatClaude, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains empty relay format")
}

func TestConvertRequestViaNilRequest(t *testing.T) {
	_, err := ConvertRequestVia(nil, infoFor(types.RelayFormatOpenAI), (*dto.GeneralOpenAIRequest)(nil), types.RelayFormatClaude)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is nil")
}

// A hop with no registered DIRECT route fails (Via only uses direct routes).
func TestConvertRequestViaUnregisteredDirectRoute(t *testing.T) {
	// claude -> gemini has no direct converter (only a chained one), so a Via
	// hop straight from claude to gemini is unregistered.
	_, err := ConvertRequestVia(nil, infoFor(types.RelayFormatClaude), newClaudeRequest(), types.RelayFormatGemini)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "from claude to gemini is not registered")
}

// ---------------------------------------------------------------------------
// ConvertRequestByID
// ---------------------------------------------------------------------------

func TestConvertRequestByIDDirect(t *testing.T) {
	info := infoFor(types.RelayFormatOpenAI)
	result, err := ConvertRequestByID(nil, info, ConverterOpenAIChatToOpenAIResponses, newOpenAIRequest())
	require.NoError(t, err)
	assert.IsType(t, &dto.OpenAIResponsesRequest{}, result.Value)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, ConverterOpenAIChatToOpenAIResponses, result.Converter)
	assert.Equal(t, RequestConverterQualityGood, result.Quality)
}

func TestConvertRequestByIDMultiHop(t *testing.T) {
	info := infoFor(types.RelayFormatClaude)
	result, err := ConvertRequestByID(nil, info, requestConverterClaudeToResponses, newClaudeRequest())
	require.NoError(t, err)
	assert.IsType(t, &dto.OpenAIResponsesRequest{}, result.Value)
	assert.Equal(t, requestConverterClaudeToResponses, result.Converter)
	assert.Equal(t, []RequestStep{
		{Converter: ConverterClaudeMessagesToOpenAIChat, From: types.RelayFormatClaude, To: types.RelayFormatOpenAI},
		{Converter: ConverterOpenAIChatToOpenAIResponses, From: types.RelayFormatOpenAI, To: types.RelayFormatOpenAIResponses},
	}, result.Steps)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}, info.RequestConversionChain)
}

func TestConvertRequestByIDNotRegistered(t *testing.T) {
	_, err := ConvertRequestByID(nil, &relaycommon.RelayInfo{}, "missing_converter", newOpenAIRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestConvertRequestByIDFromMismatch(t *testing.T) {
	_, err := ConvertRequestByID(nil, &relaycommon.RelayInfo{}, ConverterOpenAIChatToOpenAIResponses, newClaudeRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects openai request")
}

func TestConvertRequestByIDNilRequest(t *testing.T) {
	_, err := ConvertRequestByID(nil, &relaycommon.RelayInfo{}, ConverterOpenAIChatToOpenAIResponses, (*dto.GeneralOpenAIRequest)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is nil")
}

// ---------------------------------------------------------------------------
// Step-execution error propagation
// ---------------------------------------------------------------------------

func TestExecuteRequestStepsPropagatesConvertError(t *testing.T) {
	sentinel := errors.New("boom")
	spec := RequestConverterSpec{
		ID:      "fake_req_err",
		From:    types.RelayFormatOpenAI,
		To:      types.RelayFormatClaude,
		Quality: RequestConverterQualityFair,
		Convert: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ any) (any, error) {
			return nil, sentinel
		},
	}
	_, err := executeRequestSpec(nil, infoFor(types.RelayFormatOpenAI), types.RelayFormatOpenAI, types.RelayFormatClaude, newOpenAIRequest(), spec)
	require.ErrorIs(t, err, sentinel)
}

func TestExecuteRequestStepNoImplementation(t *testing.T) {
	spec := RequestConverterSpec{ID: "no_impl", From: types.RelayFormatOpenAI, To: types.RelayFormatClaude}
	_, _, err := executeRequestStep(nil, nil, spec, newOpenAIRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no registered implementation")
}

// executeRequestStep with nil info must not panic (info-guard branch).
func TestExecuteRequestStepNilInfo(t *testing.T) {
	spec := RequestConverterSpec{
		ID:   "ok",
		From: types.RelayFormatOpenAI,
		To:   types.RelayFormatClaude,
		Convert: func(_ *gin.Context, _ *relaycommon.RelayInfo, req any) (any, error) {
			return req, nil
		},
	}
	value, step, err := executeRequestStep(nil, nil, spec, newOpenAIRequest())
	require.NoError(t, err)
	assert.NotNil(t, value)
	assert.Equal(t, "ok", step.Converter)
}

// ---------------------------------------------------------------------------
// expandRequestConverterSteps
// ---------------------------------------------------------------------------

func TestExpandRequestConverterStepsDirectNoImpl(t *testing.T) {
	_, err := expandRequestConverterSteps(RequestConverterSpec{ID: "x", From: types.RelayFormatOpenAI, To: types.RelayFormatClaude})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no registered implementation")
}

func TestExpandRequestConverterStepsMixesDirectAndSteps(t *testing.T) {
	spec := RequestConverterSpec{
		ID:             "x",
		From:           types.RelayFormatOpenAI,
		To:             types.RelayFormatClaude,
		Convert:        func(_ *gin.Context, _ *relaycommon.RelayInfo, r any) (any, error) { return r, nil },
		StepConverters: []string{ConverterClaudeMessagesToOpenAIChat},
	}
	_, err := expandRequestConverterSteps(spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot mix direct and step conversion")
}

func TestExpandRequestConverterStepsMissingStep(t *testing.T) {
	spec := RequestConverterSpec{
		ID:             "x",
		From:           types.RelayFormatOpenAI,
		To:             types.RelayFormatClaude,
		StepConverters: []string{"unknown_step"},
	}
	_, err := expandRequestConverterSteps(spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references missing step converter")
}

func TestExpandRequestConverterStepsStepNotDirect(t *testing.T) {
	// requestConverterClaudeToGemini is itself a multi-step converter, so using
	// it as a step is invalid.
	spec := RequestConverterSpec{
		ID:             "x",
		From:           types.RelayFormatClaude,
		To:             types.RelayFormatGemini,
		StepConverters: []string{requestConverterClaudeToGemini},
	}
	_, err := expandRequestConverterSteps(spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a direct converter")
}

func TestExpandRequestConverterStepsFromMismatch(t *testing.T) {
	spec := RequestConverterSpec{
		ID:             "x",
		From:           types.RelayFormatGemini, // step expects claude
		To:             types.RelayFormatOpenAI,
		StepConverters: []string{ConverterClaudeMessagesToOpenAIChat},
	}
	_, err := expandRequestConverterSteps(spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects claude request")
}

func TestExpandRequestConverterStepsWrongEnd(t *testing.T) {
	spec := RequestConverterSpec{
		ID:             "x",
		From:           types.RelayFormatClaude,
		To:             types.RelayFormatGemini, // step ends at openai, not gemini
		StepConverters: []string{ConverterClaudeMessagesToOpenAIChat},
	}
	_, err := expandRequestConverterSteps(spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ends at openai, expected gemini")
}

// ---------------------------------------------------------------------------
// prepareRequestForStep
// ---------------------------------------------------------------------------

func TestPrepareRequestForStepPassthroughWhenNotResponsesToGemini(t *testing.T) {
	req := newOpenAIRequest()
	// spec.From != responses => passthrough unchanged.
	out, err := prepareRequestForStep(req, RequestConverterSpec{From: types.RelayFormatOpenAI}, types.RelayFormatGemini)
	require.NoError(t, err)
	assert.Same(t, req, out)

	// finalTarget != gemini => passthrough unchanged.
	out, err = prepareRequestForStep(req, RequestConverterSpec{From: types.RelayFormatOpenAIResponses}, types.RelayFormatClaude)
	require.NoError(t, err)
	assert.Same(t, req, out)
}

func TestPrepareRequestForStepResponsesToGeminiPointer(t *testing.T) {
	req := newResponsesRequest(t)
	out, err := prepareRequestForStep(req, RequestConverterSpec{From: types.RelayFormatOpenAIResponses}, types.RelayFormatGemini)
	require.NoError(t, err)
	assert.IsType(t, &dto.OpenAIResponsesRequest{}, out)
	assert.NotSame(t, req, out, "prepared request is a fresh pointer")
}

func TestPrepareRequestForStepResponsesToGeminiValue(t *testing.T) {
	req := *newResponsesRequest(t)
	out, err := prepareRequestForStep(req, RequestConverterSpec{From: types.RelayFormatOpenAIResponses}, types.RelayFormatGemini)
	require.NoError(t, err)
	assert.IsType(t, &dto.OpenAIResponsesRequest{}, out)
}

func TestPrepareRequestForStepResponsesToGeminiWrongType(t *testing.T) {
	_, err := prepareRequestForStep(newOpenAIRequest(), RequestConverterSpec{From: types.RelayFormatOpenAIResponses}, types.RelayFormatGemini)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected OpenAI responses request")
}

// ---------------------------------------------------------------------------
// cloneRequestConverterSpec — deep-copy independence
// ---------------------------------------------------------------------------

func TestCloneRequestConverterSpecIndependence(t *testing.T) {
	spec, ok := LookupRequestConverter(requestConverterClaudeToResponses)
	require.True(t, ok)
	require.NotEmpty(t, spec.StepConverters)
	spec.StepConverters[0] = "mutated"

	fresh, ok := LookupRequestConverter(requestConverterClaudeToResponses)
	require.True(t, ok)
	assert.Equal(t, ConverterClaudeMessagesToOpenAIChat, fresh.StepConverters[0], "registry copy is not affected by caller mutation")
}

// ---------------------------------------------------------------------------
// registerBuiltinRequestConverter — validation panics
// ---------------------------------------------------------------------------

func TestRegisterBuiltinRequestConverterPanics(t *testing.T) {
	noop := func(_ *gin.Context, _ *relaycommon.RelayInfo, r any) (any, error) { return r, nil }

	tests := []struct {
		name string
		spec RequestConverterSpec
		msg  string
	}{
		{"empty id", RequestConverterSpec{}, "ID is required"},
		{"missing from/to", RequestConverterSpec{ID: "z1", Quality: RequestConverterQualityFair, Convert: noop}, "must declare from and to"},
		{"missing quality", RequestConverterSpec{ID: "z2", From: types.RelayFormat("fake-a"), To: types.RelayFormat("fake-b"), Convert: noop}, "must declare quality"},
		{"no impl", RequestConverterSpec{ID: "z3", From: types.RelayFormat("fake-a"), To: types.RelayFormat("fake-b"), Quality: RequestConverterQualityFair}, "must declare convert or step converters"},
		{"convert and steps", RequestConverterSpec{ID: "z4", From: types.RelayFormat("fake-a"), To: types.RelayFormat("fake-b"), Quality: RequestConverterQualityFair, Convert: noop, StepConverters: []string{ConverterClaudeMessagesToOpenAIChat}}, "cannot declare convert and step converters together"},
		{"duplicate id", RequestConverterSpec{ID: ConverterClaudeMessagesToOpenAIChat, From: types.RelayFormat("fake-a"), To: types.RelayFormat("fake-b"), Quality: RequestConverterQualityFair, Convert: noop}, "is already registered"},
		{"duplicate route", RequestConverterSpec{ID: "z5", From: types.RelayFormatClaude, To: types.RelayFormatOpenAI, Quality: RequestConverterQualityFair, Convert: noop}, "route from claude to openai is already registered"},
		{"unknown step", RequestConverterSpec{ID: "z6", From: types.RelayFormat("fake-a"), To: types.RelayFormat("fake-b"), Quality: RequestConverterQualityFair, StepConverters: []string{"unknown_step"}}, "references unknown step converter"},
		{"step not direct", RequestConverterSpec{ID: "z7", From: types.RelayFormat("fake-nd-a"), To: types.RelayFormat("fake-nd-b"), Quality: RequestConverterQualityFair, StepConverters: []string{requestConverterClaudeToGemini}}, "must be a direct converter"},
		{"step from mismatch", RequestConverterSpec{ID: "z8", From: types.RelayFormat("fake-fm-a"), To: types.RelayFormatOpenAI, Quality: RequestConverterQualityFair, StepConverters: []string{ConverterClaudeMessagesToOpenAIChat}}, "expects claude after fake-fm-a"},
		{"step wrong end", RequestConverterSpec{ID: "z9", From: types.RelayFormatClaude, To: types.RelayFormat("fake-we-b"), Quality: RequestConverterQualityFair, StepConverters: []string{ConverterClaudeMessagesToOpenAIChat}}, "ends at openai, expected fake-we-b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanicContains(t, tt.msg, func() {
				registerBuiltinRequestConverter(tt.spec)
			})
		})
	}
}

// assertPanicContains asserts fn panics with a value whose string form contains
// substr. Used for registry validation panics.
func assertPanicContains(t *testing.T, substr string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic containing %q", substr)
		assert.Contains(t, panicString(r), substr)
	}()
	fn()
}

func panicString(r any) string {
	switch v := r.(type) {
	case string:
		return v
	case error:
		return v.Error()
	default:
		return ""
	}
}
