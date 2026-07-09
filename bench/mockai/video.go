package mockai

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 视频为异步任务，模拟 doubao/volc 视频任务格式（通用 /v1/video/generations 渠道走此格式）：
//   submit: POST {base}/api/v3/contents/generations/tasks → {"id":"<task_id>"}
//   fetch:  GET  {base}/api/v3/contents/generations/tasks/{id}
//           → {"id","model","status","content":{"video_url"},"usage":{...}}
//           status: running → succeeded（-video-process-time 控制"生成耗时"）。
//
// 无状态设计：task_id 内编码提交毫秒时刻，fetch 时按已过时间判定进度，避免共享 map/锁，
// 契合 mock 热路径零锁原则。

// videoSubmitHandler 处理视频任务提交，返回内嵌提交时刻的 task_id。
func videoSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if code, inject := maybeErrorCode(); inject {
		statVideo.errs.Add(1)
		writeOpenAIError(w, code)
		return
	}
	id := reqCounter.Add(1)
	statVideo.served.Add(1)
	taskID := fmt.Sprintf("vidmock-%d-%d", time.Now().UnixMilli(), id)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"%s"}`, taskID)
}

// videoFetchHandler 处理视频任务轮询，按 task_id 内编码的提交时刻判定 running/succeeded。
// succeeded 时 video_url 指向 mock 自身、真实可下载的视频（默认动图 GIF，或 -video-file）。
func videoFetchHandler(w http.ResponseWriter, host, taskID string) {
	statVideo.stream.Add(1) // stream 复用为 fetch 轮询计数
	submitMs, okParse := parseVideoSubmitMs(taskID)
	done := true // 解析失败按已完成处理，避免客户端无限轮询
	if okParse {
		done = time.Now().UnixMilli()-submitMs >= videoProcess.Milliseconds()
	}

	w.Header().Set("Content-Type", "application/json")
	if !done {
		fmt.Fprintf(w, `{"id":"%s","model":"video-mock","status":"running","created_at":%d}`, taskID, submitMs/1000)
		return
	}
	n := completionWords()
	fmt.Fprintf(w, `{"id":"%s","model":"video-mock","status":"succeeded","content":{"video_url":"%s"},"usage":{"completion_tokens":%d,"total_tokens":%d},"created_at":%d,"updated_at":%d}`,
		taskID, mediaURL(host, "video", taskID, videoAsset.ext), n, n, submitMs/1000, time.Now().Unix())
}

// parseVideoSubmitMs 从 "vidmock-<ms>-<counter>" 解析提交毫秒时刻。
func parseVideoSubmitMs(taskID string) (int64, bool) {
	parts := strings.Split(taskID, "-")
	if len(parts) < 3 || parts[0] != "vidmock" {
		return 0, false
	}
	ms, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return ms, true
}

// ============================================================================
// OpenAI Sora 视频格式（另一套主流"视频创建"接口，对应 /v1/videos 渠道）：
//   create:  POST {base}/v1/videos               → {"id","object":"video","status":"queued",...}
//   remix:   POST {base}/v1/videos/{id}/remix     → 同 create（返回新任务）
//   fetch:   GET  {base}/v1/videos/{id}           → status queued → in_progress → completed
//   content: GET  {base}/v1/videos/{id}/content   → 真实视频字节（Sora 视频经此端点取回）
// 同样无状态：task_id 内编码提交时刻。
// ============================================================================

// soraVideoSubmitHandler 处理 Sora 视频创建（create / remix）。
func soraVideoSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if code, inject := maybeErrorCode(); inject {
		statVideo.errs.Add(1)
		writeOpenAIError(w, code)
		return
	}
	id := reqCounter.Add(1)
	statVideo.served.Add(1)
	taskID := fmt.Sprintf("vidmock-%d-%d", time.Now().UnixMilli(), id)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"%s","object":"video","model":"sora-mock","status":"queued","progress":0,"created_at":%d,"size":"720x1280","seconds":"4"}`,
		taskID, time.Now().Unix())
}

// soraVideoFetchHandler 处理 Sora 视频轮询，按 task_id 内编码的提交时刻判定进度。
func soraVideoFetchHandler(w http.ResponseWriter, taskID string) {
	statVideo.stream.Add(1)
	submitMs, okParse := parseVideoSubmitMs(taskID)
	done := true
	if okParse {
		done = time.Now().UnixMilli()-submitMs >= videoProcess.Milliseconds()
	}
	w.Header().Set("Content-Type", "application/json")
	if !done {
		fmt.Fprintf(w, `{"id":"%s","object":"video","model":"sora-mock","status":"in_progress","progress":50,"created_at":%d}`,
			taskID, submitMs/1000)
		return
	}
	fmt.Fprintf(w, `{"id":"%s","object":"video","model":"sora-mock","status":"completed","progress":100,"created_at":%d,"completed_at":%d,"size":"720x1280","seconds":"4"}`,
		taskID, submitMs/1000, time.Now().Unix())
}

// serveVideoBytes 返回真实视频字节（Sora 的 /v1/videos/{id}/content）。
func serveVideoBytes(w http.ResponseWriter) {
	statVideo.stream.Add(1)
	w.Header().Set("Content-Type", videoAsset.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(videoAsset.bytes)))
	_, _ = w.Write(videoAsset.bytes)
}

// soraTaskID 从 /v1/videos/{id}、/v1/videos/{id}/content、/v1/videos/{id}/remix 提取 id。
func soraTaskID(p string) string {
	const marker = "/videos/"
	i := strings.LastIndex(p, marker)
	if i < 0 {
		return ""
	}
	rest := p[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}
	return rest
}
