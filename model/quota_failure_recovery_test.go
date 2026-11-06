package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// This opt-in test changes the write mode of an exclusively owned MySQL
// fixture. The database-name guard prevents use against the shared dev DB.
func TestQuotaFailureRecoveryMySQL(t *testing.T) {
	if os.Getenv("QUOTA_RECOVERY_FAULT_TEST") != "1" {
		t.Skip("requires an owned MySQL fault fixture")
	}
	require.Contains(t, os.Getenv("SQL_DSN"), "/quota_recovery_test?")
	require.NotNil(t, DB)
	require.Equal(t, "mysql", DB.Dialector.Name())
	for _, batch := range []bool{true, false} {
		t.Run(fmt.Sprintf("batch=%t", batch), func(t *testing.T) {
			resetBatchStores(t)
			previousDir, previousUnsaved := quotaRecoveryDir, quotaRecoveryUnsaved
			quotaRecoveryDir, quotaRecoveryUnsaved = t.TempDir(), nil
			t.Cleanup(func() { quotaRecoveryDir, quotaRecoveryUnsaved = previousDir, previousUnsaved })
			previous := common.BatchUpdateEnabled
			common.BatchUpdateEnabled = batch
			t.Cleanup(func() { common.BatchUpdateEnabled = previous })
			name := "qr" + strings.ReplaceAll(common.GetUUID(), "-", "")[:12]
			user := User{Username: name, AffCode: name, Password: "unused", Quota: 1000}
			require.NoError(t, DB.Create(&user).Error)
			token := Token{UserId: user.Id, Key: name, Name: name, RemainQuota: 1000}
			require.NoError(t, DB.Create(&token).Error)
			channel := Channel{Name: name, Key: "synthetic"}
			require.NoError(t, DB.Create(&channel).Error)
			t.Cleanup(func() {
				require.NoError(t, DB.Unscoped().Delete(&Token{}, token.Id).Error)
				require.NoError(t, DB.Unscoped().Delete(&Channel{}, channel.Id).Error)
				require.NoError(t, DB.Unscoped().Delete(&User{}, user.Id).Error)
			})
			require.NoError(t, DB.Exec("SET GLOBAL super_read_only=ON").Error)
			t.Cleanup(func() { require.NoError(t, DB.Exec("SET GLOBAL super_read_only=OFF").Error) })
			require.NoError(t, ApplyWalletSettlement(user.Id, 100))
			require.NoError(t, ApplyTokenSettlement(token.Id, token.Key, 100))
			UpdateUserUsedQuotaAndRequestCount(user.Id, 100)
			UpdateChannelUsedQuota(channel.Id, 100)
			FlushBatchUpdate()
			files, err := filepath.Glob(filepath.Join(quotaRecoveryDir, "*.json"))
			require.NoError(t, err)
			require.Len(t, files, 1)
			data, err := os.ReadFile(files[0])
			require.NoError(t, err)
			var snapshot quotaRecoveryBatch
			require.NoError(t, common.Unmarshal(data, &snapshot))
			t.Cleanup(func() {
				require.NoError(t, DB.Where("batch_key = ?", quotaRecoveryPrefix+snapshot.ID+":"+snapshot.Checksum).
					Delete(&BusinessStatsAppliedBatch{}).Error)
			})
			require.NoError(t, DB.Exec("SET GLOBAL super_read_only=OFF").Error)
			FlushBatchUpdate()
			// A crash can retain a file after the transaction committed. Restoring
			// that exact file must find the DB receipt instead of debiting twice.
			require.NoError(t, os.WriteFile(files[0], data, 0o640))
			FlushBatchUpdate()
			require.NoError(t, DB.First(&user, user.Id).Error)
			require.NoError(t, DB.First(&token, token.Id).Error)
			require.NoError(t, DB.First(&channel, channel.Id).Error)
			require.Equal(t, 900, user.Quota)
			require.Equal(t, 100, user.UsedQuota)
			require.Equal(t, 1, user.RequestCount)
			require.Equal(t, 900, token.RemainQuota)
			require.Equal(t, 100, token.UsedQuota)
			require.Equal(t, int64(100), channel.UsedQuota)
			// A rejected terminal refund must restore both balances once. It must
			// not repeat the original usage counters or debit cache adjustments.
			require.NoError(t, DB.Exec("SET GLOBAL super_read_only=ON").Error)
			require.NoError(t, ApplyWalletSettlement(user.Id, -100))
			require.NoError(t, ApplyTokenSettlement(token.Id, token.Key, -100))
			FlushBatchUpdate()
			refundFiles, err := filepath.Glob(filepath.Join(quotaRecoveryDir, "*.json"))
			require.NoError(t, err)
			require.Len(t, refundFiles, 1)
			refundData, err := os.ReadFile(refundFiles[0])
			require.NoError(t, err)
			var refundSnapshot quotaRecoveryBatch
			require.NoError(t, common.Unmarshal(refundData, &refundSnapshot))
			t.Cleanup(func() {
				require.NoError(t, DB.Where("batch_key = ?", quotaRecoveryPrefix+refundSnapshot.ID+":"+refundSnapshot.Checksum).
					Delete(&BusinessStatsAppliedBatch{}).Error)
			})
			require.NoError(t, DB.Exec("SET GLOBAL super_read_only=OFF").Error)
			FlushBatchUpdate()
			require.NoError(t, os.WriteFile(refundFiles[0], refundData, 0o640))
			FlushBatchUpdate()
			require.NoError(t, DB.First(&user, user.Id).Error)
			require.NoError(t, DB.First(&token, token.Id).Error)
			require.Equal(t, 1000, user.Quota)
			require.Equal(t, 1000, token.RemainQuota)
			require.Zero(t, token.UsedQuota)
		})
	}
}
