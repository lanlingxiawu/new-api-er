package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAI 图片生成：POST {base}/v1/images/generations（JSON）、/v1/images/edits、/v1/edits（multipart）。
// 响应 {"created":ts,"data":[{"url"|"b64_json"} × n]}。生成耗时用 -latency-* 模拟。

// onePxPNGB64 是 1x1 透明 PNG 的 base64，用于 response_format=b64_json 的响应。
const onePxPNGB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

type imageRequest struct {
	Model          string `json:"model"`
	N              int    `json:"n"`
	ResponseFormat string `json:"response_format"`
}

func imageHandler(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	// generations 是 JSON；edits/edits 是 multipart，JSON 解析失败则回退默认（n=1）。
	var req imageRequest
	_ = json.Unmarshal(body, &req)
	n := req.N
	if n < 1 {
		n = 1
	}
	if n > 10 {
		n = 10
	}

	if code, inject := maybeErrorCode(); inject {
		statImage.errs.Add(1)
		writeOpenAIError(w, code)
		return
	}
	statImage.served.Add(int64(n))
	time.Sleep(randDur(*totalMin, *totalMax))

	b64 := strings.EqualFold(req.ResponseFormat, "b64_json")
	id := reqCounter.Add(1)

	buf := bufPool.Get().(*strings.Builder)
	buf.Reset()
	buf.WriteString(`{"created":`)
	buf.WriteString(strconv.FormatInt(time.Now().Unix(), 10))
	buf.WriteString(`,"data":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		if b64 {
			buf.WriteString(`{"b64_json":"`)
			buf.WriteString(onePxPNGB64)
			buf.WriteString(`"}`)
		} else {
			buf.WriteString(`{"url":"http://mockai.local/img/`)
			buf.WriteString(strconv.FormatInt(id, 10))
			buf.WriteByte('-')
			buf.WriteString(strconv.Itoa(i))
			buf.WriteString(`.png"}`)
		}
	}
	buf.WriteString(`]}`)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = io.WriteString(w, buf.String())
	bufPool.Put(buf)
}
