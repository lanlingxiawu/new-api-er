package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestManagedImageResponseLifecycle 真正安装新会话及响应观察，验证图片 JSON 转 SSE 和 DONE 异常结算；t 为测试上下文。
func TestManagedImageResponseLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType, source string // 原始响应、媒体类型及预期收费来源。
		n                               *uint  // nil 验证图片请求默认单张。
		failed, effective               bool   // 预期异常与成功交付状态。
	}{
		{name: "JSON base64 default count", body: `{"data":[{"b64_json":"aGk="}],"usage":{"input_tokens":3,"output_tokens":4}}`, contentType: "application/json", source: "upstream", effective: true},
		{name: "JSON url", body: `{"data":[{"url":"https://fixture.invalid/image.png"}],"usage":{"input_tokens":3,"output_tokens":4}}`, contentType: "application/json", source: "upstream", effective: true},
		{name: "JSON two images", n: imageRegressionCount(2), body: `{"data":[{"b64_json":"aGk="},{"url":"https://fixture.invalid/image.png"}],"usage":{"input_tokens":3,"output_tokens":4}}`, contentType: "application/json", source: "upstream", effective: true},
		{name: "JSON empty", body: `{"data":[],"usage":{"input_tokens":3,"output_tokens":4}}`, contentType: "application/json", source: "none", failed: true},
		{name: "JSON insufficient images", n: imageRegressionCount(2), body: `{"data":[{"b64_json":"aGk="}],"usage":{"input_tokens":3,"output_tokens":4}}`, contentType: "application/json", source: "none", failed: true},
		{name: "JSON damaged second image", n: imageRegressionCount(2), body: `{"data":[{"b64_json":"aGk="},{}],"usage":{"input_tokens":3,"output_tokens":4}}`, contentType: "application/json", source: "none", failed: true},
		{name: "JSON malformed", body: `{"data":[{"b64_json":"aGk="}`, contentType: "application/json", source: "none", failed: true},
		{name: "SSE only preview then DONE", body: "data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"aGk=\"}\n\ndata: [DONE]\n\n", contentType: "text/event-stream", source: "estimated", failed: true, effective: true},
		{name: "SSE missing second then DONE", n: imageRegressionCount(2), body: "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"aGk=\"}\n\ndata: [DONE]\n\n", contentType: "text/event-stream", source: "upstream", failed: true, effective: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeImagesGenerations, Request: &dto.ImageRequest{N: tc.n}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-image-1"}}
			info.PriceData.UsePrice = true
			info.SetEstimatePromptTokens(5)
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, apiErr := OpenaiImageStreamHandler(c, info, resp)
			if !tc.failed {
				require.Nil(t, apiErr)
			}
			selected := service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, tc.failed, info.StreamResult.Failed)
			require.Equal(t, tc.effective, info.StreamResult.EffectiveContent)
			require.Equal(t, tc.failed, info.StreamResult.DiagnosticAvailable)
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			if tc.failed {
				require.Contains(t, rec.Body.String(), "upstream_stream_error")
				require.NotContains(t, rec.Body.String(), "[DONE]")
			} else {
				require.Contains(t, rec.Body.String(), "image_generation.completed")
				require.Contains(t, rec.Body.String(), "[DONE]")
				require.Equal(t, 3, selected.PromptTokens)
				require.Equal(t, 4, selected.CompletionTokens)
			}
			if tc.source == "none" {
				require.Zero(t, selected.TotalTokens)
			}
			otherLog := model.NewLogOther()
			service.AppendStreamLogInfo(info, otherLog)
			other := otherLog.Snapshot()
			_, available := other["stream_diagnostic_available"]
			require.Equal(t, tc.failed, available)
			if !tc.failed {
				require.Nil(t, other["stream_diagnostic"].(relaycommon.StreamDiagnostic).BodyHead)
			}
		})
	}
}

// imageRegressionCount 创建测试图片数量指针；n 为请求数，不修改共享状态。
func imageRegressionCount(n uint) *uint { return &n }
