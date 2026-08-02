package common

import (
	"testing"

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
