package main

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Google Gemini 格式：POST {base}/{version}/models/{model}:generateContent
// 或 :streamGenerateContent?alt=sse（流式）。覆盖 Gemini / Vertex-Gemini 渠道。
//
// 是否流式由 URL action 决定（不在 body 里）。流式为标准 SSE `data: {json}` 帧、
// 以 EOF 结束（Gemini 无 [DONE]）；末帧携带 finishReason 与 usageMetadata。

func geminiHandler(w http.ResponseWriter, r *http.Request, stream bool) {
	// Gemini 请求体不含 model/stream（model 在路径、stream 在 action），仅用于估算 prompt。
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	if code, inject := maybeErrorCode(); inject {
		geminiError(w, code)
		return
	}

	p := promptTokensFromBody(len(body))
	n := completionWords()
	statGemini.served.Add(1)

	if stream {
		statGemini.stream.Add(1)
		geminiStream(w, p, n)
		return
	}
	geminiNonStream(w, p, n)
}

func geminiNonStream(w http.ResponseWriter, promptTokens, n int) {
	time.Sleep(randDur(*totalMin, *totalMax))

	buf := bufPool.Get().(*strings.Builder)
	buf.Reset()
	buf.WriteString(`{"candidates":[{"content":{"role":"model","parts":[{"text":"`)
	for i := 0; i < n; i++ {
		buf.Write(randWord())
	}
	buf.WriteString(`"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":`)
	buf.WriteString(strconv.Itoa(promptTokens))
	buf.WriteString(`,"candidatesTokenCount":`)
	buf.WriteString(strconv.Itoa(n))
	buf.WriteString(`,"totalTokenCount":`)
	buf.WriteString(strconv.Itoa(promptTokens + n))
	buf.WriteString(`}}`)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = io.WriteString(w, buf.String())
	bufPool.Put(buf)
}

func geminiStream(w http.ResponseWriter, promptTokens, n int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	total, ttfb := streamTiming()
	time.Sleep(ttfb)

	// 中间帧：仅文本 part，无 finishReason / usageMetadata（对齐 Gemini 逐块语义）。
	const chunkPrefix = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"`
	interval := streamInterval(total, ttfb, n)
	for i := 0; i < n; i++ {
		if interval > 0 {
			time.Sleep(interval)
		}
		_, _ = io.WriteString(w, chunkPrefix)
		_, _ = w.Write(randWord())
		_, _ = io.WriteString(w, "\"}]},\"index\":0}]}\n\n")
		flusher.Flush()
	}

	// 末帧：空文本 + finishReason=STOP + 完整 usageMetadata。
	fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"\"}]},\"finishReason\":\"STOP\",\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":%d,\"candidatesTokenCount\":%d,\"totalTokenCount\":%d}}\n\n",
		promptTokens, n, promptTokens+n)
	flusher.Flush()
}

// geminiError 返回 Google 形态错误：{"error":{"code","message","status"}}。
func geminiError(w http.ResponseWriter, code int) {
	time.Sleep(randDur(*ttfbMin, *ttfbMax))
	statGemini.errs.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":{"code":%d,"message":"%s","status":"%s"}}`, code, errorMessage(code), geminiErrorStatus(code))
}

// geminiErrorStatus 把 HTTP 状态码映射为 Google API 的 status 枚举。
func geminiErrorStatus(code int) string {
	switch code {
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusInternalServerError:
		return "INTERNAL"
	case http.StatusBadGateway:
		return "UNAVAILABLE"
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	default:
		return "UNKNOWN"
	}
}
