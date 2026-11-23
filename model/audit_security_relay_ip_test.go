package model

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSecurityRelayIPOnlyAfterSuccessfulPersistence(t *testing.T) {
	previous := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": "true", "ip_intelligence_enabled": "true", "relay_ip_enabled": "true", "suspended": "false"}))
	for len(securityRelayIPQueue) > 0 {
		<-securityRelayIPQueue
	}
	t.Cleanup(func() {
		_ = config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": strconv.FormatBool(previous.Enabled), "ip_intelligence_enabled": strconv.FormatBool(previous.IPIntelligenceEnabled), "relay_ip_enabled": strconv.FormatBool(previous.RelayIPEnabled), "suspended": strconv.FormatBool(previous.Suspended)})
		for len(securityRelayIPQueue) > 0 {
			<-securityRelayIPQueue
		}
	})
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:relay-ip-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	previousLogDB := LOG_DB
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = previousLogDB })
	now := time.Now().Unix()
	entry := &Log{UserId: 42, Type: LogTypeConsume, Ip: "8.8.8.8", CreatedAt: now}
	require.Error(t, createLog(entry), "missing table must fail before observing the IP")
	require.Zero(t, SecurityRelayIPQueued())
	require.Error(t, insertRelayLogs(db, []*Log{entry}, 10))
	require.Zero(t, SecurityRelayIPQueued())
	require.NoError(t, db.AutoMigrate(&Log{}))
	require.NoError(t, createLog(entry))
	observed, ok := TakeSecurityRelayIP()
	require.True(t, ok)
	require.Equal(t, 42, observed.UserID)
	require.Equal(t, entry.Ip, observed.IP)
	batch := []*Log{
		{UserId: 43, Type: LogTypeError, Ip: "1.1.1.1", CreatedAt: now},
		{UserId: 44, Type: LogTypeManage, Ip: "8.8.4.4", CreatedAt: now},
	}
	require.NoError(t, insertRelayLogs(db, batch, 10))
	observed, ok = TakeSecurityRelayIP()
	require.True(t, ok)
	require.Equal(t, 43, observed.UserID)
	require.Zero(t, SecurityRelayIPQueued(), "management logs are not AI observations")

	requestLogTestStore(t)
	w := segmentTestWriter(t)
	w.Record([]*RequestLog{{UserId: 45, Ip: "9.9.9.9", CreatedAt: now, RequestId: "observation-success"}})
	observed, ok = TakeSecurityRelayIP()
	require.True(t, ok)
	require.Equal(t, 45, observed.UserID)
	require.NoError(t, w.file.Close())
	w.Record([]*RequestLog{{UserId: 46, Ip: "8.8.4.4", CreatedAt: now, RequestId: "observation-write-failure"}})
	require.Zero(t, SecurityRelayIPQueued(), "failed file writes must not create observations")
	w.Record([]*RequestLog{{UserId: 47, Ip: "8.8.4.4", CreatedAt: now, RequestId: "observation-recovered"}})
	observed, ok = TakeSecurityRelayIP()
	require.True(t, ok)
	require.Equal(t, 47, observed.UserID)
}
