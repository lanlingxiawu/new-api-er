package middleware

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

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

		maxBytes := common.RequestLogMaxBodyKB * 1024
		if maxBytes <= 0 {
			maxBytes = 64 * 1024
		}

		// 捕获下游请求头与请求体快照（请求体读取后会复位，供后续 relay 复用）
		requestHeaders := headersToString(http.Header(c.Request.Header))
		requestBody, reqTruncated, reqBodySize := captureRequestBody(c, maxBytes)

		// 包装 writer 以捕获返回体
		rbw := &responseBodyWriter{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
			limit:          maxBytes,
		}
		c.Writer = rbw

		c.Next()

		// 用户名过滤：RequestLogUsername 为空记录全部，否则仅记录匹配的登录用户名
		// （比较的是账号的登录用户名 username，不是显示名/邮箱）。大小写不敏感、去除首尾空白。
		username, matched := matchRequestLogUsername(c)
		if !matched {
			return
		}

		entry := &model.RequestLog{
			CreatedAt:        common.GetTimestamp(),
			UserId:           c.GetInt("id"),
			Username:         username,
			TokenName:        c.GetString("token_name"),
			ModelName:        c.GetString("original_model"),
			ChannelId:        c.GetInt("channel_id"),
			Method:           c.Request.Method,
			Url:              truncateString(c.Request.URL.RequestURI(), 2000),
			StatusCode:       c.Writer.Status(),
			Ip:               c.ClientIP(),
			RequestId:        c.GetString(common.RequestIdKey),
			IsStream:         c.GetBool("is_stream"),
			RequestBodySize:  reqBodySize,
			ResponseBodySize: rbw.totalSize,
			RequestHeaders:   requestHeaders,
			RequestBody:      appendTruncatedMark(requestBody, reqTruncated),
			ResponseHeaders:  headersToString(http.Header(rbw.Header())),
			ResponseBody:     appendTruncatedMark(captureResponseBody(c, rbw), rbw.truncated),
		}

		gopool.Go(func() {
			model.RecordRequestLog(entry)
		})
	}
}

func matchRequestLogUsername(c *gin.Context) (string, bool) {
	username := getRequestLogUsername(c)
	filterUser := strings.TrimSpace(common.RequestLogUsername)
	if filterUser == "" {
		return username, true
	}
	return username, strings.EqualFold(username, filterUser)
}

func getRequestLogUsername(c *gin.Context) string {
	username := strings.TrimSpace(c.GetString("username"))
	if username != "" {
		return username
	}
	userId := c.GetInt("id")
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

func headersToString(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	str, err := common.Marshal(h)
	if err != nil {
		return ""
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
