package mockai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAI 兼容格式：POST {base}/v1/chat/completions。
// 覆盖 new-api 中绝大多数「OpenAI 兼容」渠道（OpenAI/Azure/DeepSeek/Moonshot/…）。

type openAIRequest struct {
	Model         string `json:"model"`
	Stream        bool   `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func openaiHandler(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req openAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Model = sanitizeModel(req.Model, "gpt-4o-mini")

	if code, inject := maybeErrorCode(); inject {
		openaiError(w, code)
		return
	}

	p := promptTokensFromBody(len(body))
	n := completionWords()
	id := reqCounter.Add(1)
	created := time.Now().Unix()
	statOpenAI.served.Add(1)

	if req.Stream {
		statOpenAI.stream.Add(1)
		openaiStream(w, req, id, created, p, n)
		return
	}
	openaiNonStream(w, req, id, created, p, n)
}

func openaiNonStream(w http.ResponseWriter, req openAIRequest, id, created int64, promptTokens, n int) {
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

func openaiStream(w http.ResponseWriter, req openAIRequest, id, created int64, promptTokens, n int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	total, ttfb := streamTiming()
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

	interval := streamInterval(total, ttfb, n)
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

	// usage chunk（OpenAI 语义仅在 include_usage 时发送；不发送时由网关侧 tokenizer 计数）
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

// writeOpenAIError 写出 OpenAI 形态错误 {"error":{"message","type","code"}}（不计数）。
// image/audio/video 等 OpenAI 兼容端点共用此错误形态，各自增自己的 errs 计数。
func writeOpenAIError(w http.ResponseWriter, code int) {
	time.Sleep(randDur(*ttfbMin, *ttfbMax)) // 错误也带延迟，接近真实上游故障形态
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":{"message":"%s","type":"mock_injected_error","code":%d}}`, errorMessage(code), code)
}

// openaiError 返回 OpenAI 形态错误并计入 openai errs。
func openaiError(w http.ResponseWriter, code int) {
	statOpenAI.errs.Add(1)
	writeOpenAIError(w, code)
}
