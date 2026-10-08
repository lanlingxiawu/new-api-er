package model

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/QuantumNous/new-api/common"
)

const (
	logOtherAdminInfoKey = "admin_info"
	logOtherRootInfoKey  = "root_info"
	logOtherAuditInfoKey = "audit_info"
)

// legacySensitiveLogOtherKeys are historical top-level fields that must never
// be written via SetPublic and must be stripped from user-visible projections.
var legacySensitiveLogOtherKeys = []string{
	"channel_id",
	"channel_name",
	"channel_type",
	"reject_reason",
}

type logOtherVisibility int

const (
	logOtherVisibilityUser logOtherVisibility = iota
	logOtherVisibilityAdmin
	logOtherVisibilityRoot
)

// LogOther separates usage-log metadata by the audience allowed to see it.
// Its maps stay private so callers cannot accidentally place privileged fields
// in the user-visible top level.
type LogOther struct {
	public    map[string]any
	adminInfo map[string]any
	rootInfo  map[string]any
	auditInfo map[string]any
}

func NewLogOther() *LogOther {
	return &LogOther{}
}

func isReservedLogOtherKey(key string) bool {
	switch key {
	case logOtherAdminInfoKey, logOtherRootInfoKey, logOtherAuditInfoKey:
		return true
	default:
		return slices.Contains(legacySensitiveLogOtherKeys, key)
	}
}

// SetPublic records metadata that log owners may receive from self/token log APIs.
// It rejects role-scoped and legacy-sensitive keys so new writers cannot recreate
// the historical channel/reject-reason leak.
func (o *LogOther) SetPublic(key string, value any) bool {
	if o == nil || key == "" || isReservedLogOtherKey(key) {
		return false
	}
	if o.public == nil {
		o.public = make(map[string]any)
	}
	o.public[key] = value
	return true
}

func (o *LogOther) MergePublic(values map[string]any) {
	for key, value := range values {
		o.SetPublic(key, value)
	}
}

func (o *LogOther) SetAdmin(key string, value any) bool {
	if o == nil || key == "" {
		return false
	}
	if o.adminInfo == nil {
		o.adminInfo = make(map[string]any)
	}
	o.adminInfo[key] = value
	return true
}

func (o *LogOther) MergeAdmin(values map[string]any) {
	for key, value := range values {
		o.SetAdmin(key, value)
	}
}

func (o *LogOther) SetRoot(key string, value any) bool {
	if o == nil || key == "" {
		return false
	}
	if o.rootInfo == nil {
		o.rootInfo = make(map[string]any)
	}
	o.rootInfo[key] = value
	return true
}

func (o *LogOther) MergeRoot(values map[string]any) {
	for key, value := range values {
		o.SetRoot(key, value)
	}
}

func (o *LogOther) SetAudit(key string, value any) bool {
	if o == nil || key == "" {
		return false
	}
	if o.auditInfo == nil {
		o.auditInfo = make(map[string]any)
	}
	o.auditInfo[key] = value
	return true
}

func (o *LogOther) MergeAudit(values map[string]any) {
	for key, value := range values {
		o.SetAudit(key, value)
	}
}

func copyLogOtherMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	copyValues := make(map[string]any, len(values))
	maps.Copy(copyValues, values)
	return copyValues
}

func (o *LogOther) toMap() map[string]any {
	result := make(map[string]any)
	if o == nil {
		return result
	}

	maps.Copy(result, o.public)
	if adminInfo := copyLogOtherMap(o.adminInfo); len(adminInfo) > 0 {
		result[logOtherAdminInfoKey] = adminInfo
	}
	if auditInfo := copyLogOtherMap(o.auditInfo); len(auditInfo) > 0 {
		result[logOtherAuditInfoKey] = auditInfo
	}
	if rootInfo := copyLogOtherMap(o.rootInfo); len(rootInfo) > 0 {
		result[logOtherRootInfoKey] = rootInfo
	}
	return result
}

func (o *LogOther) jsonString() string {
	if o == nil {
		return ""
	}
	data, err := common.Marshal(o.toMap())
	if err != nil {
		common.SysError("failed to marshal log other: " + err.Error())
		return ""
	}
	return string(data)
}

// JSONString returns the complete stored representation, including all
// privileged scopes. API projections must use the role-specific formatter.
func (o *LogOther) JSONString() string {
	return o.jsonString()
}

// Snapshot returns a detached top-level view for tests and read-only
// inspection. Mutating it cannot add or replace fields in LogOther.
func (o *LogOther) Snapshot() map[string]any {
	if o == nil {
		return nil
	}
	return o.toMap()
}

func (o *LogOther) MarshalJSON() ([]byte, error) {
	return common.Marshal(o.toMap())
}

func normalizeLegacyRejectReason(values map[string]json.RawMessage) bool {
	rejectReason, ok := values["reject_reason"]
	if !ok {
		return false
	}

	adminInfo := make(map[string]json.RawMessage)
	if rawAdminInfo, exists := values[logOtherAdminInfoKey]; exists {
		_ = common.Unmarshal(rawAdminInfo, &adminInfo)
	}
	if adminInfo == nil {
		adminInfo = make(map[string]json.RawMessage)
	}
	if _, exists := adminInfo["reject_reason"]; !exists {
		adminInfo["reject_reason"] = rejectReason
	}
	encodedAdminInfo, err := common.Marshal(adminInfo)
	if err != nil {
		return false
	}
	values[logOtherAdminInfoKey] = encodedAdminInfo
	delete(values, "reject_reason")
	return true
}

// logOtherFields is a log's other decoded one level deep. Values that are not
// rewritten keep their raw JSON, so a view that changes one field does not
// round-trip the rest through float64 (integers beyond 2^53 stay exact).
type logOtherFields map[string]json.RawMessage

// parseLogOtherFields returns nil when other is empty or not a JSON object.
func parseLogOtherFields(other string) logOtherFields {
	if other == "" {
		return nil
	}
	var fields logOtherFields
	if err := common.UnmarshalJsonStr(other, &fields); err != nil {
		return nil
	}
	return fields
}

// rawJSONString decodes raw when it is a JSON string.
func rawJSONString(raw json.RawMessage) (string, bool) {
	if common.GetJsonType(raw) != "string" {
		return "", false
	}
	var value string
	if err := common.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

// str is the string value of key, or "" when it is missing or not a string.
func (f logOtherFields) str(key string) string {
	value, _ := rawJSONString(f[key])
	return value
}

// integer is the numeric value of key, or 0 when it is missing or not a number.
func (f logOtherFields) integer(key string) int {
	raw := f[key]
	if common.GetJsonType(raw) != "number" {
		return 0
	}
	var value float64
	if err := common.Unmarshal(raw, &value); err != nil {
		return 0
	}
	return int(value)
}

// object decodes key as a JSON object; nil when it is missing or not an object.
func (f logOtherFields) object(key string) logOtherFields {
	raw, ok := f[key]
	if !ok || common.GetJsonType(raw) != "object" {
		return nil
	}
	var fields logOtherFields
	if err := common.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	return fields
}

// set stores value encoded as JSON; reports whether it was stored.
func (f logOtherFields) set(key string, value any) bool {
	data, err := common.Marshal(value)
	if err != nil {
		return false
	}
	f[key] = data
	return true
}

// encode is the JSON text of f. Values that were just parsed always encode; if
// one did not, the view shows an empty object rather than the stored original,
// which may hold what the caller removed (as formatLogOtherJSON does).
func (f logOtherFields) encode() string {
	data, err := common.Marshal(f)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// formatLogOtherJSON applies the role projection while keeping untouched JSON
// values as RawMessage. This preserves integers larger than JavaScript's safe
// range instead of round-tripping them through float64.
func formatLogOtherJSON(value string, visibility logOtherVisibility) string {
	if value == "" {
		return ""
	}

	var values map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(value, &values); err != nil {
		if visibility == logOtherVisibilityRoot {
			return value
		}
		return "{}"
	}

	changed := false
	if visibility == logOtherVisibilityUser {
		for _, key := range []string{logOtherAdminInfoKey, logOtherRootInfoKey, logOtherAuditInfoKey} {
			if _, exists := values[key]; exists {
				delete(values, key)
				changed = true
			}
		}
		for _, key := range legacySensitiveLogOtherKeys {
			if _, exists := values[key]; exists {
				delete(values, key)
				changed = true
			}
		}
	} else {
		changed = normalizeLegacyRejectReason(values)
		if visibility == logOtherVisibilityAdmin {
			if _, exists := values[logOtherRootInfoKey]; exists {
				delete(values, logOtherRootInfoKey)
				changed = true
			}
		}
	}

	if !changed {
		return value
	}
	formatted, err := common.Marshal(values)
	if err != nil {
		if visibility == logOtherVisibilityRoot {
			return value
		}
		return "{}"
	}
	return string(formatted)
}
