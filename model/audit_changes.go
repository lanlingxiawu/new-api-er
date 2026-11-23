package model

import (
	"bytes"
	"encoding/json"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const auditChangesContextKey = "operation_audit_changes"

// AuditChanges contains only sanitized values. It is deliberately separate from
// operation parameters, which also feed security rules and notifications.
type AuditChanges struct {
	Version   int                 `json:"version"`
	Outcome   string              `json:"outcome"`
	Targets   []AuditTargetChange `json:"targets"`
	Truncated bool                `json:"truncated"`
	bytes     int
	fields    int
}

type AuditTargetChange struct {
	Resource    string             `json:"resource"`
	ID          string             `json:"id"`
	Name        string             `json:"name,omitempty"`
	Operation   string             `json:"operation"`
	Visibility  string             `json:"visibility"`
	OwnerID     int                `json:"owner_id,omitempty"`
	Consistency string             `json:"consistency"`
	Fields      []AuditFieldChange `json:"fields"`
}

type AuditFieldChange struct {
	Path []string         `json:"path"`
	Old  AuditChangeValue `json:"old"`
	New  AuditChangeValue `json:"new"`
}

type AuditChangeValue struct {
	State     string          `json:"state"`
	Value     json.RawMessage `json:"value,omitempty"`
	ValueType string          `json:"value_type,omitempty"`
}

func NewAuditChanges() *AuditChanges {
	return &AuditChanges{Version: 1, Outcome: "unchanged", Targets: []AuditTargetChange{}}
}

// Merge accepts an already sanitized receipt only after its transaction commits.
func (changes *AuditChanges) Merge(receipt *AuditChanges) {
	if receipt == nil {
		return
	}
	for _, target := range receipt.Targets {
		encoded, err := common.Marshal(target)
		if err != nil || changes.bytes+len(encoded)+1 > 63*1024 || len(changes.Targets) >= 100 || changes.fields+len(target.Fields) > 1000 {
			changes.Truncated = true
			break
		}
		changes.Targets = append(changes.Targets, target)
		changes.bytes += len(encoded) + 1
		changes.fields += len(target.Fields)
	}
	changes.Truncated = changes.Truncated || receipt.Truncated
	if changes.Outcome == "partial" || receipt.Outcome == "partial" {
		changes.Outcome = "partial"
	} else if receipt.Outcome == "unavailable" || changes.Outcome == "unavailable" {
		changes.Outcome = "unavailable"
	} else if len(changes.Targets) > 0 {
		changes.Outcome = "committed"
	}
}

// RequestAuditChanges is used only by management handlers, never the relay path.
func RequestAuditChanges(c *gin.Context) *AuditChanges {
	if value, ok := c.Get(auditChangesContextKey); ok {
		return value.(*AuditChanges)
	}
	changes := NewAuditChanges()
	c.Set(auditChangesContextKey, changes)
	return changes
}

func requestAuditChanges(c *gin.Context) *AuditChanges {
	if c != nil {
		if value, ok := c.Get(auditChangesContextKey); ok {
			changes, _ := value.(*AuditChanges)
			return changes
		}
	}
	return nil
}

// Add compares the write set before redaction. Call only after persistence;
// before/after are explicit snapshots, not request bodies or serialized models.
func (changes *AuditChanges) Add(target AuditTargetChange, before, after AuditFields) {
	if changes == nil {
		return
	}
	if len(changes.Targets) >= 100 || changes.bytes >= 63*1024 || changes.fields >= 1000 {
		changes.Truncated = true
		return
	}
	if target.Operation == "" {
		target.Operation = "update"
		if before == nil {
			target.Operation = "create"
		}
		if after == nil {
			target.Operation = "delete"
		}
	}
	if target.Consistency == "" {
		target.Consistency = "observed"
	}
	if target.Visibility == "" {
		target.Visibility = "admin"
	}
	if len(target.Name) > 256 {
		target.Name = ""
	}
	if len(target.ID) > 256 {
		target.ID = ""
	}
	target.Fields = nil
	keys := make([]string, 0, len(before)+len(after))
	for key := range before {
		keys = append(keys, key)
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	budget := 63*1024 - changes.bytes - 1024
	limited := false
	for _, key := range keys {
		old, oldPresent := before[key]
		newValue, newPresent := after[key]
		if oldPresent == newPresent && auditValuesEqual(old, newValue) {
			continue
		}
		paths := []AuditFieldChange{{Path: []string{key}, Old: auditChangeValue(target.Resource, []string{key}, old, oldPresent), New: auditChangeValue(target.Resource, []string{key}, newValue, newPresent)}}
		// Structured channel/user settings have a finite schema. Unknown fields
		// stay in the redacted parent; arbitrary JSON member names are not logged.
		if (target.Resource == "channel" && (key == "setting" || key == "settings" || key == "channel_info")) || (target.Resource == "user" && key == "setting") {
			oldFields, newFields := auditNestedFields(old), auditNestedFields(newValue)
			for _, nestedKey := range strings.Fields(auditNestedSafeFields[target.Resource+"."+key]) {
				a, aOK := oldFields[nestedKey]
				b, bOK := newFields[nestedKey]
				if aOK == bOK && auditValuesEqual(a, b) {
					continue
				}
				path := []string{key, nestedKey}
				paths = append(paths, AuditFieldChange{Path: path, Old: auditChangeValue(target.Resource, path, a, aOK), New: auditChangeValue(target.Resource, path, b, bOK)})
			}
		}
		for _, field := range paths {
			encoded, err := common.Marshal(field)
			if err != nil || len(encoded)+1 > budget || changes.fields+len(target.Fields) >= 1000 {
				changes.Truncated = true
				limited = true
				break
			}
			budget -= len(encoded) + 1
			target.Fields = append(target.Fields, field)
		}
		if limited {
			break
		}
	}
	if len(target.Fields) == 0 {
		return
	}
	encoded, err := common.Marshal(target)
	if err != nil || changes.bytes+len(encoded)+1 > 63*1024 {
		changes.Truncated = true
		return
	}
	changes.bytes += len(encoded) + 1
	changes.fields += len(target.Fields)
	changes.Targets = append(changes.Targets, target)
	if changes.Outcome != "partial" && changes.Outcome != "unavailable" {
		changes.Outcome = "committed"
	}
}

func auditValuesEqual(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	x, xErr := common.Marshal(a)
	y, yErr := common.Marshal(b)
	return xErr == nil && yErr == nil && bytes.Equal(x, y)
}

func auditNestedFields(value any) map[string]json.RawMessage {
	var data []byte
	switch v := value.(type) {
	case json.RawMessage:
		data = v
		var str string
		if common.Unmarshal(v, &str) == nil {
			data = []byte(str)
		}
	case string:
		data = []byte(v)
	case *string:
		if v != nil {
			data = []byte(*v)
		}
	default:
		data, _ = common.Marshal(value)
	}
	var fields map[string]json.RawMessage
	_ = common.Unmarshal(data, &fields)
	return fields
}

var auditSafeFields = map[string]string{
	"channel":        "name remark type status weight models group model_mapping status_code_mapping priority auto_ban tag test_model openai_organization daily_quota_limit daily_limit_auto_recover daily_limit_recover_minutes cost_ratio multi_key_count multi_key_status_list multi_key_mode",
	"user":           "username display_name email remark role status group group_ratios quota model_limits_enabled model_limits allow_user_quota_query is_employee employee_user_id exclusive_ratio_enabled exclusive_ratio_groups channel_exclusions admin_permissions admin_token_permissions stream_validation_override non_stream_timeout stream_timeout request_policy_timeout request_policy_retry retry_count permissions bound deleted language sidebar_modules stream_response_timeout stream_response_timeout_mode stream_total_timeout non_stream_response_timeout non_stream_total_timeout non_stream_timeout_billing retry_times",
	"token":          "name status expired_time remain_quota unlimited_quota model_limits_enabled model_limits allow_ips allow_user_quota_query group cross_group_retry auto_groups",
	"access_token":   "name scopes expires_at revoked",
	"model_pricing":  "ModelRatio CompletionRatio CacheRatio CreateCacheRatio ModelPrice ImageRatio AudioRatio AudioCompletionRatio CacheCreationRatio billing_setting.billing_mode billing_setting.billing_expr",
	"oauth_provider": "name slug icon enabled client_id scopes user_id_field username_field display_name_field email_field auth_style",
}

var auditNestedSafeFields = map[string]string{
	"channel.setting":      "force_format thinking_to_content pass_through_body_enabled responses_websocket_enabled system_prompt_override stream_protocol_validation claude_stream_validation http_protocol http2_connection_shards tls_insecure_skip_verify task_plugin_key task_extend_plugin_keys",
	"channel.settings":     "disable_probe_requests probe_request_mode azure_responses_version vertex_key_type vertex_project_id vertex_api_version openrouter_enterprise claude_beta_query allow_service_tier allow_inference_geo allow_speed allow_safety_identifier upstream_model_update_check_enabled upstream_model_update_ignored_models upstream_model_update_last_detected_models upstream_model_update_last_removed_models upstream_model_update_last_check_time",
	"channel.channel_info": "is_multi_key multi_key_size multi_key_status_list multi_key_mode",
	"user.setting":         "notify_type quota_warning_threshold quota_warning_enabled notification_email gotify_priority upstream_model_update_notify_enabled channel_notify_enabled accept_unset_model_ratio_model record_ip_log billing_preference language sidebar_modules stream_protocol_validation claude_stream_validation",
}

// All unlisted settings are change markers only. Adding a configuration field
// never implicitly grants permission to store its value in audit history.
var auditSafeOptions = "PasswordLoginEnabled PasswordRegisterEnabled EmailVerificationEnabled GitHubOAuthEnabled GoogleOAuthEnabled OIDCEnabled WeChatAuthEnabled TurnstileCheckEnabled RegisterEnabled SMTPPort SMTPAccount SMTPFrom SMTPSSLEnabled SMTPAuthEnabled SystemName Logo HomePageLink ServerAddress TopUpLink ChatLink QuotaForNewUser QuotaForInviter QuotaForInvitee QuotaRemindThreshold PreConsumedQuota LogConsumeEnabled DisplayInCurrency DisplayTokenStatEnabled ApproximateTokenEnabled RetryTimes GroupRatio ModelRatio CompletionRatio CacheRatio CacheCreationRatio ModelPrice GroupGroupRatio UserGroupRatio GroupRetryTimes StreamTimeout RequestInterval RequestRateLimitEnabled TokenLimitEnabled DrawingEnabled CheckInEnabled"

func auditChangeValue(resource string, path []string, value any, present bool) AuditChangeValue {
	if !present {
		return AuditChangeValue{State: "absent"}
	}
	key := path[len(path)-1]
	allowed := false
	if len(path) == 1 {
		allowed = auditWordContains(auditSafeFields[resource], key)
	}
	if len(path) == 2 {
		allowed = auditWordContains(auditNestedSafeFields[resource+"."+path[0]], key)
	}
	if resource == "option" {
		allowed = auditWordContains(auditSafeOptions, key) || auditSafeOptionKey(key)
	}
	urlField := (resource == "channel" && key == "base_url") || (resource == "oauth_provider" && auditWordContains("authorization_endpoint token_endpoint user_info_endpoint well_known", key)) || (resource == "option" && auditWordContains("ServerAddress Logo HomePageLink TopUpLink ChatLink", key))
	if resource == "option" && auditScalarOptionKey(key) {
		data, err := common.Marshal(value)
		if err == nil {
			var scalar any
			if common.Unmarshal(data, &scalar) == nil {
				switch scalar.(type) {
				case bool, float64, nil:
					allowed = true
				}
			}
		}
	}
	if !allowed && !urlField {
		return AuditChangeValue{State: "redacted"}
	}
	encoded, err := common.Marshal(value)
	if err != nil {
		return AuditChangeValue{State: "unavailable"}
	}
	if urlField {
		if string(encoded) == "null" || string(encoded) == `""` {
			return AuditChangeValue{State: "value", Value: encoded}
		}
		var raw string
		if err := common.Unmarshal(encoded, &raw); err != nil {
			return AuditChangeValue{State: "redacted"}
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return AuditChangeValue{State: "redacted"}
		}
		encoded, _ = common.Marshal(parsed.Scheme + "://" + parsed.Host)
	}
	if len(encoded) > 4096 {
		return AuditChangeValue{State: "truncated"}
	}
	// A safe field name does not make arbitrary nested JSON safe. Validate the
	// small set of structured values before allowing them into persisted history.
	if !auditValueShapeSafe(key, encoded) {
		return AuditChangeValue{State: "redacted"}
	}
	depth := 0
	inString, escaped := false, false
	for _, character := range encoded {
		if inString {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		if character == '"' {
			inString = true
		}
		if character == '{' || character == '[' {
			depth++
			if depth > 8 {
				return AuditChangeValue{State: "truncated"}
			}
		}
		if character == '}' || character == ']' {
			depth--
		}
	}
	// Values use RawMessage to retain decimal precision in database round trips.
	// Integers outside JavaScript's exact range are typed decimal strings.
	if n, err := strconv.ParseInt(string(encoded), 10, 64); err == nil && (n > 9007199254740991 || n < -9007199254740991) {
		quoted, _ := common.Marshal(string(encoded))
		return AuditChangeValue{State: "value", Value: quoted, ValueType: "integer"}
	}
	if n, err := strconv.ParseUint(string(encoded), 10, 64); err == nil && n > 9007199254740991 {
		quoted, _ := common.Marshal(string(encoded))
		return AuditChangeValue{State: "value", Value: quoted, ValueType: "integer"}
	}
	return AuditChangeValue{State: "value", Value: encoded}
}

func auditValueShapeSafe(key string, encoded []byte) bool {
	var value any
	if common.Unmarshal(encoded, &value) != nil {
		return false
	}
	collection := auditWordContains("model_mapping status_code_mapping group_ratios GroupRatio GroupGroupRatio UserGroupRatio GroupRetryTimes permissions scopes auto_groups channel_exclusions exclusive_ratio_groups multi_key_status_list task_extend_plugin_keys upstream_model_update_ignored_models upstream_model_update_last_detected_models upstream_model_update_last_removed_models sidebar_modules", key)
	if raw, ok := value.(string); ok && collection && strings.TrimSpace(raw) != "" {
		// These persisted fields may be JSON strings. Scopes and sidebar modules
		// can also be ordinary strings; only mapping fields require valid JSON.
		if common.UnmarshalJsonStr(raw, &value) != nil {
			return auditWordContains("scopes sidebar_modules", key)
		}
	}
	switch value.(type) {
	case map[string]any, []any:
		if !collection {
			return false
		}
	}
	return auditStructuredValueSafe(value, key, 0)
}

func auditStructuredValueSafe(value any, field string, depth int) bool {
	if depth > 8 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
			if auditWordContains("key apikey authorization password secret clientsecret accesstoken refreshtoken privatekey tokenhash tokenhint", normalized) || strings.Contains(normalized, "password") || strings.Contains(normalized, "secret") {
				return false
			}
			if !auditStructuredValueSafe(child, field, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !auditStructuredValueSafe(child, field, depth+1) {
				return false
			}
		}
	case string:
		if depth > 0 && auditWordContains("group_ratios GroupRatio GroupGroupRatio UserGroupRatio GroupRetryTimes multi_key_status_list channel_exclusions", field) {
			return false
		}
		if field == "permissions" && v != "allow" && v != "deny" {
			return false
		}
	}
	return true
}

func auditWordContains(words, word string) bool {
	return strings.Contains(" "+words+" ", " "+word+" ") && word != "" && !strings.ContainsAny(word, " \t\r\n")
}

func auditSafeOptionKey(key string) bool {
	// Only typed operational modules without credentials/free-form templates.
	for _, prefix := range []string{"performance_setting.", "request_policy.", "stream_setting.", "stream_validation_setting.", "claude_stream_validation_setting.", "stream_protocol_validation_setting.", "log_export_setting.", "channel_setting.", "quota_setting."} {
		if strings.HasPrefix(key, prefix) {
			return auditWordContains("enabled timeout retry_times max_retries max_connections max_idle_connections idle_timeout max_concurrent_queries max_batch_size poll_interval retention_days file_retention_days max_rows max_bytes cooldown_seconds read_timeout write_timeout stream_timeout non_stream_timeout", strings.TrimPrefix(key, prefix))
		}
	}
	return false
}

func auditScalarOptionKey(key string) bool {
	module, field, ok := strings.Cut(key, ".")
	if !ok {
		return false
	}
	var fields map[string]struct{}
	switch module {
	case "rate_limit_setting":
		fields = rateLimitFields
	case "db_pool_setting":
		fields = dbPoolFields
	case "user_session_setting":
		fields = userSessionFields
	case "relay_timeout_setting":
		fields = relayTimeoutFields
	case "channel_daily_limit_setting":
		fields = channelDailyLimitFields
	case "price_monitor_setting":
		fields = priceMonitorFields
	case "relay_error_display_setting":
		fields = relayErrorDisplayFields
	}
	_, allowed := fields[field]
	return allowed
}

func (changes *AuditChanges) project(selfView bool, viewerRole, ownerID int) *AuditChanges {
	if changes == nil {
		return nil
	}
	projected := NewAuditChanges()
	projected.Outcome = changes.Outcome
	filtered := false
	for _, target := range changes.Targets {
		visible := viewerRole >= common.RoleRootUser && !selfView
		if !selfView && viewerRole >= common.RoleAdminUser && (target.Visibility == "admin" || target.Visibility == "self") {
			visible = true
		}
		if (selfView || viewerRole < common.RoleAdminUser) && target.Visibility == "self" && target.OwnerID == ownerID {
			visible = true
		}
		if !visible {
			filtered = true
			continue
		}
		projected.Targets = append(projected.Targets, target)
	}
	if filtered && len(projected.Targets) == 0 {
		return nil
	}
	projected.Truncated = changes.Truncated && !filtered
	return projected
}
