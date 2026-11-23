package auditsecurity

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestRiskExportBatchRejectsUnboundedRequest(t *testing.T) {
	for _, n := range []int{0, 101} {
		_, err := QueryRiskBatch(context.Background(), nil, nil, model.AuditLogFilter{}, 10, "high", 0, RiskPosition{}, n, n)
		require.ErrorIs(t, err, ErrRiskCursor)
	}
}
