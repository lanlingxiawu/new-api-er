package common

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// DownstreamCaptureWriter 位于流式语义包装器之下，只观察底层实际接受的字节。
// 嵌入的写入器保留 Header、Flush、Hijack 等行为，不主动刷新或关闭连接。
type DownstreamCaptureWriter struct {
	gin.ResponseWriter                        // 原有响应链，不保存暂存或过滤前的 SSE 输入。
	capture            *StreamResponseCapture // 当前尝试采集器，重试时由入口替换整个包装器。
}

// NewDownstreamCaptureWriter 包装原有 writer 并关联 capture；仅采集开启时由入口安装。
// 参数 writer：实际下游写入链；capture：本次尝试采集器；返回透明观察器。
func NewDownstreamCaptureWriter(writer gin.ResponseWriter, capture *StreamResponseCapture) *DownstreamCaptureWriter {
	return &DownstreamCaptureWriter{ResponseWriter: writer, capture: capture}
}

// Unwrap 返回原有写入链，使刷新错误和写入截止时间仍能传递到底层。
func (w *DownstreamCaptureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Write 先执行底层写入，再仅复制已接受的 p[:n]；返回值和错误原样保留，短写也采集成功部分。
func (w *DownstreamCaptureWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.capture.WriteDownstreamPayload(p[:min(n, len(p))])
	}
	return n, err
}

// WriteString 将字符串 s 交给同一观察边界，避免 Gin 的字符串快捷路径漏采集。
func (w *DownstreamCaptureWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
