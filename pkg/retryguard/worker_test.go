package retryguard

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func withGuardPolicy(t testing.TB) {
	t.Helper()
	old := operation_setting.GetGroupRetryGuardSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryGuardSetting(old) })
	raw, err := common.Marshal(map[string]operation_setting.GroupRetryGuardRule{"g": testRule()})
	require.NoError(t, err)
	operation_setting.ReplaceGroupRetryGuardSetting(operation_setting.GroupRetryGuardSetting{Enabled: true, Rules: string(raw), Revision: "test"})
}

func TestSnapshotExpiresAndConfigChangeBypasses(t *testing.T) {
	withGuardPolicy(t)
	now := time.Now()
	e := &engine{}
	require.False(t, e.disabled("g", now))
	e.snapshot.Store(&runtimeSnapshot{revision: "test", updatedAt: now, groups: map[string]groupView{"g": {until: now.Add(time.Minute)}}})
	require.True(t, e.disabled("g", now))
	require.False(t, e.disabled("other", now))
	require.False(t, e.disabled("g", now.Add(6*time.Second)))
	draft := operation_setting.GetGroupRetryGuardSetting()
	draft.Enabled = false
	operation_setting.ReplaceGroupRetryGuardSetting(draft)
	require.False(t, e.disabled("g", now))
	draft.Enabled = true
	draft.Revision = "new"
	operation_setting.ReplaceGroupRetryGuardSetting(draft)
	require.False(t, e.disabled("g", now))
}

func TestObservationQueueNeverBlocks(t *testing.T) {
	withGuardPolicy(t)
	old := runtime
	runtime = &engine{events: make(chan sample, 1)}
	runtime.running.Store(true)
	t.Cleanup(func() { runtime = old })
	Observe("other", 9000, time.Now().Unix())
	require.Empty(t, runtime.events)
	Observe("g", 9000, time.Now().Unix())
	Observe("g", 9000, time.Now().Unix())
	require.Len(t, runtime.events, 1)
	require.Equal(t, uint64(1), runtime.dropped.Load())
	runtime.running.Store(false)
	Observe("g", 9000, time.Now().Unix())
	require.Equal(t, uint64(1), runtime.dropped.Load())
}

func TestWorkerPublishesOnlyCompleteSuccessfulBatches(t *testing.T) {
	withGuardPolicy(t)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	e := &engine{}
	loaded := false
	policy := operation_setting.GroupRetryGuardSnapshot()
	server.SetTime(time.Unix(1000, 0))
	require.NoError(t, e.flush(context.Background(), client, policy, nil, "node", "1", &loaded))
	for i := int64(1001); i <= 1010; i++ {
		server.SetTime(time.Unix(i, 0))
		require.NoError(t, e.flush(context.Background(), client, policy, map[string]counters{"g": {requests: 2, slow: 2}}, "node", time.Unix(i, 0).String(), &loaded))
	}
	require.True(t, e.disabled("g", time.Now()))
	snapshot := e.snapshot.Load()
	require.NoError(t, client.Close())
	require.Error(t, e.flush(context.Background(), client, policy, nil, "node", "next", &loaded))
	require.Same(t, snapshot, e.snapshot.Load())
	require.False(t, e.disabled("g", snapshot.updatedAt.Add(6*time.Second)))
	require.Error(t, e.flush(context.Background(), nil, policy, nil, "node", "panic", &loaded), "panic stays inside worker")
}

func TestFixedCooldownAndIndependentThresholds(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	rule := testRule()
	rule.CooldownMode = "fixed"
	rule.CooldownMultiplier = 1
	rule.MaxCooldownSeconds = 10
	server.SetTime(time.Unix(1000, 0))
	_, err := updateGroup(context.Background(), client, "threshold:{g}", "n", "0", rule, 0, 0)
	require.NoError(t, err)
	for i := int64(1001); i <= 1010; i++ {
		server.SetTime(time.Unix(i, 0))
		_, err = updateGroup(context.Background(), client, "threshold:{g}", "n", time.Unix(i, 0).String(), rule, 0, 0)
		require.NoError(t, err)
	}
	s, err := updateGroup(context.Background(), client, "threshold:{g}", "n", "a", rule, 1, 2)
	require.NoError(t, err)
	require.Zero(t, s.RetryDisabledUntil)
	s, err = updateGroup(context.Background(), client, "threshold:{g}", "n", "b", rule, 9, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1020), s.RetryDisabledUntil)
	for i := int64(1011); i <= 1020; i++ {
		server.SetTime(time.Unix(i, 0))
		_, err = updateGroup(context.Background(), client, "threshold:{g}", "n", time.Unix(i, 0).String(), rule, 1, 0)
		require.NoError(t, err)
	}
	// Another node flushing at the same recovery second cannot reuse old slow samples.
	s, err = updateGroup(context.Background(), client, "threshold:{g}", "other", "a", rule, 1, 9)
	require.NoError(t, err)
	require.Zero(t, s.RetryDisabledUntil)
	server.SetTime(time.Unix(1021, 0))
	s, err = updateGroup(context.Background(), client, "threshold:{g}", "n", "last", rule, 10, 2)
	require.NoError(t, err)
	require.Equal(t, 10, s.CurrentCooldownSeconds)
}

func BenchmarkRetryGuardDecision(b *testing.B) {
	withGuardPolicy(b)
	now := time.Now()
	e := &engine{}
	e.snapshot.Store(&runtimeSnapshot{revision: "test", updatedAt: now, groups: map[string]groupView{"g": {until: now.Add(time.Minute)}}})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.disabled("g", now)
	}
}

func TestProtectionPreservesFirstUpstreamAttempt(t *testing.T) {
	withGuardPolicy(t)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(http.StatusTooManyRequests) }))
	t.Cleanup(upstream.Close)
	now := time.Now()
	e := &engine{}
	e.snapshot.Store(&runtimeSnapshot{revision: "test", updatedAt: now, groups: map[string]groupView{"g": {until: now.Add(time.Minute)}}})
	for _, tc := range []struct {
		group string
		want  int32
	}{{"g", 1}, {"unconfigured", 3}} {
		hits.Store(0)
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 && e.disabled(tc.group, now) {
				break
			}
			response, err := upstream.Client().Get(upstream.URL)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
		}
		require.Equal(t, tc.want, hits.Load())
	}
}

func BenchmarkRetryGuardFullQueue(b *testing.B) {
	withGuardPolicy(b)
	old := runtime
	runtime = &engine{events: make(chan sample, 1)}
	runtime.running.Store(true)
	b.Cleanup(func() { runtime = old })
	now := time.Now().Unix()
	Observe("g", 1000, now)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Observe("g", 1000, now)
	}
}
