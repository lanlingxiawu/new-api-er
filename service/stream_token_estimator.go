package service

import (
	"math"
	"strings"
	"unicode"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// InitStreamReceivedEstimator binds per-attempt estimation without retaining response text.
// Realtime calls this again after replacing the round session; SDK retries invoke the factory.
func InitStreamReceivedEstimator(info *relaycommon.RelayInfo) {
	info.StreamSession.SetReceivedEstimator(func() func(string, int) int {
		var estimator StreamTokenEstimator
		return func(text string, media int) int {
			return estimator.Add(info.UpstreamModelName, text) + estimator.AddMedia(media)
		}
	})
}

// StreamTokenEstimator 保存一轮已交付文本的词类及小数权重，不保存正文；由写入所有者串行调用。
// 零值可用，首次非空 Add 才确定模型权重，避免早于渠道模型映射。
type StreamTokenEstimator struct {
	weights multipliers // 首次交付时的模型权重快照，不跨请求共享可变状态。
	ready   bool        // 是否已经读取过权重，空分片不初始化。
	word    uint8       // 前一字符所属词类：0 分隔符、1 字母、2 数字。
	weight  float64     // 累计未取整权重，保持与单次估算相同的字符处理顺序。
	tokens  int         // 上次已经交付给调用者的累计整数估算。

	scrub DataURLScrubber // base64 媒体不按字符计费，改按张折算。
	media int             // 本轮已确认剥离的媒体数量。
}

// Add 接收模型 model 和完整 JSON 解码后的已交付文本 text，返回本批新增估算 token，而非累计值。
// 后续分片延续词类、小数与首次模型权重；每次尝试/轮次须新建零值实例，不额外加分片起步费用。
func (e *StreamTokenEstimator) Add(model, text string) int {
	if text == "" {
		return 0
	}
	if !e.ready {
		provider := OpenAI
		lower := strings.ToLower(model)
		if strings.Contains(lower, "gemini") {
			provider = Gemini
		} else if strings.Contains(lower, "claude") {
			provider = Claude
		}
		e.weights = getMultipliers(provider)
		e.ready = true
	}
	clean, media := e.scrub.Feed(text)
	if media > 0 {
		e.media += media
		e.word = 0 // 剥离区两侧不属于同一个词，避免把断点当成词内延续。
	}
	text = clean
	m := e.weights
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			e.word = 0
			if r == '\n' || r == '\t' {
				e.weight += m.Newline
			} else {
				e.weight += m.Space
			}
		case isCJK(r):
			e.word = 0
			e.weight += m.CJK
		case isEmoji(r):
			e.word = 0
			e.weight += m.Emoji
		case isLatinOrNumber(r):
			word := uint8(1)
			if unicode.IsNumber(r) {
				word = 2
			}
			if e.word != word {
				if word == 2 {
					e.weight += m.Number
				} else {
					e.weight += m.Word
				}
				e.word = word
			}
		default:
			e.word = 0
			switch {
			case isMathSymbol(r):
				e.weight += m.MathSymbol
			case r == '@':
				e.weight += m.AtSign
			case isURLDelim(r):
				e.weight += m.URLDelim
			default:
				e.weight += m.Symbol
			}
		}
	}
	total := int(math.Ceil(e.weight)) + m.BasePad + e.media*DataURLMediaTokens
	delta := total - e.tokens
	e.tokens = total
	return delta
}

// AddMedia 接收本批原生内联媒体分片数 n，返回本批新增估算 token。
// 与 Add 共用同一份媒体累计，因此同一张图无论以 data URL 还是 inlineData 形态出现都只计一次；
// 两种形态按下游协议互斥（转换后走文本通道才有 data URL，原生协议才有 inlineData）。
func (e *StreamTokenEstimator) AddMedia(n int) int {
	if n <= 0 {
		return 0
	}
	e.media += n
	added := n * DataURLMediaTokens
	e.tokens += added
	return added
}
