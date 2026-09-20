package common_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// BenchmarkStreamEventDelivery 比较单帧观察/写出/刷新/估算在关闭、新流程及原始采集开启时的同步成本。
// 参数 b 为基准上下文；复用请求局部对象及已预热估算器，不含网络、数据库或日志持久化。
func BenchmarkStreamEventDelivery(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	data := []byte(`{"choices":[{"index":0,"delta":{"content":"A short streaming response with useful content."}}]}`)
	frame := append(append([]byte("data: "), data...), []byte("\n\n")...)
	_ = service.EstimateTokenByModel("gpt-4o", "warmup")
	for _, mode := range []string{"disabled", "managed", "managed_capture"} {
		b.Run(mode, func(b *testing.B) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			s := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
			var capture *relaycommon.StreamResponseCapture
			if mode == "managed_capture" {
				capture = relaycommon.NewStreamResponseCapture(1)
				c.Writer = relaycommon.NewDownstreamCaptureWriter(c.Writer, capture)
			}
			if mode != "disabled" {
				var estimator service.StreamTokenEstimator
				c.Writer = relaycommon.NewStreamWriter(c.Writer, s, func(text string, media int) int { return estimator.Add("gpt-4o", text) + estimator.AddMedia(media) })
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(frame)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if capture != nil {
					capture.WriteStreamPayload(frame)
				}
				if mode != "disabled" {
					if err := s.ObserveEvent("", data); err != nil {
						require.NoError(b, err)
					}
				}
				if _, err := c.Writer.Write(frame); err != nil {
					require.NoError(b, err)
				}
				c.Writer.Flush()
				rec.Body.Reset()
			}
		})
	}
}

// BenchmarkStreamObservedHTTPRead 测量独立扫描器的原始响应观察开销；每次处理固定 100 个内容帧及正常结束。
// 参数 b 为基准上下文；纯内存数据便于比较修复前后分帧实现，不代表真实网络端到端延迟。
func BenchmarkStreamObservedHTTPRead(b *testing.B) {
	body := strings.Repeat("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n", 100) + "data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
		s.ObserveHTTP(resp)
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			require.NoError(b, err)
		}
		_ = resp.Body.Close()
	}
}
