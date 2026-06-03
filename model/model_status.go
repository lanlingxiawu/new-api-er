package model

// ModelStatus tracks the real-time health of each AI model.
type ModelStatus struct {
	ID                int     `gorm:"primaryKey" json:"id"`
	ModelName         string  `gorm:"uniqueIndex;type:varchar(255);not null" json:"model_name"`
	Status            int     `gorm:"default:1;index" json:"status"` // 1=healthy 2=warning 3=unhealthy 4=unavailable
	LastTestTime      int64   `json:"last_test_time"`
	SuccessRate       float64 `gorm:"default:100" json:"success_rate"`
	AvgResponseTime   int     `json:"avg_response_time"`
	P95ResponseTime   int     `json:"p95_response_time"`
	ErrorCount        int     `gorm:"default:0" json:"error_count"`
	TotalRequests     int     `gorm:"default:0" json:"total_requests"`
	TotalChannels     int     `gorm:"default:0" json:"total_channels"`
	AvailableChannels int     `gorm:"default:0" json:"available_channels"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
}

func (m *ModelStatus) TableName() string { return "model_statuses" }

// GetAllModelStatuses returns all model status rows ordered by health then success rate.
func GetAllModelStatuses() ([]ModelStatus, error) {
	var ms []ModelStatus
	err := DB.Order("status asc, success_rate desc").Find(&ms).Error
	return ms, err
}

// UpsertModelStatus saves-or-updates a model status row.
func UpsertModelStatus(ms *ModelStatus) error {
	return DB.Save(ms).Error
}
