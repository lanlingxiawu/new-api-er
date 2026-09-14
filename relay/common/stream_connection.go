package common

import (
	"context"
	"io"
	"net/http"
)

// StreamConnectionLifetime 把连接关闭绑定请求取消，不增加常驻读取 goroutine。
// 参数 ctx 为不可变请求上下文，conn 为本次上游连接；返回清理函数会等待已启动的取消回调退出。
func StreamConnectionLifetime(ctx context.Context, conn io.Closer) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(done); _ = conn.Close() })
	return func() {
		if !stop() {
			<-done
		}
		_ = conn.Close()
	}
}

// ObserveStreamHandshake 只登记 WS 握手的原始响应头；随后收到的上游载荷由 WriteStreamPayload 采集。
// 参数 resp 为握手响应，err 为连接错误；不读取请求或握手失败时未被业务读取的响应体。
func (s *StreamResponseCapture) ObserveStreamHandshake(resp *http.Response, err error) {
	if resp == nil {
		s.observe(nil, err)
		return
	}
	s.observe(&http.Response{StatusCode: resp.StatusCode, Header: resp.Header}, err)
}
