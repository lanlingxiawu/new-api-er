package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// mkVendor inserts a Vendor with a unique name and registers cleanup.
func mkVendor(t *testing.T, mut func(v *Vendor)) *Vendor {
	t.Helper()
	requireDB(t)
	v := &Vendor{Name: uniq("zzvendor"), Status: 1}
	if mut != nil {
		mut(v)
	}
	require.NoError(t, v.Insert())
	deleteByID(t, &Vendor{}, v.Id)
	return v
}

func TestVendor_InsertAndGetByID(t *testing.T) {
	requireDB(t)
	v := mkVendor(t, func(v *Vendor) { v.Description = "d"; v.Icon = "ic" })
	assert.NotZero(t, v.CreatedTime)
	assert.NotZero(t, v.UpdatedTime)

	got, err := GetVendorByID(v.Id)
	require.NoError(t, err)
	assert.Equal(t, v.Name, got.Name)
	assert.Equal(t, "d", got.Description)
	assert.Equal(t, "ic", got.Icon)

	// missing id -> record not found
	_, err = GetVendorByID(999_000_111)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestIsVendorNameDuplicated(t *testing.T) {
	requireDB(t)
	v := mkVendor(t, nil)

	dup, err := IsVendorNameDuplicated(0, "")
	require.NoError(t, err)
	assert.False(t, dup, "empty name never duplicated")

	dup, err = IsVendorNameDuplicated(v.Id+1, v.Name)
	require.NoError(t, err)
	assert.True(t, dup, "same name / different id -> duplicated")

	dup, err = IsVendorNameDuplicated(v.Id, v.Name)
	require.NoError(t, err)
	assert.False(t, dup, "self id -> not duplicated")
}

func TestVendor_UpdateAndDelete(t *testing.T) {
	requireDB(t)
	v := mkVendor(t, nil)

	v.Description = "updated"
	v.Icon = "newicon"
	require.NoError(t, v.Update())

	got, err := GetVendorByID(v.Id)
	require.NoError(t, err)
	assert.Equal(t, "updated", got.Description)
	assert.Equal(t, "newicon", got.Icon)

	require.NoError(t, v.Delete())
	_, err = GetVendorByID(v.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGetAllVendors(t *testing.T) {
	requireDB(t)
	a := mkVendor(t, nil)
	b := mkVendor(t, nil)

	all, err := GetAllVendors(0, 1000)
	require.NoError(t, err)
	ids := make(map[int]bool)
	for _, v := range all {
		ids[v.Id] = true
	}
	assert.True(t, ids[a.Id])
	assert.True(t, ids[b.Id])
}

func TestSearchVendors(t *testing.T) {
	requireDB(t)
	kw := uniq("zzsearchvendor")
	v1 := mkVendor(t, func(v *Vendor) { v.Name = kw + "-one" })
	v2 := mkVendor(t, func(v *Vendor) { v.Name = kw + "-two"; v.Description = "has " + kw })

	// keyword matches both (name + description), ordered id DESC
	vendors, total, err := SearchVendors(kw, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, vendors, 2)
	assert.Greater(t, vendors[0].Id, vendors[1].Id, "id DESC ordering")

	// pagination limit 1
	vendors, total, err = SearchVendors(kw, 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, vendors, 1)

	// empty keyword returns all (unscoped) without error; our two are present
	vendors, _, err = SearchVendors("", 0, 1000)
	require.NoError(t, err)
	found := map[int]bool{}
	for _, v := range vendors {
		found[v.Id] = true
	}
	assert.True(t, found[v1.Id] && found[v2.Id])
}
