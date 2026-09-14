package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamImageEvidencePricing 验证完成张数仅在按张计价时独立构成可结算证据；t 使用内存写入，不访问实际资金。
func TestStreamImageEvidencePricing(t *testing.T) {
	for _, usePrice := range []bool{false, true} {
		for _, clientGone := range []bool{false, true} {
			for _, delivered := range []bool{false, true} {
				for _, usageJSON := range []string{"", `,"usage":{"input_tokens":0,"output_tokens":0}`, `,"usage":{"input_tokens":10,"output_tokens":3}`} {
					t.Run(fmt.Sprintf("price=%v/client=%v/delivered=%v/usage=%s", usePrice, clientGone, delivered, usageJSON), func(t *testing.T) {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(ctx)
						n := uint(2)
						info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeImagesGenerations, OriginModelName: "gpt-image-1", Request: &dto.ImageRequest{N: &n}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-image-1"}}
						info.PriceData.UsePrice = usePrice
						info.PriceData.ModelRatio = 1
						info.PriceData.ModelPrice = 0.1
						info.PriceData.GroupRatioInfo.GroupRatio = 1
						info.SetEstimatePromptTokens(20)
						BeginStreamAttempt(c, info)
						info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
						frame := `{"type":"image_generation.completed","b64_json":"aGk="` + usageJSON + `}`
						require.NoError(t, info.StreamSession.ObserveEvent("", []byte(frame)))
						if delivered {
							c.Header("Content-Type", "text/event-stream")
							_, err := c.Writer.Write([]byte("data: " + frame + "\n\n"))
							require.NoError(t, err)
							c.Writer.Flush()
						}
						if clientGone {
							cancel()
						} else {
							info.StreamSession.EndRead(io.EOF)
						}
						selected := FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 999, TotalTokens: 999})
						confirmed := usePrice || usageJSON != ""
						want := "none"
						if confirmed && (clientGone || delivered) {
							want = "upstream"
						} else if delivered && !clientGone {
							want = "estimated"
						}
						require.Equal(t, confirmed, info.StreamResult.ConfirmedUsage)
						require.Equal(t, want, info.StreamResult.UsageSource)
						require.Equal(t, 1, info.StreamResult.Diagnostic.UsageEvidence["image_count"], "原始完成证据始终保留")
						if want == "none" {
							require.Zero(t, selected.TotalTokens)
						} else if want == "estimated" {
							require.Equal(t, 20, selected.PromptTokens)
							require.Zero(t, selected.CompletionTokens, "不按 base64 字节估造图像 token")
							require.Greater(t, calculateTextQuotaSummary(c, info, selected).Quota, 0)
						} else if !usePrice && usageJSON == `,"usage":{"input_tokens":0,"output_tokens":0}` {
							require.Zero(t, selected.TotalTokens, "显式零不替换为估算")
						} else if usePrice {
							require.Equal(t, float64(1), info.PriceData.OtherRatios()["n"])
						}
					})
				}
			}
		}
	}
}
