package geminichat

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/internal/jsonutil"
	sharedgemini "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/gemini"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

func GeminiGenerateContentRequestToOpenAIChat(geminiRequest *dto.GeminiChatRequest, info convmeta.Meta) (*dto.GeneralOpenAIRequest, error) {
	modelName := ""
	isStream := false
	if info != nil {
		isStream = info.GetIsStream()
	}
	modelName = convmeta.UpstreamModelName(info)
	openaiRequest := &dto.GeneralOpenAIRequest{
		Model:  modelName,
		Stream: kitutil.GetPointer(isStream),
	}

	var messages []dto.Message
	for _, content := range geminiRequest.Contents {
		message := dto.Message{
			Role: convertGeminiRoleToOpenAI(content.Role),
		}

		var mediaContents []dto.MediaContent
		var toolCalls []dto.ToolCallRequest
		for _, part := range content.Parts {
			if part.Text != "" {
				mediaContent := dto.MediaContent{
					Type: "text",
					Text: part.Text,
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.InlineData != nil {
				mediaContent := dto.MediaContent{
					Type: "image_url",
					ImageUrl: &dto.MessageImageUrl{
						Url:      fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data),
						Detail:   "auto",
						MimeType: part.InlineData.MimeType,
					},
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.FileData != nil {
				mediaContent := dto.MediaContent{
					Type: "image_url",
					ImageUrl: &dto.MessageImageUrl{
						Url:      part.FileData.FileUri,
						Detail:   "auto",
						MimeType: part.FileData.MimeType,
					},
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.FunctionCall != nil {
				toolCall := dto.ToolCallRequest{
					ID:   fmt.Sprintf("call_%d", len(toolCalls)+1),
					Type: "function",
					Function: dto.FunctionRequest{
						Name:      part.FunctionCall.FunctionName,
						Arguments: jsonutil.ToJSONString(part.FunctionCall.Arguments),
					},
				}
				toolCalls = append(toolCalls, toolCall)
			} else if part.FunctionResponse != nil {
				toolMessage := dto.Message{
					Role:       "tool",
					ToolCallId: fmt.Sprintf("call_%d", len(toolCalls)),
				}
				toolMessage.SetStringContent(jsonutil.ToJSONString(part.FunctionResponse.Response))
				messages = append(messages, toolMessage)
			}
		}

		if len(toolCalls) > 0 {
			message.SetToolCalls(toolCalls)
		} else if len(mediaContents) == 1 && mediaContents[0].Type == "text" {
			message.Content = mediaContents[0].Text
		} else if len(mediaContents) > 0 {
			message.SetMediaContent(mediaContents)
		}

		if len(message.ParseContent()) > 0 || len(message.ToolCalls) > 0 {
			messages = append(messages, message)
		}
	}

	openaiRequest.Messages = messages

	// Rule 5: a field the client sent is forwarded even when it is zero.
	generationConfig := &geminiRequest.GenerationConfig
	if generationConfig.Temperature != nil {
		openaiRequest.Temperature = generationConfig.Temperature
	}
	if generationConfig.TopP != nil {
		openaiRequest.TopP = kitutil.GetPointer(*generationConfig.TopP)
	}
	if generationConfig.TopK != nil {
		openaiRequest.TopK = kitutil.GetPointer(int(*generationConfig.TopK))
	}
	if generationConfig.MaxOutputTokens != nil {
		openaiRequest.MaxTokens = kitutil.GetPointer(*generationConfig.MaxOutputTokens)
	}
	if len(generationConfig.StopSequences) > 0 {
		openaiRequest.Stop = generationConfig.StopSequences[:min(len(generationConfig.StopSequences), 4)]
	}
	if generationConfig.CandidateCount != nil {
		openaiRequest.N = kitutil.GetPointer(*generationConfig.CandidateCount)
	}
	if generationConfig.Seed != nil {
		openaiRequest.Seed = kitutil.GetPointer(float64(*generationConfig.Seed))
	}
	// presencePenalty / frequencyPenalty are not mapped (as upstream): OpenAI
	// reasoning models and the Claude target reject them.
	openaiRequest.ResponseFormat = responseFormatFromGemini(generationConfig)
	// The pivot also feeds Claude and Responses targets; the effort is only set
	// for target models known to accept reasoning_effort.
	if effort := sharedgemini.OpenAIReasoningEffortFor(modelName, generationConfig.ThinkingConfig); effort != "" {
		openaiRequest.ReasoningEffort = effort
		if info != nil {
			info.SetReasoningEffort(effort)
		}
	}

	if len(geminiRequest.GetTools()) > 0 {
		var tools []dto.ToolCallRequest
		for _, tool := range geminiRequest.GetTools() {
			if tool.FunctionDeclarations == nil {
				continue
			}
			functionDeclarations, err := kitutil.Any2Type[[]dto.FunctionRequest](tool.FunctionDeclarations)
			if err != nil {
				kitutil.LogSystemError(fmt.Sprintf("failed to parse gemini function declarations: %v (type=%T)", err, tool.FunctionDeclarations))
				continue
			}
			for _, function := range functionDeclarations {
				openAITool := dto.ToolCallRequest{
					Type: "function",
					Function: dto.FunctionRequest{
						Name:        function.Name,
						Description: function.Description,
						Parameters:  lowercaseSchemaTypes(function.Parameters, 0),
					},
				}
				tools = append(tools, openAITool)
			}
		}
		if len(tools) > 0 {
			// OpenAI rejects tool_choice without tools.
			openaiRequest.Tools, openaiRequest.ToolChoice = toolChoiceFromGemini(geminiRequest.ToolConfig, tools)
		}
	}

	if geminiRequest.SystemInstructions != nil {
		systemMessage := dto.Message{
			Role:    "system",
			Content: extractTextFromGeminiParts(geminiRequest.SystemInstructions.Parts),
		}
		openaiRequest.Messages = append([]dto.Message{systemMessage}, openaiRequest.Messages...)
	}

	return openaiRequest, nil
}

// responseFormatFromGemini maps responseMimeType application/json to an OpenAI
// response_format. A responseJsonSchema is standard JSON Schema and becomes
// json_schema. A responseSchema uses Gemini's Schema dialect (nullable,
// propertyOrdering, ...) that OpenAI's schema validator may reject, so it
// becomes json_object: the output stays JSON, the shape is left to the prompt.
// Other MIME types (text/plain, text/x.enum) have no OpenAI counterpart.
func responseFormatFromGemini(config *dto.GeminiChatGenerationConfig) *dto.ResponseFormat {
	if !strings.EqualFold(strings.TrimSpace(config.ResponseMimeType), "application/json") {
		return nil
	}
	raw := strings.TrimSpace(string(config.ResponseJsonSchema))
	if raw == "" || raw == "null" {
		return &dto.ResponseFormat{Type: "json_object"}
	}
	jsonSchema, err := kitutil.Marshal(dto.FormatJsonSchema{Name: "response", Schema: config.ResponseJsonSchema})
	if err != nil {
		kitutil.LogSystemError(fmt.Sprintf("failed to marshal gemini response schema: %v", err))
		return &dto.ResponseFormat{Type: "json_object"}
	}
	return &dto.ResponseFormat{Type: "json_schema", JsonSchema: jsonSchema}
}

// toolChoiceFromGemini maps functionCallingConfig.mode to an OpenAI
// tool_choice and returns the tools to send with it. VALIDATED lets the model
// choose, like "auto". ANY with exactly one allowed function names that
// function. ANY with several allowed functions has no tool_choice form, so the
// tools are narrowed to the allowed ones and the choice is "required". If no
// allowed name is declared, all tools are kept with "required".
func toolChoiceFromGemini(toolConfig *dto.ToolConfig, tools []dto.ToolCallRequest) ([]dto.ToolCallRequest, any) {
	if toolConfig == nil || toolConfig.FunctionCallingConfig == nil {
		return tools, nil
	}
	config := toolConfig.FunctionCallingConfig
	switch strings.ToUpper(strings.TrimSpace(string(config.Mode))) {
	case "AUTO", "VALIDATED":
		return tools, "auto"
	case "NONE":
		return tools, "none"
	case "ANY":
		if len(config.AllowedFunctionNames) == 0 {
			return tools, "required"
		}
		allowed := make(map[string]bool, len(config.AllowedFunctionNames))
		for _, name := range config.AllowedFunctionNames {
			allowed[name] = true
		}
		narrowed := make([]dto.ToolCallRequest, 0, len(tools))
		for _, tool := range tools {
			if allowed[tool.Function.Name] {
				narrowed = append(narrowed, tool)
			}
		}
		switch {
		case len(narrowed) == 0:
			// No allowed name is declared: naming one would be rejected.
			return tools, "required"
		case len(allowed) == 1:
			return tools, map[string]any{
				"type":     "function",
				"function": map[string]any{"name": narrowed[0].Function.Name},
			}
		default:
			return narrowed, "required"
		}
	default:
		return tools, nil
	}
}

const maxSchemaDepth = 64

// lowercaseSchemaTypes rewrites Gemini Schema type names (OBJECT, STRING, ...)
// to the lowercase JSON Schema names OpenAI requires. It only follows
// sub-schema keywords, so a property that happens to be called "type" is not
// touched. Input maps are copied, never mutated.
func lowercaseSchemaTypes(schema any, depth int) any {
	if depth > maxSchemaDepth {
		return schema
	}
	node, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	out := make(map[string]any, len(node))
	for key, value := range node {
		switch key {
		case "type":
			switch typed := value.(type) {
			case string:
				value = strings.ToLower(typed)
			case []any:
				types := make([]any, len(typed))
				for i, item := range typed {
					if name, ok := item.(string); ok {
						types[i] = strings.ToLower(name)
					} else {
						types[i] = item
					}
				}
				value = types
			}
		case "properties", "$defs", "definitions", "patternProperties":
			if children, ok := value.(map[string]any); ok {
				mapped := make(map[string]any, len(children))
				for name, child := range children {
					mapped[name] = lowercaseSchemaTypes(child, depth+1)
				}
				value = mapped
			}
		case "items", "additionalProperties", "not":
			value = lowercaseSchemaTypes(value, depth+1)
		case "anyOf", "oneOf", "allOf", "prefixItems":
			if children, ok := value.([]any); ok {
				mapped := make([]any, len(children))
				for i, child := range children {
					mapped[i] = lowercaseSchemaTypes(child, depth+1)
				}
				value = mapped
			}
		}
		out[key] = value
	}
	return out
}

func convertGeminiRoleToOpenAI(geminiRole string) string {
	switch geminiRole {
	case "user":
		return "user"
	case "model":
		return "assistant"
	case "function":
		return "function"
	default:
		return "user"
	}
}

func extractTextFromGeminiParts(parts []dto.GeminiPart) string {
	texts := make([]string, 0)
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}
