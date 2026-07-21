package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestChannelType2APIType(t *testing.T) {
	cases := []struct {
		channel int
		want    int
		ok      bool
	}{
		{constant.ChannelTypeOpenAI, constant.APITypeOpenAI, true},
		{constant.ChannelTypeAnthropic, constant.APITypeAnthropic, true},
		{constant.ChannelTypeGemini, constant.APITypeGemini, true},
		{constant.ChannelTypeAws, constant.APITypeAws, true},
		{constant.ChannelTypeDeepSeek, constant.APITypeDeepSeek, true},
		{constant.ChannelTypeVertexAi, constant.APITypeVertexAi, true},
		// ThirdPartySD2 maps to OpenAI api type (found=true)
		{constant.ChannelTypeThirdPartySD2, constant.APITypeOpenAI, true},
		{constant.ChannelTypeAdvancedCustom, constant.APITypeAdvancedCustom, true},
	}
	for _, tc := range cases {
		got, ok := ChannelType2APIType(tc.channel)
		assert.Equal(t, tc.want, got, "channel=%d", tc.channel)
		assert.Equal(t, tc.ok, ok, "channel=%d", tc.channel)
	}
}

// TestChannelType2APIType_AllCases exercises every case arm of the switch so
// each mapping line is covered and pinned.
func TestChannelType2APIType_AllCases(t *testing.T) {
	pairs := []struct {
		channel int
		api     int
	}{
		{constant.ChannelTypeOpenAI, constant.APITypeOpenAI},
		{constant.ChannelTypeAnthropic, constant.APITypeAnthropic},
		{constant.ChannelTypeBaidu, constant.APITypeBaidu},
		{constant.ChannelTypePaLM, constant.APITypePaLM},
		{constant.ChannelTypeZhipu, constant.APITypeZhipu},
		{constant.ChannelTypeAli, constant.APITypeAli},
		{constant.ChannelTypeXunfei, constant.APITypeXunfei},
		{constant.ChannelTypeAIProxyLibrary, constant.APITypeAIProxyLibrary},
		{constant.ChannelTypeTencent, constant.APITypeTencent},
		{constant.ChannelTypeGemini, constant.APITypeGemini},
		{constant.ChannelTypeZhipu_v4, constant.APITypeZhipuV4},
		{constant.ChannelTypeOllama, constant.APITypeOllama},
		{constant.ChannelTypePerplexity, constant.APITypePerplexity},
		{constant.ChannelTypeAws, constant.APITypeAws},
		{constant.ChannelTypeCohere, constant.APITypeCohere},
		{constant.ChannelTypeDify, constant.APITypeDify},
		{constant.ChannelTypeJina, constant.APITypeJina},
		{constant.ChannelCloudflare, constant.APITypeCloudflare},
		{constant.ChannelTypeSiliconFlow, constant.APITypeSiliconFlow},
		{constant.ChannelTypeVertexAi, constant.APITypeVertexAi},
		{constant.ChannelTypeMistral, constant.APITypeMistral},
		{constant.ChannelTypeDeepSeek, constant.APITypeDeepSeek},
		{constant.ChannelTypeMokaAI, constant.APITypeMokaAI},
		{constant.ChannelTypeVolcEngine, constant.APITypeVolcEngine},
		{constant.ChannelTypeBaiduV2, constant.APITypeBaiduV2},
		{constant.ChannelTypeOpenRouter, constant.APITypeOpenRouter},
		{constant.ChannelTypeXinference, constant.APITypeXinference},
		{constant.ChannelTypeXai, constant.APITypeXai},
		{constant.ChannelTypeCoze, constant.APITypeCoze},
		{constant.ChannelTypeJimeng, constant.APITypeJimeng},
		{constant.ChannelTypeMoonshot, constant.APITypeMoonshot},
		{constant.ChannelTypeSubmodel, constant.APITypeSubmodel},
		{constant.ChannelTypeMiniMax, constant.APITypeMiniMax},
		{constant.ChannelTypeReplicate, constant.APITypeReplicate},
		{constant.ChannelTypeCodex, constant.APITypeCodex},
		{constant.ChannelTypeThirdPartySD2, constant.APITypeOpenAI},
		{constant.ChannelTypeAdvancedCustom, constant.APITypeAdvancedCustom},
	}
	for _, p := range pairs {
		got, ok := ChannelType2APIType(p.channel)
		assert.True(t, ok, "channel=%d", p.channel)
		assert.Equal(t, p.api, got, "channel=%d", p.channel)
	}
}

func TestChannelType2APIType_Unknown(t *testing.T) {
	// An unmapped channel type falls back to OpenAI api type with found=false.
	got, ok := ChannelType2APIType(-999)
	assert.Equal(t, constant.APITypeOpenAI, got)
	assert.False(t, ok)
}
