package auditsecurity

import (
	"context"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// This is the same current ownership boundary as RequireEmployeeCustomerAccess.
// It is used only on the isolated audit read pool, without locking users.
func currentCustomerOwners(db *gorm.DB) *gorm.DB {
	return db.Table("users AS customer").
		Joins("JOIN users AS employee ON employee.id = customer.inviter_id").
		Joins("JOIN employee_profiles AS profile ON profile.user_id = employee.id AND profile.status = ?", 1).
		Where("customer.role = ? AND employee.status = ? AND customer.id <> employee.id", common.RoleCommonUser, common.UserStatusEnabled).
		Where("customer.deleted_at IS NULL AND employee.deleted_at IS NULL").
		Where("NOT EXISTS (SELECT 1 FROM employee_profiles AS cp WHERE cp.user_id = customer.id AND cp.status = ?)", 1)
}

func loadAlertAssignees(ctx context.Context, db *gorm.DB, alerts []model.SecurityAlert) error {
	if len(alerts) == 0 {
		return nil
	}
	db = db.WithContext(ctx)
	ids := make([]int64, len(alerts))
	for i := range alerts {
		ids[i] = alerts[i].ID
		alerts[i].Assignees = []model.SecurityAlertAssignee{}
	}
	var links []model.SecurityAssignee
	if err := db.Where("alert_id IN ?", ids).Order("user_id ASC").Find(&links).Error; err != nil {
		return err
	}
	manual := make(map[int64][]int, len(alerts))
	userIDs := make([]int, 0, len(links))
	for _, link := range links {
		manual[link.AlertID] = append(manual[link.AlertID], link.UserID)
		userIDs = append(userIDs, link.UserID)
	}
	accountIDs := make([]int, 0, len(alerts))
	accountAlerts := make(map[int64]bool, len(alerts))
	for _, alert := range alerts {
		if len(manual[alert.ID]) == 0 && alert.AssigneeID > 0 {
			manual[alert.ID] = []int{alert.AssigneeID}
			userIDs = append(userIDs, alert.AssigneeID)
		}
		allowed, err := customerAlertAccessible(db, alert, alertAccountID(alert))
		if err != nil {
			return err
		}
		if allowed {
			accountAlerts[alert.ID] = true
			accountIDs = append(accountIDs, alertAccountID(alert))
		}
	}
	names := make(map[int]model.User, len(userIDs))
	if len(userIDs) > 0 {
		var users []model.User
		if err := db.Select("id,username,role").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			names[user.Id] = user
		}
	}
	owners := map[int]model.SecurityAlertAssignee{}
	if len(accountIDs) > 0 {
		var rows []struct {
			CustomerID, ID, Role int
			Username             string
		}
		if err := currentCustomerOwners(db).Select("customer.id AS customer_id,employee.id,employee.username,employee.role").Where("customer.id IN ?", accountIDs).Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			owners[row.CustomerID] = model.SecurityAlertAssignee{ID: row.ID, Username: row.Username, Role: row.Role, Source: "owner"}
		}
	}
	for i := range alerts {
		alert := &alerts[i]
		alert.ManualAssigneeIDs = append([]int{}, manual[alert.ID]...)
		owner, hasOwner := owners[alertAccountID(*alert)]
		// The same actor can have both account and management/anonymous alerts.
		if hasOwner {
			if accountAlerts[alert.ID] {
				alert.Assignees = append(alert.Assignees, owner)
			} else {
				hasOwner = false
			}
		}
		for _, id := range manual[alert.ID] {
			if hasOwner && id == owner.ID {
				continue
			}
			user := names[id]
			alert.Assignees = append(alert.Assignees, model.SecurityAlertAssignee{ID: id, Username: user.Username, Role: user.Role, Source: "manual"})
		}
	}
	return nil
}

// SQL counterpart of customerAlertAccessible for bounded list/summary filters.
// Legacy identity requires a complete, matching snapshot set, exactly as detail.
func customerAccountAlertIdentity(db *gorm.DB) *gorm.DB {
	var currentParts, legacyParts []string
	var args []any
	for _, rule := range securityAlertRules {
		version := customerAccountRuleVersion(rule)
		if version == 0 {
			continue
		}
		currentParts = append(currentParts, "(COALESCE(audit_security_alerts.rule,'') = ? AND COALESCE(audit_security_alerts.rule_version,0) = ?)")
		legacyParts = append(legacyParts, "(COALESCE(audit_security_alerts.rule,'') = ? AND COALESCE(audit_security_alerts.rule_version,0) < ?)")
		args = append(args, rule, version)
	}
	// Match Go's zero-value decoding of nullable fields. Every predicate must
	// remain two-valued: NOT(NULL) would hide rows from the unassigned filter.
	role := "(COALESCE(actor_role,0) = 1 OR (COALESCE(rule,'') = 'session_reuse' AND COALESCE(actor_role,0) = 0))"
	current := db.Session(&gorm.Session{NewDB: true}).Where("("+strings.Join(currentParts, " OR ")+")", args...)
	current = current.Where(role)
	legacyParts = append(legacyParts, "(COALESCE(audit_security_alerts.rule,'') = ? AND COALESCE(audit_security_alerts.rule_version,0) >= 1 AND COALESCE(audit_security_alerts.rule_version,0) < ? AND COALESCE(actor_role,0) IN (0,1) AND COALESCE(resource_type,'') IN ('user','account'))")
	args = append(args, "sensitive_operation", customerSensitiveOperationVersion)
	legacy := db.Session(&gorm.Session{NewDB: true}).Where("("+strings.Join(legacyParts, " OR ")+")", args...)
	legacy = legacy.Where("COALESCE(occurrences,0) BETWEEN 1 AND 100 AND COALESCE(latest_finding_id,0) > 0").
		Where("(SELECT COUNT(*) FROM audit_security_findings AS af WHERE af.alert_id = audit_security_alerts.id) = COALESCE(occurrences,0)").
		Where("COALESCE(latest_finding_id,0) = COALESCE((SELECT MAX(af.id) FROM audit_security_findings AS af WHERE af.alert_id = audit_security_alerts.id),0)").
		Where("NOT EXISTS (SELECT 1 FROM audit_security_findings AS af WHERE af.alert_id = audit_security_alerts.id AND (COALESCE(af.actor_id,0) <> COALESCE(audit_security_alerts.actor_id,0) OR COALESCE(af.rule,'') <> COALESCE(audit_security_alerts.rule,'') OR NOT (COALESCE(af.actor_role,0) = 1 OR (af.rule = 'session_reuse' AND COALESCE(af.actor_role,0) = 0))))").
		Where("COALESCE(audit_security_alerts.rule,'') <> ? OR NOT EXISTS (SELECT 1 FROM audit_security_findings AS af WHERE af.alert_id = audit_security_alerts.id AND (af.rule_version IS NULL OR af.rule_version <> audit_security_alerts.rule_version OR af.resource_type IS NULL OR af.resource_type <> audit_security_alerts.resource_type OR af.target_id IS NULL OR (af.target_id <> 0 AND af.target_id <> audit_security_alerts.actor_id)))", "sensitive_operation")
	// These formats belong only to employee views, not personal notifications.
	sensitive := db.Session(&gorm.Session{NewDB: true}).Where("COALESCE(rule,'') IN ?", []string{"sensitive_operation", "bulk_sensitive_operations"}).
		Where("(COALESCE(rule_version,0) = ? AND COALESCE(actor_role,0) = ? AND COALESCE(resource_type,'') IN ('user','account')) OR (COALESCE(rule_version,0) = ? AND COALESCE(actor_role,0) = 0 AND COALESCE(resource_type,'') = 'account')", customerSensitiveOperationVersion, common.RoleCommonUser, customerRecoveryOperationVersion)
	actor := db.Session(&gorm.Session{NewDB: true}).Where("COALESCE(audit_security_alerts.actor_id,0) > 0").Where(db.Session(&gorm.Session{NewDB: true}).Where(current).Or(legacy).Or(sensitive))
	target := db.Session(&gorm.Session{NewDB: true}).Where("COALESCE(actor_id,0) = 0 AND COALESCE(actor_role,0) = 0 AND COALESCE(resource_type,'') = ? AND COALESCE(target_user_id,0) > 0 AND COALESCE(target_role,0) = ? AND COALESCE(rule_version,0) = ? AND COALESCE(rule,'') IN ?", "account", common.RoleCommonUser, 2, []string{"login_failures", "anonymous_network", "ip_abuse"})
	return db.Session(&gorm.Session{NewDB: true}).Where("COALESCE(purge_state,'') = ''").Where(db.Session(&gorm.Session{NewDB: true}).Where(actor).Or(target))
}

func filterAlertAssignee(q *gorm.DB, id int) *gorm.DB {
	db := q.Session(&gorm.Session{NewDB: true})
	owners := currentCustomerOwners(db).Select("1").Where("customer.id = CASE WHEN audit_security_alerts.actor_id > 0 THEN audit_security_alerts.actor_id ELSE audit_security_alerts.target_user_id END")
	if id > 0 {
		owners = owners.Where("employee.id = ?", id)
	}
	owner := customerAccountAlertIdentity(db).Where("EXISTS (?)", owners)
	manual := db.Where("EXISTS (SELECT 1 FROM audit_security_assignees AS assigned WHERE assigned.alert_id = audit_security_alerts.id AND assigned.user_id = ?)", id).
		Or("assignee_id = ? AND NOT EXISTS (SELECT 1 FROM audit_security_assignees AS assigned WHERE assigned.alert_id = audit_security_alerts.id)", id)
	if id == 0 {
		return q.Where("(assignee_id = 0 OR assignee_id IS NULL) AND NOT EXISTS (SELECT 1 FROM audit_security_assignees AS assigned WHERE assigned.alert_id = audit_security_alerts.id)").Not(owner)
	}
	return q.Where(manual.Or(owner))
}

func normalizeManualAssignees(input AlertInvestigation) ([]int, bool, error) {
	if input.AssigneeIDs == nil && input.AssigneeID == nil {
		return nil, false, nil
	}
	if input.AssigneeIDs != nil && input.AssigneeID != nil {
		return nil, false, ErrInvalidAlertAssignee
	}
	var ids []int
	if input.AssigneeIDs != nil {
		ids = slices.Clone(*input.AssigneeIDs)
	} else if *input.AssigneeID != 0 {
		ids = []int{*input.AssigneeID}
	}
	if len(ids) > 10 {
		return nil, false, ErrInvalidAlertAssignee
	}
	slices.Sort(ids)
	for i, id := range ids {
		if id <= 0 || i > 0 && ids[i-1] == id {
			return nil, false, ErrInvalidAlertAssignee
		}
	}
	return ids, true, nil
}
