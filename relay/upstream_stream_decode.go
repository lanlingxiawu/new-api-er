package relay

import (
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/tidwall/gjson"
)

var errMalformedChunk = fmt.Errorf("malformed stream chunk: invalid JSON")

// decodeStreamChunk decodes one chat.completion.chunk payload into chunk,
// reusing chunk's choice storage across calls.
//
// An adapted response decodes one frame per generated token, and a reflective
// Unmarshal into this DTO was the largest share of the adaptation's CPU under
// load. This walks each frame once with gjson and fills the same DTO instead.
// Behaviour is held to that Unmarshal's (TestDecodeStreamChunk_MatchesUnmarshal):
// type mismatches are rejected rather than coerced, and null follows
// encoding/json — it clears pointers and slices and leaves scalars untouched.
// usage and tool_calls are rare and deep, so they still go through
// common.UnmarshalJsonStr and keep every field exactly.
//
// Two deliberate differences, neither reachable from a real upstream: keys
// match case-sensitively (encoding/json folds case), and choices[].logprobs is
// not decoded because the aggregator never reads it. A third is reachable on
// purpose: created is read leniently (see decodeChunkCreated).
//
// Values handed out as pointers (SystemFingerprint, Usage, Content) are fresh
// per call, so the aggregator may keep them across later decodes.
func decodeStreamChunk(data string, chunk *dto.ChatCompletionsStreamResponse) error {
	if !gjson.Valid(data) {
		return errMalformedChunk
	}
	// encoding/json replaces each invalid UTF-8 byte with U+FFFD; gjson keeps
	// the raw bytes, which would change the text local token estimation sees.
	// No real upstream sends this, so the exact reference decode costs nothing.
	if !utf8.ValidString(data) {
		*chunk = dto.ChatCompletionsStreamResponse{}
		return common.UnmarshalJsonStr(data, chunk)
	}
	*chunk = dto.ChatCompletionsStreamResponse{Choices: chunk.Choices[:0]}
	root := gjson.Parse(data)
	if root.Type == gjson.Null {
		return nil
	}
	if !root.IsObject() {
		return chunkTypeError("chunk", root)
	}
	var err error
	root.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "id":
			err = decodeChunkString(value, "id", &chunk.Id)
		case "object":
			err = decodeChunkString(value, "object", &chunk.Object)
		case "model":
			err = decodeChunkString(value, "model", &chunk.Model)
		case "created":
			decodeChunkCreated(value, &chunk.Created)
		case "system_fingerprint":
			chunk.SystemFingerprint, err = decodeChunkStringPtr(value, "system_fingerprint")
		case "choices":
			err = decodeStreamChoices(value, chunk)
		case "usage":
			chunk.Usage = nil
			if value.Type != gjson.Null {
				usage := &dto.Usage{}
				err = common.UnmarshalJsonStr(value.Raw, usage)
				chunk.Usage = usage
			}
		}
		return err == nil
	})
	return err
}

func decodeStreamChoices(value gjson.Result, chunk *dto.ChatCompletionsStreamResponse) error {
	chunk.Choices = chunk.Choices[:0]
	if value.Type == gjson.Null {
		return nil
	}
	if !value.IsArray() {
		return chunkTypeError("choices", value)
	}
	var err error
	value.ForEach(func(_, item gjson.Result) bool {
		// Built fresh and appended whole: decoding in place over a reused
		// element would merge the previous frame's fields into this one.
		var choice dto.ChatCompletionsStreamResponseChoice
		err = decodeStreamChoice(item, &choice)
		chunk.Choices = append(chunk.Choices, choice)
		return err == nil
	})
	return err
}

func decodeStreamChoice(value gjson.Result, choice *dto.ChatCompletionsStreamResponseChoice) error {
	if value.Type == gjson.Null {
		return nil
	}
	if !value.IsObject() {
		return chunkTypeError("choice", value)
	}
	var err error
	value.ForEach(func(key, field gjson.Result) bool {
		switch key.Str {
		case "index":
			var index int64
			if err = decodeChunkInt64(field, "index", &index); err == nil && field.Type != gjson.Null {
				choice.Index = int(index)
			}
		case "finish_reason":
			choice.FinishReason, err = decodeChunkStringPtr(field, "finish_reason")
		case "delta":
			err = decodeStreamDelta(field, &choice.Delta)
		}
		return err == nil
	})
	return err
}

func decodeStreamDelta(value gjson.Result, delta *dto.ChatCompletionsStreamResponseChoiceDelta) error {
	if value.Type == gjson.Null {
		return nil
	}
	if !value.IsObject() {
		return chunkTypeError("delta", value)
	}
	var err error
	value.ForEach(func(key, field gjson.Result) bool {
		switch key.Str {
		case "content":
			delta.Content, err = decodeChunkStringPtr(field, "content")
		case "reasoning_content":
			delta.ReasoningContent, err = decodeChunkStringPtr(field, "reasoning_content")
		case "reasoning":
			delta.Reasoning, err = decodeChunkStringPtr(field, "reasoning")
		case "role":
			err = decodeChunkString(field, "role", &delta.Role)
		case "tool_calls":
			delta.ToolCalls = nil
			if field.Type != gjson.Null {
				err = common.UnmarshalJsonStr(field.Raw, &delta.ToolCalls)
			}
		}
		return err == nil
	})
	return err
}

// decodeChunkString assigns a JSON string; null leaves the target untouched,
// as encoding/json does for a non-pointer string.
func decodeChunkString(value gjson.Result, name string, target *string) error {
	switch value.Type {
	case gjson.Null:
		return nil
	case gjson.String:
		*target = value.Str
		return nil
	}
	return chunkTypeError(name, value)
}

// decodeChunkStringPtr returns a fresh pointer, or nil for null.
func decodeChunkStringPtr(value gjson.Result, name string) (*string, error) {
	switch value.Type {
	case gjson.Null:
		return nil, nil
	case gjson.String:
		s := value.Str
		return &s, nil
	}
	return nil, chunkTypeError(name, value)
}

// decodeChunkInt64 accepts only an integer literal in range, matching
// encoding/json: 1.5, 1e3 and "1" are all errors for an integer field.
func decodeChunkInt64(value gjson.Result, name string, target *int64) error {
	switch value.Type {
	case gjson.Null:
		return nil
	case gjson.Number:
		parsed, err := strconv.ParseInt(value.Raw, 10, 64)
		if err != nil {
			return chunkTypeError(name, value)
		}
		*target = parsed
		return nil
	}
	return chunkTypeError(name, value)
}

// decodeChunkCreated reads created more leniently than the stream DTO's
// Unmarshal would. The non-stream DTO types created as any, so an upstream that
// sends it as a float or a string works over a plain non-stream call; rejecting
// the frame here would make adapting that upstream fail on every frame. created
// only feeds the response timestamp, so an unusable value is dropped and the
// snapshot falls back to the current time. null leaves the target untouched.
func decodeChunkCreated(value gjson.Result, target *int64) {
	switch value.Type {
	case gjson.Number:
		if parsed, err := strconv.ParseInt(value.Raw, 10, 64); err == nil {
			*target = parsed
			return
		}
		if f := value.Float(); f > math.MinInt64 && f < math.MaxInt64 {
			*target = int64(f)
		}
	case gjson.String:
		if parsed, err := strconv.ParseInt(value.Str, 10, 64); err == nil {
			*target = parsed
		}
	}
}

func chunkTypeError(name string, value gjson.Result) error {
	return fmt.Errorf("malformed stream chunk: unexpected %s for %s", value.Type, name)
}
