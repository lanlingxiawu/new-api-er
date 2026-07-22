package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

func CloseResponseBodyGracefully(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	err := httpResponse.Body.Close()
	if err != nil {
		common.SysError("failed to close response body: " + err.Error())
	}
}

// drainResponseBodyLimit 排空响应体时的最大读取上限。
// 在此上限内排空可让底层连接归还连接池被复用；超过上限说明响应体过大，
// 不值得为复用而继续读取（尤其在客户端无超时时会长时间阻塞），直接关闭丢弃即可。
const drainResponseBodyLimit = 512 * 1024

// DrainAndCloseResponseBody 先将剩余响应体丢弃到 EOF（带上限，避免为复用连接
// 而读取超大响应体导致长时间阻塞），再关闭 body。
// 适用于调用方不再需要响应内容、或只读取了部分内容的场景（错误分支、非 2xx 分支等）。
// 与仅关闭的 CloseResponseBodyGracefully 不同：本函数保证连接尽可能可复用。
// 注意：对 SSE / 流式响应不要使用本函数——那种场景关闭 body 的目的是主动中断上游流，
// 排空会一直读到上游结束。
func DrainAndCloseResponseBody(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(httpResponse.Body, drainResponseBodyLimit))
	CloseResponseBodyGracefully(httpResponse)
}

// ShouldCopyUpstreamHeader checks whether a given upstream response header
// should be copied to the client response. It returns false for Content-Length
// (managed separately) and X-Oneapi-Request-Id (to preserve the local instance
// ID). When the upstream header is X-Oneapi-Request-Id, the value is captured
// into the Gin context for later logging.
func ShouldCopyUpstreamHeader(c *gin.Context, k string, v []string) bool {
	if strings.EqualFold(k, "Content-Length") {
		return false
	}
	if strings.EqualFold(k, common.RequestIdKey) {
		if c != nil && len(v) > 0 {
			c.Set(common.UpstreamRequestIdKey, v[0])
		}
		return false
	}
	return true
}

func IOCopyBytesGracefully(c *gin.Context, src *http.Response, data []byte) {
	if c.Writer == nil {
		return
	}

	body := io.NopCloser(bytes.NewBuffer(data))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the httpClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	if src != nil {
		for k, v := range src.Header {
			if !ShouldCopyUpstreamHeader(c, k, v) {
				continue
			}
			c.Writer.Header().Set(k, v[0])
		}
	}

	// set Content-Length header manually BEFORE calling WriteHeader
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write header with status code (this sends the headers)
	if src != nil {
		c.Writer.WriteHeader(src.StatusCode)
	} else {
		c.Writer.WriteHeader(http.StatusOK)
	}

	_, err := io.Copy(c.Writer, body)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("failed to copy response body: %s", err.Error()))
	}
	c.Writer.Flush()
}
