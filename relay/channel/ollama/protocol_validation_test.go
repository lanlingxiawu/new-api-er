package ollama

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOllamaMainPolicyPreservesNDJSONOutcomes(t *testing.T) {
	for _, ending := range []string{"complete", "missing terminal", "invalid JSON"} {
		t.Run(ending, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			disabled := false
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI,
				UserSetting: dto.UserSetting{StreamProtocolValidation: &disabled},
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama"}}
			service.BeginStreamAttempt(c, info)
			body := "{\"message\":{\"content\":\"hello\"},\"done\":false}\n"
			if ending == "complete" {
				body += "{\"done\":true,\"prompt_eval_count\":5,\"eval_count\":7}\n"
			} else if ending == "invalid JSON" {
				body += "{invalid}\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/x-ndjson"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			usage, apiErr := ollamaStreamHandler(c, info, resp)
			require.Equal(t, ending == "invalid JSON", apiErr != nil)
			require.Equal(t, ending == "invalid JSON", info.StreamStatus.HasErrors())
			require.Same(t, usage, service.FinalizeStreamUsage(c, info, usage))
			require.Nil(t, info.StreamResult)
			require.NotContains(t, rec.Body.String(), "event: error")
			if ending == "complete" {
				require.Equal(t, 12, usage.TotalTokens)
				require.Contains(t, rec.Body.String(), "[DONE]")
			} else {
				require.NotContains(t, rec.Body.String(), "[DONE]")
			}
		})
	}
}
