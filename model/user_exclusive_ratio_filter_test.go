package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSearchUsersExclusiveRatio(t *testing.T) {
	// Use the configured project DB when available. The fallback is isolated and
	// lets the same behavioral cases run without a database service in CI.
	if DB == nil {
		previousType := common.MainDatabaseType()
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		DB = db
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
		initCol()
		t.Cleanup(func() {
			require.NoError(t, sqlDB.Close())
			DB = nil
			common.SetMainDatabaseType(previousType)
			initCol()
		})
		require.NoError(t, db.AutoMigrate(&User{}, &EmployeeProfile{}, &CustomerProfile{}))
	}
	group := uniq("exclusive")
	cases := []struct {
		name string
		raw  any
		want bool
	}{
		{"null column", nil, false},
		{"empty", "", false},
		{"whitespace", " \t\r\n", false},
		{"empty object", "{}", false},
		{"formatted empty", " { \n\t } ", false},
		{"json null", "null", false},
		{"zero", `{"vip":0}`, true},
		{"multiple", `{"vip":0.5,"other":2}`, true},
		{"formatted", " {\n\t\"vip\" : 1.2e-3 \r\n} ", true},
		{"unicode and escaped key", `{"专属\\\"\u4e2d":1}`, true},
		{"stored null entry", `{"vip":null}`, true},
		{"negative stored entry", `{"vip":-1}`, true},
		{"invalid json", `{"vip":`, false},
		{"invalid escape", `{"\q":1}`, false},
		{"trailing garbage", `{"vip":1}oops`, false},
		{"array", `[1]`, false},
		{"string", `"vip"`, false},
		{"non numeric entry", `{"vip":"1"}`, false},
		{"nested entry", `{"vip":{"ratio":1}}`, false},
		{"boolean entry", `{"vip":true}`, false},
		{"invalid number", `{"vip":01}`, false},
		{"invalid null", `{"vip":NULL}`, false},
	}
	var configured, unconfigured []int
	for _, tc := range cases {
		u := mkUser(t, func(u *User) { u.Group = group })
		require.NoError(t, DB.Model(u).Update("group_ratios", tc.raw).Error)
		if tc.want {
			configured = append(configured, u.Id)
		} else {
			unconfigured = append(unconfigured, u.Id)
		}
		t.Run(tc.name, func(t *testing.T) {
			for _, present := range []bool{true, false} {
				users, total, err := SearchUsers(u.Username, group, nil, nil, false, false, false, 0, 10,
					UserSearchOptions{HasExclusiveRatio: &present})
				require.NoError(t, err)
				if present == tc.want {
					require.EqualValues(t, 1, total)
					require.Len(t, users, 1)
					require.Equal(t, u.Id, users[0].Id)
				} else {
					require.Zero(t, total)
					require.Empty(t, users)
				}
			}
		})
	}
	for _, present := range []bool{true, false} {
		expected := unconfigured
		if present {
			expected = configured
		}
		for page, id := range expected {
			users, total, err := SearchUsers("", group, nil, nil, false, false, false, page, 1,
				UserSearchOptions{HasExclusiveRatio: &present, Sort: NewUserSortOptions("id", "asc")})
			require.NoError(t, err)
			require.EqualValues(t, len(expected), total)
			require.Len(t, users, 1)
			require.Equal(t, id, users[0].Id)
		}
	}
	users, total, err := SearchUsers("", group, nil, nil, false, false, false, 0, 100)
	require.NoError(t, err)
	require.Len(t, users, len(cases))
	require.EqualValues(t, len(cases), total)
	role, status, present := common.RoleAdminUser, common.UserStatusDisabled, true
	target := mkUser(t, func(u *User) {
		u.Group, u.Role, u.Status, u.GroupRatios = group, role, status, `{"other":0}`
	})
	users, total, err = SearchUsers(target.Username, group, &role, &status, false, false, false, 0, 10,
		UserSearchOptions{HasExclusiveRatio: &present})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	require.Equal(t, target.Id, users[0].Id)
	_, total, err = SearchUsers("", group, &role, &status, false, true, false, 0, 10,
		UserSearchOptions{HasExclusiveRatio: &present})
	require.NoError(t, err)
	require.Zero(t, total)
}
