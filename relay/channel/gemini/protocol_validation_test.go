package gemini

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiProtocolDisabledUsesMainCumulativeMerge(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/fixture", nil)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{StreamProtocolValidation: common.GetPointer(false)})
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatGemini, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini"}}
	service.BeginStreamAttempt(c, info)
	body := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":20,\"totalTokenCount\":25}}\n\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":0,\"totalTokenCount\":5}}\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamDiagnostic.Observe(resp)
	usage, apiErr := GeminiTextGenerationStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.Equal(t, 20, usage.CompletionTokens)
	require.Nil(t, info.StreamResult)
}
