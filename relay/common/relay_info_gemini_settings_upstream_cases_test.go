package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
)

func TestRelayInfoConvOptionsUsesNormalizedGeminiSafetySettings(t *testing.T) {
	// 必须走 ReplaceGeminiSettings：GetGeminiSettings 返回的是不可变快照，
	// 通过它写字段不会被后续读者看到。这个用例原本就是靠"跨包拿到全局指针再写"
	// 生效的，而那正是 relay 热路径上读到半个 map 的成因。
	original := *model_setting.GetGeminiSettings()
	t.Cleanup(func() { model_setting.ReplaceGeminiSettings(original) })

	modified := original
	modified.SafetySettings = map[string]string{
		"HARM_CATEGORY_HATE_SPEECH":       "",
		"HARM_CATEGORY_DANGEROUS_CONTENT": "BLOCK_ONLY_HIGH",
	}
	model_setting.ReplaceGeminiSettings(modified)

	options := (&RelayInfo{}).ConvOptions()

	assert.Equal(t, "OFF", options.Gemini.SafetySetting("HARM_CATEGORY_HATE_SPEECH"))
	assert.Equal(t, "BLOCK_ONLY_HIGH", options.Gemini.SafetySetting("HARM_CATEGORY_DANGEROUS_CONTENT"))
}

// TestRelayInfoConvOptionsGeminiJSONSchemaSupport covers which upstreams get
// parametersJsonSchema and responseJsonSchema: every channel except one whose
// Gemini API version for the model is v1, and Vertex AI regardless of it.
func TestRelayInfoConvOptionsGeminiJSONSchemaSupport(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	original := settings.VersionSettings
	t.Cleanup(func() {
		settings.VersionSettings = original
	})
	settings.VersionSettings = map[string]string{"default": "v1beta", "gemini-v1-model": "v1"}

	tests := []struct {
		name        string
		channelType int
		model       string
		want        bool
	}{
		{name: "Gemini on the default version", channelType: constant.ChannelTypeGemini, model: "gemini-3.7-flash", want: true},
		{name: "Gemini on v1", channelType: constant.ChannelTypeGemini, model: "gemini-v1-model", want: false},
		{name: "Vertex AI", channelType: constant.ChannelTypeVertexAi, model: "gemini-v1-model", want: true},
		{name: "other channel on v1", channelType: constant.ChannelTypeAdvancedCustom, model: "gemini-v1-model", want: false},
		{name: "other channel on the default version", channelType: constant.ChannelTypeAdvancedCustom, model: "gemini-3.7-flash", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &RelayInfo{ChannelMeta: &ChannelMeta{ChannelType: tt.channelType}}

			assert.Equal(t, tt.want, info.ConvOptions().Gemini.SupportsJSONSchemaModel(tt.model))
		})
	}
}
