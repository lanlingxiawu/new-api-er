package deepseek

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newInfo(baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: baseURL},
	}
}

func TestGetRequestURL_ClaudeFormat(t *testing.T) {
	info := newInfo("https://api.deepseek.com")
	info.RelayFormat = types.RelayFormatClaude
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.deepseek.com/anthropic/v1/messages", url)
}

func TestGetRequestURL_CompletionsAppendsBeta(t *testing.T) {
	info := newInfo("https://api.deepseek.com")
	info.RelayMode = constant.RelayModeCompletions
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.deepseek.com/beta/completions", url)
}

func TestGetRequestURL_CompletionsWithExistingBetaSuffix(t *testing.T) {
	info := newInfo("https://api.deepseek.com/beta")
	info.RelayMode = constant.RelayModeCompletions
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	// base already ends with /beta, so it is not doubled
	assert.Equal(t, "https://api.deepseek.com/beta/completions", url)
}

func TestGetRequestURL_DefaultChatCompletions(t *testing.T) {
	info := newInfo("https://api.deepseek.com")
	info.RelayMode = constant.RelayModeChatCompletions
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.deepseek.com/v1/chat/completions", url)
}

func TestConvertOpenAIRequest_Nil(t *testing.T) {
	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, newInfo("x"), nil)
	require.Error(t, err)
}

func TestConvertOpenAIRequest_ThinkingSuffixApplied(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{Model: "deepseek-v4-flash-max"}
	info := newInfo("x")
	info.UpstreamModelName = "deepseek-v4-flash-max"

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	converted, ok := out.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Equal(t, "deepseek-v4-flash", converted.Model)
	assert.Equal(t, "max", converted.ReasoningEffort)
	assert.NotNil(t, converted.THINKING)
	// info is updated too
	assert.Equal(t, "deepseek-v4-flash", info.UpstreamModelName)
	assert.Equal(t, "max", info.ReasoningEffort)
}

func TestConvertOpenAIRequest_NoSuffixUnchanged(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{Model: "deepseek-chat"}
	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, newInfo("x"), req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	assert.Equal(t, "deepseek-chat", converted.Model)
	assert.Empty(t, converted.ReasoningEffort)
	assert.Nil(t, converted.THINKING)
}

func TestApplyClaudeThinkingSuffix_MaxEnabled(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "deepseek-v4-pro-max"}
	err := applyDeepSeekV4ClaudeThinkingSuffix(nil, req)
	require.NoError(t, err)
	assert.Equal(t, "deepseek-v4-pro", req.Model)
	require.NotNil(t, req.Thinking)
	assert.Equal(t, "enabled", req.Thinking.Type)
	assert.NotNil(t, req.OutputConfig)
}

func TestApplyClaudeThinkingSuffix_NoneDisabledNoOutputConfig(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "deepseek-v4-pro-none"}
	err := applyDeepSeekV4ClaudeThinkingSuffix(nil, req)
	require.NoError(t, err)
	assert.Equal(t, "deepseek-v4-pro", req.Model)
	require.NotNil(t, req.Thinking)
	assert.Equal(t, "disabled", req.Thinking.Type)
	assert.Nil(t, req.OutputConfig)
}

func TestApplyClaudeThinkingSuffix_NoMatchUnchanged(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-3-5-sonnet"}
	err := applyDeepSeekV4ClaudeThinkingSuffix(nil, req)
	require.NoError(t, err)
	assert.Equal(t, "claude-3-5-sonnet", req.Model)
	assert.Nil(t, req.Thinking)
}

func TestApplyClaudeThinkingSuffix_UsesUpstreamModelName(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "alias"}
	info := newInfo("x")
	info.UpstreamModelName = "deepseek-v4-pro-max"
	err := applyDeepSeekV4ClaudeThinkingSuffix(info, req)
	require.NoError(t, err)
	assert.Equal(t, "deepseek-v4-pro", req.Model)
	assert.Equal(t, "deepseek-v4-pro", info.UpstreamModelName)
	assert.Equal(t, "max", info.ReasoningEffort)
}

func TestUnimplementedReturnErrors(t *testing.T) {
	a := &Adaptor{}
	_, err := a.ConvertGeminiRequest(nil, nil, nil)
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertImageRequest(nil, nil, dto.ImageRequest{})
	assert.Error(t, err)
	_, err = a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
	// ConvertRerankRequest returns nil,nil (no-op)
	out, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	assert.NoError(t, err)
	assert.Nil(t, out)
}

func TestModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, "deepseek", a.GetChannelName())
	assert.NotEmpty(t, a.GetModelList())
}
