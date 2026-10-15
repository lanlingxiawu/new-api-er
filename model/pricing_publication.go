package model

import (
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// publishModelPricingOptions keeps expression and mode in one ConfigManager
// publication. Relay readers keep their previously loaded immutable snapshot.
// Caller holds the management mutation lock when publishing pricing writes.
func publishModelPricingOptions(values map[string]string) error {
	keys := make([]string, 0, len(values))
	billing := map[string]string{}
	for key, value := range values {
		if field, ok := strings.CutPrefix(key, "billing_setting."); ok {
			billing[field] = value
			continue
		}
		keys = append(keys, key)
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
		return updateOptionMap(PricingConfigVersionKey, version)
	}
	return nil
}
