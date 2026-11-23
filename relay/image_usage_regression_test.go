package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageHelperPreservesZeroInputWithReportedOutput(t *testing.T) {
	for _, tc := range []struct {
		name, usage          string
		input, output, total int
	}{
		{"zero_input", `,"usage":{"input_tokens":0,"output_tokens":1000,"total_tokens":1000}`, 0, 1000, 1000},
		{"minimum_output_missing_total", `,"usage":{"input_tokens":0,"output_tokens":1}`, 0, 1, 1},
		{"positive_input", `,"usage":{"input_tokens":12,"output_tokens":1000,"total_tokens":1012}`, 12, 1000, 1012},
		{"zero_output", `,"usage":{"input_tokens":12,"output_tokens":0,"total_tokens":12}`, 12, 0, 12},
		{"missing_usage_legacy_fallback", "", 1, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"data":[{"b64_json":"aW1hZ2U="}]` + tc.usage + `}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-2-c","prompt":"fixture","n":1}`))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-image-2-c")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeImagesGenerations,
				RequestURLPath: "/v1/images/generations", OriginModelName: "gpt-image-2-c",
				Request: &dto.ImageRequest{Model: "gpt-image-2-c", Prompt: "fixture"}, ChannelMeta: &relaycommon.ChannelMeta{},
				// Capture the actual settlement input using the existing passive
				// sink, then stop before wallets/logs via the idempotency guard.
				StreamProcessingOwner: relaycommon.StreamOwnerMain,
				StreamResult:          &relaycommon.StreamOutcome{SettlementAttempted: true},
			}
			require.Nil(t, ImageHelper(c, info))
			require.JSONEq(t, body, rec.Body.String())
			require.NotNil(t, info.StreamFinalUsage)
			require.Equal(t, tc.input, info.StreamFinalUsage.PromptTokens)
			require.Equal(t, tc.output, info.StreamFinalUsage.CompletionTokens)
			require.Equal(t, tc.total, info.StreamFinalUsage.TotalTokens)
		})
	}
}
