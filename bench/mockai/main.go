// Command mockai 是用于压测 new-api 网关的 OpenAI 兼容 mock 上游。
//
// 设计目标：mock 自身绝不能成为压测瓶颈——
//   - 热路径零锁：math/rand/v2 全局函数无锁、计数全部 atomic；
//   - 近零分配：响应 JSON 用 sync.Pool 缓冲区 + 预生成词池手工拼接（词池为纯 ASCII，
//     无需 JSON 转义），流式 chunk 复用同一前缀字节串；
//   - 延迟全部用 time.Sleep 模拟，挂起的 goroutine 开销极小，数万并发连接无压力。
//
// 响应时间（TTFB / 总时长）与内容（词数、词面）均在配置范围内随机。
// 仅压测用途，不属于业务代码。用法见 bench/README.md。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	port      = flag.Int("port", 18080, "监听端口")
	ttfbMin   = flag.Duration("ttfb-min", 50*time.Millisecond, "流式：首 chunk 最小延迟")
	ttfbMax   = flag.Duration("ttfb-max", 300*time.Millisecond, "流式：首 chunk 最大延迟")
	totalMin  = flag.Duration("latency-min", 300*time.Millisecond, "响应总时长下限")
	totalMax  = flag.Duration("latency-max", 2000*time.Millisecond, "响应总时长上限")
	tokensMin = flag.Int("tokens-min", 20, "随机补全词数下限")
	tokensMax = flag.Int("tokens-max", 200, "随机补全词数上限")
	modelsCSV = flag.String("models", "gpt-4o-mini,gpt-4o,gpt-3.5-turbo", "/v1/models 暴露的模型列表")
	errorRate = flag.Float64("error-rate", 0, "随机返回错误状态码的概率 [0,1]，0 关闭")
	errorsCSV = flag.String("error-codes", "429:1,500:1,502:1,503:1", "错误码权重表 code:weight,...（避免 401/403，网关可能据此自动禁用渠道）")
)

var (
	reqCounter  atomic.Int64
	activeReqs  atomic.Int64
	servedTotal atomic.Int64
	streamTotal atomic.Int64
	errorTotal  atomic.Int64
)

// errorCodes 按权重展开后的错误码采样池，rand.IntN 直取即可，无锁。
var errorCodes []int

func initErrorCodes() error {
	for _, part := range strings.Split(*errorsCSV, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cs, ws, ok := strings.Cut(part, ":")
		if !ok {
			ws = "1"
			cs = part
		}
		code, err := strconv.Atoi(strings.TrimSpace(cs))
		if err != nil || code < 400 || code > 599 {
			return fmt.Errorf("invalid error code %q", part)
		}
		weight, err := strconv.Atoi(strings.TrimSpace(ws))
		if err != nil || weight < 1 {
			return fmt.Errorf("invalid weight %q", part)
		}
		for i := 0; i < weight; i++ {
			errorCodes = append(errorCodes, code)
		}
	}
	return nil
}

var errorMessages = map[int]string{
	429: "Rate limit reached for requests",
	500: "The server had an error while processing your request",
	502: "Bad gateway",
	503: "The engine is currently overloaded, please try again later",
}

func errorResponse(w http.ResponseWriter) {
	code := errorCodes[rand.IntN(len(errorCodes))]
	msg, ok := errorMessages[code]
	if !ok {
		msg = "mock injected error"
	}
	// 错误也带一点延迟，更接近真实上游故障形态
	time.Sleep(randDur(*ttfbMin, *ttfbMax))
	errorTotal.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":{"message":"%s","type":"mock_injected_error","code":%d}}`, msg, code)
}

// words 预生成的随机词池。每个词为纯小写 ASCII + 尾随空格，可直接嵌入 JSON 字符串。
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

type chatRequest struct {
	Model         string `json:"model"`
	Stream        bool   `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

// sanitizeModel 防止客户端传入引号/反斜杠破坏手工拼接的 JSON。
func sanitizeModel(m string) string {
	if m == "" {
		return "gpt-4o-mini"
	}
	if strings.ContainsAny(m, `"\`) {
		m = strings.NewReplacer(`"`, "", `\`, "").Replace(m)
	}
	return m
}

func chatHandler(w http.ResponseWriter, r *http.Request) {
	activeReqs.Add(1)
	defer activeReqs.Add(-1)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Model = sanitizeModel(req.Model)

	// 随机错误注入
	if *errorRate > 0 && len(errorCodes) > 0 && rand.Float64() < *errorRate {
		errorResponse(w)
		return
	}

	promptTokens := len(body) / 4
	if promptTokens < 1 {
		promptTokens = 1
	}
	span := *tokensMax - *tokensMin
	n := *tokensMin
	if span > 0 {
		n += rand.IntN(span + 1)
	}
	id := reqCounter.Add(1)
	created := time.Now().Unix()
	servedTotal.Add(1)

	if req.Stream {
		streamTotal.Add(1)
		streamResponse(w, req, id, created, promptTokens, n)
		return
	}
	nonStreamResponse(w, req, id, created, promptTokens, n)
}

func nonStreamResponse(w http.ResponseWriter, req chatRequest, id, created int64, promptTokens, n int) {
	time.Sleep(randDur(*totalMin, *totalMax))

	buf := bufPool.Get().(*strings.Builder)
	buf.Reset()
	buf.WriteString(`{"id":"chatcmpl-mock`)
	buf.WriteString(strconv.FormatInt(id, 10))
	buf.WriteString(`","object":"chat.completion","created":`)
	buf.WriteString(strconv.FormatInt(created, 10))
	buf.WriteString(`,"model":"`)
	buf.WriteString(req.Model)
	buf.WriteString(`","choices":[{"index":0,"message":{"role":"assistant","content":"`)
	for i := 0; i < n; i++ {
		buf.Write(randWord())
	}
	buf.WriteString(`"},"finish_reason":"stop"}],"usage":{"prompt_tokens":`)
	buf.WriteString(strconv.Itoa(promptTokens))
	buf.WriteString(`,"completion_tokens":`)
	buf.WriteString(strconv.Itoa(n))
	buf.WriteString(`,"total_tokens":`)
	buf.WriteString(strconv.Itoa(promptTokens + n))
	buf.WriteString(`}}`)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = io.WriteString(w, buf.String())
	bufPool.Put(buf)
}

func streamResponse(w http.ResponseWriter, req chatRequest, id, created int64, promptTokens, n int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	total := randDur(*totalMin, *totalMax)
	ttfb := randDur(*ttfbMin, *ttfbMax)
	if ttfb > total {
		ttfb = total
	}
	time.Sleep(ttfb)

	// 本次请求所有 chunk 共用的帧前缀（id/created/model 不变）。
	hb := bufPool.Get().(*strings.Builder)
	hb.Reset()
	hb.WriteString(`data: {"id":"chatcmpl-mock`)
	hb.WriteString(strconv.FormatInt(id, 10))
	hb.WriteString(`","object":"chat.completion.chunk","created":`)
	hb.WriteString(strconv.FormatInt(created, 10))
	hb.WriteString(`,"model":"`)
	hb.WriteString(req.Model)
	hb.WriteString(`","choices":[{"index":0,"delta":{`)
	prefix := hb.String()

	// 首 chunk：role
	_, _ = io.WriteString(w, prefix)
	_, _ = io.WriteString(w, "\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n")
	flusher.Flush()

	var interval time.Duration
	if n > 0 && total > ttfb {
		interval = (total - ttfb) / time.Duration(n+1)
	}
	for i := 0; i < n; i++ {
		if interval > 0 {
			time.Sleep(interval)
		}
		_, _ = io.WriteString(w, prefix)
		_, _ = io.WriteString(w, `"content":"`)
		_, _ = w.Write(randWord())
		_, _ = io.WriteString(w, "\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
	}

	// 结束 chunk
	_, _ = io.WriteString(w, prefix)
	_, _ = io.WriteString(w, "},\"finish_reason\":\"stop\"}]}\n\n")

	// usage chunk（按 OpenAI 语义仅在 include_usage 时发送；不发送时由网关侧自行计数，
	// 同样能压到真实的 tokenizer 逻辑）
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		ub := bufPool.Get().(*strings.Builder)
		ub.Reset()
		ub.WriteString(`data: {"id":"chatcmpl-mock`)
		ub.WriteString(strconv.FormatInt(id, 10))
		ub.WriteString(`","object":"chat.completion.chunk","created":`)
		ub.WriteString(strconv.FormatInt(created, 10))
		ub.WriteString(`,"model":"`)
		ub.WriteString(req.Model)
		ub.WriteString(`","choices":[],"usage":{"prompt_tokens":`)
		ub.WriteString(strconv.Itoa(promptTokens))
		ub.WriteString(`,"completion_tokens":`)
		ub.WriteString(strconv.Itoa(n))
		ub.WriteString(`,"total_tokens":`)
		ub.WriteString(strconv.Itoa(promptTokens + n))
		ub.WriteString("}}\n\n")
		_, _ = io.WriteString(w, ub.String())
		bufPool.Put(ub)
	}

	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flusher.Flush()
	bufPool.Put(hb)
}

func modelsHandler(w http.ResponseWriter, _ *http.Request) {
	type modelItem struct {
		Id      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	items := make([]modelItem, 0, 8)
	for _, m := range strings.Split(*modelsCSV, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			items = append(items, modelItem{Id: m, Object: "model", OwnedBy: "mockai"})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": items})
}

func statsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"served_total":%d,"stream_total":%d,"errors_injected":%d,"active":%d}`,
		servedTotal.Load(), streamTotal.Load(), errorTotal.Load(), activeReqs.Load())
}

func main() {
	flag.Parse()
	if *tokensMax < *tokensMin {
		log.Fatal("tokens-max must be >= tokens-min")
	}
	if *errorRate < 0 || *errorRate > 1 {
		log.Fatal("error-rate must be in [0,1]")
	}
	if err := initErrorCodes(); err != nil {
		log.Fatal(err)
	}
	initWords()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/chat/completions") && r.Method == http.MethodPost:
			chatHandler(w, r)
		case strings.HasSuffix(p, "/models") && r.Method == http.MethodGet:
			modelsHandler(w, r)
		case p == "/stats":
			statsHandler(w, r)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "mockai ok\n")
		}
	})

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(*port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		// WriteTimeout 留 0：流式响应时长由 latency 参数控制
		IdleTimeout: 120 * time.Second,
	}
	log.Printf("mockai listening on :%d  ttfb=[%v,%v] total=[%v,%v] tokens=[%d,%d] error-rate=%.2f codes=%s",
		*port, *ttfbMin, *ttfbMax, *totalMin, *totalMax, *tokensMin, *tokensMax, *errorRate, *errorsCSV)
	log.Fatal(srv.ListenAndServe())
}
