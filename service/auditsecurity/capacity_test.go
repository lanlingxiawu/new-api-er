package auditsecurity

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestArchivedUnknownClosureCountsAsRetentionRisk(t *testing.T) {
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.SecurityAlert{Status: "resolved", ArchivedAt: &now}).Error)
	capacity, err := GetSecurityCapacity(context.Background(), db, 100)
	require.NoError(t, err)
	require.Equal(t, 1, capacity.Archived)
	require.Equal(t, 1, capacity.RetentionUnknown)
}
