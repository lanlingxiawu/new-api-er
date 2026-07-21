package model

import (
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// option.go — the runtime option key/value store synced to the `options` table
// and propagated to setting/* via the big switch in updateOptionMap.
//
// These tests mutate GLOBAL setting state. Every test snapshots the specific
// global(s) it touches and restores them via t.Cleanup so it never poisons the
// rest of the suite. Custom option keys are unique; the option rows they insert
// are hard-deleted on cleanup.
// ---------------------------------------------------------------------------

// ensureOptionMap guarantees common.OptionMap is non-nil so updateOptionMap can
// write into it without panicking, without rebuilding the whole map.
func ensureOptionMap() {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()
}

// optionMapGet reads a key under the RW lock.
func optionMapGet(key string) (string, bool) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	v, ok := common.OptionMap[key]
	return v, ok
}

// cleanupOptionRow hard-deletes an option row (string PK) after the test.
func cleanupOptionRow(t *testing.T, key string) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("`key` = ?", key).Delete(&Option{})
		}
	})
}

// ---------------------------------------------------------------------------
// InitOptionMap / loadOptionsFromDatabase
// ---------------------------------------------------------------------------

func TestInitOptionMap(t *testing.T) {
	requireDB(t)
	// Snapshot the whole map pointer; InitOptionMap rebuilds it from defaults +
	// DB, so restore the prior map afterwards to avoid poisoning siblings.
	common.OptionMapRWMutex.RLock()
	prev := common.OptionMap
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = prev
		common.OptionMapRWMutex.Unlock()
	})

	InitOptionMap()

	// Representative keys across types must be present.
	for _, k := range []string{
		"SystemName", "PasswordLoginEnabled", "FileUploadPermission",
		"ChannelDisableThreshold", "QuotaForNewUser", "ModelRatio",
	} {
		_, ok := optionMapGet(k)
		assert.Truef(t, ok, "InitOptionMap should populate %q", k)
	}
}

// ---------------------------------------------------------------------------
// UpdateOption — DB persistence + in-memory map
// ---------------------------------------------------------------------------

func TestUpdateOption_CustomKeyPersists(t *testing.T) {
	requireDB(t)
	ensureOptionMap()
	key := uniq("optcustom")
	cleanupOptionRow(t, key)

	require.NoError(t, UpdateOption(key, "hello"))

	// in-memory map updated
	v, ok := optionMapGet(key)
	require.True(t, ok)
	assert.Equal(t, "hello", v)

	// persisted to DB
	var row Option
	require.NoError(t, DB.Where("`key` = ?", key).First(&row).Error)
	assert.Equal(t, "hello", row.Value)

	// update again overwrites
	require.NoError(t, UpdateOption(key, "world"))
	var row2 Option
	require.NoError(t, DB.Where("`key` = ?", key).First(&row2).Error)
	assert.Equal(t, "world", row2.Value)
}

func TestUpdateOptionsBulk(t *testing.T) {
	requireDB(t)
	ensureOptionMap()

	// empty map is a no-op
	require.NoError(t, UpdateOptionsBulk(map[string]string{}))

	k1 := uniq("bulk1")
	k2 := uniq("bulk2")
	cleanupOptionRow(t, k1)
	cleanupOptionRow(t, k2)

	require.NoError(t, UpdateOptionsBulk(map[string]string{k1: "v1", k2: "v2"}))

	v1, ok1 := optionMapGet(k1)
	v2, ok2 := optionMapGet(k2)
	require.True(t, ok1)
	require.True(t, ok2)
	assert.Equal(t, "v1", v1)
	assert.Equal(t, "v2", v2)

	var r1, r2 Option
	require.NoError(t, DB.Where("`key` = ?", k1).First(&r1).Error)
	require.NoError(t, DB.Where("`key` = ?", k2).First(&r2).Error)
	assert.Equal(t, "v1", r1.Value)
	assert.Equal(t, "v2", r2.Value)
}

// ---------------------------------------------------------------------------
// updateOptionMap — propagation switch across representative value types
// ---------------------------------------------------------------------------

func TestUpdateOptionMap_BoolSetting(t *testing.T) {
	ensureOptionMap()
	prev := common.RegisterEnabled
	t.Cleanup(func() { common.RegisterEnabled = prev })

	require.NoError(t, updateOptionMap("RegisterEnabled", "true"))
	assert.True(t, common.RegisterEnabled)
	require.NoError(t, updateOptionMap("RegisterEnabled", "false"))
	assert.False(t, common.RegisterEnabled)

	// map also reflects the raw value
	v, _ := optionMapGet("RegisterEnabled")
	assert.Equal(t, "false", v)
}

func TestUpdateOptionMap_PermissionInt(t *testing.T) {
	ensureOptionMap()
	prev := common.FileUploadPermission
	t.Cleanup(func() { common.FileUploadPermission = prev })

	require.NoError(t, updateOptionMap("FileUploadPermission", "3"))
	assert.Equal(t, 3, common.FileUploadPermission)
}

func TestUpdateOptionMap_PlainInt(t *testing.T) {
	ensureOptionMap()
	prev := common.QuotaForNewUser
	t.Cleanup(func() { common.QuotaForNewUser = prev })

	require.NoError(t, updateOptionMap("QuotaForNewUser", "654321"))
	assert.Equal(t, 654321, common.QuotaForNewUser)
}

func TestUpdateOptionMap_Float(t *testing.T) {
	ensureOptionMap()
	prev := common.ChannelDisableThreshold
	t.Cleanup(func() { common.ChannelDisableThreshold = prev })

	require.NoError(t, updateOptionMap("ChannelDisableThreshold", "0.75"))
	assert.InDelta(t, 0.75, common.ChannelDisableThreshold, 1e-9)
}

func TestUpdateOptionMap_String(t *testing.T) {
	ensureOptionMap()
	prevName := common.SystemName
	prevPay := operation_setting.PayAddress
	t.Cleanup(func() {
		common.SystemName = prevName
		operation_setting.PayAddress = prevPay
	})

	require.NoError(t, updateOptionMap("SystemName", "MyTestGateway"))
	assert.Equal(t, "MyTestGateway", common.SystemName)

	require.NoError(t, updateOptionMap("PayAddress", "https://pay.example.com"))
	assert.Equal(t, "https://pay.example.com", operation_setting.PayAddress)
}

func TestUpdateOptionMap_JSONSetting(t *testing.T) {
	ensureOptionMap()
	// snapshot + restore the model ratio JSON
	orig := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = updateOptionMap("ModelRatio", orig) })

	// valid JSON is accepted and applied
	require.NoError(t, updateOptionMap("ModelRatio", `{"test-model-xyz":2.5}`))
	got, ok, _ := ratio_setting.GetModelRatio("test-model-xyz")
	assert.True(t, ok)
	assert.InDelta(t, 2.5, got, 1e-9)

	// invalid JSON surfaces an error through the switch
	err := updateOptionMap("ModelRatio", "{not-json")
	assert.Error(t, err)
}

func TestUpdateOptionMap_HierarchicalConfig(t *testing.T) {
	ensureOptionMap()
	// checkin_setting is a registered layered config; key "checkin_setting.x"
	// is routed through handleConfigUpdate (returns true, short-circuits switch).
	s := operation_setting.GetCheckinSetting()
	prev := *s
	t.Cleanup(func() { *s = prev })

	require.NoError(t, updateOptionMap("checkin_setting.min_quota", "4242"))
	assert.Equal(t, 4242, operation_setting.GetCheckinSetting().MinQuota)
}

func TestUpdateOptionMap_UnregisteredHierarchicalConfig(t *testing.T) {
	ensureOptionMap()
	// A dotted key whose config is not registered is NOT handled by the config
	// system; it falls through and is simply stored in the map.
	key := "no_such_config.some_field"
	require.NoError(t, updateOptionMap(key, "v"))
	v, ok := optionMapGet(key)
	require.True(t, ok)
	assert.Equal(t, "v", v)
}

func TestUpdateOptionMap_DisplayInCurrencyCompat(t *testing.T) {
	ensureOptionMap()
	// Legacy DisplayInCurrencyEnabled is mirrored into general_setting; the code
	// path must not panic and must return nil for both true/false.
	require.NoError(t, updateOptionMap("DisplayInCurrencyEnabled", "true"))
	require.NoError(t, updateOptionMap("DisplayInCurrencyEnabled", "false"))
}

// ---------------------------------------------------------------------------
// Concurrency guard — updateOptionMap serialises through OptionMapRWMutex.
// Distinct keys, run under `go test -race` to detect data races.
// ---------------------------------------------------------------------------

func TestUpdateOptionMap_ConcurrentDistinctKeys(t *testing.T) {
	ensureOptionMap()
	const n = 24
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			k := "concopt_" + strconv.Itoa(i)
			_ = updateOptionMap(k, strconv.Itoa(i))
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		v, ok := optionMapGet("concopt_" + strconv.Itoa(i))
		require.True(t, ok)
		assert.Equal(t, strconv.Itoa(i), v)
	}
}
