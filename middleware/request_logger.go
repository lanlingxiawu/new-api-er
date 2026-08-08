package middleware

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 请求日志的写入侧。
//
// Main-chain impact (Rule 0): 本中间件挂在每一条 relay 路由上。relay goroutine 只做
// "组装结构体 + 一次非阻塞 channel 发送"，落盘与索引登记全部交给固定数量的常驻
// worker。worker 数即稳态峰值文件句柄数，与 RPM 完全解耦——早期"每条一个 goroutine"
// 的模型在磁盘存储下会让 fd 与并发同阶（实测 c=500 时 goroutine 曾涨到 17 万）。
//
// 请求日志是尽力而为的可观测性数据（不参与计费/审计），因此过载时**丢弃**而不是
// 阻塞或无界增长（Rule 8 优雅降级）。
type requestLogTask struct {
	entry *model.RequestLog
	// resolveUserId > 0 表示同步阶段拿不到用户名，需要 worker 侧查缓存补齐。
	// 这一步刻意不在 relay goroutine 上做：缓存未命中会退化成一次 DB 查询。
	resolveUserId int
}

type requestLogQueueHandle struct {
	ch   chan requestLogTask
	done chan struct{}
	// pending 在投递成功时 +1、worker 处理完 -1，覆盖"已出队但尚未写完"的窗口。
	// 不能改成 worker 收到任务后才 +1：出队与自增之间存在空隙，排空逻辑会在那一刻
	// 看到"队列空且无在途"而提前返回，导致关停快照漏掉最后几条。
	pending atomic.Int64
	// workers 让 StopRequestLogWriters 能等 worker 真正退出后再清点残留任务。
	workers sync.WaitGroup
}

var (
	requestLogQueue   atomic.Pointer[requestLogQueueHandle]
	requestLogDropped atomic.Int64
)

// StartRequestLogWriters 构建写队列并拉起写盘 worker。
// 必须在 model.InitRequestLogStore() 之后调用：队列深度与 worker 数由它从环境变量解析。
func StartRequestLogWriters() {
	StopRequestLogWriters()
	handle := &requestLogQueueHandle{
		ch:   make(chan requestLogTask, model.RequestLogQueueSize()),
		done: make(chan struct{}),
	}
	handle.workers.Add(model.RequestLogWriterCount())
	for i := 0; i < model.RequestLogWriterCount(); i++ {
		go requestLogWriterLoop(handle)
	}
	requestLogQueue.Store(handle)
}

// StopRequestLogWriters 停止 worker。队列 channel 永不关闭，保证并发的
// enqueueRequestLog 不会向已关闭的 channel 发送而 panic。
func StopRequestLogWriters() {
	handle := requestLogQueue.Swap(nil)
	if handle == nil {
		return
	}
	close(handle.done)
	handle.workers.Wait()
	drainResidualRequestLogTasks(handle)
}

// drainResidualRequestLogTasks 清点 worker 退出后仍留在队列里的任务。
// 这些任务永远不会被写盘——可能是 enqueueRequestLog 取出 handle 之后才投递进来的。
// 记进丢弃计数，而不是让它们连同 pending 一起被无声吞掉。
func drainResidualRequestLogTasks(handle *requestLogQueueHandle) {
	for {
		select {
		case <-handle.ch:
			handle.pending.Add(-1)
			requestLogDropped.Add(1)
		default:
			return
		}
	}
}

// DrainRequestLogQueue 在关停时等待队列排空，让在途条目落盘并进索引，
// 随后的索引快照才是完整的。返回未能处理的剩余条数。
func DrainRequestLogQueue(timeout time.Duration) int {
	handle := requestLogQueue.Load()
	if handle == nil {
		return 0
	}
	deadline := time.Now().Add(timeout)
	for {
		pending := handle.pending.Load()
		if pending <= 0 {
			return 0
		}
		if !time.Now().Before(deadline) {
			common.SysLog(fmt.Sprintf("request log drain timeout, %d entries dropped", pending))
			return int(pending)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func requestLogWriterLoop(handle *requestLogQueueHandle) {
	defer handle.workers.Done()
	process := func(task requestLogTask) {
		defer handle.pending.Add(-1)
		runRequestLogTask(task)
	}
	for {
		select {
		case task := <-handle.ch:
			process(task)
		case <-handle.done:
			// 退出前尽量把队列里剩下的处理完。
			for {
				select {
				case task := <-handle.ch:
					process(task)
				default:
					return
				}
			}
		}
	}
}

func runRequestLogTask(task requestLogTask) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("request log writer panic recovered: %v", r))
		}
	}()
	if task.entry == nil {
		return
	}
	if task.resolveUserId > 0 {
		task.entry.Username = resolveRequestLogUsername(task.resolveUserId)
	}
	if !requestLogUsernameMatches(task.entry.Username) {
		return
	}
	model.RecordRequestLog(task.entry)
}

// enqueueRequestLog 非阻塞投递。队列满即丢弃并限流告警——绝不阻塞 relay goroutine。
func enqueueRequestLog(task requestLogTask) {
	handle := requestLogQueue.Load()
	if handle == nil {
		return
	}
	// 先记账再投递，保证 pending 永远不低于真实的未完成量。
	handle.pending.Add(1)
	select {
	case handle.ch <- task:
	default:
		handle.pending.Add(-1)
		if dropped := requestLogDropped.Add(1); dropped == 1 || dropped%1000 == 0 {
			common.SysError(fmt.Sprintf(
				"request log dropped under overload: queue depth %d reached, total dropped=%d",
				cap(handle.ch), dropped))
		}
	}
}

// responseBodyWriter 包装 gin.ResponseWriter，在透传响应的同时把返回体缓存到内存（受 limit 限制）。
type responseBodyWriter struct {
	gin.ResponseWriter
	body      *bytes.Buffer
	limit     int
	truncated bool
	totalSize int64 // 返回体实际字节数（含被截断的部分）
}

func (w *responseBodyWriter) capture(b []byte) {
	w.totalSize += int64(len(b))
	if w.limit <= 0 {
		return
	}
	if w.body.Len() >= w.limit {
		w.truncated = true
		return
	}
	remaining := w.limit - w.body.Len()
	if len(b) <= remaining {
		w.body.Write(b)
	} else {
		w.body.Write(b[:remaining])
		w.truncated = true
	}
}

func (w *responseBodyWriter) Write(b []byte) (int, error) {
	w.capture(b)
	return w.ResponseWriter.Write(b)
}

func (w *responseBodyWriter) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

// RequestResponseLogger 记录中转请求的下游请求体/请求头 以及 返回给下游的返回头/返回体。
// 仅在 common.RequestLogEnabled 开启时生效；当 common.RequestLogUsername 非空时仅记录该用户名。
func RequestResponseLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 关闭时零开销直接放行
		if !common.RequestLogEnabled {
			c.Next()
			return
		}

		started := time.Now()
		maxBytes := common.RequestLogMaxBodyKB * 1024
		if maxBytes <= 0 {
			maxBytes = 64 * 1024
		}

		// 捕获下游请求头与请求体快照（请求体读取后会复位，供后续 relay 复用）
		requestHeaders := headersToString(http.Header(c.Request.Header), maxBytes)
		requestBody, reqTruncated, reqBodySize := captureRequestBody(c, maxBytes)

		// 包装 writer 以捕获返回体
		rbw := &responseBodyWriter{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
			limit:          maxBytes,
		}
		c.Writer = rbw

		// 用 defer 而不是 c.Next() 之后的顺序代码：下游 panic 时栈展开会跳过顺序代码，
		// 而 panic 请求恰恰是最需要看请求体的。这里不 recover，panic 继续上交给顶层
		// Recovery，原始堆栈不受影响；completed 用来区分正常返回与栈展开。
		completed := false
		defer func() {
			recordRequestLogEntry(c, requestLogCapture{
				started:        started,
				requestHeaders: requestHeaders,
				requestBody:    requestBody,
				reqTruncated:   reqTruncated,
				reqBodySize:    reqBodySize,
				maxBytes:       maxBytes,
				writer:         rbw,
				completed:      completed,
			})
		}()

		c.Next()
		completed = true
	}
}

type requestLogCapture struct {
	started        time.Time
	requestHeaders string
	requestBody    string
	reqTruncated   bool
	reqBodySize    int64
	maxBytes       int
	writer         *responseBodyWriter
	completed      bool
}

func recordRequestLogEntry(c *gin.Context, snap requestLogCapture) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("request log capture panic recovered: %v", r))
		}
	}()

	// 用户名过滤：RequestLogUsername 为空记录全部，否则仅记录匹配的登录用户名
	// （比较的是账号的登录用户名 username，不是显示名/邮箱）。大小写不敏感、去除首尾空白。
	username := strings.TrimSpace(c.GetString("username"))
	userId := c.GetInt("id")
	resolveUserId := 0
	if username == "" && userId > 0 {
		// 鉴权中间件没往 context 写 username 的少数路径：推迟到 worker 里查缓存。
		resolveUserId = userId
	} else if !requestLogUsernameMatches(username) {
		return
	}

	status := c.Writer.Status()
	if !snap.completed {
		// 栈展开中：顶层 Recovery 还没来得及写 500，这里按最终结果记录。
		status = http.StatusInternalServerError
	}

	entry := &model.RequestLog{
		CreatedAt:        common.GetTimestamp(),
		UserId:           userId,
		Username:         username,
		TokenName:        c.GetString("token_name"),
		ModelName:        c.GetString("original_model"),
		ChannelId:        c.GetInt("channel_id"),
		Method:           c.Request.Method,
		Url:              truncateString(c.Request.URL.RequestURI(), 2000),
		StatusCode:       status,
		Ip:               c.ClientIP(),
		RequestId:        c.GetString(common.RequestIdKey),
		UseTimeMs:        time.Since(snap.started).Milliseconds(),
		IsStream:         c.GetBool("is_stream"),
		RequestBodySize:  snap.reqBodySize,
		ResponseBodySize: snap.writer.totalSize,
		RequestHeaders:   snap.requestHeaders,
		RequestBody:      appendTruncatedMark(snap.requestBody, snap.reqTruncated),
		ResponseHeaders:  headersToString(http.Header(snap.writer.Header()), snap.maxBytes),
		ResponseBody:     appendTruncatedMark(captureResponseBody(c, snap.writer), snap.writer.truncated),
	}

	enqueueRequestLog(requestLogTask{entry: entry, resolveUserId: resolveUserId})
}

// requestLogUsernameMatches 判定用户名是否命中过滤器。纯函数，不触碰缓存/DB。
func requestLogUsernameMatches(username string) bool {
	filterUser := strings.TrimSpace(common.RequestLogUsername)
	if filterUser == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(username), filterUser)
}

// resolveRequestLogUsername 在 worker goroutine 上按用户 id 补齐用户名。
func resolveRequestLogUsername(userId int) string {
	if userId <= 0 {
		return ""
	}
	userCache, err := model.GetUserCache(userId)
	if err != nil || userCache == nil {
		return ""
	}
	return strings.TrimSpace(userCache.Username)
}

// captureRequestBody 读取请求体快照并复位 body，返回 (内容, 是否截断, 实际字节数)。
// 非文本场景只统计大小、不保留正文。
func captureRequestBody(c *gin.Context, maxBytes int) (string, bool, int64) {
	if c.Request == nil || c.Request.Body == nil {
		return "", false, 0
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return "", false, 0
	}
	size := storage.Size()
	contentType := c.Request.Header.Get("Content-Type")
	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr == nil {
		c.Request.Body = io.NopCloser(storage)
	}
	if !isTextualContentType(contentType) {
		return "[non-textual request body omitted]", false, size
	}
	if _, err = storage.Seek(0, io.SeekStart); err != nil {
		return "", false, size
	}
	data, truncated := readLimited(storage, maxBytes)
	// 复位，保证后续 relay 仍可读取完整请求体
	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr == nil {
		c.Request.Body = io.NopCloser(storage)
	}
	return string(data), truncated, size
}

// captureResponseBody 根据返回 Content-Type 决定是否保留缓存的返回体。
func captureResponseBody(c *gin.Context, rbw *responseBodyWriter) string {
	contentType := rbw.Header().Get("Content-Type")
	if contentType != "" && !isTextualContentType(contentType) {
		return "[non-textual response body omitted]"
	}
	return rbw.body.String()
}

func readLimited(r io.Reader, maxBytes int) ([]byte, bool) {
	if maxBytes <= 0 {
		return nil, false
	}
	// 多读 1 字节以判断是否被截断
	buf := make([]byte, 0, maxBytes)
	limited := io.LimitReader(r, int64(maxBytes)+1)
	data, _ := io.ReadAll(limited)
	if len(data) > maxBytes {
		return append(buf, data[:maxBytes]...), true
	}
	return append(buf, data...), false
}

func isTextualContentType(contentType string) bool {
	if contentType == "" {
		return true // 缺省按文本处理（多数 relay 为 json）
	}
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "json") ||
		strings.Contains(ct, "text") ||
		strings.Contains(ct, "event-stream") ||
		strings.Contains(ct, "x-www-form-urlencoded") ||
		strings.Contains(ct, "xml")
}

// headersToString 序列化头部，并按与正文相同的上限截断。
// 头部原本不受限，与"每个字段都截断到该大小"的设置文案不符，也让单条日志的内存
// 上限无法估算（恶意的超大 Cookie/自定义头会撑爆）。
func headersToString(h http.Header, limit int) string {
	if len(h) == 0 {
		return ""
	}
	str, err := common.Marshal(h)
	if err != nil {
		return ""
	}
	if limit > 0 && len(str) > limit {
		return appendTruncatedMark(string(str[:limit]), true)
	}
	return string(str)
}

func appendTruncatedMark(s string, truncated bool) string {
	if truncated {
		return s + "\n...[truncated]"
	}
	return s
}

func truncateString(s string, max int) string {
	if max > 0 && len(s) > max {
		return s[:max]
	}
	return s
}
