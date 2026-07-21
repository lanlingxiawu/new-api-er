package ollama

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// closeNotifyRecorder adds CloseNotify for gin streaming writes.
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func newCloseNotifyRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{httptest.NewRecorder(), make(chan bool, 1)}
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool { return c.closed }

func newTestContext(rec http.ResponseWriter) *gin.Context {
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c
}

func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}
}

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}

	t.Run("embeddings uses /api/embed", func(t *testing.T) {
		info := newInfo()
		info.ChannelBaseUrl = "http://host"
		info.RelayMode = relayconstant.RelayModeEmbeddings
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "http://host/api/embed", got)
	})

	t.Run("completions path uses /api/generate", func(t *testing.T) {
		info := newInfo()
		info.ChannelBaseUrl = "http://host"
		info.RequestURLPath = "/v1/completions"
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "http://host/api/generate", got)
	})

	t.Run("completions relay mode uses /api/generate", func(t *testing.T) {
		info := newInfo()
		info.ChannelBaseUrl = "http://host"
		info.RelayMode = relayconstant.RelayModeCompletions
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "http://host/api/generate", got)
	})

	t.Run("default uses /api/chat", func(t *testing.T) {
		info := newInfo()
		info.ChannelBaseUrl = "http://host"
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "http://host/api/chat", got)
	})
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	info := newInfo()
	info.ApiKey = "sekret"
	h := http.Header{}
	err := a.SetupRequestHeader(newTestContext(httptest.NewRecorder()), &h, info)
	require.NoError(t, err)
	assert.Equal(t, "Bearer sekret", h.Get("Authorization"))
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())

	t.Run("nil request returns error", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, newInfo(), nil)
		require.Error(t, err)
	})

	t.Run("chat path returns OllamaChatRequest", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "llama3", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
		out, err := a.ConvertOpenAIRequest(c, newInfo(), req)
		require.NoError(t, err)
		_, ok := out.(*OllamaChatRequest)
		assert.True(t, ok)
	})

	t.Run("completions path returns OllamaGenerateRequest", func(t *testing.T) {
		info := newInfo()
		info.RelayMode = relayconstant.RelayModeCompletions
		req := &dto.GeneralOpenAIRequest{Model: "llama3", Prompt: "once upon"}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		g, ok := out.(*OllamaGenerateRequest)
		require.True(t, ok)
		assert.Equal(t, "once upon", g.Prompt)
	})
}

// ---- ConvertClaudeRequest ----

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	req := &dto.ClaudeRequest{
		Model:     "llama3",
		MaxTokens: lo.ToPtr(uint(100)),
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}
	out, err := a.ConvertClaudeRequest(c, newInfo(), req)
	require.NoError(t, err)
	chat, ok := out.(*OllamaChatRequest)
	require.True(t, ok)
	assert.Equal(t, "llama3", chat.Model)
	require.NotEmpty(t, chat.Messages)
}

// ---- ConvertEmbeddingRequest / requestOpenAI2Embeddings ----

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	out, err := a.ConvertEmbeddingRequest(newTestContext(httptest.NewRecorder()), newInfo(), dto.EmbeddingRequest{
		Model: "emb", Input: "hi",
	})
	require.NoError(t, err)
	assert.Equal(t, "emb", out.(*OllamaEmbeddingRequest).Model)
}

func TestRequestOpenAI2Embeddings(t *testing.T) {
	t.Run("single input unwrapped", func(t *testing.T) {
		out := requestOpenAI2Embeddings(dto.EmbeddingRequest{Model: "e", Input: "only"})
		assert.Equal(t, "only", out.Input)
	})

	t.Run("multi input stays array with options", func(t *testing.T) {
		out := requestOpenAI2Embeddings(dto.EmbeddingRequest{
			Model:       "e",
			Input:       []any{"a", "b"},
			Temperature: lo.ToPtr(0.5),
			TopP:        lo.ToPtr(0.9),
			Seed:        lo.ToPtr(1.0),
			Dimensions:  lo.ToPtr(64),
		})
		arr, ok := out.Input.([]string)
		require.True(t, ok)
		assert.Len(t, arr, 2)
		assert.Equal(t, 64, out.Dimensions)
		// temperature is stored as the original *float64 pointer
		assert.EqualValues(t, 0.5, *out.Options["temperature"].(*float64))
		assert.Equal(t, 64, out.Options["dimensions"])
	})
}

// ---- openAIChatToOllamaChat ----

func TestOpenAIChatToOllamaChat(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())

	t.Run("options and stop string mapping", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model:            "m",
			Temperature:      lo.ToPtr(0.7),
			TopP:             lo.ToPtr(0.8),
			TopK:             lo.ToPtr(40),
			FrequencyPenalty: lo.ToPtr(0.1),
			PresencePenalty:  lo.ToPtr(0.2),
			Seed:             lo.ToPtr(9.0),
			MaxTokens:        lo.ToPtr(uint(128)),
			Stop:             "STOP",
			Messages:         []dto.Message{{Role: "user", Content: "hi"}},
		}
		out, err := openAIChatToOllamaChat(c, req)
		require.NoError(t, err)
		assert.Equal(t, 0.8, out.Options["top_p"])
		assert.Equal(t, 40, out.Options["top_k"])
		assert.Equal(t, 9, out.Options["seed"])
		assert.Equal(t, 128, out.Options["num_predict"])
		assert.Equal(t, []string{"STOP"}, out.Options["stop"])
		require.Len(t, out.Messages, 1)
		assert.Equal(t, "hi", out.Messages[0].Content)
	})

	t.Run("stop as []any filters non-strings", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "m", Stop: []any{"a", 1, "b"}, Messages: []dto.Message{{Role: "user", Content: "x"}}}
		out, err := openAIChatToOllamaChat(c, req)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, out.Options["stop"])
	})

	t.Run("response format json", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "m", ResponseFormat: &dto.ResponseFormat{Type: "json"}, Messages: []dto.Message{{Role: "user", Content: "x"}}}
		out, err := openAIChatToOllamaChat(c, req)
		require.NoError(t, err)
		assert.Equal(t, "json", out.Format)
	})

	t.Run("response format json_schema", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model:          "m",
			ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: json.RawMessage(`{"type":"object"}`)},
			Messages:       []dto.Message{{Role: "user", Content: "x"}},
		}
		out, err := openAIChatToOllamaChat(c, req)
		require.NoError(t, err)
		assert.NotNil(t, out.Format)
	})

	t.Run("tools mapped", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model: "m",
			Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "get_weather", Description: "d"}}},
			Messages: []dto.Message{{Role: "user", Content: "x"}},
		}
		out, err := openAIChatToOllamaChat(c, req)
		require.NoError(t, err)
		tools := out.Tools.([]OllamaTool)
		require.Len(t, tools, 1)
		assert.Equal(t, "get_weather", tools[0].Function.Name)
	})

	t.Run("tool role name and assistant tool_calls", func(t *testing.T) {
		name := "get_weather"
		req := &dto.GeneralOpenAIRequest{
			Model: "m",
			Messages: []dto.Message{
				{Role: "assistant", Content: "", ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]`)},
				{Role: "tool", Name: &name, Content: "sunny"},
			},
		}
		out, err := openAIChatToOllamaChat(c, req)
		require.NoError(t, err)
		require.Len(t, out.Messages, 2)
		require.Len(t, out.Messages[0].ToolCalls, 1)
		assert.Equal(t, "get_weather", out.Messages[0].ToolCalls[0].Function.Name)
		assert.Equal(t, "get_weather", out.Messages[1].ToolName)
	})
}

// ---- openAIToGenerate ----

func TestOpenAIToGenerate(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())

	t.Run("prompt string and options", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "m", Prompt: "hello", Temperature: lo.ToPtr(0.3), MaxTokens: lo.ToPtr(uint(10)), Stop: []string{"z"}}
		out, err := openAIToGenerate(c, req)
		require.NoError(t, err)
		assert.Equal(t, "hello", out.Prompt)
		assert.Equal(t, 10, out.Options["num_predict"])
		assert.Equal(t, []string{"z"}, out.Options["stop"])
	})

	t.Run("prompt array concatenated", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "m", Prompt: []any{"a", "b", 1}}
		out, err := openAIToGenerate(c, req)
		require.NoError(t, err)
		assert.Equal(t, "ab", out.Prompt)
	})

	t.Run("suffix and json format", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "m", Prompt: "p", Suffix: "end", ResponseFormat: &dto.ResponseFormat{Type: "json"}}
		out, err := openAIToGenerate(c, req)
		require.NoError(t, err)
		assert.Equal(t, "end", out.Suffix)
		assert.Equal(t, "json", out.Format)
	})
}

// ---- ollamaToolCallsToOpenAI ----

func TestOllamaToolCallsToOpenAI(t *testing.T) {
	t.Run("empty returns nil", func(t *testing.T) {
		out, idx := ollamaToolCallsToOpenAI(nil, 3, true)
		assert.Nil(t, out)
		assert.Equal(t, 3, idx)
	})

	t.Run("nil args become empty object, index applied", func(t *testing.T) {
		tc := OllamaToolCall{}
		tc.Function.Name = "f"
		tc.Function.Arguments = nil
		out, idx := ollamaToolCallsToOpenAI([]OllamaToolCall{tc}, 0, true)
		require.Len(t, out, 1)
		assert.Equal(t, "{}", out[0].Function.Arguments)
		assert.Equal(t, "call_0", out[0].ID)
		assert.Equal(t, 1, idx)
		assert.NotNil(t, out[0].Index)
	})

	t.Run("args marshaled without index", func(t *testing.T) {
		tc := OllamaToolCall{}
		tc.Function.Name = "f"
		tc.Function.Arguments = map[string]any{"city": "Paris"}
		out, _ := ollamaToolCallsToOpenAI([]OllamaToolCall{tc}, 0, false)
		require.Len(t, out, 1)
		assert.Contains(t, out[0].Function.Arguments, "Paris")
		assert.Nil(t, out[0].Index)
	})
}

// ---- toUnix / contentPtr ----

func TestToUnix(t *testing.T) {
	assert.NotZero(t, toUnix(""))
	assert.Equal(t, int64(1748347200), toUnix("2025-05-27T12:00:00Z"))
	assert.NotZero(t, toUnix("not-a-time"))
}

func TestContentPtr(t *testing.T) {
	assert.Nil(t, contentPtr(""))
	require.NotNil(t, contentPtr("x"))
	assert.Equal(t, "x", *contentPtr("x"))
}

// ---- ollamaEmbeddingHandler ----

func TestOllamaEmbeddingHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		body := `{"model":"emb","embeddings":[[0.1,0.2],[0.3,0.4]],"prompt_eval_count":5}`
		usage, err := ollamaEmbeddingHandler(c, newInfo(), newResponse(http.StatusOK, body))
		require.Nil(t, err)
		require.NotNil(t, usage)
		assert.Equal(t, 5, usage.TotalTokens)
		assert.Contains(t, rec.Body.String(), "embedding")
	})

	t.Run("error field returns error", func(t *testing.T) {
		c := newTestContext(httptest.NewRecorder())
		body := `{"error":"model not found"}`
		_, err := ollamaEmbeddingHandler(c, newInfo(), newResponse(http.StatusOK, body))
		require.NotNil(t, err)
	})

	t.Run("malformed returns error", func(t *testing.T) {
		c := newTestContext(httptest.NewRecorder())
		_, err := ollamaEmbeddingHandler(c, newInfo(), newResponse(http.StatusOK, "not-json"))
		require.NotNil(t, err)
	})
}

// ---- ollamaChatHandler ----

func TestOllamaChatHandler(t *testing.T) {
	t.Run("tool calls compact and pretty", func(t *testing.T) {
		for _, raw := range []string{
			`{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Paris"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
			"{\n  \"model\": \"llama3.1\",\n  \"message\": {\n    \"role\": \"assistant\",\n    \"tool_calls\": [{\"function\":{\"name\":\"get_weather\",\"arguments\":{\"city\":\"Paris\"}}}]\n  },\n  \"done\": true,\n  \"prompt_eval_count\": 5,\n  \"eval_count\": 7\n}",
		} {
			rec := httptest.NewRecorder()
			c := newTestContext(rec)
			usage, err := ollamaChatHandler(c, newInfo(), newResponse(http.StatusOK, raw))
			require.Nil(t, err)
			assert.Equal(t, 12, usage.TotalTokens)
			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
			assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)
		}
	})

	t.Run("plain content with reasoning", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		raw := `{"model":"llama3","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"hello world","thinking":"let me think"},"done":true,"prompt_eval_count":3,"eval_count":2}`
		usage, err := ollamaChatHandler(c, newInfo(), newResponse(http.StatusOK, raw))
		require.Nil(t, err)
		assert.Equal(t, 5, usage.TotalTokens)
		body := rec.Body.String()
		assert.Contains(t, body, "hello world")
		assert.Contains(t, body, "let me think")
	})

	t.Run("generate response field", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		raw := `{"model":"llama3","response":"generated text","done":true,"prompt_eval_count":1,"eval_count":1}`
		usage, err := ollamaChatHandler(c, newInfo(), newResponse(http.StatusOK, raw))
		require.Nil(t, err)
		require.NotNil(t, usage)
		assert.Contains(t, rec.Body.String(), "generated text")
	})

	t.Run("single line malformed returns error", func(t *testing.T) {
		c := newTestContext(httptest.NewRecorder())
		_, err := ollamaChatHandler(c, newInfo(), newResponse(http.StatusOK, "not-json"))
		require.NotNil(t, err)
	})
}

// ---- ollamaStreamHandler ----

func TestOllamaStreamHandler(t *testing.T) {
	t.Run("nil response errors", func(t *testing.T) {
		c := newTestContext(newCloseNotifyRecorder())
		_, err := ollamaStreamHandler(c, newInfo(), nil)
		require.NotNil(t, err)
	})

	t.Run("delta then done emits usage and DONE", func(t *testing.T) {
		rec := newCloseNotifyRecorder()
		c := newTestContext(rec)
		stream := strings.Join([]string{
			`{"model":"llama3","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"Hello"},"done":false}`,
			`{"model":"llama3","message":{"role":"assistant","content":" world"},"done":false}`,
			`{"model":"llama3","done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
		}, "\n")
		usage, err := ollamaStreamHandler(c, newInfo(), newResponse(http.StatusOK, stream))
		require.Nil(t, err)
		require.NotNil(t, usage)
		assert.Equal(t, 12, usage.TotalTokens)
		body := rec.Body.String()
		assert.Contains(t, body, "Hello")
		assert.Contains(t, body, "data: [DONE]")
	})

	t.Run("tool calls in stream set finish reason", func(t *testing.T) {
		rec := newCloseNotifyRecorder()
		c := newTestContext(rec)
		stream := strings.Join([]string{
			`{"model":"llama3","message":{"role":"assistant","tool_calls":[{"function":{"name":"f","arguments":{"a":1}}}]},"done":false}`,
			`{"model":"llama3","done":true,"prompt_eval_count":1,"eval_count":1}`,
		}, "\n")
		_, err := ollamaStreamHandler(c, newInfo(), newResponse(http.StatusOK, stream))
		require.Nil(t, err)
		assert.Contains(t, rec.Body.String(), "tool_calls")
	})

	t.Run("malformed line returns error", func(t *testing.T) {
		rec := newCloseNotifyRecorder()
		c := newTestContext(rec)
		_, err := ollamaStreamHandler(c, newInfo(), newResponse(http.StatusOK, "not-json\n"))
		require.NotNil(t, err)
	})
}

// ---- DoResponse routing ----

func TestDoResponse(t *testing.T) {
	a := &Adaptor{}

	t.Run("embeddings", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := newInfo()
		info.RelayMode = relayconstant.RelayModeEmbeddings
		body := `{"embeddings":[[0.1]],"prompt_eval_count":2}`
		_, err := a.DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
	})

	t.Run("non-stream chat", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		body := `{"model":"llama3","message":{"role":"assistant","content":"hi"},"done":true,"eval_count":1,"prompt_eval_count":1}`
		_, err := a.DoResponse(c, newResponse(http.StatusOK, body), newInfo())
		require.Nil(t, err)
	})

	t.Run("stream", func(t *testing.T) {
		rec := newCloseNotifyRecorder()
		c := newTestContext(rec)
		info := newInfo()
		info.IsStream = true
		body := `{"model":"llama3","done":true,"eval_count":1,"prompt_eval_count":1}`
		_, err := a.DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
	})
}

// ---- HTTP management helpers (mocked upstream) ----

func TestFetchOllamaModels(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/tags", r.URL.Path)
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3","size":100}]}`))
		}))
		defer srv.Close()
		models, err := FetchOllamaModels(srv.URL, "key")
		require.NoError(t, err)
		require.Len(t, models, 1)
		assert.Equal(t, "llama3", models[0].Name)
	})

	t.Run("non-200 errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		_, err := FetchOllamaModels(srv.URL, "")
		require.Error(t, err)
	})
}

func TestPullOllamaModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/pull", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	require.NoError(t, PullOllamaModel(srv.URL, "k", "llama3"))
}

func TestPullOllamaModelStream(t *testing.T) {
	t.Run("success status ends stream", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"pulling"}` + "\n" + `{"status":"success"}` + "\n"))
		}))
		defer srv.Close()
		var got []OllamaPullResponse
		err := PullOllamaModelStream(srv.URL, "", "llama3", func(p OllamaPullResponse) { got = append(got, p) })
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(got), 2)
	})

	t.Run("error status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"error"}` + "\n"))
		}))
		defer srv.Close()
		err := PullOllamaModelStream(srv.URL, "", "llama3", nil)
		require.Error(t, err)
	})
}

func TestDeleteOllamaModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/delete", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	require.NoError(t, DeleteOllamaModel(srv.URL, "k", "llama3"))
}

func TestFetchOllamaVersion(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/version", r.URL.Path)
			_, _ = w.Write([]byte(`{"version":"0.5.0"}`))
		}))
		defer srv.Close()
		v, err := FetchOllamaVersion(srv.URL, "k")
		require.NoError(t, err)
		assert.Equal(t, "0.5.0", v)
	})

	t.Run("empty base url errors", func(t *testing.T) {
		_, err := FetchOllamaVersion("", "")
		require.Error(t, err)
	})

	t.Run("empty version field errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		_, err := FetchOllamaVersion(srv.URL, "")
		require.Error(t, err)
	})
}

// ---- getters & unimplemented ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c := newTestContext(httptest.NewRecorder())
	info := newInfo()
	a.Init(info)

	_, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertImageRequest(c, info, dto.ImageRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
	rr, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.NoError(t, err)
	assert.Nil(t, rr)
}
