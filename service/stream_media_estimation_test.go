package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStreamTokenEstimatorStripsBase64Media 断言交付文本里的 base64 媒体按张折算，
// 而不是按字符计费：这是 1,299,013 token / $155.88 那次事故的回归用例。
func TestStreamTokenEstimatorStripsBase64Media(t *testing.T) {
	oneMiB := strings.Repeat("QUJD", (1<<20)/4)
	text := "![image](data:image/png;base64," + oneMiB + ")"

	for _, model := range []string{"gemini-2.5-flash-image", "gpt-4o", "claude-sonnet-4"} {
		t.Run(model, func(t *testing.T) {
			var e StreamTokenEstimator
			got := e.Add(model, text)

			require.Equal(t, EstimateTokenByModel(model, "![image]()")+DataURLMediaTokens, got)
			require.Less(t, got, 2000, "1 MiB 图片不得再按数十万 token 计费")
			// 不少收：不低于渠道适配器按张计价的口径（imageCount * 1400）。
			require.GreaterOrEqual(t, got, DataURLMediaTokens)
		})
	}
}

// TestStreamTokenEstimatorMediaSplitAcrossChunks 断言分片切开 data URL 时估算值与一次性输入一致。
func TestStreamTokenEstimatorMediaSplitAcrossChunks(t *testing.T) {
	body := strings.Repeat("QUJD", 4096)
	// 全 ASCII：按字节切分不会切开多字节字符，切分点差异只反映剥离器状态机。
	text := "see ![image](data:image/png;base64," + body + ") note follows"

	var once StreamTokenEstimator
	expected := once.Add("gemini-2.5-flash-image", text)

	for _, at := range []int{0, 4, 12, 20, 34, 40, len(text) / 2, len(text) - 12, len(text)} {
		t.Run(fmt.Sprint(at), func(t *testing.T) {
			var e StreamTokenEstimator
			got := e.Add("gemini-2.5-flash-image", text[:at])
			got += e.Add("gemini-2.5-flash-image", text[at:])
			require.Equal(t, expected, got)
		})
	}
}

// TestStreamTokenEstimatorMultipleMedia 断言多张图各自折算，不互相吞并。
func TestStreamTokenEstimatorMultipleMedia(t *testing.T) {
	body := strings.Repeat("QUJD", 1024)
	text := "a ![1](data:image/png;base64," + body + ") b ![2](data:image/png;base64," + body + ") c"

	var e StreamTokenEstimator
	got := e.Add("gemini-2.5-flash-image", text)
	require.Equal(t, EstimateTokenByModel("gemini-2.5-flash-image", "a ![1]() b ![2]() c")+2*DataURLMediaTokens, got)
}

// TestStreamTokenEstimatorUnclosedMediaOnStreamCut 断言流在 base64 中途断开时张数不丢。
func TestStreamTokenEstimatorUnclosedMediaOnStreamCut(t *testing.T) {
	var e StreamTokenEstimator
	got := e.Add("gemini-2.5-flash-image", "![image](data:image/png;base64,")
	got += e.Add("gemini-2.5-flash-image", strings.Repeat("QUJD", 2048))

	require.Equal(t, EstimateTokenByModel("gemini-2.5-flash-image", "![image](")+DataURLMediaTokens, got)
}

// TestStreamTokenEstimatorShortDataURLStaysText 断言内联小图标仍按原文计费，不被高估成一张图。
func TestStreamTokenEstimatorShortDataURLStaysText(t *testing.T) {
	text := "icon ![i](data:image/svg+xml;base64," + strings.Repeat("A", 120) + ") done"

	var e StreamTokenEstimator
	got := e.Add("gpt-4o", text)
	require.Equal(t, EstimateTokenByModel("gpt-4o", text), got)
}

// TestStreamTokenEstimatorNoDataURLUnchanged 断言不含 data URL 的文本估算逐字节不变。
func TestStreamTokenEstimatorNoDataURLUnchanged(t *testing.T) {
	for _, text := range []string{
		"普通回复，没有任何媒体。",
		"discussing the data: scheme without any payload",
		"a link https://example.com/x?y=1 and some code `fmt.Println(\"hi\")`",
	} {
		var e StreamTokenEstimator
		require.Equal(t, EstimateTokenByModel("gpt-4o", text), e.Add("gpt-4o", text), text)
	}
}

// TestStreamTokenEstimatorAddMedia 断言原生内联媒体按张折算，且与 data URL 剥离共用同一份累计
// （同一张图不会既按 data URL 又按 inlineData 计两次）。
func TestStreamTokenEstimatorAddMedia(t *testing.T) {
	var e StreamTokenEstimator
	require.Zero(t, e.AddMedia(0))
	require.Zero(t, e.AddMedia(-1))

	textOnly := e.Add("gemini-2.5-flash-image", "here it is")
	require.Equal(t, DataURLMediaTokens, e.AddMedia(1), "一张图的增量就是折算单价")
	require.Equal(t, 2*DataURLMediaTokens, e.AddMedia(2))
	require.Zero(t, e.Add("gemini-2.5-flash-image", ""), "空分片不产生增量")

	// 再补一段文本时，增量只包含新文本，不重复计入已交付的媒体。
	more := e.Add("gemini-2.5-flash-image", " and more text")
	require.Equal(t, EstimateTokenByModel("gemini-2.5-flash-image", "here it is and more text"), textOnly+more)
}

// TestStreamTokenEstimatorMediaFormsDoNotDoubleCount 断言两种媒体形态各计一次。
func TestStreamTokenEstimatorMediaFormsDoNotDoubleCount(t *testing.T) {
	dataURL := "![image](data:image/png;base64," + strings.Repeat("QUJD", 1024) + ")"

	var viaText StreamTokenEstimator
	fromDataURL := viaText.Add("gemini-2.5-flash-image", dataURL)

	var viaParts StreamTokenEstimator
	fromInline := viaParts.Add("gemini-2.5-flash-image", "![image]()") + viaParts.AddMedia(1)

	require.Equal(t, fromDataURL, fromInline, "转换后的 data URL 与原生 inlineData 折算结果一致")
}
