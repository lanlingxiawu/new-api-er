package model

import (
	"errors"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// publishModelPricingOptions keeps expression and mode in one ConfigManager
// publication. Relay readers keep their previously loaded immutable snapshot.
// Caller holds the management mutation lock when publishing pricing writes.
func publishModelPricingOptions(values map[string]string) error {
	keys := make([]string, 0, len(values))
	billing := map[string]string{}
	performance := map[string]string{}
	guard := map[string]string{}
	for key, value := range values {
		if field, ok := strings.CutPrefix(key, "group_retry_guard_setting."); ok {
			guard[field] = value
			continue
		}
		if field, ok := strings.CutPrefix(key, "performance_setting."); ok {
			performance[field] = value
			continue
		}
		if field, ok := strings.CutPrefix(key, "billing_setting."); ok {
			billing[field] = value
			continue
		}
		keys = append(keys, key)
	}
	var performanceErr error
	if len(guard) > 0 {
		revision, hasRevision := guard["revision"]
		delete(guard, "revision")
		draft, err := operation_setting.ParseGroupRetryGuardUpdate(guard)
		if err != nil {
			performanceErr = err
		} else {
			if hasRevision {
				draft.Revision = revision
			}
			operation_setting.ReplaceGroupRetryGuardSetting(draft)
			common.OptionMapRWMutex.Lock()
			for field, value := range guard {
				common.OptionMap["group_retry_guard_setting."+field] = value
			}
			if hasRevision {
				common.OptionMap["group_retry_guard_setting.revision"] = revision
			}
			common.OptionMapRWMutex.Unlock()
		}
	}
	if len(performance) > 0 {
		draft, err := performance_setting.ParsePerformanceUpdate(performance)
		if err != nil {
			performanceErr = errors.Join(performanceErr, err)
		} else {
			performance_setting.ReplacePerformanceSetting(draft)
			common.OptionMapRWMutex.Lock()
			for key, value := range performance {
				common.OptionMap["performance_setting."+key] = value
			}
			common.OptionMapRWMutex.Unlock()
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == PricingConfigVersionKey {
			continue
		}
		if err := updateOptionMap(key, values[key]); err != nil {
			return err
		}
	}
	if len(billing) > 0 {
		common.OptionMapRWMutex.Lock()
		err := config.GlobalConfig.UpdateFromMap("billing_setting", billing)
		if err == nil {
			for key, value := range billing {
				common.OptionMap["billing_setting."+key] = value
			}
		}
		common.OptionMapRWMutex.Unlock()
		if err != nil {
			return err
		}
		InvalidatePricingCache()
		ratio_setting.InvalidateExposedDataCache()
	}
	if version, ok := values[PricingConfigVersionKey]; ok {
		return errors.Join(performanceErr, updateOptionMap(PricingConfigVersionKey, version))
	}
	return performanceErr
}
