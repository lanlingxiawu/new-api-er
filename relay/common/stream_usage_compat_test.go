package common

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestStreamUsageCompatibilityEvidence(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cache_creation_input_tokens":30,"cache_write_tokens_details":{"ttl_30m_tokens":10,"ttl_12h_tokens":20},"cached_tokens_details":{"text_tokens":0,"image_tokens":12}}}}`)))
	e := s.Snapshot().Evidence
	require.Equal(t, 100, e["input_tokens"])
	_, hasCreation := e["openai_cache_creation_input_tokens"]
	require.True(t, hasCreation)
	require.Equal(t, 30, e["openai_cache_creation_input_tokens"])
	require.Equal(t, 20, e["cache_write_tokens_details.ttl_12h_tokens"])
	require.Contains(t, e, "cached_tokens_details.text_tokens")
	require.NotContains(t, e, "cached_tokens_details.audio_tokens")
	require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens_details":{"cached_tokens_details":{"image_tokens":0}}}}`)))
	require.Zero(t, s.Snapshot().Evidence["cached_tokens_details.image_tokens"])
}

func TestStreamUsageCompatibilityNullableCacheModality(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens_details":{"text_tokens":null,"image_tokens":0}}}}`)))
	require.NotContains(t, s.Snapshot().Evidence, "cached_tokens_details.text_tokens")
	require.Contains(t, s.Snapshot().Evidence, "cached_tokens_details.image_tokens")
	require.Zero(t, s.Snapshot().Evidence["cached_tokens_details.image_tokens"])
}

func TestStreamUsageCompatibilityGeminiFragments(t *testing.T) {
	s := NewStreamSession(types.RelayFormatGemini)
	require.NoError(t, s.ObserveEvent("", []byte(`{"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":30,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":40},{"modality":" audio ","tokenCount":2}],"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":30}]}}`)))
	require.NoError(t, s.ObserveEvent("", []byte(`{"usageMetadata":{"toolUsePromptTokenCount":0,"promptTokensDetails":[{"modality":"TEXT","tokenCount":60}],"candidatesTokensDetails":[{"modality":"AUDIO","tokenCount":8}]}}`)))
	e := s.Snapshot().Evidence
	require.Equal(t, 100, e["input_tokens"])
	require.Contains(t, e, "tool_use_prompt_tokens")
	require.Zero(t, e["tool_use_prompt_tokens"])
	require.Equal(t, 42, e["input_audio_tokens"])
	require.Equal(t, 60, e["input_text_tokens"])
	require.Equal(t, 30, e["tool_input_text_tokens"])
	require.Equal(t, 8, e["output_audio_tokens"])
	require.NoError(t, s.ObserveEvent("", []byte(`{"usageMetadata":{"promptTokensDetails":[{"modality":"AUDIO","tokenCount":0}]}}`)))
	require.Zero(t, s.Snapshot().Evidence["input_audio_tokens"])
}

func TestStreamUsageCompatibilityInvalidBatchPreservesEvidence(t *testing.T) {
	for _, invalid := range []string{
		`{"usage":{"prompt_tokens":999,"prompt_tokens_details":{"cache_creation_input_tokens":-1}}}`,
		`{"usage":{"prompt_tokens":999,"prompt_tokens_details":{"cached_tokens_details":{"image_tokens":1.5}}}}`,
		`{"usageMetadata":{"promptTokenCount":999,"toolUsePromptTokenCount":1000000001}}`,
		`{"usageMetadata":{"promptTokenCount":999,"promptTokensDetails":{}}}`,
		`{"usageMetadata":{"promptTokenCount":999,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":-1}]}}`,
		`{"usageMetadata":{"promptTokenCount":999,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":1000000000},{"modality":"AUDIO","tokenCount":1}]}}`,
	} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":7}}`)))
		require.Error(t, s.ObserveEvent("", []byte(invalid)), invalid)
		require.Equal(t, map[string]int{"input_tokens": 7}, s.Snapshot().Evidence)
	}
}
