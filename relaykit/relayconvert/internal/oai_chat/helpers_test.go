package oaichat

import (
	"context"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// defaultMediaResolver returns deterministic, network-free fixtures so any
// converter path that resolves media (Claude images/documents, Gemini inline
// data, markdown data-URLs) can run in a pure unit test.
//
// GetBase64Data echoes a mime type derived from the source identifier so tests
// can steer the Claude image-vs-document branch and the Gemini
// supported-mime-type gate purely through fixture inputs:
//   - identifier containing "pdf"  -> "application/pdf"
//   - identifier containing "wav"  -> "audio/wav"
//   - identifier containing "bad"  -> "application/x-unsupported"
//   - otherwise                    -> "image/png"
func defaultMediaResolver() media.MediaResolver {
	return media.MediaResolver{
		GetBase64Data: func(c context.Context, source types.FileSource, reason ...string) (string, string, error) {
			id := ""
			if source != nil {
				id = source.GetIdentifier()
			}
			mime := "image/png"
			switch {
			case containsFold(id, "pdf"):
				mime = "application/pdf"
			case containsFold(id, "wav"):
				mime = "audio/wav"
			case containsFold(id, "bad"):
				mime = "application/x-unsupported"
			}
			return "QkFTRTY0", mime, nil
		},
		DecodeBase64FileData: func(base64String string) (string, string, error) {
			// Mirror the real decoder shape: (format/mime, base64Payload, err).
			return "image/png", "REVDT0RFRA==", nil
		},
	}
}

// media_resolverErr returns a resolver whose functions always fail, for
// exercising error-propagation branches in the converters.
func media_resolverErr() media.MediaResolver {
	return media.MediaResolver{
		GetBase64Data: func(c context.Context, source types.FileSource, reason ...string) (string, string, error) {
			return "", "", errFakeMedia
		},
		DecodeBase64FileData: func(base64String string) (string, string, error) {
			return "", "", errFakeMedia
		},
	}
}

var errFakeMedia = errFake("fake media failure")

type errFake string

func (e errFake) Error() string { return string(e) }

func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	sl := len(s)
	subl := len(sub)
	for i := 0; i+subl <= sl; i++ {
		match := true
		for j := 0; j < subl; j++ {
			cs := s[i+j]
			cb := sub[j]
			if cs >= 'A' && cs <= 'Z' {
				cs += 'a' - 'A'
			}
			if cb >= 'A' && cb <= 'Z' {
				cb += 'a' - 'A'
			}
			if cs != cb {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// setMediaResolver installs a resolver for the duration of a single test and
// restores the default afterwards.
func setMediaResolver(t *testing.T, r media.MediaResolver) {
	t.Helper()
	media.SetMediaResolver(r)
	t.Cleanup(func() { media.SetMediaResolver(defaultMediaResolver()) })
}

func TestMain(m *testing.M) {
	media.SetMediaResolver(defaultMediaResolver())
	os.Exit(m.Run())
}

// claudeMeta 提供 Claude 转换所需的 DefaultMaxTokens 钩子。Claude Messages API
// 强制要求 max_tokens，源请求没带时由该钩子注入（主仓库默认 8192）。
// thinking 适配器同样由 Options 驱动：模型名带 -thinking 后缀时才生效，
// 因此对不带后缀的用例是无影响的。
func claudeMeta() convmeta.Meta {
	return &convmeta.Values{Options: &convmeta.Options{
		Claude: convmeta.ClaudeOptions{
			DefaultMaxTokens:                      func(string) int { return 8192 },
			ThinkingAdapterEnabled:                true,
			ThinkingAdapterBudgetTokensPercentage: 0.8,
		},
	}}
}
