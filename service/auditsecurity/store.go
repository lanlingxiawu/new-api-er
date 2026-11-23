package auditsecurity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

var errAlertVersionConflict = errors.New("audit alert version changed during scan")

// Apply commits receipts, findings and checkpoint together. The lease row is
// updated first, so an expired worker cannot commit after a new leader starts.
func Apply(ctx context.Context, db *gorm.DB, state model.SecurityState, events []model.SecurityEvent, next model.SecurityState, cfg operation_setting.AuditSecuritySetting) error {
	if len(events) == 0 {
		return commitSecurityBatch(ctx, db, state, events, next, cfg)
	}
	for start := 0; start < len(events); start += 20 {
		end := min(start+20, len(events))
		checkpoint := next
		if end < len(events) && (next.CursorAt != state.CursorAt || next.CursorEvent != state.CursorEvent) {
			checkpoint = state
			checkpoint.CursorAt = events[end-1].At
			checkpoint.CursorEvent = events[end-1].EventID
		}
		if err := commitSecurityBatch(ctx, db, state, events[start:end], checkpoint, cfg); err != nil {
			return err
		}
	}
	return nil
}

func commitSecurityBatch(ctx context.Context, db *gorm.DB, state model.SecurityState, events []model.SecurityEvent, next model.SecurityState, cfg operation_setting.AuditSecuritySetting) error {
	var candidates []model.SecurityEvent
	var ids []string
	highRiskEvents := make(map[string]bool)
	for _, e := range events {
		var facts Facts
		_ = common.UnmarshalJsonStr(e.Facts, &facts)
		high := highRisk(e, facts) > 0
		authentication := e.Action == "login" && e.Success || e.Action == "auth.login_failed" && (e.Reason == "invalid_credentials" || e.Reason == "invalid_code") || e.Action == "auth.recovery_requested" || e.Action == "auth.access_denied" || e.Action == "auth.session_refresh_reuse" || strings.HasPrefix(e.Action, "auth.password_reset_")
		if high || authentication || successfulSensitiveOperation(e, facts) || cfg.RelayIPEnabled && e.Action == "relay.ip_observed" && ipIntelligenceCandidate(e) {
			candidates = append(candidates, e)
			ids = append(ids, e.EventID)
			highRiskEvents[e.EventID] = high
		}
	}
	for range 3 {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var pending []findingWork
			var lateLogins []model.SecurityEvent
			riskChanged := false
			activityChanged := false
			if err := model.TouchAuditSecurityLease(tx, state); err != nil {
				return err
			}
			var processed []model.SecurityEvent
			if len(ids) > 0 {
				if err := tx.Select(model.SecurityEventColumns).Where("event_id IN ?", ids).Find(&processed).Error; err != nil {
					return err
				}
			}
			seen := make(map[string]model.SecurityEvent, len(processed))
			for _, event := range processed {
				seen[event.EventID] = event
			}
			for _, e := range candidates {
				previous, alreadyProcessed := seen[e.EventID]
				if alreadyProcessed {
					if previous.IPCheckState != "pending" || e.IPCheckState == "" || (e.IPCheckState == "pending" && e.IPNextAttemptAt <= previous.IPNextAttemptAt) {
						continue
					}
					// The receipt owns the original facts. Only enrichment and its state
					// may change; overlapping source rows cannot rewrite the base event.
					var original, enriched Facts
					if err := common.UnmarshalJsonStr(previous.Facts, &original); err != nil && previous.Facts != "" {
						return err
					}
					if err := common.UnmarshalJsonStr(e.Facts, &enriched); err != nil && e.Facts != "" {
						return err
					}
					original.IPIntelligence = enriched.IPIntelligence
					raw, err := common.Marshal(original)
					if err != nil {
						return err
					}
					previous.Facts, previous.IPCheckState, previous.IPNextAttemptAt = string(raw), e.IPCheckState, e.IPNextAttemptAt
					e = previous
				}
				var history []model.SecurityEvent
				var findings []model.SecurityFinding
				historyTruncated := false
				if !alreadyProcessed && e.Action != "relay.ip_observed" {
					high := highRiskEvents[e.EventID]
					var err error
					history, historyTruncated, err = loadCorrelationHistory(tx, e, high)
					if err != nil {
						return err
					}
					findings = Evaluate(e, history, cfg)
					compromiseHistory, compromiseTruncated, err := loadCompromiseHistory(tx, e)
					if err != nil {
						return err
					}
					var facts Facts
					_ = common.UnmarshalJsonStr(e.Facts, &facts)
					facts.FirstObservedAt = time.Now().Unix()
					if state.Name == "scanner" {
						facts.LivePolicyAt = cfg.AutoBanEffectiveAt
					}
					if high || successfulSensitiveOperation(e, facts) {
						facts.SourceBaseline, err = observeManagementSource(tx, e)
						if err != nil {
							return err
						}
					}
					facts.NewManagementSource = facts.SourceBaseline != nil
					raw, err := common.Marshal(facts)
					if err != nil {
						return err
					}
					e.Facts = string(raw)
					compromiseFindings := EvaluateAccountCompromise(e, compromiseHistory, cfg, facts.NewManagementSource)
					for i := range compromiseFindings {
						compromiseFindings[i].Truncated = compromiseTruncated
					}
					findings = append(findings, compromiseFindings...)
					if e.Action == "login" && e.Success && e.ActorID > 0 && e.SourceRef != "" {
						var baseline model.SecurityBaseline
						err := tx.Where("account_id = ? AND source_ref = ?", e.ActorID, e.SourceRef).Take(&baseline).Error
						if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
							return err
						}
						if baseline.ID == 0 {
							var established model.SecurityBaseline
							if err = tx.Select("id,first_seen,last_seen").Where("account_id = ? AND (kind = ? OR kind IS NULL) AND first_seen <= ? AND last_seen >= ?", e.ActorID, "", e.At-86400, e.At-30*86400).Limit(1).Find(&established).Error; err != nil {
								return err
							}
							if established.ID > 0 {
								findings = append(findings, model.SecurityFinding{Rule: "new_login_source", SubjectRef: e.SubjectRef, Severity: 1, Count: 1, Dimensions: model.SecurityDimensions{
									"new_source": 1, "baseline": 1, "new_window_seconds": 86400,
									"source_first_seen": int(e.At), "baseline_first_seen": int(established.FirstSeen), "baseline_last_seen": int(established.LastSeen),
								}})
							}
							baseline = model.SecurityBaseline{AccountID: e.ActorID, SourceRef: e.SourceRef, FirstSeen: e.At, LastSeen: e.At}
							if err = tx.Create(&baseline).Error; err != nil {
								return err
							}
						} else if err = tx.Model(&baseline).Updates(map[string]any{"first_seen": min(baseline.FirstSeen, e.At), "last_seen": max(baseline.LastSeen, e.At)}).Error; err != nil {
							return err
						}
					}
				}
				if cfg.IPIntelligenceEnabled && ipIntelligenceCandidate(e) && e.IPCheckState == "" {
					var facts Facts
					_ = common.UnmarshalJsonStr(e.Facts, &facts)
					switch {
					case facts.IPIntelligence != nil && len(facts.IPIntelligence.Providers) > 0:
						e.IPCheckState = "done"
					case e.At < time.Now().Unix()-86400:
						e.IPCheckState = "skipped"
					default:
						e.IPCheckState, e.IPNextAttemptAt = "pending", time.Now().Unix()+60
					}
				}
				ipFindings, err := evaluateIPIntelligenceFindings(tx, e, cfg)
				if err != nil {
					return err
				}
				findings = append(findings, ipFindings...)
				var facts Facts
				_ = common.UnmarshalJsonStr(e.Facts, &facts)
				findings = append(findings, evaluateSensitiveFindings(e, cfg, facts.NewManagementSource, time.Now().Unix())...)
				completeFindingMetadata(findings, e, history, cfg)
				detectedAt := time.Now().Unix()
				if alreadyProcessed {
					r := tx.Model(&model.SecurityEvent{}).Where("event_id = ? AND ip_check_state = ?", e.EventID, "pending").Updates(map[string]any{"facts": e.Facts, "ip_check_state": e.IPCheckState, "ip_next_attempt_at": e.IPNextAttemptAt})
					if r.Error != nil {
						return r.Error
					}
					if r.RowsAffected != 1 {
						continue
					}
				} else {
					e.ProcessedAt = detectedAt
					if err := tx.Create(&e).Error; err != nil {
						return err
					}
				}
				seen[e.EventID] = e
				for _, f := range findings {
					pending = append(pending, newFindingWork(e, f, history, historyTruncated))
				}
				if !alreadyProcessed && e.Action == "login" && e.Success {
					lateLogins = append(lateLogins, e)
				}
			}
			// All event receipts are durable in this transaction before late
			// correlation; alert writes start only after group locks are held.
			late, err := lateLoginFindings(tx, lateLogins, cfg)
			if err != nil {
				return err
			}
			pending = append(pending, late...)
			slices.SortStableFunc(pending, func(a, b findingWork) int {
				if a.event.ActorID < b.event.ActorID {
					return -1
				}
				if a.event.ActorID > b.event.ActorID {
					return 1
				}
				if a.event.At < b.event.At {
					return -1
				}
				if a.event.At > b.event.At {
					return 1
				}
				return strings.Compare(a.event.EventID, b.event.EventID)
			})
			rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
			if err != nil {
				return err
			}
			for i := range pending {
				work := &pending[i]
				var recorded int64
				if err := tx.Model(&model.SecurityFindingSnapshot{}).Where("detection_key = ?", findingDetectionKey(work.finding, work.event)).Count(&recorded).Error; err != nil {
					return err
				}
				if recorded > 0 {
					continue
				}
				if !cfg.Enabled || cfg.Suspended || !cfg.AutoBanEnabled {
					break
				}
				selected := false
				for _, rule := range rules {
					if rule.ID == work.finding.Rule && rule.HasActions() {
						selected = true
					}
				}
				if !selected {
					continue
				}
				facts, reason := automaticResponseEligibility(work.event, work.finding, cfg, time.Now().Unix(), work.truncated)
				if reason != "" {
					continue
				}
				work.group, err = model.ClaimSecurityResponseGroup(tx, work.event, cfg.AutoBanEffectiveAt, *facts.Identity, time.Now().Unix())
				if err != nil {
					return err
				}
			}
			for _, work := range pending {
				e, f, history, historyTruncated := work.event, work.finding, work.history, work.truncated
				detectedAt := time.Now().Unix()
				key := findingDetectionKey(f, e)
				var count int64
				if err := tx.Model(&model.SecurityFindingSnapshot{}).Where("detection_key = ?", key).Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					continue
				}
				var a model.SecurityAlert
				ruleVersion := alertRuleVersion(f.Rule, e)
				target := eventLoginTarget(e)
				if loginTargetRuleVersion(f.Rule) == 0 {
					target = nil
				}
				repeatKey := ""
				if !historyTruncated {
					repeatKey = repeatOperationKey(e, f)
				}
				var err error
				if repeatKey != "" {
					// Fetch only the ID through the covering index before loading evidence.
					err = tx.Select("id").Where("repeat_key = ? AND repeat_until > ? AND repeat_until <= ? AND (purge_state = ? OR purge_state IS NULL) AND archived_at IS NULL", repeatKey, e.At, e.At+86400, "").Order("repeat_until DESC").Take(&a).Error
					if err == nil {
						err = tx.First(&a, a.ID).Error
						if errors.Is(err, gorm.ErrRecordNotFound) {
							return errAlertVersionConflict
						}
						if err == nil && (a.ArchivedAt != nil || a.PurgeState != "" || a.RepeatKey == nil || *a.RepeatKey != repeatKey || a.RepeatUntil == nil || *a.RepeatUntil <= e.At || *a.RepeatUntil > e.At+86400) {
							// A concurrent archive/cleanup may commit between the indexed
							// ID lookup and the row read. Re-select an eligible episode.
							return errAlertVersionConflict
						}
					}
					// Only actual matching triggers belong to a repeat episode.
					history = nil
				} else {
					episode := tx.Where("rule = ? AND subject_ref = ? AND visibility = ? AND resource_type = ? AND rule_version = ? AND last_seen >= ? AND last_seen <= ? AND (purge_state = ? OR purge_state IS NULL) AND repeat_key IS NULL", f.Rule, f.SubjectRef, e.Visibility, e.ResourceType, ruleVersion, e.At-900, e.At+900, "")
					if customerAccountRuleVersion(f.Rule) > 0 || (f.Rule == "sensitive_operation" || f.Rule == "bulk_sensitive_operations") && (ruleVersion == customerSensitiveOperationVersion || ruleVersion == customerRecoveryOperationVersion) {
						// A reused account name must never merge two customers' risk records.
						episode = episode.Where("actor_id = ? AND actor_role = ?", e.ActorID, e.ActorRole)
					}
					if loginTargetRuleVersion(f.Rule) > 0 {
						if target != nil {
							episode = episode.Where("actor_id = 0 AND actor_role = 0 AND target_user_id = ? AND target_role = ?", target.UserID, target.Role)
						} else {
							episode = episode.Where("target_user_id IS NULL")
						}
					}
					err = episode.Order("last_seen DESC").Take(&a).Error
				}
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				fresh := a.ID == 0
				if fresh {
					a = model.SecurityAlert{Rule: f.Rule, SubjectRef: f.SubjectRef, Visibility: e.Visibility, ActorID: e.ActorID, ActorRole: e.ActorRole, ResourceType: e.ResourceType, FirstSeen: e.At, Status: "open", RuleVersion: ruleVersion, UpdatedAt: detectedAt}
					if target != nil {
						a.TargetUserID, a.TargetRole = &target.UserID, &target.Role
					}
					if repeatKey != "" {
						until := e.At + 86400
						a.RepeatKey, a.RepeatUntil = &repeatKey, &until
					}
				}
				previousSeverity := a.Severity
				previousStatus := a.Status
				reopened := a.Status == "resolved" || a.Status == "false_positive"
				if repeatKey != "" && a.Status == "resolved" && a.Severity == 2 && a.ConfirmedOccurrences != nil && *a.ConfirmedOccurrences > 0 && a.Conclusion != "malicious" && a.Conclusion != "false_positive" {
					reopened = false
				}
				a.LastSeen = max(a.LastSeen, e.At)
				a.FirstSeen = min(a.FirstSeen, e.At)
				a.Severity = max(a.Severity, f.Severity)
				a.Version++
				a.Occurrences++
				a.MatchedCount = max(a.MatchedCount, f.Count)
				a.UpdatedAt = detectedAt
				var triggerFacts Facts
				if err := common.UnmarshalJsonStr(e.Facts, &triggerFacts); err != nil && e.Facts != "" {
					return err
				}
				a.AttemptedAccount = triggerFacts.AttemptedAccount
				activityChanged = true
				if reopened {
					a.Status = "open"
					a.ClosedAt = nil
					a.ArchivedAt = nil
					a.ConfirmedOccurrences = nil
				}
				var evidence []model.SecurityEvent
				evidenceLimit := 20
				_ = common.UnmarshalJsonStr(a.Evidence, &evidence)
				if f.Evidence != nil {
					// Correlated account findings retain this target's own login/key
					// proof first, even when an older episode already filled the cap.
					evidence = nil
					// At most 100 distinct logins and 100 key reads are needed to
					// prove the largest supported combination, plus the trigger.
					evidenceLimit = 201
				}
				seenEvidence := make(map[string]bool, len(evidence))
				for _, item := range evidence {
					seenEvidence[item.EventID] = true
				}
				for _, item := range history {
					if len(evidence) >= evidenceLimit-1 {
						if f.Evidence != nil {
							f.Truncated = true
						}
						break
					}
					if (item.Visibility > a.Visibility || item.Visibility != a.Visibility && f.Evidence == nil) || seenEvidence[item.EventID] {
						continue
					}
					related := item.SubjectRef == f.SubjectRef && item.Action == e.Action
					if f.Rule == "anonymous_network" || f.Rule == "ip_abuse" || f.Rule == "impossible_travel" {
						// This snapshot describes the trigger's IP, not other sources
						// used by the same account in the correlation window.
						related = false
					}
					if f.Rule == "password_spray" {
						related = item.IP == e.IP && item.Action == "auth.login_failed"
					}
					if f.Rule == "login_after_failures" {
						related = item.SubjectRef == f.SubjectRef && item.Action == "auth.login_failed" && item.At >= e.At-900 && item.At <= e.At && (item.Reason == "invalid_credentials" || item.Reason == "invalid_code")
					}
					if f.Rule == "bulk_sensitive_operations" || f.Rule == "bulk_key_access" {
						var facts Facts
						_ = common.UnmarshalJsonStr(item.Facts, &facts)
						related = item.ActorID == e.ActorID && item.ResourceType == e.ResourceType && highRisk(item, facts) > 0
					}
					if f.Evidence != nil {
						related = true
					}
					if related {
						if (f.Rule == "sensitive_operation" || f.Rule == "bulk_sensitive_operations") && (ruleVersion == customerSensitiveOperationVersion || ruleVersion == customerRecoveryOperationVersion) &&
							(customerSensitiveEventVersion(item) != ruleVersion || item.ActorID != e.ActorID || item.ResourceType != e.ResourceType) {
							continue
						}
						if target != nil {
							other := eventLoginTarget(item)
							if other == nil || *other != *target {
								continue
							}
						}
						evidence = append(evidence, item)
						seenEvidence[item.EventID] = true
					}
				}
				evidence = appendTriggerEvidence(evidence, e, evidenceLimit)
				raw, err := common.Marshal(evidence)
				if err != nil {
					return err
				}
				a.Evidence = string(raw)
				if fresh {
					if err := tx.Create(&a).Error; err != nil {
						return err
					}
				} else {
					// Do not use Save: it overwrites an analyst's committed state and
					// can insert a stale row when a concurrent cleanup deleted it.
					r := tx.Model(&model.SecurityAlert{}).Where("id = ? AND version = ?", a.ID, a.Version-1).Select("*").Omit("id").Updates(&a)
					if r.Error != nil {
						return r.Error
					}
					if r.RowsAffected != 1 {
						return errAlertVersionConflict
					}
				}
				if err := persistFindingSnapshot(tx, &a, f, e, state, detectedAt, historyTruncated); err != nil {
					return err
				}
				if err := queueAutomaticAccountResponse(tx, a, f, e, state, cfg, detectedAt, historyTruncated, work.group); err != nil {
					return err
				}
				if reopened {
					source := "live"
					if state.Name == "ip_retry" {
						source = "repair"
					}
					if err := tx.Create(&model.SecurityAction{AlertID: a.ID, Action: "system_reopen", Source: source, FromStatus: previousStatus, ToStatus: "open", EventID: e.EventID, EventAt: e.At, DetectedAt: detectedAt, CreatedAt: detectedAt}).Error; err != nil {
						return err
					}
				}
				if err := StoreRiskEvidence(tx, a, evidence); err != nil {
					return err
				}
				if ipRiskLevel(a.Rule) > 0 {
					riskChanged = true
				}
				if err := queueAccountNotification(tx, a, fmt.Sprintf("finding:%d", a.LatestFindingID), cfg, detectedAt, false); err != nil {
					return err
				}
				if reason := notificationSkipReason(cfg, a, e, fresh, previousSeverity, time.Now()); reason != "" {
					if err := recordNotificationSkip(tx, &a, detectedAt, reason); err != nil {
						return err
					}
					continue
				}
				var destinations []model.SecurityDelivery
				for _, email := range strings.Split(cfg.NotificationEmails, ";") {
					if strings.TrimSpace(email) != "" {
						destinations = append(destinations, model.SecurityDelivery{Kind: "email", Destination: strings.TrimSpace(email)})
					}
				}
				if cfg.WebhookURL != "" {
					destinations = append(destinations, model.SecurityDelivery{Kind: "webhook", Destination: cfg.WebhookURL})
				}
				for _, d := range destinations {
					d.AlertID = a.ID
					d.FindingID = a.LatestFindingID
					d.Version = a.Version
					d.Status = "pending"
					d.CreatedAt = detectedAt
					d.UpdatedAt = detectedAt
					d.NextAttemptAt = d.CreatedAt
					if err := tx.Create(&d).Error; err != nil {
						return err
					}
				}
				if len(destinations) > 0 {
					a.NotifiedAt = detectedAt
					if err := tx.Model(&a).Updates(map[string]any{"notified_at": detectedAt, "updated_at": detectedAt}).Error; err != nil {
						return err
					}
				}
			}
			if riskChanged {
				if err := model.AdvanceAuditRiskRevision(tx); err != nil {
					return err
				}
			}
			if activityChanged {
				if err := model.AdvanceAuditActivityRevision(tx); err != nil {
					return err
				}
			}
			if state.Name == "ip_retry" {
				return nil
			}
			return tx.Model(&model.SecurityState{}).Where("name = ? AND owner = ? AND fence = ?", "scanner", state.Owner, state.Fence).Updates(map[string]any{"cursor_at": next.CursorAt, "cursor_event": next.CursorEvent, "window_end": next.WindowEnd, "last_scan": time.Now().Unix()}).Error
		})
		if !errors.Is(err, errAlertVersionConflict) {
			return err
		}
		// A new transaction is necessary for MySQL REPEATABLE READ to see the
		// committed analyst action. Receipts, outbox and checkpoint roll back too.
	}
	return model.ErrSecurityConflict
}

// Each correlation has one indexed identity and a bounded event-time range.
// Separate account/IP reads avoid a broad OR plan and empty-IP correlations.
func loadCorrelationHistory(tx *gorm.DB, e model.SecurityEvent, high bool) ([]model.SecurityEvent, bool, error) {
	var queries []*gorm.DB
	window := int64(300)
	switch {
	case high:
		q := tx.Where("visibility = ? AND resource_type = ?", e.Visibility, e.ResourceType)
		if e.ActorID > 0 {
			queries = append(queries, q.Where("actor_id = ?", e.ActorID))
		} else if e.SubjectRef != "" && e.SubjectRef != "source:" {
			queries = append(queries, q.Where("subject_ref = ? AND actor_id = ?", e.SubjectRef, e.ActorID))
		}
	case e.Action == "auth.login_failed" || e.Action == "login":
		q := tx.Where("action = ? AND reason IN ? AND visibility = ?", "auth.login_failed", []string{"invalid_credentials", "invalid_code"}, e.Visibility)
		queries = append(queries, q.Session(&gorm.Session{}).Where("subject_ref = ?", e.SubjectRef))
		if e.Action == "login" {
			window = 900
		} else if e.IP != "" {
			queries = append(queries, q.Session(&gorm.Session{}).Where("ip = ?", e.IP))
		}
	case e.Action == "auth.recovery_requested" || e.Action == "auth.access_denied":
		if e.Action == "auth.recovery_requested" {
			window = 600
		}
		queries = append(queries, tx.Where("subject_ref = ? AND action = ? AND visibility = ?", e.SubjectRef, e.Action, e.Visibility))
	}
	var history []model.SecurityEvent
	truncated := false
	seen := make(map[string]bool)
	for _, q := range queries {
		var rows []model.SecurityEvent
		err := q.Select("event_id,at,actor_id,actor_role,target_id,visibility,resource_type,subject_ref,request_id,action,ip,success,reason,count,facts").Where("at >= ? AND at <= ?", e.At-window, e.At).Order("at DESC").Limit(1001).Find(&rows).Error
		if err != nil {
			return nil, false, err
		}
		if len(rows) > 1000 {
			truncated = true
			rows = rows[:1000]
			healthMu.Lock()
			health.HistoryTruncated = true
			healthMu.Unlock()
		}
		for _, row := range rows {
			if !seen[row.EventID] {
				seen[row.EventID] = true
				history = append(history, row)
			}
		}
	}
	return history, truncated, nil
}

// appendTriggerEvidence stores the trigger once. Findings that already carry
// the trigger in their proof must not append it again. An empty id is not an
// identity. Trimming keeps the newest history and still retains the trigger
// when that trim would otherwise drop the only copy.
func appendTriggerEvidence(evidence []model.SecurityEvent, trigger model.SecurityEvent, limit int) []model.SecurityEvent {
	if limit < 1 {
		limit = 1
	}
	if len(evidence) >= limit {
		evidence = evidence[len(evidence)-(limit-1):]
	}
	if trigger.EventID != "" {
		for _, item := range evidence {
			if item.EventID == trigger.EventID {
				return evidence
			}
		}
	}
	return append(evidence, trigger)
}

// collapseEvidenceEvents keeps the last copy of a repeated event id. Older
// saved alerts appended the trigger after the proof that already contained it.
// Rows with an empty id stay separate.
func collapseEvidenceEvents(events []model.SecurityEvent) []model.SecurityEvent {
	if len(events) < 2 {
		return events
	}
	last := make(map[string]int, len(events))
	for i, event := range events {
		if event.EventID != "" {
			last[event.EventID] = i
		}
	}
	out := make([]model.SecurityEvent, 0, len(events))
	for i, event := range events {
		if event.EventID != "" && last[event.EventID] != i {
			continue
		}
		out = append(out, event)
	}
	return out
}

type AlertDetail struct {
	Alert      model.SecurityAlert             `json:"alert"`
	Events     []model.SecurityEvent           `json:"events"`
	IPMarkers  []IPRiskMarker                  `json:"ip_markers"`
	Findings   []model.SecurityFindingSnapshot `json:"findings"`
	Actions    []model.SecurityAction          `json:"actions"`
	Deliveries []model.SecurityDelivery        `json:"deliveries"`
}

func GetAlert(ctx context.Context, db *gorm.DB, role int, id int64) (AlertDetail, error) {
	var result AlertDetail
	err := model.SecurityVisibleQuery(db.WithContext(ctx), role).Where("id = ? AND (purge_state = ? OR purge_state IS NULL)", id, "").Take(&result.Alert).Error
	if err != nil {
		return result, err
	}
	result.Events = make([]model.SecurityEvent, 0)
	result.Findings = make([]model.SecurityFindingSnapshot, 0)
	result.Actions = make([]model.SecurityAction, 0)
	result.Deliveries = make([]model.SecurityDelivery, 0)
	if err = common.UnmarshalJsonStr(result.Alert.Evidence, &result.Events); err != nil {
		return result, err
	}
	result.Events = collapseEvidenceEvents(result.Events)
	result.Alert.Evidence = ""
	alerts := []model.SecurityAlert{result.Alert}
	if err := loadAlertAssignees(ctx, db, alerts); err != nil {
		return result, err
	}
	result.Alert.Assignees = alerts[0].Assignees
	result.Alert.ManualAssigneeIDs = alerts[0].ManualAssigneeIDs
	if err := loadAlertResponses(ctx, db, alerts); err != nil {
		return result, err
	}
	result.Alert.Response = alerts[0].Response
	result.Alert.Responses = alerts[0].Responses
	result.Alert.ResponseGroupID = alerts[0].ResponseGroupID
	result.Alert.CanStop = role >= common.RoleRootUser && alerts[0].CanStop
	for i := range result.Events {
		result.Events[i] = publicSecurityEvent(result.Events[i])
	}
	sort.Slice(result.Events, func(i, j int) bool {
		if result.Events[i].At == result.Events[j].At {
			return result.Events[i].EventID < result.Events[j].EventID
		}
		return result.Events[i].At < result.Events[j].At
	})
	logs := make([]*model.AuditLog, 0, len(result.Events))
	for _, event := range result.Events {
		logs = append(logs, &model.AuditLog{EventId: event.EventID, Ip: event.IP, CreatedAt: event.At})
	}
	if result.IPMarkers, err = RiskMarkers(ctx, db, logs, role); err != nil {
		return result, err
	}
	if err = db.WithContext(ctx).Where("alert_id = ?", id).Order("id DESC").Limit(100).Find(&result.Findings).Error; err != nil {
		return result, err
	}
	actions := db.WithContext(ctx).Where("alert_id = ?", id)
	if result.Alert.ResponseGroupID != nil {
		actions = db.WithContext(ctx).Where("alert_id = ? OR response_group_id = ?", id, *result.Alert.ResponseGroupID)
	}
	if role < common.RoleRootUser {
		actions = actions.Where("actor_id = ? OR actor_role IN ?", 0, []int{common.RoleCommonUser, common.RoleAdminUser})
	}
	if err = actions.Order("id DESC").Limit(100).Find(&result.Actions).Error; err != nil {
		return result, err
	}
	for i := range result.Actions {
		result.Actions[i].AffectedCount = result.Actions[i].ResponseSummary().AffectedCount
		if result.Actions[i].Outcome == "" && result.Actions[i].Result != "" {
			var snapshot struct {
				Assignees []model.SecurityAlertAssignee `json:"assignees"`
			}
			if err := common.UnmarshalJsonStr(result.Actions[i].Result, &snapshot); err != nil {
				return result, err
			}
			result.Actions[i].Assignees = snapshot.Assignees
		}
	}
	if err := db.WithContext(ctx).Omit("destination").Where("alert_id = ?", id).Order("id DESC").Limit(100).Find(&result.Deliveries).Error; err != nil {
		return result, err
	}
	actors := []securityActorName{{result.Alert.ActorID, &result.Alert.ActorUsername}}
	if result.Alert.TargetUserID != nil {
		actors = append(actors, securityActorName{*result.Alert.TargetUserID, &result.Alert.TargetUsername})
	}
	for i := range result.Events {
		actors = append(actors, securityActorName{result.Events[i].ActorID, &result.Events[i].ActorUsername})
		if result.Events[i].TargetRole > 0 {
			actors = append(actors, securityActorName{result.Events[i].TargetID, &result.Events[i].TargetUsername})
		}
	}
	for i := range result.Findings {
		actors = append(actors, securityActorName{result.Findings[i].ActorID, &result.Findings[i].ActorUsername})
		if result.Findings[i].TargetRole != nil {
			actors = append(actors, securityActorName{result.Findings[i].TargetID, &result.Findings[i].TargetUsername})
		}
	}
	for i := range result.Actions {
		actors = append(actors, securityActorName{result.Actions[i].ActorID, &result.Actions[i].ActorUsername})
	}
	if err := loadSecurityActorNames(ctx, db, actors); err != nil {
		return result, err
	}
	return result, nil
}

func ActOnAlert(ctx context.Context, db *gorm.DB, role, actor int, id, version int64, status, note string) error {
	_, err := UpdateAlertInvestigation(ctx, db, role, actor, AlertInvestigation{Version: version, Status: status, Note: note}, id)
	return err
}

type findingWork struct {
	event     model.SecurityEvent
	finding   model.SecurityFinding
	history   []model.SecurityEvent
	truncated bool
	group     *model.SecurityResponseGroup
}

func newFindingWork(e model.SecurityEvent, f model.SecurityFinding, history []model.SecurityEvent, truncated bool) findingWork {
	if f.TargetEvent != nil {
		e = *f.TargetEvent
	}
	if f.Evidence != nil {
		history = f.Evidence
		// An independent rule carries its own evidence completeness; a cut in
		// an unrelated correlation query must not suppress its response.
		truncated = f.Truncated
	}
	return findingWork{event: e, finding: f, history: history, truncated: truncated}
}

func findingDetectionKey(f model.SecurityFinding, e model.SecurityEvent) string {
	return model.SecurityAccountRef(fmt.Sprintf("finding:%s:%d:%d:%s", f.Rule, alertRuleVersion(f.Rule, e), e.ActorID, e.EventID))
}

// Only newly arrived authenticated logins can repair earlier key correlations.
// Queries use the existing actor/IP time indexes and never replay source logs.
func lateLoginFindings(tx *gorm.DB, logins []model.SecurityEvent, cfg operation_setting.AuditSecuritySetting) ([]findingWork, error) {
	// Each arriving login needs two indexed windows, independent of the number
	// of old key events. Re-querying two windows for every trigger would turn a
	// shared NAT burst into thousands of SQL calls inside the scanner lease.
	var work []findingWork
	seenWindows, seenTriggers, seenFindings := map[string]bool{}, map[string]bool{}, map[string]bool{}
	truncated := false
	for _, login := range logins {
		if login.ActorID <= 0 || !publicIPForIntelligence(login.IP) {
			continue
		}
		for _, scope := range []struct {
			rule, key string
			query     *gorm.DB
		}{
			{"multi_source_key_access", fmt.Sprintf("actor:%d:%d", login.ActorID, login.At), tx.Where("actor_id = ?", login.ActorID)},
			{"multi_account_key_access", fmt.Sprintf("ip:%s:%d", login.IP, login.At), tx.Where("ip = ?", login.IP)},
		} {
			if seenWindows[scope.key] {
				continue
			}
			seenWindows[scope.key] = true
			var rows []model.SecurityEvent
			if err := scope.query.Select(model.SecurityEventColumns).Where("at >= ? AND at <= ? AND action IN ?", login.At-600, login.At+600, []string{"login", "token.key_view", "token.key_view_batch"}).Order("at, event_id").Limit(1001).Find(&rows).Error; err != nil {
				return nil, err
			}
			cut := len(rows) > 1000
			if cut {
				rows = rows[:1000]
			}
			for _, trigger := range rows {
				if trigger.At < login.At || trigger.Action == "login" {
					continue
				}
				triggerKey := scope.rule + ":" + trigger.EventID
				if seenTriggers[triggerKey] {
					continue
				}
				if len(seenTriggers) >= 1000 {
					truncated = true
					break
				}
				seenTriggers[triggerKey] = true
				for _, finding := range EvaluateAccountCompromise(trigger, rows, cfg, false) {
					if finding.Rule != scope.rule {
						continue
					}
					finding.Truncated = finding.Truncated || cut
					item := newFindingWork(trigger, finding, rows, cut)
					key := findingDetectionKey(finding, item.event)
					if seenFindings[key] {
						continue
					}
					if len(work) >= 1000 {
						truncated = true
						break
					}
					seenFindings[key] = true
					work = append(work, item)
				}
			}
		}
	}
	if truncated {
		for i := range work {
			work[i].truncated = true
			work[i].finding.Truncated = true
		}
	}
	return work, nil
}
