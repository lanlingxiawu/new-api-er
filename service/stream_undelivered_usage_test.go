package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUndeliveredConfirmedUsageLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, frame string
		evidence    map[string]int
	}{
		{"reported counts", `{"usage":{"prompt_tokens":20,"completion_tokens":3}}`, map[string]int{"input_tokens": 20, "output_tokens": 3}},
		{"cache without input", `{"usage":{"prompt_tokens_details":{"cached_tokens":120}}}`, map[string]int{"cached_tokens": 120}},
		{"explicit zeros", `{"usage":{"prompt_tokens":0,"completion_tokens":0}}`, map[string]int{"input_tokens": 0, "output_tokens": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			info.SetEstimatePromptTokens(999)
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			require.NoError(t, info.StreamSession.ObserveEvent("", []byte(tc.frame)))
			info.StreamSession.EndRead(io.EOF)
			usage := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 999, CompletionTokens: 999})
			require.True(t, info.StreamResult.Failed)
			require.False(t, info.StreamResult.EffectiveContent)
			require.True(t, info.StreamResult.ConfirmedUsage)
			require.Equal(t, "none", info.StreamResult.UsageSource)
			require.Equal(t, &dto.Usage{}, usage)
			require.Equal(t, tc.evidence, info.StreamResult.Diagnostic.UsageEvidence)
			require.False(t, info.StreamResult.Diagnostic.UsageFinal)
			require.Same(t, usage, SupplementStreamPartialOutput(c, info, usage, 1000, 0))
			require.Nil(t, info.StreamResult.Diagnostic.EstimatedUsage)
		})
	}
}
