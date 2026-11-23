package controller

import (
	"errors"
	"maps"
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// prepareUserGroupRatios shares the website's master/cleanup and numeric rules
// with the restricted token patch. Only the website tolerates round-tripped
// historical null entries; new token submissions must contain numeric ratios.
func prepareUserGroupRatios(c *gin.Context, stored, raw string, allowStoredNull bool) (string, []string, bool, bool) {
	current, storedNull := parseStoredUserGroupRatios(stored)
	if !allowStoredNull {
		storedNull = nil
	}
	submitted, skippedNull, err := parseSubmittedUserGroupRatios(raw, storedNull)
	if err != nil {
		var invalid *invalidGroupRatioError
		if errors.As(err, &invalid) {
			common.ApiErrorI18n(c, i18n.MsgUserGroupRatiosInvalid, map[string]any{"Group": invalid.Group})
		} else {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		}
		return "", nil, false, false
	}
	if !common.IsMasterNode {
		if !maps.Equal(current, submitted) {
			common.ApiErrorI18n(c, i18n.MsgGroupRatioMasterRequired)
			return "", nil, false, false
		}
		return stored, nil, false, true
	}
	var removed []string
	baseGroups := ratio_setting.GetGroupRatioCopy()
	for name, ratio := range submitted {
		if _, ok := baseGroups[name]; !ok {
			removed = append(removed, name)
			delete(submitted, name)
			continue
		}
		if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			common.ApiErrorI18n(c, i18n.MsgUserGroupRatiosInvalid, map[string]any{"Group": name})
			return "", nil, false, false
		}
	}
	if busy := activeGroupRatioCleanups(changedGroupRatioNames(current, submitted)); len(busy) > 0 {
		common.ApiErrorI18n(c, i18n.MsgUserGroupRatioCleanupInProgress, map[string]any{"Groups": strings.Join(busy, ", ")})
		return "", nil, false, false
	}
	storedCorrupt := false
	if stored != "" && stored != "{}" {
		_, _, err := ratio_setting.DecodeUserGroupRatios(stored)
		storedCorrupt = err != nil
	}
	write := !maps.Equal(current, submitted) || skippedNull || storedCorrupt
	if !write {
		return stored, removed, false, true
	}
	if len(removed) > 0 || skippedNull {
		sort.Strings(removed)
		encoded, err := common.Marshal(submitted)
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return "", nil, false, false
		}
		raw = string(encoded)
	}
	return raw, removed, true, true
}
