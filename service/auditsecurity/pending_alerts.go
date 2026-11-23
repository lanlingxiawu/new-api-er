package auditsecurity

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// A count of 100 means at least 100. No count query scans the entire history.
const PendingAlertCountLimit = 100

func CountPendingAlerts(ctx context.Context, db *gorm.DB, role int) (int, error) {
	var ids []int64
	err := model.SecurityVisibleQuery(db.WithContext(ctx).Model(&model.SecurityAlert{}), role).
		Where("status IN ? AND archived_at IS NULL AND (purge_state = ? OR purge_state IS NULL)", []string{"open", "investigating"}, "").
		Limit(PendingAlertCountLimit).Pluck("id", &ids).Error
	return len(ids), err
}

type PendingCustomerFilter struct {
	EmployeeID int
	CustomerID int // Zero selects all currently owned customers.
	Cursor     string
}

type pendingCustomerPosition struct {
	Binding    string
	CustomerID int
	AlertID    int64
}

func (f PendingCustomerFilter) decodeCursor() (pendingCustomerPosition, error) {
	position := pendingCustomerPosition{Binding: fmt.Sprintf("customer-pending:%d:%d:%d", model.AuditActivityProjectionVersion, f.EmployeeID, f.CustomerID)}
	if f.EmployeeID <= 0 || f.CustomerID < 0 || len(f.Cursor) > 2048 {
		return position, model.ErrCustomerAuditParams
	}
	if f.Cursor == "" {
		return position, nil
	}
	var decoded pendingCustomerPosition
	raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
	if err != nil || common.Unmarshal(raw, &decoded) != nil || decoded.Binding != position.Binding || decoded.CustomerID <= 0 || decoded.AlertID <= 0 || f.CustomerID > 0 && decoded.CustomerID != f.CustomerID {
		return position, model.ErrCustomerAuditParams
	}
	return decoded, nil
}

func CountEmployeePendingAlerts(ctx context.Context, db *gorm.DB, employeeID int) (int, error) {
	rows, _, err := readPendingCustomerAlerts(ctx, db, PendingCustomerFilter{EmployeeID: employeeID}, pendingCustomerPosition{}, PendingAlertCountLimit)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func ListPendingCustomerAlerts(ctx context.Context, db *gorm.DB, f PendingCustomerFilter) (EmployeeCustomerAlertPage, error) {
	page := EmployeeCustomerAlertPage{Items: []EmployeeCustomerSecurityAlert{}}
	position, err := f.decodeCursor()
	if err != nil {
		return page, err
	}
	rows, identities, err := readPendingCustomerAlerts(ctx, db, f, position, model.CustomerAuditPageSize+1)
	if err != nil {
		return page, err
	}
	if len(rows) > model.CustomerAuditPageSize {
		rows = rows[:model.CustomerAuditPageSize]
		last := rows[len(rows)-1]
		position.CustomerID, position.AlertID = alertAccountID(last), last.ID
		raw, err := common.Marshal(position)
		if err != nil {
			return page, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	if err := loadAlertResponses(ctx, db, rows); err != nil {
		return page, err
	}
	for _, row := range rows {
		page.Items = append(page.Items, EmployeeCustomerSecurityAlert{CustomerSecurityAlert: projectCustomerAlert(row), Customer: identities[alertAccountID(row)]})
	}
	return page, nil
}

// Count and list share identity checks, including legacy evidence and a final
// ownership recheck. All reads use the caller's bounded, isolated audit pool.
func readPendingCustomerAlerts(ctx context.Context, db *gorm.DB, f PendingCustomerFilter, position pendingCustomerPosition, limit int) ([]model.SecurityAlert, map[int]model.CustomerAuditIdentity, error) {
	if err := model.RequireEmployeeAuditAccess(ctx, db, f.EmployeeID); err != nil {
		return nil, nil, err
	}
	if f.CustomerID > 0 {
		if err := model.RequireEmployeeCustomerAccess(ctx, db, f.EmployeeID, f.CustomerID); err != nil {
			return nil, nil, err
		}
	}
	var rows []model.SecurityAlert
	for after := max(0, position.CustomerID-1); len(rows) < limit; {
		ids := []int{f.CustomerID}
		if f.CustomerID == 0 {
			var err error
			ids, err = model.EmployeeAuditCustomerIDs(ctx, db, f.EmployeeID, after)
			if err != nil {
				return nil, nil, err
			}
		}
		if len(ids) == 0 {
			break
		}
		batch, err := queryPendingCustomerAlerts(ctx, db, ids, position, limit-len(rows))
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, batch...)
		if f.CustomerID > 0 || len(ids) < model.EmployeeAuditCustomerBatchSize {
			break
		}
		after = ids[len(ids)-1]
	}
	// Also recheck empty results: a revoked employee must never receive a
	// successful "nothing pending" response from an in-flight read.
	if err := model.RequireEmployeeAuditAccess(ctx, db, f.EmployeeID); err != nil {
		return nil, nil, err
	}
	if f.CustomerID > 0 {
		if err := model.RequireEmployeeCustomerAccess(ctx, db, f.EmployeeID, f.CustomerID); err != nil {
			return nil, nil, err
		}
	}
	identities := make(map[int]model.CustomerAuditIdentity)
	var customerIDs []int
	for _, row := range rows {
		id := alertAccountID(row)
		// Batches and rows are ordered by customer, so adjacent IDs suffice.
		if len(customerIDs) == 0 || customerIDs[len(customerIDs)-1] != id {
			customerIDs = append(customerIDs, id)
		}
	}
	for offset := 0; offset < len(customerIDs); offset += model.CustomerAuditPageSize + 1 {
		ids := customerIDs[offset:min(len(customerIDs), offset+model.CustomerAuditPageSize+1)]
		batch, err := model.EmployeeAuditCustomerIdentities(ctx, db, f.EmployeeID, ids)
		if err != nil {
			return nil, nil, err
		}
		for id, identity := range batch {
			identities[id] = identity
		}
	}
	return rows, identities, nil
}

// Walk each (customer, status, id) index independently. A global id sort can
// scan the primary key across the entire alert history for a small customer.
func queryPendingCustomerAlerts(ctx context.Context, db *gorm.DB, customerIDs []int, position pendingCustomerPosition, limit int) ([]model.SecurityAlert, error) {
	db = db.WithContext(ctx)
	var items []model.SecurityAlert
	for _, column := range []string{"actor_id", "target_user_id"} {
		for _, status := range []string{"open", "investigating"} {
			cursor := position
			accepted := 0
			for accepted < limit {
				q := db.Model(&model.SecurityAlert{}).Select("id,"+column).
					Where(column+" IN ? AND status = ? AND archived_at IS NULL AND (purge_state = ? OR purge_state IS NULL)", customerIDs, status, "")
				if cursor.CustomerID > 0 {
					q = q.Where(column+" > ? OR ("+column+" = ? AND id > ?)", cursor.CustomerID, cursor.CustomerID, cursor.AlertID)
				}
				var candidates []model.SecurityAlert
				if err := q.Order(column + " ASC").Order("id ASC").Limit(limit).Find(&candidates).Error; err != nil {
					return nil, err
				}
				if len(candidates) == 0 {
					break
				}
				ids := make([]int64, len(candidates))
				for i, row := range candidates {
					ids[i] = row.ID
				}
				var rows []model.SecurityAlert
				if err := db.Select(customerAlertColumns+",archived_at").Where("id IN ?", ids).Find(&rows).Error; err != nil {
					return nil, err
				}
				byID := make(map[int64]model.SecurityAlert, len(rows))
				for _, row := range rows {
					byID[row.ID] = row
				}
				for _, candidate := range candidates {
					accountID := candidate.ActorID
					if column == "target_user_id" && candidate.TargetUserID != nil {
						accountID = *candidate.TargetUserID
					}
					cursor.CustomerID, cursor.AlertID = accountID, candidate.ID
					row, ok := byID[candidate.ID]
					if !ok || row.Status != status || row.ArchivedAt != nil || alertAccountID(row) != accountID {
						continue
					}
					allowed, err := customerAlertAccessible(db, row, accountID)
					if err != nil {
						return nil, err
					}
					if allowed {
						items = append(items, row)
						accepted++
						if accepted == limit {
							break
						}
					}
				}
				if len(candidates) < limit {
					break
				}
			}
		}
	}
	slices.SortFunc(items, func(a, b model.SecurityAlert) int {
		if order := cmp.Compare(alertAccountID(a), alertAccountID(b)); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	items = slices.CompactFunc(items, func(a, b model.SecurityAlert) bool { return a.ID == b.ID })
	return items[:min(len(items), limit)], nil
}
