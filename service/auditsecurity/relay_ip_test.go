package auditsecurity

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestRelayIPObservationIsolationAndDeduplication(t *testing.T) {
	db, _ := customerAccessFixture(t)
	w := &worker{db: db, writer: db}
	now := time.Now().Unix()
	input := model.SecurityRelayIP{UserID: 3, IP: "8.8.8.8", At: now, QueuedAt: now}
	for range 2 {
		_, err := w.observeRelayIP(t.Context(), input)
		require.NoError(t, err)
	}
	var rows []model.AuditLog
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	e := Normalize(rows[0])
	require.Equal(t, "relay:3", e.SubjectRef)
	require.Equal(t, 3, e.ActorID)
	require.Equal(t, "relay.ip_observed", e.Action)
	require.True(t, ipIntelligenceCandidate(e))
	require.Empty(t, e.SessionRef)
	require.Empty(t, e.TokenRef)
	require.Empty(t, e.RequestID)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "relay-ip-test")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.Enabled, cfg.IPIntelligenceEnabled, cfg.RelayIPEnabled = true, true, true
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{e}, state, cfg))
	var count int64
	require.NoError(t, db.Model(&model.SecurityBaseline{}).Count(&count).Error)
	require.Zero(t, count, "AI clients must not become login baselines")
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("outcome <> ?", "").Count(&count).Error)
	require.Zero(t, count, "IP observations cannot authorize account changes")
	input.UserID = 4
	_, err = w.observeRelayIP(t.Context(), input)
	require.NoError(t, err)
	input.At += 600
	_, err = w.observeRelayIP(t.Context(), input)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AuditLog{}).Count(&count).Error)
	require.EqualValues(t, 3, count)
	for _, ip := range []string{"", "127.0.0.1", "10.0.0.1", "::1", "not-an-ip"} {
		input.IP = ip
		_, err = w.observeRelayIP(t.Context(), input)
		require.NoError(t, err)
	}
	input.IP, input.UserID = "8.8.4.4", 999999
	_, err = w.observeRelayIP(t.Context(), input)
	require.NoError(t, err)
	input.UserID, input.QueuedAt = 3, now-121
	_, err = w.observeRelayIP(t.Context(), input)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.AuditLog{}).Count(&count).Error)
	require.EqualValues(t, 3, count)
}

func TestRelayIPCaptureIsBoundedAndFailsClosed(t *testing.T) {
	previous := operation_setting.GetAuditSecuritySetting()
	t.Cleanup(func() {
		_ = config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": fmt.Sprint(previous.Enabled), "ip_intelligence_enabled": fmt.Sprint(previous.IPIntelligenceEnabled), "relay_ip_enabled": fmt.Sprint(previous.RelayIPEnabled), "suspended": fmt.Sprint(previous.Suspended)})
		for {
			if _, ok := model.TakeSecurityRelayIP(); !ok {
				break
			}
		}
	})
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": "true", "ip_intelligence_enabled": "true", "relay_ip_enabled": "true", "suspended": "false"}))
	for {
		if _, ok := model.TakeSecurityRelayIP(); !ok {
			break
		}
	}
	now := time.Now().Unix()
	for _, change := range []map[string]string{{"enabled": "false"}, {"enabled": "true", "ip_intelligence_enabled": "false"}, {"ip_intelligence_enabled": "true", "relay_ip_enabled": "false"}, {"relay_ip_enabled": "true", "suspended": "true"}} {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, change))
		model.QueueSecurityRelayIP(3, "8.8.8.8", now)
		require.Zero(t, model.SecurityRelayIPQueued())
	}
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"suspended": "false"}))
	missing := model.SecurityRelayIPMissing.Load()
	model.QueueSecurityRelayIP(3, "", now)
	require.Equal(t, missing+1, model.SecurityRelayIPMissing.Load())
	model.QueueSecurityRelayIP(3, "8.8.8.8", now-121)
	require.Zero(t, model.SecurityRelayIPQueued())
	model.QueueSecurityRelayIP(0, "8.8.8.8", now)
	require.Zero(t, model.SecurityRelayIPQueued())
	dropped := model.SecurityRelayIPDropped.Load()
	for range 1100 {
		model.QueueSecurityRelayIP(3, "8.8.8.8", now)
	}
	require.Equal(t, 1024, model.SecurityRelayIPQueued())
	require.Equal(t, dropped+76, model.SecurityRelayIPDropped.Load())
	w := worker{}
	cfg := operation_setting.GetAuditSecuritySetting()
	cfg.Suspended = true
	for range 6 {
		require.NoError(t, w.captureRelayIPs(t.Context(), cfg))
	}
	require.Zero(t, model.SecurityRelayIPQueued())
}
