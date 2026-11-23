package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogExportRaw_SQLitePreservesStoredValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	previousDB, previousKind, previousGroup := LOG_DB, common.LogDatabaseType(), logGroupCol
	LOG_DB, logGroupCol = db, "`group`"
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		LOG_DB, logGroupCol = previousDB, previousGroup
		common.SetLogDatabaseType(previousKind)
		require.NoError(t, conn.Close())
	})
	require.NoError(t, db.AutoMigrate(&Log{}))
	other := ` {"stream_diagnostic":{"read_error":"private"}} `
	row := mkExportLog(t, func(l *Log) { l.Other, l.Group = other, "test-group" })
	require.NoError(t, db.Table("logs").Where("id = ?", row.Id).Updates(map[string]any{
		"content": nil, "quota": nil, "channel_name": "stored name",
	}).Error)
	batch, err := scanRawLogExportBatch(context.Background(), LogExportFilter{UserId: row.UserId, Group: row.Group}, row.CreatedAt, row.CreatedAt, nil, 100)
	require.NoError(t, err)
	require.Len(t, batch.rawLogs, 1)
	encoded, err := batch.rawLogs[0].json()
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, common.UnmarshalJsonStr(encoded, &record))
	require.Len(t, record, 21)
	require.Equal(t, other, record["other"])
	require.Equal(t, "stored name", record["channel_name"])
	require.Nil(t, record["content"])
	require.Nil(t, record["quota"])
	require.Equal(t, false, record["is_stream"])
	require.NotContains(t, batch.Logs[0].Other, "read_error")
}

func TestLogExportRaw_ClickHouseProjectionOmitsAbsentColumn(t *testing.T) {
	previous := common.LogDatabaseType()
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	t.Cleanup(func() { common.SetLogDatabaseType(previous) })
	fields := logExportRawFields()
	require.Len(t, fields, 20)
	require.NotContains(t, fields, "channel_name")
	require.Contains(t, fields, "request_id", "ClickHouse pagination requires the request ID")
	encoded, err := (&logExportRawLog{}).json()
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, common.UnmarshalJsonStr(encoded, &record))
	require.Len(t, record, 20)
	require.NotContains(t, record, "channel_name")
	require.Contains(t, record, "channel_id")
	require.Contains(t, record, "upstream_request_id")
	require.Contains(t, record, "other")
	require.Nil(t, record["other"])
}
