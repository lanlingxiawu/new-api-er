package retryguard

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func testRule() operation_setting.GroupRetryGuardRule {
	return operation_setting.GroupRetryGuardRule{Enabled: true, WindowSeconds: 10, RPMThreshold: 60, LatencyThresholdMs: 1000, SlowCountThreshold: 2, CooldownMode: "progressive", CooldownSeconds: 10, CooldownMultiplier: 2, MaxCooldownSeconds: 25, ResetAfterSeconds: 30}
}

func TestRedisProtectionLifecycle(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	rule := testRule()
	seq := 0
	tick := func(second int64, requests, slow int64) GroupStatus {
		server.SetTime(time.Unix(second, 0))
		seq++
		status, err := updateGroup(context.Background(), client, "test:{g}", "node", fmt.Sprint(seq), rule, requests, slow)
		require.NoError(t, err)
		return status
	}
	require.False(t, tick(1000, 0, 0).Ready)
	for sec := int64(1001); sec < 1010; sec++ {
		require.Zero(t, tick(sec, 1, 0).RetryDisabledUntil)
	}
	status := tick(1010, 1, 2)
	require.True(t, status.Ready)
	require.Equal(t, int64(1020), status.RetryDisabledUntil)
	require.Equal(t, 1, status.CooldownLevel)
	require.Equal(t, int64(1020), tick(1011, 20, 20).RetryDisabledUntil)
	for sec := int64(1012); sec <= 1020; sec++ {
		status = tick(sec, 1, 0)
	}
	require.Zero(t, status.RetryDisabledUntil)
	require.Zero(t, status.SlowCount)
	require.Equal(t, int64(1041), tick(1021, 10, 2).RetryDisabledUntil)
	for sec := int64(1022); sec <= 1041; sec++ {
		tick(sec, 1, 0)
	}
	status = tick(1042, 10, 2)
	require.Equal(t, 25, status.CurrentCooldownSeconds)
	for sec := int64(1043); sec <= 1097; sec++ {
		status = tick(sec, 1, 0)
	}
	require.Zero(t, status.CooldownLevel)
	require.Equal(t, 10, tick(1098, 10, 2).CurrentCooldownSeconds)
}

func TestRedisWindowsDeduplicationAndIsolation(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1000, 0))
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	rule := testRule()
	rule.WindowSeconds = 120
	for _, node := range []string{"a", "a", "b"} {
		_, err := updateGroup(context.Background(), client, "test:{g}", node, "1", rule, 300, 0)
		require.NoError(t, err)
	}
	status, err := updateGroup(context.Background(), client, "test:{g}", "a", "2", rule, 0, 0)
	require.NoError(t, err)
	require.Equal(t, float64(300), status.RPM)
	other, err := updateGroup(context.Background(), client, "test:{other}", "a", "1", rule, 0, 0)
	require.NoError(t, err)
	require.Zero(t, other.RPM)
	server.SetTime(time.Unix(1120, 0))
	status, err = updateGroup(context.Background(), client, "test:{g}", "a", "3", rule, 0, 0)
	require.NoError(t, err)
	require.Zero(t, status.RPM)
	require.False(t, status.Ready)
}

func TestRollingRPMUsesConfiguredWindow(t *testing.T) {
	for _, window := range []int{10, 60, 120} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { client.Close() })
			rule := testRule()
			rule.WindowSeconds = window
			rule.RPMThreshold = 10000000
			var counts []int64
			for second := 0; second < window*2+1; second++ {
				server.SetTime(time.Unix(int64(1000+second), 0))
				count := int64(second % 3)
				counts = append(counts, count)
				status, err := updateGroup(context.Background(), client, "rolling:{g}", "node", fmt.Sprint(second), rule, count, 0)
				require.NoError(t, err)
				var total int64
				for i := max(0, second-window+1); i <= second; i++ {
					total += counts[i]
				}
				require.InDelta(t, float64(total)*60/float64(window), status.RPM, 0.000001)
			}
		})
	}
}
