package mistral

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestOpenAI2Mistral_ValidToolCallIdPreserved(t *testing.T) {
	msg := dto.Message{Role: "assistant"}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "abc123XYZ", Type: "function", Function: dto.FunctionRequest{Name: "f"}}, // 9 alnum -> kept
	})
	out := requestOpenAI2Mistral(&dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{msg},
	})
	require.Len(t, out.Messages, 1)
	calls := out.Messages[0].ParseToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "abc123XYZ", calls[0].ID)
}

func TestRequestOpenAI2Mistral_InvalidToolCallIdRemapped(t *testing.T) {
	msg := dto.Message{Role: "assistant"}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "call_toolongandinvalid", Type: "function", Function: dto.FunctionRequest{Name: "f"}},
	})
	out := requestOpenAI2Mistral(&dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{msg},
	})
	calls := out.Messages[0].ParseToolCalls()
	require.Len(t, calls, 1)
	assert.NotEqual(t, "call_toolongandinvalid", calls[0].ID)
	assert.Regexp(t, "^[a-zA-Z0-9]{9}$", calls[0].ID)
}

func TestRequestOpenAI2Mistral_ToolCallIdAndToolCallConsistentRemap(t *testing.T) {
	// A tool_call in an assistant message and a later tool message with the
	// matching tool_call_id must be remapped to the SAME new id.
	assistant := dto.Message{Role: "assistant"}
	assistant.SetToolCalls([]dto.ToolCallRequest{
		{ID: "invalid_id_1", Type: "function", Function: dto.FunctionRequest{Name: "f"}},
	})
	toolMsg := dto.Message{Role: "tool", ToolCallId: "invalid_id_1", Content: "result"}

	out := requestOpenAI2Mistral(&dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{assistant, toolMsg},
	})
	require.Len(t, out.Messages, 2)
	newID := out.Messages[0].ParseToolCalls()[0].ID
	assert.Regexp(t, "^[a-zA-Z0-9]{9}$", newID)
	assert.Equal(t, newID, out.Messages[1].ToolCallId)
}

func TestRequestOpenAI2Mistral_ValidToolCallIdOnToolMessagePreserved(t *testing.T) {
	toolMsg := dto.Message{Role: "tool", ToolCallId: "abc123XYZ", Content: "result"}
	out := requestOpenAI2Mistral(&dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{toolMsg},
	})
	assert.Equal(t, "abc123XYZ", out.Messages[0].ToolCallId)
}

func TestRequestOpenAI2Mistral_ImageUrlFlattened(t *testing.T) {
	// Client-style structured content with an image_url object.
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeText, Text: "hi"},
		{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "https://img/x.png"}},
	})
	out := requestOpenAI2Mistral(&dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{msg},
	})
	require.Len(t, out.Messages, 1)
	// transform completes without panicking and message is carried over
	assert.Equal(t, "user", out.Messages[0].Role)
}

func TestRequestOpenAI2Mistral_MaxTokensPropagated(t *testing.T) {
	mt := uint(256)
	out := requestOpenAI2Mistral(&dto.GeneralOpenAIRequest{
		Model:     "mistral-large-latest",
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		MaxTokens: &mt,
	})
	require.NotNil(t, out.MaxTokens)
	assert.Equal(t, uint(256), *out.MaxTokens)
}

func TestRequestOpenAI2Mistral_NoMaxTokensLeftNil(t *testing.T) {
	out := requestOpenAI2Mistral(&dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	})
	assert.Nil(t, out.MaxTokens)
}

func TestConvertOpenAIRequest_Nil(t *testing.T) {
	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, nil)
	require.Error(t, err)
}

func TestConvertOpenAIRequest_Normal(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
	require.NoError(t, err)
	converted, ok := out.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Equal(t, "mistral-large-latest", converted.Model)
}

func TestGetRequestURL(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RequestURLPath: "/v1/chat/completions",
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.mistral.ai"},
	}
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.mistral.ai/v1/chat/completions", url)
}

func TestConvertClaudeRequestReturnsError(t *testing.T) {
	_, err := (&Adaptor{}).ConvertClaudeRequest(nil, nil, nil)
	assert.ErrorIs(t, err, relaycommon.ErrLegacyAdaptorNotImplemented)
	assert.NotPanics(t, func() {
		_, _ = (&Adaptor{}).ConvertClaudeRequest(nil, nil, nil)
	})
}

func TestUnimplemented(t *testing.T) {
	a := &Adaptor{}
	_, err := a.ConvertGeminiRequest(nil, nil, nil)
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertImageRequest(nil, nil, dto.ImageRequest{})
	assert.Error(t, err)
	_, err = a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
	// ConvertRerankRequest returns nil,nil
	out, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	assert.NoError(t, err)
	assert.Nil(t, out)
}

func TestMetadata(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, "mistral", a.GetChannelName())
	assert.NotEmpty(t, a.GetModelList())
}
