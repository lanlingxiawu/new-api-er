// Regression tests (branch audit M1): a null exclusive ratio must not become an
// explicit zero (free) override. Decoding null into a float64 yields 0; the edit
// path used to accept it and store the raw string.

package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A missing/null amount must not become an explicit zero-price override:
// UpdateUser stores the raw string and the relay parser decodes null as 0.
func TestBranchAuditUserGroupRatioRejectsNullAmounts(t *testing.T) {
	for _, raw := range []string{`{"vip":null}`, `{"vip":1,"free":null}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := parseUserGroupRatiosForEdit(raw)
			require.Error(t, err)
		})
	}
}

func TestParseUserGroupRatiosForEdit(t *testing.T) {
	cases := []struct {
		raw       string
		want      map[string]float64
		wantErr   bool
		nullGroup string
	}{
		{raw: "", want: map[string]float64{}},
		{raw: "{}", want: map[string]float64{}},
		{raw: `{"vip":0}`, want: map[string]float64{"vip": 0}}, // explicit zero = free, allowed
		{raw: `{"vip":0.5,"svip":2}`, want: map[string]float64{"vip": 0.5, "svip": 2}},
		{raw: `{"b":null,"a":null}`, wantErr: true, nullGroup: "a"},
		{raw: `{"vip":"0.5"}`, wantErr: true},
		{raw: `{"vip":true}`, wantErr: true},
		{raw: `{"vip":[1]}`, wantErr: true},
		{raw: `null`, want: map[string]float64{}},
		{raw: `[1]`, wantErr: true},
		{raw: `not json`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := parseUserGroupRatiosForEdit(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.Empty(t, got)
				var invalid *invalidGroupRatioError
				if tc.nullGroup != "" {
					require.ErrorAs(t, err, &invalid)
					assert.Equal(t, tc.nullGroup, invalid.Group)
				} else {
					assert.NotErrorAs(t, err, &invalid)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Through the handler: a null amount is refused with the per-group message and
// nothing is stored; an explicit zero is still accepted.
func TestBranchAuditUpdateUser_NullGroupRatioRejected(t *testing.T) {
	requireDB(t)
	withMasterNode(t, true)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	target := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":1.5}` })

	res := auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"group_ratios": `{"vip":null}`}), i18n.MsgUserGroupRatiosInvalid)
	assert.False(t, res.resp.Success)
	assert.Contains(t, res.resp.Message, "vip")
	assert.Equal(t, `{"vip":1.5}`, auditStoredUser(t, target.Id).GroupRatios)

	res = auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"group_ratios": `{"vip":"x"}`}), "")
	assert.False(t, res.resp.Success)
	assert.Equal(t, `{"vip":1.5}`, auditStoredUser(t, target.Id).GroupRatios)

	res = auditUpdateUser(t, nextTestID(), common.RoleAdminUser,
		auditUserBody(target, map[string]any{"group_ratios": `{"vip":0}`}), "")
	require.True(t, res.resp.Success, res.resp.Message)
	assert.JSONEq(t, `{"vip":0}`, auditStoredUser(t, target.Id).GroupRatios)
}
