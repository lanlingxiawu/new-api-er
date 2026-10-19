package performance_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestBodyGovernanceSnapshotConcurrentPublication(t *testing.T) {
	old := *GetPerformanceSetting()
	defer ReplacePerformanceSetting(old)
	first := old
	first.BodyGovernanceEnabled = true
	first.BodyMemoryBudgetMB = 2
	first.DiskCacheMaxSizeMB = 4
	second := first
	second.BodyMemoryBudgetMB = 4
	second.DiskCacheMaxSizeMB = 8
	ReplacePerformanceSetting(first)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			ReplacePerformanceSetting(first)
			ReplacePerformanceSetting(second)
		}
	}()
	for i := 0; i < 2000; i++ {
		snapshot := GetPerformanceSetting()
		require.Equal(t, snapshot.BodyMemoryBudgetMB*2, snapshot.DiskCacheMaxSizeMB)
		runtime := common.GetDiskCacheConfig()
		require.Equal(t, runtime.MemoryBudgetMB*2, runtime.MaxSizeMB)
	}
	wg.Wait()
}

func TestBodyGovernanceConfiguration(t *testing.T) {
	old := *GetPerformanceSetting()
	defer ReplacePerformanceSetting(old)
	_, err := ParsePerformanceUpdate(map[string]string{"body_governance_enabled": "true"})
	require.Error(t, err)
	for _, values := range []map[string]string{
		{"body_memory_budget_mb": "-1"}, {"body_governance_enabled": "invalid"}, {"unknown": "1"},
	} {
		_, err = ParsePerformanceUpdate(values)
		require.Error(t, err)
	}
	draft, err := ParsePerformanceUpdate(map[string]string{"body_governance_enabled": "true", "body_memory_budget_mb": "2"})
	require.NoError(t, err)
	before := GetPerformanceSetting()
	ReplacePerformanceSetting(draft)
	require.Equal(t, old, *before)
	require.True(t, GetPerformanceSetting().BodyGovernanceEnabled)
	require.Equal(t, 2, common.GetDiskCacheConfig().MemoryBudgetMB)
	invalid := draft
	invalid.BodyMemoryBudgetMB = 0
	ReplacePerformanceSetting(invalid)
	require.Equal(t, draft, *GetPerformanceSetting())
}
