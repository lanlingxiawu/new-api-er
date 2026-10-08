package service

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tiktoken-go/tokenizer"
)

// tokenizer_test.go covers countTokensBounded (tokenizer.go): the segmented
// counter that keeps tiktoken BPE linear on adversarial input while returning
// exactly the whole-text count for normal text.

func allTestCodecs(t testing.TB) map[string]tokenizer.Codec {
	t.Helper()
	out := map[string]tokenizer.Codec{}
	for _, enc := range []tokenizer.Encoding{tokenizer.Cl100kBase, tokenizer.O200kBase, tokenizer.P50kBase, tokenizer.R50kBase} {
		c, err := tokenizer.Get(enc)
		require.NoError(t, err)
		out[string(enc)] = c
	}
	return out
}

// directCount is the pre-fix behaviour: the whole text in one Codec.Count call.
func directCount(enc tokenizer.Codec, text string) int {
	n, _ := enc.Count(text)
	return n
}

const sampleEnglish = `The quick brown fox jumps over the lazy dog. In 2024, researchers at the
institute published a 37-page report ("Scaling Laws, Revisited") arguing that
data quality matters more than raw parameter count; critics weren't convinced.
Don't forget: it's the user's responsibility to verify outputs -- always!
Q: What's 12,345.67 + 890? A: 13,235.67.

`

const sampleChinese = `人工智能正在深刻改变软件开发的方式。开发者可以借助大语言模型快速生成代码、编写测试，并对复杂的系统进行分析与重构；但与此同时，模型输出的正确性仍然需要人工审查。
在高并发的网关场景中，每一次请求都要经过鉴权、限流、计费和转发，任何一个环节的阻塞都会放大为整体延迟！因此，设计时必须把耗时操作移出主链路（例如异步写日志、批量落库）。
价格：每百万输入令牌 2.5 美元，输出 10 美元；缓存命中按 10% 计费。
`

const sampleJapanese = `東京は日本の首都であり、世界有数の大都市です。春には桜が咲き、多くの観光客が訪れます。「おもてなし」の文化は、海外でも広く知られるようになりました。
`

const sampleCode = "package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\n" +
	"// Sum returns the sum of xs.\nfunc Sum(xs []int) (total int) {\n\tfor _, x := range xs {\n\t\ttotal += x\n\t}\n\treturn total\n}\n\n" +
	"func main() {\n\tnames := []string{\"alice\", \"bob\", \"carol\"}\n\tfmt.Println(strings.Join(names, \", \"), Sum([]int{1, 2, 3}))\n\tif len(names) > 2 && names[0] != \"\" {\n\t\tfmt.Printf(\"%d users: %v\\n\", len(names), names)\n\t}\n}\n\n" +
	"def fib(n: int) -> int:\n    \"\"\"Return the n-th Fibonacci number.\"\"\"\n    a, b = 0, 1\n    for _ in range(n):\n        a, b = b, a + b\n    return a\n\n" +
	`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"temperature":0.7,"stream":true}` + "\n" +
	"SELECT id, name FROM users WHERE created_at >= '2024-01-01' ORDER BY id DESC LIMIT 20;\n\n"

// sampleTricky packs the constructs the split patterns treat specially:
// contractions, punctuation followed by newlines, combining marks, multiple
// spaces, space-before-newline, long digit runs, tabs, CamelCase (o200k),
// slashes (o200k absorbs '/' after punctuation), emoji and several scripts.
const sampleTricky = "it's I'd we'll THEY'RE don't\n!!\n。\nfoo \nbar  baz   qux\t\ttab\r\nCRLF\n" +
	"áb́c école ÉCOLE élève naïve résumé\n" +
	"12345678901234567890 1,000,000 3.14159 v2.0.1\n" +
	"HelloWorld camelCase XMLHttpRequest iPhone\n" +
	"https://example.com/a/b?c=d&e=f#frag path/to/file.go !/ ?/\n" +
	"😀😀 🚀 ✅ — “quoted” ‘single’ …\n" +
	"हिन्दी भाषा العربية لغة 한국어 문장 ไทย ภาษา\n" +
	"end."

func repeatToSize(s string, size int) string {
	var b strings.Builder
	for b.Len() < size {
		b.WriteString(s)
	}
	return b.String()
}

func TestTokenizeBounded_EqualsWholeTextOnRealisticText(t *testing.T) {
	codecs := allTestCodecs(t)
	corpus := map[string]string{
		"english":  sampleEnglish,
		"chinese":  sampleChinese,
		"japanese": sampleJapanese,
		"code":     sampleCode,
		"tricky":   sampleTricky,
		"mixed":    sampleEnglish + sampleChinese + sampleCode + sampleJapanese + sampleTricky,
	}
	// Sizes straddle the fast path (<= tokenizeMaxRunBytes) and force several
	// segment cuts (> tokenizeMaxSegmentBytes).
	sizes := []int{1, tokenizeMaxRunBytes, tokenizeMaxRunBytes + 1, 4 << 10, tokenizeMaxSegmentBytes + 1, 100 << 10}
	for name, base := range corpus {
		for _, size := range sizes {
			text := repeatToSize(base, size)
			if size < len(base) {
				text = strings.ToValidUTF8(base[:size], "")
			}
			for encName, enc := range codecs {
				t.Run(fmt.Sprintf("%s/%d/%s", name, size, encName), func(t *testing.T) {
					assert.Equal(t, directCount(enc, text), countTokensBounded(enc, text))
				})
			}
		}
	}
}

// Every position isSafeTokenBoundary accepts must split the count exactly.
// This is the property the segmenter relies on to stay exact on normal text.
func assertSafeBoundariesExact(t *testing.T, enc tokenizer.Codec, encName, text string) int {
	t.Helper()
	whole := directCount(enc, text)
	checked := 0
	prevClass := tokenClassNone
	for i, r := range text {
		class := classifyTokenRune(r)
		if i > 0 && isSafeTokenBoundary(prevClass, r, class) {
			checked++
			got := directCount(enc, text[:i]) + directCount(enc, text[i:])
			if !assert.Equal(t, whole, got, "%s: cut at %d in %q", encName, i, text) {
				return checked
			}
		}
		prevClass = class
	}
	return checked
}

func TestTokenizeBounded_SafeBoundaryIsExact(t *testing.T) {
	codecs := allTestCodecs(t)
	texts := []string{sampleTricky, sampleEnglish, sampleChinese, sampleJapanese, sampleCode}
	for encName, enc := range codecs {
		for ti, text := range texts {
			checked := assertSafeBoundariesExact(t, enc, encName, text)
			require.Greater(t, checked, 5, "%s text#%d should contain safe boundaries", encName, ti)
		}
	}
}

// Same property on random strings over an alphabet of the characters the
// split patterns treat specially (apostrophe + contraction letters incl.
// case-folding look-alikes, CR/LF, '/', combining marks, exotic whitespace,
// digits, symbols, CJK, emoji, invalid UTF-8). Fixed seed: deterministic.
func TestTokenizeBounded_SafeBoundaryIsExact_HostileAlphabet(t *testing.T) {
	alphabet := []string{
		"'", "s", "S", "t", "re", "ve", "m", "ll", "d", "D", string(rune(0x017F)), string(rune(0x212A)),
		"a", "Z", "e", string(rune(0x0301)), string(rune(0x0308)), string(rune(0x4E2D)), string(rune(0x6587)),
		"0", "7", "42", string(rune(0x0663)), string(rune(0x216B)),
		" ", "  ", "\t", "\n", "\r", "\r\n", string(rune(0x00A0)), string(rune(0x3000)), string(rune(0x2028)), string(rune(0x0085)), "\x1c", "\x0b",
		".", ",", "!", "/", "-", "\"", string(rune(0xFF0C)), string(rune(0x3002)), string(rune(0x2019)), "$", "+",
		string(rune(0x1F600)), string(rune(0x200B)), "\xff", "\xc3",
	}
	rng := rand.New(rand.NewSource(20260930))
	codecs := allTestCodecs(t)
	for n := 0; n < 3000; n++ {
		var b strings.Builder
		for k := 0; k < 4+rng.Intn(28); k++ {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		text := b.String()
		for encName, enc := range codecs {
			assertSafeBoundariesExact(t, enc, encName, text)
		}
		if t.Failed() {
			return
		}
	}
}

func TestTokenizeBounded_IsSafeTokenBoundary(t *testing.T) {
	const mark = rune(0x0301)
	cases := []struct {
		name      string
		prev, cur rune
		want      bool
	}{
		{"start of text", 0, 'a', false},
		{"space then space", ' ', ' ', false},
		{"newline then letter", '\n', 'a', false},
		{"letter then space", 'a', ' ', true},
		{"punct then space", '!', ' ', true},
		{"mark then space", mark, ' ', true},
		{"letter then tab", 'a', '\t', true},
		{"punct then ideographic space", '!', 0x3000, true},
		{"letter then newline", 'a', '\n', true},
		{"digit then CR", '9', '\r', true},
		{"cjk then newline", 0x6587, '\n', true},
		{"punct then newline (absorbed by punct piece)", '!', '\n', false},
		{"cjk punct then newline", 0x3002, '\n', false},
		{"mark then newline", mark, '\n', false},
		{"letter then punct", 'a', '!', true},
		{"cjk then cjk punct", 0x6587, 0xFF0C, true},
		{"letter then apostrophe (o200k contraction)", 't', '\'', false},
		{"letter then letter", 'a', 'b', false},
		{"letter then mark", 'e', mark, false},
		{"letter then digit", 'a', '1', true},
		{"digit then digit", '1', '2', false},
		{"digit then letter", '1', 'a', true},
		{"digit then mark", '1', mark, true},
		{"digit then punct", '1', ',', true},
		{"punct then digit", ',', '1', true},
		{"mark then digit", mark, '1', true},
		{"punct then letter (prefix of a letter piece)", '!', 'a', false},
		{"apostrophe then letter (contraction)", '\'', 's', false},
		{"punct then punct", '!', '?', false},
		{"punct then mark", '.', mark, false},
		{"mark then punct", mark, '!', false},
		{"mark then letter", mark, 'a', false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prevClass := tokenClassNone
			if tc.prev != 0 {
				prevClass = classifyTokenRune(tc.prev)
			}
			assert.Equal(t, tc.want, isSafeTokenBoundary(prevClass, tc.cur, classifyTokenRune(tc.cur)))
		})
	}
}

func TestTokenizeBounded_ClassifyTokenRune(t *testing.T) {
	cases := map[rune]tokenCharClass{
		'a': tokenClassLetter, 'Z': tokenClassLetter, 0x00E9: tokenClassLetter, 0x4E2D: tokenClassLetter,
		0x0301: tokenClassMark, 0x093F: tokenClassMark,
		'0': tokenClassDigit, 0x0663: tokenClassDigit, 0x216B: tokenClassDigit,
		' ': tokenClassSpace, '\t': tokenClassSpace, '\n': tokenClassSpace, '\r': tokenClassSpace, '\v': tokenClassSpace, '\f': tokenClassSpace,
		0x3000: tokenClassSpace, 0x00A0: tokenClassSpace, 0x0085: tokenClassSpace,
		'!': tokenClassOther, '\'': tokenClassOther, 0xFF0C: tokenClassOther, 0x1F600: tokenClassOther, 0x00: tokenClassOther, 0x1C: tokenClassOther,
	}
	for r, want := range cases {
		assert.Equal(t, want, classifyTokenRune(r), "%U", r)
	}
}

// Stretches without a safe boundary are force-cut. The count may drift by
// about one token per cut; the drift must stay small relative to the exact
// count.
func pathologicalInputs(size int) map[string]string {
	cjk := string([]rune{0x4E2D, 0x6587, 0x5B57, 0x7B26, 0x6D4B, 0x8BD5})
	return map[string]string{
		"letters":      strings.Repeat("a", size),
		"mixed-case":   repeatToSize("aB", size),
		"digits":       strings.Repeat("7", size),
		"spaces":       strings.Repeat(" ", size),
		"newlines":     strings.Repeat("\n", size),
		"punct":        strings.Repeat("!", size),
		"cjk-no-punct": repeatToSize(cjk, size),
		"base64-like":  repeatToSize("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo", size),
		// One cl100k punctuation piece built from alternating classes.
		"punct-mark": repeatToSize("."+string(rune(0x0301)), size),
		// One o200k punctuation piece via the [\r\n/]* suffix.
		"punct-newline-slash": "." + repeatToSize("\n/", size),
		"contractions":        repeatToSize("'s'S't", size),
	}
}

func TestTokenizeBounded_PathologicalRunsStayClose(t *testing.T) {
	codecs := allTestCodecs(t)
	for name, text := range pathologicalInputs(12000) {
		for encName, enc := range codecs {
			t.Run(name+"/"+encName, func(t *testing.T) {
				want := directCount(enc, text)
				got := countTokensBounded(enc, text)
				t.Logf("exact %d, bounded %d", want, got)
				cuts := len(text)/tokenizeMaxRunBytes + 1
				assert.LessOrEqual(t, math.Abs(float64(got-want)), float64(cuts), "at most ~1 token per cut")
				assert.LessOrEqual(t, math.Abs(float64(got-want))/float64(want), 0.05, "want %d got %d", want, got)
			})
		}
	}
}

// 1 MiB of each pathological input took minutes or more before (quadratic
// BPE); it must now cost well under a second. The bound is loose to stay
// stable on a loaded machine while still failing by orders of magnitude on a
// regression.
func TestTokenizeBounded_PathologicalMegabyteIsFast(t *testing.T) {
	codecs := allTestCodecs(t)
	for name, text := range pathologicalInputs(1 << 20) {
		for _, encName := range []string{string(tokenizer.Cl100kBase), string(tokenizer.O200kBase)} {
			enc := codecs[encName]
			start := time.Now()
			n := countTokensBounded(enc, text)
			elapsed := time.Since(start)
			t.Logf("%s/%s: %d tokens in %s", name, encName, n, elapsed)
			assert.Greater(t, n, 0)
			assert.Less(t, elapsed, 5*time.Second, "%s/%s: 1 MiB took %s", name, encName, elapsed)
		}
	}
}

// Past tokenizeMaxExactBytes the tail is extrapolated: cost stops growing (the
// 33 MB body from the incident) and the estimate stays close for uniform text.
func TestTokenizeBounded_ExtrapolatesBeyondExactCap(t *testing.T) {
	enc := getTokenEncoder("gpt-4o")

	t.Run("unbroken 32MiB is bounded", func(t *testing.T) {
		text := strings.Repeat("a", 32<<20)
		start := time.Now()
		got := countTokensBounded(enc, text)
		elapsed := time.Since(start)
		prefix := countTokensBounded(enc, text[:tokenizeMaxExactBytes])
		assert.InDelta(t, float64(prefix)*4, float64(got), float64(prefix)*4*0.01)
		assert.Less(t, elapsed, 20*time.Second, "32 MiB unbroken input took %s", elapsed)
	})

	t.Run("normal text just over the cap", func(t *testing.T) {
		text := repeatToSize(sampleEnglish+sampleChinese+sampleCode, tokenizeMaxExactBytes+(1<<20))
		want := directCount(enc, text)
		got := countTokensBounded(enc, text)
		t.Logf("exact %d, extrapolated %d", want, got)
		assert.InDelta(t, float64(want), float64(got), float64(want)*0.01)
	})

	t.Run("normal text at the cap is exact", func(t *testing.T) {
		text := repeatToSize(sampleEnglish, tokenizeMaxExactBytes)[:tokenizeMaxExactBytes]
		assert.Equal(t, directCount(enc, text), countTokensBounded(enc, text))
	})
}

func TestTokenizeBounded_GetTokenNumRoutesThroughBoundedCounter(t *testing.T) {
	tokInit()
	enc := getTokenEncoder("gpt-4o")
	assert.Equal(t, 0, getTokenNum(enc, ""))
	text := strings.Repeat("x", 4*tokenizeMaxRunBytes)
	assert.Equal(t, countTokensBounded(enc, text), getTokenNum(enc, text))
	assert.Equal(t, countTokensBounded(enc, text), CountTextToken(text, "gpt-4o"))
}

// ---------------------------------------------------------------------------
// Benchmarks
//
//	go test ./service -run '^$' -bench 'Tokenize' -benchtime 3x
//
// *_Direct is the pre-fix path (whole text in one Codec.Count).
// ---------------------------------------------------------------------------

func benchCount(b *testing.B, text string, direct bool) {
	enc := getTokenEncoder("gpt-4o")
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if direct {
			directCount(enc, text)
		} else {
			countTokensBounded(enc, text)
		}
	}
}

func BenchmarkTokenizeNormalPrompt(b *testing.B) {
	prompts := map[string]string{
		"english-2KB": repeatToSize(sampleEnglish, 2<<10),
		"chinese-2KB": repeatToSize(sampleChinese, 2<<10),
		"code-8KB":    repeatToSize(sampleCode, 8<<10),
		"mixed-64KB":  repeatToSize(sampleEnglish+sampleChinese+sampleCode, 64<<10),
		"short-100B":  sampleEnglish[:100],
	}
	for name, text := range prompts {
		b.Run(name+"/Direct", func(b *testing.B) { benchCount(b, text, true) })
		b.Run(name+"/Bounded", func(b *testing.B) { benchCount(b, text, false) })
	}
}

func BenchmarkTokenizeUnbroken(b *testing.B) {
	for _, size := range []int{20 << 10, 40 << 10, 80 << 10} {
		text := strings.Repeat("a", size)
		b.Run(fmt.Sprintf("%dKB/Direct", size>>10), func(b *testing.B) { benchCount(b, text, true) })
		b.Run(fmt.Sprintf("%dKB/Bounded", size>>10), func(b *testing.B) { benchCount(b, text, false) })
	}
	for _, size := range []int{1 << 20, 2 << 20, 4 << 20, 8 << 20, 10 << 20, 32 << 20} {
		text := strings.Repeat("a", size)
		b.Run(fmt.Sprintf("%dMB/Bounded", size>>20), func(b *testing.B) { benchCount(b, text, false) })
	}
}
