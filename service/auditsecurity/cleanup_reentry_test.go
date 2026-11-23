package auditsecurity

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeletingAlertSurvivesRetentionChangeAcrossCycles(t *testing.T) {
	db := newWorkerTestDB(t)
	ctx := context.Background()
	now := time.Now().Unix()
	closedAt := now - 1000
	alert := model.SecurityAlert{Status: "resolved", ClosedAt: &closedAt, LastSeen: closedAt}
	require.NoError(t, db.Create(&alert).Error)
	delivery := model.SecurityDelivery{AlertID: alert.ID, Status: "sending", UpdatedAt: now}
	require.NoError(t, db.Create(&delivery).Error)
	_, err := model.CleanupAuditSecurityAlerts(ctx, db, now-100)
	require.NoError(t, err, "mark deleting before allowing delivery completion")
	_, err = model.CleanupAuditSecurityAlerts(ctx, db, now-100)
	require.NoError(t, err, "sending delivery is skipped while the cursor advances")
	drained, err := model.CleanupAuditSecurityAlerts(ctx, db, now-100)
	require.NoError(t, err)
	require.True(t, drained)
	require.NoError(t, db.Model(&delivery).Updates(map[string]any{"status": "sent", "sent_at": now}).Error)
	for range 5 {
		_, err = model.CleanupAuditSecurityAlerts(ctx, db, now-2000)
		require.NoError(t, err)
	}
	require.ErrorIs(t, db.First(&model.SecurityAlert{}, alert.ID).Error, gorm.ErrRecordNotFound, "persistent deleting eligibility must not be blocked by a longer retention period")
}

func TestZeroActionTimeDoesNotEstablishClosure(t *testing.T) {
	db := newWorkerTestDB(t)
	alert := model.SecurityAlert{Status: "resolved"}
	require.NoError(t, db.Create(&alert).Error)
	action := model.SecurityAction{AlertID: alert.ID, Action: "resolved", CreatedAt: 0}
	require.NoError(t, db.Create(&action).Error)
	require.NoError(t, db.Model(&action).UpdateColumn("created_at", 0).Error)
	for range 3 {
		_, err := model.CleanupAuditSecurityAlerts(context.Background(), db, time.Now().Unix()-100)
		require.NoError(t, err)
	}
	var stored model.SecurityAlert
	require.NoError(t, db.First(&stored, alert.ID).Error)
	if stored.ClosedAt != nil {
		require.LessOrEqual(t, *stored.ClosedAt, int64(0))
	}
	require.Empty(t, stored.PurgeState)
}

func TestCleanupDoesNotBackfillLegacyClosure(t *testing.T) {
	db := newWorkerTestDB(t)
	zero := int64(0)
	alerts := []model.SecurityAlert{{Status: "resolved"}, {Status: "false_positive", ClosedAt: &zero}}
	require.NoError(t, db.Create(&alerts).Error)
	for _, alert := range alerts {
		require.NoError(t, db.Create(&model.SecurityAction{AlertID: alert.ID, Action: alert.Status, CreatedAt: time.Now().Unix() - 1000}).Error)
	}
	for range 3 {
		_, err := model.CleanupAuditSecurityAlerts(context.Background(), db, time.Now().Unix()-100)
		require.NoError(t, err)
	}
	for _, alert := range alerts {
		var stored model.SecurityAlert
		require.NoError(t, db.First(&stored, alert.ID).Error)
		require.Equal(t, alert, stored, "valid old actions must not backfill missing closure or trigger deletion")
	}
}
