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

var ErrCustomerAuditForbidden = errors.New("customer audit access denied")
var ErrCustomerAuditParams = errors.New("invalid customer audit query")

// RequireEmployeeCustomerAccess deliberately bypasses employee/profile caches.
// Do not lock users here: relay quota persistence updates those same rows.
func RequireEmployeeCustomerAccess(ctx context.Context, db *gorm.DB, employeeID, customerID int) error {
	if employeeID <= 0 || customerID <= 0 || employeeID == customerID {
		return ErrCustomerAuditForbidden
	}
	if err := RequireEmployeeAuditAccess(ctx, db, employeeID); err != nil {
		return err
	}
	var customer User
	err := employeeAuditCustomers(db.WithContext(ctx), employeeID).Select("id").Where("id = ?", customerID).Take(&customer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrCustomerAuditForbidden
	}
	return err
}

// RequireEmployeeAuditAccess uses current database state, never the employee cache.
func RequireEmployeeAuditAccess(ctx context.Context, db *gorm.DB, employeeID int) error {
	if employeeID <= 0 {
		return ErrCustomerAuditForbidden
	}
	var operator User
	err := db.WithContext(ctx).Select("id").Where("id = ? AND status = ?", employeeID, common.UserStatusEnabled).
		Where("id IN (?)", db.Model(&EmployeeProfile{}).Select("user_id").Where("user_id = ? AND status = ?", employeeID, 1)).Take(&operator).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrCustomerAuditForbidden
	}
	return err
}

const CustomerAuditPageSize = 20

type CustomerAuditFilter struct {
	EmployeeID int
	CustomerID int
	Start      int64
	End        int64
	RequestID  string
	IP         string
	Cursor     string
	View       string `json:",omitempty"`
}

type CustomerAuditPosition struct {
	Start, End int64
	At         int64
	ID         int64
	EventID    string
	Binding    string
}

func (f CustomerAuditFilter) DecodeCursor(kind string) (CustomerAuditPosition, error) {
	var position CustomerAuditPosition
	if f.View != "" && (kind != "alerts" || f.View != "handled") {
		return position, ErrCustomerAuditParams
	}
	if f.EmployeeID <= 0 || f.CustomerID <= 0 || f.Start <= 0 || f.End < f.Start || f.End-f.Start > 31*86400 || len(f.RequestID) > 64 || len(f.IP) > 64 || len(f.Cursor) > 2048 {
		return position, ErrCustomerAuditParams
	}
	bound := f
	bound.Cursor = ""
	raw, err := common.Marshal(bound)
	if err != nil {
		return position, err
	}
	binding := fmt.Sprintf("%s:%x", kind, sha256.Sum256(raw))
	position.Binding = binding
	position.Start, position.End = f.Start, f.End
	if f.Cursor == "" {
		return position, nil
	}
	raw, err = base64.RawURLEncoding.DecodeString(f.Cursor)
	if err != nil || common.Unmarshal(raw, &position) != nil || position.Binding != binding || position.At < f.Start || position.At > f.End || position.ID < 0 || len(position.EventID) > 64 || kind == "audit" && position.EventID == "" || kind == "alerts" && position.ID == 0 {
		return CustomerAuditPosition{}, ErrCustomerAuditParams
	}
	return position, nil
}

func (p CustomerAuditPosition) Encode() (string, error) {
	raw, err := common.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(raw), err
}

type CustomerAuditPage struct {
	Items      []*AuditLog `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

func QueryCustomerAuditLogs(ctx context.Context, mainDB, logsDB *gorm.DB, f CustomerAuditFilter) (CustomerAuditPage, error) {
	page := CustomerAuditPage{Items: []*AuditLog{}}
	if err := RequireEmployeeCustomerAccess(ctx, mainDB, f.EmployeeID, f.CustomerID); err != nil {
		return page, err
	}
	position, err := f.DecodeCursor("audit")
	if err != nil {
		return page, err
	}
	filter := AuditLogFilter{UserId: f.CustomerID, SelfView: true, StartTimestamp: f.Start, EndTimestamp: f.End, RequestId: f.RequestID, IP: f.IP}
	page.Items, err = queryCustomerAuditRecords(ctx, logsDB, filter, []int{f.CustomerID}, position)
	if err != nil {
		return CustomerAuditPage{Items: []*AuditLog{}}, err
	}
	if len(page.Items) > CustomerAuditPageSize {
		page.Items = page.Items[:CustomerAuditPageSize]
		last := page.Items[len(page.Items)-1]
		position.At, position.EventID = last.CreatedAt, last.EventId
		page.NextCursor, err = position.Encode()
	}
	ProjectAuditLogs(page.Items, true, common.RoleCommonUser)
	return page, err
}

// queryCustomerAuditRecords fills a page after excluding historical management
// rows. Callers must authorize every ID before using this shared reader.
func queryCustomerAuditRecords(ctx context.Context, logsDB *gorm.DB, filter AuditLogFilter, customerIDs []int, position CustomerAuditPosition) ([]*AuditLog, error) {
	items := []*AuditLog{}
	if len(customerIDs) == 0 {
		return items, nil
	}
	for len(items) <= CustomerAuditPageSize {
		query := AuditLogQuery(logsDB.WithContext(ctx), filter, common.RoleCommonUser).
			Where("user_id IN ?", customerIDs).
			Where("actor_role = ?", common.RoleCommonUser).
			Where("actor_is_employee = ? OR actor_is_employee IS NULL", false)
		if position.At > 0 {
			query = query.Where("created_at < ? OR (created_at = ? AND event_id < ?)", position.At, position.At, position.EventID)
		}
		var rows []*AuditLog
		if err := query.Select(AuditLogColumns).Order("created_at DESC").Order("event_id DESC").Limit(CustomerAuditPageSize + 1).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			// Historical management records may predate actor snapshots. Skip them
			// before counting the page, including whole batches of hidden records.
			if row.Other.AdminInfo == nil {
				items = append(items, row)
				if len(items) > CustomerAuditPageSize {
					break
				}
			}
		}
		if len(rows) < CustomerAuditPageSize+1 {
			break
		}
		last := rows[len(rows)-1]
		position.At, position.EventID = last.CreatedAt, last.EventId
	}
	return items, nil
}
