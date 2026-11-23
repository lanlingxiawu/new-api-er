package controller

import (
	"reflect"
	"sort"

	"github.com/QuantumNous/new-api/model"
)

// ChannelSecurityChanges compares persisted configuration only. Returning field
// names prevents secrets, overrides and credential-bearing URLs entering alerts.
func ChannelSecurityChanges(before, after *model.Channel) []string {
	fields := map[string][2]any{
		"key": {before.Key, after.Key}, "base_url": {before.BaseURL, after.BaseURL},
		"models": {before.Models, after.Models}, "group": {before.Group, after.Group},
		"type": {before.Type, after.Type}, "status": {before.Status, after.Status},
		"priority": {before.Priority, after.Priority}, "weight": {before.Weight, after.Weight},
		"auto_ban": {before.AutoBan, after.AutoBan}, "model_mapping": {before.ModelMapping, after.ModelMapping},
		"status_code_mapping": {before.StatusCodeMapping, after.StatusCodeMapping},
		"param_override":      {before.ParamOverride, after.ParamOverride}, "header_override": {before.HeaderOverride, after.HeaderOverride},
		"setting": {before.Setting, after.Setting}, "settings": {before.OtherSettings, after.OtherSettings}, "other": {before.Other, after.Other},
		"daily_limit": {before.DailyQuotaLimit, after.DailyQuotaLimit},
	}
	changed := make([]string, 0)
	for name, pair := range fields {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}
