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

// requestLogWriterLoop 每次取到一条后，再非阻塞地捎带队列里已有的条目凑成一批
// （不等待，不引入延迟），整批一次写盘、一次登记索引。
func requestLogWriterLoop(handle *requestLogQueueHandle) {
	defer handle.workers.Done()
	writer := model.NewRequestLogWriter()
	defer writer.Close()
	batch := make([]requestLogTask, 0, model.RequestLogMaxBatch)
	process := func(first requestLogTask) {
		batch = append(batch[:0], first)
	fill:
		for len(batch) < model.RequestLogMaxBatch {
			select {
			case task := <-handle.ch:
				batch = append(batch, task)
			default:
				break fill
			}
		}
		defer handle.pending.Add(-int64(len(batch)))
		runRequestLogBatch(writer, batch)
		// 复用的切片不能继续引用已写完的条目（每条带着完整正文）。
		clear(batch)
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

func runRequestLogBatch(writer *model.RequestLogWriter, tasks []requestLogTask) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("request log writer panic recovered: %v", r))
		}
	}()
	logs := make([]*model.RequestLog, 0, len(tasks))
	for _, task := range tasks {
		if task.entry == nil {
			continue
		}
		if task.resolveUserId > 0 {
			task.entry.Username = resolveRequestLogUsername(task.resolveUserId)
		}
		if !requestLogUsernameMatches(task.entry.Username) {
			continue
		}
		logs = append(logs, task.entry)
	}
	writer.Record(logs)
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

// Unwrap 让流式 ResponseController 到达底层 FlushError，防止日志包装掩盖客户端写出失败。
func (w *responseBodyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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

		// 捕获下游请求头与请求体前缀。请求体只读 maxBytes+1 字节，读到的前缀原样拼回
		// c.Request.Body，下游（鉴权、Distribute、relay）看到的仍是完整请求体。
		requestHeaders := headersToString(http.Header(c.Request.Header), maxBytes)
		reqBody := captureRequestBody(c, maxBytes)

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
				requestBody:    reqBody,
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
	requestBody    requestBodyCapture
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

	// Url 会出现在对非 root 管理员开放的列表里，记录时即去掉查询串里的凭据。
	entry := &model.RequestLog{
		CreatedAt:        common.GetTimestamp(),
		UserId:           userId,
		Username:         username,
		TokenName:        c.GetString("token_name"),
		ModelName:        c.GetString("original_model"),
		ChannelId:        c.GetInt("channel_id"),
		Method:           c.Request.Method,
		Url:              truncateString(common.RedactRequestURI(c.Request.URL), 2000),
		StatusCode:       status,
		Ip:               c.ClientIP(),
		RequestId:        c.GetString(common.RequestIdKey),
		UseTimeMs:        time.Since(snap.started).Milliseconds(),
		IsStream:         c.GetBool("is_stream"),
		RequestBodySize:  snap.requestBody.size(),
		ResponseBodySize: snap.writer.totalSize,
		RequestHeaders:   snap.requestHeaders,
		RequestBody:      appendTruncatedMark(snap.requestBody.content, snap.requestBody.truncated),
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

// requestBodyCapture 是请求体的日志快照：正文前缀在进入下游前读取，总字节数要等
// 请求结束（下游已按需读完请求体）才能确定。
type requestBodyCapture struct {
	content   string
	truncated bool
	tap       *requestLogBodyTap
	// contentLength 是请求声明的长度（未知为 -1），下游没读完请求体时参与估算总大小。
	contentLength int64
}

// size 返回请求体字节数：已读到 EOF 时为实际读到的字节数；下游没读完（如鉴权拒绝、
// 超限）时取声明的 Content-Length 与已读字节数中的较大者——未声明长度（chunked）时
// 只能给出下界；压缩请求经 DecompressRequestMiddleware 解压后，声明长度是压缩前的，
// 可能小于已读到的解压字节数。
func (rc requestBodyCapture) size() int64 {
	if rc.tap == nil {
		return 0
	}
	read, drained := rc.tap.consumed()
	if drained {
		return read
	}
	return max(read, rc.contentLength)
}

// captureRequestBody 读取请求体的前 maxBytes+1 字节作为日志内容，并把 c.Request.Body
// 换成"回放已读前缀 + 继续读原始流"的读取器。非文本请求体不读正文，只统计大小。
//
// 只读有界前缀：本中间件排在鉴权之前，整读会让每个被拒请求都付出最多
// MAX_REQUEST_BODY_MB 的读取与缓存。也不调用 common.GetBodyStorage——它失败时已把
// 原始 body 读空并关闭，relay 随后只能拿到 "invalid Read on closed Body"，超限请求
// 得不到 413。读前缀时遇到的读错误在前缀回放完后原样交给下游，由 relay 自己处理。
func captureRequestBody(c *gin.Context, maxBytes int) requestBodyCapture {
	if c.Request == nil || c.Request.Body == nil || c.Request.Body == http.NoBody {
		return requestBodyCapture{}
	}
	rc := requestBodyCapture{contentLength: c.Request.ContentLength}
	tap := &requestLogBodyTap{src: c.Request.Body}
	if isTextualContentType(c.Request.Header.Get("Content-Type")) {
		prefix, eof, err := readBodyPrefix(tap.src, maxBytes+1, rc.contentLength)
		tap.prefix, tap.prefixLen, tap.srcErr = prefix, int64(len(prefix)), err
		if eof {
			tap.drained.Store(true)
		}
		if len(prefix) > maxBytes {
			rc.content, rc.truncated = string(prefix[:maxBytes]), true
		} else {
			rc.content = string(prefix)
		}
	} else {
		rc.content = "[non-textual request body omitted]"
	}
	rc.tap = tap
	c.Request.Body = tap
	return rc
}

// requestLogBodyTap 先回放日志已读走的前缀，再继续读原始请求体，并统计读到的总
// 字节数。计数用原子量：个别路径把请求体交给 http.Transport 的写 goroutine 读取。
type requestLogBodyTap struct {
	src       io.ReadCloser
	prefix    []byte
	prefixLen int64
	// srcErr 是读前缀时原始请求体返回的错误，前缀回放完后原样交给下游。
	srcErr  error
	srcRead atomic.Int64
	drained atomic.Bool
}

func (t *requestLogBodyTap) Read(p []byte) (int, error) {
	if len(t.prefix) > 0 {
		n := copy(p, t.prefix)
		t.prefix = t.prefix[n:]
		if len(t.prefix) == 0 {
			t.prefix = nil // 回放完即释放，不跟着请求存活到结束
		}
		return n, nil
	}
	if t.srcErr != nil {
		return 0, t.srcErr
	}
	if t.drained.Load() {
		return 0, io.EOF
	}
	n, err := t.src.Read(p)
	t.srcRead.Add(int64(n))
	if err == io.EOF {
		t.drained.Store(true)
	}
	return n, err
}

func (t *requestLogBodyTap) Close() error { return t.src.Close() }

// consumed 返回从原始请求体读到的总字节数，以及是否已读到 EOF。
func (t *requestLogBodyTap) consumed() (int64, bool) {
	return t.prefixLen + t.srcRead.Load(), t.drained.Load()
}

// captureResponseBody 根据返回 Content-Type 决定是否保留缓存的返回体。
func captureResponseBody(c *gin.Context, rbw *responseBodyWriter) string {
	contentType := rbw.Header().Get("Content-Type")
	if contentType != "" && !isTextualContentType(contentType) {
		return "[non-textual response body omitted]"
	}
	return rbw.body.String()
}

// readBodyPrefix 从 r 最多读 limit 字节，返回 (内容, 是否已读到 EOF, 非 EOF 的读错误)。
//
// sizeHint 为声明的总字节数（未知传 -1），只用来一次性精确分配：该函数在每个 relay
// 请求上执行，按上限（默认 64 KB）预分配会让小请求体也付出大块分配与 GC 成本。
// 提示不准时照样按实际读到的内容判定。
//
// 只有 r 自己返回的 io.EOF 才算读完。不用 io.ReadFull：它把"读了一部分后遇到
// EOF"改写成 io.ErrUnexpectedEOF，与 net/http 在客户端没发完 Content-Length /
// chunked 请求体就断开时返回的 io.ErrUnexpectedEOF 无法区分——后者当成正常结束，
// relay 会拿着截断的请求体报 400，日志也会把它记成完整读完。
func readBodyPrefix(r io.Reader, limit int, sizeHint int64) ([]byte, bool, error) {
	if limit <= 0 {
		return nil, false, nil
	}
	want := limit
	if sizeHint >= 0 && sizeHint < int64(limit) {
		// 多要 1 字节：读满说明提示偏小，读不满即已到 EOF。
		want = int(sizeHint) + 1
	}
	buf := make([]byte, want)
	n, eof, err := readUntilFull(r, buf)
	data := buf[:n]
	switch {
	case err != nil:
		return data, false, err
	case eof:
		return data, true, nil
	case n == limit:
		return data, false, nil
	}
	// 提示偏小：实际内容比提示长，按上限扩容后补读。
	grown := make([]byte, limit)
	copy(grown, data)
	m, eof, err := readUntilFull(r, grown[n:])
	data = grown[:n+m]
	if err != nil {
		return data, false, err
	}
	return data, eof, nil
}

// readUntilFull 反复读 r 直到填满 buf、r 返回 io.EOF 或其他错误。eof 只在 r 自己
// 返回 io.EOF 时为 true；其余错误原样返回。
func readUntilFull(r io.Reader, buf []byte) (n int, eof bool, err error) {
	for n < len(buf) {
		m, readErr := r.Read(buf[n:])
		n += m
		if readErr == io.EOF {
			return n, true, nil
		}
		if readErr != nil {
			return n, false, readErr
		}
	}
	return n, false, nil
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
