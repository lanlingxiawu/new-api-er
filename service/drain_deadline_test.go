package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// slowBody 模拟“慢速涓流”上游：先给出少量数据，随后阻塞直到被 Close。
// 总量远小于 drainResponseBodyLimit，因此字节上限拦不住它——只有时间边界能。
type slowBody struct {
	sent   bool
	closed chan struct{}
}

func newSlowBody() *slowBody { return &slowBody{closed: make(chan struct{})} }

func (s *slowBody) Read(p []byte) (int, error) {
	if !s.sent {
		s.sent = true
		p[0] = 'x'
		return 1, nil
	}
	<-s.closed // 一直阻塞，直到 Close 被调用
	return 0, io.EOF
}

func (s *slowBody) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

// 无 deadline 且 RELAY_TIMEOUT=0 时，排空必须被跳过，直接关闭，绝不阻塞。
func TestDrainAndClose_NoDeadline_DoesNotBlock(t *testing.T) {
	prev := common.RelayTimeout
	common.RelayTimeout = 0
	defer func() { common.RelayTimeout = prev }()

	req, err := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	body := newSlowBody()
	resp := &http.Response{Body: body, Request: req}

	done := make(chan struct{})
	go func() {
		DrainAndCloseResponseBody(resp)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("排空在无 deadline 的响应上阻塞了，应直接关闭而不排空")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("body 未被关闭")
	}
}

// 请求带 deadline 时应照常排空（deadline 由 transport 保证不会无限等待）。
func TestDrainAndClose_WithDeadline_StillDrains(t *testing.T) {
	prev := common.RelayTimeout
	common.RelayTimeout = 0
	defer func() { common.RelayTimeout = prev }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	DrainAndCloseResponseBody(resp)

	// 已排空并关闭：再次读取应立即返回错误/EOF，而不是阻塞。
	buf := make([]byte, 1)
	if _, err := resp.Body.Read(buf); err == nil {
		t.Fatal("期望 body 已关闭")
	}
}

// 配置了全局 RELAY_TIMEOUT 时，客户端超时覆盖读 body，应照常排空。
func TestDrainAndClose_RelayTimeoutSet_StillDrains(t *testing.T) {
	prev := common.RelayTimeout
	common.RelayTimeout = 30
	defer func() { common.RelayTimeout = prev }()

	req, err := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	body := &countingBody{data: []byte("hello")}
	resp := &http.Response{Body: body, Request: req}

	DrainAndCloseResponseBody(resp)

	if body.pos != len(body.data) {
		t.Fatalf("期望完整排空 %d 字节，实际 %d", len(body.data), body.pos)
	}
	if body.closeCount != 1 {
		t.Fatalf("期望关闭一次，实际 %d", body.closeCount)
	}
}

// Request 为 nil（内存构造的响应，不涉及网络）时应照常排空。
func TestDrainAndClose_NilRequest_StillDrains(t *testing.T) {
	prev := common.RelayTimeout
	common.RelayTimeout = 0
	defer func() { common.RelayTimeout = prev }()

	body := &countingBody{data: []byte("in-memory")}
	resp := &http.Response{Body: body}

	DrainAndCloseResponseBody(resp)

	if body.pos != len(body.data) {
		t.Fatalf("期望完整排空 %d 字节，实际 %d", len(body.data), body.pos)
	}
}

type countingBody struct {
	data       []byte
	pos        int
	closeCount int
}

func (c *countingBody) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := copy(p, c.data[c.pos:])
	c.pos += n
	return n, nil
}

func (c *countingBody) Close() error {
	c.closeCount++
	return nil
}
