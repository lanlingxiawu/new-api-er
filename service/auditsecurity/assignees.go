package auditsecurity

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"gorm.io/gorm"
)

var ErrInvalidAlertAssignee = errors.New("assignee cannot investigate this alert")

type AlertAssignee struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     int    `json:"role"`
}

func securityAssigneeAllowed(ctx context.Context, db *gorm.DB, visibility int, user model.User) (bool, error) {
	if user.Status != common.UserStatusEnabled || (user.Role != common.RoleAdminUser && user.Role != common.RoleRootUser) {
		return false, nil
	}
	if user.Role == common.RoleRootUser {
		return true, nil
	}
	if visibility != common.RoleAdminUser {
		return false, nil
	}
	permissions := []authz.Permission{authz.AuditRead, authz.AuditSecurityRead, authz.AuditSecurityManage}
	current, err := authz.ReadCurrentPermissionsFromDB(ctx, db, user.Id, user.Role, permissions)
	if err != nil {
		return false, err
	}
	for _, permission := range permissions {
		if !current[permission] {
			return false, nil
		}
	}
	return true, nil
}

func ListAlertAssignees(ctx context.Context, db *gorm.DB, viewerRole int, alertID int64) ([]AlertAssignee, error) {
	if viewerRole < common.RoleAdminUser {
		return nil, gorm.ErrRecordNotFound
	}
	var alert model.SecurityAlert
	if err := model.SecurityVisibleQuery(db.WithContext(ctx), viewerRole).Select("id", "visibility").Where("id = ? AND (purge_state = ? OR purge_state IS NULL)", alertID, "").Take(&alert).Error; err != nil {
		return nil, err
	}
	roles := []int{common.RoleRootUser}
	if alert.Visibility == common.RoleAdminUser {
		roles = append(roles, common.RoleAdminUser)
	}
	result := make([]AlertAssignee, 0)
	var after int
	for {
		var users []model.User
		if err := db.WithContext(ctx).Select("id", "username", "role", "status").Where("id > ? AND role IN ? AND status = ?", after, roles, common.UserStatusEnabled).Order("id").Limit(100).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, user := range users {
			allowed, err := securityAssigneeAllowed(ctx, db, alert.Visibility, user)
			if err != nil {
				return nil, err
			}
			if allowed {
				result = append(result, AlertAssignee{ID: user.Id, Username: user.Username, Role: user.Role})
			}
		}
		if len(users) < 100 {
			break
		}
		after = users[len(users)-1].Id
	}
	slices.SortFunc(result, func(a, b AlertAssignee) int {
		if order := cmp.Compare(a.Username, b.Username); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return result, nil
}
