package auditsecurity

import (
	"cmp"
	"context"
	"slices"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// These versions isolate authenticated account-and-role episodes. Trusted
// failed-login targets have a separate contract in loginTargetRuleVersion.
func customerAccountRuleVersion(rule string) int {
	switch rule {
	case "multi_account_key_access", "multi_source_key_access":
		return 1
	case "risky_ip_login", "risky_ip_sensitive_operation", "new_source_sensitive_operation":
		return 2
	case "login_after_failures", "new_login_source", "permission_failures", "impossible_travel":
		return 3
	case "session_reuse", "anonymous_network", "ip_abuse":
		return 2
	default:
		return 0
	}
}

// Only ordinary accounts' own operations use this employee-visible format.
// Keep it separate from the rules shared by self views and personal notices.
const customerSensitiveOperationVersion = 4
const customerRecoveryOperationVersion = 5

func customerSensitiveEventVersion(event model.SecurityEvent) int {
	if event.ActorID <= 0 || (event.ResourceType != "user" && event.ResourceType != "account") ||
		(event.TargetID != 0 && event.TargetID != event.ActorID) {
		return 0
	}
	recovery := event.Action == "auth.password_reset_committed" || event.Action == "auth.password_reset_cleanup_failed"
	if !recovery && event.ActorRole == common.RoleCommonUser {
		return customerSensitiveOperationVersion
	}
	// Recovery verifies the affected account, not an authenticated operator.
	// Normalize accepts Identity only from the server-owned audit envelope.
	if !recovery || event.ActorRole != 0 || event.ResourceType != "account" || !event.Success {
		return 0
	}
	var facts Facts
	if common.UnmarshalJsonStr(event.Facts, &facts) != nil || facts.EvidenceIncomplete || facts.Identity == nil {
		return 0
	}
	identity := facts.Identity
	if identity.ActorID != event.ActorID || identity.Role != common.RoleCommonUser || identity.AuthMethod != "recovery" ||
		identity.AuthVersion <= 0 || identity.EffectiveVersion <= identity.AuthVersion {
		return 0
	}
	return customerRecoveryOperationVersion
}

func customerSensitiveAlertAccessible(db *gorm.DB, alert model.SecurityAlert, customerID int) (bool, error) {
	if customerID <= 0 || alert.ActorID != customerID || alert.PurgeState != "" ||
		(alert.ResourceType != "user" && alert.ResourceType != "account") {
		return false, nil
	}
	if alert.RuleVersion == customerSensitiveOperationVersion {
		return alert.ActorRole == common.RoleCommonUser, nil
	}
	if alert.RuleVersion == customerRecoveryOperationVersion {
		return alert.ActorRole == 0 && alert.ResourceType == "account", nil
	}
	// Old bulk snapshots identify only the trigger, not every counted event.
	// Their complete evidence must be verified by explicit maintenance first.
	if alert.Rule == "bulk_sensitive_operations" {
		return false, nil
	}
	if alert.RuleVersion < 1 || alert.RuleVersion >= customerSensitiveOperationVersion ||
		(alert.ActorRole != 0 && alert.ActorRole != common.RoleCommonUser) ||
		alert.Occurrences < 1 || alert.Occurrences > 100 || alert.LatestFindingID <= 0 {
		return false, nil
	}
	// A NULL target is incomplete proof, unlike an explicitly absent target (0).
	var findings []struct {
		ID           int64
		ActorID      int
		ActorRole    int
		Rule         string
		RuleVersion  int
		ResourceType string
		TargetID     *int
	}
	if err := db.Model(&model.SecurityFindingSnapshot{}).
		Select("id,actor_id,actor_role,rule,rule_version,resource_type,target_id").
		Where("alert_id = ?", alert.ID).Order("id DESC").Limit(101).Find(&findings).Error; err != nil {
		return false, err
	}
	if int64(len(findings)) != alert.Occurrences || findings[0].ID != alert.LatestFindingID {
		return false, nil
	}
	for _, finding := range findings {
		if finding.ActorID != customerID || finding.ActorRole != common.RoleCommonUser ||
			finding.Rule != alert.Rule || finding.RuleVersion != alert.RuleVersion || finding.ResourceType != alert.ResourceType ||
			finding.TargetID == nil || (*finding.TargetID != 0 && *finding.TargetID != customerID) {
			return false, nil
		}
	}
	return true, nil
}

func customerRiskRoleAllowed(rule string, role int) bool {
	// Refresh reuse is emitted only after matching a known previous refresh
	// secret (model.RotateUserSessionRefresh). Its UserId identifies the affected
	// account, while the attacker's role correctly remains unknown in raw audit.
	return role == common.RoleCommonUser || rule == "session_reuse" && role == 0
}

type CustomerSecurityAlert struct {
	Responses    []CustomerSecurityResponse `json:"responses,omitempty"`
	Response     *CustomerSecurityResponse  `json:"response,omitempty"`
	ID           int64                      `json:"id"`
	Rule         string                     `json:"rule"`
	Severity     int                        `json:"severity"`
	Status       string                     `json:"status"`
	FirstSeen    int64                      `json:"first_seen"`
	LastSeen     int64                      `json:"last_seen"`
	UpdatedAt    int64                      `json:"updated_at"`
	Occurrences  int64                      `json:"occurrences"`
	MatchedCount int                        `json:"matched_count"`
	Version      int64                      `json:"version"`
	Conclusion   string                     `json:"conclusion"`
}

func projectCustomerAlert(a model.SecurityAlert) CustomerSecurityAlert {
	result := CustomerSecurityAlert{ID: a.ID, Rule: a.Rule, Severity: a.Severity, Status: a.Status, FirstSeen: a.FirstSeen, LastSeen: a.LastSeen, UpdatedAt: a.UpdatedAt, Occurrences: a.Occurrences, MatchedCount: a.MatchedCount, Version: a.Version, Conclusion: a.Conclusion}
	if a.Response != nil {
		response := projectCustomerResponse(*a.Response)
		result.Response = &response
	}
	for _, response := range a.Responses {
		result.Responses = append(result.Responses, projectCustomerResponse(response))
	}
	return result
}

type CustomerSecurityResponse struct {
	ID            int64  `json:"id"`
	Action        string `json:"action"`
	Source        string `json:"source"`
	Outcome       string `json:"outcome"`
	Reason        string `json:"reason,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	AffectedCount *int64 `json:"affected_count,omitempty"`
}

func projectCustomerResponse(response model.SecurityAccountResponse) CustomerSecurityResponse {
	result := CustomerSecurityResponse{ID: response.ID, CreatedAt: response.CreatedAt, AffectedCount: response.AffectedCount, Source: "manual", Outcome: "unknown"}
	if response.Source == "automatic" {
		result.Source = "automatic"
	}
	if slices.Contains([]string{"disable_account", "clear_api_keys", "clear_access_tokens"}, response.Action) {
		result.Action = response.Action
	}
	if slices.Contains([]string{"pending", "committed", "clearing", "succeeded", "failed", "skipped", "sync_pending", "partial", "unknown", "superseded"}, response.Outcome) {
		result.Outcome = response.Outcome
	}
	switch response.ReasonCode {
	case "", "stopped", "superseded", "already_processed", "already_disabled":
		result.Reason = response.ReasonCode
	case "disabled", "excluded_account", "protected_account":
		result.Reason = "not_applicable"
	case "expired", "incomplete_evidence", "forbidden", "invalid_target", "sync_failed":
		result.Reason = "review_required"
	default:
		result.Reason = "unknown"
	}
	return result
}

const customerAlertColumns = "id,rule,actor_id,actor_role,target_user_id,target_role,resource_type,rule_version,severity,status,first_seen,last_seen,updated_at,occurrences,matched_count,version,conclusion,latest_finding_id,purge_state"

func customerAlertAccessible(db *gorm.DB, a model.SecurityAlert, customerID int) (bool, error) {
	return accountAlertAccessible(db, a, customerID, false)
}

// Self views may show the owner's authenticated account events from any known
// role. Employee views must retain their ordinary-customer role boundary.
func accountRiskRoleAllowed(rule string, role int, selfView bool) bool {
	return customerRiskRoleAllowed(rule, role) || selfView && (role == common.RoleAdminUser || role == common.RoleRootUser)
}

func accountAlertAccessible(db *gorm.DB, a model.SecurityAlert, accountID int, selfView bool) (bool, error) {
	if a.Rule == "sensitive_operation" || a.Rule == "bulk_sensitive_operations" {
		if selfView {
			return false, nil
		}
		return customerSensitiveAlertAccessible(db, a, accountID)
	}
	if target := alertLoginTarget(a); target != nil {
		return accountID > 0 && target.UserID == accountID && a.PurgeState == "" && accountRiskRoleAllowed(a.Rule, target.Role, selfView), nil
	}
	version := customerAccountRuleVersion(a.Rule)
	if accountID <= 0 || a.ActorID != accountID || version == 0 || a.PurgeState != "" {
		return false, nil
	}
	if a.RuleVersion == version && accountRiskRoleAllowed(a.Rule, a.ActorRole, selfView) {
		return true, nil
	}
	// Legacy identity is proved only from a complete, bounded set of snapshots.
	// Missing snapshots or a different actor/role fail closed.
	if a.RuleVersion >= version || a.Occurrences < 1 || a.Occurrences > 100 || a.LatestFindingID <= 0 {
		return false, nil
	}
	var findings []model.SecurityFindingSnapshot
	if err := db.Select("id,actor_id,actor_role,rule").Where("alert_id = ?", a.ID).Order("id DESC").Limit(101).Find(&findings).Error; err != nil {
		return false, err
	}
	if int64(len(findings)) != a.Occurrences || findings[0].ID != a.LatestFindingID {
		return false, nil
	}
	for _, finding := range findings {
		if finding.ActorID != accountID || !accountRiskRoleAllowed(a.Rule, finding.ActorRole, selfView) || finding.Rule != a.Rule {
			return false, nil
		}
	}
	return true, nil
}

func customerAlert(db *gorm.DB, customerID int, id int64) (model.SecurityAlert, error) {
	var alert model.SecurityAlert
	err := db.Select(customerAlertColumns+",evidence,assignee_id").Where("id = ? AND (purge_state = ? OR purge_state IS NULL)", id, "").Take(&alert).Error
	if err != nil {
		return alert, err
	}
	allowed, err := customerAlertAccessible(db, alert, customerID)
	if err != nil {
		return alert, err
	}
	if !allowed {
		return alert, gorm.ErrRecordNotFound
	}
	return alert, nil
}

type CustomerAlertPage struct {
	Items      []CustomerSecurityAlert `json:"items"`
	NextCursor string                  `json:"next_cursor"`
}

func ListCustomerAlerts(ctx context.Context, db *gorm.DB, f model.CustomerAuditFilter) (CustomerAlertPage, error) {
	page := CustomerAlertPage{Items: []CustomerSecurityAlert{}}
	if err := model.RequireEmployeeCustomerAccess(ctx, db, f.EmployeeID, f.CustomerID); err != nil {
		return page, err
	}
	position, err := f.DecodeCursor("alerts")
	if err != nil {
		return page, err
	}
	rows, err := queryCustomerAlertRecords(ctx, db, []int{f.CustomerID}, f.Start, f.End, position, f.View)
	if err != nil {
		return page, err
	}
	for _, row := range rows {
		page.Items = append(page.Items, projectCustomerAlert(row))
	}
	if len(page.Items) > model.CustomerAuditPageSize {
		page.Items = page.Items[:model.CustomerAuditPageSize]
		last := page.Items[len(page.Items)-1]
		position.At, position.ID = last.LastSeen, last.ID
		page.NextCursor, err = position.Encode()
	}
	responseAlerts := make([]model.SecurityAlert, len(page.Items))
	for i := range page.Items {
		responseAlerts[i] = rows[i]
	}
	if err := loadAlertResponses(ctx, db, responseAlerts); err != nil {
		return page, err
	}
	for i := range page.Items {
		projected := projectCustomerAlert(responseAlerts[i])
		page.Items[i].Response, page.Items[i].Responses = projected.Response, projected.Responses
	}
	return page, err
}

// Fill pages after identity checks. Callers have authorized the customer IDs;
// every row still needs its own historical account/role evidence check.
func queryCustomerAlertRecords(ctx context.Context, db *gorm.DB, customerIDs []int, start, end int64, position model.CustomerAuditPosition, view string) ([]model.SecurityAlert, error) {
	return queryAccountAlertRecords(ctx, db, customerIDs, start, end, position, false, view)
}

// Each identity lane walks its covering index independently. Filtering never
// consumes the caller's page, and merging preserves the shared keyset cursor.
func queryAccountAlertRecords(ctx context.Context, db *gorm.DB, accountIDs []int, start, end int64, position model.CustomerAuditPosition, selfView bool, view string) ([]model.SecurityAlert, error) {
	items := []model.SecurityAlert{}
	if len(accountIDs) == 0 {
		return items, nil
	}
	db = db.WithContext(ctx)
	for _, column := range []string{"actor_id", "target_user_id"} {
		cursor := position
		accepted := 0
		for accepted <= model.CustomerAuditPageSize {
			query := db.Model(&model.SecurityAlert{}).Select("id,last_seen").Where(column+" IN ? AND last_seen >= ? AND last_seen <= ? AND (purge_state = ? OR purge_state IS NULL)", accountIDs, start, end, "")
			if view == "handled" {
				query = query.Where("status IN ?", []string{"resolved", "false_positive"})
			}
			if cursor.ID > 0 {
				query = query.Where("last_seen < ? OR (last_seen = ? AND id < ?)", cursor.At, cursor.At, cursor.ID)
			}
			var candidates []model.SecurityAlert
			if err := query.Order("last_seen DESC").Order("id DESC").Limit(model.CustomerAuditPageSize + 1).Find(&candidates).Error; err != nil {
				return nil, err
			}
			if len(candidates) == 0 {
				break
			}
			ids := make([]int64, len(candidates))
			for i, row := range candidates {
				ids[i] = row.ID
			}
			var rows []model.SecurityAlert
			if err := db.Select(customerAlertColumns).Where("id IN ?", ids).Order("last_seen DESC").Order("id DESC").Find(&rows).Error; err != nil {
				return nil, err
			}
			for _, row := range rows {
				if view == "handled" && row.Status != "resolved" && row.Status != "false_positive" {
					continue
				}
				accountID := alertAccountID(row)
				if !slices.Contains(accountIDs, accountID) || row.LastSeen < start || row.LastSeen > end || position.ID > 0 && (row.LastSeen > position.At || row.LastSeen == position.At && row.ID >= position.ID) {
					continue
				}
				allowed, err := accountAlertAccessible(db, row, accountID, selfView)
				if err != nil {
					return nil, err
				}
				if allowed {
					items = append(items, row)
					accepted++
					if accepted > model.CustomerAuditPageSize {
						break
					}
				}
			}
			if len(candidates) < model.CustomerAuditPageSize+1 {
				break
			}
			last := candidates[len(candidates)-1]
			endAt, endID := last.LastSeen, last.ID
			cursor.At, cursor.ID = endAt, endID
		}
	}
	slices.SortFunc(items, func(a, b model.SecurityAlert) int {
		if order := cmp.Compare(b.LastSeen, a.LastSeen); order != 0 {
			return order
		}
		return cmp.Compare(b.ID, a.ID)
	})
	items = slices.CompactFunc(items, func(a, b model.SecurityAlert) bool { return a.ID == b.ID })
	return items[:min(len(items), model.CustomerAuditPageSize+1)], nil
}

type CustomerSecurityEvent struct {
	EventID   string `json:"event_id"`
	Action    string `json:"action"`
	At        int64  `json:"at"`
	IP        string `json:"ip"`
	RequestID string `json:"request_id"`
	Success   bool   `json:"success"`
}

type CustomerSecurityAction struct {
	AffectedCount *int64 `json:"affected_count,omitempty"`
	Action        string `json:"action,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	Reason        string `json:"reason,omitempty"`
	ID            int64  `json:"id"`
	Source        string `json:"source"`
	FromStatus    string `json:"from_status"`
	ToStatus      string `json:"to_status"`
	Conclusion    string `json:"conclusion"`
	Note          string `json:"note,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}

type CustomerSecurityDetail struct {
	Alert   CustomerSecurityAlert    `json:"alert"`
	Events  []CustomerSecurityEvent  `json:"events"`
	Actions []CustomerSecurityAction `json:"actions"`
}

func GetCustomerAlert(ctx context.Context, db *gorm.DB, employeeID, customerID int, id int64) (CustomerSecurityDetail, error) {
	detail := CustomerSecurityDetail{Events: []CustomerSecurityEvent{}, Actions: []CustomerSecurityAction{}}
	if err := model.RequireEmployeeCustomerAccess(ctx, db, employeeID, customerID); err != nil {
		return detail, err
	}
	db = db.WithContext(ctx)
	alert, err := customerAlert(db, customerID, id)
	if err != nil {
		return detail, err
	}
	detail.Alert = projectCustomerAlert(alert)
	responseAlerts := []model.SecurityAlert{alert}
	if err := loadAlertResponses(ctx, db, responseAlerts); err != nil {
		return detail, err
	}
	projected := projectCustomerAlert(responseAlerts[0])
	detail.Alert.Response, detail.Alert.Responses = projected.Response, projected.Responses
	var events []model.SecurityEvent
	if alert.Evidence != "" {
		if err := common.UnmarshalJsonStr(alert.Evidence, &events); err != nil {
			return detail, err
		}
	}
	for _, event := range collapseEvidenceEvents(events) {
		if accountEventAccessible(alert, event, customerID, false) {
			detail.Events = append(detail.Events, CustomerSecurityEvent{EventID: event.EventID, Action: event.Action, At: event.At, IP: event.IP, RequestID: event.RequestID, Success: event.Success})
		}
		if len(detail.Events) == 20 {
			break
		}
	}
	var actions []model.SecurityAction
	if err := db.Select("id,source,from_status,to_status,conclusion,note,created_at,action,outcome,reason_code,target_user_id,result").Where("alert_id = ?", id).Order("created_at DESC").Order("id DESC").Limit(100).Find(&actions).Error; err != nil {
		return detail, err
	}
	for _, action := range actions {
		item := CustomerSecurityAction{ID: action.ID, Source: "system", FromStatus: action.FromStatus, ToStatus: action.ToStatus, Conclusion: action.Conclusion, CreatedAt: action.CreatedAt}
		if (action.Action == "disable_account" || action.Action == "clear_api_keys" || action.Action == "clear_access_tokens") && action.TargetUserID == customerID {
			response := projectCustomerResponse(action.ResponseSummary())
			item.Action, item.Outcome, item.Reason = response.Action, response.Outcome, response.Reason
			item.AffectedCount = response.AffectedCount
		}
		if action.Source == "employee" {
			item.Source, item.Note = "employee", action.Note
		} else if action.Source == "human" {
			item.Source = "admin"
		}
		detail.Actions = append(detail.Actions, item)
	}
	return detail, nil
}

func UpdateCustomerAlert(ctx context.Context, db *gorm.DB, role, employeeID, customerID int, id int64, input AlertInvestigation) (CustomerSecurityAlert, error) {
	if customerID <= 0 || id <= 0 || input.Version < 1 || input.AssigneeID != nil || input.AssigneeIDs != nil || utf8.RuneCountInString(input.Note) > 1000 {
		return CustomerSecurityAlert{}, model.ErrCustomerAuditParams
	}
	switch input.Status {
	case "", "open", "investigating", "resolved", "false_positive":
	default:
		return CustomerSecurityAlert{}, model.ErrCustomerAuditParams
	}
	if input.Conclusion != nil {
		switch *input.Conclusion {
		case "", "authorized", "expected", "malicious", "false_positive", "other":
		default:
			return CustomerSecurityAlert{}, model.ErrCustomerAuditParams
		}
	}
	input.customerID = customerID
	alert, err := UpdateAlertInvestigation(ctx, db, role, employeeID, input, id)
	return projectCustomerAlert(alert), err
}
