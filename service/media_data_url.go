package service

import "strings"

// base64 data URL 的判定与折算参数。固定为常量：媒体折算不接受运行时配置，
// 避免误配把媒体算成免费输出；需要调整时改这里并重新发布。
const (
	// DataURLMediaTokens 与 relay/channel/gemini/relay-gemini.go 的 imageCount*1400 同源。
	DataURLMediaTokens = 1400
	// DataURLMinBase64Len 以下的 base64 段（内联图标、占位图）按原文计入文本，
	// 不折算成一张图，避免把几十字符的 svg 高估成 1400 token。
	DataURLMinBase64Len = 256
	// dataURLHeaderMaxLen 限制 data: 到 ;base64, 之间的长度，超出即判定为普通文本，
	// 防止构造超长 data: 前缀把正文藏在缓冲里逃费。
	dataURLHeaderMaxLen = 128

	dataURLPrefix       = "data:"
	dataURLBase64Marker = ";base64,"
)

type dataURLState uint8

const (
	dataURLStateText   dataURLState = iota // 普通文本，扫描 data: 起点
	dataURLStateHeader                     // 已见 data:，寻找 ;base64,
	dataURLStateBody                       // 已确认 ;base64,，累计 base64 正文长度
	dataURLStateDrop                       // 正文已达阈值并计为媒体，丢弃到终止符
)

// DataURLScrubber 把 base64 data URL 正文从计费文本中剔除，改以张数交给调用方按图片口径折算。
// 零值可用，状态跨分片延续；不保存正文，缓冲上限为 dataURLHeaderMaxLen + DataURLMinBase64Len 字节。
//
// 背景：图片经 Gemini→OpenAI 转换以 ![image](data:...;base64,...) 形态走文本通道下发，
// 按字符估算会把一张图算成数十万 token（实测放大 570–600 倍）。
type DataURLScrubber struct {
	state   dataURLState
	tail    []byte // 文本状态下可能开启 data: 的尾部字节，确认前既不计费也不丢弃
	head    []byte // data: 起的原文，未确认为媒体时整段吐回计费
	bodyLen int    // 当前 base64 正文已见长度
}

// Feed 接收一批已交付文本 chunk，返回应计费的普通文本与本批确认剥离的媒体数量。
// base64 正文不出现在返回值里；chunk 不含 data URL 时返回原字符串，不复制。
func (s *DataURLScrubber) Feed(chunk string) (string, int) {
	if chunk == "" {
		return "", 0
	}
	if s.state == dataURLStateText && len(s.tail) == 0 && !mayStartDataURL(chunk) {
		return chunk, 0
	}

	buf := chunk
	if len(s.tail) > 0 {
		buf = string(s.tail) + chunk
		s.tail = s.tail[:0]
	}

	var out strings.Builder
	media := 0
	for i := 0; i < len(buf); {
		switch s.state {
		case dataURLStateText:
			j := indexPrefixFold(buf[i:], dataURLPrefix)
			if j < 0 {
				keep := dataURLPartialPrefixLen(buf[i:])
				out.WriteString(buf[i : len(buf)-keep])
				s.tail = append(s.tail, buf[len(buf)-keep:]...)
				i = len(buf)
				continue
			}
			out.WriteString(buf[i : i+j])
			s.head = append(s.head[:0], buf[i+j:i+j+len(dataURLPrefix)]...)
			i += j + len(dataURLPrefix)
			s.state = dataURLStateHeader
		case dataURLStateHeader:
			for i < len(buf) {
				c := buf[i]
				if isDataURLTerminator(c) {
					s.abortMedia(&out)
					break
				}
				s.head = append(s.head, c)
				i++
				if c == ',' {
					if hasSuffixFold(s.head, dataURLBase64Marker) {
						s.state = dataURLStateBody
						s.bodyLen = 0
					} else {
						s.abortMedia(&out) // data:text/plain,... 这类非 base64 形态
					}
					break
				}
				if len(s.head) > dataURLHeaderMaxLen {
					s.abortMedia(&out)
					break
				}
			}
		case dataURLStateBody:
			for i < len(buf) {
				c := buf[i]
				if isDataURLTerminator(c) {
					s.abortMedia(&out) // 正文未达阈值，整段按普通文本计费
					break
				}
				s.head = append(s.head, c)
				s.bodyLen++
				i++
				if s.bodyLen >= DataURLMinBase64Len {
					// 达阈值即确认媒体，不等终止符：流在正文中途断开时张数不会丢。
					media++
					s.head = s.head[:0]
					s.bodyLen = 0
					s.state = dataURLStateDrop
					break
				}
			}
		case dataURLStateDrop:
			for i < len(buf) && !isDataURLTerminator(buf[i]) {
				i++
			}
			if i < len(buf) {
				s.state = dataURLStateText // 终止符本身仍按普通文本计费
			}
		}
	}
	return out.String(), media
}

// abortMedia 把尚未确认为媒体的 data: 片段原样交回计费，并回到文本状态。
func (s *DataURLScrubber) abortMedia(out *strings.Builder) {
	out.Write(s.head)
	s.head = s.head[:0]
	s.bodyLen = 0
	s.state = dataURLStateText
}

// mayStartDataURL 判断 chunk 是否可能含 data: 起点，用于跳过绝大多数纯文本分片。
func mayStartDataURL(chunk string) bool {
	return strings.IndexByte(chunk, 'd') >= 0 || strings.IndexByte(chunk, 'D') >= 0
}

// isDataURLTerminator 判断 c 是否结束 data URL；markdown 形态由 ')' 闭合。
// data URL 全为 ASCII，多字节字符的字节都 >= 0x80，不会误命中。
func isDataURLTerminator(c byte) bool {
	switch c {
	case ')', '"', '\'', '`', '<', ']', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// indexPrefixFold 返回 s 中首个不区分大小写匹配 prefix 的位置，prefix 必须为小写。
func indexPrefixFold(s, prefix string) int {
	for i := 0; i+len(prefix) <= len(s); i++ {
		if lowerASCII(s[i]) != prefix[0] {
			continue
		}
		if equalFoldASCII(s[i:i+len(prefix)], prefix) {
			return i
		}
	}
	return -1
}

// dataURLPartialPrefixLen 返回 s 尾部能构成 dataURLPrefix 真前缀的最长长度，
// 这段字节留到下一批再判定，既不提前计费也不丢失。
func dataURLPartialPrefixLen(s string) int {
	limit := len(dataURLPrefix) - 1
	if len(s) < limit {
		limit = len(s)
	}
	for k := limit; k > 0; k-- {
		if equalFoldASCII(s[len(s)-k:], dataURLPrefix[:k]) {
			return k
		}
	}
	return 0
}

func hasSuffixFold(b []byte, suffix string) bool {
	if len(b) < len(suffix) {
		return false
	}
	return equalFoldASCII(string(b[len(b)-len(suffix):]), suffix)
}

// equalFoldASCII 比较等长字符串，lower 必须已是小写。
func equalFoldASCII(s, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := range len(s) {
		if lowerASCII(s[i]) != lower[i] {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
