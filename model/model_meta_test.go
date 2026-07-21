package model

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// mkModel inserts a Model with a unique name via Model.Insert and registers cleanup.
func mkModel(t *testing.T, mut func(m *Model)) *Model {
	t.Helper()
	requireDB(t)
	m := &Model{
		ModelName: uniq("zzmodel"),
		Status:    1,
		NameRule:  NameRuleExact,
	}
	if mut != nil {
		mut(m)
	}
	require.NoError(t, m.Insert())
	deleteByID(t, &Model{}, m.Id)
	return m
}

// ---------------------------------------------------------------------------
// Model.Insert — zero-value preservation for status / sync_official.
// ---------------------------------------------------------------------------

func TestModel_Insert_ZeroValuePreservation(t *testing.T) {
	requireDB(t)

	// Status=0 and SyncOfficial=0 must survive despite GORM default:1 tags.
	m := mkModel(t, func(m *Model) {
		m.Status = 0
		m.SyncOfficial = 0
	})
	var reloaded Model
	require.NoError(t, DB.First(&reloaded, m.Id).Error)
	assert.Equal(t, 0, reloaded.Status)
	assert.Equal(t, 0, reloaded.SyncOfficial)
	assert.NotZero(t, reloaded.CreatedTime)
	assert.NotZero(t, reloaded.UpdatedTime)

	// A non-default, non-zero value round-trips too.
	m2 := mkModel(t, func(m *Model) { m.Status = 2; m.SyncOfficial = 1 })
	var r2 Model
	require.NoError(t, DB.First(&r2, m2.Id).Error)
	assert.Equal(t, 2, r2.Status)
	assert.Equal(t, 1, r2.SyncOfficial)
}

// ---------------------------------------------------------------------------
// IsModelNameDuplicated
// ---------------------------------------------------------------------------

func TestIsModelNameDuplicated(t *testing.T) {
	requireDB(t)
	m := mkModel(t, nil)

	// empty name -> never duplicated, no error
	dup, err := IsModelNameDuplicated(0, "")
	require.NoError(t, err)
	assert.False(t, dup)

	// same name, different id -> duplicated
	dup, err = IsModelNameDuplicated(m.Id+1, m.ModelName)
	require.NoError(t, err)
	assert.True(t, dup)

	// same name, same id (self) -> not duplicated
	dup, err = IsModelNameDuplicated(m.Id, m.ModelName)
	require.NoError(t, err)
	assert.False(t, dup)

	// name that does not exist -> not duplicated
	dup, err = IsModelNameDuplicated(0, uniq("zznope"))
	require.NoError(t, err)
	assert.False(t, dup)
}

// ---------------------------------------------------------------------------
// Model.Update / Delete
// ---------------------------------------------------------------------------

func TestModel_UpdateAndDelete(t *testing.T) {
	requireDB(t)
	m := mkModel(t, func(m *Model) { m.Description = "orig"; m.Status = 1 })

	m.Description = "changed"
	m.Status = 0 // Update uses Select(...) so zero values are written
	m.Tags = "t1"
	require.NoError(t, m.Update())

	var reloaded Model
	require.NoError(t, DB.First(&reloaded, m.Id).Error)
	assert.Equal(t, "changed", reloaded.Description)
	assert.Equal(t, 0, reloaded.Status)
	assert.Equal(t, "t1", reloaded.Tags)

	require.NoError(t, m.Delete())
	err := DB.First(&Model{}, m.Id).Error
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound) // soft-deleted -> not found
}

// ---------------------------------------------------------------------------
// GetVendorModelCounts
// ---------------------------------------------------------------------------

func TestGetVendorModelCounts(t *testing.T) {
	requireDB(t)
	vendorID := 900_000_000 + (nextTestID() % 1000)
	mkModel(t, func(m *Model) { m.VendorID = vendorID })
	mkModel(t, func(m *Model) { m.VendorID = vendorID })

	counts, err := GetVendorModelCounts()
	require.NoError(t, err)
	assert.EqualValues(t, 2, counts[int64(vendorID)])
}

// ---------------------------------------------------------------------------
// normalizeLookupValues — pure: trim, drop empties, dedup, preserve order.
// ---------------------------------------------------------------------------

func TestNormalizeLookupValues(t *testing.T) {
	assert.Equal(t, []string{}, normalizeLookupValues(nil))
	assert.Equal(t, []string{}, normalizeLookupValues([]string{"", "   "}))
	assert.Equal(t,
		[]string{"a", "b"},
		normalizeLookupValues([]string{" a ", "a", "b", "  ", "b"}),
	)
}

// ---------------------------------------------------------------------------
// GetBoundChannelsByModelsMap
// ---------------------------------------------------------------------------

func TestGetBoundChannelsByModelsMap(t *testing.T) {
	requireDB(t)

	// empty input -> empty map, no error
	res, err := GetBoundChannelsByModelsMap(nil)
	require.NoError(t, err)
	assert.Empty(t, res)

	model := uniq("zzboundmodel")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = uniq("bgrp")
		c.Models = model
		c.Status = common.ChannelStatusEnabled
		c.Type = 3
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	res, err = GetBoundChannelsByModelsMap([]string{model})
	require.NoError(t, err)
	require.Contains(t, res, model)
	require.Len(t, res[model], 1)
	assert.Equal(t, ch.Name, res[model][0].Name)
	assert.Equal(t, 3, res[model][0].Type)
}

// ---------------------------------------------------------------------------
// GetPreferredModelOwnerChannelTypes
// ---------------------------------------------------------------------------

func TestGetPreferredModelOwnerChannelTypes(t *testing.T) {
	requireDB(t)

	// empty models -> empty result
	res, err := GetPreferredModelOwnerChannelTypes(nil, nil)
	require.NoError(t, err)
	assert.Empty(t, res)

	group := uniq("owngrp")
	model := uniq("zzownermodel")
	ch := mkChannel(t, func(c *Channel) {
		c.Group = group
		c.Models = model
		c.Status = common.ChannelStatusEnabled
		c.Type = 14
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	// without group filter
	res, err = GetPreferredModelOwnerChannelTypes([]string{model, "", " " + model + " "}, nil)
	require.NoError(t, err)
	assert.Equal(t, 14, res[model])

	// with matching group filter
	res, err = GetPreferredModelOwnerChannelTypes([]string{model}, []string{group})
	require.NoError(t, err)
	assert.Equal(t, 14, res[model])

	// with non-matching group filter -> no rows
	res, err = GetPreferredModelOwnerChannelTypes([]string{model}, []string{uniq("othergrp")})
	require.NoError(t, err)
	assert.NotContains(t, res, model)
}

// ---------------------------------------------------------------------------
// SearchModels + GetAllModels
// ---------------------------------------------------------------------------

func TestSearchModels(t *testing.T) {
	requireDB(t)
	tag := uniq("zzsrchtag")
	vendorID := 910_000_000 + (nextTestID() % 1000)

	m1 := mkModel(t, func(m *Model) { m.Tags = tag; m.VendorID = vendorID; m.Status = 1; m.SyncOfficial = 1 })
	m2 := mkModel(t, func(m *Model) { m.Tags = tag; m.VendorID = vendorID; m.Status = 0; m.SyncOfficial = 0 })

	// keyword by tag returns both, ordered id DESC (m2 inserted after m1)
	models, total, err := SearchModels(tag, "", "", "", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, models, 2)
	assert.Greater(t, models[0].Id, models[1].Id, "ordered by id DESC")

	// vendor numeric filter + status enabled -> only m1
	models, total, err = SearchModels(tag, strconv.Itoa(vendorID), "enabled", "", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, models, 1)
	assert.Equal(t, m1.Id, models[0].Id)

	// sync filter "no" -> only m2
	models, total, err = SearchModels(tag, "", "", "no", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, models, 1)
	assert.Equal(t, m2.Id, models[0].Id)

	// pagination: limit 1 returns first page only
	models, total, err = SearchModels(tag, "", "", "", 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, models, 1)

	// GetAllModels delegates to SearchModels with no filters.
	all, err := GetAllModels(0, 5)
	require.NoError(t, err)
	assert.NotNil(t, all)
}

// TestSearchModels_VendorNameJoin exercises the non-numeric vendor branch that
// joins the vendors table by name. NOTE: it must be called WITHOUT a keyword,
// because keyword + vendor-name-join is broken (see TestSearchModels_KeywordVendorJoinBug).
func TestSearchModels_VendorNameJoin(t *testing.T) {
	requireDB(t)
	vendorName := uniq("zzsvendor")
	v := &Vendor{Name: vendorName, Status: 1}
	require.NoError(t, v.Insert())
	deleteByID(t, &Vendor{}, v.Id)

	m := mkModel(t, func(m *Model) { m.VendorID = v.Id })

	// Empty keyword -> only the vendor-name join filter applies.
	models, total, err := SearchModels("", vendorName, "", "", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, models, 1)
	assert.Equal(t, m.Id, models[0].Id)
}

// TestSearchModels_KeywordVendorJoinBug pins the FIX for a real defect in
// SearchModels (model/model_meta.go): when a keyword AND a non-numeric vendor
// name were both supplied, the keyword WHERE clause referenced unqualified
// columns ("model_name LIKE ? OR description LIKE ? OR tags LIKE ?"). Once the
// vendors table is JOINed (non-numeric vendor branch), `description` exists in
// BOTH `models` and `vendors`, so MySQL failed with:
//
//	Error 1052 (23000): Column 'description' in where clause is ambiguous
//
// FIXED: the keyword columns are now qualified as models.model_name /
// models.description / models.tags, so keyword + vendor-name filtering succeeds.
func TestSearchModels_KeywordVendorJoinBug(t *testing.T) {
	requireDB(t)
	vendorName := uniq("zzbugvendor")
	v := &Vendor{Name: vendorName, Status: 1}
	require.NoError(t, v.Insert())
	deleteByID(t, &Vendor{}, v.Id)

	tag := uniq("zzbugtag")
	mkModel(t, func(m *Model) { m.VendorID = v.Id; m.Tags = tag })

	models, total, err := SearchModels(tag, vendorName, "", "", 0, 10)
	// FIXED: keyword + vendor-name join no longer errors; it returns the model
	// matched by the keyword (tags) and the joined vendor name.
	require.NoError(t, err, "keyword + vendor-name join must succeed after qualifying columns")
	assert.EqualValues(t, 1, total)
	require.Len(t, models, 1)
}

// ---------------------------------------------------------------------------
// parseModelStatusFilter / parseModelSyncFilter — exhaustive branch coverage.
// ---------------------------------------------------------------------------

func TestParseModelStatusFilter(t *testing.T) {
	cases := []struct {
		in    string
		value int
		ok    bool
	}{
		{"", 0, false},
		{"all", 0, false},
		{"ALL", 0, false},
		{"enabled", 1, true},
		{"1", 1, true},
		{"disabled", 0, true},
		{"0", 0, true},
		{"2", 2, true},   // arbitrary numeric
		{"xyz", 0, false}, // non-numeric junk
	}
	for _, c := range cases {
		v, ok := parseModelStatusFilter(c.in)
		assert.Equalf(t, c.ok, ok, "ok for %q", c.in)
		assert.Equalf(t, c.value, v, "value for %q", c.in)
	}
}

func TestParseModelSyncFilter(t *testing.T) {
	cases := []struct {
		in    string
		value int
		ok    bool
	}{
		{"", 0, false},
		{"all", 0, false},
		{"yes", 1, true},
		{"1", 1, true},
		{"no", 0, true},
		{"0", 0, true},
		{"5", 5, true},
		{"junk", 0, false},
	}
	for _, c := range cases {
		v, ok := parseModelSyncFilter(c.in)
		assert.Equalf(t, c.ok, ok, "ok for %q", c.in)
		assert.Equalf(t, c.value, v, "value for %q", c.in)
	}
}
