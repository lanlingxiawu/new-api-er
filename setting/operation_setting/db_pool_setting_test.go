package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDBPoolSnapshotResolvesLogInheritance(t *testing.T) {
	original := dbPoolSetting
	t.Cleanup(func() { dbPoolSetting = original; PublishDBPoolSetting() })

	dbPoolSetting.MaxIdleConns = 12
	dbPoolSetting.MaxOpenConns = 34
	dbPoolSetting.LogMaxIdleConns = 0
	dbPoolSetting.LogMaxOpenConns = 0
	PublishDBPoolSetting()
	snapshot := GetDBPoolSnapshot()
	require.Equal(t, 12, snapshot.LogMaxIdle)
	require.Equal(t, 34, snapshot.LogMaxOpen)
}

func TestValidateDBPoolSettingRejectsInvalidCombination(t *testing.T) {
	setting := DBPoolSetting{MaxIdleConns: 20, MaxOpenConns: 10, MaxLifetimeSec: 60}
	require.Error(t, ValidateDBPoolSetting(setting))
}

// 边界值分析：每个范围在下界-1 / 下界 / 上界 / 上界+1 各取一点。
func TestValidateDBPoolSettingBoundaries(t *testing.T) {
	valid := DBPoolSetting{MaxIdleConns: 100, MaxOpenConns: 1000, MaxLifetimeSec: 60}
	with := func(mutate func(*DBPoolSetting)) DBPoolSetting {
		out := valid
		mutate(&out)
		return out
	}

	cases := []struct {
		name    string
		setting DBPoolSetting
		wantErr bool
	}{
		{"idle 下界-1", with(func(s *DBPoolSetting) { s.MaxIdleConns = 0 }), true},
		{"idle 下界", with(func(s *DBPoolSetting) { s.MaxIdleConns = 1 }), false},
		{"idle 上界", with(func(s *DBPoolSetting) { s.MaxIdleConns = 10_000; s.MaxOpenConns = 10_000 }), false},
		{"idle 上界+1", with(func(s *DBPoolSetting) { s.MaxIdleConns = 10_001; s.MaxOpenConns = 100_000 }), true},
		{"open 下界-1", with(func(s *DBPoolSetting) { s.MaxIdleConns = 1; s.MaxOpenConns = 0 }), true},
		{"open 下界", with(func(s *DBPoolSetting) { s.MaxIdleConns = 1; s.MaxOpenConns = 1 }), false},
		{"open 上界", with(func(s *DBPoolSetting) { s.MaxOpenConns = 100_000 }), false},
		{"open 上界+1", with(func(s *DBPoolSetting) { s.MaxOpenConns = 100_001 }), true},
		{"lifetime 下界-1", with(func(s *DBPoolSetting) { s.MaxLifetimeSec = 0 }), true},
		{"lifetime 下界", with(func(s *DBPoolSetting) { s.MaxLifetimeSec = 1 }), false},
		{"lifetime 上界", with(func(s *DBPoolSetting) { s.MaxLifetimeSec = 86_400 }), false},
		{"lifetime 上界+1", with(func(s *DBPoolSetting) { s.MaxLifetimeSec = 86_401 }), true},
		{"idle == open 合法", with(func(s *DBPoolSetting) { s.MaxIdleConns = 500; s.MaxOpenConns = 500 }), false},
		{"log idle 负数", with(func(s *DBPoolSetting) { s.LogMaxIdleConns = -1 }), true},
		{"log open 负数", with(func(s *DBPoolSetting) { s.LogMaxOpenConns = -1 }), true},
		{"log idle 上界+1", with(func(s *DBPoolSetting) { s.LogMaxIdleConns = 10_001 }), true},
		{"log open 上界+1", with(func(s *DBPoolSetting) { s.LogMaxOpenConns = 100_001 }), true},
		{"log 全 0 表示继承主库", with(func(s *DBPoolSetting) { s.LogMaxIdleConns = 0; s.LogMaxOpenConns = 0 }), false},
		{"log idle 显式大于 log open", with(func(s *DBPoolSetting) {
			s.LogMaxIdleConns = 900
			s.LogMaxOpenConns = 800
		}), true},
		{"log idle 显式，open 继承主库后仍合法", with(func(s *DBPoolSetting) {
			s.LogMaxIdleConns = 900
			s.LogMaxOpenConns = 0 // 继承 1000
		}), false},
		{"log idle 显式，open 继承主库后越界", with(func(s *DBPoolSetting) {
			s.MaxOpenConns = 100
			s.MaxIdleConns = 100
			s.LogMaxIdleConns = 900
			s.LogMaxOpenConns = 0 // 继承 100，900 > 100
		}), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDBPoolSetting(tc.setting)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// 快照的钳制与 Validate 的判定是两套逻辑：Validate 拒绝非法输入，
// 而快照必须在任何输入下都产出可用值（历史库里可能存着越界的旧值）。
func TestDBPoolSnapshotClampsOutOfRangeStoredValues(t *testing.T) {
	original := GetDBPoolSetting()
	t.Cleanup(func() { ReplaceDBPoolSetting(original) })

	ReplaceDBPoolSetting(DBPoolSetting{
		MaxIdleConns:   -5,
		MaxOpenConns:   0,
		MaxLifetimeSec: 999_999,
	})
	snapshot := GetDBPoolSnapshot()
	require.Equal(t, 100, snapshot.MaxIdle, "非正值回落到默认")
	require.Equal(t, 1000, snapshot.MaxOpen, "非正值回落到默认")
	require.Equal(t, 86_400.0, snapshot.Lifetime.Seconds(), "超上界钳到上界")

	// idle 不允许超过 open，否则 database/sql 会静默把 idle 降到 open
	ReplaceDBPoolSetting(DBPoolSetting{
		MaxIdleConns:   9_000,
		MaxOpenConns:   50,
		MaxLifetimeSec: 60,
	})
	snapshot = GetDBPoolSnapshot()
	require.LessOrEqual(t, snapshot.MaxIdle, snapshot.MaxOpen)
	require.LessOrEqual(t, snapshot.LogMaxIdle, snapshot.LogMaxOpen)
}

func TestReplaceDBPoolSettingRepublishesSnapshot(t *testing.T) {
	original := GetDBPoolSetting()
	t.Cleanup(func() { ReplaceDBPoolSetting(original) })

	before := GetDBPoolSnapshot()
	beforeMaxOpen := before.MaxOpen

	ReplaceDBPoolSetting(DBPoolSetting{MaxIdleConns: 50, MaxOpenConns: 500, MaxLifetimeSec: 120})
	after := GetDBPoolSnapshot()

	require.NotSame(t, before, after, "必须发布新快照，而不是原地改旧快照")
	require.Equal(t, 500, after.MaxOpen)
	require.Equal(t, 50, after.MaxIdle)
	// 已被持有的旧快照必须保持不变——请求路径正是这样按值读快照的
	require.Equal(t, beforeMaxOpen, before.MaxOpen)
	require.Equal(t, 50, GetDBPoolSetting().MaxIdleConns)
}

func TestApplyDBPoolEnvDefaultsReadsEnvironment(t *testing.T) {
	original := GetDBPoolSetting()
	t.Cleanup(func() { ReplaceDBPoolSetting(original) })

	t.Setenv("SQL_MAX_IDLE_CONNS", "44")
	t.Setenv("SQL_MAX_OPEN_CONNS", "444")
	t.Setenv("SQL_MAX_LIFETIME", "444")
	t.Setenv("LOG_SQL_MAX_IDLE_CONNS", "22")
	t.Setenv("LOG_SQL_MAX_OPEN_CONNS", "222")

	ApplyDBPoolEnvDefaults()

	setting := GetDBPoolSetting()
	require.Equal(t, 44, setting.MaxIdleConns)
	require.Equal(t, 444, setting.MaxOpenConns)
	require.Equal(t, 444, setting.MaxLifetimeSec)
	require.Equal(t, 22, setting.LogMaxIdleConns)
	require.Equal(t, 222, setting.LogMaxOpenConns)

	// env 只是种子，必须同步反映到请求路径读的快照上
	snapshot := GetDBPoolSnapshot()
	require.Equal(t, 444, snapshot.MaxOpen)
	require.Equal(t, 222, snapshot.LogMaxOpen)
}

// 未设置日志库 env 时保持 0（表示继承主库），不能被误写成主库的具体数值。
func TestApplyDBPoolEnvDefaultsLeavesLogInheritanceUnset(t *testing.T) {
	original := GetDBPoolSetting()
	t.Cleanup(func() { ReplaceDBPoolSetting(original) })

	t.Setenv("SQL_MAX_IDLE_CONNS", "60")
	t.Setenv("SQL_MAX_OPEN_CONNS", "600")

	ApplyDBPoolEnvDefaults()

	setting := GetDBPoolSetting()
	require.Zero(t, setting.LogMaxIdleConns)
	require.Zero(t, setting.LogMaxOpenConns)
	snapshot := GetDBPoolSnapshot()
	require.Equal(t, 60, snapshot.LogMaxIdle, "继承主库 idle")
	require.Equal(t, 600, snapshot.LogMaxOpen, "继承主库 open")
}
