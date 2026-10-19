package service

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
)

// CacheResources is an administrative sample, not a relay admission decision.
type CacheResources struct {
	SampledAt       int64  `json:"sampled_at"`
	MemoryScope     string `json:"memory_scope"`
	MemoryTotal     int64  `json:"memory_total"`
	MemoryAvailable int64  `json:"memory_available"`
	DiskTotal       int64  `json:"disk_total"`
	DiskFree        int64  `json:"disk_free"`
	CachePath       string `json:"cache_path"`
}

type CacheRecommendation struct {
	ValueMiB   *int64 `json:"value_mib"`
	Reason     string `json:"reason"`
	BelowUsage bool   `json:"below_usage"`
}

type CacheGuidance struct {
	Resources CacheResources      `json:"resources"`
	Memory    CacheRecommendation `json:"memory"`
	Disk      CacheRecommendation `json:"disk"`
	Threshold CacheRecommendation `json:"threshold"`
}

// BuildCacheGuidance uses conservative starting heuristics. It never changes settings.
func BuildCacheGuidance(r CacheResources, stats common.DiskCacheStats, cfg common.DiskCacheConfig, now time.Time) CacheGuidance {
	g := CacheGuidance{Resources: r, Memory: CacheRecommendation{Reason: "unavailable"}, Disk: CacheRecommendation{Reason: "unavailable"}, Threshold: CacheRecommendation{Reason: "unchanged"}}
	if r.SampledAt <= 0 || r.SampledAt > now.UnixMilli() || now.UnixMilli()-r.SampledAt > 30000 {
		g.Memory.Reason, g.Disk.Reason, g.Threshold.Reason = "stale", "stale", "stale"
		return g
	}
	if r.MemoryScope != "host" {
		g.Memory.Reason = "scope_unknown"
	} else if r.MemoryTotal > 0 && r.MemoryAvailable >= 0 && r.MemoryAvailable <= r.MemoryTotal && stats.BodyMemoryReservedBytes >= 0 && stats.BodyMemoryReservedBytes <= math.MaxInt64-r.MemoryAvailable/4 {
		candidate := min(r.MemoryTotal/10, stats.BodyMemoryReservedBytes+r.MemoryAvailable/4) / (16 << 20) * 16
		g.Memory.Reason = "low_headroom"
		if candidate >= 16 {
			g.Memory = CacheRecommendation{ValueMiB: &candidate, Reason: "memory_headroom", BelowUsage: candidate<<20 < stats.BodyMemoryReservedBytes}
		}
	}
	if r.DiskTotal > 0 && r.DiskFree >= 0 && r.DiskFree <= r.DiskTotal && stats.DiskReservedBytes >= 0 {
		headroom := max(int64(0), r.DiskFree-max(int64(2<<30), r.DiskTotal/10))
		if stats.DiskReservedBytes <= math.MaxInt64-headroom/4 {
			candidate := min(r.DiskTotal/10, stats.DiskReservedBytes+headroom/4) / (64 << 20) * 64
			g.Disk.Reason = "low_headroom"
			if headroom > 0 && candidate >= 100 {
				g.Disk = CacheRecommendation{ValueMiB: &candidate, Reason: "disk_headroom", BelowUsage: candidate<<20 < stats.DiskReservedBytes}
			}
		}
	}
	if cfg.GovernanceEnabled && cfg.Enabled && cfg.ThresholdMB > 1 && cfg.MemoryBudgetMB > 0 && uint64(cfg.MemoryBudgetMB) <= uint64(math.MaxInt64>>20) && g.Disk.ValueMiB != nil {
		budget := int64(cfg.MemoryBudgetMB) << 20
		// ceil(80% * budget), without multiplying a potentially large budget by 4.
		pressureAt := budget - budget/5
		if stats.BodyMemoryReservedBytes >= pressureAt {
			value := int64(1)
			g.Threshold = CacheRecommendation{ValueMiB: &value, Reason: "check_disk_latency"}
		}
	}
	return g
}

// CollectCacheResources performs bounded administrative OS reads only. Unknown
// cgroup layouts deliberately suppress memory advice rather than assume host RAM.
func CollectCacheResources(cfg common.DiskCacheConfig) (CacheResources, []error) {
	r := CacheResources{MemoryScope: cacheMemoryScope(common.IsRunningInContainer(), runtime.GOOS, os.ReadFile)}
	var errors []error
	info, err := mem.VirtualMemory()
	if err != nil {
		errors = append(errors, fmt.Errorf("sample host memory: %w", err))
	} else if info.Total <= math.MaxInt64 && info.Available <= math.MaxInt64 {
		r.MemoryTotal, r.MemoryAvailable = int64(info.Total), int64(info.Available)
	}
	base := cfg.Path
	if base == "" {
		base = os.TempDir()
	}
	r.CachePath = filepath.Join(base, "new-api-body-cache")
	probe := r.CachePath
	if _, err := os.Stat(probe); os.IsNotExist(err) {
		probe = base
	}
	diskInfo, err := disk.Usage(probe)
	if err != nil {
		errors = append(errors, fmt.Errorf("sample cache filesystem: %w", err))
	} else if diskInfo.Total <= math.MaxInt64 && diskInfo.Free <= math.MaxInt64 {
		r.DiskTotal, r.DiskFree = int64(diskInfo.Total), int64(diskInfo.Free)
	}
	r.SampledAt = time.Now().UnixMilli()
	return r, errors
}

// Only a visible, unlimited cgroup-v2 ancestry establishes the host-memory scope.
// v1, containers, delegated mounts and unreadable files remain unknown in this version.
func cacheMemoryScope(container bool, goos string, readFile func(string) ([]byte, error)) string {
	if container || goos != "linux" {
		return "unknown"
	}
	mounts, err := readFile("/proc/self/mountinfo")
	if err != nil {
		return "unknown"
	}
	visibleRoot := false
	for _, line := range strings.Split(string(mounts), "\n") {
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "cgroup2 ") {
			continue
		}
		fields := strings.Fields(parts[0])
		if len(fields) >= 5 && fields[3] == "/" && fields[4] == "/sys/fs/cgroup" {
			visibleRoot = true
		}
	}
	if !visibleRoot {
		return "unknown"
	}
	groups, err := readFile("/proc/self/cgroup")
	if err != nil {
		return "unknown"
	}
	group := strings.TrimSpace(string(groups))
	if !strings.HasPrefix(group, "0::/") || strings.Contains(group, "\n") {
		return "unknown"
	}
	group = strings.TrimPrefix(group, "0::")
	if path.Clean(group) != group {
		return "unknown"
	}
	for depth := 0; depth < 64; depth++ {
		limit, err := readFile("/sys/fs/cgroup" + strings.TrimSuffix(group, "/") + "/memory.max")
		if err != nil {
			if group == "/" && os.IsNotExist(err) {
				return "host"
			}
			return "unknown"
		}
		value := strings.TrimSpace(string(limit))
		if value != "max" {
			if _, err := strconv.ParseUint(value, 10, 64); err != nil {
				return "unknown"
			}
			return "limited"
		}
		if group == "/" {
			return "host"
		}
		group = path.Dir(group)
	}
	return "unknown"
}
