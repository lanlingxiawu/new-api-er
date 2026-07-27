package oairesponses

import (
	"context"
	"errors"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// TestMain installs a deterministic media resolver so that converters that call
// relaymedia.ResolveBase64Data (Claude/Gemini media parts) return fixed fixtures
// instead of performing any network/DB access. The desired mime type / error is
// encoded in the source's raw data via marker substrings (see fakeGetBase64Data).
func TestMain(m *testing.M) {
	media.SetMediaResolver(media.MediaResolver{
		GetBase64Data:        fakeGetBase64Data,
		DecodeBase64FileData: fakeDecodeBase64FileData,
	})
	os.Exit(m.Run())
}

// fakeGetBase64Data returns deterministic (base64, mimeType, err) derived from
// marker substrings in the source identifier/raw data:
//
//	"trigger-error" -> returns an error
//	"pdf"           -> application/pdf   (Claude document branch)
//	"unsupported"   -> application/x-nope (Gemini unsupported-mime branch)
//	"png"           -> image/png         (supported everywhere)
//	otherwise       -> image/jpeg
func fakeGetBase64Data(_ context.Context, source types.FileSource, _ ...string) (string, string, error) {
	raw := source.GetRawData()
	switch {
	case strings.Contains(raw, "trigger-error"):
		return "", "", errors.New("boom: resolver failed")
	case strings.Contains(raw, "pdf"):
		return "cGRmZGF0YQ==", "application/pdf", nil
	case strings.Contains(raw, "unsupported"):
		return "dW5zdXA=", "application/x-nope", nil
	case strings.Contains(raw, "png"):
		return "iVBORw0KAAAA", "image/png", nil
	default:
		return "ZGF0YQ==", "image/jpeg", nil
	}
}

func fakeDecodeBase64FileData(base64String string) (string, string, error) {
	return base64String, "application/octet-stream", nil
}

func newTestGinContext() context.Context {
	return context.Background()
}

// claudeMeta 提供 Claude Messages 必需的 max_tokens 注入钩子。
func claudeMeta() convmeta.Meta {
	return &convmeta.Values{Options: &convmeta.Options{
		Claude: convmeta.ClaudeOptions{DefaultMaxTokens: func(string) int { return 8192 }},
	}}
}

// geminiMeta 提供安全阈值钩子；阈值原先取自全局 GeminiSettings 的 "OFF" 默认值。
func geminiMeta() convmeta.Meta {
	return &convmeta.Values{Options: &convmeta.Options{
		Gemini: convmeta.GeminiOptions{SafetySetting: func(string) string { return "OFF" }},
	}}
}
