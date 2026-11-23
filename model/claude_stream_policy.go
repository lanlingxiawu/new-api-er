package model

import (
	"encoding/json"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrLegacyStreamValidationConflict = errors.New("legacy Claude setting conflicts with the protocol setting")

// preserveUserStreamValidation keeps the admin policy when ordinary preference
// writers submit a stale snapshot. The caller holds the user's row lock.
func preserveUserStreamValidation(submitted, stored string) (string, error) {
	requested := map[string]json.RawMessage{}
	current := map[string]json.RawMessage{}
	if submitted != "" {
		if err := common.UnmarshalJsonStr(submitted, &requested); err != nil {
			return "", err
		}
	}
	if stored != "" {
		if err := common.UnmarshalJsonStr(stored, &current); err != nil {
			return "", err
		}
	}
	if requested == nil {
		requested = map[string]json.RawMessage{}
	}
	for _, key := range []string{"claude_stream_validation", "stream_protocol_validation"} {
		delete(requested, key)
		if value, exists := current[key]; exists {
			requested[key] = value
		}
	}
	encoded, err := common.Marshal(requested)
	return string(encoded), err
}

// UpdateUserClaudeStreamValidation changes only the admin-owned JSON field.
// Call in the admin transaction and publish the user cache after commit.
func UpdateUserClaudeStreamValidation(tx *gorm.DB, userID int, value *bool, audit ...*AuditChanges) error {
	return updateUserStreamValidation(tx, userID, "claude_stream_validation", value, audit...)
}

func UpdateUserStreamProtocolValidation(tx *gorm.DB, userID int, value *bool, audit ...*AuditChanges) error {
	return updateUserStreamValidation(tx, userID, "stream_protocol_validation", value, audit...)
}

func updateUserStreamValidation(tx *gorm.DB, userID int, key string, value *bool, audit ...*AuditChanges) error {
	var user User
	if err := lockForUpdate(tx).Select("id", "setting").First(&user, userID).Error; err != nil {
		return err
	}
	settings := map[string]json.RawMessage{}
	if user.Setting != "" {
		if err := common.UnmarshalJsonStr(user.Setting, &settings); err != nil {
			return err
		}
	}
	if settings == nil {
		settings = map[string]json.RawMessage{}
	}
	if key == "claude_stream_validation" && user.GetSetting().StreamProtocolValidation != nil {
		return ErrLegacyStreamValidationConflict
	}
	delete(settings, key)
	if key == "stream_protocol_validation" {
		delete(settings, "claude_stream_validation")
	}
	if value != nil {
		encoded, err := common.Marshal(*value)
		if err != nil {
			return err
		}
		settings[key] = encoded
	}
	encoded, err := common.Marshal(settings)
	if err != nil {
		return err
	}
	err = tx.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"setting": string(encoded), "profile_version": gorm.Expr("profile_version + ?", 1),
	}).Error
	if err == nil && optionalAudit(audit) != nil {
		target := AuditTarget("user", userID, "")
		target.Consistency = "atomic"
		audit[0].Add(target, AuditFields{"setting": user.Setting}, AuditFields{"setting": string(encoded)})
	}
	return err
}

// PreserveStreamValidationSetting distinguishes an omitted field from null
// when an older channel editor replaces the rest of the setting document.
func (channel *Channel) PreserveStreamValidationSetting(original *Channel) error {
	if channel.Setting == nil {
		return nil
	}
	settings := map[string]json.RawMessage{}
	if *channel.Setting != "" {
		if err := common.UnmarshalJsonStr(*channel.Setting, &settings); err != nil {
			return err
		}
	}
	if settings == nil {
		settings = map[string]json.RawMessage{}
	}
	old := original.GetSetting()
	if raw, provided := settings["stream_protocol_validation"]; provided {
		var value *bool
		if err := common.Unmarshal(raw, &value); err != nil {
			return err
		}
		delete(settings, "claude_stream_validation")
		if value == nil {
			delete(settings, "stream_protocol_validation")
		}
	} else {
		if _, legacyProvided := settings["claude_stream_validation"]; legacyProvided && old.StreamProtocolValidation != nil {
			return ErrLegacyStreamValidationConflict
		}
		for key, value := range map[string]*bool{"stream_protocol_validation": old.StreamProtocolValidation, "claude_stream_validation": old.ClaudeStreamValidation} {
			if _, provided := settings[key]; !provided && value != nil {
				encoded, err := common.Marshal(*value)
				if err != nil {
					return err
				}
				settings[key] = encoded
			}
		}
	}
	encoded, err := common.Marshal(settings)
	if err != nil {
		return err
	}
	channel.Setting = common.GetPointer(string(encoded))
	return nil
}
