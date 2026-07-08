package main

import (
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 本文件是三种上游格式（OpenAI / Claude / Gemini）共享的生成、计时、错误注入与
// 计数原语。设计不变量：热路径零锁、近零分配——
//   - 随机数走 math/rand/v2 全局函数（无锁）；计数全部 atomic；
//   - 词池预生成为纯 ASCII（无需 JSON 转义），响应手工拼接、流式逐词直写。

// formatStat 单个上游格式的计数（served/stream/errs），/stats 分格式汇总。
type formatStat struct {
	served atomic.Int64
	stream atomic.Int64
	errs   atomic.Int64
}

var (
	reqCounter atomic.Int64 // 全局自增 id，用于响应 id
	activeReqs atomic.Int64 // 在途请求数（对照压测并发，判断请求是否堆在网关）
	statOpenAI formatStat
	statClaude formatStat
	statGemini formatStat
)

// words 预生成的随机词池：每个词为纯小写 ASCII + 尾随空格，可直接嵌入 JSON 字符串。
var words [][]byte

func initWords() {
	const n = 512
	words = make([][]byte, n)
	for i := range words {
		l := 3 + rand.IntN(6)
		b := make([]byte, l+1)
		for j := 0; j < l; j++ {
			b[j] = byte('a' + rand.IntN(26))
		}
		b[l] = ' '
		words[i] = b
	}
}

func randWord() []byte { return words[rand.IntN(len(words))] }

func randDur(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int64N(int64(max-min)))
}

var bufPool = sync.Pool{New: func() any { return new(strings.Builder) }}

// completionWords 返回本次请求的随机补全词数（[-tokens-min, -tokens-max]）。
func completionWords() int {
	span := *tokensMax - *tokensMin
	n := *tokensMin
	if span > 0 {
		n += rand.IntN(span + 1)
	}
	return n
}

// promptTokensFromBody 用请求体长度粗略估算 prompt tokens（约 4 chars/token）。
// 三种格式的 prompt 内容都在 body 里，用长度估算对压测足够（网关自身按真实
// tokenizer 计费，mock 只需给出结构正确、量级合理的 usage）。
func promptTokensFromBody(bodyLen int) int {
	p := bodyLen / 4
	if p < 1 {
		p = 1
	}
	return p
}

// streamInterval 计算流式逐词发送的间隔，使整体时长落在 [ttfb, total]。
func streamInterval(total, ttfb time.Duration, n int) time.Duration {
	if n > 0 && total > ttfb {
		return (total - ttfb) / time.Duration(n+1)
	}
	return 0
}

// streamTiming 返回本次流式响应的 (总时长, 首包延迟)，保证 ttfb<=total。
func streamTiming() (total, ttfb time.Duration) {
	total = randDur(*totalMin, *totalMax)
	ttfb = randDur(*ttfbMin, *ttfbMax)
	if ttfb > total {
		ttfb = total
	}
	return total, ttfb
}

// readBody 读取并限制请求体大小；出错时已写回 400，返回 ok=false。
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// sanitizeModel 防止客户端传入引号/反斜杠破坏手工拼接的 JSON；空值回落到 def。
func sanitizeModel(m, def string) string {
	if m == "" {
		return def
	}
	if strings.ContainsAny(m, "\"\\") {
		m = strings.NewReplacer("\"", "", "\\", "").Replace(m)
	}
	return m
}

// maybeErrorCode 按 -error-rate 决定是否注入错误；返回 (code, true) 表示注入。
// 决策共享，各格式各自按自己的错误 JSON 形态写回（见 *Error 函数）。
func maybeErrorCode() (int, bool) {
	if *errorRate <= 0 || len(errorCodes) == 0 || rand.Float64() >= *errorRate {
		return 0, false
	}
	return errorCodes[rand.IntN(len(errorCodes))], true
}
