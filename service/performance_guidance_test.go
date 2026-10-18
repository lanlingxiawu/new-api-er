package service

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestCacheGuidanceBudgets(t *testing.T) {
	now := time.Unix(1000, 0)
	base := CacheResources{SampledAt: now.UnixMilli(), MemoryScope: "host", MemoryTotal: 2 << 30, MemoryAvailable: 512 << 20, DiskTotal: 50 << 30, DiskFree: 9 << 30}
	for _, tc := range []struct {
		name         string
		change       func(*CacheResources, *common.DiskCacheStats)
		memory, disk int64
	}{
		{"small host", func(*CacheResources, *common.DiskCacheStats) {}, 128, 1024},
		{"low memory", func(r *CacheResources, _ *common.DiskCacheStats) { r.MemoryAvailable = 1 << 20 }, 0, 1024},
		{"disk reserve boundary", func(r *CacheResources, _ *common.DiskCacheStats) { r.DiskFree = 5 << 30 }, 128, 0},
		{"limited", func(r *CacheResources, _ *common.DiskCacheStats) { r.MemoryScope = "limited" }, 0, 1024},
		{"unknown", func(r *CacheResources, _ *common.DiskCacheStats) { r.MemoryScope = "unknown" }, 0, 1024},
		{"missing", func(r *CacheResources, _ *common.DiskCacheStats) { r.MemoryTotal = 0; r.DiskTotal = 0 }, 0, 0},
		{"invalid available", func(r *CacheResources, _ *common.DiskCacheStats) {
			r.MemoryAvailable = -1
			r.DiskFree = r.DiskTotal + 1
		}, 0, 0},
		{"expired", func(r *CacheResources, _ *common.DiskCacheStats) { r.SampledAt -= 30001 }, 0, 0},
		{"expiry boundary", func(r *CacheResources, _ *common.DiskCacheStats) { r.SampledAt -= 30000 }, 128, 1024},
		{"negative reservations", func(_ *CacheResources, s *common.DiskCacheStats) {
			s.BodyMemoryReservedBytes = -1
			s.DiskReservedBytes = -1
		}, 0, 0},
		{"future", func(r *CacheResources, _ *common.DiskCacheStats) { r.SampledAt += 1 }, 0, 0},
		{"overflow", func(_ *CacheResources, s *common.DiskCacheStats) {
			s.BodyMemoryReservedBytes = math.MaxInt64
			s.DiskReservedBytes = math.MaxInt64
		}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s := base, common.DiskCacheStats{}
			tc.change(&r, &s)
			g := BuildCacheGuidance(r, s, common.DiskCacheConfig{}, now)
			for _, pair := range []struct {
				got  CacheRecommendation
				want int64
			}{{g.Memory, tc.memory}, {g.Disk, tc.disk}} {
				if pair.want == 0 {
					require.Nil(t, pair.got.ValueMiB)
				} else {
					require.NotNil(t, pair.got.ValueMiB)
					require.Equal(t, pair.want, *pair.got.ValueMiB)
				}
			}
		})
	}
	base.MemoryTotal, base.MemoryAvailable = 503<<30, 378<<30
	s := common.DiskCacheStats{BodyMemoryReservedBytes: 65 << 30}
	g := BuildCacheGuidance(base, s, common.DiskCacheConfig{}, now)
	require.NotNil(t, g.Memory.ValueMiB)
	require.LessOrEqual(t, *g.Memory.ValueMiB<<20, base.MemoryTotal/10)
	require.True(t, g.Memory.BelowUsage)
	// Reservation failures can include successful spill fallback: they do not change advice.
	s.BodyCapacityRejects = 10000
	require.Equal(t, g, BuildCacheGuidance(base, s, common.DiskCacheConfig{}, now))
}

func TestCollectCacheResourcesPath(t *testing.T) {
	base := t.TempDir()
	r, errs := CollectCacheResources(common.DiskCacheConfig{Path: base})
	require.Empty(t, errs)
	require.Equal(t, filepath.Join(base, "new-api-body-cache"), r.CachePath)
	require.Positive(t, r.DiskTotal)
	require.Positive(t, r.SampledAt)
	_, err := os.Stat(r.CachePath)
	require.True(t, os.IsNotExist(err), "sampling must not create a cache directory")
	r, errs = CollectCacheResources(common.DiskCacheConfig{Path: filepath.Join(base, "missing")})
	require.NotEmpty(t, errs)
	require.Zero(t, r.DiskTotal)
	require.Nil(t, BuildCacheGuidance(r, common.DiskCacheStats{}, common.DiskCacheConfig{}, time.Now()).Disk.ValueMiB)
}

func TestCacheGuidanceThreshold(t *testing.T) {
	now := time.Now()
	r := CacheResources{SampledAt: now.UnixMilli(), DiskTotal: 50 << 30, DiskFree: 9 << 30}
	cfg := common.DiskCacheConfig{GovernanceEnabled: true, Enabled: true, MemoryBudgetMB: 100, ThresholdMB: 10}
	s := common.DiskCacheStats{BodyMemoryReservedBytes: (80 << 20) - 1}
	require.Nil(t, BuildCacheGuidance(r, s, cfg, now).Threshold.ValueMiB)
	s.BodyMemoryReservedBytes++
	require.Equal(t, int64(1), *BuildCacheGuidance(r, s, cfg, now).Threshold.ValueMiB)
	cfg.Enabled = false
	require.Nil(t, BuildCacheGuidance(r, s, cfg, now).Threshold.ValueMiB)
	cfg.Enabled = true
	r.DiskFree = 1 << 30
	require.Nil(t, BuildCacheGuidance(r, s, cfg, now).Threshold.ValueMiB)
}

func TestCacheMemoryScope(t *testing.T) {
	for _, tc := range []struct {
		name, limit, mount, scope string
		container                 bool
	}{
		{"unlimited", "max", "/", "host", false},
		{"parent limit", "1073741824", "/", "limited", false},
		{"unknown limit", "invalid", "/", "unknown", false},
		{"hidden ancestor", "max", "/hidden", "unknown", false},
		{"container", "max", "/", "unknown", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"/proc/self/cgroup": "0::/user.slice/test.scope\n", "/proc/self/mountinfo": "1 0 0:1 " + tc.mount + " /sys/fs/cgroup rw - cgroup2 cgroup rw\n", "/sys/fs/cgroup/user.slice/test.scope/memory.max": "max", "/sys/fs/cgroup/user.slice/memory.max": tc.limit}
			read := func(path string) ([]byte, error) {
				if s, ok := files[path]; ok {
					return []byte(s), nil
				}
				return nil, os.ErrNotExist
			}
			require.Equal(t, tc.scope, cacheMemoryScope(tc.container, "linux", read))
		})
	}
	require.Equal(t, "unknown", cacheMemoryScope(false, "windows", os.ReadFile))
	require.Equal(t, "unknown", cacheMemoryScope(false, "linux", func(string) ([]byte, error) { return nil, os.ErrPermission }))
}
