package model

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// persistAuditOption shares the existing FirstOrCreate/save lifecycle. The
// receipt remains local until the caller's transaction has committed.
func persistAuditOption(tx *gorm.DB, key, value string, receipt *AuditChanges) error {
	option := Option{Key: key}
	result := tx.FirstOrCreate(&option, Option{Key: key})
	if result.Error != nil {
		return result.Error
	}
	before := AuditFields{}
	if result.RowsAffected == 0 {
		before[key] = auditOptionValue(option.Value)
	}
	option.Value = value
	if err := tx.Save(&option).Error; err != nil {
		return err
	}
	if receipt != nil {
		if IsModelPricingOption(key) {
			previous := map[string]string{}
			if result.RowsAffected == 0 {
				if raw, ok := before[key].(json.RawMessage); ok {
					previous[key] = string(raw)
				}
			}
			receipt.OptionValues(previous, map[string]string{key: value}, false)
			return nil
		}
		target := AuditTargetChange{Resource: "option", ID: key, Visibility: "admin", Consistency: "observed"}
		if strings.HasPrefix(key, "audit_security_setting.") {
			target.Visibility = "root"
		}
		receipt.Add(target, before, AuditFields{key: auditOptionValue(value)})
	}
	return nil
}

func auditOptionValue(value string) any {
	var raw json.RawMessage
	if common.Unmarshal([]byte(value), &raw) == nil {
		return raw
	}
	return value
}

func (audit *AuditChanges) OptionValues(before, after map[string]string, atomic bool) {
	if audit == nil {
		return
	}
	keys := make([]string, 0, len(after))
	for key := range after {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == PricingConfigVersionKey {
			continue
		}
		if IsModelPricingOption(key) {
			var oldEntries, newEntries map[string]json.RawMessage
			oldErr := common.UnmarshalJsonStr(before[key], &oldEntries)
			newErr := common.UnmarshalJsonStr(after[key], &newEntries)
			if newErr == nil && (oldErr == nil || before[key] == "") {
				names := map[string]bool{}
				for name := range oldEntries {
					names[name] = true
				}
				for name := range newEntries {
					names[name] = true
				}
				ordered := make([]string, 0, len(names))
				for name := range names {
					ordered = append(ordered, name)
				}
				sort.Strings(ordered)
				for _, name := range ordered {
					old, next := AuditFields{}, AuditFields{}
					if value, ok := oldEntries[name]; ok {
						old[key] = value
					}
					if value, ok := newEntries[name]; ok {
						next[key] = value
					}
					target := AuditTargetChange{Resource: "model_pricing", ID: name, Visibility: "admin", Consistency: "observed"}
					if atomic {
						target.Consistency = "atomic"
					}
					audit.Add(target, old, next)
				}
				continue
			}
		}
		old := AuditFields{}
		if value, ok := before[key]; ok {
			old[key] = auditOptionValue(value)
		}
		target := AuditTargetChange{Resource: "option", ID: key, Visibility: "admin", Consistency: "observed"}
		if atomic {
			target.Consistency = "atomic"
		}
		if strings.HasPrefix(key, "audit_security_setting.") {
			target.Visibility = "root"
		}
		audit.Add(target, old, AuditFields{key: auditOptionValue(after[key])})
	}
}
