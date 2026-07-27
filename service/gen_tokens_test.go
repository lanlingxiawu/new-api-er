package service

import (
	"encoding/base64"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gen_tokens_test.go covers the deterministic token-counting logic in
// token_estimator.go, token_counter.go, and tokenizer.go.
//
// All token counts asserted here are exact and deterministic:
//   - EstimateToken values are computed from the fixed multiplier tables +
//     math.Ceil (pure arithmetic, no external state).
//   - CountTextToken / getTokenNum values come from the embedded tiktoken BPE
//     data (cl100k / o200k), which is deterministic and requires no network.
// Values were pinned by executing the functions once and recording the stable
// output; the accompanying comment explains the derivation.

// tokEncodersOnce ensures the embedded default encoder is initialized exactly
// once for tests in this file (TestMain does not init encoders). Known models
// resolve their own codec via tokenizer.ForModel, but unknown models fall back
// to defaultTokenEncoder, so we initialize it defensively.
var tokEncodersOnce sync.Once

func tokInit() {
	tokEncodersOnce.Do(func() {
		InitTokenEncoders()
	})
}

func tokNewCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	return c
}

// ---------------------------------------------------------------------------
// token_estimator.go — EstimateToken
// ---------------------------------------------------------------------------

func TestTok_EstimateToken_OpenAI_ASCII(t *testing.T) {
	// OpenAI multipliers: Word=1.02, Number=1.55, Symbol=0.4, URLDelim=1.0,
	// AtSign=2.0, Newline=0.5, Space=0.42; result = ceil(sum) + BasePad(0).
	cases := []struct {
		text string
		want int
		note string
	}{
		{"hello", 2, "one word 1.02 -> ceil = 2"},
		{"hello world", 3, "1.02 + 0.42(space) + 1.02 = 2.46 -> ceil 3"},
		{"123", 2, "one number run 1.55 -> ceil 2"},
		{"abc123", 3, "word 1.02 + number-switch 1.55 = 2.57 -> ceil 3"},
		{"a", 2, "single latin word 1.02 -> ceil 2"},
		{"a b c", 4, "1.02*3 + 0.42*2 = 3.90 -> ceil 4"},
		{"!!!", 2, "3 symbols * 0.4 = 1.2 -> ceil 2"},
		{"user@example.com", 6, "user(1.02)+@(2.0)+example(1.02)+.(0.4)+com(1.02)=5.46 -> ceil 6"},
		{"https://a.com/b?c=1", 14, "mix of words/url-delims/numbers -> ceil 14"},
		{"1+1=2", 7, "1(1.55)+ +(0.4)+1(1.55)+=(1.0 urldelim)+2(1.55)=6.05 -> ceil 7"},
		{"tab\tnew\nline", 5, "words 1.02*3 + tab/newline 0.5*2 = 4.06 -> ceil 5"},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, EstimateToken(OpenAI, tc.text), "text=%q (%s)", tc.text, tc.note)
	}
}

func TestTok_EstimateToken_ProviderWeights(t *testing.T) {
	// CJK weight differs per provider: Claude 1.21, Gemini 0.68, OpenAI 0.85.
	assert.Equal(t, 3, EstimateToken(Claude, "你好"), "2 CJK * 1.21 = 2.42 -> ceil 3")
	assert.Equal(t, 3, EstimateToken(Gemini, "你好世界"), "4 CJK * 0.68 = 2.72 -> ceil 3")
	// Same word text, different Word multipliers -> both ceil to 2 here.
	assert.Equal(t, 2, EstimateToken(Claude, "hello"), "Claude Word 1.13 -> ceil 2")
	assert.Equal(t, 2, EstimateToken(Gemini, "hi"), "Gemini Word 1.15 -> ceil 2")
}

func TestTok_EstimateToken_CharClasses(t *testing.T) {
	// Math symbols use MathSymbol weight (OpenAI 2.68).
	assert.Equal(t, 6, EstimateToken(OpenAI, "∑∫"), "2 math symbols * 2.68 = 5.36 -> ceil 6")
	// Emoji weight (OpenAI 2.12); U+1F600 grinning face is a single emoji rune.
	assert.Equal(t, 3, EstimateToken(OpenAI, "😀"), "1 emoji * 2.12 -> ceil 3")
	// Empty text -> sum 0 -> ceil 0 + BasePad 0.
	assert.Equal(t, 0, EstimateToken(OpenAI, ""), "empty text -> 0")
}

// ---------------------------------------------------------------------------
// token_estimator.go — EstimateTokenByModel (model -> provider routing)
// ---------------------------------------------------------------------------

func TestTok_EstimateTokenByModel_Routing(t *testing.T) {
	// Empty text short-circuits to 0 regardless of model.
	assert.Equal(t, 0, EstimateTokenByModel("gpt-4o", ""))

	// "你好" -> Claude(1.21)=ceil(2.42)=3, Gemini(0.68)=ceil(1.36)=2,
	// OpenAI/default(0.85)=ceil(1.70)=2. This distinguishes all three routes.
	assert.Equal(t, 3, EstimateTokenByModel("claude-3-5-sonnet", "你好"), "claude route")
	assert.Equal(t, 2, EstimateTokenByModel("gemini-1.5-pro", "你好"), "gemini route")
	assert.Equal(t, 2, EstimateTokenByModel("gpt-4o", "你好"), "openai route")
	assert.Equal(t, 2, EstimateTokenByModel("some-random-model", "你好"), "default -> openai weights")

	// Case-insensitive matching (model is lower-cased internally).
	assert.Equal(t, 3, EstimateTokenByModel("CLAUDE-3", "你好"), "uppercase still routes to claude")
	assert.Equal(t, 2, EstimateTokenByModel("Gemini-Pro", "你好"), "mixed case routes to gemini")
}

// ---------------------------------------------------------------------------
// token_estimator.go — getMultipliers
// ---------------------------------------------------------------------------

func TestTok_getMultipliers(t *testing.T) {
	assert.Equal(t, multipliersMap[Gemini], getMultipliers(Gemini))
	assert.Equal(t, multipliersMap[Claude], getMultipliers(Claude))
	assert.Equal(t, multipliersMap[OpenAI], getMultipliers(OpenAI))
	// Unknown / any other provider falls back to the OpenAI table.
	assert.Equal(t, multipliersMap[OpenAI], getMultipliers(Unknown), "unknown -> openai fallback")
	assert.Equal(t, multipliersMap[OpenAI], getMultipliers(Provider("nope")), "arbitrary -> openai fallback")
}

// ---------------------------------------------------------------------------
// token_estimator.go — rune classification predicates
// ---------------------------------------------------------------------------

func TestTok_isCJK(t *testing.T) {
	assert.True(t, isCJK('中'), "Han")
	assert.True(t, isCJK('あ'), "Hiragana in 0x3040-0x30FF")
	assert.True(t, isCJK('가'), "Hangul in 0xAC00-0xD7A3")
	assert.False(t, isCJK('a'), "latin")
	assert.False(t, isCJK('1'), "digit")
	assert.False(t, isCJK(' '), "space")
}

func TestTok_isLatinOrNumber(t *testing.T) {
	assert.True(t, isLatinOrNumber('a'))
	assert.True(t, isLatinOrNumber('Z'))
	assert.True(t, isLatinOrNumber('7'))
	assert.False(t, isLatinOrNumber('!'))
	assert.False(t, isLatinOrNumber(' '))
}

func TestTok_isEmoji(t *testing.T) {
	assert.True(t, isEmoji('😀'), "U+1F600 emoticons")
	assert.True(t, isEmoji(rune(0x2600)), "misc symbols lower bound")
	assert.True(t, isEmoji(rune(0x1F9FF)), "supplemental symbols upper bound")
	assert.False(t, isEmoji('a'))
	assert.False(t, isEmoji(rune(0x25FF)), "just below 0x2600")
}

func TestTok_isMathSymbol(t *testing.T) {
	assert.True(t, isMathSymbol('∑'), "explicit list")
	assert.True(t, isMathSymbol('×'), "explicit list")
	assert.True(t, isMathSymbol(rune(0x2200)), "Mathematical Operators lower bound")
	assert.True(t, isMathSymbol(rune(0x22FF)), "Mathematical Operators upper bound")
	assert.True(t, isMathSymbol(rune(0x2A00)), "Supplemental Mathematical Operators")
	assert.False(t, isMathSymbol('a'))
	assert.False(t, isMathSymbol('+'), "ASCII plus is not classified as math symbol")
}

func TestTok_isURLDelim(t *testing.T) {
	for _, r := range "/:?&=;#%" {
		assert.Truef(t, isURLDelim(r), "%q should be URL delim", r)
	}
	assert.False(t, isURLDelim('a'))
	assert.False(t, isURLDelim('@'), "@ handled separately, not a URL delim")
}

// ---------------------------------------------------------------------------
// token_counter.go / tokenizer.go — CountTextToken + encoder path
// ---------------------------------------------------------------------------

func TestTok_CountTextToken_OpenAI(t *testing.T) {
	tokInit()
	// OpenAI models route through the real tiktoken encoder (deterministic).
	// gpt-3.5-turbo / gpt-4 -> cl100k_base; gpt-4o -> o200k_base.
	assert.Equal(t, 0, CountTextToken("", "gpt-3.5-turbo"), "empty -> 0")
	assert.Equal(t, 2, CountTextToken("hello world", "gpt-3.5-turbo"), "cl100k: 2 tokens")
	assert.Equal(t, 4, CountTextToken("Hello, World!", "gpt-3.5-turbo"), "cl100k: 4 tokens")
	assert.Equal(t, 1, CountTextToken("a", "gpt-3.5-turbo"), "cl100k: single token")
	assert.Equal(t, 4, CountTextToken("The quick brown fox", "gpt-3.5-turbo"), "cl100k: 4 tokens")
	assert.Equal(t, 2, CountTextToken("hello world", "gpt-4o"), "o200k: 2 tokens")
	assert.Equal(t, 2, CountTextToken("hello world", "gpt-4"), "cl100k: 2 tokens")
}

func TestTok_CountTextToken_NonOpenAI(t *testing.T) {
	// Non-OpenAI models bypass the tokenizer and use EstimateTokenByModel.
	// "hello world" via OpenAI-weight estimate = 3 (see EstimateToken test),
	// and claude uses claude weights: 1.13 + 0.39(space) + 1.13 = 2.65 -> 3.
	assert.Equal(t, 3, CountTextToken("hello world", "claude-3-5-sonnet"))
	// Empty stays 0 on the estimate branch too.
	assert.Equal(t, 0, CountTextToken("", "claude-3-5-sonnet"))
	// gemini routes to estimate as well; "你好世界" -> 3 (see routing test).
	assert.Equal(t, 3, CountTextToken("你好世界", "gemini-1.5-pro"))
}

func TestTok_getTokenNum(t *testing.T) {
	tokInit()
	enc := getTokenEncoder("gpt-3.5-turbo")
	require.NotNil(t, enc)
	assert.Equal(t, 0, getTokenNum(enc, ""), "empty text -> 0 (no encoder call)")
	assert.Equal(t, 2, getTokenNum(enc, "hello world"), "cl100k deterministic count")
}

func TestTok_getTokenEncoder_CachingAndFallback(t *testing.T) {
	tokInit()
	// Known model returns a working encoder and is cached (same instance twice).
	e1 := getTokenEncoder("gpt-3.5-turbo")
	e2 := getTokenEncoder("gpt-3.5-turbo")
	require.NotNil(t, e1)
	assert.Same(t, e1, e2, "known model encoder should be cached")

	// Unknown model: tokenizer.ForModel fails -> falls back to defaultTokenEncoder
	// and caches it. Two calls return the same cached fallback instance.
	f1 := getTokenEncoder("totally-unknown-model-xyz")
	f2 := getTokenEncoder("totally-unknown-model-xyz")
	require.NotNil(t, f1, "fallback encoder must be non-nil (InitTokenEncoders ran)")
	assert.Same(t, f1, f2, "fallback encoder should be cached")
	// Fallback still produces a deterministic count.
	assert.Equal(t, 2, getTokenNum(f1, "hello world"))
}

// ---------------------------------------------------------------------------
// token_counter.go — CountTokenInput (type dispatch)
// ---------------------------------------------------------------------------

func TestTok_CountTokenInput(t *testing.T) {
	tokInit()
	model := "gpt-3.5-turbo"

	// string branch
	assert.Equal(t, 2, CountTokenInput("hello world", model), "string -> CountTextToken")

	// []string branch: concatenated then counted -> "helloworld".
	got := CountTokenInput([]string{"hello", "world"}, model)
	assert.Equal(t, CountTextToken("helloworld", model), got, "[]string concatenates then counts")

	// []interface{} branch: fmt-formatted then concatenated.
	gotIface := CountTokenInput([]interface{}{"ab", 12}, model)
	assert.Equal(t, CountTextToken("ab12", model), gotIface, "[]interface{} -> fmt each then count")

	// default branch: non-string scalar formatted via fmt then counted.
	assert.Equal(t, CountTextToken("42", model), CountTokenInput(42, model), "int falls through to fmt path")
}

// ---------------------------------------------------------------------------
// token_counter.go — audio token counting (pure, base64 only, no network)
// ---------------------------------------------------------------------------

func TestTok_CountAudioTokenInput(t *testing.T) {
	// Empty audio -> 0, no error.
	n, err := CountAudioTokenInput("", "pcm16")
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// pcm16: samplesCount = len/2, rate = 24000. 48000 zero bytes -> 24000
	// samples -> duration 1.0s. token = QuotaFromFloat(1/60*100/0.06) = 27.
	b64 := base64.StdEncoding.EncodeToString(make([]byte, 48000))
	n, err = CountAudioTokenInput(b64, "pcm16")
	require.NoError(t, err)
	assert.Equal(t, 27, n, "1s pcm16 input -> 27 tokens")

	// Invalid base64 propagates a decode error.
	_, err = CountAudioTokenInput("!!!not-base64!!!", "pcm16")
	assert.Error(t, err, "invalid base64 should error")
}

func TestTok_CountAudioTokenOutput(t *testing.T) {
	// Empty audio -> 0, no error.
	n, err := CountAudioTokenOutput("", "pcm16")
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// 1.0s pcm16 -> QuotaFromFloat(1/60*200/0.24) = 13.
	b64 := base64.StdEncoding.EncodeToString(make([]byte, 48000))
	n, err = CountAudioTokenOutput(b64, "pcm16")
	require.NoError(t, err)
	assert.Equal(t, 13, n, "1s pcm16 output -> 13 tokens")

	_, err = CountAudioTokenOutput("!!!not-base64!!!", "pcm16")
	assert.Error(t, err, "invalid base64 should error")
}

// ---------------------------------------------------------------------------
// token_counter.go — getImageToken (model classification / short-circuits)
//
// We exercise only the branches that do NOT require decoding an image:
//   - nil / nil-source guards,
//   - the glm-4 special case,
//   - the detail=="low" && !isPatchBased short-circuit, which returns the
//     model-specific baseTokens before any file/network access.
// Patch-based and full tile-decode paths require GetImageConfig (file decode)
// and are intentionally skipped here (not pure logic).
// ---------------------------------------------------------------------------

func tokLowDetailMeta() *types.FileMeta {
	// Any non-nil Source satisfies the nil guard; the low-detail path never
	// dereferences the source contents.
	return &types.FileMeta{
		Source: types.NewURLFileSource("http://example.com/img.png"),
		Detail: "low",
	}
}

func TestTok_getImageToken_Guards(t *testing.T) {
	c := tokNewCtx()
	_, err := getImageToken(c, nil, "gpt-4o", false)
	assert.Error(t, err, "nil fileMeta -> error")

	_, err = getImageToken(c, &types.FileMeta{Source: nil}, "gpt-4o", false)
	assert.Error(t, err, "nil source -> error")
}

func TestTok_getImageToken_GLM4(t *testing.T) {
	c := tokNewCtx()
	m := tokLowDetailMeta()
	// glm-4 is a hard-coded special case returning 1047 before any decode,
	// regardless of detail.
	tok, err := getImageToken(c, m, "glm-4v", false)
	require.NoError(t, err)
	assert.Equal(t, 1047, tok)
}

func TestTok_getImageToken_LowDetailBaseTokens(t *testing.T) {
	c := tokNewCtx()
	// detail=="low" and non-patch-based -> returns model-specific baseTokens.
	cases := []struct {
		model string
		want  int
		note  string
	}{
		{"gpt-4o", 85, "4o family base 85"},
		{"gpt-4.1", 85, "4.1 family base 85"},
		{"gpt-4o-mini", 2833, "4o-mini base 2833"},
		{"gpt-5-chat-latest", 70, "gpt-5 chat base 70"},
		{"gpt-5", 70, "gpt-5 (non mini/nano) base 70"},
		{"o1", 75, "o1 base 75"},
		{"o3", 75, "o3 base 75"},
		{"computer-use-preview", 65, "computer-use base 65"},
		{"unknown-model", 85, "default base 85"},
	}
	for _, tc := range cases {
		m := tokLowDetailMeta()
		got, err := getImageToken(c, m, tc.model, false)
		require.NoErrorf(t, err, "model=%s", tc.model)
		assert.Equalf(t, tc.want, got, "model=%s (%s)", tc.model, tc.note)
	}
}
