package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupExclusiveRatioDB(t *testing.T) {
	t.Helper()
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
}

func TestSearchUsersExclusiveRatio(t *testing.T) {
	setupExclusiveRatioDB(t)
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

func TestSearchUsersExclusiveRatioGroup(t *testing.T) {
	setupExclusiveRatioDB(t)
	group := uniq("exclusive_group")
	for _, tc := range []struct {
		name, key string
		raw       any
		want      bool
	}{
		{"zero", "vip", `{"vip":0}`, true},
		{"first", "vip", `{"vip":0.5,"other":2}`, true},
		{"middle", "vip", `{"a":2,"vip":0.5,"other":2}`, true},
		{"last", "vip", `{"other":2,"vip":0.5}`, true},
		{"formatted", "vip", " {\n\t\"vip\" : 1.2e-3 \r\n} ", true},
		{"null entry", "vip", `{"vip":null}`, true},
		{"negative entry", "vip", `{"vip":-1}`, true},
		{"absent", "vip", `{"default":1}`, false},
		{"prefix", "vip", `{"vip2":1}`, false},
		{"suffix", "vip", `{"supervip":1}`, false},
		{"case sensitive", "vip", `{"VIP":1}`, false},
		{"escaped ascii", "vip", `{"\u0076i\u0070":1}`, true},
		{"unicode", "专属", `{"专属":1}`, true},
		{"escaped unicode", "专属", `{"\u4e13\u5C5E":1}`, true},
		{"astral unicode", "🌟", `{"🌟":1}`, true},
		{"escaped astral unicode", "🌟", `{"\ud83c\uDF1F":1}`, true},
		{"escaped quote", `v"ip`, `{"v\"ip":1}`, true},
		{"escaped slash", "v/ip", `{"v\/ip":1}`, true},
		{"literal slash", "v/ip", `{"v/ip":1}`, true},
		{"escaped backslash", `v\ip`, `{"v\\ip":1}`, true},
		{"unicode backslash", `v\ip`, `{"v\u005cip":1}`, true},
		{"escaped key decoy", "vip", `{"decoy\",\"vip":1}`, false},
		{"regex literal", `a.*[b]()+?^$|{}%_`, `{"a.*[b]()+?^$|{}%_":1}`, true},
		{"regex not wildcard", "v.*", `{"vip":1}`, false},
		{"sql literal", `a' OR 1=1 --`, `{"a' OR 1=1 --":1}`, true},
		{"html escaping", "<vip>&", `{"\u003cvip\u003E\u0026":1}`, true},
		{"control escaping", "a\n\t\b\f\r", `{"a\n\t\b\f\r":1}`, true},
		{"null column", "vip", nil, false},
		{"empty", "vip", "", false},
		{"empty object", "vip", " {\n} ", false},
		{"json null", "vip", "null", false},
		{"invalid json", "vip", `{"vip":`, false},
		{"trailing data", "vip", `{"vip":1}oops`, false},
		{"array", "vip", `[{"vip":1}]`, false},
		{"invalid other entry", "vip", `{"vip":1,"other":"2"}`, false},
		{"string value", "vip", `{"vip":"1"}`, false},
		{"boolean value", "vip", `{"vip":true}`, false},
		{"nested value", "vip", `{"vip":{"ratio":1}}`, false},
		{"invalid number", "vip", `{"vip":01}`, false},
		{"invalid null", "vip", `{"vip":NULL}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := mkUser(t, func(u *User) { u.Group = group })
			require.NoError(t, DB.Model(u).Update("group_ratios", tc.raw).Error)
			users, total, err := SearchUsers(u.Username, group, nil, nil, false, false, false, 0, 10,
				UserSearchOptions{ExclusiveRatioGroup: tc.key})
			require.NoError(t, err)
			if !tc.want {
				require.Zero(t, total)
				require.Empty(t, users)
				return
			}
			require.EqualValues(t, 1, total)
			require.Len(t, users, 1)
			require.Equal(t, u.Id, users[0].Id)
		})
	}

	t.Run("pagination and combined filters", func(t *testing.T) {
		accountGroup := uniq("ratio_paging")
		var ids []int
		for range 25 {
			u := mkUser(t, func(u *User) {
				u.Group, u.GroupRatios = accountGroup, `{"vip":0}`
				u.Role, u.Status = common.RoleAdminUser, common.UserStatusDisabled
			})
			ids = append(ids, u.Id)
		}
		for range 25 {
			mkUser(t, func(u *User) { u.Group = accountGroup })
		}
		role, status := common.RoleAdminUser, common.UserStatusDisabled
		options := UserSearchOptions{ExclusiveRatioGroup: "vip", Sort: NewUserSortOptions("id", "asc")}
		users, total, err := SearchUsers("", accountGroup, &role, &status, false, false, false, 20, 20, options)
		require.NoError(t, err)
		require.EqualValues(t, 25, total)
		require.Len(t, users, 5)
		for i, user := range users {
			require.Equal(t, ids[20+i], user.Id)
		}
		for _, present := range []bool{true, false} {
			options.HasExclusiveRatio = &present
			users, total, err = SearchUsers("", accountGroup, nil, nil, false, false, false, 0, 50, options)
			require.NoError(t, err)
			if present {
				require.EqualValues(t, 25, total)
				require.Len(t, users, 25)
			} else {
				require.Zero(t, total)
				require.Empty(t, users)
			}
		}
	})
}
