package common

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	commonpkg "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRelayContext(t *testing.T, path string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c
}

// ---------------------------------------------------------------------------
// GetFinalRequestRelayFormat
// ---------------------------------------------------------------------------

func TestGetFinalRequestRelayFormat(t *testing.T) {
	t.Run("explicit final wins", func(t *testing.T) {
		info := &RelayInfo{
			RelayFormat:             types.RelayFormatOpenAI,
			RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
			FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
		}
		assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
	})
	t.Run("falls back to conversion chain tail", func(t *testing.T) {
		info := &RelayInfo{
			RelayFormat:            types.RelayFormatOpenAI,
			RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
		}
		assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
	})
	t.Run("falls back to relay format", func(t *testing.T) {
		info := &RelayInfo{RelayFormat: types.RelayFormatGemini}
		assert.Equal(t, types.RelayFormat(types.RelayFormatGemini), info.GetFinalRequestRelayFormat())
	})
	t.Run("nil receiver", func(t *testing.T) {
		var info *RelayInfo
		assert.Equal(t, types.RelayFormat(""), info.GetFinalRequestRelayFormat())
	})
}

func TestRequestConversionChain(t *testing.T) {
	t.Run("init sets first element from relay format", func(t *testing.T) {
		info := &RelayInfo{RelayFormat: types.RelayFormatOpenAI}
		info.InitRequestConversionChain()
		require.Len(t, info.RequestConversionChain, 1)
	})
	t.Run("init no-op when already populated", func(t *testing.T) {
		info := &RelayInfo{
			RelayFormat:            types.RelayFormatOpenAI,
			RequestConversionChain: []types.RelayFormat{types.RelayFormatClaude},
		}
		info.InitRequestConversionChain()
		require.Len(t, info.RequestConversionChain, 1)
		assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.RequestConversionChain[0])
	})
	t.Run("init no-op when relay format empty", func(t *testing.T) {
		info := &RelayInfo{}
		info.InitRequestConversionChain()
		assert.Empty(t, info.RequestConversionChain)
	})
	t.Run("append skips empty and consecutive duplicates", func(t *testing.T) {
		info := &RelayInfo{}
		info.AppendRequestConversion("")
		assert.Empty(t, info.RequestConversionChain)
		info.AppendRequestConversion(types.RelayFormatOpenAI)
		info.AppendRequestConversion(types.RelayFormatOpenAI) // duplicate tail skipped
		info.AppendRequestConversion(types.RelayFormatClaude)
		require.Len(t, info.RequestConversionChain, 2)
	})
	t.Run("nil receiver safe", func(t *testing.T) {
		var info *RelayInfo
		assert.NotPanics(t, func() {
			info.InitRequestConversionChain()
			info.AppendRequestConversion(types.RelayFormatOpenAI)
		})
	})
}

// ---------------------------------------------------------------------------
// genBaseRelayInfo via Gen* constructors
// ---------------------------------------------------------------------------

func TestGenRelayInfoOpenAI(t *testing.T) {
	c := newRelayContext(t, "/v1/chat/completions")
	commonpkg.SetContextKey(c, constant.ContextKeyUserId, 42)
	commonpkg.SetContextKey(c, constant.ContextKeyUserGroup, "grp")
	commonpkg.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
	commonpkg.SetContextKey(c, constant.ContextKeyTokenId, 7)
	commonpkg.SetContextKey(c, constant.ContextKeyEstimatedTokens, 123)

	req := &dto.GeneralOpenAIRequest{Model: "gpt-4o"}
	info := GenRelayInfoOpenAI(c, req)
	require.NotNil(t, info)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), info.RelayFormat)
	assert.Equal(t, 42, info.UserId)
	assert.Equal(t, "gpt-4o", info.OriginModelName)
	assert.Equal(t, 7, info.TokenId)
	assert.Equal(t, 123, info.GetEstimatePromptTokens())
	// token group falls back to user group when token group empty
	assert.Equal(t, "grp", info.TokenGroup)
	require.Len(t, info.RequestConversionChain, 0) // constructors do not init chain; GenRelayInfo does
}

func TestGenBaseRelayInfo_PlaygroundPathRewrite(t *testing.T) {
	c := newRelayContext(t, "/pg/chat/completions")
	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "m"})
	assert.True(t, info.IsPlayground)
	assert.Equal(t, "/v1/chat/completions", info.RequestURLPath)
}

func TestGenBaseRelayInfo_StartTimeDefault(t *testing.T) {
	c := newRelayContext(t, "/v1/chat/completions")
	before := time.Now()
	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "m"})
	assert.False(t, info.StartTime.Before(before.Add(-time.Second)))
	// FirstResponseTime is StartTime - 1s until SetFirstResponseTime.
	assert.True(t, info.FirstResponseTime.Before(info.StartTime))
	assert.False(t, info.HasSendResponse())
}

func TestGenRelayInfoClaude(t *testing.T) {
	c := newRelayContext(t, "/v1/messages?beta=true")
	info := GenRelayInfoClaude(c, &dto.ClaudeRequest{Model: "claude-3"})
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.RelayFormat)
	require.NotNil(t, info.ClaudeConvertInfo)
	assert.Equal(t, LastMessageTypeNone, info.ClaudeConvertInfo.LastMessagesType)
	assert.True(t, info.IsClaudeBetaQuery)
	assert.False(t, info.ShouldIncludeUsage)
}

func TestGenRelayInfoRerank(t *testing.T) {
	c := newRelayContext(t, "/v1/rerank")
	req := &dto.RerankRequest{Query: "q", Documents: []any{"d1", "d2"}}
	info := GenRelayInfoRerank(c, req)
	assert.Equal(t, types.RelayFormat(types.RelayFormatRerank), info.RelayFormat)
	assert.Equal(t, relayconstant.RelayModeRerank, info.RelayMode)
	require.NotNil(t, info.RerankerInfo)
	assert.Len(t, info.RerankerInfo.Documents, 2)
}

func TestGenRelayInfoResponses(t *testing.T) {
	c := newRelayContext(t, "/v1/responses")
	req := &dto.OpenAIResponsesRequest{Model: "gpt-4o"}
	req.Tools = []byte(`[{"type":"` + dto.BuildInToolWebSearchPreview + `"}]`)
	info := GenRelayInfoResponses(c, req)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.RelayFormat)
	require.NotNil(t, info.ResponsesUsageInfo)
	tool, ok := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]
	require.True(t, ok)
	assert.Equal(t, "medium", tool.SearchContextSize)
}

func TestGenRelayInfo_Dispatch(t *testing.T) {
	mk := func(path string) *gin.Context { return newRelayContext(t, path) }

	tests := []struct {
		name    string
		format  types.RelayFormat
		req     dto.Request
		wantErr bool
	}{
		{"openai", types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: "m"}, false},
		{"audio", types.RelayFormatOpenAIAudio, &dto.AudioRequest{Model: "tts"}, false},
		{"image", types.RelayFormatOpenAIImage, &dto.ImageRequest{Model: "gpt-image-1"}, false},
		{"claude", types.RelayFormatClaude, &dto.ClaudeRequest{Model: "c"}, false},
		{"gemini", types.RelayFormatGemini, &dto.GeminiChatRequest{}, false},
		{"embedding", types.RelayFormatEmbedding, &dto.EmbeddingRequest{Model: "e"}, false},
		{"rerank ok", types.RelayFormatRerank, &dto.RerankRequest{Query: "q", Documents: []any{"d"}}, false},
		{"responses ok", types.RelayFormatOpenAIResponses, &dto.OpenAIResponsesRequest{Model: "m"}, false},
		{"invalid format", types.RelayFormat("nope"), &dto.GeneralOpenAIRequest{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := GenRelayInfo(mk("/v1/x"), tt.format, tt.req, nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, info)
			// GenRelayInfo initializes the conversion chain.
			require.NotEmpty(t, info.RequestConversionChain)
		})
	}
}

func TestGenRelayInfo_RerankWrongType(t *testing.T) {
	_, err := GenRelayInfo(newRelayContext(t, "/v1/rerank"), types.RelayFormatRerank, &dto.GeneralOpenAIRequest{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "RerankRequest")
}

func TestGenRelayInfo_ResponsesWrongType(t *testing.T) {
	_, err := GenRelayInfo(newRelayContext(t, "/v1/responses"), types.RelayFormatOpenAIResponses, &dto.GeneralOpenAIRequest{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "OpenAIResponsesRequest")
}

func TestGenRelayInfo_Task(t *testing.T) {
	info, err := GenRelayInfo(newRelayContext(t, "/v1/video/generations"), types.RelayFormatTask, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, info.TaskRelayInfo)
}

func TestGenRelayInfoResponsesCompaction(t *testing.T) {
	c := newRelayContext(t, "/v1/responses/compact")
	info := GenRelayInfoResponsesCompaction(c, &dto.OpenAIResponsesCompactionRequest{Model: "m"})
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponsesCompaction), info.RelayFormat)
}

// ---------------------------------------------------------------------------
// InitChannelMeta
// ---------------------------------------------------------------------------

func TestInitChannelMeta(t *testing.T) {
	c := newRelayContext(t, "/v1/chat/completions")
	commonpkg.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	commonpkg.SetContextKey(c, constant.ContextKeyChannelId, 99)
	commonpkg.SetContextKey(c, constant.ContextKeyChannelName, "my-channel")
	commonpkg.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://api.openai.com")
	commonpkg.SetContextKey(c, constant.ContextKeyChannelKey, "sk-secret")
	commonpkg.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")

	info := &RelayInfo{OriginModelName: "gpt-4o"}
	info.InitChannelMeta(c)
	require.NotNil(t, info.ChannelMeta)
	assert.Equal(t, constant.ChannelTypeOpenAI, info.ChannelMeta.ChannelType)
	assert.Equal(t, 99, info.ChannelMeta.ChannelId)
	assert.Equal(t, "my-channel", info.ChannelMeta.ChannelName)
	assert.Equal(t, "sk-secret", info.ChannelMeta.ApiKey)
	// OpenAI supports stream options.
	assert.True(t, info.ChannelMeta.SupportStreamOptions)
}

func TestInitChannelMeta_AzureApiVersion(t *testing.T) {
	c := newRelayContext(t, "/v1/chat?api-version=2024-05-01")
	commonpkg.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAzure)
	info := &RelayInfo{}
	info.InitChannelMeta(c)
	assert.Equal(t, "2024-05-01", info.ChannelMeta.ApiVersion)
}

// ---------------------------------------------------------------------------
// SetFirstResponseTime / estimate token setters
// ---------------------------------------------------------------------------

func TestSetFirstResponseTime(t *testing.T) {
	info := &RelayInfo{StartTime: time.Now().Add(-time.Minute), isFirstResponse: true}
	info.SetFirstResponseTime()
	assert.True(t, info.HasSendResponse())
	prev := info.FirstResponseTime
	// second call is a no-op
	info.SetFirstResponseTime()
	assert.Equal(t, prev, info.FirstResponseTime)
}

func TestEstimatePromptTokens(t *testing.T) {
	info := &RelayInfo{}
	info.SetEstimatePromptTokens(555)
	assert.Equal(t, 555, info.GetEstimatePromptTokens())
}

// ---------------------------------------------------------------------------
// ToString (masking)
// ---------------------------------------------------------------------------

func TestRelayInfoToString(t *testing.T) {
	assert.Equal(t, "RelayInfo<nil>", (*RelayInfo)(nil).ToString())

	info := &RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-4o",
		UserId:          1,
		UserEmail:       "user@example.com",
		StartTime:       time.Now(),
		ChannelMeta:     &ChannelMeta{ChannelType: 1, ChannelId: 5, ApiKey: "sk-secret-key"},
	}
	s := info.ToString()
	assert.Contains(t, s, "gpt-4o")
	assert.Contains(t, s, "***masked***")
	assert.NotContains(t, s, "sk-secret-key")
}

// ---------------------------------------------------------------------------
// TaskSubmitReq
// ---------------------------------------------------------------------------

func TestTaskSubmitReqUnmarshal(t *testing.T) {
	t.Run("integer duration", func(t *testing.T) {
		var req TaskSubmitReq
		require.NoError(t, commonpkg.UnmarshalJsonStr(`{"prompt":"p","duration":8}`, &req))
		assert.Equal(t, 8, req.Duration)
	})
	t.Run("string duration", func(t *testing.T) {
		var req TaskSubmitReq
		require.NoError(t, commonpkg.UnmarshalJsonStr(`{"prompt":"p","duration":"12"}`, &req))
		assert.Equal(t, 12, req.Duration)
	})
	t.Run("metadata object", func(t *testing.T) {
		var req TaskSubmitReq
		require.NoError(t, commonpkg.UnmarshalJsonStr(`{"prompt":"p","metadata":{"a":1}}`, &req))
		require.NotNil(t, req.Metadata)
		assert.EqualValues(t, 1, req.Metadata["a"])
	})
	t.Run("metadata string-encoded object", func(t *testing.T) {
		var req TaskSubmitReq
		require.NoError(t, commonpkg.UnmarshalJsonStr(`{"prompt":"p","metadata":"{\"b\":2}"}`, &req))
		require.NotNil(t, req.Metadata)
		assert.EqualValues(t, 2, req.Metadata["b"])
	})
	t.Run("helpers", func(t *testing.T) {
		req := TaskSubmitReq{Prompt: "hello", Images: []string{"x"}}
		assert.Equal(t, "hello", req.GetPrompt())
		assert.True(t, req.HasImage())
		empty := TaskSubmitReq{}
		assert.False(t, empty.HasImage())
	})
	t.Run("unmarshal metadata into target", func(t *testing.T) {
		req := TaskSubmitReq{Metadata: map[string]interface{}{"n": 3}}
		var target struct {
			N int `json:"n"`
		}
		require.NoError(t, req.UnmarshalMetadata(&target))
		assert.Equal(t, 3, target.N)
	})
}

func TestFailTaskInfo(t *testing.T) {
	info := FailTaskInfo("bad")
	assert.Equal(t, "FAILURE", info.Status)
	assert.Equal(t, "bad", info.Reason)
}

func TestRelayInfoMetaTypedNilReceiver(t *testing.T) {
	var info *RelayInfo
	var meta convmeta.Meta = info

	assert.Empty(t, meta.GetOriginModelName())
	assert.Empty(t, meta.GetUpstreamModelName())
	assert.False(t, meta.HasChannelMeta())
	assert.Zero(t, meta.GetChannelID())
	assert.Zero(t, meta.GetChannelType())
	assert.False(t, meta.GetIsStream())
	assert.Empty(t, meta.GetReasoningEffort())
	assert.Zero(t, meta.GetEstimatePromptTokens())
	assert.Zero(t, meta.GetSendResponseCount())

	assert.NotPanics(t, func() {
		meta.SetReasoningEffort("high")
		meta.IncrSendResponseCount()
		meta.AppendRequestConversion(types.RelayFormatClaude)
	})

	firstState := meta.EnsureClaudeConvertInfo()
	secondState := meta.EnsureClaudeConvertInfo()
	require.NotNil(t, firstState)
	require.NotNil(t, secondState)
	assert.Equal(t, convmeta.LastMessageTypeNone, firstState.LastMessagesType)
	assert.NotSame(t, firstState, secondState)

	firstOptions := meta.ConvOptions()
	secondOptions := meta.ConvOptions()
	require.NotNil(t, firstOptions)
	require.NotNil(t, secondOptions)
	assert.NotSame(t, firstOptions, secondOptions)
	assert.NotNil(t, firstOptions.Claude.DefaultMaxTokens)
	assert.NotNil(t, firstOptions.Gemini.SupportsImagine)
	assert.NotNil(t, firstOptions.Gemini.SafetySetting)
	assert.NotNil(t, firstOptions.PreserveThinkingSuffix)
}
