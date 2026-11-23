package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachUpstreamExchangeTraceCallsExistingHooksOnce(t *testing.T) {
	start := time.Now()
	clock := &relaycommon.UpstreamExchangeClock{}
	var acquiring, connected, wrote, firstByte atomic.Int64
	parent := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		GetConn:              func(string) { acquiring.Add(1) },
		GotConn:              func(httptrace.GotConnInfo) { connected.Add(1) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { wrote.Add(1) },
		GotFirstResponseByte: func() { firstByte.Add(1) },
	})
	ctx := AttachUpstreamExchangeTrace(parent, clock)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })

	mark := clock.Snapshot(start, time.Now())
	require.EqualValues(t, 1, acquiring.Load())
	require.EqualValues(t, 1, connected.Load())
	require.EqualValues(t, 1, wrote.Load())
	require.EqualValues(t, 1, firstByte.Load())
	require.NotNil(t, mark.ConnMs)
	require.NotNil(t, mark.GetConnMs)
	require.LessOrEqual(t, *mark.GetConnMs, *mark.ConnMs)
	require.NotNil(t, mark.WroteMs)
	require.NotNil(t, mark.WroteOK)
	require.True(t, *mark.WroteOK)
	require.NotNil(t, mark.FirstByteMs)
	require.NotNil(t, mark.EndMs)
	require.GreaterOrEqual(t, *mark.EndMs, *mark.FirstByteMs)
}

func TestUpstreamExchangeSnapshotOmitsMissingCallbacks(t *testing.T) {
	start := time.Now()
	mark := (&relaycommon.UpstreamExchangeClock{}).Snapshot(start, start.Add(20*time.Millisecond))
	require.Nil(t, mark.GetConnMs)
	require.Nil(t, mark.ConnMs)
	require.Nil(t, mark.WroteMs)
	require.Nil(t, mark.WroteOK)
	require.Nil(t, mark.FirstByteMs)
	require.NotNil(t, mark.EndMs)
	require.EqualValues(t, 20, *mark.EndMs)
}

func TestUpstreamExchangeKeepsFirstConnectionStart(t *testing.T) {
	start := time.Unix(1000, 0)
	clock := &relaycommon.UpstreamExchangeClock{}
	before := clock.Snapshot(start, start)
	clock.MarkGetConn(start.Add(100 * time.Millisecond))
	clock.MarkGetConn(start.Add(500 * time.Millisecond))
	clock.MarkConn(start.Add(1600 * time.Millisecond))
	mark := clock.Snapshot(start, start.Add(2*time.Second))
	require.Nil(t, before.GetConnMs, "late callbacks must not backfill a snapshot")
	require.NotNil(t, mark.GetConnMs)
	require.EqualValues(t, 100, *mark.GetConnMs)
	require.EqualValues(t, 1500, *mark.ConnMs-*mark.GetConnMs)
	zero := &relaycommon.UpstreamExchangeClock{}
	zero.MarkGetConn(time.Time{})
	require.Nil(t, zero.Snapshot(start, start).GetConnMs)
	zero.MarkGetConn(start)
	mark = zero.Snapshot(start, start)
	require.NotNil(t, mark.GetConnMs)
	require.Zero(t, *mark.GetConnMs)
	require.Nil(t, zero.Snapshot(time.Time{}, start).GetConnMs)
}

func TestUpstreamExchangeFirstWriteResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{name: "success"}, {name: "failure", err: errors.New("write failed")}} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			clock := &relaycommon.UpstreamExchangeClock{}
			var parentCalls int
			var parentErr error
			parent := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
				WroteRequest: func(info httptrace.WroteRequestInfo) {
					parentCalls++
					parentErr = info.Err
				},
			})
			trace := httptrace.ContextClientTrace(AttachUpstreamExchangeTrace(parent, clock))
			before := clock.Snapshot(start, start)
			trace.WroteRequest(httptrace.WroteRequestInfo{Err: tc.err})
			mark := clock.Snapshot(start, time.Now())
			require.Equal(t, 1, parentCalls)
			require.Equal(t, tc.err, parentErr)
			require.NotNil(t, mark.WroteMs)
			require.NotNil(t, mark.WroteOK)
			require.Equal(t, tc.err == nil, *mark.WroteOK)
			// A late callback cannot backfill an already submitted snapshot.
			require.Nil(t, before.WroteMs)
			require.Nil(t, before.WroteOK)
			trace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("retry write")})
			after := clock.Snapshot(start, time.Now())
			require.Equal(t, mark.WroteMs, after.WroteMs)
			require.Equal(t, mark.WroteOK, after.WroteOK)
			require.Equal(t, 2, parentCalls, "each transport event must reach the existing hook once")
		})
	}
}

func TestUpstreamExchangeConcurrentWriteSnapshot(t *testing.T) {
	start := time.Unix(1000, 0)
	clock := &relaycommon.UpstreamExchangeClock{}
	var workers sync.WaitGroup
	for i := range 100 {
		workers.Go(func() {
			ok := i%2 == 0
			ms := 200
			if ok {
				ms = 100
			}
			clock.MarkWrote(start.Add(time.Duration(ms)*time.Millisecond), ok)
			mark := clock.Snapshot(start, start.Add(time.Second))
			if assert.NotNil(t, mark.WroteMs) && assert.NotNil(t, mark.WroteOK) {
				assert.Equal(t, *mark.WroteMs == 100, *mark.WroteOK)
			}
		})
	}
	workers.Wait()
	zero := &relaycommon.UpstreamExchangeClock{}
	zero.MarkWrote(start, false)
	mark := zero.Snapshot(start, start)
	require.NotNil(t, mark.WroteMs)
	require.Zero(t, *mark.WroteMs)
	require.NotNil(t, mark.WroteOK)
	require.False(t, *mark.WroteOK)
	invalid := zero.Snapshot(time.Time{}, start)
	require.Nil(t, invalid.WroteMs)
	require.Nil(t, invalid.WroteOK)
}
