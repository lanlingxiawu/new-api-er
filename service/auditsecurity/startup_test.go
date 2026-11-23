package auditsecurity

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/logger"
)

func TestWorkerInitializationDoesNotMigrate(t *testing.T) {
	db := newWorkerTestDB(t)
	// A missing index would be recreated by AutoMigrate. Worker initialization
	// must leave schema maintenance to the master startup migration.
	require.NoError(t, db.Migrator().DropIndex(&model.SecurityEvent{}, "idx_sec_ip_pending"))
	recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	db.Logger = recorder
	previous := primary.Swap(nil)
	t.Cleanup(func() { primary.Store(previous) })
	w := worker{db: db, source: db, writer: db}
	require.NoError(t, w.initialize(t.Context()))
	require.Empty(t, recorder.statements)
	require.False(t, db.Migrator().HasIndex(&model.SecurityEvent{}, "idx_sec_ip_pending"))
}
