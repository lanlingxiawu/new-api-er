package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"gorm.io/gorm"
)

// ObserveChannels performs at most one bounded management-only read. It never
// adds locks to the relay's channel rows, and failure cannot reject a mutation.
func (audit *AuditChanges) ObserveChannels(query *gorm.DB) []Channel {
	if audit == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var rows []Channel
	columns := make([]string, 0, len(ChannelAuditColumns)-1)
	for _, column := range ChannelAuditColumns {
		if column != "key" {
			columns = append(columns, column)
		}
	}
	if err := query.WithContext(ctx).Select(columns).Order("id").Limit(101).Find(&rows).Error; err != nil {
		audit.Outcome = "unavailable"
		logger.LogError(ctx, "channel audit snapshot unavailable")
		return nil
	}
	if len(rows) > 100 {
		rows = rows[:100]
		audit.Truncated = true
	}
	return rows
}

// ChannelWrites receives the normalized values sent to UPDATE, never a later
// read that might belong to another writer. Nil updates denotes deletion.
func (audit *AuditChanges) ChannelWrites(rows []Channel, updates AuditFields) {
	if audit == nil {
		return
	}
	for i := range rows {
		row := &rows[i]
		before := ChannelAuditSnapshot(row)
		delete(before, "key") // omitted by ObserveChannels; do not invent a value
		if updates != nil {
			selected := AuditFields{}
			for key := range updates {
				selected[key] = before[key]
			}
			before = selected
		}
		audit.Add(AuditTarget("channel", row.Id, row.Name), before, updates)
	}
}

func optionalAudit(audit []*AuditChanges) *AuditChanges {
	if len(audit) > 0 {
		return audit[0]
	}
	return nil
}
