package helper

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newJSONContext builds a gin.Context whose request carries a JSON body at the
// given path, so Path2RelayMode resolves the right relay mode.
func newJSONContext(t *testing.T, path, body string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// ---------------------------------------------------------------------------
// GetAndValidateRequest — format dispatch
// ---------------------------------------------------------------------------

func TestGetAndValidateRequest_Dispatch(t *testing.T) {
	tests := []struct {
		name    string
		format  types.RelayFormat
		path    string
		body    string
		wantErr bool
	}{
		{"openai", types.RelayFormatOpenAI, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`, false},
		{"gemini generate", types.RelayFormatGemini, "/v1beta/models/gemini:generateContent", `{"contents":[{"parts":[{"text":"hi"}]}]}`, false},
		{"gemini embed", types.RelayFormatGemini, "/v1beta/models/gemini:embedContent", `{"content":{"parts":[{"text":"hi"}]}}`, false},
		{"gemini batch embed", types.RelayFormatGemini, "/v1beta/models/gemini:batchEmbedContents", `{"requests":[]}`, false},
		{"claude", types.RelayFormatClaude, "/v1/messages", `{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`, false},
		{"responses", types.RelayFormatOpenAIResponses, "/v1/responses", `{"model":"gpt-4o","input":"hi"}`, false},
		{"responses compaction", types.RelayFormatOpenAIResponsesCompaction, "/v1/responses/compact", `{"model":"gpt-4o"}`, false},
		{"image", types.RelayFormatOpenAIImage, "/v1/images/generations", `{"model":"gpt-image-1","prompt":"a cat"}`, false},
		{"embedding", types.RelayFormatEmbedding, "/v1/embeddings", `{"model":"text-embedding-3-small","input":"hi"}`, false},
		{"rerank", types.RelayFormatRerank, "/v1/rerank", `{"query":"q","documents":["d"]}`, false},
		{"audio", types.RelayFormatOpenAIAudio, "/v1/audio/speech", `{"model":"tts-1","input":"hi"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newJSONContext(t, tt.path, tt.body)
			req, err := GetAndValidateRequest(c, tt.format)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, req)
		})
	}
}

func TestGetAndValidateRequest_Realtime(t *testing.T) {
	c := newJSONContext(t, "/v1/realtime", ``)
	req, err := GetAndValidateRequest(c, types.RelayFormatOpenAIRealtime)
	require.NoError(t, err)
	require.IsType(t, &dto.BaseRequest{}, req)
}

func TestGetAndValidateRequest_UnsupportedFormat(t *testing.T) {
	c := newJSONContext(t, "/x", ``)
	_, err := GetAndValidateRequest(c, types.RelayFormat("no-such-format"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported relay format")
}

// ---------------------------------------------------------------------------
// GetAndValidateTextRequest
// ---------------------------------------------------------------------------

func TestGetAndValidateTextRequest(t *testing.T) {
	t.Run("chat completion valid", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
		req, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", req.Model)
	})

	t.Run("missing model rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "model is required")
	})

	t.Run("chat completion without messages/prefix/suffix rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o"}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "messages is required")
	})

	t.Run("FIM prefix allows empty messages", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"deepseek","prefix":"def "}`)
		req, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.NoError(t, err)
		require.NotNil(t, req.Prefix)
	})

	t.Run("completions requires prompt", func(t *testing.T) {
		c := newJSONContext(t, "/v1/completions", `{"model":"gpt-3.5-turbo-instruct","prompt":""}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "prompt is required")
	})

	t.Run("moderations defaults model", func(t *testing.T) {
		c := newJSONContext(t, "/v1/moderations", `{"input":"hello"}`)
		req, err := GetAndValidateTextRequest(c, relayconstant.RelayModeModerations)
		require.NoError(t, err)
		require.Equal(t, "text-moderation-latest", req.Model)
	})

	t.Run("moderations requires input", func(t *testing.T) {
		c := newJSONContext(t, "/v1/moderations", `{"model":"text-moderation-latest"}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeModerations)
		require.Error(t, err)
		require.Contains(t, err.Error(), "input is required")
	})

	t.Run("edits requires instruction", func(t *testing.T) {
		c := newJSONContext(t, "/v1/edits", `{"model":"text-davinci-edit-001","messages":[{"role":"user","content":"x"}]}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeEdits)
		require.Error(t, err)
		require.Contains(t, err.Error(), "instruction is required")
	})

	t.Run("web search options valid size", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"web_search_options":{"search_context_size":"high"}}`)
		req, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.NoError(t, err)
		require.Equal(t, "high", req.WebSearchOptions.SearchContextSize)
	})

	t.Run("web search options invalid size rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"web_search_options":{"search_context_size":"gigantic"}}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid search_context_size")
	})

	t.Run("web search options empty size defaults to medium", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"web_search_options":{}}`)
		req, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.NoError(t, err)
		require.Equal(t, "medium", req.WebSearchOptions.SearchContextSize)
	})

	t.Run("invalid json rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{not json`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// max_tokens bounds (billing invariant, Rule 5)
// ---------------------------------------------------------------------------

func TestMaxTokensBounds(t *testing.T) {
	const hugeN = "18446744073686646784"

	t.Run("openai max_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":`+hugeN+`}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_tokens is invalid")
	})
	t.Run("openai max_completion_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":`+hugeN+`}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_tokens is invalid")
	})
	t.Run("claude max_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/messages", `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":`+hugeN+`}`)
		_, err := GetAndValidateClaudeRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_tokens is invalid")
	})
	t.Run("claude normal max_tokens accepted", func(t *testing.T) {
		c := newJSONContext(t, "/v1/messages", `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":8192}`)
		req, err := GetAndValidateClaudeRequest(c)
		require.NoError(t, err)
		require.EqualValues(t, 8192, *req.MaxTokens)
	})
	t.Run("gemini maxOutputTokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1beta/models/gemini:generateContent", `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":`+hugeN+`}}`)
		_, err := GetAndValidateGeminiRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "maxOutputTokens is invalid")
	})
	t.Run("responses max_output_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/responses", `{"model":"gpt-4o","input":"hi","max_output_tokens":`+hugeN+`}`)
		_, err := GetAndValidateResponsesRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_output_tokens is invalid")
	})
	t.Run("boundary at limit accepted", func(t *testing.T) {
		limit := fmt.Sprintf("%d", maxTokensLimit)
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":`+limit+`}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.NoError(t, err)
	})
	t.Run("boundary just above limit rejected", func(t *testing.T) {
		over := fmt.Sprintf("%d", maxTokensLimit+1)
		c := newJSONContext(t, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":`+over+`}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// Claude / Gemini / Responses / Embedding / Rerank / Audio validators
// ---------------------------------------------------------------------------

func TestGetAndValidateClaudeRequest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := newJSONContext(t, "/v1/messages", `{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`)
		req, err := GetAndValidateClaudeRequest(c)
		require.NoError(t, err)
		require.Equal(t, "claude-3", req.Model)
	})
	t.Run("missing messages rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/messages", `{"model":"claude-3"}`)
		_, err := GetAndValidateClaudeRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "messages is required")
	})
	t.Run("missing model rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/messages", `{"messages":[{"role":"user","content":"hi"}]}`)
		_, err := GetAndValidateClaudeRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "model is required")
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/messages", `not json`)
		_, err := GetAndValidateClaudeRequest(c)
		require.Error(t, err)
	})
}

func TestGetAndValidateGeminiRequest(t *testing.T) {
	t.Run("valid contents", func(t *testing.T) {
		c := newJSONContext(t, "/v1beta/models/gemini:generateContent", `{"contents":[{"parts":[{"text":"hi"}]}]}`)
		req, err := GetAndValidateGeminiRequest(c)
		require.NoError(t, err)
		require.Len(t, req.Contents, 1)
	})
	t.Run("empty contents and requests rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1beta/models/gemini:generateContent", `{}`)
		_, err := GetAndValidateGeminiRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "contents is required")
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1beta/models/gemini:generateContent", `bad`)
		_, err := GetAndValidateGeminiRequest(c)
		require.Error(t, err)
	})
}

func TestGetAndValidateGeminiEmbeddingRequests(t *testing.T) {
	c := newJSONContext(t, "/v1beta/models/gemini:embedContent", `{"content":{"parts":[{"text":"hi"}]}}`)
	_, err := GetAndValidateGeminiEmbeddingRequest(c)
	require.NoError(t, err)

	c = newJSONContext(t, "/v1beta/models/gemini:embedContent", `bad`)
	_, err = GetAndValidateGeminiEmbeddingRequest(c)
	require.Error(t, err)

	c = newJSONContext(t, "/v1beta/models/gemini:batchEmbedContents", `{"requests":[]}`)
	_, err = GetAndValidateGeminiBatchEmbeddingRequest(c)
	require.NoError(t, err)

	c = newJSONContext(t, "/v1beta/models/gemini:batchEmbedContents", `bad`)
	_, err = GetAndValidateGeminiBatchEmbeddingRequest(c)
	require.Error(t, err)
}

func TestGetAndValidateResponsesRequest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := newJSONContext(t, "/v1/responses", `{"model":"gpt-4o","input":"hi"}`)
		req, err := GetAndValidateResponsesRequest(c)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", req.Model)
	})
	t.Run("missing model rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/responses", `{"input":"hi"}`)
		_, err := GetAndValidateResponsesRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "model is required")
	})
	t.Run("missing input rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/responses", `{"model":"gpt-4o"}`)
		_, err := GetAndValidateResponsesRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "input is required")
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/responses", `bad`)
		_, err := GetAndValidateResponsesRequest(c)
		require.Error(t, err)
	})
}

func TestGetAndValidateResponsesCompactionRequest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := newJSONContext(t, "/v1/responses/compact", `{"model":"gpt-4o"}`)
		req, err := GetAndValidateResponsesCompactionRequest(c)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", req.Model)
	})
	t.Run("missing model rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/responses/compact", `{}`)
		_, err := GetAndValidateResponsesCompactionRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "model is required")
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/responses/compact", `bad`)
		_, err := GetAndValidateResponsesCompactionRequest(c)
		require.Error(t, err)
	})
}

func TestGetAndValidateEmbeddingRequest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := newJSONContext(t, "/v1/embeddings", `{"model":"text-embedding-3-small","input":"hi"}`)
		req, err := GetAndValidateEmbeddingRequest(c, relayconstant.RelayModeEmbeddings)
		require.NoError(t, err)
		require.Equal(t, "text-embedding-3-small", req.Model)
	})
	t.Run("nil input rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/embeddings", `{"model":"text-embedding-3-small"}`)
		_, err := GetAndValidateEmbeddingRequest(c, relayconstant.RelayModeEmbeddings)
		require.Error(t, err)
		require.Contains(t, err.Error(), "input is empty")
	})
	t.Run("moderations defaults model", func(t *testing.T) {
		c := newJSONContext(t, "/v1/moderations", `{"input":"hi"}`)
		req, err := GetAndValidateEmbeddingRequest(c, relayconstant.RelayModeModerations)
		require.NoError(t, err)
		require.Equal(t, "omni-moderation-latest", req.Model)
	})
	t.Run("embeddings defaults model from path param", func(t *testing.T) {
		c := newJSONContext(t, "/v1/embeddings", `{"input":"hi"}`)
		c.Params = gin.Params{{Key: "model", Value: "path-embed-model"}}
		req, err := GetAndValidateEmbeddingRequest(c, relayconstant.RelayModeEmbeddings)
		require.NoError(t, err)
		require.Equal(t, "path-embed-model", req.Model)
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/embeddings", `bad`)
		_, err := GetAndValidateEmbeddingRequest(c, relayconstant.RelayModeEmbeddings)
		require.Error(t, err)
	})
}

func TestGetAndValidateRerankRequest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := newJSONContext(t, "/v1/rerank", `{"query":"q","documents":["d1","d2"]}`)
		req, err := GetAndValidateRerankRequest(c)
		require.NoError(t, err)
		require.Equal(t, "q", req.Query)
	})
	t.Run("empty query rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/rerank", `{"documents":["d1"]}`)
		_, err := GetAndValidateRerankRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "query is empty")
	})
	t.Run("empty documents rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/rerank", `{"query":"q","documents":[]}`)
		_, err := GetAndValidateRerankRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "documents is empty")
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/rerank", `bad`)
		_, err := GetAndValidateRerankRequest(c)
		require.Error(t, err)
	})
}

func TestGetAndValidAudioRequest(t *testing.T) {
	t.Run("speech valid", func(t *testing.T) {
		c := newJSONContext(t, "/v1/audio/speech", `{"model":"tts-1","input":"hi"}`)
		req, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioSpeech)
		require.NoError(t, err)
		require.Equal(t, "tts-1", req.Model)
	})
	t.Run("speech missing model rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/audio/speech", `{"input":"hi"}`)
		_, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioSpeech)
		require.Error(t, err)
		require.Contains(t, err.Error(), "model is required")
	})
	t.Run("transcription defaults response format", func(t *testing.T) {
		c := newJSONContext(t, "/v1/audio/transcriptions", `{"model":"whisper-1"}`)
		req, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioTranscription)
		require.NoError(t, err)
		require.Equal(t, "json", req.ResponseFormat)
	})
	t.Run("transcription missing model rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/audio/transcriptions", `{}`)
		_, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioTranscription)
		require.Error(t, err)
	})
	t.Run("invalid json rejected", func(t *testing.T) {
		c := newJSONContext(t, "/v1/audio/speech", `bad`)
		_, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioSpeech)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// GetAndValidOpenAIImageRequest
// ---------------------------------------------------------------------------

func TestGetAndValidOpenAIImageRequest_JSON(t *testing.T) {
	boundErr := fmt.Sprintf("n must be an integer between 1 and %d", dto.MaxImageN)

	tests := []struct {
		name    string
		body    string
		wantErr string
		wantN   uint
		wantSz  string
		wantQ   string
	}{
		{"overflowed n rejected", `{"model":"gpt-image-1","prompt":"c","n":18446744073686646784}`, boundErr, 0, "", ""},
		{"n above max rejected", fmt.Sprintf(`{"model":"gpt-image-1","prompt":"c","n":%d}`, dto.MaxImageN+1), boundErr, 0, "", ""},
		{"n at max accepted", fmt.Sprintf(`{"model":"gpt-image-1","prompt":"c","n":%d}`, dto.MaxImageN), "", dto.MaxImageN, "", "auto"},
		{"explicit n", `{"model":"gpt-image-1","prompt":"c","n":3}`, "", 3, "", "auto"},
		{"zero n defaults 1", `{"model":"gpt-image-1","prompt":"c","n":0}`, "", 1, "", "auto"},
		{"absent n defaults 1", `{"model":"gpt-image-1","prompt":"c"}`, "", 1, "", "auto"},
		{"missing model rejected", `{"prompt":"c"}`, "model is required", 0, "", ""},
		{"multiplication sign rejected", `{"model":"gpt-image-1","prompt":"c","size":"1024×1024"}`, "use 'x' instead", 0, "", ""},
		{"dall-e-2 invalid size rejected", `{"model":"dall-e-2","prompt":"c","size":"999x999"}`, "must be one of 256x256", 0, "", ""},
		{"dall-e-2 empty size defaults", `{"model":"dall-e-2","prompt":"c"}`, "", 1, "1024x1024", ""},
		{"dall-e-3 invalid size rejected", `{"model":"dall-e-3","prompt":"c","size":"256x256"}`, "must be one of 1024x1024", 0, "", ""},
		{"dall-e-3 defaults size and quality", `{"model":"dall-e-3","prompt":"c"}`, "", 1, "1024x1024", "standard"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newJSONContext(t, "/v1/images/generations", tt.body)
			req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, req.N)
			require.Equal(t, tt.wantN, *req.N)
			if tt.wantSz != "" {
				require.Equal(t, tt.wantSz, req.Size)
			}
			if tt.wantQ != "" {
				require.Equal(t, tt.wantQ, req.Quality)
			}
		})
	}
}

func TestGetAndValidOpenAIImageRequest_MultipartEdit(t *testing.T) {
	newMultipart := func(t *testing.T, fields map[string]string, withImage bool) (*gin.Context, string) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		for k, v := range fields {
			require.NoError(t, w.WriteField(k, v))
		}
		if withImage {
			part, err := w.CreateFormFile("image", "in.png")
			require.NoError(t, err)
			_, _ = part.Write([]byte("fake"))
		}
		require.NoError(t, w.Close())
		orig := body.String()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", w.FormDataContentType())
		return c, orig
	}

	t.Run("valid stream keeps body replayable and parses fields", func(t *testing.T) {
		c, orig := newMultipart(t, map[string]string{
			"model": "gpt-image-1", "prompt": "edit", "stream": "true", "n": "2", "watermark": "true",
		}, true)
		req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.NoError(t, err)
		require.NotNil(t, req.Stream)
		require.True(t, *req.Stream)
		require.EqualValues(t, 2, *req.N)
		require.NotNil(t, req.Watermark)
		require.True(t, *req.Watermark)

		require.NotEmpty(t, orig)
		form, err := common.ParseMultipartFormReusable(c)
		require.NoError(t, err)
		require.Len(t, form.File["image"], 1)
	})

	t.Run("invalid stream rejected", func(t *testing.T) {
		c, _ := newMultipart(t, map[string]string{"model": "gpt-image-1", "prompt": "e", "stream": "notabool"}, false)
		_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid stream value")
	})

	t.Run("negative n rejected", func(t *testing.T) {
		c, _ := newMultipart(t, map[string]string{"model": "gpt-image-1", "prompt": "e", "n": "-5"}, false)
		_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.Error(t, err)
		require.Contains(t, err.Error(), "n must be an integer")
	})

	t.Run("gpt-image-1 defaults quality standard and n=1", func(t *testing.T) {
		c, _ := newMultipart(t, map[string]string{"model": "gpt-image-1", "prompt": "e"}, false)
		req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.NoError(t, err)
		require.Equal(t, "standard", req.Quality)
		require.EqualValues(t, 1, *req.N)
	})

	t.Run("non-multipart edits falls through to json branch", func(t *testing.T) {
		c := newJSONContext(t, "/v1/images/edits", `{"model":"gpt-image-1","prompt":"e"}`)
		req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.NoError(t, err)
		require.EqualValues(t, 1, *req.N)
	})
}

// image edit form parsing preserves url.Values access
func TestGetAndValidOpenAIImageRequest_MultipartPostForm(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("model", "gpt-image-1"))
	require.NoError(t, w.WriteField("prompt", "p"))
	require.NoError(t, w.WriteField("stream", "true"))
	require.NoError(t, w.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())

	_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
	require.NoError(t, err)
	require.Equal(t, "true", url.Values(c.Request.PostForm).Get("stream"))
}
