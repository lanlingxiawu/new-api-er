package constant

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPath2RelayMode covers every branch of the prefix/suffix dispatch table,
// including the ordering-sensitive cases (responses/compact before responses,
// embeddings prefix vs. suffix) and the unknown fall-through.
func TestPath2RelayMode_Table(t *testing.T) {
	cases := []struct {
		name string
		path string
		want int
	}{
		// chat completions — both accepted prefixes
		{"v1 chat", "/v1/chat/completions", RelayModeChatCompletions},
		{"pg chat", "/pg/chat/completions", RelayModeChatCompletions},
		{"chat with suffix segments", "/v1/chat/completions/extra", RelayModeChatCompletions},

		{"completions", "/v1/completions", RelayModeCompletions},

		// embeddings — prefix branch AND the suffix fallback branch
		{"embeddings prefix", "/v1/embeddings", RelayModeEmbeddings},
		{"embeddings suffix", "/openai/deployments/x/embeddings", RelayModeEmbeddings},

		{"moderations", "/v1/moderations", RelayModeModerations},
		{"images generations", "/v1/images/generations", RelayModeImagesGenerations},
		{"images edits", "/v1/images/edits", RelayModeImagesEdits},
		{"edits", "/v1/edits", RelayModeEdits},

		// responses/compact must win over the plain responses prefix (order-sensitive)
		{"responses compact", "/v1/responses/compact", RelayModeResponsesCompact},
		{"responses plain", "/v1/responses", RelayModeResponses},

		{"audio speech", "/v1/audio/speech", RelayModeAudioSpeech},
		{"audio transcriptions", "/v1/audio/transcriptions", RelayModeAudioTranscription},
		{"audio translations", "/v1/audio/translations", RelayModeAudioTranslation},

		{"rerank", "/v1/rerank", RelayModeRerank},
		{"realtime", "/v1/realtime", RelayModeRealtime},

		// gemini — both accepted prefixes
		{"gemini v1beta", "/v1beta/models", RelayModeGemini},
		{"gemini v1 models", "/v1/models", RelayModeGemini},

		// /mj delegates to the midjourney dispatcher
		{"mj delegate", "/mj/submit/imagine", RelayModeMidjourneyImagine},

		// unknown fall-through
		{"unknown", "/healthz", RelayModeUnknown},
		{"empty", "", RelayModeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Path2RelayMode(tc.path))
		})
	}
}

// TestPath2RelayMode_EmbeddingsPrefixWins asserts the /v1/embeddings prefix
// branch is reached before the generic "embeddings" suffix branch. Both map to
// the same mode, so this documents the intended short-circuit, not a value diff.
func TestPath2RelayMode_EmbeddingsPrefixWins(t *testing.T) {
	assert.Equal(t, RelayModeEmbeddings, Path2RelayMode("/v1/embeddings"))
}

// TestPath2RelayModeMidjourney covers every suffix branch of the midjourney
// dispatcher, including the two distinct suffixes that both map to
// RelayModeMidjourneyChange, and the unknown fall-through.
func TestPath2RelayModeMidjourney(t *testing.T) {
	cases := []struct {
		name string
		path string
		want int
	}{
		{"action", "/mj/submit/action", RelayModeMidjourneyAction},
		{"modal", "/mj/submit/modal", RelayModeMidjourneyModal},
		{"shorten", "/mj/submit/shorten", RelayModeMidjourneyShorten},
		{"swap face", "/mj/insight-face/swap", RelayModeSwapFace},
		{"upload", "/mj/submit/upload-discord-images", RelayModeMidjourneyUpload},
		{"imagine", "/mj/submit/imagine", RelayModeMidjourneyImagine},
		{"video", "/mj/submit/video", RelayModeMidjourneyVideo},
		{"edits", "/mj/submit/edits", RelayModeMidjourneyEdits},
		{"blend", "/mj/submit/blend", RelayModeMidjourneyBlend},
		{"describe", "/mj/submit/describe", RelayModeMidjourneyDescribe},
		{"notify", "/mj/notify", RelayModeMidjourneyNotify},
		{"change", "/mj/submit/change", RelayModeMidjourneyChange},
		{"simple-change also maps to change", "/mj/submit/simple-change", RelayModeMidjourneyChange},
		{"fetch", "/mj/task/abc/fetch", RelayModeMidjourneyTaskFetch},
		{"image-seed", "/mj/task/abc/image-seed", RelayModeMidjourneyTaskImageSeed},
		{"list-by-condition", "/mj/task/list-by-condition", RelayModeMidjourneyTaskFetchByCondition},
		{"unknown", "/mj/unknown", RelayModeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Path2RelayModeMidjourney(tc.path))
		})
	}
}

// TestPath2RelaySuno covers the method+path decision matrix: POST /fetch,
// GET /fetch/, /submit/, and the unknown fall-through — including the condition
// coverage where method or path individually fails to match.
func TestPath2RelaySuno(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		// POST + /fetch suffix
		{"post fetch", http.MethodPost, "/suno/fetch", RelayModeSunoFetch},
		// method mismatch: GET + /fetch suffix does NOT hit SunoFetch;
		// falls through (no /fetch/ substring, no /submit/) to Unknown
		{"get fetch suffix only", http.MethodGet, "/suno/fetch", RelayModeUnknown},
		// GET + /fetch/ substring
		{"get fetch by id", http.MethodGet, "/suno/fetch/task123", RelayModeSunoFetchByID},
		// path mismatch: POST + /fetch/ substring -> not POST /fetch suffix,
		// not GET, but /submit/ absent -> Unknown
		{"post fetch by id path", http.MethodPost, "/suno/fetch/task123", RelayModeUnknown},
		// /submit/ substring (any method)
		{"submit post", http.MethodPost, "/suno/submit/music", RelayModeSunoSubmit},
		{"submit get", http.MethodGet, "/suno/submit/music", RelayModeSunoSubmit},
		// unknown
		{"unknown", http.MethodGet, "/suno/other", RelayModeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Path2RelaySuno(tc.method, tc.path))
		})
	}
}

// TestRelayModeConstants_AreDistinct is a light sanity guard that the iota
// block did not accidentally collapse two modes to the same value (which would
// silently misroute requests). Not a bare-constant test — it verifies the
// uniqueness invariant the dispatch functions above rely on.
func TestRelayModeConstants_AreDistinct(t *testing.T) {
	all := []int{
		RelayModeUnknown, RelayModeChatCompletions, RelayModeCompletions,
		RelayModeEmbeddings, RelayModeModerations, RelayModeImagesGenerations,
		RelayModeImagesEdits, RelayModeEdits, RelayModeMidjourneyImagine,
		RelayModeMidjourneyDescribe, RelayModeMidjourneyBlend, RelayModeMidjourneyChange,
		RelayModeMidjourneySimpleChange, RelayModeMidjourneyNotify, RelayModeMidjourneyTaskFetch,
		RelayModeMidjourneyTaskImageSeed, RelayModeMidjourneyTaskFetchByCondition,
		RelayModeMidjourneyAction, RelayModeMidjourneyModal, RelayModeMidjourneyShorten,
		RelayModeSwapFace, RelayModeMidjourneyUpload, RelayModeMidjourneyVideo,
		RelayModeMidjourneyEdits, RelayModeAudioSpeech, RelayModeAudioTranscription,
		RelayModeAudioTranslation, RelayModeSunoFetch, RelayModeSunoFetchByID,
		RelayModeSunoSubmit, RelayModeVideoFetchByID, RelayModeVideoSubmit,
		RelayModeRerank, RelayModeResponses, RelayModeRealtime, RelayModeGemini,
		RelayModeResponsesCompact,
	}
	seen := make(map[int]bool, len(all))
	for _, v := range all {
		assert.Falsef(t, seen[v], "duplicate relay mode value %d", v)
		seen[v] = true
	}
}
func TestPath2RelayMode(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{path: "/v1/alpha/search", want: RelayModeAlphaSearch},
		{path: "/v1/alpha/search?foo=1", want: RelayModeAlphaSearch},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, Path2RelayMode(tt.path))
		})
	}
}
