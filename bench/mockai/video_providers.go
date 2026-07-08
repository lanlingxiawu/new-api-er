package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 各视频厂商的上游任务格式各不相同（submit URL / 响应信封 / status 枚举 / 结果 url 位置都不同）。
// 本文件把 doubao、sora 之外的主流视频渠道补齐：kling、vidu、ali、hailuo、jimeng。
// 统一无状态：submit 返回 vidmock-<ms>-<n> 的 task_id，fetch 时按其中编码的提交时刻判定
// queued/running → success（-video-process-time 控制"生成耗时"）；成功时 url 指向 mock 自身
// 真实可下载的视频（见 media.go）。

// newVideoTaskID 生成内嵌提交毫秒时刻的 task_id。
func newVideoTaskID() string {
	id := reqCounter.Add(1)
	return fmt.Sprintf("vidmock-%d-%d", time.Now().UnixMilli(), id)
}

// videoDone 按 task_id 内编码的提交时刻判断是否已"生成完成"。
func videoDone(taskID string) bool {
	submitMs, ok := parseVideoSubmitMs(taskID)
	if !ok {
		return true // 解析失败按已完成处理，避免无限轮询
	}
	return time.Now().UnixMilli()-submitMs >= videoProcess.Milliseconds()
}

// videoErrInject 命中错误注入时写回错误并返回 true（HTTP 状态码即触发网关失败/重试路径）。
func videoErrInject(w http.ResponseWriter) bool {
	if code, inject := maybeErrorCode(); inject {
		statVideo.errs.Add(1)
		writeOpenAIError(w, code)
		return true
	}
	return false
}

// lastSeg 返回路径最后一段（用于 /.../{taskID}）。
func lastSeg(p string) string {
	return p[strings.LastIndex(p, "/")+1:]
}

// segAfter 返回 marker 之后、到下一个 '/' 之前的一段（用于 /tasks/{taskID}/creations）。
func segAfter(p, marker string) string {
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

// ---------------------------------------------------------------------------
// kling：submit POST /v1/videos/{image2video|text2video} → data.task_id；
//        fetch  GET  /v1/videos/{action}/{id} → data.task_status（submitted/processing/succeed/failed）
//        成功 url 在 data.task_result.videos[0].url。
// ---------------------------------------------------------------------------

func klingSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if videoErrInject(w) {
		return
	}
	statVideo.served.Add(1)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"code":0,"message":"SUCCEED","request_id":"req-mock","data":{"task_id":"%s","task_status":"submitted"}}`, newVideoTaskID())
}

func klingFetchHandler(w http.ResponseWriter, host, taskID string) {
	statVideo.stream.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if !videoDone(taskID) {
		fmt.Fprintf(w, `{"code":0,"message":"SUCCEED","data":{"task_id":"%s","task_status":"processing"}}`, taskID)
		return
	}
	fmt.Fprintf(w, `{"code":0,"message":"SUCCEED","data":{"task_id":"%s","task_status":"succeed","task_result":{"videos":[{"id":"1","url":"%s","duration":"4"}]}}}`,
		taskID, mediaURL(host, "video", taskID, videoAsset.ext))
}

// ---------------------------------------------------------------------------
// vidu：submit POST /ent/v2/{img2video|...} → task_id/state；
//       fetch  GET  /ent/v2/tasks/{id}/creations → state（created/queueing/processing/success/failed）
//       成功 url 在 creations[0].url。
// ---------------------------------------------------------------------------

func viduSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if videoErrInject(w) {
		return
	}
	statVideo.served.Add(1)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"task_id":"%s","state":"created"}`, newVideoTaskID())
}

func viduFetchHandler(w http.ResponseWriter, host, taskID string) {
	statVideo.stream.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if !videoDone(taskID) {
		fmt.Fprintf(w, `{"task_id":"%s","state":"processing"}`, taskID)
		return
	}
	fmt.Fprintf(w, `{"task_id":"%s","state":"success","creations":[{"id":"1","url":"%s"}]}`,
		taskID, mediaURL(host, "video", taskID, videoAsset.ext))
}

// ---------------------------------------------------------------------------
// ali（阿里云 DashScope）：submit POST /api/v1/services/aigc/video-generation/video-synthesis
//       → output.task_id/task_status；fetch GET /api/v1/tasks/{id} → output.task_status
//       （PENDING/RUNNING/SUCCEEDED/FAILED），成功 url 在 output.video_url。
// ---------------------------------------------------------------------------

func aliSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if videoErrInject(w) {
		return
	}
	statVideo.served.Add(1)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"request_id":"req-mock","output":{"task_id":"%s","task_status":"PENDING"}}`, newVideoTaskID())
}

func aliFetchHandler(w http.ResponseWriter, host, taskID string) {
	statVideo.stream.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if !videoDone(taskID) {
		fmt.Fprintf(w, `{"request_id":"req-mock","output":{"task_id":"%s","task_status":"RUNNING"}}`, taskID)
		return
	}
	fmt.Fprintf(w, `{"request_id":"req-mock","output":{"task_id":"%s","task_status":"SUCCEEDED","video_url":"%s"}}`,
		taskID, mediaURL(host, "video", taskID, videoAsset.ext))
}

// ---------------------------------------------------------------------------
// hailuo（MiniMax）：submit POST /v1/video_generation → task_id；
//       query  GET /v1/query/video_generation?task_id={id} → status（Preparing/Queueing/Processing/Success/Fail）+ file_id；
//       file   GET /v1/files/retrieve?file_id={id} → file.download_url（两步取回真实 url）。
// ---------------------------------------------------------------------------

func hailuoSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if videoErrInject(w) {
		return
	}
	statVideo.served.Add(1)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"task_id":"%s","base_resp":{"status_code":0,"status_msg":"success"}}`, newVideoTaskID())
}

func hailuoQueryHandler(w http.ResponseWriter, taskID string) {
	statVideo.stream.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if !videoDone(taskID) {
		fmt.Fprintf(w, `{"task_id":"%s","status":"Processing","base_resp":{"status_code":0,"status_msg":"success"}}`, taskID)
		return
	}
	// file_id 复用 task_id，供 /v1/files/retrieve 构造真实 url。
	fmt.Fprintf(w, `{"task_id":"%s","status":"Success","file_id":"%s","base_resp":{"status_code":0,"status_msg":"success"}}`, taskID, taskID)
}

func hailuoFileHandler(w http.ResponseWriter, host, fileID string) {
	statVideo.stream.Add(1)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"file":{"file_id":0,"download_url":"%s"},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		mediaURL(host, "video", fileID, videoAsset.ext))
}

// ---------------------------------------------------------------------------
// jimeng（火山即梦，volc 签名 API）：submit/fetch 均 POST，由 query 参数 Action 区分。
//       submit Action=CVSync2AsyncSubmitTask → data.task_id；
//       fetch  Action=CVSync2AsyncGetResult（task_id 在 body）→ data.status（in_queue/generating/done）+ data.video_url。
// ---------------------------------------------------------------------------

func jimengSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if videoErrInject(w) {
		return
	}
	statVideo.served.Add(1)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"code":10000,"message":"Success","data":{"task_id":"%s"}}`, newVideoTaskID())
}

func jimengFetchHandler(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	statVideo.stream.Add(1)
	var req struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(body, &req)
	w.Header().Set("Content-Type", "application/json")
	if !videoDone(req.TaskID) {
		fmt.Fprint(w, `{"code":10000,"message":"Success","data":{"status":"in_queue"}}`)
		return
	}
	fmt.Fprintf(w, `{"code":10000,"message":"Success","data":{"status":"done","video_url":"%s"}}`,
		mediaURL(r.Host, "video", req.TaskID, videoAsset.ext))
}
