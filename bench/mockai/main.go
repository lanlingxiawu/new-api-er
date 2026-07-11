// Command mockai 是用于压测 new-api 网关的多格式 mock 上游。
//
// 按渠道接入的多种上游格式同时模拟，一个进程即可给不同类型渠道当上游（按请求 URL 路由）：
//   - chat/多模态：OpenAI POST /v1/chat/completions（openai.go）、Anthropic Claude
//     POST /v1/messages（claude.go）、Gemini POST /{ver}/models/{model}:generateContent
//     或 :streamGenerateContent?alt=sse（gemini.go）
//   - 图片：POST /v1/images/generations|/images/edits|/edits（image.go）
//   - 语音：TTS POST /v1/audio/speech（二进制）、STT POST /v1/audio/transcriptions|/translations（audio.go）
//   - 视频（异步任务，多厂商上游格式）：doubao/volc、OpenAI Sora（/v1/videos + remix/content）、
//     kling、vidu、ali(DashScope)、hailuo(MiniMax，两步取回)、jimeng(即梦) 的 submit + 轮询 fetch；
//     无状态按 task_id 内编码的提交时刻判定 queued→completed（video.go、video_providers.go）
//
// 真实媒体：图片/语音/视频返回真实可用的字节（PNG/WAV/GIF），URL 挂在 mock 自身 /media 或
// /v1/videos/{id}/content 下、真实可下载；-image-file/-audio-file/-video-file 可替换为真实素材
// （如真实 MP4——stdlib 无 MP4 编码器）（media.go）。
//
// 设计目标：mock 自身绝不能成为压测瓶颈——热路径零锁（math/rand/v2 全局函数 + atomic 计数）、
// 近零分配（sync.Pool 缓冲 + 预生成 ASCII 词池手工拼接 JSON）、延迟全部用 time.Sleep 模拟；
// 媒体在启动时生成一次、之后只做字节直写。响应时间（TTFB / 总时长）与内容（词数、词面）均在
// 配置范围内随机；各格式共用同一套延迟 / 词数 / 错误注入参数（见 gen.go）。仅压测用途，不属于
// 业务代码。用法见 bench/README.md。
package mockai

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

// defaultModelsCSV 是 /v1/models 默认暴露的模型清单，覆盖 mock 支持的全部端点
// （OpenAI/Claude/Gemini 对话、embedding、图像、音频、视频）。mock 对 chat 来者不拒，
// 未知模型名照常应答；此清单只影响"获取模型列表"的返回，可用 -models 覆盖。
const defaultModelsCSV = "gpt-4o-mini,gpt-4o,gpt-4o-2024-11-20,gpt-4.1,gpt-4.1-mini,gpt-4-turbo,gpt-4,gpt-3.5-turbo," +
	"o1,o1-mini,o3,o3-mini,o4-mini," +
	"text-embedding-3-small,text-embedding-3-large,text-embedding-ada-002," +
	"dall-e-3,dall-e-2,gpt-image-1," +
	"whisper-1,tts-1,tts-1-hd," +
	"claude-3-5-sonnet-20241022,claude-3-5-haiku-20241022,claude-3-opus-20240229,claude-sonnet-4-20250514,claude-opus-4-20250514," +
	"gemini-1.5-pro,gemini-1.5-flash,gemini-2.0-flash,gemini-2.5-pro,gemini-2.5-flash," +
	"sora,kling-v1,vidu-2.0,wan-2.1,hailuo-02,jimeng-video-3.0"

// fs 是 mockai 子命令自己的 FlagSet。三个子命令（mockai/loadgen/webui）编译进同一二进制，
// 各用独立 FlagSet 避免全局 flag 名冲突（如 mockai 与 webui 都有 -port）。
var fs = flag.NewFlagSet("mockai", flag.ExitOnError)

var (
	port         = fs.Int("port", 18080, "监听端口")
	ttfbMin      = fs.Duration("ttfb-min", 50*time.Millisecond, "流式：首 chunk 最小延迟")
	ttfbMax      = fs.Duration("ttfb-max", 300*time.Millisecond, "流式：首 chunk 最大延迟")
	totalMin     = fs.Duration("latency-min", 300*time.Millisecond, "响应总时长下限")
	totalMax     = fs.Duration("latency-max", 2000*time.Millisecond, "响应总时长上限")
	tokensMin    = fs.Int("tokens-min", 20, "随机补全词数下限")
	tokensMax    = fs.Int("tokens-max", 200, "随机补全词数上限")
	modelsCSV    = fs.String("models", defaultModelsCSV, "/v1/models 暴露的模型列表")
	errorRate    = fs.Float64("error-rate", 0, "随机返回错误状态码的概率 [0,1]，0 关闭")
	errorsCSV    = fs.String("error-codes", "429:1,500:1,502:1,503:1", "错误码权重表 code:weight,...（避免 401/403，网关可能据此自动禁用渠道）")
	videoProcess = fs.Duration("video-process-time", 3*time.Second, "视频任务从 submit 到 succeeded 的模拟生成耗时")
	imageFile    = fs.String("image-file", "", "图片响应用的真实文件（png/jpg/...）；留空则生成 512x512 PNG")
	audioFile    = fs.String("audio-file", "", "TTS 响应用的真实音频文件（mp3/wav/...）；留空则生成 1s WAV 正弦音")
	videoFile    = fs.String("video-file", "", "视频响应用的真实文件（mp4/webm/...）；留空则用内嵌示例 MP4（H.264，浏览器可播）")
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
	served := statOpenAI.served.Load() + statClaude.served.Load() + statGemini.served.Load() +
		statImage.served.Load() + statAudio.served.Load() + statVideo.served.Load()
	stream := statOpenAI.stream.Load() + statClaude.stream.Load() + statGemini.stream.Load()
	errs := statOpenAI.errs.Load() + statClaude.errs.Load() + statGemini.errs.Load() +
		statImage.errs.Load() + statAudio.errs.Load() + statVideo.errs.Load()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"served_total":%d,"stream_total":%d,"errors_injected":%d,"active":%d,`+
		`"openai":{"served":%d,"stream":%d,"errors":%d},`+
		`"claude":{"served":%d,"stream":%d,"errors":%d},`+
		`"gemini":{"served":%d,"stream":%d,"errors":%d},`+
		`"image":{"images":%d,"errors":%d},`+
		`"audio":{"served":%d,"errors":%d},`+
		`"video":{"submits":%d,"fetches":%d,"errors":%d}}`,
		served, stream, errs, activeReqs.Load(),
		statOpenAI.served.Load(), statOpenAI.stream.Load(), statOpenAI.errs.Load(),
		statClaude.served.Load(), statClaude.stream.Load(), statClaude.errs.Load(),
		statGemini.served.Load(), statGemini.stream.Load(), statGemini.errs.Load(),
		statImage.served.Load(), statImage.errs.Load(),
		statAudio.served.Load(), statAudio.errs.Load(),
		statVideo.served.Load(), statVideo.stream.Load(), statVideo.errs.Load())
}

// route 按请求 URL 路由到对应格式的 handler。Gemini 的流式由 URL action 决定，
// 需在 :generateContent 之前判断 :streamGenerateContent。
func route(w http.ResponseWriter, r *http.Request) {
	activeReqs.Add(1)
	defer activeReqs.Add(-1)

	p := r.URL.Path
	switch {
	// ==== 视频（异步任务，多厂商上游格式）====
	// jimeng（volc 签名，POST + query Action 区分 submit/fetch）
	case r.Method == http.MethodPost && strings.Contains(r.URL.RawQuery, "CVSync2AsyncSubmitTask"):
		jimengSubmitHandler(w, r)
	case r.Method == http.MethodPost && strings.Contains(r.URL.RawQuery, "CVSync2AsyncGetResult"):
		jimengFetchHandler(w, r)
	// doubao/volc：submit POST .../tasks、fetch GET .../tasks/{id}
	case r.Method == http.MethodGet && strings.Contains(p, "/contents/generations/tasks/"):
		videoFetchHandler(w, r.Host, videoTaskID(p))
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/contents/generations/tasks"):
		videoSubmitHandler(w, r)
	case r.Method == http.MethodGet && strings.Contains(p, "/video/generations/"):
		openaiVideoFetchHandler(w, r.Host, lastSeg(p))
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/video/generations"):
		openaiVideoSubmitHandler(w, r)
	// kling（fetch 必须在 sora 之前判断 /videos/{action}/{id}）
	case r.Method == http.MethodGet && (strings.Contains(p, "/videos/image2video/") || strings.Contains(p, "/videos/text2video/")):
		klingFetchHandler(w, r.Host, lastSeg(p))
	case r.Method == http.MethodPost && (strings.HasSuffix(p, "/image2video") || strings.HasSuffix(p, "/text2video")):
		klingSubmitHandler(w, r)
	// vidu：submit POST /ent/v2/{img2video|...}、fetch GET /ent/v2/tasks/{id}/creations
	case r.Method == http.MethodGet && strings.Contains(p, "/ent/v2/tasks/") && strings.HasSuffix(p, "/creations"):
		viduFetchHandler(w, r.Host, segAfter(p, "/tasks/"))
	case r.Method == http.MethodPost && (strings.HasSuffix(p, "/img2video") || strings.HasSuffix(p, "/start-end2video") || strings.HasSuffix(p, "/reference2video")):
		viduSubmitHandler(w, r)
	// ali（DashScope）：submit POST .../video-synthesis、fetch GET /api/v1/tasks/{id}
	case r.Method == http.MethodGet && strings.Contains(p, "/api/v1/tasks/"):
		aliFetchHandler(w, r.Host, lastSeg(p))
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/video-synthesis"):
		aliSubmitHandler(w, r)
	// hailuo（MiniMax）：submit POST /v1/video_generation、query GET .../query/video_generation、file GET .../files/retrieve
	case r.Method == http.MethodGet && strings.Contains(p, "/query/video_generation"):
		hailuoQueryHandler(w, r.URL.Query().Get("task_id"))
	case r.Method == http.MethodGet && strings.Contains(p, "/files/retrieve"):
		hailuoFileHandler(w, r.Host, r.URL.Query().Get("file_id"))
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/video_generation"):
		hailuoSubmitHandler(w, r)
	// sora（OpenAI）：content（GET .../content）/ create·remix（POST）/ fetch（GET .../videos/{id}）
	case r.Method == http.MethodGet && strings.Contains(p, "/videos/") && strings.HasSuffix(p, "/content"):
		serveVideoBytes(w)
	case r.Method == http.MethodPost && (strings.HasSuffix(p, "/videos") || strings.HasSuffix(p, "/remix")):
		soraVideoSubmitHandler(w, r)
	case r.Method == http.MethodGet && strings.Contains(p, "/videos/"):
		soraVideoFetchHandler(w, soraTaskID(p))
	// 真实媒体托管：图片 / 语音 / 视频的 URL 指向这里，返回真实可用的字节
	case r.Method == http.MethodGet && strings.Contains(p, "/media/"):
		mediaHandler(w, p)
	// 文本/多模态 chat 三格式
	case strings.Contains(p, ":streamGenerateContent"):
		geminiHandler(w, r, true)
	case strings.Contains(p, ":generateContent"):
		geminiHandler(w, r, false)
	case strings.HasSuffix(p, "/messages") && r.Method == http.MethodPost:
		claudeHandler(w, r)
	case strings.HasSuffix(p, "/chat/completions") && r.Method == http.MethodPost:
		openaiHandler(w, r)
	// 图片
	case r.Method == http.MethodPost && (strings.HasSuffix(p, "/images/generations") || strings.HasSuffix(p, "/images/edits") || strings.HasSuffix(p, "/edits")):
		imageHandler(w, r)
	// 语音：TTS（二进制）/ STT（{"text"}）
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/audio/speech"):
		audioSpeechHandler(w, r)
	case r.Method == http.MethodPost && (strings.HasSuffix(p, "/audio/transcriptions") || strings.HasSuffix(p, "/audio/translations")):
		audioTranscriptionHandler(w, r)
	case strings.HasSuffix(p, "/models") && r.Method == http.MethodGet:
		modelsHandler(w, r)
	case p == "/stats":
		statsHandler(w, r)
	default:
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "mockai ok (chat: openai /v1/chat/completions, claude /v1/messages, gemini /v1beta/models/*:generateContent; image /v1/images/generations; audio /v1/audio/speech|transcriptions; video /api/v3/contents/generations/tasks)\n")
	}
}

// videoTaskID 从 .../contents/generations/tasks/{id} 提取 task_id。
func videoTaskID(p string) string {
	const marker = "/contents/generations/tasks/"
	if i := strings.LastIndex(p, marker); i >= 0 {
		return p[i+len(marker):]
	}
	return ""
}

// Run 是 mockai 子命令入口（由 bench 根命令 dispatch，args 为 mockai 之后的参数）。
func Run(args []string) {
	_ = fs.Parse(args)
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
	initMedia()

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(*port),
		Handler:           http.HandlerFunc(route),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		// WriteTimeout 留 0：流式响应时长由 latency 参数控制
		IdleTimeout: 120 * time.Second,
	}
	log.Printf("mockai listening on :%d  formats=[openai,claude,gemini,image,audio,video]  ttfb=[%v,%v] total=[%v,%v] tokens=[%d,%d] video-process=%v error-rate=%.2f codes=%s",
		*port, *ttfbMin, *ttfbMax, *totalMin, *totalMax, *tokensMin, *tokensMax, *videoProcess, *errorRate, *errorsCSV)
	log.Fatal(srv.ListenAndServe())
}
