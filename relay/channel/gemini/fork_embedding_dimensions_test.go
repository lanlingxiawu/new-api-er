package gemini

import (
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestGeminiEmbeddingDimensionsOnSerializedBatch(t *testing.T) {
	for _, model := range []string{"gemini-embedding-2", "gemini-embedding-2-preview", "gemini-embedding-001", "embedding-001"} {
		for _, dimensions := range []*int{nil, common.GetPointer(0), common.GetPointer(256)} {
			t.Run(model, func(t *testing.T) {
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model}}
				converted, err := (&Adaptor{}).ConvertEmbeddingRequest(nil, info, dto.EmbeddingRequest{
					Model: model, Input: []any{"first input", "second input"}, Dimensions: dimensions,
				})
				require.NoError(t, err)
				wire, err := common.Marshal(converted)
				require.NoError(t, err)
				requests := ojson.GetBytes(wire, "requests").Array()
				require.Len(t, requests, 2)
				require.True(t, info.IsGeminiBatchEmbedding)
				for i, request := range requests {
					require.Equal(t, "models/"+model, request.Get("model").String())
					require.Equal(t, []string{"first input", "second input"}[i], request.Get("content.parts.0.text").String())
					value := request.Get("outputDimensionality")
					if model != "embedding-001" && dimensions != nil && *dimensions > 0 {
						require.Equal(t, int64(*dimensions), value.Int())
					} else {
						require.False(t, value.Exists())
					}
				}
			})
		}
	}
}
