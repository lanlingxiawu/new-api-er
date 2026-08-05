package authz

const (
	AdminMenuResourcePrefix = "admin_menu."

	ResourceAdminMenuChannels         = AdminMenuResourcePrefix + "channels"
	ResourceAdminMenuModels           = AdminMenuResourcePrefix + "models"
	ResourceAdminMenuUsers            = AdminMenuResourcePrefix + "users"
	ResourceAdminMenuRedemptionCodes  = AdminMenuResourcePrefix + "redemption_codes"
	ResourceAdminMenuSubscriptions    = AdminMenuResourcePrefix + "subscriptions"
	ResourceAdminMenuEmployees        = AdminMenuResourcePrefix + "employees"
	ResourceAdminMenuBusinessOverview = AdminMenuResourcePrefix + "business_overview"
	ResourceAdminMenuRequestLogs      = AdminMenuResourcePrefix + "request_logs"
	ResourceAdminMenuSystemInfo       = AdminMenuResourcePrefix + "system_info"
)

var (
	AdminMenuChannelsView         = Permission{Resource: ResourceAdminMenuChannels, Action: ActionView}
	AdminMenuModelsView           = Permission{Resource: ResourceAdminMenuModels, Action: ActionView}
	AdminMenuUsersView            = Permission{Resource: ResourceAdminMenuUsers, Action: ActionView}
	AdminMenuRedemptionCodesView  = Permission{Resource: ResourceAdminMenuRedemptionCodes, Action: ActionView}
	AdminMenuSubscriptionsView    = Permission{Resource: ResourceAdminMenuSubscriptions, Action: ActionView}
	AdminMenuEmployeesView        = Permission{Resource: ResourceAdminMenuEmployees, Action: ActionView}
	AdminMenuBusinessOverviewView = Permission{Resource: ResourceAdminMenuBusinessOverview, Action: ActionView}
	AdminMenuRequestLogsView      = Permission{Resource: ResourceAdminMenuRequestLogs, Action: ActionView}
	AdminMenuSystemInfoView       = Permission{Resource: ResourceAdminMenuSystemInfo, Action: ActionView}
)

var adminMenuDefinitions = []struct {
	Resource string
	LabelKey string
	Sort     int
	// DefaultRoles lists the built-in roles whose baseline grants include this
	// resource's view action. nil means no built-in role other than the
	// superuser role gets it by default — it must be granted per user.
	DefaultRoles []string
}{
	{Resource: ResourceAdminMenuChannels, LabelKey: "Channels", Sort: 1, DefaultRoles: []string{BuiltInRoleAdmin}},
	{Resource: ResourceAdminMenuModels, LabelKey: "Models", Sort: 2, DefaultRoles: []string{BuiltInRoleAdmin}},
	{Resource: ResourceAdminMenuUsers, LabelKey: "Users", Sort: 3, DefaultRoles: []string{BuiltInRoleAdmin}},
	{Resource: ResourceAdminMenuRedemptionCodes, LabelKey: "Redemption Codes", Sort: 4, DefaultRoles: []string{BuiltInRoleAdmin}},
	{Resource: ResourceAdminMenuSubscriptions, LabelKey: "Subscriptions", Sort: 5, DefaultRoles: []string{BuiltInRoleAdmin}},
	{Resource: ResourceAdminMenuEmployees, LabelKey: "Employee Management", Sort: 6, DefaultRoles: []string{BuiltInRoleAdmin}},
	{Resource: ResourceAdminMenuBusinessOverview, LabelKey: "Business Overview", Sort: 7, DefaultRoles: []string{BuiltInRoleAdmin}},
	// Request logs can contain full upstream request/response bodies and headers,
	// and system info exposes node topology — both stay off by default and must
	// be granted to a specific administrator by root.
	{Resource: ResourceAdminMenuRequestLogs, LabelKey: "Request Logs", Sort: 8, DefaultRoles: nil},
	{Resource: ResourceAdminMenuSystemInfo, LabelKey: "System Info", Sort: 9, DefaultRoles: nil},
}

func init() {
	for _, definition := range adminMenuDefinitions {
		RegisterResource(ResourceDefinition{
			Resource:      definition.Resource,
			LabelKey:      definition.LabelKey,
			Group:         "admin-menu",
			GroupLabelKey: "Menu access",
			Sort:          definition.Sort,
			Actions: []ActionDefinition{
				{
					Action:         ActionView,
					LabelKey:       "Visible and accessible",
					DescriptionKey: "Show this menu and allow access to its pages and APIs.",
					DefaultRoles:   definition.DefaultRoles,
				},
			},
		})
	}
}

func AdminMenuResources() []string {
	result := make([]string, 0, len(adminMenuDefinitions))
	for _, definition := range adminMenuDefinitions {
		result = append(result, definition.Resource)
	}
	return result
}
