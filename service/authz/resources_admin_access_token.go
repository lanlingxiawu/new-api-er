package authz

const (
	ResourceAdminAccessToken = "admin_access_token"
	ActionUse                = "use"
	ActionUsersList          = "users_list"
	ActionUsersQuota         = "users_quota"
	ActionUsersGroupRatios   = "users_group_ratios"
	ActionUsersEmployee      = "users_employee"
	ActionGroupsRead         = "groups_read"
	ActionEmployeesRead      = "employees_read"
	ActionUsersStats         = "users_stats"
	ActionTopupsReconcile    = "topups_reconcile"
	ActionChannelsOwned      = "channels_owned"
)

func AdminAccessTokenPermissions() []Permission {
	permissions := make([]Permission, 0, 10)
	for _, action := range []string{ActionUse, ActionUsersList, ActionUsersQuota, ActionUsersGroupRatios, ActionUsersEmployee, ActionGroupsRead, ActionEmployeesRead, ActionUsersStats, ActionTopupsReconcile, ActionChannelsOwned} {
		permissions = append(permissions, Permission{Resource: ResourceAdminAccessToken, Action: action})
	}
	return permissions
}

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceAdminAccessToken, LabelKey: "Administrator token access",
		Actions: []ActionDefinition{
			{Action: ActionUse, LabelKey: "Allow management tokens", DescriptionKey: "Enable only the selected token capabilities. Turning this off blocks all management token requests."},
			{Action: ActionUsersList, LabelKey: "Allow exact user lookup", DescriptionKey: "Look up one user by ID or full username, including balance, usage, assigned employee, and exclusive ratios. Requires the Users menu."},
			{Action: ActionUsersQuota, LabelKey: "Allow balance changes", DescriptionKey: "Add, subtract, or set a user's balance without per-operation verification. Requires the Users menu."},
			{Action: ActionUsersGroupRatios, LabelKey: "Allow exclusive ratio changes", DescriptionKey: "Change only a user's exclusive group ratios without per-operation verification. Requires the Users menu."},
			{Action: ActionUsersEmployee, LabelKey: "Allow employee assignment", DescriptionKey: "Assign, change, or remove a customer's employee without per-operation verification. Requires the Users and Employees menus."},
			{Action: ActionGroupsRead, LabelKey: "Allow group names", DescriptionKey: "View group names for choosing exclusive ratios. Requires the Users menu."},
			{Action: ActionEmployeesRead, LabelKey: "Allow employee selection", DescriptionKey: "View employee IDs, names, and status for assignment. Requires the Users and Employees menus."},
			{Action: ActionUsersStats, LabelKey: "Allow usage statistics", DescriptionKey: "View site-wide or individual user usage statistics for up to 30 days. Requires the Users menu."},
			{Action: ActionTopupsReconcile, LabelKey: "Allow verified top-up reconciliation", DescriptionKey: "Check one order with the payment provider and credit it only after payment is confirmed. Requires the Users menu."},
			{Action: ActionChannelsOwned, LabelKey: "Allow own channels", DescriptionKey: "Allow tokens to access channels created by this administrator, within existing channel permissions. High-risk changes require independent verification."},
		},
	})
}
