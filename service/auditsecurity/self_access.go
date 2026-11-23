package auditsecurity

import (
	"context"
	"encoding/base64"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

var ErrSelfSecurityParams = errors.New("invalid self security query")

const selfSecurityPageSize = 20

// Never serialize a management/customer DTO through a self-service endpoint.
type SelfSecurityAlert struct {
	ID        int64  `json:"id"`
	Rule      string `json:"rule"`
	Severity  int    `json:"severity"`
	Status    string `json:"status"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
}

type SelfSecurityPage struct {
	Items      []SelfSecurityAlert `json:"items"`
	NextCursor string              `json:"next_cursor"`
}

type SelfSecurityFilter struct {
	UserID     int
	Start, End int64
	Cursor     string
}

type selfSecurityCursor struct {
	UserID         int
	Start, End, At int64
	ID             int64
}

func projectSelfSecurityAlert(a model.SecurityAlert) SelfSecurityAlert {
	return SelfSecurityAlert{ID: a.ID, Rule: a.Rule, Severity: a.Severity, Status: a.Status, FirstSeen: a.FirstSeen, LastSeen: a.LastSeen}
}

func ListSelfSecurityAlerts(ctx context.Context, db *gorm.DB, f SelfSecurityFilter) (SelfSecurityPage, error) {
	page := SelfSecurityPage{Items: []SelfSecurityAlert{}}
	if f.UserID <= 0 || f.Start <= 0 || f.End < f.Start || f.End-f.Start > 31*86400 || len(f.Cursor) > 2048 {
		return page, ErrSelfSecurityParams
	}
	position := selfSecurityCursor{UserID: f.UserID, Start: f.Start, End: f.End}
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil || common.Unmarshal(raw, &position) != nil || position.UserID != f.UserID || position.Start != f.Start || position.End != f.End || position.At < f.Start || position.At > f.End || position.ID <= 0 {
			return page, ErrSelfSecurityParams
		}
	}
	db = db.WithContext(ctx)
	rows, err := queryAccountAlertRecords(ctx, db, []int{f.UserID}, f.Start, f.End, model.CustomerAuditPosition{At: position.At, ID: position.ID}, true, "")
	if err != nil {
		return page, err
	}
	for _, row := range rows {
		page.Items = append(page.Items, projectSelfSecurityAlert(row))
	}

	if len(page.Items) > selfSecurityPageSize {
		page.Items = page.Items[:selfSecurityPageSize]
		last := page.Items[len(page.Items)-1]
		position.At, position.ID = last.LastSeen, last.ID
		raw, err := common.Marshal(position)
		if err != nil {
			return page, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

type SelfSecurityEvent struct {
	Action  string `json:"action"`
	At      int64  `json:"at"`
	IP      string `json:"ip"`
	Success bool   `json:"success"`
}

type SelfSecurityResponse struct {
	ID            int64  `json:"id"`
	Action        string `json:"action"`
	Outcome       string `json:"outcome"`
	CreatedAt     int64  `json:"created_at"`
	AffectedCount *int64 `json:"affected_count,omitempty"`
}

type SelfSecurityDetail struct {
	Alert     SelfSecurityAlert      `json:"alert"`
	Events    []SelfSecurityEvent    `json:"events"`
	Responses []SelfSecurityResponse `json:"responses"`
}

func GetSelfSecurityAlert(ctx context.Context, db *gorm.DB, userID int, id int64) (SelfSecurityDetail, error) {
	detail := SelfSecurityDetail{Events: []SelfSecurityEvent{}, Responses: []SelfSecurityResponse{}}
	if userID <= 0 || id <= 0 {
		return detail, ErrSelfSecurityParams
	}
	db = db.WithContext(ctx)
	var alert model.SecurityAlert
	if err := db.Select(customerAlertColumns+",evidence").Where("id = ? AND (purge_state = ? OR purge_state IS NULL)", id, "").Take(&alert).Error; err != nil {
		return detail, err
	}
	allowed, err := accountAlertAccessible(db, alert, userID, true)
	if err != nil {
		return detail, err
	}
	if !allowed {
		return detail, gorm.ErrRecordNotFound
	}
	detail.Alert = projectSelfSecurityAlert(alert)
	var events []model.SecurityEvent
	if alert.Evidence != "" {
		if err := common.UnmarshalJsonStr(alert.Evidence, &events); err != nil {
			return detail, err
		}
	}
	for _, event := range collapseEvidenceEvents(events) {
		// Access requires a verified actor or server-recorded login target;
		// an attempted account name alone never establishes ownership.
		if !accountEventAccessible(alert, event, userID, true) {
			continue
		}
		detail.Events = append(detail.Events, SelfSecurityEvent{Action: event.Action, At: event.At, IP: event.IP, Success: event.Success})
		if len(detail.Events) == 20 {
			break
		}
	}
	alerts := []model.SecurityAlert{alert}
	if err := loadAlertResponses(ctx, db, alerts); err != nil {
		return detail, err
	}
	for i := len(alerts[0].Responses) - 1; i >= 0; i-- {
		action := alerts[0].Responses[i]
		detail.Responses = append(detail.Responses, SelfSecurityResponse{ID: action.ID, Action: action.Action, Outcome: action.Outcome, CreatedAt: action.CreatedAt, AffectedCount: action.AffectedCount})
	}
	return detail, nil
}
