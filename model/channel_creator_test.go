package model

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type creatorMigrationLog struct {
	gormlogger.Interface
	ddl []string
}

func (l *creatorMigrationLog) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "ALTER ") || strings.HasPrefix(upper, "DROP ") {
		l.ddl = append(l.ddl, sql)
	}
}

func newAdminTokenModelDB(t *testing.T) *gorm.DB {
	t.Helper()
	var dialector gorm.Dialector = sqlite.Open(":memory:")
	dsn := os.Getenv("TEST_ADMIN_TOKEN_DSN")
	if strings.HasPrefix(dsn, "postgres") {
		dialector = postgres.Open(dsn)
	} else if dsn != "" {
		dialector = mysql.Open(dsn)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	old := DB
	DB = db
	t.Cleanup(func() { DB = old; require.NoError(t, sqlDB.Close()) })
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	switch db.Dialector.Name() {
	case "postgres":
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	case "mysql":
		common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
	default:
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	}
	InitColumnNames()
	t.Cleanup(func() {
		common.SetDatabaseTypes(previousMain, previousLog)
		InitColumnNames()
	})
	var version string
	query := "SELECT VERSION()"
	if db.Dialector.Name() == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("database=%s version=%s", db.Dialector.Name(), version)
	return db
}

func TestChannelCreatorIsolationAndAtomicCreation(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	legacy := Channel{Name: "legacy-no-creator", Key: "legacy"}
	require.NoError(t, db.Create(&legacy).Error)
	t.Cleanup(func() { db.Delete(&legacy) })
	require.NoError(t, db.AutoMigrate(&ChannelCreator{}))
	require.ErrorIs(t, RequireChannelCreator(context.Background(), 42, []int{legacy.Id}), ErrChannelNotAccessible)
	migrationLog := &creatorMigrationLog{Interface: gormlogger.Default}
	require.NoError(t, db.Session(&gorm.Session{Logger: migrationLog}).AutoMigrate(&ChannelCreator{}))
	require.Empty(t, migrationLog.ddl, "second migration must not issue DDL")
	channels := []Channel{{Id: 123, Name: "owned", Key: "secret", Models: "m", Group: "default"}}
	require.NoError(t, BatchInsertChannels(channels, 42))
	t.Cleanup(func() {
		db.Where("channel_id = ?", channels[0].Id).Delete(&ChannelCreator{})
		db.Where("channel_id = ?", channels[0].Id).Delete(&Ability{})
		db.Delete(&channels[0])
	})
	require.NotEqual(t, 123, channels[0].Id, "caller-provided identifiers must be discarded")
	require.NoError(t, RequireChannelCreator(context.Background(), 42, []int{channels[0].Id}))
	require.ErrorIs(t, RequireChannelCreator(context.Background(), 43, []int{channels[0].Id}), ErrChannelNotAccessible)
	require.ErrorIs(t, RequireChannelCreator(context.Background(), 42, []int{channels[0].Id, 999}), ErrChannelNotAccessible)
	var ids []int
	require.NoError(t, ChannelsCreatedBy(db, 43).Pluck("id", &ids).Error)
	require.Empty(t, ids)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:creator_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "channel_creators" {
			tx.AddError(errors.New("injected creator storage failure"))
		}
	}))
	t.Cleanup(func() { db.Callback().Create().Remove("test:creator_failure") })
	require.Error(t, BatchInsertChannels([]Channel{{Name: "rollback", Key: "secret", Models: "m", Group: "default"}}, 42))
	var count int64
	require.NoError(t, db.Model(&Channel{}).Where("name = ?", "rollback").Count(&count).Error)
	require.Zero(t, count)
}

func TestCreatedChannelBatchRejectsMixedOwnersAndFreezesTagIDs(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &ChannelCreator{}, &ChannelCostConfig{}, &UserChannelExclusionCleanup{}, &PlatformChannelDailyStat{}, &ChannelLimitPeriodUsage{}, &ChannelDailyUsage{}))
	tag := "shared-tag"
	own := []Channel{{Name: "own", Key: "secret", Models: "a", Group: "default", Tag: &tag}}
	other := []Channel{{Name: "other", Key: "other-secret", Models: "b", Group: "default", Tag: &tag}}
	require.NoError(t, BatchInsertChannels(own, 41))
	require.NoError(t, BatchInsertChannels(other, 42))
	foreign := Channel{Id: other[0].Id, Name: "must-not-change"}
	require.Error(t, foreign.Update(41))
	require.ErrorIs(t, foreign.Delete(41), ErrChannelNotAccessible)
	_, err := BatchDeleteChannels([]int{own[0].Id, other[0].Id}, 41)
	require.ErrorIs(t, err, ErrChannelNotAccessible)
	ids := []int{own[0].Id, other[0].Id}
	t.Cleanup(func() {
		db.Where("channel_id IN ?", ids).Delete(&ChannelCreator{})
		db.Where("channel_id IN ?", ids).Delete(&Ability{})
		db.Where("id IN ?", ids).Delete(&Channel{})
	})
	require.ErrorIs(t, UpdateCreatedChannels(context.Background(), 41, ids, map[string]any{"tag": "changed"}, DailyLimitEdit{}), ErrChannelNotAccessible)
	var stored Channel
	require.NoError(t, db.First(&stored, own[0].Id).Error)
	require.Equal(t, tag, stored.GetTag())
	require.NoError(t, UpdateCreatedChannels(context.Background(), 41, []int{own[0].Id}, map[string]any{"tag": "changed", "models": "new"}, DailyLimitEdit{}))
	stored = Channel{}
	require.NoError(t, db.First(&stored, other[0].Id).Error)
	require.Equal(t, tag, stored.GetTag())
	require.Equal(t, "b", stored.Models)
	var abilities []Ability
	require.NoError(t, db.Where("channel_id = ?", own[0].Id).Find(&abilities).Error)
	require.Len(t, abilities, 1)
	require.Equal(t, "new", abilities[0].Model)
	require.NoError(t, db.Model(&Channel{}).Where("id = ?", own[0].Id).Updates(map[string]any{"status": common.ChannelStatusManuallyDisabled, "daily_limit_recover_minutes": 10, "daily_limit_period_start": 1}).Error)
	require.NoError(t, UpdateCreatedChannels(context.Background(), 41, []int{own[0].Id}, map[string]any{"status": common.ChannelStatusEnabled}, DailyLimitEdit{}))
	stored = Channel{}
	require.NoError(t, db.First(&stored, own[0].Id).Error)
	periodStart := stored.DailyLimitPeriodStart
	require.Greater(t, periodStart, int64(1))
	require.NoError(t, UpdateCreatedChannels(context.Background(), 41, []int{own[0].Id}, map[string]any{"status": common.ChannelStatusEnabled}, DailyLimitEdit{}))
	require.NoError(t, db.First(&stored, own[0].Id).Error)
	require.Equal(t, periodStart, stored.DailyLimitPeriodStart, "enabling an already enabled channel must not reset its allowance")
	injected := errors.New("delete rollback")
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := deleteChannelCostConfigsTx(tx, []int{own[0].Id}); err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	require.NoError(t, RequireChannelCreator(context.Background(), 41, []int{own[0].Id}), "rollback keeps the creator relation")
	var plan []map[string]any
	sql := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return ChannelsCreatedBy(tx, 41).Select("id").Order("id").Limit(20).Find(&[]Channel{})
	})
	prefix := "EXPLAIN "
	if db.Dialector.Name() == "sqlite" {
		prefix = "EXPLAIN QUERY PLAN "
	}
	require.NoError(t, db.Raw(prefix+sql).Scan(&plan).Error)
	t.Logf("creator query plan: %v", plan)

	deleted, err := BatchDeleteChannels([]int{own[0].Id, own[0].Id}, 41)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	var creatorCount int64
	require.NoError(t, db.Model(&ChannelCreator{}).Where("channel_id = ?", own[0].Id).Count(&creatorCount).Error)
	require.Zero(t, creatorCount)
}

func TestCreatedChannelAuditReportsOnlyCommittedConfiguration(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &ChannelCreator{}, &ChannelCostConfig{}))
	channels := []Channel{{Name: "same", Key: "secret", Models: "fixture", Group: "default", Status: common.ChannelStatusEnabled}, {Name: "before", Key: "secret", Models: "fixture", Group: "default", Status: common.ChannelStatusEnabled}}
	require.NoError(t, BatchInsertChannels(channels, 947331))
	ids := []int{channels[0].Id, channels[1].Id}
	t.Cleanup(func() {
		db.Where("channel_id IN ?", ids).Delete(&ChannelCreator{})
		db.Where("channel_id IN ?", ids).Delete(&Ability{})
		db.Where("channel_id IN ?", ids).Delete(&ChannelCostConfig{})
		db.Where("id IN ?", ids).Delete(&Channel{})
	})
	changes, err := UpdateCreatedChannelsWithChanges(context.Background(), 947331, ids, map[string]any{"name": "same"}, DailyLimitEdit{})
	require.NoError(t, err)
	require.Equal(t, []int{ids[1]}, changes.IDs)
	require.Equal(t, []string{"name"}, changes.Fields)
	changes, err = UpdateCreatedChannelsWithChanges(context.Background(), 947331, ids, map[string]any{"name": "same"}, DailyLimitEdit{})
	require.NoError(t, err)
	require.Empty(t, changes.IDs)
	require.Empty(t, changes.Fields)
	minutes := 15
	changes, err = UpdateCreatedChannelsWithChanges(context.Background(), 947331, ids, nil, DailyLimitEdit{RecoverMinutes: &minutes})
	require.NoError(t, err)
	require.ElementsMatch(t, ids, changes.IDs)
	require.Equal(t, []string{"daily_limit"}, changes.Fields)
	changes, err = UpdateCreatedChannelsWithChanges(context.Background(), 947331, ids, nil, DailyLimitEdit{RecoverMinutes: &minutes})
	require.NoError(t, err)
	require.Empty(t, changes.IDs)
	for _, invalidIDs := range [][]int{nil, {ids[0], ids[0]}, {ids[0], -1}, make([]int, 101)} {
		changes, err = UpdateCreatedChannelsWithChanges(context.Background(), 947331, invalidIDs, map[string]any{"status": common.ChannelStatusManuallyDisabled}, DailyLimitEdit{})
		require.Error(t, err)
		require.Empty(t, changes.IDs)
	}
	negative := int64(-1)
	changes, err = UpdateCreatedChannelsWithChanges(context.Background(), 947331, ids, nil, DailyLimitEdit{QuotaLimit: &negative})
	require.Error(t, err)
	require.Empty(t, changes.IDs)
	// A later statement failure rolls back both the update and its audit result.
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:audit-abilities-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "abilities" {
			tx.AddError(errors.New("fixture ability failure"))
		}
	}))
	t.Cleanup(func() { db.Callback().Create().Remove("test:audit-abilities-failure") })
	changes, err = UpdateCreatedChannelsWithChanges(context.Background(), 947331, ids, map[string]any{"models": "changed"}, DailyLimitEdit{})
	require.Error(t, err)
	require.Empty(t, changes.IDs)
	require.Empty(t, changes.Fields)
	var saved Channel
	require.NoError(t, db.First(&saved, ids[0]).Error)
	require.Equal(t, "fixture", saved.Models)
}
