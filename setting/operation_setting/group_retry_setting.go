package operation_setting

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// GroupRetryDisabled is the per-group "do not retry" value. It means one
// attempt, not "skip this group": the group is still tried once (design §7.2).
const GroupRetryDisabled = -1

// MaxGroupRetryTimes bounds a single group's quota. It is deliberately much
// smaller than the total-attempt cap: a group that needs more than this many
// retries has a health problem that retrying will not fix.
const MaxGroupRetryTimes = 20

// groupRetryTimesMap holds group name -> retry quota. Absent means "inherit the
// global RetryTimes"; -1 means "no retry"; a positive value is the quota.
var groupRetryTimesMap = types.NewRWMap[string, int]()

// GetGroupRetryTimes reports the quota configured for a group. The second
// return value is false when the group has no entry, which means "inherit".
func GetGroupRetryTimes(group string) (int, bool) {
	if group == "" {
		return 0, false
	}
	return groupRetryTimesMap.Get(group)
}

func GetGroupRetryTimesCopy() map[string]int {
	return groupRetryTimesMap.ReadAll()
}

func GroupRetryTimes2JSONString() string {
	return groupRetryTimesMap.MarshalJSONString()
}

func UpdateGroupRetryTimesByJSONString(jsonStr string) error {
	return types.LoadFromJsonString(groupRetryTimesMap, jsonStr)
}

// CheckGroupRetryTimes validates an operator-supplied JSON map before it is
// stored. Values other than -1 (disabled) must be within [0, MaxGroupRetryTimes];
// 0 is accepted and means "inherit the global setting", matching the per-user
// timeout fields' convention.
func CheckGroupRetryTimes(jsonStr string) error {
	parsed := make(map[string]int)
	if err := common.UnmarshalJsonStr(jsonStr, &parsed); err != nil {
		return err
	}
	for name, times := range parsed {
		if times == GroupRetryDisabled {
			continue
		}
		if times < 0 {
			return fmt.Errorf("group retry times must be -1 or not less than 0: %s", name)
		}
		if times > MaxGroupRetryTimes {
			return fmt.Errorf("group retry times must not exceed %d: %s", MaxGroupRetryTimes, name)
		}
	}
	return nil
}
