package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
)

const AdminTokenMaxLifetimeSeconds int64 = 30 * 24 * 60 * 60

var ErrAdminTokenDenied = errors.New("Management access is not allowed for this token. Sign in to the dashboard or contact the root administrator.")

func IsManagementScope(scope string) bool {
	resource, _, _ := strings.Cut(scope, ":")
	for _, r := range accessTokenStaticResources {
		if r.Resource == resource {
			return r.group != AccessTokenGroupPersonal
		}
	}
	for _, r := range authz.Catalog() {
		if r.Resource == resource {
			return true
		}
	}
	return false
}

// AdminTokenRouteModule is an explicit allowlist, shared with verification.
// New management routes remain inaccessible until object and response handling
// have been reviewed. Never infer safety from the HTTP verb alone.
func AdminTokenRouteModule(route string) string {
	switch route {
	case "GET /api/user/search":
		return authz.ActionUsersList
	case "GET /api/group/":
		return authz.ActionGroupsRead
	case "GET /api/admin/employee":
		return authz.ActionEmployeesRead
	case "GET /api/log/stat":
		return authz.ActionUsersStats
	case "POST /api/user/manage":
		// The PAT handler only accepts add_quota; account management stays session-only.
		return authz.ActionUsersQuota
	case "PUT /api/user/":
		return authz.ActionUsersGroupRatios
	case "POST /api/admin/employee/:id/assign-customer", "DELETE /api/admin/employee/:id/customer/:user_id":
		return authz.ActionUsersEmployee
	case "POST /api/user/topup/reconcile":
		return authz.ActionTopupsReconcile
	case "GET /api/channel/", "GET /api/channel/search", "GET /api/channel/:id",
		"GET /api/channel/models", "GET /api/channel/models_enabled", "GET /api/channel/tag/models",
		"GET /api/channel/exclusion-options",
		"POST /api/channel/", "PUT /api/channel/", "DELETE /api/channel/:id",
		"POST /api/channel/batch", "POST /api/channel/copy/:id",
		"POST /api/channel/:id/status", "POST /api/channel/status/batch",
		"POST /api/channel/batch/tag", "POST /api/channel/batch/daily_limit",
		"POST /api/channel/tag/enabled", "POST /api/channel/tag/disabled", "PUT /api/channel/tag",
		"POST /api/channel/multi_key/manage":
		return authz.ActionChannelsOwned
	}
	// Provider probes share outbound clients with relay. Until their redirect,
	// credential and proxy handling has a dedicated management boundary, PATs
	// cannot trigger them, even using an owned channel's saved configuration.
	return ""
}

func AdminTokenPersonalScopeDenied(scope string) bool {
	return slices.Contains([]string{"api_key:write", "api_key:reveal", "account_security:write"}, scope)
}

func AdminTokenLifetimeValid(token *model.UserAccessToken) bool {
	now := common.GetTimestamp()
	return token != nil && token.CreatedAt > 0 && token.CreatedAt <= now && token.ExpiresAt > now && token.ExpiresAt > token.CreatedAt && token.ExpiresAt-token.CreatedAt <= AdminTokenMaxLifetimeSeconds
}

// ReadAdminTokenAuthorization never serves an allow decision from a shared
// cache. All reads share a bounded context and use the primary database.
func ReadAdminTokenAuthorization(ctx context.Context, userID int, module string, permissions ...authz.Permission) (*model.User, map[authz.Permission]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var user model.User
	if err := model.DB.WithContext(ctx).Select("id", "role", "status", "auth_version").First(&user, userID).Error; err != nil {
		return nil, nil, err
	}
	if user.Role != common.RoleAdminUser || user.Status != common.UserStatusEnabled {
		return &user, nil, ErrAdminTokenDenied
	}
	use := authz.Permission{Resource: authz.ResourceAdminAccessToken, Action: authz.ActionUse}
	part := authz.Permission{Resource: authz.ResourceAdminAccessToken, Action: module}
	permissions = append(permissions, use, part)
	grants, err := authz.ReadCurrentPermissions(ctx, userID, user.Role, permissions)
	if err != nil {
		return &user, nil, err
	}
	if !grants[use] || !grants[part] {
		return &user, grants, ErrAdminTokenDenied
	}
	return &user, grants, nil
}
