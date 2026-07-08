package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Anthropic Claude Messages 格式：POST {base}/v1/messages。
// 覆盖 Claude 原生渠道，以及以 Claude Messages 为载荷的 AWS Bedrock / Vertex-Claude。
//
// 流式为 Anthropic 事件序列：message_start → content_block_start →
// content_block_delta* → content_block_stop → message_delta → message_stop。
// 网关的 SSE scanner 只取 `data:` 行、忽略 `event:` 行，并以 EOF 结束（Claude 无 [DONE]）。

type claudeRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func claudeHandler(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req claudeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Model = sanitizeModel(req.Model, "claude-3-5-sonnet-20241022")

	if code, inject := maybeErrorCode(); inject {
		claudeError(w, code)
		return
	}

	p := promptTokensFromBody(len(body))
	n := completionWords()
	id := reqCounter.Add(1)
	statClaude.served.Add(1)

	if req.Stream {
		statClaude.stream.Add(1)
		claudeStream(w, req.Model, id, p, n)
		return
	}
	claudeNonStream(w, req.Model, id, p, n)
}

func claudeNonStream(w http.ResponseWriter, model string, id int64, promptTokens, n int) {
	time.Sleep(randDur(*totalMin, *totalMax))

	buf := bufPool.Get().(*strings.Builder)
	buf.Reset()
	buf.WriteString(`{"id":"msg_mock`)
	buf.WriteString(strconv.FormatInt(id, 10))
	buf.WriteString(`","type":"message","role":"assistant","model":"`)
	buf.WriteString(model)
	buf.WriteString(`","content":[{"type":"text","text":"`)
	for i := 0; i < n; i++ {
		buf.Write(randWord())
	}
	buf.WriteString(`"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":`)
	buf.WriteString(strconv.Itoa(promptTokens))
	buf.WriteString(`,"output_tokens":`)
	buf.WriteString(strconv.Itoa(n))
	buf.WriteString(`}}`)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = io.WriteString(w, buf.String())
	bufPool.Put(buf)
}

func claudeStream(w http.ResponseWriter, model string, id int64, promptTokens, n int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	total, ttfb := streamTiming()
	time.Sleep(ttfb)

	idStr := "msg_mock" + strconv.FormatInt(id, 10)

	// message_start：携带 input_tokens；output_tokens 起始为 1（对齐 Anthropic 语义）。
	fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"%s\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"%s\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":%d,\"output_tokens\":1}}}\n\n", idStr, model, promptTokens)
	// content_block_start：文本块
	_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	flusher.Flush()

	const deltaPrefix = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\""
	interval := streamInterval(total, ttfb, n)
	for i := 0; i < n; i++ {
		if interval > 0 {
			time.Sleep(interval)
		}
		_, _ = io.WriteString(w, deltaPrefix)
		_, _ = w.Write(randWord())
		_, _ = io.WriteString(w, "\"}}\n\n")
		flusher.Flush()
	}

	// content_block_stop → message_delta（携带最终 output_tokens）→ message_stop
	_, _ = io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n", n)
	_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	flusher.Flush()
}

// claudeError 返回 Anthropic 形态错误：{"type":"error","error":{"type","message"}}。
func claudeError(w http.ResponseWriter, code int) {
	time.Sleep(randDur(*ttfbMin, *ttfbMax))
	statClaude.errs.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"type":"error","error":{"type":"%s","message":"%s"}}`, claudeErrorType(code), errorMessage(code))
}

// claudeErrorType 把 HTTP 状态码映射为 Anthropic 错误类型。
func claudeErrorType(code int) string {
	switch code {
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable, 529:
		return "overloaded_error"
	case http.StatusBadRequest:
		return "invalid_request_error"
	default:
		return "api_error"
	}
}
