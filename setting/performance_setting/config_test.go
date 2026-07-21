package performance_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// savePerformanceState snapshots the package-global performanceSetting AND the
// downstream common-package config/stats that syncToCommon mutates, restoring
// all of them on cleanup so tests never leak into each other or other packages.
func savePerformanceState(t *testing.T) {
	t.Helper()
	origSetting := performanceSetting
	origDisk := common.GetDiskCacheConfig()
	origMonitor := common.GetPerformanceMonitorConfig()
	origStats := common.GetDiskCacheStats()
	t.Cleanup(func() {
		performanceSetting = origSetting
		common.SetDiskCacheConfig(origDisk)
		common.SetPerformanceMonitorConfig(origMonitor)
		// Restore hit counters (ResetStats zeroes them). The only mutation the
		// setter exposes is +1, so re-apply the original counts. In a fresh test
		// binary these start at 0, making this a no-op in practice.
		common.ResetDiskCacheStats()
		for i := int64(0); i < origStats.DiskCacheHits; i++ {
			common.IncrementDiskCacheHits()
		}
		for i := int64(0); i < origStats.MemoryCacheHits; i++ {
			common.IncrementMemoryCacheHits()
		}
	})
}

func TestGetPerformanceSetting_ReturnsGlobalPointer(t *testing.T) {
	savePerformanceState(t)
	got := GetPerformanceSetting()
	assert.Same(t, &performanceSetting, got)

	got.DiskCacheThresholdMB = 42
	assert.Equal(t, 42, performanceSetting.DiskCacheThresholdMB)
}

func TestDefaults(t *testing.T) {
	// Package defaults documented in config.go.
	assert.False(t, performanceSetting.DiskCacheEnabled)
	assert.Equal(t, 10, performanceSetting.DiskCacheThresholdMB)
	assert.Equal(t, 1024, performanceSetting.DiskCacheMaxSizeMB)
	assert.Equal(t, "", performanceSetting.DiskCachePath)
	assert.True(t, performanceSetting.MonitorEnabled)
	assert.Equal(t, 90, performanceSetting.MonitorCPUThreshold)
	assert.Equal(t, 90, performanceSetting.MonitorMemoryThreshold)
	assert.Equal(t, 95, performanceSetting.MonitorDiskThreshold)
}

func TestUpdateAndSync_PropagatesDiskCacheConfigToCommon(t *testing.T) {
	savePerformanceState(t)

	performanceSetting.DiskCacheEnabled = true
	performanceSetting.DiskCacheThresholdMB = 7
	performanceSetting.DiskCacheMaxSizeMB = 2048
	performanceSetting.DiskCachePath = "/var/cache/newapi"

	UpdateAndSync()

	dc := common.GetDiskCacheConfig()
	assert.True(t, dc.Enabled)
	assert.Equal(t, 7, dc.ThresholdMB)
	assert.Equal(t, 2048, dc.MaxSizeMB)
	assert.Equal(t, "/var/cache/newapi", dc.Path)
}

func TestUpdateAndSync_PropagatesMonitorConfigToCommon(t *testing.T) {
	savePerformanceState(t)

	performanceSetting.MonitorEnabled = false
	performanceSetting.MonitorCPUThreshold = 55
	performanceSetting.MonitorMemoryThreshold = 66
	performanceSetting.MonitorDiskThreshold = 77

	UpdateAndSync()

	mc := common.GetPerformanceMonitorConfig()
	assert.False(t, mc.Enabled)
	assert.Equal(t, 55, mc.CPUThreshold)
	assert.Equal(t, 66, mc.MemoryThreshold)
	assert.Equal(t, 77, mc.DiskThreshold)
}

func TestGetCacheStats_ProxiesCommon(t *testing.T) {
	savePerformanceState(t)

	// Drive the disk-cache config so derived stat fields are deterministic.
	performanceSetting.DiskCacheMaxSizeMB = 100
	performanceSetting.DiskCacheThresholdMB = 5
	UpdateAndSync()

	stats := GetCacheStats()
	// GetCacheStats must equal a direct read of the common stats.
	assert.Equal(t, common.GetDiskCacheStats(), stats)
	assert.Equal(t, int64(100)<<20, stats.DiskCacheMaxBytes)
	assert.Equal(t, int64(5)<<20, stats.DiskCacheThresholdBytes)
}

func TestResetStats_ZeroesHitCounters(t *testing.T) {
	savePerformanceState(t)

	common.IncrementDiskCacheHits()
	common.IncrementMemoryCacheHits()
	require.NotZero(t, common.GetDiskCacheStats().DiskCacheHits)

	ResetStats()

	after := common.GetDiskCacheStats()
	assert.Zero(t, after.DiskCacheHits)
	assert.Zero(t, after.MemoryCacheHits)
}
