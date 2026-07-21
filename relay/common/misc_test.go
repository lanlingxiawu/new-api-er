package common

import (
	"io"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GuessRelayFormatFromRequest / AppendRequestConversionFromRequest
// ---------------------------------------------------------------------------

func TestGuessRelayFormatFromRequest(t *testing.T) {
	tests := []struct {
		name string
		req  any
		want types.RelayFormat
		ok   bool
	}{
		{"openai ptr", &dto.GeneralOpenAIRequest{}, types.RelayFormatOpenAI, true},
		{"openai val", dto.GeneralOpenAIRequest{}, types.RelayFormatOpenAI, true},
		{"responses", &dto.OpenAIResponsesRequest{}, types.RelayFormatOpenAIResponses, true},
		{"claude", &dto.ClaudeRequest{}, types.RelayFormatClaude, true},
		{"gemini", &dto.GeminiChatRequest{}, types.RelayFormatGemini, true},
		{"embedding", &dto.EmbeddingRequest{}, types.RelayFormatEmbedding, true},
		{"rerank", &dto.RerankRequest{}, types.RelayFormatRerank, true},
		{"image", &dto.ImageRequest{}, types.RelayFormatOpenAIImage, true},
		{"audio", &dto.AudioRequest{}, types.RelayFormatOpenAIAudio, true},
		{"unknown", struct{}{}, "", false},
		{"nil", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := GuessRelayFormatFromRequest(tt.req)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAppendRequestConversionFromRequest(t *testing.T) {
	t.Run("nil info is a no-op", func(t *testing.T) {
		assert.NotPanics(t, func() { AppendRequestConversionFromRequest(nil, &dto.ClaudeRequest{}) })
	})
	t.Run("unknown request does not append", func(t *testing.T) {
		info := &RelayInfo{}
		AppendRequestConversionFromRequest(info, struct{}{})
		assert.Empty(t, info.RequestConversionChain)
	})
	t.Run("known request appends format", func(t *testing.T) {
		info := &RelayInfo{}
		AppendRequestConversionFromRequest(info, &dto.ClaudeRequest{})
		require.Len(t, info.RequestConversionChain, 1)
		assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.RequestConversionChain[0])
	})
}

// ---------------------------------------------------------------------------
// NewOutboundJSONBody
// ---------------------------------------------------------------------------

func TestNewOutboundJSONBody(t *testing.T) {
	data := []byte(`{"model":"gpt-4o","stream":true}`)
	body, size, closer, err := NewOutboundJSONBody(data)
	require.NoError(t, err)
	require.NotNil(t, body)
	require.NotNil(t, closer)
	assert.Equal(t, int64(len(data)), size)

	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, data, got)
	require.NoError(t, closer.Close())
}

func TestNewOutboundJSONBody_Empty(t *testing.T) {
	body, size, closer, err := NewOutboundJSONBody([]byte{})
	require.NoError(t, err)
	require.NotNil(t, closer)
	assert.Equal(t, int64(0), size)
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Empty(t, got)
	require.NoError(t, closer.Close())
}

// ---------------------------------------------------------------------------
// ParamOverrideReturnError family
// ---------------------------------------------------------------------------

func TestParamOverrideReturnError_Error(t *testing.T) {
	var nilErr *ParamOverrideReturnError
	assert.Equal(t, "param override return error", nilErr.Error())
	assert.Equal(t, "param override return error", (&ParamOverrideReturnError{}).Error())
	assert.Equal(t, "blocked", (&ParamOverrideReturnError{Message: "blocked"}).Error())
}

func TestAsParamOverrideReturnError(t *testing.T) {
	_, ok := AsParamOverrideReturnError(nil)
	assert.False(t, ok)

	_, ok = AsParamOverrideReturnError(assertErr("plain"))
	assert.False(t, ok)

	target := &ParamOverrideReturnError{Message: "x"}
	got, ok := AsParamOverrideReturnError(target)
	require.True(t, ok)
	assert.Equal(t, target, got)
}

func TestNewAPIErrorFromParamOverride(t *testing.T) {
	t.Run("nil error yields channel invalid", func(t *testing.T) {
		apiErr := NewAPIErrorFromParamOverride(nil)
		require.NotNil(t, apiErr)
	})
	t.Run("defaults applied for empty fields", func(t *testing.T) {
		apiErr := NewAPIErrorFromParamOverride(&ParamOverrideReturnError{})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Contains(t, apiErr.Error(), "request blocked by param override")
	})
	t.Run("custom fields preserved", func(t *testing.T) {
		apiErr := NewAPIErrorFromParamOverride(&ParamOverrideReturnError{
			Message:    "denied",
			StatusCode: http.StatusForbidden,
			Code:       "custom_code",
			Type:       "custom_type",
			SkipRetry:  true,
		})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
		assert.Contains(t, apiErr.Error(), "denied")
		assert.True(t, types.IsSkipRetryError(apiErr))
	})
	t.Run("out-of-range status normalized to 400", func(t *testing.T) {
		apiErr := NewAPIErrorFromParamOverride(&ParamOverrideReturnError{StatusCode: 99, Message: "m"})
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	})
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
