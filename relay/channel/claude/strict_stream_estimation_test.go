package claude

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// TestStrictStreamEstimatePartitionInvariant 用 t 验证原生 Claude 在旧缓冲边界前后分片不改变估算，确认报告优先。
func TestStrictStreamEstimatePartitionInvariant(t *testing.T) {
	for _, size := range []int{5, 8191, 8192, 8193, 16385} {
		for _, full := range []string{strings.Repeat("a", size), strings.Repeat("字", size) + " abc123🙂\n"} {
			for _, batch := range []int{min(size/5, 257), 8192, len([]rune(full))} {
				for _, mode := range []string{"estimated", "mixed", "confirmed-zero", "confirmed-positive"} {
					t.Run(fmt.Sprintf("size=%d/bytes=%d/batch=%d/%s", size, len(full), batch, mode), func(t *testing.T) {
						start := `{"id":"fixture","type":"message","role":"assistant","model":"claude","content":[]}`
						if mode != "estimated" {
							start = strings.TrimSuffix(start, "}") + `,"usage":{"input_tokens":10,"output_tokens":0}}`
						}
						var body strings.Builder
						body.WriteString("event: message_start\ndata: {\"type\":\"message_start\",\"message\":" + start + "}\n\n")
						body.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
						runes := []rune(full)
						for offset := 0; offset < len(runes); offset += batch {
							payload, err := common.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": string(runes[offset:min(offset+batch, len(runes))])}})
							require.NoError(t, err)
							body.WriteString("event: content_block_delta\ndata: " + string(payload) + "\n\n")
						}
						expected := service.EstimateTokenByModel("claude", full)
						source := mode
						if mode != "estimated" {
							body.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
							report := ""
							if mode == "confirmed-zero" || mode == "confirmed-positive" {
								expected = 0
								if mode == "confirmed-positive" {
									expected = 7
								}
								report = fmt.Sprintf(`,"usage":{"output_tokens":%d}`, expected)
								source = "upstream"
								if mode == "confirmed-zero" {
									expected = service.EstimateTokenByModel("claude", full)
									source = "mixed"
								}
							}
							body.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}" + report + "}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						}
						c, _, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body.String())))
						usage, err := strictClaudeStream(c, resp, info)
						require.Nil(t, err)
						require.Equal(t, source, info.StreamResult.UsageSource)
						require.Equal(t, expected, usage.CompletionTokens)
					})
				}
			}
		}
	}
}
