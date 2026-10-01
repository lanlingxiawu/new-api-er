// Regression tests (branch audit L17): an invalid UserExclusiveGroupRatioCacheMax
// (-5, abc, 1.5) used to be stored in OptionMap and reported as saved while the
// runtime kept its previous cap. It is now rejected before anything is written.

package model

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchAuditRegressionUserGroupRatioCacheMaxRejectsInvalidValues(t *testing.T) {
	ensureOptionMap()
	const key = "UserExclusiveGroupRatioCacheMax"
	prevMax := ratio_setting.GetUserGroupRatioCacheMax()
	prevOpt, hadOpt := optionMapGet(key)
	t.Cleanup(func() {
		ratio_setting.SetUserGroupRatioCacheMax(prevMax)
		common.OptionMapRWMutex.Lock()
		if hadOpt {
			common.OptionMap[key] = prevOpt
		} else {
			delete(common.OptionMap, key)
		}
		common.OptionMapRWMutex.Unlock()
	})
	ratio_setting.SetUserGroupRatioCacheMax(4096)

	for _, value := range []string{"-5", "abc", "1.5"} {
		err := updateOptionMap(key, value)
		shown, _ := optionMapGet(key)
		if err == nil {
			assert.Equal(t, "4096", shown,
				"value %q reported as saved (OptionMap=%q) but runtime cap is %d", value, shown, ratio_setting.GetUserGroupRatioCacheMax())
		}
		assert.Equal(t, 4096, ratio_setting.GetUserGroupRatioCacheMax())
		common.OptionMapRWMutex.Lock()
		common.OptionMap[key] = "4096"
		common.OptionMapRWMutex.Unlock()
	}
}

// Through UpdateOption: an invalid value errors before the database row or the
// in-memory map is touched; a valid value (including 0 = cache off) applies.
func TestUpdateOptionUserGroupRatioCacheMaxValidation(t *testing.T) {
	requireDB(t)
	ensureOptionMap()
	const key = "UserExclusiveGroupRatioCacheMax"
	prevMax := ratio_setting.GetUserGroupRatioCacheMax()
	prevOpt, hadOpt := optionMapGet(key)
	var prevRow Option
	hadRow := DB.Where(&Option{Key: key}).First(&prevRow).Error == nil
	t.Cleanup(func() {
		ratio_setting.SetUserGroupRatioCacheMax(prevMax)
		common.OptionMapRWMutex.Lock()
		if hadOpt {
			common.OptionMap[key] = prevOpt
		} else {
			delete(common.OptionMap, key)
		}
		common.OptionMapRWMutex.Unlock()
		if hadRow {
			DB.Save(&prevRow)
		} else {
			DB.Where(&Option{Key: key}).Delete(&Option{})
		}
	})
	require.NoError(t, UpdateOption(key, "4096"))

	for _, value := range []string{"-1", "abc", "1.5", ""} {
		require.Error(t, UpdateOption(key, value), value)
		shown, _ := optionMapGet(key)
		assert.Equal(t, "4096", shown)
		var row Option
		require.NoError(t, DB.Where(&Option{Key: key}).First(&row).Error)
		assert.Equal(t, "4096", row.Value, "rejected value %q reached the database", value)
		assert.Equal(t, 4096, ratio_setting.GetUserGroupRatioCacheMax())
	}

	for value, want := range map[string]int{"0": 0, "100": 100} {
		require.NoError(t, UpdateOption(key, value))
		assert.Equal(t, want, ratio_setting.GetUserGroupRatioCacheMax())
		shown, _ := optionMapGet(key)
		assert.Equal(t, value, shown)
	}
}

// A bad value stored before validation existed is loaded on every SyncOptions
// pass. It must fall back to the default (runtime and settings page agree),
// log once rather than on every sync, and log again only for a new bad value.
func TestLoadInvalidStoredUserGroupRatioCacheMaxFallsBackOnce(t *testing.T) {
	ensureOptionMap()
	const key = "UserExclusiveGroupRatioCacheMax"
	prevMax := ratio_setting.GetUserGroupRatioCacheMax()
	prevOpt, hadOpt := optionMapGet(key)
	var logBuf bytes.Buffer
	common.LogWriterMu.Lock()
	prevErrWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuf
	common.LogWriterMu.Unlock()
	invalidUserGroupRatioCacheMaxMu.Lock()
	prevLogged := invalidUserGroupRatioCacheMaxLogged
	invalidUserGroupRatioCacheMaxLogged = ""
	invalidUserGroupRatioCacheMaxMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = prevErrWriter
		common.LogWriterMu.Unlock()
		invalidUserGroupRatioCacheMaxMu.Lock()
		invalidUserGroupRatioCacheMaxLogged = prevLogged
		invalidUserGroupRatioCacheMaxMu.Unlock()
		ratio_setting.SetUserGroupRatioCacheMax(prevMax)
		common.OptionMapRWMutex.Lock()
		if hadOpt {
			common.OptionMap[key] = prevOpt
		} else {
			delete(common.OptionMap, key)
		}
		common.OptionMapRWMutex.Unlock()
	})
	logLines := func() int { return strings.Count(logBuf.String(), key+" is invalid") }
	def := ratio_setting.DefaultUserGroupRatioCacheMax

	ratio_setting.SetUserGroupRatioCacheMax(100)
	for i := 0; i < 3; i++ { // three sync passes
		require.NoError(t, updateOptionMap(key, "-5"))
		assert.Equal(t, def, ratio_setting.GetUserGroupRatioCacheMax())
		shown, _ := optionMapGet(key)
		assert.Equal(t, strconv.Itoa(def), shown, "the settings page shows the value in effect")
	}
	assert.Equal(t, 1, logLines(), "logged once, not per sync")

	require.NoError(t, updateOptionMap(key, "abc"))
	assert.Equal(t, 2, logLines(), "a different bad value is logged again")

	require.NoError(t, updateOptionMap(key, "200"))
	assert.Equal(t, 200, ratio_setting.GetUserGroupRatioCacheMax(), "a valid value applies as before")
	require.NoError(t, updateOptionMap(key, "abc"))
	assert.Equal(t, 3, logLines(), "after a valid value, the bad value is reported again")
}
