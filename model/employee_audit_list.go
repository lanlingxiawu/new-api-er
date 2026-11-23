package model

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const EmployeeAuditCustomerBatchSize = 200

var ErrCustomerAuditScopeChanged = errors.New("customer audit scope changed")

type EmployeeAuditFilter struct {
	EmployeeID int
	Start      int64
	End        int64
	RequestID  string
	IP         string
	Cursor     string
	View       string `json:",omitempty"`
}

// DecodeCursor keeps aggregate cursors separate from the single-customer API.
func (f EmployeeAuditFilter) DecodeCursor(kind string) (CustomerAuditPosition, error) {
	var position CustomerAuditPosition
	if f.View != "" && (kind != "alerts" || f.View != "handled") {
		return position, ErrCustomerAuditParams
	}
	if f.EmployeeID <= 0 || f.Start <= 0 || f.End < f.Start || f.End-f.Start > 31*86400 || len(f.RequestID) > 64 || len(f.IP) > 64 || len(f.Cursor) > 2048 || kind != "audit" && kind != "alerts" {
		return position, ErrCustomerAuditParams
	}
	bound := f
	bound.Cursor = ""
	raw, err := common.Marshal(bound)
	if err != nil {
		return position, err
	}
	binding := fmt.Sprintf("employee-customers:%d:%s:%x", AuditActivityProjectionVersion, kind, sha256.Sum256(raw))
	position.Binding, position.Start, position.End = binding, f.Start, f.End
	if f.Cursor == "" {
		return position, nil
	}
	raw, err = base64.RawURLEncoding.DecodeString(f.Cursor)
	if err != nil || common.Unmarshal(raw, &position) != nil || position.Binding != binding || position.Start != f.Start || position.End != f.End || position.At < f.Start || position.At > f.End || position.ID < 0 || len(position.EventID) > 64 || kind == "audit" && position.EventID == "" || kind == "alerts" && position.ID == 0 {
		return CustomerAuditPosition{}, ErrCustomerAuditParams
	}
	return position, nil
}

func employeeAuditCustomers(db *gorm.DB, employeeID int) *gorm.DB {
	return db.Model(&User{}).
		Where("inviter_id = ? AND role = ? AND id <> ?", employeeID, common.RoleCommonUser, employeeID).
		Where("NOT EXISTS (?)", db.Model(&EmployeeProfile{}).Select("1").Where("user_id = users.id AND status = ?", 1))
}

// EmployeeAuditCustomerIDs returns one bounded ownership batch from the audit
// read pool. The caller must first check RequireEmployeeAuditAccess.
func EmployeeAuditCustomerIDs(ctx context.Context, db *gorm.DB, employeeID, after int) ([]int, error) {
	if employeeID <= 0 || after < 0 {
		return nil, ErrCustomerAuditParams
	}
	var ids []int
	err := employeeAuditCustomers(db.WithContext(ctx), employeeID).Where("id > ?", after).
		Order("id ASC").Limit(EmployeeAuditCustomerBatchSize).Pluck("id", &ids).Error
	return ids, err
}

type CustomerAuditIdentity struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// EmployeeAuditCustomerIdentities rechecks the final page before exposing it.
// A transfer during a multi-batch read must not produce a partial successful page.
func EmployeeAuditCustomerIdentities(ctx context.Context, db *gorm.DB, employeeID int, ids []int) (map[int]CustomerAuditIdentity, error) {
	identities := make(map[int]CustomerAuditIdentity)
	if len(ids) == 0 {
		return identities, nil
	}
	if len(ids) > CustomerAuditPageSize+1 {
		return nil, ErrCustomerAuditParams
	}
	if err := RequireEmployeeAuditAccess(ctx, db, employeeID); err != nil {
		return nil, err
	}
	var rows []CustomerAuditIdentity
	if err := employeeAuditCustomers(db.WithContext(ctx), employeeID).Select("id,username,display_name").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		identities[row.ID] = row
	}
	for _, id := range ids {
		if _, ok := identities[id]; !ok {
			return nil, ErrCustomerAuditScopeChanged
		}
	}
	return identities, nil
}

type EmployeeCustomerAuditActivity struct {
	AuditActivity
	Customer CustomerAuditIdentity `json:"customer"`
}

type EmployeeAuditPage struct {
	Items      []EmployeeCustomerAuditActivity `json:"items"`
	NextCursor string                          `json:"next_cursor"`
}

func QueryEmployeeAuditLogs(ctx context.Context, mainDB, logsDB *gorm.DB, f EmployeeAuditFilter) (EmployeeAuditPage, error) {
	page := EmployeeAuditPage{Items: []EmployeeCustomerAuditActivity{}}
	if err := RequireEmployeeAuditAccess(ctx, mainDB, f.EmployeeID); err != nil {
		return page, err
	}
	position, err := f.DecodeCursor("audit")
	if err != nil {
		return page, err
	}
	filter := AuditLogFilter{SelfView: true, StartTimestamp: f.Start, EndTimestamp: f.End, RequestId: f.RequestID, IP: f.IP}
	var candidates []*AuditLog
	for after := 0; ; {
		ids, err := EmployeeAuditCustomerIDs(ctx, mainDB, f.EmployeeID, after)
		if err != nil {
			return page, err
		}
		if len(ids) == 0 {
			break
		}
		rows, err := queryCustomerAuditRecords(ctx, logsDB, filter, ids, position)
		if err != nil {
			return page, err
		}
		if len(candidates) == 0 {
			candidates = rows
		} else if len(rows) > 0 {
			// Let the source database order mixed-case event IDs. A Go string
			// comparison can disagree with its collation and lose cursor rows.
			byEvent := make(map[string]*AuditLog, len(candidates)+len(rows))
			eventIDs := make([]string, 0, len(candidates)+len(rows))
			for _, row := range append(candidates, rows...) {
				byEvent[row.EventId] = row
				eventIDs = append(eventIDs, row.EventId)
			}
			var ordered []string
			if err := logsDB.WithContext(ctx).Model(&AuditLog{}).
				Where("event_id IN ? AND created_at >= ? AND created_at <= ?", eventIDs, f.Start, f.End).
				Order("created_at DESC,event_id DESC").Limit(CustomerAuditPageSize+1).Pluck("event_id", &ordered).Error; err != nil {
				return page, err
			}
			candidates = make([]*AuditLog, 0, len(ordered))
			for _, eventID := range ordered {
				candidates = append(candidates, byEvent[eventID])
			}
		}
		if len(ids) < EmployeeAuditCustomerBatchSize {
			break
		}
		after = ids[len(ids)-1]
	}
	ids := make([]int, len(candidates))
	for i, row := range candidates {
		ids[i] = row.UserId
	}
	identities, err := EmployeeAuditCustomerIdentities(ctx, mainDB, f.EmployeeID, ids)
	if err != nil {
		return page, err
	}
	if len(candidates) > CustomerAuditPageSize {
		candidates = candidates[:CustomerAuditPageSize]
		last := candidates[len(candidates)-1]
		position.At, position.EventID = last.CreatedAt, last.EventId
		page.NextCursor, err = position.Encode()
		if err != nil {
			return page, err
		}
	}
	ProjectAuditLogs(candidates, true, common.RoleCommonUser)
	for _, row := range candidates {
		page.Items = append(page.Items, EmployeeCustomerAuditActivity{AuditActivity: ProjectAuditActivity(row), Customer: identities[row.UserId]})
	}
	return page, nil
}
