package model

import (
	"fmt"
	"maps"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

const streamValidationOption = "stream_protocol_validation_setting.enabled"
const legacyStreamValidationOption = "claude_stream_validation_setting.enabled"

// Caller holds configGroupMu; admin mutations are separate from relay reads.
func saveStreamValidationOptions(values map[string]string, audit ...*AuditChanges) error {
	values = maps.Clone(values)
	for key, value := range values {
		if (key != streamValidationOption && key != legacyStreamValidationOption) || (value != "true" && value != "false") {
			return fmt.Errorf("invalid stream validation setting")
		}
	}
	_, canonical := values[streamValidationOption]
	if canonical {
		delete(values, legacyStreamValidationOption)
	}
	var receipt *AuditChanges
	if optionalAudit(audit) != nil {
		receipt = NewAuditChanges()
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if !canonical {
			var count int64
			if err := tx.Model(&Option{}).Where(&Option{Key: streamValidationOption}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return ErrLegacyStreamValidationConflict
			}
		}
		for key, value := range values {
			if err := persistAuditOption(tx, key, value, receipt); err != nil {
				return err
			}
		}
		if canonical {
			return tx.Where(&Option{Key: legacyStreamValidationOption}).Delete(&Option{}).Error
		}
		return nil
	}); err != nil {
		return err
	}
	if receipt != nil {
		audit[0].Merge(receipt)
	}
	if canonical {
		values[legacyStreamValidationOption] = "true"
	}
	return publishStreamValidationOptions(values)
}

func publishStreamValidationOptions(values map[string]string) error {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	canonical := common.OptionMap[streamValidationOption]
	legacy := common.OptionMap[legacyStreamValidationOption]
	if value, ok := values[streamValidationOption]; ok {
		canonical = value
	}
	if value, ok := values[legacyStreamValidationOption]; ok {
		legacy = value
	}
	var enabled *bool
	if canonical != "" && canonical != "null" {
		if canonical != "true" && canonical != "false" {
			return fmt.Errorf("invalid stored stream validation setting")
		}
		enabled = common.GetPointer(canonical == "true")
	}
	if legacy != "" && legacy != "true" && legacy != "false" {
		return fmt.Errorf("invalid stored Claude validation setting")
	}
	operation_setting.ReplaceStreamValidationDefaults(enabled, legacy != "false")
	common.OptionMap[streamValidationOption] = "null"
	if enabled != nil {
		common.OptionMap[streamValidationOption] = canonical
	}
	common.OptionMap[legacyStreamValidationOption] = "true"
	if legacy != "" {
		common.OptionMap[legacyStreamValidationOption] = legacy
	}
	return nil
}
