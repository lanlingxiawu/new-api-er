package operation_setting

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

var ErrGroupRetryGuardInvalid = errors.New("invalid group retry protection configuration")

type GroupRetryGuardRule struct {
	Enabled            bool   `json:"enabled"`
	WindowSeconds      int    `json:"window_seconds"`
	RPMThreshold       int64  `json:"rpm_threshold"`
	LatencyThresholdMs int64  `json:"latency_threshold_ms"`
	SlowCountThreshold int64  `json:"slow_count_threshold"`
	CooldownMode       string `json:"cooldown_mode"`
	CooldownSeconds    int    `json:"cooldown_seconds"`
	CooldownMultiplier int    `json:"cooldown_multiplier,omitempty"`
	MaxCooldownSeconds int    `json:"max_cooldown_seconds,omitempty"`
	ResetAfterSeconds  int    `json:"reset_after_seconds,omitempty"`
}

type GroupRetryGuardSetting struct {
	Enabled bool   `json:"enabled"`
	Rules   string `json:"rules"`
	// Server-generated revision invalidates old distributed cooldowns on edits.
	Revision string `json:"revision"`
}

// CompiledGroupRetryGuard is immutable after publication, including Rules.
type CompiledGroupRetryGuard struct {
	GroupRetryGuardSetting
	Rules map[string]GroupRetryGuardRule
}

var groupRetryGuardSetting = GroupRetryGuardSetting{Rules: "{}"}
var groupRetryGuardCompiled config.Snapshot[CompiledGroupRetryGuard]
var groupRetryGuardLastError atomic.Int64

func init() {
	config.GlobalConfig.RegisterSnapshot("group_retry_guard_setting", &groupRetryGuardSetting, publishGroupRetryGuard)
}

func CompileGroupRetryGuard(setting GroupRetryGuardSetting) (CompiledGroupRetryGuard, error) {
	result := CompiledGroupRetryGuard{GroupRetryGuardSetting: setting, Rules: map[string]GroupRetryGuardRule{}}
	if len(setting.Rules) > 65536 {
		return result, ErrGroupRetryGuardInvalid
	}
	var raw map[string]map[string]any
	if err := common.UnmarshalJsonStr(setting.Rules, &raw); err != nil || raw == nil || len(raw) > 256 {
		return result, ErrGroupRetryGuardInvalid
	}
	required := []string{"enabled", "window_seconds", "rpm_threshold", "latency_threshold_ms", "slow_count_threshold", "cooldown_mode", "cooldown_seconds"}
	for group, fields := range raw {
		if group == "" || group == "auto" || strings.TrimSpace(group) != group || fields == nil {
			return result, ErrGroupRetryGuardInvalid
		}
		for _, key := range required {
			if fields[key] == nil {
				return result, ErrGroupRetryGuardInvalid
			}
		}
		for key, value := range fields {
			switch key {
			case "enabled", "window_seconds", "rpm_threshold", "latency_threshold_ms", "slow_count_threshold", "cooldown_mode", "cooldown_seconds", "cooldown_multiplier", "max_cooldown_seconds", "reset_after_seconds":
				if value == nil {
					return result, ErrGroupRetryGuardInvalid
				}
			default:
				return result, ErrGroupRetryGuardInvalid
			}
		}
		data, err := common.Marshal(fields)
		if err != nil {
			return result, fmt.Errorf("%w: %v", ErrGroupRetryGuardInvalid, err)
		}
		var rule GroupRetryGuardRule
		if err = common.Unmarshal(data, &rule); err != nil {
			return result, ErrGroupRetryGuardInvalid
		}
		if rule.WindowSeconds < 10 || rule.WindowSeconds > 3600 || rule.RPMThreshold < 1 || rule.RPMThreshold > 10000000 || rule.LatencyThresholdMs < 1 || rule.LatencyThresholdMs > 3600000 || rule.SlowCountThreshold < 1 || rule.SlowCountThreshold > 10000000 || rule.CooldownSeconds < 1 || rule.CooldownSeconds > 86400 {
			return result, ErrGroupRetryGuardInvalid
		}
		switch rule.CooldownMode {
		case "fixed":
			rule.CooldownMultiplier = 1
			rule.MaxCooldownSeconds = rule.CooldownSeconds
			rule.ResetAfterSeconds = 10
		case "progressive":
			if rule.CooldownMultiplier < 2 || rule.CooldownMultiplier > 10 || rule.MaxCooldownSeconds < rule.CooldownSeconds || rule.MaxCooldownSeconds > 86400 || rule.ResetAfterSeconds < 10 || rule.ResetAfterSeconds > 86400 {
				return result, ErrGroupRetryGuardInvalid
			}
		default:
			return result, ErrGroupRetryGuardInvalid
		}
		result.Rules[group] = rule
	}
	return result, nil
}

func ParseGroupRetryGuardUpdate(values map[string]string) (GroupRetryGuardSetting, error) {
	draft := GetGroupRetryGuardSetting()
	for key, value := range values {
		switch key {
		case "enabled":
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				return draft, ErrGroupRetryGuardInvalid
			}
			draft.Enabled = enabled
		case "rules":
			draft.Rules = value
		default:
			return draft, ErrGroupRetryGuardInvalid
		}
	}
	_, err := CompileGroupRetryGuard(draft)
	return draft, err
}

func publishGroupRetryGuard() {
	if old := groupRetryGuardCompiled.Load(); old != nil && old.GroupRetryGuardSetting == groupRetryGuardSetting {
		return
	}
	snapshot, err := CompileGroupRetryGuard(groupRetryGuardSetting)
	if err != nil {
		now := time.Now().Unix()
		last := groupRetryGuardLastError.Load()
		if (last == 0 || now-last >= 60) && groupRetryGuardLastError.CompareAndSwap(last, now) {
			common.SysError("group retry protection reload rejected: " + err.Error())
		}
		return
	}
	groupRetryGuardCompiled.Publish(snapshot)
}

func GetGroupRetryGuardSetting() GroupRetryGuardSetting {
	return groupRetryGuardCompiled.Load().GroupRetryGuardSetting
}

func GroupRetryGuardSnapshot() *CompiledGroupRetryGuard { return groupRetryGuardCompiled.Load() }

func ReplaceGroupRetryGuardSetting(setting GroupRetryGuardSetting) {
	config.WithConfigDraft(func() { groupRetryGuardSetting = setting; publishGroupRetryGuard() })
}
