package auditsecurity

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type SecurityExport struct {
	ExportedAt int64         `json:"exported_at"`
	Filters    AlertFilter   `json:"filters"`
	Truncated  bool          `json:"truncated"`
	Items      []AlertDetail `json:"items"`
}

func ExportAlerts(ctx context.Context, db *gorm.DB, role int, filter AlertFilter, limit int) (SecurityExport, error) {
	result := SecurityExport{Items: []AlertDetail{}}
	if limit < 1 || limit > 100 || filter.View == "activity" || filter.View == "recent_hits" {
		return result, ErrActivityCursor
	}
	filter.Limit = limit
	page, err := ListAlerts(ctx, db, role, filter)
	if err != nil {
		return result, err
	}
	result.Filters = filter
	switch next := page.Next.(type) {
	case int64:
		result.Truncated = next > 0
	case string:
		result.Truncated = next != ""
	}
	for _, alert := range page.Items {
		detail, err := GetAlert(ctx, db, role, alert.ID)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, detail)
	}
	result.ExportedAt = time.Now().Unix()
	return result, nil
}
