package gemini

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// imageBillingInfo 构造一次 gemini 图像请求的中转上下文，估算 prompt 固定以便断言输出侧数值。
func imageBillingInfo() *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-flash-image",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-2.5-flash-image",
		},
	}
	info.SetEstimatePromptTokens(7)
	return info
}

func geminiImagePart(text string, images int) dto.GeminiChatResponse {
	parts := make([]dto.GeminiPart, 0, images+1)
	if text != "" {
		parts = append(parts, dto.GeminiPart{Text: text})
	}
	for range images {
		parts = append(parts, dto.GeminiPart{
			InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "QUJDRA=="},
		})
	}
	return dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Role: "model", Parts: parts}}},
	}
}

// TestGeminiStreamHandlerImagesBillAdditively 断言缺少 usageMetadata 时图片按张叠加在文本估算之上，
// 而不是只在文本为 0 时替补——否则"文字+图"的响应会把图片白送。
func TestGeminiStreamHandlerImagesBillAdditively(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	const text = "here is the picture you asked for"
	run := func(t *testing.T, chunk dto.GeminiChatResponse) *dto.Usage {
		t.Helper()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		data, err := common.Marshal(chunk)
		require.NoError(t, err)
		resp := &http.Response{Body: io.NopCloser(bytes.NewReader([]byte("data: " + string(data) + "\ndata: [DONE]\n")))}
		usage, apiErr := geminiStreamHandler(c, imageBillingInfo(), resp, func(_ string, _ *dto.GeminiChatResponse) bool { return true })
		require.Nil(t, apiErr)
		require.NotNil(t, usage)
		return usage
	}

	textOnly := run(t, geminiImagePart(text, 0))
	withImage := run(t, geminiImagePart(text, 1))
	twoImages := run(t, geminiImagePart(text, 2))
	imageOnly := run(t, geminiImagePart("", 1))

	require.Greater(t, textOnly.CompletionTokens, 0)
	require.Equal(t, textOnly.CompletionTokens+service.DataURLMediaTokens, withImage.CompletionTokens,
		"一张图必须叠加在文本估算之上")
	require.Equal(t, textOnly.CompletionTokens+2*service.DataURLMediaTokens, twoImages.CompletionTokens)
	require.GreaterOrEqual(t, imageOnly.CompletionTokens, service.DataURLMediaTokens,
		"纯图片响应不得按 0 输出结算")
	for _, usage := range []*dto.Usage{textOnly, withImage, twoImages, imageOnly} {
		require.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
	}
}

// TestGeminiChatHandlerImagesBillAdditively 覆盖非流式：原先这条路径完全不看图片张数。
func TestGeminiChatHandlerImagesBillAdditively(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	const text = "rendered as requested"
	textOnly := buildUsageFromGeminiResponse(c, imageBillingInfo(), func() *dto.GeminiChatResponse {
		r := geminiImagePart(text, 0)
		return &r
	}())
	withImage := buildUsageFromGeminiResponse(c, imageBillingInfo(), func() *dto.GeminiChatResponse {
		r := geminiImagePart(text, 1)
		return &r
	}())
	imageOnly := buildUsageFromGeminiResponse(c, imageBillingInfo(), func() *dto.GeminiChatResponse {
		r := geminiImagePart("", 1)
		return &r
	}())

	require.Greater(t, textOnly.CompletionTokens, 0)
	require.Equal(t, textOnly.CompletionTokens+service.DataURLMediaTokens, withImage.CompletionTokens)
	require.GreaterOrEqual(t, imageOnly.CompletionTokens, service.DataURLMediaTokens)
	require.Equal(t, imageOnly.PromptTokens+imageOnly.CompletionTokens, imageOnly.TotalTokens)
}

// TestGeminiUsageMetadataUnchangedWithImages 断言上游给了确认用量时数值不受图片张数影响（防回归）。
func TestGeminiUsageMetadataUnchangedWithImages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	response := geminiImagePart("confirmed", 2)
	response.UsageMetadata = dto.GeminiUsageMetadata{
		PromptTokenCount:     7,
		CandidatesTokenCount: 1298,
		TotalTokenCount:      1305,
	}

	usage := buildUsageFromGeminiResponse(c, imageBillingInfo(), &response)
	require.Equal(t, 7, usage.PromptTokens)
	require.Equal(t, 1298, usage.CompletionTokens, "有确认用量时不得叠加本地张数折算")
}

// TestGeminiPromptOnlyUsageMetadataAddsImages 覆盖 patchGeminiZeroCompletionUsage：
// 上游只回了 prompt 侧用量（completion=0）时，图片要按张叠加在文本估算之上。
func TestGeminiPromptOnlyUsageMetadataAddsImages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	const text = "here you go"
	build := func(images int) dto.Usage {
		response := geminiImagePart(text, images)
		response.UsageMetadata = dto.GeminiUsageMetadata{PromptTokenCount: 11, TotalTokenCount: 11}
		return buildUsageFromGeminiResponse(c, imageBillingInfo(), &response)
	}

	textOnly := build(0)
	withImage := build(1)

	require.Equal(t, 11, withImage.PromptTokens, "确认的输入量不被改写")
	require.Greater(t, textOnly.CompletionTokens, 0)
	require.Equal(t, textOnly.CompletionTokens+service.DataURLMediaTokens, withImage.CompletionTokens)
	require.Equal(t, withImage.PromptTokens+withImage.CompletionTokens, withImage.TotalTokens)
}
