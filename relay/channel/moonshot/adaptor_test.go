package moonshot

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func infoWith(base string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base},
	}
}

func TestGetRequestURL_Claude(t *testing.T) {
	info := infoWith("https://api.moonshot.cn")
	info.RelayFormat = types.RelayFormatClaude
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.moonshot.cn/anthropic/v1/messages", url)
}

func TestGetRequestURL_ModeVariants(t *testing.T) {
	cases := []struct {
		mode int
		want string
	}{
		{constant.RelayModeRerank, "https://b/v1/rerank"},
		{constant.RelayModeEmbeddings, "https://b/v1/embeddings"},
		{constant.RelayModeChatCompletions, "https://b/v1/chat/completions"},
		{constant.RelayModeCompletions, "https://b/v1/completions"},
	}
	for _, tc := range cases {
		info := infoWith("https://b")
		info.RelayMode = tc.mode
		url, err := (&Adaptor{}).GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, tc.want, url)
	}
}

func TestGetRequestURL_DefaultFallback(t *testing.T) {
	info := infoWith("https://b")
	info.RelayMode = -999 // unknown mode falls through to chat/completions
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://b/v1/chat/completions", url)
}

func TestGetRequestURL_SpecialBasePlan(t *testing.T) {
	// kimi-coding-plan is a registered ChannelSpecialBase
	claudeInfo := infoWith("kimi-coding-plan")
	claudeInfo.RelayFormat = types.RelayFormatClaude
	url, err := (&Adaptor{}).GetRequestURL(claudeInfo)
	require.NoError(t, err)
	assert.Equal(t, "https://api.kimi.com/coding/v1/messages", url)

	oaiInfo := infoWith("kimi-coding-plan")
	oaiInfo.RelayFormat = types.RelayFormatOpenAI
	url2, err := (&Adaptor{}).GetRequestURL(oaiInfo)
	require.NoError(t, err)
	assert.Equal(t, "https://api.kimi.com/coding/v1/chat/completions", url2)
}

func TestConvertOpenAIRequest_KimiK26ForcesTemperatureOne(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:       "kimi-k2.6",
		Temperature: common.GetPointer[float64](0.7),
	}
	info := infoWith("x")
	info.UpstreamModelName = "kimi-k2.6"

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	require.NotNil(t, converted.Temperature)
	assert.Equal(t, 1.0, *converted.Temperature)
}

func TestConvertOpenAIRequest_KimiK26AlreadyOneUnchanged(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:       "kimi-k2.6",
		Temperature: common.GetPointer[float64](1.0),
	}
	info := infoWith("x")
	info.UpstreamModelName = "kimi-k2.6"

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	assert.Equal(t, 1.0, *converted.Temperature)
}

func TestConvertOpenAIRequest_KimiK26NilTemperatureStaysNil(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{Model: "kimi-k2.6"}
	info := infoWith("x")
	info.UpstreamModelName = "kimi-k2.6"

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	assert.Nil(t, converted.Temperature)
}

func TestConvertOpenAIRequest_OtherModelKeepsTemperature(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:       "kimi-k2.5",
		Temperature: common.GetPointer[float64](0.7),
	}
	info := infoWith("x")
	info.UpstreamModelName = "kimi-k2.5"

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	assert.Equal(t, 0.7, *converted.Temperature)
}

func TestGetUpstreamModelName_FallbackWhenNoMeta(t *testing.T) {
	assert.True(t, isTemperatureOneOnlyModel("kimi-k2.6"))
	assert.True(t, isTemperatureOneOnlyModel("KIMI-K2.6")) // case-insensitive
	assert.False(t, isTemperatureOneOnlyModel("kimi-k2.5"))

	// getUpstreamModelName: nil ChannelMeta -> fallback used
	assert.Equal(t, "fb", getUpstreamModelName(&relaycommon.RelayInfo{}, "fb"))
	// ChannelMeta with UpstreamModelName -> used
	i := infoWith("x")
	i.UpstreamModelName = "up"
	assert.Equal(t, "up", getUpstreamModelName(i, "fb"))
}

func TestConvertRerankAndEmbeddingPassThrough(t *testing.T) {
	a := &Adaptor{}
	rr := dto.RerankRequest{Query: "q"}
	out, err := a.ConvertRerankRequest(nil, 0, rr)
	require.NoError(t, err)
	assert.Equal(t, rr, out)

	er := dto.EmbeddingRequest{Model: "m"}
	out2, err := a.ConvertEmbeddingRequest(nil, nil, er)
	require.NoError(t, err)
	assert.Equal(t, er, out2)
}

func TestUnimplemented(t *testing.T) {
	a := &Adaptor{}
	_, err := a.ConvertGeminiRequest(nil, nil, nil)
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
}

func TestSetupRequestHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	info := infoWith("x")
	info.ApiKey = "secret"
	h := http.Header{}
	err := (&Adaptor{}).SetupRequestHeader(c, &h, info)
	require.NoError(t, err)
	assert.Equal(t, "Bearer secret", h.Get("Authorization"))
	assert.Equal(t, "application/json", h.Get("Content-Type"))
}

func TestMetadata(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, "moonshot", a.GetChannelName())
	assert.NotEmpty(t, a.GetModelList())
	a.Init(infoWith("x")) // no-op, must not panic
}
