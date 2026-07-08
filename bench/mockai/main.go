// Command mockai 是用于压测 new-api 网关的多格式 mock 上游。
//
// 按渠道接入的三种原生上游格式同时模拟，一个进程即可给 OpenAI / Claude / Gemini
// 三类渠道当上游（按请求 URL 路由）：
//   - OpenAI 兼容：POST {base}/v1/chat/completions              （openai.go）
//   - Anthropic Claude：POST {base}/v1/messages                 （claude.go）
//   - Google Gemini：POST {base}/{ver}/models/{model}:generateContent
//     或 :streamGenerateContent?alt=sse                          （gemini.go）
//
// 设计目标：mock 自身绝不能成为压测瓶颈——热路径零锁（math/rand/v2 全局函数 + atomic 计数）、
// 近零分配（sync.Pool 缓冲 + 预生成 ASCII 词池手工拼接 JSON）、延迟全部用 time.Sleep 模拟。
// 响应时间（TTFB / 总时长）与内容（词数、词面）均在配置范围内随机。三种格式共用同一套
// 延迟 / 词数 / 错误注入参数（见 gen.go）。仅压测用途，不属于业务代码。用法见 bench/README.md。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
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
	http.StatusTooManyRequests:     "Rate limit reached for requests",
	http.StatusInternalServerError: "The server had an error while processing your request",
	http.StatusBadGateway:          "Bad gateway",
	http.StatusServiceUnavailable:  "The engine is currently overloaded, please try again later",
}

// errorMessage 返回状态码对应的人类可读消息（三种格式共用）。
func errorMessage(code int) string {
	if msg, ok := errorMessages[code]; ok {
		return msg
	}
	return "mock injected error"
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
	served := statOpenAI.served.Load() + statClaude.served.Load() + statGemini.served.Load()
	stream := statOpenAI.stream.Load() + statClaude.stream.Load() + statGemini.stream.Load()
	errs := statOpenAI.errs.Load() + statClaude.errs.Load() + statGemini.errs.Load()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"served_total":%d,"stream_total":%d,"errors_injected":%d,"active":%d,`+
		`"openai":{"served":%d,"stream":%d,"errors":%d},`+
		`"claude":{"served":%d,"stream":%d,"errors":%d},`+
		`"gemini":{"served":%d,"stream":%d,"errors":%d}}`,
		served, stream, errs, activeReqs.Load(),
		statOpenAI.served.Load(), statOpenAI.stream.Load(), statOpenAI.errs.Load(),
		statClaude.served.Load(), statClaude.stream.Load(), statClaude.errs.Load(),
		statGemini.served.Load(), statGemini.stream.Load(), statGemini.errs.Load())
}

// route 按请求 URL 路由到对应格式的 handler。Gemini 的流式由 URL action 决定，
// 需在 :generateContent 之前判断 :streamGenerateContent。
func route(w http.ResponseWriter, r *http.Request) {
	activeReqs.Add(1)
	defer activeReqs.Add(-1)

	p := r.URL.Path
	switch {
	case strings.Contains(p, ":streamGenerateContent"):
		geminiHandler(w, r, true)
	case strings.Contains(p, ":generateContent"):
		geminiHandler(w, r, false)
	case strings.HasSuffix(p, "/messages") && r.Method == http.MethodPost:
		claudeHandler(w, r)
	case strings.HasSuffix(p, "/chat/completions") && r.Method == http.MethodPost:
		openaiHandler(w, r)
	case strings.HasSuffix(p, "/models") && r.Method == http.MethodGet:
		modelsHandler(w, r)
	case p == "/stats":
		statsHandler(w, r)
	default:
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "mockai ok (openai:/v1/chat/completions claude:/v1/messages gemini:/v1beta/models/*:generateContent)\n")
	}
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

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(*port),
		Handler:           http.HandlerFunc(route),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		// WriteTimeout 留 0：流式响应时长由 latency 参数控制
		IdleTimeout: 120 * time.Second,
	}
	log.Printf("mockai listening on :%d  formats=[openai,claude,gemini]  ttfb=[%v,%v] total=[%v,%v] tokens=[%d,%d] error-rate=%.2f codes=%s",
		*port, *ttfbMin, *ttfbMax, *totalMin, *totalMax, *tokensMin, *tokensMax, *errorRate, *errorsCSV)
	log.Fatal(srv.ListenAndServe())
}
