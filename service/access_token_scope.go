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

const (
	AccessTokenGroupPersonal = "personal"
	AccessTokenGroupAdmin    = "admin"
	AccessTokenGroupSystem   = "system"

	AccessTokenMaxPerUser                = 20
	AccessTokenDefaultExpiryDays         = 30
	accessTokenActionReveal              = "reveal"
	accessTokenScopeSeparator            = ":"
	accessTokenChannelOperateDescription = "Enable or disable your own channels and manage their key status. Channel tests and balance refresh are not available to tokens."
)

var (
	ErrAccessTokenScopeInvalid   = errors.New("access token scope is invalid")
	ErrAccessTokenScopeForbidden = errors.New("access token scope cannot be granted")
)

// AccessTokenResource mirrors authz.ResourceDefinition so both catalogs share
// one JSON shape and one frontend matrix component.
type AccessTokenResource struct {
	Resource string                   `json:"resource"`
	LabelKey string                   `json:"label_key"`
	Actions  []authz.ActionDefinition `json:"actions"`
	group    string
	minRole  int
}

type AccessTokenCatalogGroup struct {
	Group     string                `json:"group"`
	Resources []AccessTokenResource `json:"resources"`
}

func accessTokenView(description string) authz.ActionDefinition {
	return authz.ActionDefinition{Action: authz.ActionRead, LabelKey: "View", DescriptionKey: description}
}

func accessTokenEdit(description string) authz.ActionDefinition {
	return authz.ActionDefinition{Action: authz.ActionWrite, LabelKey: "Edit", DescriptionKey: description}
}

// accessTokenStaticResources covers dashboard routes that are not guarded by a
// Casbin permission. Casbin resources come from authz.Catalog() at runtime and
// must never be duplicated here.
var accessTokenStaticResources = []AccessTokenResource{
	{Resource: "profile", LabelKey: "Profile", group: AccessTokenGroupPersonal, minRole: common.RoleCommonUser, Actions: []authz.ActionDefinition{
		accessTokenView("View your profile, groups, and available models."),
		accessTokenEdit("Change your display name and personal settings."),
	}},
	{Resource: "api_key", LabelKey: "API keys", group: AccessTokenGroupPersonal, minRole: common.RoleCommonUser, Actions: []authz.ActionDefinition{
		accessTokenView("View API keys and their settings without the full key."),
		accessTokenEdit("Create, edit, and delete API keys."),
		{Action: accessTokenActionReveal, LabelKey: "Reveal full keys", DescriptionKey: "View complete API keys."},
	}},
	{Resource: "usage", LabelKey: "Usage", group: AccessTokenGroupPersonal, minRole: common.RoleCommonUser, Actions: []authz.ActionDefinition{
		accessTokenView("View your usage logs, statistics, tasks, and audit logs."),
	}},
	{Resource: "wallet", LabelKey: "Wallet", group: AccessTokenGroupPersonal, minRole: common.RoleCommonUser, Actions: []authz.ActionDefinition{
		accessTokenView("View balance, top-up records, subscriptions, and referral information."),
		accessTokenEdit("Top up, pay for subscriptions, check in, and transfer referral rewards."),
	}},
	{Resource: "account_security", LabelKey: "Account security", group: AccessTokenGroupPersonal, minRole: common.RoleCommonUser, Actions: []authz.ActionDefinition{
		accessTokenView("View Passkey, two-factor, and sign-in binding status."),
		accessTokenEdit("Includes changes that require security verification, such as password, two-factor settings, and account deletion."),
	}},
	{Resource: "user", LabelKey: "Users", group: AccessTokenGroupAdmin, minRole: common.RoleAdminUser, Actions: []authz.ActionDefinition{
		accessTokenView("Look up one user by ID or full username, including balance, usage, assigned employee, and exclusive ratios. Requires the Users menu."),
		accessTokenEdit("Create, edit, disable, and delete users, and reset their security settings."),
		{Action: "quota_write", LabelKey: "Allow balance changes", DescriptionKey: "Add, subtract, or set a user's balance without per-operation verification. Requires the Users menu."},
		{Action: "group_ratios_write", LabelKey: "Allow exclusive ratio changes", DescriptionKey: "Change only a user's exclusive group ratios without per-operation verification. Requires the Users menu."},
		{Action: "employee_assign", LabelKey: "Allow employee assignment", DescriptionKey: "Assign, change, or remove a customer's employee without per-operation verification. Requires the Users and Employees menus."},
		{Action: "employee_read", LabelKey: "Allow employee selection", DescriptionKey: "View employee IDs, names, and status for assignment. Requires the Users and Employees menus."},
	}},
	{Resource: "billing", LabelKey: "Billing management", group: AccessTokenGroupAdmin, minRole: common.RoleAdminUser, Actions: []authz.ActionDefinition{
		accessTokenView("View all top-up records, subscription plans, and user subscriptions."),
		accessTokenEdit("Complete top-up orders and manage subscription plans and user subscriptions."),
		{Action: "topup_reconcile", LabelKey: "Allow verified top-up reconciliation", DescriptionKey: "Check one order with the payment provider and credit it only after payment is confirmed. Requires the Users menu."},
	}},
	{Resource: "model", LabelKey: "Models", group: AccessTokenGroupAdmin, minRole: common.RoleAdminUser, Actions: []authz.ActionDefinition{
		accessTokenView("View models and vendors, and preview upstream sync."),
		accessTokenEdit("Edit models and vendors, and sync them from upstream."),
	}},
	{Resource: "deployment", LabelKey: "Deployments", group: AccessTokenGroupAdmin, minRole: common.RoleAdminUser, Actions: []authz.ActionDefinition{
		accessTokenView("View deployments and price estimates."),
		accessTokenEdit("Create, edit, and delete deployments, and test connections."),
	}},
	{Resource: "log", LabelKey: "Logs", group: AccessTokenGroupAdmin, minRole: common.RoleAdminUser, Actions: []authz.ActionDefinition{
		accessTokenView("View all users' usage logs, statistics, and tasks."),
		{Action: "stats_read", LabelKey: "Allow usage statistics", DescriptionKey: "View site-wide or individual user usage statistics for up to 30 days. Requires the Users menu."},
	}},
	{Resource: "redemption", LabelKey: "Redemption codes", group: AccessTokenGroupAdmin, minRole: common.RoleAdminUser, Actions: []authz.ActionDefinition{
		accessTokenView("View redemption codes."),
		accessTokenEdit("Create, edit, and delete redemption codes."),
	}},
	{Resource: "group", LabelKey: "Groups", group: AccessTokenGroupAdmin, minRole: common.RoleAdminUser, Actions: []authz.ActionDefinition{
		accessTokenView("View group names for choosing exclusive ratios. Requires the Users menu."),
		accessTokenEdit("Create, edit, and delete prefill groups."),
	}},
	{Resource: "option", LabelKey: "System settings", group: AccessTokenGroupSystem, minRole: common.RoleRootUser, Actions: []authz.ActionDefinition{
		accessTokenView("View system settings, custom OAuth providers, and price sync sources."),
		accessTokenEdit("Change system settings and custom OAuth providers, and sync prices."),
	}},
	{Resource: "plugin", LabelKey: "Task plugins", group: AccessTokenGroupSystem, minRole: common.RoleRootUser, Actions: []authz.ActionDefinition{
		accessTokenView("View installed task plugins and their settings."),
		accessTokenEdit("Install, configure, test, and remove task plugins."),
	}},
	{Resource: "ops", LabelKey: "Operations", group: AccessTokenGroupSystem, minRole: common.RoleRootUser, Actions: []authz.ActionDefinition{
		accessTokenView("View performance, system tasks, and system information."),
		accessTokenEdit("Run and manage maintenance tasks and performance settings."),
	}},
}

// AccessTokenScopeOf is the token scope key for a Casbin permission.
func AccessTokenScopeOf(permission authz.Permission) string {
	return permission.Resource + accessTokenScopeSeparator + permission.Action
}

// AccessTokenCatalog lists the scopes this user may grant right now, grouped
// for display. Resources without a grantable action and empty groups are
// omitted.
func AccessTokenCatalog(userID, role int) []AccessTokenCatalogGroup {
	var current map[authz.Permission]bool
	if role == common.RoleAdminUser {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		var err error
		current, err = authz.ReadCurrentPermissions(ctx, userID, role, authz.AllPermissions())
		cancel()
		if err != nil {
			common.SysError("failed to load current management token permissions")
			current = nil
		}
	}
	use := current[authz.Permission{Resource: authz.ResourceAdminAccessToken, Action: authz.ActionUse}]
	users := use && current[authz.AdminMenuUsersView]
	channels := use && current[authz.Permission{Resource: authz.ResourceAdminAccessToken, Action: authz.ActionChannelsOwned}] && current[authz.AdminMenuChannelsView]
	resources := map[string][]AccessTokenResource{}
	for _, resource := range accessTokenStaticResources {
		if role < resource.minRole {
			continue
		}
		resource.Actions = slices.Clone(resource.Actions)
		if resource.group != AccessTokenGroupPersonal {
			if !users {
				continue
			}
			resource.Actions = slices.DeleteFunc(resource.Actions, func(action authz.ActionDefinition) bool {
				var capability string
				switch resource.Resource + ":" + action.Action {
				case "user:read":
					capability = authz.ActionUsersList
				case "user:quota_write":
					capability = authz.ActionUsersQuota
				case "user:group_ratios_write":
					capability = authz.ActionUsersGroupRatios
				case "user:employee_assign":
					if !current[authz.AdminMenuEmployeesView] {
						return true
					}
					capability = authz.ActionUsersEmployee
				case "user:employee_read":
					if !current[authz.AdminMenuEmployeesView] {
						return true
					}
					capability = authz.ActionEmployeesRead
				case "group:read":
					capability = authz.ActionGroupsRead
				case "log:stats_read":
					capability = authz.ActionUsersStats
				case "billing:topup_reconcile":
					capability = authz.ActionTopupsReconcile
				default:
					return true
				}
				return !current[authz.Permission{Resource: authz.ResourceAdminAccessToken, Action: capability}]
			})
		} else if role >= common.RoleAdminUser {
			resource.Actions = slices.DeleteFunc(resource.Actions, func(a authz.ActionDefinition) bool {
				return AdminTokenPersonalScopeDenied(resource.Resource + ":" + a.Action)
			})
		}
		if len(resource.Actions) == 0 {
			continue
		}
		resources[resource.group] = append(resources[resource.group], resource)
	}
	if role >= common.RoleAdminUser {
		for _, definition := range authz.Catalog() {
			if definition.Resource != authz.ResourceChannel || !channels {
				continue
			}
			resource := AccessTokenResource{Resource: definition.Resource, LabelKey: definition.LabelKey}
			for _, action := range definition.Actions {
				if action.Action != authz.ActionSecretView && current[authz.Permission{Resource: definition.Resource, Action: action.Action}] {
					if action.Action == authz.ActionOperate {
						action.DescriptionKey = accessTokenChannelOperateDescription
					}
					resource.Actions = append(resource.Actions, action)
				}
			}
			if len(resource.Actions) > 0 {
				resources[AccessTokenGroupAdmin] = append(resources[AccessTokenGroupAdmin], resource)
			}
		}
	}
	groups := make([]AccessTokenCatalogGroup, 0, 3)
	for _, group := range []string{AccessTokenGroupPersonal, AccessTokenGroupAdmin, AccessTokenGroupSystem} {
		if len(resources[group]) > 0 {
			groups = append(groups, AccessTokenCatalogGroup{Group: group, Resources: resources[group]})
		}
	}
	return groups
}

// AccessTokenScopeDictionary lists every resource a token scope can name,
// whatever the viewer may grant, so stored grants keep their labels after the
// granting user loses a permission or when another user reads them.
func AccessTokenScopeDictionary() []AccessTokenResource {
	definitions := authz.Catalog()
	resources := make([]AccessTokenResource, 0, len(accessTokenStaticResources)+len(definitions))
	for _, resource := range accessTokenStaticResources {
		resource.Actions = slices.Clone(resource.Actions)
		resources = append(resources, resource)
	}
	for _, definition := range definitions {
		if definition.Resource == authz.ResourceAdminAccessToken {
			continue
		}
		actions := slices.Clone(definition.Actions)
		if definition.Resource == authz.ResourceChannel {
			for i := range actions {
				if actions[i].Action == authz.ActionOperate {
					actions[i].DescriptionKey = accessTokenChannelOperateDescription
				}
			}
		}
		resources = append(resources, AccessTokenResource{Resource: definition.Resource, LabelKey: definition.LabelKey, Actions: actions})
	}
	return resources
}

// NormalizeAccessTokenScopes validates a requested grant against the catalog
// the user sees and returns it trimmed, de-duplicated and sorted.
func NormalizeAccessTokenScopes(userID, role int, scopes []string) ([]string, error) {
	normalized, ok := NormalizeAccessTokenScopeList(scopes)
	if !ok {
		return nil, ErrAccessTokenScopeInvalid
	}
	grantable := make(map[string]bool)
	for _, group := range AccessTokenCatalog(userID, role) {
		for _, resource := range group.Resources {
			for _, action := range resource.Actions {
				grantable[resource.Resource+accessTokenScopeSeparator+action.Action] = true
			}
		}
	}
	for _, scope := range normalized {
		if grantable[scope] {
			continue
		}
		if !knownAccessTokenScope(scope) {
			return nil, ErrAccessTokenScopeInvalid
		}
		return nil, ErrAccessTokenScopeForbidden
	}
	return normalized, nil
}

func knownAccessTokenScope(scope string) bool {
	resourceName, action, ok := strings.Cut(scope, accessTokenScopeSeparator)
	if !ok {
		return false
	}
	for _, resource := range accessTokenStaticResources {
		if resource.Resource == resourceName && slices.ContainsFunc(resource.Actions, func(definition authz.ActionDefinition) bool { return definition.Action == action }) {
			return true
		}
	}
	return slices.Contains(authz.AllPermissions(), authz.Permission{Resource: resourceName, Action: action})
}

func AccessTokenScopeGranted(scopes []string, scope string) bool {
	return scope != "" && slices.Contains(scopes, scope)
}

// accessTokenVerificationScopes maps every verification scope to the token
// scope a PAT needs to obtain its proof. An empty value means PATs can never
// obtain it; scopes missing from this table are denied too.
var accessTokenVerificationScopes = map[string]string{
	VerificationScopeAuditSecurityResponseStop:      "",
	VerificationScopeAuditSecuritySettings:          "",
	VerificationScopeAdminChannelCreate:             AccessTokenScopeOf(authz.ChannelSensitiveWrite),
	VerificationScopeAdminChannelChange:             AccessTokenScopeOf(authz.ChannelOperate),
	VerificationScopeAdminChannelCopy:               AccessTokenScopeOf(authz.ChannelSensitiveWrite),
	VerificationScopeAdminChannelDelete:             AccessTokenScopeOf(authz.ChannelSensitiveWrite),
	VerificationScopeChannelKeyRead:                 AccessTokenScopeOf(authz.ChannelSecretView),
	VerificationScopeAdminUserAccessTokensDeleteAll: "", // Access token clearing is session-only.
	VerificationScopeAdminUserAPIKeysDeleteAll:      "", // Emergency API key revocation is session-only.
	VerificationScopeAdminUserCreate:                "user:write",
	VerificationScopeAdminUserUpdate:                "user:write",
	VerificationScopeAdminUserDelete:                "user:write",
	VerificationScopeAdminUserManage:                "user:write",
	VerificationScopeAdminUserPasskeyReset:          "user:write",
	VerificationScopeAdminUserTwoFADisable:          "user:write",
	VerificationScopeAdminUserBindingClear:          "user:write",
	VerificationScopePasskeyRegister:                "account_security:write",
	VerificationScopePasskeyDelete:                  "account_security:write",
	VerificationScopeTwoFASetup:                     "account_security:write",
	VerificationScopeTwoFADisable:                   "account_security:write",
	VerificationScopeTwoFABackupCodes:               "account_security:write",
	VerificationScopePasswordSet:                    "account_security:write",
	VerificationScopePasswordChange:                 "account_security:write",
	VerificationScopeAccountBind:                    "account_security:write",
	VerificationScopeAccountUnbind:                  "account_security:write",
	VerificationScopeAccountDelete:                  "account_security:write",
	VerificationScopeAccessTokenGenerate:            "",
	VerificationScopeAccessTokenUpdate:              "",
	VerificationScopeAccessTokenRevoke:              "",
	VerificationScopeLogin:                          "",
}

// AccessTokenVerificationScope returns the token scope a PAT needs for a
// verification scope and whether the scope is listed at all.
func AccessTokenVerificationScope(verificationScope string) (scope string, listed bool) {
	scope, listed = accessTokenVerificationScopes[verificationScope]
	return scope, listed
}

// requireAccessTokenVerificationScope rejects a step-up request from a PAT
// whose grant does not cover the operation. Browser sessions pass through.
func requireAccessTokenVerificationScope(identity AuthIdentity, verificationScope string) error {
	tokenID, ok := model.ParseAccessTokenSessionID(identity.SessionID)
	if !ok {
		return nil
	}
	required, _ := AccessTokenVerificationScope(verificationScope)
	if required == "" {
		return ErrVerificationForbidden
	}
	token, err := model.GetUserAccessToken(identity.UserID, tokenID)
	if errors.Is(err, model.ErrAccessTokenNotFound) {
		return ErrAuthTokenInvalid
	}
	if err != nil {
		return err
	}
	if !AccessTokenScopeGranted(token.GetScopes(), required) {
		// Changes use their exact action's permission in the authorized binding.
		if verificationScope != VerificationScopeAdminChannelChange {
			return ErrVerificationForbidden
		}
	}
	var user model.User
	if err := model.DB.Select("id", "role", "status").First(&user, identity.UserID).Error; err != nil {
		return err
	}
	if strings.HasPrefix(verificationScope, "admin.channel.") {
		if !AdminTokenLifetimeValid(token) {
			return ErrAdminTokenDenied
		}
		if _, _, err := ReadAdminTokenAuthorization(context.Background(), identity.UserID, authz.ActionChannelsOwned); err != nil {
			return err
		}
	} else if IsManagementScope(required) || (user.Role >= common.RoleAdminUser && AdminTokenPersonalScopeDenied(required)) {
		return ErrVerificationForbidden
	}
	return nil
}
