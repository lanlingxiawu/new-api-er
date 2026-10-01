// Package proberouting classifies bounded, self-contained text requests before
// channel selection. It never performs I/O or relies on upstream usage.
package proberouting

import (
	"unicode"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const MaxBodyBytes = 16 * 1024
const RuleVersion = "short-text-v1"

// Classify returns the complete input's Unicode character count and whether
// the request matches the short, single-turn text policy. Unknown structures
// conservatively remain ordinary traffic.
func Classify(path string, body []byte, limit int) (chars int, matched bool) {
	if limit < 1 || limit > 1024 || len(body) > MaxBodyBytes || !utf8.Valid(body) {
		return 0, false
	}
	switch path {
	case "/v1/chat/completions", "/v1/responses", "/v1/messages":
	default:
		return 0, false
	}
	if !gjson.ValidBytes(body) {
		return 0, false
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return 0, false
	}
	var messages, content, instructions, system gjson.Result
	valid := true
	// Inspect the root once. Repeated Get calls rescan unrelated metadata and
	// turn a bounded 16 KiB request into hundreds of KiB of scanning.
	root.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "messages":
			if messages.Exists() {
				valid = false
				break
			}
			messages = value
		case "input":
			if content.Exists() {
				valid = false
				break
			}
			content = value
		case "instructions":
			if instructions.Exists() {
				valid = false
				break
			}
			instructions = value
		case "system":
			if system.Exists() {
				valid = false
				break
			}
			system = value
		case "tools", "functions", "tool_choice", "function_call":
			valid = inactiveTool(value)
		case "previous_response_id", "conversation", "prompt", "context_management", "cached_content", "cachedContent":
			valid = value.Type == gjson.Null || (value.Type == gjson.String && value.Str == "")
		case "n":
			valid = value.Type == gjson.Number && value.Float() == 1
		case "response_format", "output_format":
			valid = plainTextFormat(value)
		case "text", "output_config":
			valid = value.Type == gjson.Null || (value.IsObject() && plainTextFormat(value.Get("format")))
		case "audio":
			valid = value.Type == gjson.Null
		case "modalities":
			valid = value.IsArray()
			if valid {
				value.ForEach(func(_, modality gjson.Result) bool {
					valid = modality.Type == gjson.String && modality.Str == "text"
					return valid
				})
			}
		}
		return valid
	})
	if !valid {
		return 0, false
	}
	input := textInput{limit: limit}
	switch path {
	case "/v1/responses":
		if messages.Exists() || system.Exists() {
			return 0, false
		}
		if instructions.Exists() && instructions.Type != gjson.Null {
			if instructions.Type != gjson.String || !input.add(instructions.Str, false) {
				return 0, false
			}
		}
		if content.Type == gjson.String {
			if !input.add(content.Str, true) {
				return 0, false
			}
			return input.chars, input.hasUserText
		}
		if !input.messages(content, true) {
			return 0, false
		}
	case "/v1/messages":
		if content.Exists() || instructions.Exists() {
			return 0, false
		}
		if system.Exists() && system.Type != gjson.Null {
			if !input.content(system, false, false) {
				return 0, false
			}
		}
		if !input.messages(messages, false) {
			return 0, false
		}
	default:
		if content.Exists() || instructions.Exists() || system.Exists() {
			return 0, false
		}
		if !input.messages(messages, false) {
			return 0, false
		}
	}
	return input.chars, input.hasUserText
}

func plainTextFormat(value gjson.Result) bool {
	return !value.Exists() || value.Type == gjson.Null || (value.IsObject() && value.Get("type").Str == "text")
}

func inactiveTool(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return true
	}
	if value.IsArray() {
		empty := true
		value.ForEach(func(_, _ gjson.Result) bool {
			empty = false
			return false
		})
		return empty
	}
	if value.Type == gjson.String {
		return value.Str == "none"
	}
	// Claude's explicit disabled tool choice.
	if !value.IsObject() {
		return false
	}
	fields, disabled := 0, true
	value.ForEach(func(key, item gjson.Result) bool {
		fields++
		disabled = fields == 1 && key.Str == "type" && item.Type == gjson.String && item.Str == "none"
		return disabled
	})
	return fields == 1 && disabled
}

type textInput struct {
	limit, chars int
	hasUserText  bool
}

func (input *textInput) add(text string, user bool) bool {
	for _, char := range text {
		input.chars++
		if input.chars > input.limit {
			return false
		}
		if user && !unicode.IsSpace(char) {
			input.hasUserText = true
		}
	}
	return true
}

func (input *textInput) content(value gjson.Result, user, responses bool) bool {
	if value.Type == gjson.String {
		return input.add(value.Str, user)
	}
	if !value.IsArray() {
		return false
	}
	valid := true
	value.ForEach(func(_, block gjson.Result) bool {
		kind := block.Get("type").Str
		if !block.IsObject() || (kind != "text" && !(responses && kind == "input_text")) {
			valid = false
			return false
		}
		text := block.Get("text")
		valid = text.Type == gjson.String && input.add(text.Str, user)
		return valid
	})
	return valid
}

func (input *textInput) messages(value gjson.Result, responses bool) bool {
	if !value.IsArray() {
		return false
	}
	users, valid := 0, true
	value.ForEach(func(_, message gjson.Result) bool {
		if !message.IsObject() {
			valid = false
			return false
		}
		if kind := message.Get("type"); kind.Exists() && kind.Str != "message" {
			valid = false
			return false
		}
		for _, field := range []string{"tool_calls", "function_call", "audio", "refusal"} {
			if item := message.Get(field); item.Exists() && item.Type != gjson.Null {
				valid = false
				return false
			}
		}
		role := message.Get("role").Str
		switch role {
		case "user":
			users++
			if users != 1 {
				valid = false
				return false
			}
		case "system", "developer":
			if users != 0 {
				valid = false
				return false
			}
		default:
			valid = false
			return false
		}
		valid = input.content(message.Get("content"), role == "user", responses)
		return valid
	})
	return valid && users == 1
}
