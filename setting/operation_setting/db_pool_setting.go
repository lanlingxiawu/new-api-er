package operation_setting

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type DBPoolSetting struct {
	MaxIdleConns    int `json:"max_idle_conns"`
	MaxOpenConns    int `json:"max_open_conns"`
	MaxLifetimeSec  int `json:"max_lifetime_sec"`
	LogMaxIdleConns int `json:"log_max_idle_conns"`
	LogMaxOpenConns int `json:"log_max_open_conns"`
}
type DBPoolSnapshot struct {
	MaxIdle, MaxOpen, LogMaxIdle, LogMaxOpen int
	Lifetime                                 time.Duration
}

const dbPoolConfigName = "db_pool_setting"

var dbPoolSetting = DBPoolSetting{MaxIdleConns: 100, MaxOpenConns: 1000, MaxLifetimeSec: 60}
var dbPoolSnapshot atomic.Pointer[DBPoolSnapshot]

func init() { config.GlobalConfig.Register(dbPoolConfigName, &dbPoolSetting); PublishDBPoolSetting() }
func PublishDBPoolSetting() {
	s := GetDBPoolSetting()
	idle := clamp(s.MaxIdleConns, 100, 10_000)
	open := clamp(s.MaxOpenConns, 1000, 100_000)
	if idle > open {
		idle = open
	}
	li := s.LogMaxIdleConns
	if li == 0 {
		li = idle
	} else {
		li = clamp(li, idle, 10_000)
	}
	lo := s.LogMaxOpenConns
	if lo == 0 {
		lo = open
	} else {
		lo = clamp(lo, open, 100_000)
	}
	if li > lo {
		li = lo
	}
	snap := DBPoolSnapshot{idle, open, li, lo, time.Duration(clamp(s.MaxLifetimeSec, 60, 86_400)) * time.Second}
	dbPoolSnapshot.Store(&snap)
}
func GetDBPoolSnapshot() *DBPoolSnapshot { return dbPoolSnapshot.Load() }

// See the note on GetRateLimitSetting: these fields are also read and written by
// reflection from the config load/save paths, so they share the draft mutex.
func GetDBPoolSetting() DBPoolSetting {
	var out DBPoolSetting
	config.WithConfigDraft(func() { out = dbPoolSetting })
	return out
}
func ReplaceDBPoolSetting(s DBPoolSetting) {
	config.WithConfigDraft(func() { dbPoolSetting = s })
	PublishDBPoolSetting()
}
func ValidateDBPoolSetting(s DBPoolSetting) error {
	if s.MaxIdleConns < 1 || s.MaxIdleConns > 10_000 || s.MaxOpenConns < 1 || s.MaxOpenConns > 100_000 || s.MaxLifetimeSec < 1 || s.MaxLifetimeSec > 86_400 {
		return fmt.Errorf("database pool value out of range")
	}
	if s.MaxIdleConns > s.MaxOpenConns {
		return fmt.Errorf("max idle connections cannot exceed max open connections")
	}
	li, lo := s.LogMaxIdleConns, s.LogMaxOpenConns
	if li < 0 || li > 10_000 || lo < 0 || lo > 100_000 {
		return fmt.Errorf("log database pool value out of range")
	}
	if li == 0 {
		li = s.MaxIdleConns
	}
	if lo == 0 {
		lo = s.MaxOpenConns
	}
	if li > lo {
		return fmt.Errorf("log max idle connections cannot exceed max open connections")
	}
	return nil
}
func ApplyDBPoolEnvDefaults() {
	s := &dbPoolSetting
	s.MaxIdleConns = common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", s.MaxIdleConns)
	s.MaxOpenConns = common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", s.MaxOpenConns)
	s.MaxLifetimeSec = common.GetEnvOrDefault("SQL_MAX_LIFETIME", s.MaxLifetimeSec)
	s.LogMaxIdleConns = common.GetEnvOrDefault("LOG_SQL_MAX_IDLE_CONNS", 0)
	s.LogMaxOpenConns = common.GetEnvOrDefault("LOG_SQL_MAX_OPEN_CONNS", 0)
	PublishDBPoolSetting()
}
