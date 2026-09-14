package openai

import (
	"errors"
	"fmt"
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

// audioReadFailure 在成功返回一段媒体后注入读取错误，不使用网络或真实编解码器。
type audioReadFailure struct{ io.Reader }

// Read 先交付输入缓冲区 p 对应的媒体字节，再把正常 EOF 替换为读取异常。
func (r audioReadFailure) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, errors.New("audio truncated")
	}
	return n, err
}

// TestUnifiedRawAudioStream 验证流式裸音频直接交付而非误按 SSE 丢弃，异常不把错误文本混入音频。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedRawAudioStream(t *testing.T) {
	for _, contentType := range []string{
		"audio/mpeg", "application/octet-stream", "application/octet-stream; charset=binary",
		"Application/Octet-Stream; charset=binary", "Audio/MPEG; name=audio.mp3",
		`application/octet-stream; name="audio.json.eventstream"`,
	} {
		for _, failed := range []bool{false, true} {
			t.Run(contentType+"/failed="+fmt.Sprint(failed), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
				info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "tts-1"}}
				info.SetEstimatePromptTokens(5)
				service.BeginStreamAttempt(c, info)
				var reader io.Reader = strings.NewReader("audio-bytes")
				if failed {
					reader = audioReadFailure{reader}
				}
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(reader)}
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				u := OpenaiTTSHandler(c, resp, info)
				service.FinalizeStreamUsage(c, info, u)
				require.Equal(t, "audio-bytes", rec.Body.String())
				require.True(t, info.StreamResult.EffectiveContent)
				require.Equal(t, failed, info.StreamResult.Failed)
				require.Equal(t, failed, info.StreamResult.DiagnosticAvailable)
				require.Equal(t, contentType, rec.Header().Get("Content-Type"))
			})
		}
	}
}

// audioDeliveryReader 生成定长媒体；再次读取之前必须已交付前一块，以无等待的断言检测预读到 EOF。
type audioDeliveryReader struct {
	recorder  *httptest.ResponseRecorder // 实际下游内容，仅检查写入进度。
	remaining int                        // 尚未生成的总字节数，支持超过文本帧上限的夹具。
	produced  int                        // 已交给上游观察器的字节数。
}

// Read 向 p 写入最多 32 KiB 媒体，发现前一块未交付则返回测试错误，而非阻塞等待。
func (r *audioDeliveryReader) Read(p []byte) (int, error) {
	if r.recorder.Body.Len() != r.produced || (r.produced > 0 && !r.recorder.Flushed) {
		return 0, errors.New("fixture read ahead before downstream delivery")
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), 32<<10, r.remaining)
	for i := range p[:n] {
		p[i] = 0x81
	}
	r.produced += n
	r.remaining -= n
	return n, nil
}

// TestUnifiedRawAudioImmediateDelivery 验证带 MIME 参数的媒体首块不等 EOF，且大于 8 MiB 仍逐块完成；t 为测试上下文。
func TestUnifiedRawAudioImmediateDelivery(t *testing.T) {
	for _, size := range []int{1, 32 << 10, relaycommon.MaxStreamFrameBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "tts-1"}}
			service.BeginStreamAttempt(c, info)
			reader := &audioDeliveryReader{recorder: rec, remaining: size}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream; charset=binary"}}, Body: io.NopCloser(reader)}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamSession.ObserveHTTP(resp)
			usage := OpenaiTTSHandler(c, resp, info)
			service.FinalizeStreamUsage(c, info, usage)
			require.False(t, info.StreamResult.Failed)
			require.Equal(t, size, rec.Body.Len())
			require.EqualValues(t, size, info.StreamSession.Snapshot().MediaBytes)
		})
	}
}
