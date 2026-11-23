package claude

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
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

type claudeParityReader struct {
	io.Reader
	bytewise bool
}

type claudeParityReadError struct{}

func (claudeParityReadError) Read([]byte) (int, error) { return 0, errors.New("fixture read failure") }

type claudeParityWriteError struct{ gin.ResponseWriter }

func (claudeParityWriteError) Write([]byte) (int, error) {
	return 0, errors.New("fixture write failure")
}
func (claudeParityWriteError) WriteString(string) (int, error) {
	return 0, errors.New("fixture write failure")
}

func (r claudeParityReader) Read(p []byte) (int, error) {
	if r.bytewise && len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

// Shared synthetic upstream inputs for the pinned-main oracle and strict mode.
func claudeParityFixtures() map[string]string {
	start := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"audit\",\"model\":\"claude\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2,\"cache_read_input_tokens\":77864,\"output_tokens\":2}}}\n\n"
	text := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello world\"}}\n\n"
	blockStop := "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	end := "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":32000}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	tool := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_audit\",\"name\":\"write\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"a.py\\\"\"}}\n\n"
	return map[string]string{
		"read_error":                 start + text,
		"write_error":                start + text + blockStop + end,
		"client_cancel":              "",
		"idle_timeout":               "",
		"normal":                     start + text + blockStop + end,
		"open_text":                  start + text + end,
		"open_tool":                  start + tool + end,
		"truncated_tool":             start + tool + blockStop + end,
		"missing_stop":               start + text,
		"invalid_json":               start + text + "data: {invalid}\n\n",
		"upstream_error":             start + text + "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n",
		"missing_usage":              strings.Replace(start, `,"usage":{"input_tokens":2,"cache_read_input_tokens":77864,"output_tokens":2}`, "", 1) + text,
		"zero_usage":                 strings.Replace(start, `"input_tokens":2,"cache_read_input_tokens":77864,"output_tokens":2`, `"input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0`, 1) + text + blockStop + strings.Replace(end, "32000", "0", 1),
		"empty":                      "",
		"ping_only":                  "event: ping\ndata: {\"type\":\"ping\"}\n\n",
		"no_final_newline":           strings.TrimRight(start+text+blockStop+end, "\n"),
		"duplicate_index":            start + text + text + blockStop + end,
		"start_only":                 start,
		"text_before_start":          text + blockStop + end,
		"duplicate_start":            start + start + text + blockStop + end,
		"orphan_stop":                start + blockStop + end,
		"wrong_delta_index":          start + strings.Replace(text, `"index":0,"delta"`, `"index":7,"delta"`, 1) + blockStop + end,
		"negative_index":             start + strings.ReplaceAll(text+blockStop, `"index":0`, `"index":-1`) + end,
		"fractional_index":           start + strings.ReplaceAll(text+blockStop, `"index":0`, `"index":0.5`) + end,
		"stop_without_delta":         start + text + blockStop + "data: {\"type\":\"message_stop\"}\n\n",
		"delta_without_stop":         start + text + blockStop + strings.Split(end, "data: {\"type\":\"message_stop\"}")[0],
		"conflicting_stop_reason":    start + text + blockStop + strings.Replace(end, "max_tokens", "end_turn", 1) + end,
		"null_event":                 start + text + "data: null\n\n" + blockStop + end,
		"array_event":                start + text + "data: []\n\n" + blockStop + end,
		"wrong_type_event":           start + text + "data: {\"type\":123}\n\n" + blockStop + end,
		"null_message":               "data: {\"type\":\"message_start\",\"message\":null}\n\n" + text + blockStop + end,
		"null_delta":                 start + "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":null}\n\n" + end,
		"usage_array":                start + text + blockStop + strings.Replace(end, `{"output_tokens":32000}`, `[]`, 1),
		"usage_string":               start + text + blockStop + strings.Replace(end, `32000`, `"32000"`, 1),
		"usage_overflow":             start + text + blockStop + strings.Replace(end, `32000`, `9223372036854775808`, 1),
		"usage_negative":             start + text + blockStop + strings.Replace(end, `32000`, `-1`, 1),
		"usage_omitted_final":        start + text + blockStop + strings.Replace(end, `,"usage":{"output_tokens":32000}`, "", 1),
		"half_json":                  start + text + "data: {\"type\":\"message_delta\",",
		"html_body":                  "data: <html>upstream unavailable</html>\n\n",
		"unknown_event":              start + "data: {\"type\":\"future_event\",\"extra\":true}\n\n" + text + blockStop + end,
		"event_name_mismatch":        start + text + blockStop + "event: ping\n" + end,
		"comments_and_ids":           ": keepalive\nid: synthetic\nretry: 1000\n\n" + start + text + blockStop + end,
		"multiline_data":             strings.Replace(start, `,"message":`, ",\ndata: \"message\":", 1) + text + blockStop + end,
		"data_without_space":         strings.ReplaceAll(start+text+blockStop+end, "data: ", "data:"),
		"utf8_split":                 start + strings.Replace(text, "hello world", "你好🌐", 1) + blockStop + end,
		"done_foreign_protocol":      start + text + "data: [DONE]\n\n",
		"chat_foreign_protocol":      "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\n",
		"responses_foreign_protocol": "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
		"gemini_foreign_protocol":    "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}\n\n",
		"error_before_start":         "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n",
		"malformed_error":            start + text + "data: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":42}}\n\n",
		"tool_json_array":            start + strings.Replace(tool, `"{\"path\":\"a.py\""`, `"[]"`, 1) + blockStop + end,
		"tool_use_incomplete":        start + tool + blockStop + strings.Replace(end, "max_tokens", "tool_use", 1),
		"thinking_without_signature": start + strings.NewReplacer(`"type":"text"`, `"type":"thinking"`, `"text"`, `"thinking"`, `"text_delta"`, `"thinking_delta"`).Replace(text) + blockStop + end,
		"signature_in_text":          start + text + "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"opaque\"}}\n\n" + blockStop + end,
	}
}

// The golden was recorded by these same fixtures against official main
// bd6bb5301. Only timestamps are normalized; event order and content are hashed.
func TestClaudeMainStreamParity(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	fixtures := claudeParityFixtures()
	golden, err := os.ReadFile("testdata/main-stream-parity.json")
	require.NoError(t, err)
	var expected map[string]any
	require.NoError(t, common.Unmarshal(golden, &expected))
	actual := map[string]any{}
	clockFields := regexp.MustCompile(`"(created|created_at)":\s*\d+`)
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatGemini} {
		for name, body := range fixtures {
			for _, bytewise := range []bool{false, true} {
				key := fmt.Sprintf("%s/%s/bytewise=%t", format, name, bytewise)
				t.Run(key, func(t *testing.T) {
					if bytewise {
						body = strings.ReplaceAll(body, "\n", "\r\n")
					}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
					c.Set(common.RequestIdKey, "audit")
					if name == "write_error" {
						c.Writer = claudeParityWriteError{c.Writer}
					}
					if name == "client_cancel" {
						ctx, cancel := context.WithCancel(c.Request.Context())
						cancel()
						c.Request = c.Request.WithContext(ctx)
					}
					info := &relaycommon.RelayInfo{RelayFormat: format, IsStream: true, DisablePing: true, ShouldIncludeUsage: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude"}}
					info.SetEstimatePromptTokens(50)
					resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(claudeParityReader{Reader: strings.NewReader(body), bytewise: bytewise})}
					if name == "read_error" {
						resp.Body = io.NopCloser(io.MultiReader(resp.Body, claudeParityReadError{}))
					}
					if name == "idle_timeout" || name == "client_cancel" {
						reader, writer := io.Pipe()
						defer writer.Close()
						resp.Body = reader
					}
					// BEGIN fork setup: omitted when recording official main.
					common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{ClaudeStreamValidation: common.GetPointer(false)})
					service.BeginStreamAttempt(c, info)
					info.StreamSession.ObserveTransport(resp, nil)
					info.StreamDiagnostic.Observe(resp)
					info.StreamSession.ObserveHTTP(resp)
					// END fork setup.
					var usage *dto.Usage
					var apiErr *types.NewAPIError
					if format == types.RelayFormatOpenAIResponses {
						usage, apiErr = ClaudeResponsesStreamHandler(c, resp, info)
					} else {
						usage, apiErr = ClaudeStreamHandler(c, resp, info)
					}
					usage = service.FinalizeStreamUsage(c, info, usage) // fork finalizer
					var errorText string
					if apiErr != nil {
						errorText = apiErr.Error()
					}
					downstream := clockFields.ReplaceAllString(rec.Body.String(), `"$1":0`)
					result := map[string]any{"downstream_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(downstream))), "usage": usage, "error": errorText}
					actual[key] = result
					want, err := common.Marshal(expected[key])
					require.NoError(t, err)
					got, err := common.Marshal(result)
					require.NoError(t, err)
					require.JSONEq(t, string(want), string(got), downstream)
				})
			}
		}
	}
	require.Len(t, expected, len(actual), "golden must cover exactly this fixture matrix")
}
