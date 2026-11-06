package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestForkAuditEmbeddingReportedUsage records the observed billing selection on
// this checkout. It only invokes response handlers; no settlement or DB is used.
func TestForkAuditEmbeddingReportedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const adoptsReportedUsage = true
	for _, route := range []string{"openai", "native_single", "native_batch"} {
		for _, tc := range []struct {
			name     string
			estimate int
			metadata string
			reported int
		}{
			{"estimate_below_report", 50, `,"usageMetadata":{"promptTokenCount":123,"promptTokenDetails":[{"modality":"TEXT","tokenCount":23},{"modality":"IMAGE","tokenCount":100}]}`, 123},
			{"estimate_above_report", 500, `,"usageMetadata":{"promptTokenCount":123}`, 123},
			{"no_metadata_fallback", 50, "", 0},
			{"null_metadata_fallback", 50, `,"usageMetadata":null`, 0},
			{"zero_report_fallback", 50, `,"usageMetadata":{"promptTokenCount":0}`, 0},
			{"negative_report_fallback", 50, `,"usageMetadata":{"promptTokenCount":-1}`, -1},
			{"minimum_positive_report", 50, `,"usageMetadata":{"promptTokenCount":1}`, 1},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				payload := `{"embeddings":[{"values":[0.1,0.2]}]` + tc.metadata + "}"
				if route == "native_single" {
					payload = `{"embedding":{"values":[0.1,0.2]}` + tc.metadata + "}"
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
				info := &relaycommon.RelayInfo{
					OriginModelName:        "gemini-embedding-audit",
					ChannelMeta:            &relaycommon.ChannelMeta{UpstreamModelName: "gemini-embedding-audit"},
					IsGeminiBatchEmbedding: route == "native_batch",
				}
				info.SetEstimatePromptTokens(tc.estimate)
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}
				var usage *dto.Usage
				if route == "openai" {
					result, apiErr := GeminiEmbeddingHandler(c, info, resp)
					require.Nil(t, apiErr)
					usage = result
				} else {
					result, apiErr := NativeGeminiEmbeddingHandler(c, resp, info)
					require.Nil(t, apiErr)
					usage = result
					require.Equal(t, payload, rec.Body.String())
				}
				want := tc.estimate
				if adoptsReportedUsage && tc.reported > 0 {
					want = tc.reported
				}
				require.NotNil(t, usage)
				require.Equal(t, want, usage.PromptTokens)
				require.Equal(t, want, usage.TotalTokens)
				require.Zero(t, usage.CompletionTokens)
				if adoptsReportedUsage && tc.reported > 0 {
					require.NotNil(t, usage.BillingUsage)
					require.False(t, usage.BillingUsage.Estimated)
					require.Equal(t, tc.reported, usage.BillingUsage.GeminiUsageMetadata.PromptTokenCount)
					if tc.name == "estimate_below_report" {
						require.Equal(t, 100, usage.PromptTokensDetails.ImageTokens)
					}
				}
				if route == "openai" {
					var delivered dto.OpenAIEmbeddingResponse
					require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &delivered))
					require.Equal(t, want, delivered.Usage.PromptTokens)
					require.Len(t, delivered.Data, 1)
					require.Equal(t, []float64{0.1, 0.2}, delivered.Data[0].Embedding)
				}
				require.NotEmpty(t, rec.Body.String())
				t.Logf("route=%s estimated_input=%d reported_input=%d selected_input=%d billing_snapshot=%t", route, tc.estimate, tc.reported, usage.PromptTokens, usage.BillingUsage != nil)
			})
		}
	}
}
