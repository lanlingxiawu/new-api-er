package authz

const ResourceAudit = "audit"

var AuditRead = Permission{Resource: ResourceAudit, Action: ActionRead}
var AuditExport = Permission{Resource: ResourceAudit, Action: "export"}

var AuditSecurityRead = Permission{Resource: "audit_security", Action: ActionRead}
var AuditSecurityManage = Permission{Resource: "audit_security", Action: "manage"}
var AuditSecurityExport = Permission{Resource: "audit_security", Action: "export"}
var AuditSecurityDelete = Permission{Resource: "audit_security", Action: "delete"}

func init() {
	RegisterResource(ResourceDefinition{Resource: "audit_security", LabelKey: "Security monitoring", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View security alerts", DescriptionKey: "View security alerts within your audit access."},
		{Action: "manage", LabelKey: "Manage security alerts", DescriptionKey: "Record the investigation status of security alerts."},
		{Action: "export", LabelKey: "Export security alerts", DescriptionKey: "Export visible security alerts and investigation evidence."},
		{Action: "delete", LabelKey: "Delete security alerts", DescriptionKey: "Delete visible alerts and their evidence. Original audit logs are retained."},
	}})
	RegisterResource(ResourceDefinition{
		Resource: ResourceAudit,
		LabelKey: "Audit Logs",
		Actions: []ActionDefinition{{
			Action:         ActionRead,
			LabelKey:       "View other accounts' audit logs",
			DescriptionKey: "View audit records from user and admin roles. Root records are always excluded.",
		}, {
			Action:         "export",
			LabelKey:       "Export audit logs",
			DescriptionKey: "Export audit logs and manage export tasks. Requires audit read access.",
		}},
	})
}
