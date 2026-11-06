package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

const quotaRecoveryPrefix = "quota_recovery:v1:"

var errInvalidQuotaRecoveryBatch = errors.New("invalid quota recovery batch")

// Only the batch flush worker accesses these fields, under batchUpdateFlushLock.
var quotaRecoveryDir = filepath.Join("data", "quota-recovery")
var quotaRecoveryUnsaved *quotaRecoveryBatch

type quotaRecoveryDelta struct {
	Kind  int `json:"kind"`
	ID    int `json:"id"`
	Quota int `json:"quota"`
	Used  int `json:"used,omitempty"`
	Count int `json:"count,omitempty"`
}

type quotaRecoveryBatch struct {
	Version  int                  `json:"version"`
	ID       string               `json:"id"`
	Entries  []quotaRecoveryDelta `json:"entries"`
	Checksum string               `json:"checksum"`
}

// A server rejection proves this statement did not apply. Transport errors do
// not: a successful COMMIT can lose its reply, so never retry those bare deltas.
func quotaWriteDefinitelyRejected(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 1142, 1143, 1205, 1213, 1290, 1792:
			return true
		}
	}
	var postgresErr interface{ SQLState() string }
	if errors.As(err, &postgresErr) {
		switch postgresErr.SQLState() {
		case "25006", "42501", "40001", "40P01":
			return true
		}
	}
	return false
}

func queueRejectedQuotaUpdate(kind, id, delta int, err error) bool {
	if !operation_setting.QuotaRecoveryEnabled() || !quotaWriteDefinitelyRejected(err) {
		return false
	}
	addNewRecord(kind, id, delta)
	return true
}

// ApplyWalletSettlement is terminal adjustment only. Pre-consume retains its
// existing fail-closed DB contract and must not call this recovery entry point.
func ApplyWalletSettlement(userID, delta int) error {
	if delta == 0 {
		return nil
	}
	if delta == math.MinInt {
		return errors.New("wallet settlement delta out of range")
	}
	var err error
	if delta > 0 {
		err = DecreaseUserQuota(userID, delta, false)
	} else {
		err = IncreaseUserQuota(userID, -delta, false)
	}
	if err == nil || !queueRejectedQuotaUpdate(BatchUpdateTypeUserQuota, userID, -delta, err) {
		return err
	}
	// Debits already adjusted the cache before the rejected DB write. A credit
	// only adjusts it on success, so perform that original cache step once here.
	if delta < 0 {
		gopool.Go(func() {
			if err := cacheIncrUserQuota(userID, int64(-delta)); err != nil {
				common.SysError("quota recovery: credit cache adjustment failed: " + err.Error())
			}
		})
	}
	return nil
}

func ApplyTokenSettlement(tokenID int, key string, delta int) error {
	if delta == 0 {
		return nil
	}
	if delta == math.MinInt {
		return errors.New("token settlement delta out of range")
	}
	var err error
	if delta > 0 {
		err = DecreaseTokenQuota(tokenID, key, delta)
	} else {
		err = IncreaseTokenQuota(tokenID, key, -delta)
	}
	if err != nil && queueRejectedQuotaUpdate(BatchUpdateTypeTokenQuota, tokenID, -delta, err) {
		return nil
	}
	return err
}

func quotaBatchChecksum(batch quotaRecoveryBatch) (string, error) {
	batch.Checksum = ""
	data, err := common.Marshal(batch)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func saveQuotaRecoveryBatch(batch *quotaRecoveryBatch) error {
	if err := os.MkdirAll(quotaRecoveryDir, 0o750); err != nil {
		return err
	}
	data, err := common.Marshal(batch)
	if err != nil {
		return err
	}
	path := filepath.Join(quotaRecoveryDir, batch.ID+".json")
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return errors.New("quota recovery snapshot identity conflict")
	} else if !os.IsNotExist(err) {
		return err
	}
	files, err := os.ReadDir(quotaRecoveryDir)
	if err != nil {
		return err
	}
	if len(files) >= 1024 {
		return errors.New("quota recovery retention cap reached; snapshot remains in memory")
	}
	file, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// drainQuotaRecovery snapshots maps only; all disk and database work is outside
// the producer locks. Failed snapshots keep their own stable identity.
func drainQuotaRecovery() *quotaRecoveryBatch {
	stores := make([]map[int]int, BatchUpdateTypeCount)
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		stores[i] = make(map[int]int)
		for id, value := range batchUpdateStores[i] {
			stores[i][id] = value
			delete(batchUpdateStores[i], id)
			if len(stores[i]) >= 2000 {
				break
			}
		}
		batchUpdateLocks[i].Unlock()
	}
	batch := &quotaRecoveryBatch{Version: 1, ID: strings.ReplaceAll(common.GetUUID(), "-", "")}
	users := make(map[int]struct{})
	for _, kind := range []int{BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount} {
		for id := range stores[kind] {
			users[id] = struct{}{}
		}
	}
	for id := range users {
		batch.Entries = append(batch.Entries, quotaRecoveryDelta{Kind: BatchUpdateTypeUserQuota, ID: id,
			Quota: stores[BatchUpdateTypeUserQuota][id], Used: stores[BatchUpdateTypeUsedQuota][id],
			Count: stores[BatchUpdateTypeRequestCount][id]})
	}
	for _, kind := range []int{BatchUpdateTypeTokenQuota, BatchUpdateTypeChannelUsedQuota} {
		for id, value := range stores[kind] {
			batch.Entries = append(batch.Entries, quotaRecoveryDelta{Kind: kind, ID: id, Quota: value})
		}
	}
	if len(batch.Entries) == 0 {
		return nil
	}
	// Deterministic update order avoids opposing account lock orders.
	slices.SortFunc(batch.Entries, func(a, b quotaRecoveryDelta) int {
		if a.Kind != b.Kind {
			return a.Kind - b.Kind
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return batch
}

func applyQuotaRecoveryBatch(ctx context.Context, batch quotaRecoveryBatch) error {
	checksum, err := quotaBatchChecksum(batch)
	_, idErr := hex.DecodeString(batch.ID)
	if err != nil || idErr != nil || batch.Version != 1 || checksum != batch.Checksum || len(batch.ID) != 32 || len(batch.Entries) > 10000 {
		return errInvalidQuotaRecoveryBatch
	}
	for offset := 0; offset < len(batch.Entries); offset += 500 {
		key := quotaRecoveryPrefix + batch.ID + ":" + checksum
		if offset > 0 {
			key += fmt.Sprintf(":%d", offset)
		}
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var receipt BusinessStatsAppliedBatch
			err := tx.Where("batch_key = ?", key).First(&receipt).Error
			if err == nil {
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(&BusinessStatsAppliedBatch{BatchKey: key, AppliedAt: time.Now().Unix()}).Error; err != nil {
				return err
			}
			for _, entry := range batch.Entries[offset:min(offset+500, len(batch.Entries))] {
				if entry.ID <= 0 {
					return errInvalidQuotaRecoveryBatch
				}
				var result *gorm.DB
				switch entry.Kind {
				case BatchUpdateTypeUserQuota:
					if entry.Quota == 0 && entry.Used == 0 && entry.Count == 0 {
						continue
					}
					query := tx.Unscoped().Model(&User{}).Where("id = ?", entry.ID)
					if entry.Quota > 0 {
						query = query.Where("quota <= ?", common.MaxWalletQuota-entry.Quota)
					}
					result = query.Updates(map[string]any{
						"quota": gorm.Expr("quota + ?", entry.Quota), "used_quota": gorm.Expr("used_quota + ?", entry.Used),
						"request_count": gorm.Expr("request_count + ?", entry.Count)})
				case BatchUpdateTypeTokenQuota:
					if entry.Quota == 0 {
						continue
					}
					result = tx.Unscoped().Model(&Token{}).Where("id = ?", entry.ID).Updates(map[string]any{
						"remain_quota": gorm.Expr("remain_quota + ?", entry.Quota), "used_quota": gorm.Expr("used_quota - ?", entry.Quota),
						"accessed_time": common.GetTimestamp()})
				case BatchUpdateTypeChannelUsedQuota:
					if entry.Quota == 0 {
						continue
					}
					result = tx.Model(&Channel{}).Where("id = ?", entry.ID).Update("used_quota", gorm.Expr("used_quota + ?", entry.Quota))
				default:
					return errInvalidQuotaRecoveryBatch
				}
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					// Channels are hard-deleted by administration. Their historical
					// counter has no surviving target; do not let that advisory
					// update roll back valid wallet/token adjustments in this chunk.
					if entry.Kind == BatchUpdateTypeChannelUsedQuota && result.RowsAffected == 0 {
						common.SysError(fmt.Sprintf("quota recovery: deleted channel counter skipped: channel_id=%d", entry.ID))
						continue
					}
					return fmt.Errorf("quota recovery target unavailable: kind=%d id=%d", entry.Kind, entry.ID)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func flushQuotaRecovery() {
	// A process may stop after syncing a temporary snapshot but before rename.
	// Promote it before accepting new work; replay validates its checksum.
	temps, err := filepath.Glob(filepath.Join(quotaRecoveryDir, "*.json.tmp"))
	if err != nil {
		common.SysError("quota recovery: list temporary snapshots failed: " + err.Error())
		return
	}
	for _, path := range temps[:min(len(temps), 16)] {
		target := strings.TrimSuffix(path, ".tmp")
		if _, err := os.Stat(target); os.IsNotExist(err) {
			if err := os.Rename(path, target); err != nil {
				common.SysError("quota recovery: temporary snapshot retained: " + err.Error())
			}
		}
	}
	if quotaRecoveryUnsaved == nil {
		quotaRecoveryUnsaved = drainQuotaRecovery()
	}
	if quotaRecoveryUnsaved != nil {
		checksum, err := quotaBatchChecksum(*quotaRecoveryUnsaved)
		if err == nil {
			quotaRecoveryUnsaved.Checksum = checksum
			err = saveQuotaRecoveryBatch(quotaRecoveryUnsaved)
		}
		if err != nil {
			common.SysError("quota recovery: snapshot persistence failed: " + err.Error())
			return
		}
		quotaRecoveryUnsaved = nil
	}
	paths, err := filepath.Glob(filepath.Join(quotaRecoveryDir, "*.json"))
	if err != nil {
		common.SysError("quota recovery: list snapshots failed: " + err.Error())
		return
	}
	// At most one bounded file pass per cycle; no unbounded shutdown drain.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, path := range paths[:min(len(paths), 16)] {
		if ctx.Err() != nil {
			break
		}
		file, err := os.Open(path)
		if err != nil {
			common.SysError("quota recovery: open snapshot failed: " + err.Error())
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, 16*1024*1024+1))
		err = errors.Join(err, file.Close())
		var batch quotaRecoveryBatch
		if err == nil && len(data) <= 16*1024*1024 {
			err = common.Unmarshal(data, &batch)
			if err != nil {
				err = errors.Join(errInvalidQuotaRecoveryBatch, err)
			}
		} else if err == nil {
			err = errInvalidQuotaRecoveryBatch
		}
		if err == nil {
			err = applyQuotaRecoveryBatch(ctx, batch)
		}
		if err != nil {
			common.SysError("quota recovery: pending batch retained: " + err.Error())
			if errors.Is(err, errInvalidQuotaRecoveryBatch) {
				if renameErr := os.Rename(path, path+fmt.Sprintf(".invalid.%d", time.Now().UnixNano())); renameErr != nil {
					common.SysError("quota recovery: invalid snapshot quarantine failed: " + renameErr.Error())
				}
			}
			if ctx.Err() != nil || quotaWriteDefinitelyRejected(err) {
				break
			}
			continue
		}
		if err := os.Remove(path); err != nil {
			common.SysError("quota recovery: completed snapshot retained: " + err.Error())
		}
	}
}
