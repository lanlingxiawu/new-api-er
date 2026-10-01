package relay

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// referenceDecode is what the adapted path used before the reflection-free
// decoder: a plain Unmarshal into the DTO. decodeStreamChunk must agree with it
// on every frame an upstream can plausibly send.
func referenceDecode(data string) (dto.ChatCompletionsStreamResponse, error) {
	var chunk dto.ChatCompletionsStreamResponse
	err := common.UnmarshalJsonStr(data, &chunk)
	return chunk, err
}

// normalizeChunk erases the two differences decodeStreamChunk documents:
// logprobs is never decoded, and an empty choices list may reuse storage.
func normalizeChunk(chunk dto.ChatCompletionsStreamResponse) dto.ChatCompletionsStreamResponse {
	if len(chunk.Choices) == 0 {
		chunk.Choices = nil
	}
	for i := range chunk.Choices {
		chunk.Choices[i].Logprobs = nil
	}
	return chunk
}

var validChunkCorpus = map[string]string{
	"openai content":        `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1790000000,"model":"gpt-4o","service_tier":"default","system_fingerprint":"fp_1","choices":[{"index":0,"delta":{"content":" hi"},"logprobs":null,"finish_reason":null}],"usage":null}`,
	"first frame":           `{"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"finish_reason":null}]}`,
	"finish":                `{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	"usage only":            `{"id":"c","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}`,
	"reasoning_content":     `{"choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
	"reasoning":             `{"choices":[{"index":0,"delta":{"reasoning":"think"}}]}`,
	"tool call first":       `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
	"tool call fragment":    `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
	"tool call no index":    `{"choices":[{"delta":{"tool_calls":[{"id":"x","function":{"name":"f","arguments":"{}"}}]}}]}`,
	"escaped content":       `{"choices":[{"index":0,"delta":{"content":"line\n\"quoted\" \\ 你好 😀 \t"}}]}`,
	"raw unicode":           `{"choices":[{"index":0,"delta":{"content":"你好，世界 😀"}}]}`,
	"content null":          `{"choices":[{"index":0,"delta":{"content":null}}]}`,
	"delta null":            `{"choices":[{"index":0,"delta":null}]}`,
	"choice null":           `{"choices":[null]}`,
	"choices null":          `{"choices":null}`,
	"fingerprint null":      `{"system_fingerprint":null}`,
	"all null scalars":      `{"id":null,"object":null,"created":null,"model":null}`,
	"frame is null":         `null`,
	"empty object":          `{}`,
	"unknown fields":        `{"x_extra":{"deep":[1,2,{"a":"b"}]},"choices":[{"index":0,"extra":true,"delta":{"content":"a","audio":{"id":"x"}}}]}`,
	"duplicate key":         `{"id":"first","id":"second"}`,
	"duplicate null scalar": `{"choices":[{"delta":{"role":"assistant","role":null}}]}`,
	"duplicate choices":     `{"choices":[{"index":0},{"index":1}],"choices":[{"index":5}]}`,
	"two choices":           `{"choices":[{"index":0,"delta":{"content":"a"}},{"index":1,"delta":{"content":"b"}}]}`,
	"negative index":        `{"choices":[{"index":-1,"delta":{"content":"a"}}]}`,
	"whitespace":            " { \"id\" : \"c\" , \"choices\" : [ { \"index\" : 0 } ] } ",
	"created negative zero": `{"created":-0}`,
}

var invalidChunkCorpus = map[string]string{
	"truncated":           `{"id":"c","choices":[{"index":0,"delta":{"content":"hel`,
	"not json":            `hello`,
	"trailing garbage":    `{"id":"c"} x`,
	"raw control char":    "{\"choices\":[{\"delta\":{\"content\":\"a\tb\"}}]}",
	"top-level array":     `[1,2]`,
	"top-level string":    `"x"`,
	"id not string":       `{"id":5}`,
	"choices object":      `{"choices":{"index":0}}`,
	"choice string":       `{"choices":["x"]}`,
	"index float":         `{"choices":[{"index":0.5}]}`,
	"delta array":         `{"choices":[{"delta":[]}]}`,
	"content number":      `{"choices":[{"delta":{"content":123}}]}`,
	"content object":      `{"choices":[{"delta":{"content":{"a":1}}}]}`,
	"role bool":           `{"choices":[{"delta":{"role":true}}]}`,
	"finish number":       `{"choices":[{"finish_reason":1}]}`,
	"fingerprint number":  `{"system_fingerprint":1}`,
	"usage string":        `{"usage":"x"}`,
	"usage field wrong":   `{"usage":{"prompt_tokens":"eleven"}}`,
	"tool_calls object":   `{"choices":[{"delta":{"tool_calls":{"id":"x"}}}]}`,
	"tool call index str": `{"choices":[{"delta":{"tool_calls":[{"index":"0"}]}}]}`,
}

// created is the one field read more leniently than Unmarshal: the non-stream
// DTO accepts any value there, so an upstream sending a float or a string must
// not fail every adapted frame. An unusable value is dropped, never an error.
func TestDecodeStreamChunk_LenientCreated(t *testing.T) {
	cases := map[string]struct {
		data string
		want int64
	}{
		"integer":          {`{"created":1790000000}`, 1790000000},
		"float":            {`{"created":1790000000.75}`, 1790000000},
		"exponent":         {`{"created":1.79e9}`, 1790000000},
		"numeric string":   {`{"created":"1790000000"}`, 1790000000},
		"non-numeric text": {`{"created":"yesterday"}`, 0},
		"overflow":         {`{"created":99999999999999999999}`, 0},
		"bool":             {`{"created":true}`, 0},
		"object":           {`{"created":{"s":1}}`, 0},
		"null":             {`{"created":null}`, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var chunk dto.ChatCompletionsStreamResponse
			require.NoError(t, decodeStreamChunk(tc.data, &chunk))
			assert.Equal(t, tc.want, chunk.Created)
		})
	}
}

// createdNotInteger reports input whose top-level created is anything but an
// integer literal — the documented lenient field, held only to syntax parity.
func createdNotInteger(data string) bool {
	created := gjson.Get(data, "created")
	if !created.Exists() || created.Type == gjson.Null {
		return false
	}
	if created.Type != gjson.Number {
		return true
	}
	_, err := strconv.ParseInt(created.Raw, 10, 64)
	return err != nil
}

func TestDecodeStreamChunk_MatchesUnmarshal(t *testing.T) {
	for name, data := range validChunkCorpus {
		t.Run(name, func(t *testing.T) {
			want, err := referenceDecode(data)
			require.NoError(t, err, "corpus entry must be valid for the reference decoder")

			var got dto.ChatCompletionsStreamResponse
			require.NoError(t, decodeStreamChunk(data, &got))
			require.Equal(t, normalizeChunk(want), normalizeChunk(got))
		})
	}
}

func TestDecodeStreamChunk_RejectsWhatUnmarshalRejects(t *testing.T) {
	for name, data := range invalidChunkCorpus {
		t.Run(name, func(t *testing.T) {
			_, refErr := referenceDecode(data)
			require.Error(t, refErr, "corpus entry must be invalid for the reference decoder")

			var got dto.ChatCompletionsStreamResponse
			require.Error(t, decodeStreamChunk(data, &got),
				"the fast path must never accept a frame the DTO would reject")
		})
	}
}

// The handler reuses one chunk across frames. Nothing from an earlier frame may
// leak into a later one — a stale content pointer or finish_reason would be
// folded in a second time.
func TestDecodeStreamChunk_ReuseDoesNotLeakPreviousFrame(t *testing.T) {
	var chunk dto.ChatCompletionsStreamResponse
	require.NoError(t, decodeStreamChunk(
		`{"id":"a","system_fingerprint":"fp","choices":[{"index":0,"delta":{"role":"assistant","content":"x","tool_calls":[{"index":0,"id":"t"}]},"finish_reason":"stop"},{"index":1}],"usage":{"prompt_tokens":1}}`,
		&chunk))

	for name, next := range validChunkCorpus {
		reused := chunk
		reused.Choices = append(chunk.Choices[:0:0], chunk.Choices...)
		require.NoError(t, decodeStreamChunk(next, &reused), name)
		want, _ := referenceDecode(next)
		require.Equal(t, normalizeChunk(want), normalizeChunk(reused), name)
	}
}

// Pointers handed to the aggregator must stay valid after the next frame is
// decoded into the same chunk: the aggregator keeps SystemFingerprint and Usage.
func TestDecodeStreamChunk_RetainedPointersSurviveReuse(t *testing.T) {
	var chunk dto.ChatCompletionsStreamResponse
	require.NoError(t, decodeStreamChunk(`{"system_fingerprint":"fp_a","usage":{"prompt_tokens":1}}`, &chunk))
	fingerprint, usage := chunk.SystemFingerprint, chunk.Usage

	require.NoError(t, decodeStreamChunk(`{"system_fingerprint":"fp_b","usage":{"prompt_tokens":2}}`, &chunk))
	require.Equal(t, "fp_a", *fingerprint)
	require.Equal(t, 1, usage.PromptTokens)
}

// The whole handler must produce byte-identical output whichever decoder runs,
// across the fixture shapes the adapted path has to handle.
func TestDecodeStreamChunk_AggregatedOutputUnchanged(t *testing.T) {
	streams := []string{
		realisticSSE(0), realisticSSE(1), realisticSSE(50),
		strings.Join([]string{
			`data: {"id":"c","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"f","arguments":""}}]}}]}`,
			`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}}]}`,
			`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
			`data: {"id":"c","choices":[{"index":0,"delta":{"reasoning_content":"why"},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
			`data: [DONE]`,
		}, "\n"),
	}
	for i, body := range streams {
		fast := newChatStreamAggregator("m")
		slow := newChatStreamAggregator("m")
		var reused dto.ChatCompletionsStreamResponse
		for _, line := range strings.Split(body, "\n") {
			data, ok := adaptedStreamPayload(line)
			if !ok || data == "[DONE]" {
				continue
			}
			require.NoError(t, decodeStreamChunk(data, &reused))
			fast.AddChunk(&reused)
			ref, err := referenceDecode(data)
			require.NoError(t, err)
			slow.AddChunk(&ref)
		}
		fastBody, err := common.Marshal(fast.Snapshot("fb", false))
		require.NoError(t, err)
		slowBody, err := common.Marshal(slow.Snapshot("fb", false))
		require.NoError(t, err)
		require.Equal(t, string(slowBody), string(fastBody), fmt.Sprintf("stream %d", i))
		require.Equal(t, slow.AssembledText(), fast.AssembledText())
	}
}

// FuzzDecodeStreamChunkValues generates frames from arbitrary field values
// (encoded by the real marshaller, so every escape and unicode form appears)
// and requires field-for-field agreement with the reference decoder.
func FuzzDecodeStreamChunkValues(f *testing.F) {
	f.Add("chatcmpl-1", "gpt-4o", "hello", "assistant", "stop", "think", "{\"a\":1}", int64(1790000000), 0, true, true)
	f.Add("", "", "", "", "", "", "", int64(0), -1, false, false)
	f.Add(" ", "\x00", "\"\\/\b\f\n\r\t", "\xff\xfe", "<>&", "\xed\xa0\xbd", "😀", int64(-1), 7, true, false)
	f.Fuzz(func(t *testing.T, id, model, content, role, finish, reasoning, args string, created int64, index int, withUsage, withTool bool) {
		delta := map[string]any{"content": content, "role": role, "reasoning_content": reasoning}
		if withTool {
			delta["tool_calls"] = []any{map[string]any{"index": index, "id": id, "type": "function",
				"function": map[string]any{"name": model, "arguments": args}}}
		}
		frame := map[string]any{
			"id": id, "model": model, "created": created, "object": "chat.completion.chunk",
			"choices": []any{map[string]any{"index": index, "delta": delta, "finish_reason": finish}},
		}
		if withUsage {
			frame["usage"] = map[string]any{"prompt_tokens": index, "completion_tokens": 1}
		}
		encoded, err := common.Marshal(frame)
		require.NoError(t, err)
		data := string(encoded)

		want, refErr := referenceDecode(data)
		var got dto.ChatCompletionsStreamResponse
		gotErr := decodeStreamChunk(data, &got)
		require.Equal(t, refErr == nil, gotErr == nil, "accept/reject must agree for %s", data)
		if refErr == nil {
			require.Equal(t, normalizeChunk(want), normalizeChunk(got), data)
		}
	})
}

// foldsKeyCase reports input where encoding/json's case-folded key matching
// could apply — any letter that is not lowercase ASCII, including the two
// non-ASCII runes it folds onto k and s. decodeStreamChunk documents this as
// its one behavioural difference, so such input is only held to syntax parity.
func foldsKeyCase(data string) bool {
	return strings.ToLower(data) != data || strings.ContainsAny(data, "Kſ")
}

// FuzzDecodeStreamChunkRaw feeds arbitrary bytes and requires the same
// accept/reject decision and the same decoded values as the reference, except
// where key case folding could apply. Syntax validity must agree regardless.
func FuzzDecodeStreamChunkRaw(f *testing.F) {
	for _, data := range validChunkCorpus {
		f.Add(data)
	}
	for _, data := range invalidChunkCorpus {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data string) {
		var got dto.ChatCompletionsStreamResponse
		gotErr := decodeStreamChunk(data, &got)

		// RawMessage only checks syntax, so it separates "not JSON" from a
		// value that is valid JSON but does not fit the DTO.
		var raw json.RawMessage
		if common.UnmarshalJsonStr(data, &raw) != nil {
			require.Error(t, gotErr, "invalid JSON must be rejected: %q", data)
			return
		}
		if foldsKeyCase(data) || createdNotInteger(data) {
			return
		}
		want, refErr := referenceDecode(data)
		require.Equal(t, refErr == nil, gotErr == nil,
			"accept/reject must agree for %q (reference: %v, fast: %v)", data, refErr, gotErr)
		if refErr == nil {
			require.Equal(t, normalizeChunk(want), normalizeChunk(got), "%q", data)
		}
	})
}
