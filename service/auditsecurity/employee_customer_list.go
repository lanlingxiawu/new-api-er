package auditsecurity

import (
	"cmp"
	"context"
	"slices"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type EmployeeCustomerSecurityAlert struct {
	CustomerSecurityAlert
	Customer model.CustomerAuditIdentity `json:"customer"`
}

type EmployeeCustomerAlertPage struct {
	Items      []EmployeeCustomerSecurityAlert `json:"items"`
	NextCursor string                          `json:"next_cursor"`
}

func ListEmployeeCustomerAlerts(ctx context.Context, db *gorm.DB, f model.EmployeeAuditFilter) (EmployeeCustomerAlertPage, error) {
	page := EmployeeCustomerAlertPage{Items: []EmployeeCustomerSecurityAlert{}}
	if err := model.RequireEmployeeAuditAccess(ctx, db, f.EmployeeID); err != nil {
		return page, err
	}
	position, err := f.DecodeCursor("alerts")
	if err != nil {
		return page, err
	}
	if f.RequestID != "" || f.IP != "" {
		return page, model.ErrCustomerAuditParams
	}
	var candidates []model.SecurityAlert
	for after := 0; ; {
		ids, err := model.EmployeeAuditCustomerIDs(ctx, db, f.EmployeeID, after)
		if err != nil {
			return page, err
		}
		if len(ids) == 0 {
			break
		}
		rows, err := queryCustomerAlertRecords(ctx, db, ids, f.Start, f.End, position, f.View)
		if err != nil {
			return page, err
		}
		candidates = append(candidates, rows...)
		slices.SortFunc(candidates, func(a, b model.SecurityAlert) int {
			if order := cmp.Compare(b.LastSeen, a.LastSeen); order != 0 {
				return order
			}
			return cmp.Compare(b.ID, a.ID)
		})
		if len(candidates) > model.CustomerAuditPageSize+1 {
			clear(candidates[model.CustomerAuditPageSize+1:])
			candidates = candidates[:model.CustomerAuditPageSize+1]
		}
		if len(ids) < model.EmployeeAuditCustomerBatchSize {
			break
		}
		after = ids[len(ids)-1]
	}
	ids := make([]int, len(candidates))
	for i, row := range candidates {
		ids[i] = alertAccountID(row)
	}
	identities, err := model.EmployeeAuditCustomerIdentities(ctx, db, f.EmployeeID, ids)
	if err != nil {
		return page, err
	}
	if len(candidates) > model.CustomerAuditPageSize {
		candidates = candidates[:model.CustomerAuditPageSize]
		last := candidates[len(candidates)-1]
		position.At, position.ID = last.LastSeen, last.ID
		page.NextCursor, err = position.Encode()
		if err != nil {
			return page, err
		}
	}
	if err := loadAlertResponses(ctx, db, candidates); err != nil {
		return EmployeeCustomerAlertPage{Items: []EmployeeCustomerSecurityAlert{}}, err
	}
	for _, row := range candidates {
		page.Items = append(page.Items, EmployeeCustomerSecurityAlert{CustomerSecurityAlert: projectCustomerAlert(row), Customer: identities[alertAccountID(row)]})
	}
	return page, nil
}
