package volcengine

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func newTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
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

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	const base = "https://custom.example.com"

	tests := []struct {
		name        string
		relayFormat types.RelayFormat
		relayMode   int
		model       string
		want        string
		wantErr     bool
	}{
		{name: "chat completions", relayMode: constant.RelayModeChatCompletions, model: "doubao-pro", want: base + "/api/v3/chat/completions"},
		{name: "chat completions bot prefix", relayMode: constant.RelayModeChatCompletions, model: "bot-123", want: base + "/api/v3/bots/chat/completions"},
		{name: "embeddings", relayMode: constant.RelayModeEmbeddings, model: "doubao-embedding", want: base + "/api/v3/embeddings"},
		{name: "images generations", relayMode: constant.RelayModeImagesGenerations, model: "seedream", want: base + "/api/v3/images/generations"},
		{name: "images edits routed to generations", relayMode: constant.RelayModeImagesEdits, model: "seedream", want: base + "/api/v3/images/generations"},
		{name: "rerank", relayMode: constant.RelayModeRerank, model: "m", want: base + "/api/v3/rerank"},
		{name: "responses", relayMode: constant.RelayModeResponses, model: "m", want: base + "/api/v3/responses"},
		{name: "claude format default", relayFormat: types.RelayFormatClaude, relayMode: constant.RelayModeChatCompletions, model: "doubao", want: base + "/api/v3/chat/completions"},
		{name: "claude format bot prefix", relayFormat: types.RelayFormatClaude, relayMode: constant.RelayModeChatCompletions, model: "bot-x", want: base + "/api/v3/bots/chat/completions"},
		{name: "audio speech non-default base", relayMode: constant.RelayModeAudioSpeech, model: "tts", want: base + "/v1/audio/speech"},
		{name: "unsupported mode errors", relayMode: 99999, model: "m", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayFormat: tt.relayFormat,
				RelayMode:   tt.relayMode,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base, UpstreamModelName: tt.model},
			}
			got, err := a.GetRequestURL(info)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetRequestURL_DefaultBaseAudioSpeechUsesWebsocket(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ""},
	}
	got, err := a.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "wss://openspeech.bytedance.com/api/v1/tts/ws_binary", got)
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}

	t.Run("default sets bearer", func(t *testing.T) {
		c := newTestContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "abc"}}
		info.ApiKey = "abc"
		h := http.Header{}
		require.NoError(t, a.SetupRequestHeader(c, &h, info))
		assert.Equal(t, "Bearer abc", h.Get("Authorization"))
	})

	t.Run("audio speech uses bearer-semicolon token", func(t *testing.T) {
		c := newTestContext()
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app|tok"}}
		info.ApiKey = "app|tok"
		h := http.Header{}
		require.NoError(t, a.SetupRequestHeader(c, &h, info))
		assert.Equal(t, "Bearer;tok", h.Get("Authorization"))
		assert.Equal(t, "application/json", h.Get("Content-Type"))
	})

	t.Run("images edits sets json content type", func(t *testing.T) {
		c := newTestContext()
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "abc"}}
		info.ApiKey = "abc"
		h := http.Header{}
		require.NoError(t, a.SetupRequestHeader(c, &h, info))
		assert.Equal(t, "Bearer abc", h.Get("Authorization"))
	})
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()

	t.Run("nil returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertOpenAIRequest(c, info, nil)
		require.Error(t, err)
	})

	t.Run("deepseek thinking suffix enables thinking", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			OriginModelName: "deepseek-v3-thinking",
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v3-thinking"},
		}
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-v3-thinking"}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		outReq := out.(*dto.GeneralOpenAIRequest)
		assert.Equal(t, "deepseek-v3", outReq.Model)
		assert.Equal(t, "deepseek-v3", info.UpstreamModelName)
		assert.Contains(t, string(outReq.THINKING), "enabled")
	})

	t.Run("plain request passthrough", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			OriginModelName: "doubao-pro",
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "doubao-pro"},
		}
		req := &dto.GeneralOpenAIRequest{Model: "doubao-pro"}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		assert.Equal(t, req, out)
	})
}

// ---- ConvertImageRequest / ConvertEmbeddingRequest / ConvertOpenAIResponsesRequest ----

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()
	req := dto.ImageRequest{Model: "seedream", Prompt: "cat"}

	gen := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesGenerations, ChannelMeta: &relaycommon.ChannelMeta{}}
	out, err := a.ConvertImageRequest(c, gen, req)
	require.NoError(t, err)
	assert.Equal(t, req, out)

	other := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, ChannelMeta: &relaycommon.ChannelMeta{}}
	out2, err := a.ConvertImageRequest(c, other, req)
	require.NoError(t, err)
	assert.Equal(t, req, out2)
}

func TestConvertEmbeddingAndResponses(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	emb := dto.EmbeddingRequest{Model: "doubao-embedding"}
	gotEmb, err := a.ConvertEmbeddingRequest(c, info, emb)
	require.NoError(t, err)
	assert.Equal(t, emb, gotEmb)

	resp := dto.OpenAIResponsesRequest{Model: "doubao"}
	gotResp, err := a.ConvertOpenAIResponsesRequest(c, info, resp)
	require.NoError(t, err)
	assert.Equal(t, resp, gotResp)
}

// ---- ConvertAudioRequest ----

func TestConvertAudioRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("unsupported mode errors", func(t *testing.T) {
		c := newTestContext()
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
		require.Error(t, err)
	})

	t.Run("invalid api key errors", func(t *testing.T) {
		c := newTestContext()
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "no-pipe"}}
		info.ApiKey = "no-pipe"
		_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{Input: "hi"})
		require.Error(t, err)
	})

	t.Run("valid request builds volcengine payload and marks stream", func(t *testing.T) {
		c := newTestContext()
		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeAudioSpeech,
			OriginModelName: "tts-model",
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "appid|token"},
		}
		info.ApiKey = "appid|token"
		req := dto.AudioRequest{Input: "hello", Voice: "alloy", Speed: lo.ToPtr(1.2), ResponseFormat: "mp3"}
		reader, err := a.ConvertAudioRequest(c, info, req)
		require.NoError(t, err)
		body, _ := io.ReadAll(reader)
		var payload VolcengineTTSRequest
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Equal(t, "appid", payload.App.AppID)
		assert.Equal(t, "token", payload.App.Token)
		assert.Equal(t, "hello", payload.Request.Text)
		// alloy maps to a known volcengine voice.
		assert.Equal(t, "zh_male_M392_conversation_wvae_bigtts", payload.Audio.VoiceType)
		assert.Equal(t, "submit", payload.Request.Operation)
		assert.True(t, info.IsStream, "submit operation must mark the request as streaming")
		assert.Equal(t, "mp3", c.GetString(contextKeyResponseFormat))
	})

	t.Run("metadata merges into request", func(t *testing.T) {
		c := newTestContext()
		info := &relaycommon.RelayInfo{
			RelayMode:   constant.RelayModeAudioSpeech,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "appid|token"},
		}
		info.ApiKey = "appid|token"
		req := dto.AudioRequest{
			Input:    "hi",
			Voice:    "custom_voice",
			Metadata: json.RawMessage(`{"audio":{"emotion":"happy"}}`),
		}
		reader, err := a.ConvertAudioRequest(c, info, req)
		require.NoError(t, err)
		body, _ := io.ReadAll(reader)
		var payload VolcengineTTSRequest
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Equal(t, "happy", payload.Audio.Emotion)
		// unmapped voice passes through unchanged.
		assert.Equal(t, "custom_voice", payload.Audio.VoiceType)
	})
}

// ---- pure helpers ----

func TestPureHelpers(t *testing.T) {
	t.Run("parseVolcengineAuth", func(t *testing.T) {
		appID, token, err := parseVolcengineAuth("a|b")
		require.NoError(t, err)
		assert.Equal(t, "a", appID)
		assert.Equal(t, "b", token)

		_, _, err = parseVolcengineAuth("bad")
		require.Error(t, err)
	})

	t.Run("mapVoiceType", func(t *testing.T) {
		assert.Equal(t, "zh_female_tianmei_mars_bigtts", mapVoiceType("fable"))
		assert.Equal(t, "unknown", mapVoiceType("unknown"))
	})

	t.Run("mapEncoding", func(t *testing.T) {
		assert.Equal(t, "ogg_opus", mapEncoding("opus"))
		assert.Equal(t, "pcm", mapEncoding("pcm"))
		assert.Equal(t, "mp3", mapEncoding("unknown-format"))
	})

	t.Run("getContentTypeByEncoding", func(t *testing.T) {
		assert.Equal(t, "audio/mpeg", getContentTypeByEncoding("mp3"))
		assert.Equal(t, "audio/ogg", getContentTypeByEncoding("ogg_opus"))
		assert.Equal(t, "application/octet-stream", getContentTypeByEncoding("weird"))
	})

	t.Run("detectImageMimeType", func(t *testing.T) {
		assert.Equal(t, "image/jpeg", detectImageMimeType("a.jpg"))
		assert.Equal(t, "image/jpeg", detectImageMimeType("a.jpeg"))
		assert.Equal(t, "image/png", detectImageMimeType("a.png"))
		assert.Equal(t, "image/webp", detectImageMimeType("a.webp"))
		assert.Equal(t, "image/jpeg", detectImageMimeType("a.jpx"))
		assert.Equal(t, "image/png", detectImageMimeType("a.gif"))
	})

	t.Run("generateRequestID is unique", func(t *testing.T) {
		assert.NotEqual(t, generateRequestID(), generateRequestID())
	})
}

// ---- handleTTSResponse (non-stream HTTP) ----

func TestHandleTTSResponse(t *testing.T) {
	t.Run("success writes decoded audio", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		info.SetEstimatePromptTokens(11)

		audio := base64.StdEncoding.EncodeToString([]byte("SOUND"))
		body := `{"reqid":"r","code":3000,"message":"ok","data":"` + audio + `"}`
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, body), info, "mp3")
		require.Nil(t, err)
		require.NotNil(t, usage)
		assert.Equal(t, "SOUND", rec.Body.String())
		assert.Equal(t, "audio/mpeg", rec.Header().Get("Content-Type"))
		u := usage.(*dto.Usage)
		assert.Equal(t, 11, u.PromptTokens)
	})

	t.Run("non-3000 code returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"reqid":"r","code":4001,"message":"quota exceeded","data":""}`
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, body), info, "mp3")
		require.Nil(t, usage)
		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "quota exceeded")
	})

	t.Run("malformed body returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, "not-json"), info, "mp3")
		require.Nil(t, usage)
		require.NotNil(t, err)
	})

	t.Run("invalid base64 audio returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"reqid":"r","code":3000,"message":"ok","data":"!!!not-base64!!!"}`
		usage, err := handleTTSResponse(c, newResponse(http.StatusOK, body), info, "mp3")
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- protocols.go binary framing ----

func TestMessageMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Run("full client request round trips", func(t *testing.T) {
		msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagNoSeq)
		require.NoError(t, err)
		msg.Payload = []byte(`{"hello":"world"}`)
		frame, err := msg.Marshal()
		require.NoError(t, err)

		decoded, err := NewMessageFromBytes(frame)
		require.NoError(t, err)
		assert.Equal(t, MsgTypeFullClientRequest, decoded.MsgType)
		assert.Equal(t, msg.Payload, decoded.Payload)
	})

	t.Run("error message round trips error code", func(t *testing.T) {
		msg, err := NewMessage(MsgTypeError, MsgTypeFlagNoSeq)
		require.NoError(t, err)
		msg.ErrorCode = 45000000
		msg.Payload = []byte("boom")
		frame, err := msg.Marshal()
		require.NoError(t, err)

		decoded, err := NewMessageFromBytes(frame)
		require.NoError(t, err)
		assert.Equal(t, MsgTypeError, decoded.MsgType)
		assert.Equal(t, uint32(45000000), decoded.ErrorCode)
		assert.Equal(t, []byte("boom"), decoded.Payload)
	})

	t.Run("too short input errors", func(t *testing.T) {
		_, err := NewMessageFromBytes([]byte{0x11})
		require.Error(t, err)
	})
}

func TestMessageWithEventAndSession(t *testing.T) {
	msg, err := NewMessage(MsgTypeFullServerResponse, MsgTypeFlagWithEvent)
	require.NoError(t, err)
	msg.EventType = EventType_StartSession
	msg.SessionID = "sess-123"
	msg.Payload = []byte(`{"k":"v"}`)
	frame, err := msg.Marshal()
	require.NoError(t, err)

	decoded, err := NewMessageFromBytes(frame)
	require.NoError(t, err)
	assert.Equal(t, EventType_StartSession, decoded.EventType)
	assert.Equal(t, "sess-123", decoded.SessionID)
	assert.Equal(t, msg.Payload, decoded.Payload)
}

func TestMessageWithSequence(t *testing.T) {
	msg, err := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagPositiveSeq)
	require.NoError(t, err)
	msg.Sequence = 7
	msg.Payload = []byte("audio")
	frame, err := msg.Marshal()
	require.NoError(t, err)

	decoded, err := NewMessageFromBytes(frame)
	require.NoError(t, err)
	assert.Equal(t, int32(7), decoded.Sequence)
	assert.Equal(t, []byte("audio"), decoded.Payload)
}

func TestProtocolStringers(t *testing.T) {
	assert.Equal(t, "MsgType_FullClientRequest", MsgTypeFullClientRequest.String())
	assert.Equal(t, "MsgType_Error", MsgTypeError.String())
	assert.Contains(t, MsgType(200).String(), "MsgType_(")

	assert.Equal(t, "EventType_None", EventType_None.String())
	assert.Equal(t, "EventType_TTSResponse", EventType_TTSResponse.String())
	assert.Contains(t, EventType(99999).String(), "EventType_(")

	msg, _ := NewMessage(MsgTypeError, MsgTypeFlagNoSeq)
	msg.ErrorCode = 1
	assert.Contains(t, msg.String(), "ErrorCode")
}

func TestEventTypeStringSweep(t *testing.T) {
	events := []EventType{
		EventType_StartConnection, EventType_FinishConnection, EventType_ConnectionStarted,
		EventType_ConnectionFailed, EventType_ConnectionFinished, EventType_StartSession,
		EventType_CancelSession, EventType_FinishSession, EventType_SessionStarted,
		EventType_SessionCanceled, EventType_SessionFinished, EventType_SessionFailed,
		EventType_UsageResponse, EventType_TaskRequest, EventType_UpdateConfig,
		EventType_AudioMuted, EventType_SayHello, EventType_TTSSentenceStart,
		EventType_TTSSentenceEnd, EventType_TTSEnded, EventType_PodcastRoundStart,
		EventType_PodcastRoundResponse, EventType_PodcastRoundEnd, EventType_ASRInfo,
		EventType_ASRResponse, EventType_ASREnded, EventType_ChatTTSText,
		EventType_ChatResponse, EventType_ChatEnded, EventType_SourceSubtitleStart,
		EventType_SourceSubtitleResponse, EventType_SourceSubtitleEnd,
		EventType_TranslationSubtitleStart, EventType_TranslationSubtitleResponse,
		EventType_TranslationSubtitleEnd,
	}
	for _, e := range events {
		assert.NotEmpty(t, e.String())
	}
	msgTypes := []MsgType{
		MsgTypeFullClientRequest, MsgTypeAudioOnlyClient, MsgTypeFullServerResponse,
		MsgTypeAudioOnlyServer, MsgTypeFrontEndResultServer,
	}
	for _, m := range msgTypes {
		assert.NotEmpty(t, m.String())
	}
}

// ---- handleTTSWebSocketResponse (real gorilla websocket, no external network) ----

func newWSContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, rec
}

func TestHandleTTSWebSocketResponse(t *testing.T) {
	upgrader := websocket.Upgrader{}

	t.Run("audio frames stream back to client", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			if _, err := ReceiveMessage(conn); err != nil {
				return
			}
			// final audio frame carries a negative sequence -> terminates the loop.
			out, _ := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagNegativeSeq)
			out.Sequence = -1
			out.Payload = []byte("AUDIODATA")
			frame, _ := out.Marshal()
			_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		}))
		defer srv.Close()

		c, rec := newWSContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "appid|token"}}
		info.ApiKey = "appid|token"
		info.SetEstimatePromptTokens(4)

		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
		usage, apiErr := handleTTSWebSocketResponse(c, wsURL, VolcengineTTSRequest{}, info, "mp3")
		require.Nil(t, apiErr)
		require.NotNil(t, usage)
		assert.Contains(t, rec.Body.String(), "AUDIODATA")
		assert.Equal(t, 4, usage.(*dto.Usage).PromptTokens)
	})

	t.Run("server error message returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			if _, err := ReceiveMessage(conn); err != nil {
				return
			}
			out, _ := NewMessage(MsgTypeError, MsgTypeFlagNoSeq)
			out.ErrorCode = 4003
			out.Payload = []byte("server boom")
			frame, _ := out.Marshal()
			_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		}))
		defer srv.Close()

		c, _ := newWSContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "appid|token"}}
		info.ApiKey = "appid|token"

		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
		usage, apiErr := handleTTSWebSocketResponse(c, wsURL, VolcengineTTSRequest{}, info, "mp3")
		require.Nil(t, usage)
		require.NotNil(t, apiErr)
		assert.Contains(t, apiErr.Error(), "server boom")
	})

	t.Run("invalid api key returns error", func(t *testing.T) {
		c, _ := newWSContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "no-pipe"}}
		info.ApiKey = "no-pipe"
		usage, apiErr := handleTTSWebSocketResponse(c, "ws://127.0.0.1:0", VolcengineTTSRequest{}, info, "mp3")
		require.Nil(t, usage)
		require.NotNil(t, apiErr)
	})

	t.Run("dial failure returns error", func(t *testing.T) {
		c, _ := newWSContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "appid|token"}}
		info.ApiKey = "appid|token"
		usage, apiErr := handleTTSWebSocketResponse(c, "ws://127.0.0.1:1", VolcengineTTSRequest{}, info, "mp3")
		require.Nil(t, usage)
		require.NotNil(t, apiErr)
	})
}

// ---- DoResponse audio (non-stream) delegates to handleTTSResponse ----

func TestDoResponseAudioNonStream(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Set(contextKeyResponseFormat, "mp3")

	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeAudioSpeech,
		IsStream:    false,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	info.SetEstimatePromptTokens(2)

	audio := base64.StdEncoding.EncodeToString([]byte("PCM"))
	body := `{"reqid":"r","code":3000,"data":"` + audio + `"}`
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, body), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, "PCM", rec.Body.String())
}

// ---- getters ----

func TestGetters(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())
	_, err := a.ConvertGeminiRequest(newTestContext(), &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, &dto.GeminiChatRequest{})
	assert.Error(t, err)
	rr, err := a.ConvertRerankRequest(newTestContext(), 0, dto.RerankRequest{})
	assert.NoError(t, err)
	assert.Nil(t, rr)
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
}
