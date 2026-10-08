package service

import (
	"math"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/tiktoken-go/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"
)

// tokenEncoderMap won't grow after initialization
var defaultTokenEncoder tokenizer.Codec

// tokenEncoderMap is used to store token encoders for different models
var tokenEncoderMap = make(map[string]tokenizer.Codec)

// tokenEncoderMutex protects tokenEncoderMap for concurrent access
var tokenEncoderMutex sync.RWMutex

func InitTokenEncoders() {
	common.SysLog("initializing token encoders")
	defaultTokenEncoder = codec.NewCl100kBase()
	common.SysLog("token encoders initialized")
}

func getTokenEncoder(model string) tokenizer.Codec {
	// First, try to get the encoder from cache with read lock
	tokenEncoderMutex.RLock()
	if encoder, exists := tokenEncoderMap[model]; exists {
		tokenEncoderMutex.RUnlock()
		return encoder
	}
	tokenEncoderMutex.RUnlock()

	// If not in cache, create new encoder with write lock
	tokenEncoderMutex.Lock()
	defer tokenEncoderMutex.Unlock()

	// Double-check if another goroutine already created the encoder
	if encoder, exists := tokenEncoderMap[model]; exists {
		return encoder
	}

	// Create new encoder
	modelCodec, err := tokenizer.ForModel(tokenizer.Model(model))
	if err != nil {
		// Cache the default encoder for this model to avoid repeated failures
		tokenEncoderMap[model] = defaultTokenEncoder
		return defaultTokenEncoder
	}

	// Cache the new encoder
	tokenEncoderMap[model] = modelCodec
	return modelCodec
}

func getTokenNum(tokenEncoder tokenizer.Codec, text string) int {
	if text == "" {
		return 0
	}
	return countTokensBounded(tokenEncoder, text)
}

// The tiktoken codec must never see unbounded input. Its pre-tokenizer regex
// can turn a long stretch of text into one piece (a run of letters, digits,
// punctuation or whitespace; also mixes such as punctuation + combining marks),
// and BPE (codec.mergePairs) is O(n²) in the piece length: 80 KB of unbroken
// "a" took ~3 s, and a 33 MB body pinned a core for days without any way to
// cancel it. The regex engine also copies its whole input into a []rune.
// So the text is counted segment by segment:
//
//   - segments end on a "safe" boundary (isSafeTokenBoundary) where no
//     pre-tokenizer piece can span, so the summed count equals the count of
//     the whole text; a segment is closed at the last safe boundary once it
//     reaches tokenizeMaxSegmentBytes;
//   - a stretch of tokenizeMaxRunBytes without any safe boundary is cut where
//     it reaches the limit. Every piece lies between two safe boundaries, so
//     this bounds every piece and makes the total cost linear in the input
//     (~5 MB/s per core, the same as normal text);
//   - only the first tokenizeMaxExactBytes are tokenized; the rest is
//     extrapolated at the tokens-per-byte rate measured on that prefix, so one
//     call costs a few seconds of CPU at most (1.4 s for 8 MiB of "a", up to
//     ~3-6 s for input dense in tokens), whatever the body size.
//
// Only the forced cuts can change the count, by about one token each. Normal
// prose and code have safe boundaries every few bytes; the realistic exception
// is scripts written without spaces or punctuation (≥ ~86 CJK characters in a
// row), where each forced cut may shift the count by ±1. The exact prefix
// (8 MiB) is larger than any model context (1M tokens ≈ 4 MB), so text an
// upstream can accept is never extrapolated.
const (
	tokenizeMaxRunBytes     = 256
	tokenizeMaxSegmentBytes = 16 << 10
	tokenizeMaxExactBytes   = 8 << 20
)

type tokenCharClass uint8

const (
	tokenClassNone tokenCharClass = iota
	tokenClassLetter
	tokenClassMark
	tokenClassDigit
	tokenClassSpace
	tokenClassOther
)

// classifyTokenRune maps a rune onto the classes the split patterns use:
// \p{L}, \p{M}, \p{N}, \s (regexp2's \s is unicode.IsSpace) and the rest.
func classifyTokenRune(r rune) tokenCharClass {
	if r < utf8.RuneSelf {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			return tokenClassLetter
		case r >= '0' && r <= '9':
			return tokenClassDigit
		case r == ' ', r == '\t', r == '\n', r == '\r', r == '\v', r == '\f':
			return tokenClassSpace
		default:
			return tokenClassOther
		}
	}
	switch {
	case unicode.IsLetter(r):
		return tokenClassLetter
	case unicode.Is(unicode.M, r):
		return tokenClassMark
	case unicode.IsNumber(r):
		return tokenClassDigit
	case unicode.IsSpace(r):
		return tokenClassSpace
	default:
		return tokenClassOther
	}
}

// isSafeTokenBoundary reports whether the text may be cut between prev and cur
// without changing any piece produced by the cl100k / o200k / p50k / r50k split
// patterns. None of those patterns looks behind, and their only lookahead
// (\s+(?!\S)) matters only when prev is whitespace, which is never safe. So a
// cut is exact whenever no single piece can contain both prev and cur. The
// pieces that join two non-whitespace characters are: letter runs (o200k also
// folds in marks and an 's / 't ... suffix), an optional non-letter/non-digit
// prefix + letters, digit runs, punctuation/mark/symbol runs plus trailing
// \r\n (o200k: \r\n/), and ' + contraction letters. Everything else is safe:
//
//   - non-whitespace, then whitespace other than \r \n;
//   - letter or digit, then \r / \n (only punctuation pieces absorb them);
//   - letter, then digit, or punctuation other than an apostrophe;
//   - digit, then anything but a digit;
//   - punctuation / symbol / mark, then digit.
func isSafeTokenBoundary(prevClass tokenCharClass, cur rune, curClass tokenCharClass) bool {
	switch prevClass {
	case tokenClassNone, tokenClassSpace:
		return false
	}
	if curClass == tokenClassSpace {
		if cur == '\n' || cur == '\r' {
			return prevClass == tokenClassLetter || prevClass == tokenClassDigit
		}
		return true
	}
	switch prevClass {
	case tokenClassLetter:
		return curClass == tokenClassDigit || (curClass == tokenClassOther && cur != '\'')
	case tokenClassDigit:
		return curClass != tokenClassDigit
	default: // mark, punctuation, symbol
		return curClass == tokenClassDigit
	}
}

// countTokensBounded counts tokens segment by segment (see the constants
// above). Cost is linear in len(text) and capped at tokenizeMaxExactBytes.
func countTokensBounded(tokenEncoder tokenizer.Codec, text string) int {
	if len(text) <= tokenizeMaxRunBytes {
		// No piece can exceed the stretch limit; skip the scan.
		n, _ := tokenEncoder.Count(text)
		return n
	}
	total := 0
	segStart := 0
	lastSafe := -1 // last safe boundary inside the current segment
	lastBreak := 0 // last safe boundary or cut
	prevClass := tokenClassNone
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		class := classifyTokenRune(r)
		if i > segStart && isSafeTokenBoundary(prevClass, r, class) {
			lastSafe = i
			lastBreak = i
		}
		cut := -1
		switch {
		case i-lastBreak >= tokenizeMaxRunBytes:
			// No safe boundary for too long: force a cut to bound the piece.
			cut = i
		case i-segStart >= tokenizeMaxSegmentBytes:
			cut = i
			if lastSafe > segStart {
				cut = lastSafe
			}
		}
		if cut >= 0 {
			n, _ := tokenEncoder.Count(text[segStart:cut])
			total += n
			segStart = cut
			lastSafe = -1
			if lastBreak < segStart {
				lastBreak = segStart
			}
			if segStart >= tokenizeMaxExactBytes {
				// Extrapolate the untokenized tail at the prefix's rate.
				return int(math.Ceil(float64(total) * float64(len(text)) / float64(segStart)))
			}
		}
		prevClass = class
		i += size
	}
	if segStart < len(text) {
		n, _ := tokenEncoder.Count(text[segStart:])
		total += n
	}
	return total
}
