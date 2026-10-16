package model

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// PricingConfigVersionKey 是价格配置的持久化版本号。任何写价格类 option 的路径都必须
// 在同一事务内递增它，否则并发写入只能被最后一个提交者静默覆盖。
const PricingConfigVersionKey = "PricingConfigVersion"

// GetPricingConfigVersion 读取当前价格配置版本号。缺失或非法时按 0 处理。
func GetPricingConfigVersion() int64 {
	common.OptionMapRWMutex.RLock()
	raw, ok := common.OptionMap[PricingConfigVersionKey]
	common.OptionMapRWMutex.RUnlock()
	if !ok {
		return 0
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return version
}

// ReadPricingConfigVersion reads the database generation for administrative
// reviews. Cross-instance validation must never use this process's OptionMap.
func ReadPricingConfigVersion() (int64, error) {
	return readPricingVersionForUpdate(DB)
}

// lockedPricingTx clones statements so separate option reads never accumulate predicates.
func lockedPricingTx(tx *gorm.DB) *gorm.DB {
	return lockForUpdate(tx).Session(&gorm.Session{})
}

func readPricingVersionForUpdate(tx *gorm.DB) (int64, error) {
	var option Option
	// key 是三种数据库里的保留字，列名引用方式不同，走 model/main.go 的 commonKeyCol。
	err := tx.Where(&Option{Key: PricingConfigVersionKey}).Take(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if option.Value == "" {
		return 0, nil
	}
	version, parseErr := strconv.ParseInt(option.Value, 10, 64)
	if parseErr != nil {
		return 0, nil
	}
	return version, nil
}
