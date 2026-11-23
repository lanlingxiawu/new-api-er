package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"
)

// EmployeeChoice is the directory projection available to management tokens.
// ID is the employee profile ID used by the assignment endpoint.
type EmployeeChoice struct {
	ID          int    `json:"id"`
	UserID      int    `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Status      int    `json:"status"`
}

func ListEmployeeChoices(ctx context.Context, page, pageSize, status int) ([]EmployeeChoice, int64, error) {
	query := DB.WithContext(ctx).Table("employee_profiles").
		Joins("JOIN users ON users.id = employee_profiles.user_id").
		Where("users.deleted_at IS NULL AND users.status = ? AND users.role = ?", common.UserStatusEnabled, common.RoleCommonUser)
	if status != 0 {
		query = query.Where("employee_profiles.status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]EmployeeChoice, 0)
	err := query.Select("employee_profiles.id, employee_profiles.user_id, users.username, users.display_name, employee_profiles.status").
		Order("employee_profiles.id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&items).Error
	return items, total, err
}
