package main

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
func videoFetchHandler(w http.ResponseWriter, taskID string) {
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
	fmt.Fprintf(w, `{"id":"%s","model":"video-mock","status":"succeeded","content":{"video_url":"http://mockai.local/video/%s.mp4"},"usage":{"completion_tokens":%d,"total_tokens":%d},"created_at":%d,"updated_at":%d}`,
		taskID, taskID, n, n, submitMs/1000, time.Now().Unix())
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
