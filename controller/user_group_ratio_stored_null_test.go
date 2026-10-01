package controller

// Stored null exclusive ratios (written before the edit path rejected null)
// must not block later edits of the user. See
// docs/design/user-exclusive-group-ratio-deletion.md §14.1.

import (
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseUserGroupRatiosForEdit is the strict parse (no stored nulls to
// tolerate), kept for the partition tests of the submit parser.
func parseUserGroupRatiosForEdit(raw string) (map[string]float64, error) {
	ratios, _, err := parseSubmittedUserGroupRatios(raw, nil)
	return ratios, err
}

func TestParseStoredUserGroupRatios(t *testing.T) {
	cases := []struct {
		raw       string
		want      map[string]float64
		wantNulls []string
	}{
		{"", map[string]float64{}, nil},
		{"{}", map[string]float64{}, nil},
		{`{"vip":0.5}`, map[string]float64{"vip": 0.5}, nil},
		{`{"vip":null,"a":1,"b":null}`, map[string]float64{"a": 1}, []string{"b", "vip"}},
		{`{"vip":null}`, map[string]float64{}, []string{"vip"}},
		{`not json`, map[string]float64{}, nil},
		{`{"vip":"1"}`, map[string]float64{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, nulls := parseStoredUserGroupRatios(tc.raw)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantNulls, nulls)
		})
	}
}

func TestParseSubmittedUserGroupRatios_ToleratesOnlyStoredNulls(t *testing.T) {
	stored := []string{"vip"}

	got, skipped, err := parseSubmittedUserGroupRatios(`{"vip":null,"a":1}`, stored)
	require.NoError(t, err, "a stored null sent back unchanged is not a new null")
	assert.True(t, skipped)
	assert.Equal(t, map[string]float64{"a": 1}, got)

	got, skipped, err = parseSubmittedUserGroupRatios(`{"a":1}`, stored)
	require.NoError(t, err)
	assert.False(t, skipped)
	assert.Equal(t, map[string]float64{"a": 1}, got)

	_, _, err = parseSubmittedUserGroupRatios(`{"vip":null,"a":null}`, stored)
	var invalid *invalidGroupRatioError
	require.ErrorAs(t, err, &invalid, "a null on a group that was not null before is still rejected")
	assert.Equal(t, "a", invalid.Group)

	_, _, err = parseSubmittedUserGroupRatios(`{"vip":null}`, nil)
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "vip", invalid.Group)

	got, skipped, err = parseSubmittedUserGroupRatios(`{"vip":0}`, stored)
	require.NoError(t, err)
	assert.False(t, skipped)
	assert.Equal(t, map[string]float64{"vip": 0}, got, "an explicit zero replacing the null is a real rule")
}

// Non-master node: the form round-trips the stored value; with a stored null
// that used to fail (stored parsed as empty, resubmission rejected), blocking
// every edit of the user on that node.
func TestUpdateUserOnSlaveToleratesStoredNullRatio(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2, "a": 1})
	asSlaveNode(t)
	const stored = `{"vip":null,"a":1}`
	user := mkUser(t, func(u *model.User) { u.GroupRatios = stored })

	resp := updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": stored, "remark": "unrelated edit"})
	assert.True(t, resp.Success, resp.Message)
	assert.JSONEq(t, stored, storedGroupRatios(t, user.Id), "a non-master node never rewrites the stored value")

	// Dropping the ineffective null is not a change either (relay skips it).
	resp = updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"a":1}`, "remark": "unrelated edit 2"})
	assert.True(t, resp.Success, resp.Message)
	assert.JSONEq(t, stored, storedGroupRatios(t, user.Id))

	// A real change still needs the master node.
	resp = updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"vip":1,"a":1}`})
	assert.False(t, resp.Success)
	assert.JSONEq(t, stored, storedGroupRatios(t, user.Id))
}

// Master node: the round-trip saves, and the ineffective null is dropped from
// the stored value (billing is unchanged: relay skipped it already).
func TestUpdateUserOnMasterDropsRoundTrippedStoredNull(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2, "a": 1})
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":null,"a":1}` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"vip":null,"a":1}`})
	assert.True(t, resp.Success, resp.Message)
	assert.JSONEq(t, `{"a":1}`, storedGroupRatios(t, user.Id))

	// With the null gone, submitting null again is a new null and is rejected.
	resp = updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"vip":null,"a":1}`})
	assert.False(t, resp.Success)
	assert.NotContains(t, resp.Message, i18n.MsgUserGroupRatiosInvalid, "the message must be translated")
	assert.JSONEq(t, `{"a":1}`, storedGroupRatios(t, user.Id))
}
