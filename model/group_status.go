package model

// GroupStatus tracks the real-time health of each user group.
type GroupStatus struct {
	ID                int     `gorm:"primaryKey" json:"id"`
	UserGroup         string  `gorm:"uniqueIndex;type:varchar(64);not null" json:"user_group"`
	Status            int     `gorm:"default:1;index" json:"status"` // 1=healthy 2=warning 3=unhealthy 4=unavailable
	LastTestTime      int64   `json:"last_test_time"`
	SuccessRate       float64 `gorm:"default:100" json:"success_rate"`
	AvgResponseTime   int     `json:"avg_response_time"` // ms
	P95ResponseTime   int     `json:"p95_response_time"` // ms
	ErrorCount        int     `gorm:"default:0" json:"error_count"`
	TotalRequests     int     `gorm:"default:0" json:"total_requests"`
	TotalChannels     int     `gorm:"default:0" json:"total_channels"`
	AvailableChannels int     `gorm:"default:0" json:"available_channels"`
	DisabledChannels  int     `gorm:"default:0" json:"disabled_channels"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
}

func (g *GroupStatus) TableName() string { return "group_statuses" }

// GroupStatusHistory stores hourly snapshots of each group's metrics.
type GroupStatusHistory struct {
	ID                int64   `gorm:"primaryKey" json:"id"`
	UserGroup         string  `gorm:"index:idx_group_hour,priority:1;type:varchar(64);not null" json:"user_group"`
	SnapshotHour      int64   `gorm:"index:idx_group_hour,priority:2;index:idx_snapshot_hour" json:"snapshot_hour"` // unix timestamp of hour start
	SuccessRate       float64 `json:"success_rate"`
	AvgResponseTime   int     `json:"avg_response_time"`
	AvailableChannels int     `json:"available_channels"`
	ErrorCount        int     `json:"error_count"`
	TotalRequests     int     `json:"total_requests"`
	CreatedAt         int64   `json:"created_at"`
}

func (h *GroupStatusHistory) TableName() string { return "group_status_histories" }

// GetAllGroupStatuses returns all group status rows.
func GetAllGroupStatuses() ([]GroupStatus, error) {
	var gs []GroupStatus
	err := DB.Order("status asc, success_rate desc").Find(&gs).Error
	return gs, err
}

// UpsertGroupStatus saves-or-updates a group status row.
func UpsertGroupStatus(gs *GroupStatus) error {
	return DB.Save(gs).Error
}

// GetGroupStatusHistory returns hourly history for a group within a time range.
func GetGroupStatusHistory(userGroup string, startTime int64) ([]GroupStatusHistory, error) {
	var hist []GroupStatusHistory
	err := DB.Where("user_group = ? AND snapshot_hour >= ?", userGroup, startTime).
		Order("snapshot_hour asc").Find(&hist).Error
	return hist, err
}
