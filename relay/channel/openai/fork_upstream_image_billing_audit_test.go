package openai

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// Synthetic local streams exercise quantity selection and pure expression
// evaluation only; no database, wallet, provider or real image is accessed.
func TestForkUpstreamImageBillingAudit(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, expression := range []bool{false, true} {
		for _, abort := range []bool{false, true} {
			t.Run(fmt.Sprintf("expression=%t/abort=%t", expression, abort), func(t *testing.T) {
				body := "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"first\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}\n\n"
				if !abort {
					body += "data: [DONE]\n\n"
				}
				c, recorder, resp, info := newImageTestContext(t, body, "text/event-stream", true)
				if abort {
					c, recorder, resp, info = newDisconnectingImageStream(t, body, "first")
				}
				n := uint(3)
				requested := 3
				info.Request = &dto.ImageRequest{N: &n}
				info.ImageRequestCount = requested
				info.RelayMode = relayconstant.RelayModeImagesGenerations
				info.RelayFormat = types.RelayFormatOpenAI
				info.DisablePing = true
				info.OriginModelName = "audit-image"
				info.UpstreamModelName = "audit-image"
				info.PriceData.GroupRatioInfo.GroupRatio = 1
				if expression {
					info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{
						BillingMode: "tiered_expr", ExprString: `tier("image", fixed(0.1)) * image_count`,
						ExprHash:            billingexpr.ExprHashString(`tier("image", fixed(0.1)) * image_count`),
						EstimatedImageCount: &requested, GroupRatio: 1, QuotaPerUnit: 500000,
					}
				} else {
					info.PriceData.UsePrice = true
					info.PriceData.ModelPrice = 0.1
					info.PriceData.AddOtherRatio("n", 3)
				}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				usage, apiErr := OpenaiImageStreamHandler(c, info, resp)
				require.Nil(t, apiErr)
				countBefore := info.RequestedImageCount()
				if info.BillingImageCount != nil {
					countBefore = *info.BillingImageCount
				} else if value, ok := info.PriceData.OtherRatios()["n"]; ok {
					countBefore = int(value)
				}
				usage = service.FinalizeStreamUsage(c, info, usage)
				count := info.RequestedImageCount()
				if info.BillingImageCount != nil {
					count = *info.BillingImageCount
				} else if value, ok := info.PriceData.OtherRatios()["n"]; ok {
					count = int(value)
				}
				wantCount := 1
				if abort {
					wantCount = 3
				}
				require.Equal(t, wantCount, count)
				require.Equal(t, wantCount, countBefore, "only a normal terminal stream may lower the requested quantity")
				require.Equal(t, abort, info.StreamResult.Failed)
				if abort {
					require.True(t, info.StreamResult.ClientGone)
					require.Contains(t, []string{"client_gone", "handler_stop"}, string(info.StreamStatus.EndReason))
				} else {
					require.False(t, info.StreamResult.ClientGone)
					require.Equal(t, "done", string(info.StreamStatus.EndReason))
					require.Empty(t, info.StreamResult.Diagnostic.Error)
					require.NotContains(t, recorder.Body.String(), "event: error")
				}
				require.Equal(t, !abort, strings.Contains(recorder.Body.String(), "[DONE]"))
				quota := count * 50000
				if expression {
					ok, actual, result := service.TryTieredSettle(info, billingexpr.TokenParams{})
					require.True(t, ok)
					require.NotNil(t, result)
					require.Equal(t, quota, actual)
					require.NotNil(t, result.ImageCount)
					require.Equal(t, count, *result.ImageCount)
					quota = actual
				}
				t.Logf("requested=%d completed=1 count_before_finalize=%d final_count=%d expression=%t abort=%t quota_at_0.1_USD_per_image=%d end=%s output_tokens=%d error_frame=%t done_frame=%t",
					requested, countBefore, count, expression, abort, quota, info.StreamStatus.EndReason, usage.CompletionTokens,
					strings.Contains(recorder.Body.String(), "event: error"), strings.Contains(recorder.Body.String(), "[DONE]"))
				require.Contains(t, recorder.Body.String(), "first")
			})
		}
	}
}
