package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// UpdateUser reads the user outside its transaction. If the group-deletion
// cleanup rewrites group_ratios between that read and the edit's UPDATE, the
// edit must not write the value it read back — that would resurrect the ratio
// the cleanup just removed. Only a request that changes the ratios writes the
// column. See docs/design/user-exclusive-group-ratio-deletion.md §14.4.

// cleanupBeforeUserUpdate simulates the cleanup's CAS landing right before the
// next UPDATE of the user's row (after UpdateUser's read, inside its
// transaction window).
func cleanupBeforeUserUpdate(t *testing.T, userID int, from, to string) *bool {
	t.Helper()
	const hook = "test:cleanup_between_read_and_edit"
	fired := false
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(hook, func(db *gorm.DB) {
		if fired || db.Statement.Table != "users" {
			return
		}
		fired = true
		require.NoError(t, model.DB.Model(&model.User{}).
			Where("id = ? AND group_ratios = ?", userID, from).
			Updates(map[string]any{
				"group_ratios":    to,
				"profile_version": gorm.Expr("profile_version + ?", 1),
			}).Error)
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(hook) })
	return &fired
}

func TestUpdateUserDoesNotResurrectRatioRemovedByConcurrentCleanup(t *testing.T) {
	requireDB(t)
	const stored = `{"gone":0.1,"vip":2}`
	cases := []struct {
		name      string
		registry  map[string]float64
		body      map[string]any
		slaveNode bool
	}{
		{"group_ratios omitted", map[string]float64{"default": 1, "vip": 2}, map[string]any{"remark": "omitted"}, false},
		// The group still existed at the baseGroups check and was deleted after it.
		{"round trip on master", map[string]float64{"default": 1, "vip": 2, "gone": 1}, map[string]any{"group_ratios": stored, "remark": "round trip"}, false},
		{"round trip on slave", map[string]float64{"default": 1, "vip": 2}, map[string]any{"group_ratios": stored, "remark": "slave"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withGroupRatio(t, tc.registry)
			if tc.slaveNode {
				asSlaveNode(t)
			}
			user := mkUser(t, func(u *model.User) { u.GroupRatios = stored })
			fired := cleanupBeforeUserUpdate(t, user.Id, stored, `{"vip":2}`)

			resp := updateUserWithGroupRatios(t, user, tc.body)
			require.True(t, resp.Success, resp.Message)
			require.True(t, *fired, "the simulated cleanup must land inside the edit")
			assert.JSONEq(t, `{"vip":2}`, storedGroupRatios(t, user.Id), "the removed ratio must stay removed")
		})
	}
}

// A request that changes the ratios still writes them.
func TestUpdateUserWritesChangedRatiosDespiteConcurrentWrite(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":2}` })
	fired := cleanupBeforeUserUpdate(t, user.Id, `{"vip":2}`, `{}`)

	resp := updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": `{"vip":1.5}`})
	require.True(t, resp.Success, resp.Message)
	require.True(t, *fired)
	assert.JSONEq(t, `{"vip":1.5}`, storedGroupRatios(t, user.Id))
}

// A corrupt stored value compares as an empty map; an explicit "{}" must still
// overwrite it instead of being mistaken for a round trip.
func TestUpdateUserOverwritesCorruptStoredRatios(t *testing.T) {
	requireDB(t)
	withGroupRatio(t, map[string]float64{"default": 1, "vip": 2})
	user := mkUser(t, func(u *model.User) { u.GroupRatios = `{"vip":` })

	resp := updateUserWithGroupRatios(t, user, map[string]any{"group_ratios": "{}"})
	require.True(t, resp.Success, resp.Message)
	assert.Equal(t, "{}", storedGroupRatios(t, user.Id))
}
