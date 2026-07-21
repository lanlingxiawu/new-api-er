package minimax

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func newContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, rec
}

func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func uintPtr(v uint) *uint { return &v }

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	const base = "https://api.minimax.chat"
	tests := []struct {
		name        string
		relayFormat types.RelayFormat
		relayMode   int
		want        string
		wantErr     bool
	}{
		{name: "chat completions", relayMode: relayconstant.RelayModeChatCompletions, want: base + "/v1/text/chatcompletion_v2"},
		{name: "image generation", relayMode: relayconstant.RelayModeImagesGenerations, want: base + "/v1/image_generation"},
		{name: "audio speech", relayMode: relayconstant.RelayModeAudioSpeech, want: base + "/v1/t2a_v2"},
		{name: "claude format", relayFormat: types.RelayFormatClaude, relayMode: relayconstant.RelayModeChatCompletions, want: base + "/anthropic/v1/messages"},
		{name: "unsupported mode", relayMode: 99999, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayFormat: tt.relayFormat,
				RelayMode:   tt.relayMode,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base},
			}
			got, err := GetRequestURL(info)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetRequestURL_EmptyBaseUsesDefault(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ""},
	}
	got, err := GetRequestURL(info)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, "/v1/text/chatcompletion_v2"))
	assert.True(t, strings.HasPrefix(got, "http"))
}

// ---- image request conversion helpers ----

func TestNormalizeMiniMaxResponseFormat(t *testing.T) {
	assert.Equal(t, "url", normalizeMiniMaxResponseFormat(""))
	assert.Equal(t, "url", normalizeMiniMaxResponseFormat("url"))
	assert.Equal(t, "base64", normalizeMiniMaxResponseFormat("b64_json"))
	assert.Equal(t, "base64", normalizeMiniMaxResponseFormat("BASE64"))
	assert.Equal(t, "webp", normalizeMiniMaxResponseFormat("webp"))
}

func TestParseImageSizeAndRatio(t *testing.T) {
	w, h, ok := parseImageSize("1024x512")
	require.True(t, ok)
	assert.Equal(t, 1024, w)
	assert.Equal(t, 512, h)

	_, _, ok = parseImageSize("bad")
	assert.False(t, ok)
	_, _, ok = parseImageSize("axb")
	assert.False(t, ok)
	_, _, ok = parseImageSize("0x0")
	assert.False(t, ok)

	assert.Equal(t, "2:1", reduceAspectRatio(1024, 512))
	assert.Equal(t, 1, gcd(0, 0))
	assert.Equal(t, 4, gcd(8, 12))
}

func TestAspectRatioFromImageRequest(t *testing.T) {
	t.Run("known size maps to ratio", func(t *testing.T) {
		assert.Equal(t, "1:1", aspectRatioFromImageRequest(dto.ImageRequest{Size: "1024x1024"}))
		assert.Equal(t, "16:9", aspectRatioFromImageRequest(dto.ImageRequest{Size: "1792x1024"}))
		assert.Equal(t, "3:2", aspectRatioFromImageRequest(dto.ImageRequest{Size: "1536x1024"}))
	})

	t.Run("extra overrides size", func(t *testing.T) {
		req := dto.ImageRequest{
			Size:  "1024x1024",
			Extra: map[string]json.RawMessage{"aspect_ratio": json.RawMessage(`"21:9"`)},
		}
		assert.Equal(t, "21:9", aspectRatioFromImageRequest(req))
	})

	t.Run("derived ratio when supported", func(t *testing.T) {
		assert.Equal(t, "16:9", aspectRatioFromImageRequest(dto.ImageRequest{Size: "1920x1080"}))
	})

	t.Run("unsupported derived ratio yields empty", func(t *testing.T) {
		assert.Equal(t, "", aspectRatioFromImageRequest(dto.ImageRequest{Size: "1000x999"}))
	})
}

func TestOaiImage2MiniMaxImageRequest(t *testing.T) {
	t.Run("defaults model and n", func(t *testing.T) {
		out := oaiImage2MiniMaxImageRequest(dto.ImageRequest{Prompt: "a cat"})
		assert.Equal(t, "image-01", out.Model)
		assert.Equal(t, "a cat", out.Prompt)
		assert.Equal(t, 1, out.N)
		assert.Equal(t, "url", out.ResponseFormat)
	})

	t.Run("honors N, watermark, aspect ratio and prompt_optimizer", func(t *testing.T) {
		req := dto.ImageRequest{
			Model:          "image-01",
			Prompt:         "fox",
			N:              uintPtr(3),
			Size:           "1024x1024",
			ResponseFormat: "b64_json",
			Watermark:      lo.ToPtr(true),
			Extra:          map[string]json.RawMessage{"prompt_optimizer": json.RawMessage(`true`)},
		}
		out := oaiImage2MiniMaxImageRequest(req)
		assert.Equal(t, 3, out.N)
		assert.Equal(t, "1:1", out.AspectRatio)
		assert.Equal(t, "base64", out.ResponseFormat)
		require.NotNil(t, out.PromptOptimizer)
		assert.True(t, *out.PromptOptimizer)
		require.NotNil(t, out.AigcWatermark)
		assert.True(t, *out.AigcWatermark)
	})
}

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newContext()

	t.Run("unsupported mode errors", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{})
		require.Error(t, err)
	})

	t.Run("valid produces minimax request with aspect ratio", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations, OriginModelName: "image-01", ChannelMeta: &relaycommon.ChannelMeta{}}
		out, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "image-01", Prompt: "a red fox", Size: "1536x1024", N: uintPtr(2)})
		require.NoError(t, err)
		req := out.(MiniMaxImageRequest)
		assert.Equal(t, "3:2", req.AspectRatio)
		assert.Equal(t, 2, req.N)
	})
}

// ---- responseMiniMax2OpenAIImage + miniMaxImageHandler ----

func TestResponseMiniMax2OpenAIImage(t *testing.T) {
	info := &relaycommon.RelayInfo{StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &MiniMaxImageResponse{}
	resp.Data.ImageURLs = []string{"https://x/1.png"}
	resp.Data.ImageBase64 = []string{"QUJD"}
	resp.Metadata = map[string]any{"k": "v"}

	out, err := responseMiniMax2OpenAIImage(resp, info)
	require.NoError(t, err)
	require.Len(t, out.Data, 2)
	assert.Equal(t, "https://x/1.png", out.Data[0].Url)
	assert.Equal(t, "QUJD", out.Data[1].B64Json)
	assert.Contains(t, string(out.Metadata), "\"k\"")
}

func TestMiniMaxImageHandler(t *testing.T) {
	a := &Adaptor{}

	t.Run("success writes OpenAI image response", func(t *testing.T) {
		c, rec := newContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations, StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"id":"i1","data":{"image_urls":["https://x/a.png"]},"base_resp":{"status_code":0,"status_msg":"success"}}`
		usage, err := a.DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		out := rec.Body.String()
		assert.Contains(t, out, "https://x/a.png")
		assert.NotContains(t, out, "image_urls")
	})

	t.Run("base_resp error returns error", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"base_resp":{"status_code":1004,"status_msg":"auth failed"}}`
		usage, err := miniMaxImageHandler(c, newResponse(http.StatusBadRequest, body), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
		assert.Contains(t, err.ToOpenAIError().Message, "auth failed")
	})

	t.Run("malformed body returns error", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := miniMaxImageHandler(c, newResponse(http.StatusOK, "not-json"), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- ConvertAudioRequest + handleTTSResponse ----

func TestConvertAudioRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("unsupported mode errors", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
		require.Error(t, err)
	})

	t.Run("valid builds minimax tts request", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech, OriginModelName: "speech-01-hd", ChannelMeta: &relaycommon.ChannelMeta{}}
		req := dto.AudioRequest{Input: "hello", Voice: "male-01", Speed: lo.ToPtr(1.1), ResponseFormat: "mp3"}
		reader, err := a.ConvertAudioRequest(c, info, req)
		require.NoError(t, err)
		body, _ := io.ReadAll(reader)
		var payload MiniMaxTTSRequest
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Equal(t, "speech-01-hd", payload.Model)
		assert.Equal(t, "hello", payload.Text)
		assert.Equal(t, "male-01", payload.VoiceSetting.VoiceID)
		// non-hex output format is normalized to url for the context flag.
		assert.Equal(t, "url", c.GetString("response_format"))
	})

	t.Run("hex output format preserved in context", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech, OriginModelName: "speech-01-hd", ChannelMeta: &relaycommon.ChannelMeta{}}
		req := dto.AudioRequest{Input: "hi", Voice: "v", ResponseFormat: "hex"}
		_, err := a.ConvertAudioRequest(c, info, req)
		require.NoError(t, err)
		assert.Equal(t, "hex", c.GetString("response_format"))
	})

	t.Run("metadata merges", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{}}
		req := dto.AudioRequest{Input: "hi", Voice: "v", Metadata: json.RawMessage(`{"language_boost":"English"}`)}
		reader, err := a.ConvertAudioRequest(c, info, req)
		require.NoError(t, err)
		body, _ := io.ReadAll(reader)
		var payload MiniMaxTTSRequest
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Equal(t, "English", payload.LanguageBoost)
	})
}

func TestHandleTTSResponse(t *testing.T) {
	t.Run("hex audio decoded and written", func(t *testing.T) {
		c, rec := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		info.SetEstimatePromptTokens(5)
		audio := hex.EncodeToString([]byte("SND"))
		body := `{"data":{"audio":"` + audio + `","status":2},"extra_info":{"usage_characters":9},"base_resp":{"status_code":0,"status_msg":"success"}}`
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		assert.Equal(t, "SND", rec.Body.String())
		assert.Equal(t, 9, usage.(*dto.Usage).TotalTokens)
	})

	t.Run("http audio triggers redirect", func(t *testing.T) {
		c, rec := newContext()
		// http.Redirect only flushes a body (and thus the status) for GET.
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"data":{"audio":"https://cdn/x.mp3"},"base_resp":{"status_code":0}}`
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		assert.Equal(t, http.StatusFound, rec.Code)
		assert.Equal(t, "https://cdn/x.mp3", rec.Header().Get("Location"))
	})

	t.Run("base_resp error returns error", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"base_resp":{"status_code":1002,"status_msg":"rate limited"}}`
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "rate limited")
	})

	t.Run("missing audio returns error", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"data":{"audio":""},"base_resp":{"status_code":0}}`
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
	})

	t.Run("invalid hex audio returns error", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"data":{"audio":"zzz"},"base_resp":{"status_code":0}}`
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
	})

	t.Run("malformed body returns error", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, "not-json"), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- getContentTypeByFormat ----

func TestGetContentTypeByFormat(t *testing.T) {
	assert.Equal(t, "audio/mpeg", getContentTypeByFormat("mp3"))
	assert.Equal(t, "audio/wav", getContentTypeByFormat("wav"))
	assert.Equal(t, "audio/mpeg", getContentTypeByFormat("unknown"))
}

// ---- other converters + getters ----

func TestConvertersAndGetters(t *testing.T) {
	a := &Adaptor{}
	c, _ := newContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	t.Run("ConvertOpenAIRequest nil errors", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, info, nil)
		require.Error(t, err)
	})

	t.Run("ConvertOpenAIRequest passthrough", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "MiniMax-M2"}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		assert.Equal(t, req, out)
	})

	t.Run("ConvertEmbeddingRequest passthrough", func(t *testing.T) {
		emb := dto.EmbeddingRequest{Model: "embo"}
		out, err := a.ConvertEmbeddingRequest(c, info, emb)
		require.NoError(t, err)
		assert.Equal(t, emb, out)
	})

	t.Run("SetupRequestHeader sets bearer", func(t *testing.T) {
		info2 := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k"}}
		info2.ApiKey = "k"
		h := http.Header{}
		require.NoError(t, a.SetupRequestHeader(c, &h, info2))
		assert.Equal(t, "Bearer k", h.Get("Authorization"))
	})

	t.Run("unimplemented converters error", func(t *testing.T) {
		_, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
		assert.Error(t, err)
		_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
		assert.Error(t, err)
	})

	t.Run("ConvertRerankRequest nil", func(t *testing.T) {
		out, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
		require.NoError(t, err)
		assert.Nil(t, out)
	})

	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())
	a.Init(info)
}
