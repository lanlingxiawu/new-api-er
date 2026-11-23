package auditsecurity

import (
	"errors"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

type sourceBaselineEvidence struct {
	SourceFirstSeen   int64 `json:"source_first_seen"`
	BaselineFirstSeen int64 `json:"baseline_first_seen"`
	BaselineLastSeen  int64 `json:"baseline_last_seen"`
}

func observeManagementSource(tx *gorm.DB, e model.SecurityEvent) (*sourceBaselineEvidence, error) {
	if e.ActorID <= 0 || !e.Success || e.SourceRef == "" || !publicIPForIntelligence(e.IP) {
		return nil, nil
	}
	source := model.SecurityAccountRef("management:" + e.SourceRef)
	source = source[len("account:"):]
	var baseline model.SecurityBaseline
	err := tx.Where("account_id = ? AND source_ref = ?", e.ActorID, source).Take(&baseline).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if baseline.ID == 0 {
		baseline = model.SecurityBaseline{AccountID: e.ActorID, SourceRef: source, Kind: "management", FirstSeen: e.At, LastSeen: e.At}
		if err := tx.Create(&baseline).Error; err != nil {
			return nil, err
		}
	} else if err := tx.Model(&baseline).Updates(map[string]any{"first_seen": min(e.At, baseline.FirstSeen), "last_seen": max(e.At, baseline.LastSeen)}).Error; err != nil {
		return nil, err
	}
	if e.At < baseline.FirstSeen || e.At-baseline.FirstSeen > 86400 {
		return nil, nil
	}
	var mature model.SecurityBaseline
	err = tx.Select("id,first_seen,last_seen").Where("account_id = ? AND kind = ? AND first_seen <= ? AND last_seen >= ? AND source_ref <> ?", e.ActorID, "management", e.At-86400, e.At-30*86400, source).Limit(1).Take(&mature).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sourceBaselineEvidence{SourceFirstSeen: baseline.FirstSeen, BaselineFirstSeen: mature.FirstSeen, BaselineLastSeen: mature.LastSeen}, nil
}

func successfulAccountKeyAccess(e model.SecurityEvent, f Facts) bool {
	if !e.Success || e.ActorID <= 0 || f.EvidenceIncomplete {
		return false
	}
	switch e.Action {
	case "token.key_view":
		return f.ID > 0
	case "token.key_view_batch":
		return f.Count != nil && *f.Count > 0 && len(f.ReturnedIDs) > 0
	default:
		return false
	}
}

func actualChannelAddressChange(e model.SecurityEvent, f Facts) bool {
	return e.Success && e.ActorID > 0 && e.TargetID > 0 && e.Action == "channel.update" && !f.EvidenceIncomplete && (f.Changed == nil || *f.Changed) && slices.Contains(f.ChangedFields, "base_url")
}

// EvaluateAccountCompromise correlates only authenticated successes. Anonymous
// failures, geography and shared IPs alone cannot produce an automatic action.
func EvaluateAccountCompromise(e model.SecurityEvent, history []model.SecurityEvent, cfg operation_setting.AuditSecuritySetting, newManagementSource bool) []model.SecurityFinding {
	if e.ActorID <= 0 || !e.Success || !publicIPForIntelligence(e.IP) {
		return nil
	}
	var current Facts
	if common.UnmarshalJsonStr(e.Facts, &current) != nil || current.EvidenceIncomplete {
		return nil
	}
	rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
	if err != nil {
		return nil
	}
	rows := append(append([]model.SecurityEvent(nil), history...), e)
	slices.SortFunc(rows, func(a, b model.SecurityEvent) int {
		if a.At < b.At {
			return -1
		}
		if a.At > b.At {
			return 1
		}
		if a.EventID < b.EventID {
			return -1
		}
		if a.EventID > b.EventID {
			return 1
		}
		return 0
	})
	seenEvents, seenRequests := map[string]bool{}, map[string]bool{}
	clean := make([]model.SecurityEvent, 0, len(rows))
	facts := make(map[string]Facts, len(rows))
	for _, row := range rows {
		if row.EventID == "" || seenEvents[row.EventID] || row.RequestID != "" && seenRequests[row.RequestID] || row.At > e.At || row.At < e.At-600 || row.ActorID <= 0 || !row.Success || !publicIPForIntelligence(row.IP) {
			continue
		}
		var f Facts
		if common.UnmarshalJsonStr(row.Facts, &f) != nil || f.EvidenceIncomplete {
			continue
		}
		seenEvents[row.EventID] = true
		if row.RequestID != "" {
			seenRequests[row.RequestID] = true
		}
		clean = append(clean, row)
		facts[row.EventID] = f
	}
	var findings []model.SecurityFinding
	for _, rule := range rules {
		if rule.ID == "session_reuse" {
			continue
		} // Evaluated by the authentication rule.
		start := e.At - int64(rule.WindowSeconds)
		switch rule.ID {
		case "multi_account_key_access":
			if current.ClientRef == "" || !successfulAccountKeyAccess(e, current) {
				continue
			}
			var logins, keys []model.SecurityEvent
			for _, row := range clean {
				if row.At < start || row.IP != e.IP || facts[row.EventID].ClientRef != current.ClientRef {
					continue
				}
				if row.Action == "login" {
					logins = append(logins, row)
				}
				if successfulAccountKeyAccess(row, facts[row.EventID]) {
					keys = append(keys, row)
				}
			}
			targets := map[int]model.SecurityFinding{}
			for first, login := range logins {
				accounts := map[int]model.SecurityEvent{}
				for _, item := range logins[first:] {
					if item.At-login.At > int64(rule.LoginWindowSeconds) {
						break
					}
					if _, exists := accounts[item.ActorID]; !exists {
						accounts[item.ActorID] = item
					}
				}
				if len(accounts) < rule.LoginAccounts {
					continue
				}
				readers := map[int]model.SecurityEvent{}
				for _, item := range keys {
					ownLogin, ok := accounts[item.ActorID]
					if ok && item.At >= ownLogin.At && item.At-login.At <= int64(rule.WindowSeconds) {
						readers[item.ActorID] = item
					}
				}
				if len(readers) < rule.KeyAccounts {
					continue
				}
				if key, ok := readers[e.ActorID]; !ok || key.EventID != e.EventID {
					continue
				}
				for actor, key := range readers {
					target := key
					target.Visibility = common.RoleRootUser // Shared-source evidence is privileged.
					proof := []model.SecurityEvent{accounts[actor], key}
					proofAccounts := map[int]bool{actor: true}
					proofKeys := 1
					if actor != e.ActorID {
						proof = append(proof, accounts[e.ActorID], e)
						proofAccounts[e.ActorID] = true
						proofKeys++
					}
					actorIDs := make([]int, 0, len(readers))
					for id := range readers {
						actorIDs = append(actorIDs, id)
					}
					slices.Sort(actorIDs)
					for _, id := range actorIDs {
						if !proofAccounts[id] && proofKeys < rule.KeyAccounts {
							proof = append(proof, accounts[id], readers[id])
							proofAccounts[id] = true
							proofKeys++
						}
					}
					actorIDs = actorIDs[:0]
					for id := range accounts {
						actorIDs = append(actorIDs, id)
					}
					slices.Sort(actorIDs)
					for _, id := range actorIDs {
						if !proofAccounts[id] && len(proofAccounts) < rule.LoginAccounts {
							proof = append(proof, accounts[id])
							proofAccounts[id] = true
						}
					}
					targets[actor] = model.SecurityFinding{Rule: rule.ID, SubjectRef: "actor:" + strconv.Itoa(actor), Severity: 3, Count: len(readers), Threshold: rule.KeyAccounts, WindowSeconds: rule.WindowSeconds, TargetEvent: &target, Evidence: proof, Dimensions: model.SecurityDimensions{"accounts": len(accounts), "key_accounts": len(readers), "account_threshold": rule.LoginAccounts, "key_account_threshold": rule.KeyAccounts, "login_window_seconds": rule.LoginWindowSeconds}}
				}
			}
			ids := make([]int, 0, len(targets))
			for id := range targets {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			for _, id := range ids {
				findings = append(findings, targets[id])
			}
		case "multi_source_key_access":
			if !successfulAccountKeyAccess(e, current) {
				continue
			}
			logins, keys := map[string]model.SecurityEvent{}, map[string]model.SecurityEvent{}
			for _, row := range clean {
				if row.At < start || row.ActorID != e.ActorID {
					continue
				}
				if row.Action == "login" {
					if _, exists := logins[row.IP]; !exists {
						logins[row.IP] = row
					}
				}
			}
			// Events have second precision. Collect logins first so random event
			// IDs cannot make a same-second key read precede its login.
			for _, row := range clean {
				if row.At < start || row.ActorID != e.ActorID {
					continue
				}
				if successfulAccountKeyAccess(row, facts[row.EventID]) {
					if login, ok := logins[row.IP]; ok && row.At >= login.At {
						keys[row.IP] = row
					}
				}
			}
			if len(logins) < rule.LoginIPs || len(keys) < rule.KeyIPs {
				continue
			}
			if key, ok := keys[e.IP]; !ok || key.EventID != e.EventID {
				continue
			}
			target := e
			target.Visibility = common.RoleRootUser
			proof := []model.SecurityEvent{logins[e.IP], e}
			proofIPs := map[string]bool{e.IP: true}
			ips := make([]string, 0, len(keys))
			for ip := range keys {
				ips = append(ips, ip)
			}
			slices.Sort(ips)
			for _, ip := range ips {
				if !proofIPs[ip] && len(proofIPs) < rule.KeyIPs {
					proof = append(proof, logins[ip], keys[ip])
					proofIPs[ip] = true
				}
			}
			ips = ips[:0]
			for ip := range logins {
				ips = append(ips, ip)
			}
			slices.Sort(ips)
			for _, ip := range ips {
				if !proofIPs[ip] && len(proofIPs) < rule.LoginIPs {
					proof = append(proof, logins[ip])
					proofIPs[ip] = true
				}
			}
			findings = append(findings, model.SecurityFinding{Rule: rule.ID, SubjectRef: "actor:" + strconv.Itoa(e.ActorID), Severity: 3, Count: len(keys), Threshold: rule.KeyIPs, WindowSeconds: rule.WindowSeconds, TargetEvent: &target, Evidence: proof, Dimensions: model.SecurityDimensions{"login_ips": len(logins), "key_ips": len(keys), "login_ip_threshold": rule.LoginIPs, "key_ip_threshold": rule.KeyIPs}})
		case "new_source_channel_rewrite":
			if !newManagementSource || !actualChannelAddressChange(e, current) {
				continue
			}
			channels := map[int]bool{}
			proof := []model.SecurityEvent{e}
			proofChannels := map[int]bool{e.TargetID: true}
			for _, row := range clean {
				if row.At >= start && row.ActorID == e.ActorID && row.IP == e.IP && actualChannelAddressChange(row, facts[row.EventID]) {
					channels[row.TargetID] = true
					if !proofChannels[row.TargetID] && len(proofChannels) < rule.Channels {
						proof = append(proof, row)
						proofChannels[row.TargetID] = true
					}
				}
			}
			if len(channels) >= rule.Channels {
				findings = append(findings, model.SecurityFinding{Rule: rule.ID, SubjectRef: "actor:" + strconv.Itoa(e.ActorID), Severity: 3, Count: len(channels), Threshold: rule.Channels, WindowSeconds: rule.WindowSeconds, Evidence: proof, Dimensions: model.SecurityDimensions{"distinct_channels": len(channels), "new_source": 1}})
			}
		}
	}
	return findings
}

func loadCompromiseHistory(tx *gorm.DB, e model.SecurityEvent) ([]model.SecurityEvent, bool, error) {
	var f Facts
	_ = common.UnmarshalJsonStr(e.Facts, &f)
	var queries []*gorm.DB
	if successfulAccountKeyAccess(e, f) && publicIPForIntelligence(e.IP) {
		queries = append(queries, tx.Where("actor_id = ?", e.ActorID), tx.Where("ip = ?", e.IP))
	} else if actualChannelAddressChange(e, f) && publicIPForIntelligence(e.IP) {
		queries = append(queries, tx.Where("actor_id = ?", e.ActorID))
	}
	var history []model.SecurityEvent
	truncated := false
	seen := map[string]bool{}
	for _, q := range queries {
		var rows []model.SecurityEvent
		if err := q.Select(model.SecurityEventColumns).Where("at >= ? AND at <= ?", e.At-600, e.At).Order("at DESC").Limit(1001).Find(&rows).Error; err != nil {
			return nil, false, err
		}
		if len(rows) > 1000 {
			truncated = true
			rows = rows[:1000]
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
