package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// JSONValue — driver.Valuer / sql.Scanner / json.Marshaler round-trips (pure).
// ---------------------------------------------------------------------------

func TestJSONValue_Value(t *testing.T) {
	// nil -> NULL
	var jNil JSONValue
	v, err := jNil.Value()
	require.NoError(t, err)
	assert.Nil(t, v)

	// non-nil -> raw bytes
	j := JSONValue([]byte(`["a","b"]`))
	v, err = j.Value()
	require.NoError(t, err)
	assert.Equal(t, []byte(`["a","b"]`), v)
}

func TestJSONValue_Scan(t *testing.T) {
	var j JSONValue

	// nil -> nil
	require.NoError(t, j.Scan(nil))
	assert.Nil(t, j)

	// []byte -> deep copy (mutating source must not affect stored value)
	src := []byte(`{"k":1}`)
	require.NoError(t, j.Scan(src))
	assert.Equal(t, []byte(`{"k":1}`), []byte(j))
	src[0] = 'X'
	assert.Equal(t, byte('{'), j[0], "Scan must copy the source bytes")

	// string -> bytes
	require.NoError(t, j.Scan(`"hi"`))
	assert.Equal(t, []byte(`"hi"`), []byte(j))

	// other type -> JSON-marshaled
	require.NoError(t, j.Scan(42))
	assert.Equal(t, []byte("42"), []byte(j))
}

func TestJSONValue_MarshalUnmarshalJSON(t *testing.T) {
	// nil marshals to null
	var jNil JSONValue
	b, err := jNil.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, []byte("null"), b)

	// non-nil marshals to raw content
	j := JSONValue([]byte(`{"x":1}`))
	b, err = j.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"x":1}`), b)

	// UnmarshalJSON copies input
	var out JSONValue
	data := []byte(`[1,2,3]`)
	require.NoError(t, out.UnmarshalJSON(data))
	assert.Equal(t, []byte(`[1,2,3]`), []byte(out))
	data[0] = 'X'
	assert.Equal(t, byte('['), out[0], "UnmarshalJSON must copy input")

	// nil data -> nil
	var out2 JSONValue
	require.NoError(t, out2.UnmarshalJSON(nil))
	assert.Nil(t, out2)
}

// ---------------------------------------------------------------------------
// PrefillGroup CRUD + listing.
// ---------------------------------------------------------------------------

func mkPrefillGroup(t *testing.T, mut func(g *PrefillGroup)) *PrefillGroup {
	t.Helper()
	requireDB(t)
	g := &PrefillGroup{
		Name:  uniq("zzpfg"),
		Type:  "model",
		Items: JSONValue([]byte(`["gpt-4o"]`)),
	}
	if mut != nil {
		mut(g)
	}
	require.NoError(t, g.Insert())
	deleteByID(t, &PrefillGroup{}, g.Id)
	return g
}

func TestPrefillGroup_InsertAndDuplicateName(t *testing.T) {
	requireDB(t)
	g := mkPrefillGroup(t, nil)
	assert.NotZero(t, g.CreatedTime)
	assert.NotZero(t, g.UpdatedTime)

	// duplicate-name checks
	dup, err := IsPrefillGroupNameDuplicated(0, "")
	require.NoError(t, err)
	assert.False(t, dup)

	dup, err = IsPrefillGroupNameDuplicated(g.Id+1, g.Name)
	require.NoError(t, err)
	assert.True(t, dup)

	dup, err = IsPrefillGroupNameDuplicated(g.Id, g.Name)
	require.NoError(t, err)
	assert.False(t, dup, "self id excluded")
}

func TestPrefillGroup_UpdateAndDelete(t *testing.T) {
	requireDB(t)
	g := mkPrefillGroup(t, nil)

	g.Description = "updated"
	g.Items = JSONValue([]byte(`["a","b","c"]`))
	require.NoError(t, g.Update())

	var reloaded PrefillGroup
	require.NoError(t, DB.First(&reloaded, g.Id).Error)
	assert.Equal(t, "updated", reloaded.Description)
	assert.JSONEq(t, `["a","b","c"]`, string(reloaded.Items))

	require.NoError(t, DeletePrefillGroupByID(g.Id))
	err := DB.First(&PrefillGroup{}, g.Id).Error
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGetAllPrefillGroups(t *testing.T) {
	requireDB(t)
	typeA := uniq("typeA")
	typeB := uniq("typeB")

	gA1 := mkPrefillGroup(t, func(g *PrefillGroup) { g.Type = typeA })
	gA2 := mkPrefillGroup(t, func(g *PrefillGroup) { g.Type = typeA })
	gB := mkPrefillGroup(t, func(g *PrefillGroup) { g.Type = typeB })

	// filter by typeA -> only the two typeA groups
	groups, err := GetAllPrefillGroups(typeA)
	require.NoError(t, err)
	require.Len(t, groups, 2)
	// ordered by updated_time DESC; gA2 inserted last so it comes first
	// (both share the same second may tie — assert set membership instead).
	got := map[int]bool{groups[0].Id: true, groups[1].Id: true}
	assert.True(t, got[gA1.Id] && got[gA2.Id])

	// filter by typeB -> only gB
	groups, err = GetAllPrefillGroups(typeB)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, gB.Id, groups[0].Id)

	// empty type -> returns all (our three are present)
	groups, err = GetAllPrefillGroups("")
	require.NoError(t, err)
	present := map[int]bool{}
	for _, g := range groups {
		present[g.Id] = true
	}
	assert.True(t, present[gA1.Id] && present[gA2.Id] && present[gB.Id])
}
