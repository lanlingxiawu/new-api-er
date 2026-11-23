package xai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestXAIProtocolDisabledUsesMainUsage(t *testing.T) {
	full := `{"prompt_tokens":100,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":90,"text_tokens":5,"audio_tokens":3,"image_tokens":2,"cached_tokens_details":{"text_tokens":80,"audio_tokens":0,"image_tokens":10},"cache_write_tokens":4,"cache_write_tokens_details":{"ttl_30m_tokens":1,"ttl_12h_tokens":3}},"completion_tokens_details":{"reasoning_tokens":7,"text_tokens":8,"audio_tokens":3,"image_tokens":2}}`
	for _, tc := range []struct{ name, last string }{
		{"full details", full},
		{"omitted details", `{"prompt_tokens":100,"total_tokens":120}`},
		{"explicit zero", `{"prompt_tokens":100,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{StreamProtocolValidation: common.GetPointer(false)})
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok"}}
			service.BeginStreamAttempt(c, info)
			body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}],\"usage\":" + full + "}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":" + tc.last + "}\n\ndata: [DONE]\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			usage, apiErr := xAIStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			require.Equal(t, 100, usage.PromptTokens)
			require.Equal(t, 20, usage.CompletionTokens)
			var expected dto.Usage
			require.NoError(t, common.UnmarshalJsonStr(tc.last, &expected))
			require.Equal(t, expected.PromptTokensDetails, usage.PromptTokensDetails, "main copies each frame's details, including omitted and explicit zero fields")
			require.Equal(t, expected.CompletionTokenDetails, usage.CompletionTokenDetails)
			require.Nil(t, info.StreamResult)
		})
	}
}
