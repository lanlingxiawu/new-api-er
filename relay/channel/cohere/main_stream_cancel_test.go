package cohere

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cancellationBody struct {
	entered chan struct{}
	closed  chan struct{}
	exited  chan struct{}
	once    sync.Once
}

func (b *cancellationBody) Read([]byte) (int, error) {
	close(b.entered)
	<-b.closed
	close(b.exited)
	return 0, io.ErrClosedPipe
}

func (b *cancellationBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestMainStreamCancellationReleasesReader(t *testing.T) {
	body := &cancellationBody{entered: make(chan struct{}), closed: make(chan struct{}), exited: make(chan struct{})}
	t.Cleanup(func() { _ = body.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(newCloseNotifyRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command"}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cohereStreamHandler(c, info, &http.Response{Body: body})
	}()
	select {
	case <-body.entered:
	case <-time.After(time.Second * 5):
		require.FailNow(t, "reader did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second * 5):
		require.FailNow(t, "handler did not release its reader")
	}
	select {
	case <-body.exited:
	default:
		require.FailNow(t, "handler returned before reader exited")
	}
}
