package model

// GroupStatus tracks the available models for each user group.
type GroupStatus struct {
	ID              int    `gorm:"primaryKey" json:"id"`
	UserGroup       string `gorm:"uniqueIndex;type:varchar(64);not null" json:"user_group"`
	AvailableModels int    `gorm:"default:0" json:"available_models"`
	TotalModels     int    `gorm:"default:0" json:"total_models"`
	LastTestTime    int64  `json:"last_test_time"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

func (g *GroupStatus) TableName() string { return "group_statuses" }

// GroupModelStatus tracks model availability within a user group.
type GroupModelStatus struct {
	ID           int    `gorm:"primaryKey" json:"id"`
	UserGroup    string `gorm:"uniqueIndex:idx_group_model;priority:1;type:varchar(64);not null" json:"user_group"`
	ModelName    string `gorm:"uniqueIndex:idx_group_model;priority:2;type:varchar(128);not null" json:"model_name"`
	Available    bool   `gorm:"default:1" json:"available"`
	LastTestTime int64  `json:"last_test_time"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

func (m *GroupModelStatus) TableName() string { return "group_model_statuses" }

// GetAllGroupStatuses returns all group status rows ordered by available models.
func GetAllGroupStatuses() ([]GroupStatus, error) {
	var gs []GroupStatus
	err := DB.Order("available_models DESC, user_group ASC").Find(&gs).Error
	return gs, err
}

// GetGroupStatus returns the status of a specific group.
func GetGroupStatus(userGroup string) (*GroupStatus, error) {
	var gs GroupStatus
	err := DB.Where("user_group = ?", userGroup).First(&gs).Error
	if err != nil {
		return nil, err
	}
	return &gs, nil
}

// UpsertGroupStatus saves-or-updates a group status row.
func UpsertGroupStatus(gs *GroupStatus) error {
	return DB.Save(gs).Error
}

// GetAvailabilityRatio returns the availability rate (0-100%) for a group.
func (g *GroupStatus) GetAvailabilityRatio() float64 {
	if g.TotalModels == 0 {
		return 0
	}
	return float64(g.AvailableModels) / float64(g.TotalModels) * 100
}

// GetGroupModelStatus returns the status of a specific model in a group.
func GetGroupModelStatus(userGroup, modelName string) (*GroupModelStatus, error) {
	var ms GroupModelStatus
	err := DB.Where("user_group = ? AND model_name = ?", userGroup, modelName).First(&ms).Error
	if err != nil {
		return nil, err
	}
	return &ms, nil
}

// GetGroupModelStatuses returns all model statuses for a group.
func GetGroupModelStatuses(userGroup string) ([]GroupModelStatus, error) {
	var statuses []GroupModelStatus
	err := DB.Where("user_group = ?", userGroup).Find(&statuses).Error
	return statuses, err
}

// GetAllGroupModelStatuses returns every group/model availability row.
func GetAllGroupModelStatuses() ([]GroupModelStatus, error) {
	var statuses []GroupModelStatus
	err := DB.Find(&statuses).Error
	return statuses, err
}

// UpsertGroupModelStatus saves-or-updates a group model status row.
func UpsertGroupModelStatus(ms *GroupModelStatus) error {
	values := map[string]any{
		"user_group":     ms.UserGroup,
		"model_name":     ms.ModelName,
		"available":      ms.Available,
		"last_test_time": ms.LastTestTime,
		"created_at":     ms.CreatedAt,
		"updated_at":     ms.UpdatedAt,
	}
	if ms.ID != 0 {
		return DB.Model(&GroupModelStatus{}).Where("id = ?", ms.ID).Updates(values).Error
	}
	return DB.Model(&GroupModelStatus{}).Create(values).Error
}

// GetModelStatusesByGroup returns model availability for a group as a map.
func GetModelStatusesByGroup(userGroup string) (map[string]bool, error) {
	var statuses []GroupModelStatus
	err := DB.Where("user_group = ?", userGroup).Find(&statuses).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool)
	for _, s := range statuses {
		result[s.ModelName] = s.Available
	}
	return result, nil
}
