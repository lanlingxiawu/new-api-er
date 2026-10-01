package relay

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relay-timeout-cost-bearing.md §3.2 (decision 2026-10-01): a stream-adapted
// charge request cut by our deadline before any output bills at least the
// input — upstream-confirmed input when a usage frame arrived, else the
// estimate; output 0 — and still answers 504. Before, no chunk meant 0.
func TestAdaptedTimeoutWithoutOutputBillsInput(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, tc := range []struct {
		name    string
		body    string
		perCall bool
		ratio   float64
		prompt  int
		note    bool
	}{
		{name: "no chunks: estimated input", body: "", ratio: 1, prompt: 30, note: true},
		{name: "role only: estimated input", body: `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}` + "\n", ratio: 1, prompt: 30, note: true},
		{name: "usage frame: confirmed input", body: `data: {"id":"c1","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":0,"total_tokens":12}}` + "\n", ratio: 1, prompt: 12, note: true},
		{name: "per call stays unbilled", body: "", perCall: true, prompt: 0},
		{name: "free model stays unbilled", body: "", ratio: 0, prompt: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec, info := bufferedCase(t)
			markTimedOut(c)
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, "charge")
			info.SetEstimatePromptTokens(30)
			info.PriceData.ModelRatio = tc.ratio
			info.PriceData.CompletionRatio = 1
			info.PriceData.GroupRatioInfo.GroupRatio = 1
			if tc.perCall {
				info.PriceData.UsePrice = true
				info.PriceData.ModelPrice = 0.01
			}
			var settled *dto.Usage
			var notes []string
			restore := stubSettlement(func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, extra []string) {
				settled, notes = usage, extra
			})
			defer restore()

			_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(tc.body))
			require.Nil(t, apiErr)
			assert.Equal(t, http.StatusGatewayTimeout, rec.Code, "still a timeout for the client")
			require.NotNil(t, settled)
			assert.Equal(t, tc.prompt, settled.PromptTokens)
			assert.Zero(t, settled.CompletionTokens)
			assert.Equal(t, tc.note, len(notes) == 1 && strings.Contains(notes[0], "charged for input only"), notes)
		})
	}
}
