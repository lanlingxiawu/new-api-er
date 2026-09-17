package operation_setting

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"

	"github.com/stretchr/testify/assert"
)

// IsCommissionTierResetEnabled 只读已发布快照：草稿在锁内被改写、尚未发布时必须仍返回已发布的值。
func TestIsCommissionTierResetEnabledReadsPublishedSnapshotNotDraft(t *testing.T) {
	original := *GetCommissionTierResetSetting()
	t.Cleanup(func() { ReplaceCommissionTierResetSetting(original) })
	published := original
	published.Enabled = true
	ReplaceCommissionTierResetSetting(published)

	config.WithConfigDraft(func() {
		draft := commissionTierResetSetting
		defer func() { commissionTierResetSetting = draft }()
		commissionTierResetSetting.Enabled = false
		assert.True(t, IsCommissionTierResetEnabled())
	})
}

// 写者经 ConfigManager 反复切换开关时，读者从快照读到的开关与同一快照内的字段保持一致。
func TestCommissionTierResetSnapshotStaysConsistentDuringConfigWrites(t *testing.T) {
	original := *GetCommissionTierResetSetting()
	t.Cleanup(func() { ReplaceCommissionTierResetSetting(original) })
	baseline := original
	baseline.Enabled, baseline.Timezone = false, "disabled"
	ReplaceCommissionTierResetSetting(baseline)

	variants := []map[string]string{
		{"enabled": "true", "timezone": "enabled"},
		{"enabled": "false", "timezone": "disabled"},
	}
	const writers, readers, rounds = 2, 4, 300
	var writersWG, readersWG sync.WaitGroup
	stop := make(chan struct{})
	for writer := 0; writer < writers; writer++ {
		writersWG.Add(1)
		go func(seed int) {
			defer writersWG.Done()
			for round := 0; round < rounds; round++ {
				if err := config.GlobalConfig.UpdateFromMap("commission_tier_reset_setting", variants[(seed+round)%len(variants)]); err != nil {
					t.Errorf("update commission tier reset setting: %v", err)
					return
				}
			}
		}(writer)
	}
	for reader := 0; reader < readers; reader++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snapshot := GetCommissionTierResetSetting()
				if want := map[bool]string{true: "enabled", false: "disabled"}[snapshot.Enabled]; snapshot.Timezone != want {
					t.Errorf("inconsistent commission tier reset snapshot: %+v", *snapshot)
					return
				}
				_ = IsCommissionTierResetEnabled()
			}
		}()
	}
	writersWG.Wait()
	close(stop)
	readersWG.Wait()
}
