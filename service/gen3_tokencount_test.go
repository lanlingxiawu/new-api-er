package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// token_counter.go — EstimateRequestToken + CountTokenRealtime.
// Pure CPU token estimation. No DB / network for the covered paths.
// ===========================================================================

func withCountToken(t *testing.T, v bool) {
	t.Helper()
	orig := constant.CountToken
	constant.CountToken = v
	t.Cleanup(func() { constant.CountToken = orig })
}

func TestTokenCount_EstimateRequestToken_Disabled(t *testing.T) {
	withCountToken(t, false)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	got, err := EstimateRequestToken(c, &types.TokenCountMeta{}, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

func TestTokenCount_EstimateRequestToken_NilMeta(t *testing.T) {
	withCountToken(t, true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, err := EstimateRequestToken(c, nil, &relaycommon.RelayInfo{})
	assert.Error(t, err)
}

func TestTokenCount_EstimateRequestToken_RealtimeZero(t *testing.T) {
	withCountToken(t, true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime}
	got, err := EstimateRequestToken(c, &types.TokenCountMeta{}, info)
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

func TestTokenCount_EstimateRequestToken_TextNumber(t *testing.T) {
	withCountToken(t, true)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// TokenTypeTextNumber => rune count of CombineText, plus OpenAI framing.
	meta := &types.TokenCountMeta{
		TokenType:     types.TokenTypeTextNumber,
		CombineText:   "hello", // 5 runes
		ToolsCount:    2,
		MessagesCount: 3,
		NameCount:     1,
	}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, IsStream: true}
	got, err := EstimateRequestToken(c, meta, info)
	require.NoError(t, err)
	// 5 + tools*8(16) + messages*3(9) + names*3(3) + 3 = 36
	assert.Equal(t, 36, got)
	// Result is cached on the context.
	assert.Equal(t, 36, c.GetInt(string(constant.ContextKeyPromptTokens)))
}

func TestTokenCount_EstimateRequestToken_FileTypes(t *testing.T) {
	withCountToken(t, true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// Files with known FileType and non-URL sources skip fetching.
	audio := &types.FileMeta{FileType: types.FileTypeAudio, Source: types.NewBase64FileSource("x", "audio/mp3")}
	video := &types.FileMeta{FileType: types.FileTypeVideo, Source: types.NewBase64FileSource("x", "video/mp4")}
	file := &types.FileMeta{FileType: types.FileTypeFile, Source: types.NewBase64FileSource("x", "application/pdf")}
	meta := &types.TokenCountMeta{
		TokenType: types.TokenTypeTextNumber,
		Files:     []*types.FileMeta{audio, video, file},
	}
	// Non-OpenAI format so no framing tokens added.
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatGemini}
	got, err := EstimateRequestToken(c, meta, info)
	require.NoError(t, err)
	// audio 256 + video 8192 + file 4096 = 12544
	assert.Equal(t, 256+4096*2+4096, got)
}

func TestTokenCount_EstimateRequestToken_ImageFile(t *testing.T) {
	withCountToken(t, true)
	o1, o2 := constant.GetMediaToken, constant.GetMediaTokenNotStream
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true
	t.Cleanup(func() {
		constant.GetMediaToken = o1
		constant.GetMediaTokenNotStream = o2
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// OpenAI text model + a known-type image file => getImageToken path.
	img := imageMeta(t, 512, 512, "high")
	meta := &types.TokenCountMeta{
		TokenType: types.TokenTypeTextNumber,
		CombineText: "hi",
		Files:       []*types.FileMeta{img},
	}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, IsStream: true}
	got, err := EstimateRequestToken(c, meta, info)
	require.NoError(t, err)
	assert.Greater(t, got, 100, "image tokens included")
}

// --- CountTokenRealtime ----------------------------------------------------

func TestTokenCount_CountTokenRealtime_SessionUpdate(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	// Return order is (textToken, audioToken, err).
	text, audio, err := CountTokenRealtime(info, dto.RealtimeEvent{
		Type:    dto.RealtimeEventTypeSessionUpdate,
		Session: &dto.RealtimeSession{Instructions: "hello world"},
	}, "gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, 0, audio)
	assert.Greater(t, text, 0)

	// Session nil => no crash, zero.
	text, audio, err = CountTokenRealtime(info, dto.RealtimeEvent{Type: dto.RealtimeEventTypeSessionUpdate}, "gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, 0, audio+text)
}

func TestTokenCount_CountTokenRealtime_TextDelta(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	text, _, err := CountTokenRealtime(info, dto.RealtimeEvent{
		Type:  dto.RealtimeEventResponseAudioTranscriptionDelta,
		Delta: "some transcript text",
	}, "gpt-4o")
	require.NoError(t, err)
	assert.Greater(t, text, 0)
}

func TestTokenCount_CountTokenRealtime_EmptyAudio(t *testing.T) {
	info := &relaycommon.RelayInfo{OutputAudioFormat: "pcm16", InputAudioFormat: "pcm16"}
	// Empty audio deltas => zero tokens, no error (exercises the audio branches).
	text, audio, err := CountTokenRealtime(info, dto.RealtimeEvent{
		Type: dto.RealtimeEventResponseAudioDelta, Delta: "",
	}, "gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, 0, text+audio)

	text, audio, err = CountTokenRealtime(info, dto.RealtimeEvent{
		Type: dto.RealtimeEventInputAudioBufferAppend, Audio: "",
	}, "gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, 0, text+audio)
}

func TestTokenCount_CountTokenRealtime_ConversationItem(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	text, _, err := CountTokenRealtime(info, dto.RealtimeEvent{
		Type: dto.RealtimeEventConversationItemCreated,
		Item: &dto.RealtimeItem{
			Type:    "message",
			Content: []dto.RealtimeContent{{Type: "input_text", Text: "hello there"}},
		},
	}, "gpt-4o")
	require.NoError(t, err)
	assert.Greater(t, text, 0)
}

func TestTokenCount_CountTokenRealtime_ResponseDoneTools(t *testing.T) {
	info := &relaycommon.RelayInfo{
		IsFirstRequest: false,
		RealtimeTools:  []dto.RealTimeTool{{Name: "get_weather", Description: "gets the weather"}},
	}
	text, _, err := CountTokenRealtime(info, dto.RealtimeEvent{Type: dto.RealtimeEventTypeResponseDone}, "gpt-4o")
	require.NoError(t, err)
	assert.Greater(t, text, 0)
}
