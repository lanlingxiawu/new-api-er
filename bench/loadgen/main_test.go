package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkerRejectsInvalidSuccessBodies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream bool
		body   string
		valid  bool
	}{
		{"complete stream", true, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n", true},
		{"truncated stream", true, "data: {\"choices\":[]}\n\n", false},
		{"stream error", true, "data: {\"error\":{\"message\":\"failed\"}}\n\ndata: [DONE]\n\n", false},
		{"invalid stream JSON", true, "data: broken\n\ndata: [DONE]\n\n", false},
		{"empty stream", true, "data: [DONE]\n\n", false},
		{"completion", false, "{\"choices\":[{\"message\":{\"content\":\"ok\"}}]}", true},
		{"JSON error", false, "{\"error\":{\"message\":\"failed\"}}", false},
		{"empty choices", false, "{\"choices\":[]}", false},
		{"invalid JSON", false, "broken", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			oldTarget, oldRatio := *target, *streamRatio
			t.Cleanup(func() { *target, *streamRatio = oldTarget, oldRatio })
			*target, *streamRatio = srv.URL, 0
			if tc.stream {
				*streamRatio = 1
			}
			initPromptPool()
			st := &workerStat{codes: make(map[int]int64)}
			var done atomic.Int64
			worker(srv.Client(), time.Now().Add(20*time.Millisecond), st, &done)
			require.Positive(t, done.Load())
			if tc.valid {
				require.Zero(t, st.errN)
				require.Len(t, st.samples, int(done.Load()))
			} else {
				require.Equal(t, done.Load(), st.errN)
				require.Empty(t, st.samples)
			}
		})
	}
}

func TestWorkerHonorsScheduledRequestLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	oldTarget, oldRatio, oldRPS, oldStart := *target, *streamRatio, *targetRPS, loadStart
	oldScheduled := scheduled.Load()
	t.Cleanup(func() {
		*target, *streamRatio, *targetRPS, loadStart = oldTarget, oldRatio, oldRPS, oldStart
		scheduled.Store(oldScheduled)
	})
	*target, *streamRatio, *targetRPS = srv.URL, 0, 100
	initPromptPool()
	scheduled.Store(0)
	loadStart = time.Now()
	st := &workerStat{codes: make(map[int]int64)}
	var done atomic.Int64
	worker(srv.Client(), loadStart.Add(60*time.Millisecond), st, &done)
	require.Positive(t, done.Load())
	require.LessOrEqual(t, done.Load(), int64(6))
	require.Zero(t, st.errN)
}
