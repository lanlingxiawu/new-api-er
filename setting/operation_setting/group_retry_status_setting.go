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

var ErrGroupRetryStatusInvalid = errors.New("invalid group retry status configuration")

type GroupRetryStatusSetting struct {
	Enabled bool   `json:"enabled"`
	Rules   string `json:"rules"`
}

type groupRetryStatusSnapshot struct {
	setting GroupRetryStatusSetting
	groups  map[string][10]uint64
}

var groupRetryStatusSetting = GroupRetryStatusSetting{Rules: "{}"}
var groupRetryStatusCompiled config.Snapshot[groupRetryStatusSnapshot]
var groupRetryStatusLastError atomic.Int64

func init() {
	config.GlobalConfig.RegisterSnapshot("group_retry_status_setting", &groupRetryStatusSetting, publishGroupRetryStatusSetting)
}

// compileGroupRetryStatus compiles only on configuration changes. Published
// maps and bitmaps are immutable; failed reloads never affect relay decisions.
func compileGroupRetryStatus(setting GroupRetryStatusSetting) (groupRetryStatusSnapshot, error) {
	result := groupRetryStatusSnapshot{setting: setting, groups: make(map[string][10]uint64)}
	if len(setting.Rules) > 64*1024 {
		return result, fmt.Errorf("%w: rules exceed 64 KiB", ErrGroupRetryStatusInvalid)
	}
	var groups map[string]*string
	if err := common.UnmarshalJsonStr(setting.Rules, &groups); err != nil || groups == nil {
		return result, fmt.Errorf("%w: rules must be an object of strings", ErrGroupRetryStatusInvalid)
	}
	if len(groups) > 256 {
		return result, fmt.Errorf("%w: more than 256 groups", ErrGroupRetryStatusInvalid)
	}
	for group, raw := range groups {
		if group == "" || strings.TrimSpace(group) != group || raw == nil {
			return result, fmt.Errorf("%w: empty group or null rule", ErrGroupRetryStatusInvalid)
		}
		rule := strings.ReplaceAll(*raw, "，", ",")
		if strings.Count(rule, ",")+1 > 128 {
			return result, fmt.Errorf("%w: too many status code segments", ErrGroupRetryStatusInvalid)
		}
		ranges, err := ParseHTTPStatusCodeRanges(rule)
		if err != nil {
			return result, fmt.Errorf("%w: %v", ErrGroupRetryStatusInvalid, err)
		}
		var bitmap [10]uint64
		for _, r := range ranges {
			for code := r.Start; code <= r.End; code++ {
				if code/100 == 2 || IsAlwaysSkipRetryStatusCode(code) {
					continue
				}
				bitmap[code/64] |= uint64(1) << uint(code%64)
			}
		}
		result.groups[group] = bitmap
	}
	return result, nil
}

func ValidateGroupRetryStatusSetting(setting GroupRetryStatusSetting) error {
	_, err := compileGroupRetryStatus(setting)
	return err
}

// ParseGroupRetryStatusUpdate validates the whole candidate before either the
// single-option endpoint or the grouped endpoint persists anything.
func ParseGroupRetryStatusUpdate(values map[string]string) (GroupRetryStatusSetting, error) {
	draft := GetGroupRetryStatusSetting()
	for key, value := range values {
		switch key {
		case "enabled":
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				return draft, fmt.Errorf("%w: enabled must be boolean", ErrGroupRetryStatusInvalid)
			}
			draft.Enabled = enabled
		case "rules":
			draft.Rules = value
		default:
			return draft, fmt.Errorf("%w: field is not editable", ErrGroupRetryStatusInvalid)
		}
	}
	return draft, ValidateGroupRetryStatusSetting(draft)
}

func publishGroupRetryStatusSetting() {
	if previous := groupRetryStatusCompiled.Load(); previous != nil && previous.setting == groupRetryStatusSetting {
		return
	}
	snapshot, err := compileGroupRetryStatus(groupRetryStatusSetting)
	if err != nil {
		now := time.Now().Unix()
		last := groupRetryStatusLastError.Load()
		if (last == 0 || now-last >= 60) && groupRetryStatusLastError.CompareAndSwap(last, now) {
			common.SysError("group retry status reload rejected: " + err.Error())
		}
		return
	}
	groupRetryStatusCompiled.Publish(snapshot)
}

// GetGroupRetryStatusSetting returns the last valid setting, so a malformed
// externally edited draft cannot prevent an administrator from disabling it.
func GetGroupRetryStatusSetting() GroupRetryStatusSetting {
	if snapshot := groupRetryStatusCompiled.Load(); snapshot != nil {
		return snapshot.setting
	}
	return GroupRetryStatusSetting{Rules: "{}"}
}

func ReplaceGroupRetryStatusSetting(setting GroupRetryStatusSetting) {
	config.WithConfigDraft(func() {
		groupRetryStatusSetting = setting
		publishGroupRetryStatusSetting()
	})
}

// GroupRetryStatusAllowed returns (allowed, overridden). Missing entries and
// the disabled feature fall back to each caller's existing retry policy.
func GroupRetryStatusAllowed(group string, code int) (bool, bool) {
	snapshot := groupRetryStatusCompiled.Load()
	if snapshot == nil || !snapshot.setting.Enabled {
		return false, false
	}
	bitmap, ok := snapshot.groups[group]
	if !ok {
		return false, false
	}
	if code < 100 || code > 599 {
		return false, true
	}
	return bitmap[code/64]&(uint64(1)<<uint(code%64)) != 0, true
}
