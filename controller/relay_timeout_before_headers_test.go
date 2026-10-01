package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeoutStreamBeforeHeadersBilling(t *testing.T) {
	for _, protocol := range []struct {
		name        string
		channelType int
	}{
		{"openai", constant.ChannelTypeOpenAI},
		{"openai-to-claude", constant.ChannelTypeAnthropic},
		{"claude-native", constant.ChannelTypeAnthropic},
		{"gemini-native", constant.ChannelTypeGemini},
	} {
		for _, mode := range []string{"refund", "input", "charge"} {
			t.Run(protocol.name+"/"+mode, func(t *testing.T) {
				f := newCostBearingFixture(t, costBearingOptions{channelType: protocol.channelType, upstream: hangUpstream()})
				f.setUser(t, map[string]any{"stream_total_timeout": 1, "non_stream_timeout_billing": mode})
				path := "/v1/chat/completions"
				body := `{"model":"` + f.modelName + `","stream":true,"messages":[{"role":"user","content":"Explain why autumn leaves change color."}]}`
				switch protocol.name {
				case "claude-native":
					f.engine.POST("/v1/messages", func(c *gin.Context) { Relay(c, types.RelayFormatClaude) })
					path = "/v1/messages"
					body = `{"model":"` + f.modelName + `","stream":true,"max_tokens":30,"messages":[{"role":"user","content":"Explain why autumn leaves change color."}]}`
				case "gemini-native":
					f.engine.POST("/v1beta/models/*path", func(c *gin.Context) { Relay(c, types.RelayFormatGemini) })
					path = "/v1beta/models/" + f.modelName + ":streamGenerateContent"
					body = `{"contents":[{"role":"user","parts":[{"text":"Explain why autumn leaves change color."}]}]}`
				}
				rec := f.post(t, path, body)
				require.Equal(t, http.StatusGatewayTimeout, rec.Code, rec.Body.String())
				assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
				consumes := f.logs(t, model.LogTypeConsume)
				if mode == "refund" {
					f.spent(t, 0)
					require.Empty(t, consumes)
					errs := f.logs(t, model.LogTypeError)
					require.NotEmpty(t, errs)
					record := timeoutAbsorbedOf(t, errs[len(errs)-1].Other)
					require.NotNil(t, record)
					assert.Equal(t, "stream", record["kind"])
					assert.NotEqual(t, false, record["request_sent"], "successful writes use the default sent state")
					assert.Positive(t, record["absorbed_quota_min"])
					return
				}
				require.Len(t, consumes, 1)
				assert.Positive(t, consumes[0].PromptTokens)
				assert.Zero(t, consumes[0].CompletionTokens)
				assert.Equal(t, int(float64(consumes[0].PromptTokens)*costBearingRatio), consumes[0].Quota)
				f.spent(t, consumes[0].Quota)
			})
		}
	}
}
