package claude

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func auditNativeClaudeHandler(t *testing.T, body string) (*dto.Usage, *relaycommon.RelayInfo, string) {
	t.Helper()
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, IsStream: true, DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude"}}
	info.SetEstimatePromptTokens(50)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	usage, apiErr := ClaudeStreamHandler(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	t.Logf("fixture_sha256=%x input_bytes=%d end_reason=%s end_error=%v downstream_stop=%d downstream_error=%d", sha256.Sum256([]byte(body)), len(body), info.StreamStatus.EndReason, info.StreamStatus.EndError, strings.Count(rec.Body.String(), "event: message_stop"), strings.Count(rec.Body.String(), "event: error"))
	return usage, info, rec.Body.String()
}

func TestAuditClaudeFineGrainedMaxTokens(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprintf("complete_json=%t", complete), func(t *testing.T) {
			fragment := `{"document":"unfinished`
			if complete {
				fragment += `"}`
			}
			fragmentJSON, err := common.Marshal(fragment)
			require.NoError(t, err)
			events := []string{
				`{"type":"message_start","message":{"id":"audit-tool","type":"message","role":"assistant","model":"claude","content":[],"usage":{"input_tokens":100,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool-audit","name":"make_file","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + string(fragmentJSON) + `}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":20}}`,
				`{"type":"message_stop"}`,
			}
			var body strings.Builder
			for _, event := range events {
				var parsed dto.ClaudeResponse
				require.NoError(t, common.UnmarshalJsonStr(event, &parsed))
				fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", parsed.Type, event)
			}
			_, info, downstream := auditNativeClaudeHandler(t, body.String())
			require.NoError(t, info.StreamStatus.EndError)
			require.NotContains(t, downstream, "event: error")
			require.Contains(t, downstream, `"stop_reason":"max_tokens"`)
			require.Contains(t, downstream, "event: message_stop")
		})
	}
}

func TestAuditClaudePreservesOriginalBillingSnapshot(t *testing.T) {
	meta := &convmeta.Values{EstimatePromptTokens: 100, SendResponseCount: 1, ClaudeConvertInfo: &convmeta.ClaudeConvertInfo{LastMessagesType: convmeta.LastMessageTypeNone}}
	firstMetadata := &dto.GeminiUsageMetadata{PromptTokenCount: 100, CachedContentTokenCount: 20, TotalTokenCount: 100, PromptTokensDetails: []dto.GeminiPromptTokensDetails{{Modality: "AUDIO", TokenCount: 60}, {Modality: "TEXT", TokenCount: 40}}}
	finalMetadata := *firstMetadata
	finalMetadata.CandidatesTokenCount = 5
	finalMetadata.ThoughtsTokenCount = 7
	finalMetadata.TotalTokenCount = 112
	content, finish := "hello", "stop"
	first := &dto.ChatCompletionsStreamResponse{Id: "audit-billing", Model: "gemini-audit", Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &content}}}, Usage: relayconvert.UsageFromGeminiMetadata(firstMetadata, 0)}
	events := relayconvert.StreamResponseOpenAI2Claude(first, meta)
	meta.SendResponseCount = 2
	last := &dto.ChatCompletionsStreamResponse{Id: "audit-billing", Model: "gemini-audit", Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finish}}, Usage: relayconvert.UsageFromGeminiMetadata(&finalMetadata, 0)}
	events = append(events, relayconvert.StreamResponseOpenAI2Claude(last, meta)...)
	var body strings.Builder
	for _, event := range events {
		payload, err := common.Marshal(event)
		require.NoError(t, err)
		fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", event.Type, payload)
	}
	require.Contains(t, body.String(), `"billing_usage"`)
	require.Contains(t, body.String(), `"gemini_usage_metadata"`)
	usage, info, downstream := auditNativeClaudeHandler(t, body.String())
	require.NoError(t, info.StreamStatus.EndError)
	require.Contains(t, downstream, "event: message_stop")
	require.Contains(t, downstream, `"gemini_usage_metadata"`, "the original snapshot survives forwarding but must also survive local settlement")
	require.NotNil(t, usage.BillingUsage)
	canonical, ok := usage.BillingUsage.CanonicalUsage()
	require.True(t, ok)
	require.Equal(t, 12, canonical.CompletionTokens)
	require.Equal(t, 20, canonical.PromptTokensDetails.CachedTokens)
	tokens := service.BuildTieredTokenParams(canonical, canonical.UsageSemantic == "anthropic", map[string]bool{"p": true, "c": true, "ai": true, "cr": true})
	require.Equal(t, float64(12), tokens.C)
	require.Equal(t, float64(20), tokens.CR)
	require.Equal(t, float64(100), tokens.Len)
	require.Equal(t, "gemini", canonical.UsageSemantic)
	require.Equal(t, 100, canonical.PromptTokens)
	require.Equal(t, 60, canonical.PromptTokensDetails.AudioTokens)
	require.Equal(t, 7, canonical.CompletionTokenDetails.ReasoningTokens)
	require.Equal(t, float64(20), tokens.P)
	require.Equal(t, float64(60), tokens.AI)
	t.Logf("billing_source=%s semantic=%s prompt=%d cache=%d input_audio=%d completion=%d reasoning=%d", usage.BillingUsage.Source, canonical.UsageSemantic, canonical.PromptTokens, canonical.PromptTokensDetails.CachedTokens, canonical.PromptTokensDetails.AudioTokens, canonical.CompletionTokens, canonical.CompletionTokenDetails.ReasoningTokens)
	t.Logf("settlement_tokens p=%g ai=%g cr=%g c=%g len=%g", tokens.P, tokens.AI, tokens.CR, tokens.C, tokens.Len)
}

func TestStrictFineGrainedTerminalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, fragment, blockKind, terminal, reason string
	}{
		{"max tokens", `{"document":"unfinished`, "tool_use", "max_tokens", "done"},
		{"ordinary end", `{"document":"unfinished`, "tool_use", "end_turn", "upstream_protocol_error"},
		{"tool end", `{"document":"unfinished`, "tool_use", "tool_use", "upstream_protocol_error"},
		{"missing terminal", `{"document":"unfinished`, "tool_use", "", "upstream_incomplete"},
		{"server tool", `{"document":"unfinished`, "server_tool_use", "max_tokens", "upstream_protocol_error"},
		{"array input", `[]`, "tool_use", "max_tokens", "upstream_protocol_error"},
		{"null input", `null`, "tool_use", "max_tokens", "upstream_protocol_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fragment, err := common.Marshal(tc.fragment)
			require.NoError(t, err)
			body := strictStart + `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"` + tc.blockKind + `","id":"tool-audit","name":"make_file","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + string(fragment) + `}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

`
			if tc.terminal != "" {
				body += "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"" + tc.terminal + "\"},\"usage\":{\"output_tokens\":20}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			}
			_, info, downstream := auditNativeClaudeHandler(t, body)
			require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
			require.False(t, info.StreamResult.EffectiveContent, "incomplete/non-object tools are never executable deliveries")
			require.Equal(t, tc.reason != "done", info.StreamResult.Failed)
			if tc.reason == "done" {
				require.NotContains(t, downstream, "event: error")
				require.Equal(t, 1, strings.Count(downstream, "event: message_stop"))
			} else {
				require.Contains(t, downstream, "event: error")
				require.NotContains(t, downstream, "event: message_stop")
			}
		})
	}
}

func TestStrictOriginalBillingSnapshotBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, snapshot, semantic string
		prompt, output, audio    int
	}{
		{"original OpenAI", `{"source":"oai_chat","semantic":"openai","openai_usage":{"prompt_tokens":90,"completion_tokens":7,"prompt_tokens_details":{"audio_tokens":60}}}`, "openai", 90, 7, 60},
		{"original Gemini", `{"source":"gemini_chat","semantic":"gemini","gemini_usage_metadata":{"promptTokenCount":90,"candidatesTokenCount":7,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":60}]}}`, "gemini", 90, 7, 60},
		{"original Anthropic", `{"source":"claude_messages","semantic":"anthropic","claude_usage":{"input_tokens":90,"output_tokens":7}}`, "anthropic", 90, 7, 0},
		{"estimated snapshot", `{"source":"oai_chat","estimated":true,"openai_usage":{"prompt_tokens":90,"completion_tokens":7}}`, "anthropic", 100, 7, 0},
		{"oversized extension array", `{"source":"oai_chat","openai_usage":{"prompt_tokens":90,"completion_tokens":7,"extra":[` + strings.Repeat(`0,`, 128) + `0]}}`, "anthropic", 100, 7, 0},
		{"oversized extension object", `{"source":"oai_chat","openai_usage":{"prompt_tokens":90,"completion_tokens":7,"extra":{` + strings.Repeat(`"x":0,`, 512) + `"end":0}}}`, "anthropic", 100, 7, 0},
		{"multiple provider payloads", `{"source":"oai_chat","openai_usage":{"prompt_tokens":90,"completion_tokens":7},"gemini_usage_metadata":{"promptTokenCount":100}}`, "anthropic", 100, 7, 0},
		{"unknown source", `{"source":"unknown","openai_usage":{"prompt_tokens":90,"completion_tokens":7}}`, "anthropic", 100, 7, 0},
		{"negative nested", `{"source":"gemini_chat","gemini_usage_metadata":{"promptTokenCount":90,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":-60}]}}`, "anthropic", 100, 7, 0},
		{"oversized nested", `{"source":"oai_chat","openai_usage":{"prompt_tokens":90,"prompt_tokens_details":{"cached_tokens":1000000001}}}`, "anthropic", 100, 7, 0},
		{"empty snapshot", `{"source":"oai_chat","openai_usage":{"prompt_tokens":0,"completion_tokens":0}}`, "anthropic", 100, 7, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stop := strings.Replace(strictStop, `"output_tokens":7`, `"output_tokens":7,"billing_usage":`+tc.snapshot, 1)
			usage, info, downstream := auditNativeClaudeHandler(t, strictStart+strictText+stop)
			require.False(t, info.StreamResult.Failed)
			require.Contains(t, downstream, `"billing_usage":`+tc.snapshot, "preserve original wire data, even when unsuitable for settlement")
			canonical, ok := usage.BillingUsage.CanonicalUsage()
			require.True(t, ok)
			require.Equal(t, tc.semantic, canonical.UsageSemantic)
			require.Equal(t, tc.prompt, canonical.PromptTokens)
			require.Equal(t, tc.output, canonical.CompletionTokens)
			require.Equal(t, tc.audio, canonical.PromptTokensDetails.AudioTokens)
		})
	}
}

func TestStrictOriginalBillingSnapshotDoesNotMaskLaterReports(t *testing.T) {
	for _, tc := range []struct {
		name            string
		initial, latest int
		wantOriginal    bool
	}{
		{"initial zero superseded", 0, 7, false},
		{"later explicit zero", 2, 0, false},
		{"unchanged count", 2, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := fmt.Sprintf(`"output_tokens":%d,"billing_usage":{"source":"oai_chat","semantic":"openai","openai_usage":{"prompt_tokens":100,"completion_tokens":%d,"prompt_tokens_details":{"audio_tokens":60}}}`, tc.initial, tc.initial)
			body := strings.Replace(strictStart, `"output_tokens":0`, wire, 1)
			body += fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":%d}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", tc.latest)
			usage, info, _ := auditNativeClaudeHandler(t, body)
			require.False(t, info.StreamResult.Failed)
			canonical, ok := usage.BillingUsage.CanonicalUsage()
			require.True(t, ok)
			require.Equal(t, tc.latest, canonical.CompletionTokens)
			if tc.wantOriginal {
				require.Equal(t, "openai", canonical.UsageSemantic)
				require.Equal(t, 60, canonical.PromptTokensDetails.AudioTokens)
			} else {
				require.Equal(t, "anthropic", canonical.UsageSemantic)
				require.Zero(t, canonical.PromptTokensDetails.AudioTokens)
			}
		})
	}
}

func TestStrictUndeliveredUsageRefunded(t *testing.T) {
	for _, tc := range []struct {
		name, rawUsage       string
		perCall              bool
		status               int
		input, output, cache int
	}{
		{"screenshot cache counts", `{"input_tokens":2,"cache_creation_input_tokens":5012,"cache_read_input_tokens":344706,"cache_creation":{"ephemeral_5m_input_tokens":5012,"ephemeral_1h_input_tokens":0},"output_tokens":3}`, false, 200, 2, 3, 344706},
		{"explicit zeros", `{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0}`, false, 200, 0, 0, 0},
		{"only output", `{"output_tokens":2}`, false, 200, 0, 2, 0},
		{"only cache", `{"cache_read_input_tokens":20}`, false, 200, 0, 0, 20},
		{"no token evidence", `{}`, false, 200, 0, 0, 0},
		{"only tool counts", `{"server_tool_use":{"web_search_requests":1}}`, false, 200, 0, 0, 0},
		{"estimated snapshot excluded", `{"output_tokens":0,"billing_usage":{"source":"oai_chat","estimated":true,"openai_usage":{"prompt_tokens":900,"completion_tokens":100}}}`, false, 200, 0, 0, 0},
		{"original snapshot cannot replace partial flat evidence", `{"output_tokens":2,"billing_usage":{"source":"oai_chat","openai_usage":{"prompt_tokens":900,"completion_tokens":100}}}`, false, 200, 0, 2, 0},
		{"per call", `{"input_tokens":100,"output_tokens":2}`, true, 200, 100, 2, 0},
		{"HTTP refusal", `{"input_tokens":100,"output_tokens":2}`, false, 500, 100, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(strictStart, `{"input_tokens":100,"output_tokens":0}`, tc.rawUsage, 1)
			body += "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n"
			c, rec, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
			resp.StatusCode = tc.status
			info.PriceData.UsePrice = tc.perCall
			usage, apiErr := strictClaudeStream(c, resp, info)
			require.Nil(t, apiErr)
			require.True(t, info.StreamResult.Failed)
			require.False(t, info.StreamResult.EffectiveContent)
			require.False(t, info.StreamResult.Diagnostic.UsageFinal)
			require.Equal(t, "upstream_incomplete", string(info.StreamStatus.EndReason))
			require.Equal(t, "none", info.StreamResult.UsageSource)
			require.Zero(t, usage.PromptTokens)
			require.Equal(t, tc.input, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
			require.Zero(t, usage.CompletionTokens)
			require.Equal(t, tc.output, info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
			require.Zero(t, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, tc.cache, info.StreamResult.Diagnostic.UsageEvidence["cache_read_input_tokens"])
			require.Empty(t, info.StreamResult.Diagnostic.EstimatedUsage)
			if usage.BillingUsage != nil {
				require.False(t, usage.BillingUsage.Estimated)
			}
			if tc.name == "screenshot cache counts" {
				require.Equal(t, 5012, info.StreamResult.Diagnostic.UsageEvidence["cache_creation_input_tokens"])
				require.Equal(t, 5012, info.StreamResult.Diagnostic.UsageEvidence["cache_creation.ephemeral_5m_input_tokens"])
				require.Zero(t, usage.ClaudeCacheCreation1hTokens)
			}
			require.Contains(t, rec.Body.String(), "event: error")
			require.NotContains(t, rec.Body.String(), "event: message_stop")
		})
	}
}

func TestStrictInvalidLaterSnapshotKeepsUnchangedConfirmedSnapshot(t *testing.T) {
	wire := `"output_tokens":2,"billing_usage":{"source":"oai_chat","openai_usage":{"prompt_tokens":100,"completion_tokens":2,"prompt_tokens_details":{"audio_tokens":60}}}`
	body := strings.Replace(strictStart, `"output_tokens":0`, wire, 1)
	body += "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2,\"billing_usage\":{\"source\":\"oai_chat\",\"estimated\":true,\"openai_usage\":{\"prompt_tokens\":900,\"completion_tokens\":200}}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	usage, info, _ := auditNativeClaudeHandler(t, body)
	require.False(t, info.StreamResult.Failed)
	canonical, ok := usage.BillingUsage.CanonicalUsage()
	require.True(t, ok)
	require.Equal(t, "openai", canonical.UsageSemantic)
	require.Equal(t, 100, canonical.PromptTokens)
	require.Equal(t, 2, canonical.CompletionTokens)
	require.Equal(t, 60, canonical.PromptTokensDetails.AudioTokens)
	require.False(t, usage.BillingUsage.Estimated)
}
