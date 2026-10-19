package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"strconv"
	"strings"
)

// saveGroupRetryPolicy is called with configGroupMu held. Legacy count writes
// share the same transaction/serialization path as the unified editor.
func saveGroupRetryPolicy(module string, values map[string]string) (map[string]string, error) {
	statuses := operation_setting.GetGroupRetryStatusSetting()
	guard := operation_setting.GetGroupRetryGuardSetting()
	stored := map[string]string{}
	if module == "group_retry_policy" || module == "group_retry_times" {
		expected := 1
		if module == "group_retry_policy" {
			expected = 5
		}
		if len(values) != expected {
			return nil, operation_setting.ErrGroupRetryGuardInvalid
		}
		raw, ok := values["times"]
		if !ok || len(raw) > 65536 {
			return nil, operation_setting.ErrGroupRetryGuardInvalid
		}
		var entries map[string]*int
		if err := common.UnmarshalJsonStr(raw, &entries); err != nil || entries == nil || len(entries) > 256 {
			return nil, operation_setting.ErrGroupRetryGuardInvalid
		}
		for group, n := range entries {
			if group == "" || strings.TrimSpace(group) != group || n == nil || *n < -1 || *n > operation_setting.MaxGroupRetryTimes {
				return nil, operation_setting.ErrGroupRetryGuardInvalid
			}
		}
		stored["GroupRetryTimes"] = raw
	}
	if module == "group_retry_policy" {
		for _, field := range []string{"status_enabled", "status_rules", "guard_enabled", "guard_rules"} {
			if _, ok := values[field]; !ok {
				return nil, operation_setting.ErrGroupRetryGuardInvalid
			}
		}
		var err error
		statuses, err = operation_setting.ParseGroupRetryStatusUpdate(map[string]string{"enabled": values["status_enabled"], "rules": values["status_rules"]})
		if err != nil {
			return nil, err
		}
		stored["group_retry_status_setting.enabled"] = strconv.FormatBool(statuses.Enabled)
		stored["group_retry_status_setting.rules"] = statuses.Rules
		guard, err = operation_setting.ParseGroupRetryGuardUpdate(map[string]string{"enabled": values["guard_enabled"], "rules": values["guard_rules"]})
		if err != nil {
			return nil, err
		}
	}
	if module == "group_retry_guard_setting" {
		var err error
		guard, err = operation_setting.ParseGroupRetryGuardUpdate(values)
		if err != nil {
			return nil, err
		}
	}
	if module != "group_retry_times" {
		if guard != operation_setting.GetGroupRetryGuardSetting() {
			guard.Revision = common.GetUUID()
		}
		stored["group_retry_guard_setting.enabled"] = strconv.FormatBool(guard.Enabled)
		stored["group_retry_guard_setting.rules"] = guard.Rules
		stored["group_retry_guard_setting.revision"] = guard.Revision
	}
	if err := persistOptionsTx(stored); err != nil {
		return nil, err
	}
	if module != "group_retry_guard_setting" {
		// The exact JSON was fully decoded above; publishing cannot fail validation.
		if err := operation_setting.UpdateGroupRetryTimesByJSONString(stored["GroupRetryTimes"]); err != nil {
			return nil, err
		}
	}
	if module == "group_retry_policy" {
		operation_setting.ReplaceGroupRetryStatusSetting(statuses)
	}
	if module != "group_retry_times" {
		operation_setting.ReplaceGroupRetryGuardSetting(guard)
	}
	return stored, nil
}
