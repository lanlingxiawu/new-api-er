package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/require"
)

func TestGeminiEmbeddingUsageMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, metadata string
		wantPrompt     int
	}{
		{"absent", "", 0},
		{"null", `,"usageMetadata":null`, 0},
		{"empty", `,"usageMetadata":{}`, 0},
		{"zero", `,"usageMetadata":{"promptTokenCount":0}`, 0},
		{"negative", `,"usageMetadata":{"promptTokenCount":-1}`, 0},
		{"minimum", `,"usageMetadata":{"promptTokenCount":1}`, 1},
		{"modalities", `,"usageMetadata":{"promptTokenCount":123,"promptTokenDetails":[{"modality":"TEXT","tokenCount":23},{"modality":"IMAGE","tokenCount":100}]}`, 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var single GeminiEmbeddingResponse
			require.NoError(t, kitutil.Unmarshal([]byte(`{"embedding":{"values":[0.1]}`+tc.metadata+"}"), &single))
			var batch GeminiBatchEmbeddingResponse
			require.NoError(t, kitutil.Unmarshal([]byte(`{"embeddings":[{"values":[0.1]}]`+tc.metadata+"}"), &batch))
			for _, metadata := range []*GeminiEmbeddingUsageMetadata{single.UsageMetadata, batch.UsageMetadata} {
				usage := metadata.GeminiUsageMetadata()
				if tc.wantPrompt == 0 {
					require.Nil(t, usage, "unreported or nonpositive totals retain host estimation")
					continue
				}
				require.NotNil(t, usage)
				require.Equal(t, tc.wantPrompt, usage.PromptTokenCount)
				require.Equal(t, tc.wantPrompt, usage.TotalTokenCount)
				require.Zero(t, usage.CandidatesTokenCount)
				require.Equal(t, metadata.PromptTokenDetails, usage.PromptTokensDetails)
			}
		})
	}
}
