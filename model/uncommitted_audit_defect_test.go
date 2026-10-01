//go:build audit_uncommitted

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Model the documented ID-reuse case, but both rows belong to two failed
// attempts of the same relay request in the same second. request_id is a
// request identity, not a unique log-event identity.
func TestUncommittedAuditLogIDReuseWithinRequest(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)
			stored := &Log{RequestId: prefix + "request", CreatedAt: 100, UserId: 91919,
				Type: LogTypeError, ChannelId: 10, Content: "attempt on channel 10"}
			require.NoError(t, db.Create(stored).Error)
			retried := &Log{Id: stored.Id, RequestId: stored.RequestId, CreatedAt: stored.CreatedAt, UserId: stored.UserId,
				Type: LogTypeError, ChannelId: 20, Content: "attempt on channel 20"}
			require.NoError(t, insertRelayLogs(db, []*Log{retried}, 10))
			var rows []Log
			require.NoError(t, db.Where("request_id = ?", stored.RequestId).Find(&rows).Error)
			assert.Len(t, rows, 2, "a different attempt must not be deduplicated only by request identity")
		})
	}
}
