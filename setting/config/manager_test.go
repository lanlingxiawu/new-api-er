package config

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// managed is a representative registered config module.
type managed struct {
	Enabled  bool   `json:"enabled"`
	MaxItems int    `json:"max_items"`
	Name     string `json:"name"`
}

// ---------------------------------------------------------------------------
// NewConfigManager / Register / Get
// ---------------------------------------------------------------------------

func TestNewConfigManager_InitializedEmpty(t *testing.T) {
	cm := NewConfigManager()
	require.NotNil(t, cm)
	require.NotNil(t, cm.configs)
	assert.Empty(t, cm.configs)
	assert.Nil(t, cm.Get("missing"), "unknown key returns nil")
}

func TestRegister_NewKey(t *testing.T) {
	cm := NewConfigManager()
	cfg := &managed{Name: "a"}
	cm.Register("mod", cfg)

	got := cm.Get("mod")
	require.NotNil(t, got)
	assert.Same(t, cfg, got.(*managed), "Get returns the exact pointer registered")
}

func TestRegister_DuplicateKeyOverwrites(t *testing.T) {
	cm := NewConfigManager()
	first := &managed{Name: "first"}
	second := &managed{Name: "second"}
	cm.Register("mod", first)
	cm.Register("mod", second)

	assert.Same(t, second, cm.Get("mod").(*managed), "second registration wins")
}

func TestRegister_MultipleModulesIndependent(t *testing.T) {
	cm := NewConfigManager()
	a := &managed{Name: "a"}
	b := &managed{Name: "b"}
	cm.Register("a", a)
	cm.Register("b", b)

	assert.Same(t, a, cm.Get("a").(*managed))
	assert.Same(t, b, cm.Get("b").(*managed))
}

func TestGet_UnknownKeyReturnsNil(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("known", &managed{})
	assert.Nil(t, cm.Get("unknown"))
}

// ---------------------------------------------------------------------------
// LoadFromDB
// ---------------------------------------------------------------------------

func TestLoadFromDB_UpdatesMatchingPrefix(t *testing.T) {
	cm := NewConfigManager()
	cfg := &managed{}
	cm.Register("mod", cfg)

	err := cm.LoadFromDB(map[string]string{
		"mod.enabled":   "true",
		"mod.max_items": "50",
		"mod.name":      "svc",
		"other.enabled": "true", // different prefix -> ignored
		"modtypo":       "x",     // no dot -> not the "mod." prefix
	})
	require.NoError(t, err)

	assert.True(t, cfg.Enabled)
	assert.Equal(t, 50, cfg.MaxItems)
	assert.Equal(t, "svc", cfg.Name)
}

func TestLoadFromDB_ExactNameWithoutDotIgnored(t *testing.T) {
	// boundary: prefix is "mod." so a bare "mod" key must not match.
	cm := NewConfigManager()
	cfg := &managed{Name: "keep"}
	cm.Register("mod", cfg)

	err := cm.LoadFromDB(map[string]string{"mod": "value"})
	require.NoError(t, err)
	assert.Equal(t, "keep", cfg.Name, "bare module name (no dot) does not match")
}

func TestLoadFromDB_NoMatchingKeysLeavesConfigUntouched(t *testing.T) {
	// decision: len(configMap)==0 path skips updateConfigFromMap.
	cm := NewConfigManager()
	cfg := &managed{Enabled: true, MaxItems: 7, Name: "orig"}
	cm.Register("mod", cfg)

	err := cm.LoadFromDB(map[string]string{"unrelated.key": "1"})
	require.NoError(t, err)
	assert.Equal(t, &managed{Enabled: true, MaxItems: 7, Name: "orig"}, cfg)
}

func TestLoadFromDB_EmptyOptions(t *testing.T) {
	cm := NewConfigManager()
	cfg := &managed{Name: "orig"}
	cm.Register("mod", cfg)

	require.NoError(t, cm.LoadFromDB(map[string]string{}))
	require.NoError(t, cm.LoadFromDB(nil))
	assert.Equal(t, "orig", cfg.Name)
}

func TestLoadFromDB_NestedKeySubKeyPreserved(t *testing.T) {
	// A key with an extra dot keeps the remainder as the config key. It won't
	// match any field of managed, so nothing changes — but it exercises the
	// TrimPrefix path with a multi-segment remainder.
	cm := NewConfigManager()
	cfg := &managed{Name: "orig"}
	cm.Register("mod", cfg)

	err := cm.LoadFromDB(map[string]string{"mod.sub.deep": "v", "mod.name": "changed"})
	require.NoError(t, err)
	assert.Equal(t, "changed", cfg.Name)
}

func TestLoadFromDB_MultipleModules(t *testing.T) {
	cm := NewConfigManager()
	a := &managed{}
	b := &managed{}
	cm.Register("a", a)
	cm.Register("b", b)

	err := cm.LoadFromDB(map[string]string{
		"a.name": "alpha",
		"b.name": "bravo",
	})
	require.NoError(t, err)
	assert.Equal(t, "alpha", a.Name)
	assert.Equal(t, "bravo", b.Name)
}

func TestLoadFromDB_NonPointerConfigIsNoOp(t *testing.T) {
	// updateConfigFromMap rejects non-pointer configs; LoadFromDB stays nil.
	cm := NewConfigManager()
	cm.Register("mod", managed{Name: "orig"}) // value, not pointer

	err := cm.LoadFromDB(map[string]string{"mod.name": "new"})
	require.NoError(t, err)
	assert.Equal(t, managed{Name: "orig"}, cm.Get("mod").(managed))
}

// ---------------------------------------------------------------------------
// SaveToDB
// ---------------------------------------------------------------------------

func TestSaveToDB_CollectsAllFieldsWithModulePrefix(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("mod", &managed{Enabled: true, MaxItems: 9, Name: "svc"})

	saved := map[string]string{}
	err := cm.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, "true", saved["mod.enabled"])
	assert.Equal(t, "9", saved["mod.max_items"])
	assert.Equal(t, "svc", saved["mod.name"])
}

func TestSaveToDB_MultipleModules(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("a", &managed{Name: "alpha"})
	cm.Register("b", &managed{Name: "bravo"})

	saved := map[string]string{}
	err := cm.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "alpha", saved["a.name"])
	assert.Equal(t, "bravo", saved["b.name"])
}

func TestSaveToDB_UpdateFuncErrorPropagates(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("mod", &managed{Name: "svc"})

	sentinel := errors.New("db write failed")
	err := cm.SaveToDB(func(key, value string) error {
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
}

func TestSaveToDB_ConfigToMapErrorPropagates(t *testing.T) {
	// configToMap fails to marshal an unmarshalable field -> SaveToDB returns err.
	cm := NewConfigManager()
	cm.Register("bad", &badStructConfig{})

	called := false
	err := cm.SaveToDB(func(key, value string) error {
		called = true
		return nil
	})
	require.Error(t, err)
	assert.False(t, called, "updateFunc not reached when configToMap fails")
}

func TestSaveToDB_LoadRoundTrip(t *testing.T) {
	// Full persistence round trip via the two public manager methods.
	src := NewConfigManager()
	src.Register("mod", &managed{Enabled: true, MaxItems: 123, Name: "roundtrip"})

	store := map[string]string{}
	require.NoError(t, src.SaveToDB(func(key, value string) error {
		store[key] = value
		return nil
	}))

	dst := NewConfigManager()
	loaded := &managed{}
	dst.Register("mod", loaded)
	require.NoError(t, dst.LoadFromDB(store))

	assert.Equal(t, &managed{Enabled: true, MaxItems: 123, Name: "roundtrip"}, loaded)
}

// ---------------------------------------------------------------------------
// ExportAllConfigs
// ---------------------------------------------------------------------------

func TestExportAllConfigs_FlattensWithPrefix(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("a", &managed{Name: "alpha", MaxItems: 1})
	cm.Register("b", &managed{Name: "bravo", MaxItems: 2})

	all := cm.ExportAllConfigs()
	assert.Equal(t, "alpha", all["a.name"])
	assert.Equal(t, "1", all["a.max_items"])
	assert.Equal(t, "bravo", all["b.name"])
	assert.Equal(t, "2", all["b.max_items"])
}

func TestExportAllConfigs_EmptyManager(t *testing.T) {
	cm := NewConfigManager()
	assert.Empty(t, cm.ExportAllConfigs())
}

func TestExportAllConfigs_SkipsConfigWithMarshalError(t *testing.T) {
	// decision: configToMap error -> continue, other modules still exported.
	cm := NewConfigManager()
	cm.Register("good", &managed{Name: "ok"})
	cm.Register("bad", &badStructConfig{})

	all := cm.ExportAllConfigs()
	assert.Equal(t, "ok", all["good.name"], "good module still exported")
	for k := range all {
		assert.NotContains(t, k, "bad.", "faulty module produces no keys")
	}
}

func TestExportAllConfigs_NonStructConfigProducesNoKeys(t *testing.T) {
	// configToMap returns (nil,nil) for non-struct -> no keys, no error.
	cm := NewConfigManager()
	cm.Register("scalar", 42)
	cm.Register("good", &managed{Name: "ok"})

	all := cm.ExportAllConfigs()
	assert.Equal(t, "ok", all["good.name"])
	for k := range all {
		assert.NotContains(t, k, "scalar", "scalar module yields nothing")
	}
}

// ---------------------------------------------------------------------------
// Concurrency (-race)
// ---------------------------------------------------------------------------

func TestConfigManager_ConcurrentAccessIsRaceFree(t *testing.T) {
	cm := NewConfigManager()
	// Pre-register a stable module so readers always have something to touch.
	cm.Register("stable", &managed{Name: "stable"})

	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers * 5)

	for i := 0; i < workers; i++ {
		i := i
		// Registrations
		go func() { defer wg.Done(); cm.Register(fmt.Sprintf("mod-%d", i), &managed{Name: "x", MaxItems: i}) }()
		// Reads
		go func() { defer wg.Done(); _ = cm.Get("stable") }()
		// LoadFromDB
		go func() {
			defer wg.Done()
			_ = cm.LoadFromDB(map[string]string{"stable.max_items": fmt.Sprintf("%d", i)})
		}()
		// SaveToDB
		go func() {
			defer wg.Done()
			_ = cm.SaveToDB(func(key, value string) error { return nil })
		}()
		// ExportAllConfigs
		go func() { defer wg.Done(); _ = cm.ExportAllConfigs() }()
	}

	wg.Wait()
	// Sanity: stable module still resolvable and correctly typed.
	require.NotNil(t, cm.Get("stable"))
}
