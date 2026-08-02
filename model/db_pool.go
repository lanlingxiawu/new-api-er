package model

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

type DBPoolStats struct {
	MaxOpenConnections int   `json:"max_open_connections"`
	OpenConnections    int   `json:"open_connections"`
	InUse              int   `json:"in_use"`
	Idle               int   `json:"idle"`
	WaitCount          int64 `json:"wait_count"`
	WaitDurationMs     int64 `json:"wait_duration_ms"`
	MaxIdleClosed      int64 `json:"max_idle_closed"`
	MaxIdleTimeClosed  int64 `json:"max_idle_time_closed"`
	MaxLifetimeClosed  int64 `json:"max_lifetime_closed"`
}

type DBPoolRuntimeStats struct {
	SampledAt     int64        `json:"sampled_at"`
	Main          DBPoolStats  `json:"main"`
	Log           *DBPoolStats `json:"log"`
	LogReusesMain bool         `json:"log_reuses_main"`
}

func GetDBPoolRuntimeStats() (DBPoolRuntimeStats, error) {
	mainStats, err := getPoolStats(DB)
	if err != nil {
		return DBPoolRuntimeStats{}, fmt.Errorf("get main database pool stats: %w", err)
	}

	stats := DBPoolRuntimeStats{
		SampledAt:     time.Now().UnixMilli(),
		Main:          mainStats,
		LogReusesMain: LOG_DB == nil || LOG_DB == DB,
	}
	if stats.LogReusesMain {
		return stats, nil
	}

	logStats, err := getPoolStats(LOG_DB)
	if err != nil {
		return DBPoolRuntimeStats{}, fmt.Errorf("get log database pool stats: %w", err)
	}
	stats.Log = &logStats
	return stats, nil
}

func getPoolStats(db *gorm.DB) (DBPoolStats, error) {
	if db == nil {
		return DBPoolStats{}, errors.New("database is not initialized")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return DBPoolStats{}, err
	}
	return mapDBPoolStats(sqlDB.Stats()), nil
}

func mapDBPoolStats(stats sql.DBStats) DBPoolStats {
	return DBPoolStats{
		MaxOpenConnections: stats.MaxOpenConnections,
		OpenConnections:    stats.OpenConnections,
		InUse:              stats.InUse,
		Idle:               stats.Idle,
		WaitCount:          stats.WaitCount,
		WaitDurationMs:     stats.WaitDuration.Milliseconds(),
		MaxIdleClosed:      stats.MaxIdleClosed,
		MaxIdleTimeClosed:  stats.MaxIdleTimeClosed,
		MaxLifetimeClosed:  stats.MaxLifetimeClosed,
	}
}

func ApplyDBPoolSetting() error {
	s := operation_setting.GetDBPoolSnapshot()
	var errs []error
	if err := applyPool(DB, s.MaxIdle, s.MaxOpen, s.Lifetime); err != nil {
		errs = append(errs, fmt.Errorf("main database: %w", err))
	}
	if LOG_DB != nil && LOG_DB != DB {
		if err := applyPool(LOG_DB, s.LogMaxIdle, s.LogMaxOpen, s.Lifetime); err != nil {
			errs = append(errs, fmt.Errorf("log database: %w", err))
		}
	}
	return errors.Join(errs...)
}

func applyPool(db *gorm.DB, idle, open int, lifetime time.Duration) (err error) {
	if db == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic while applying database pool setting: %v", recovered)
		}
	}()
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxIdleConns(idle)
	sqlDB.SetMaxOpenConns(open)
	sqlDB.SetConnMaxLifetime(lifetime)
	return nil
}
