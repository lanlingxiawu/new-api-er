package auditsecurity

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

func ipIntelligenceCandidate(e model.SecurityEvent) bool {
	if !publicIPForIntelligence(e.IP) {
		return false
	}
	switch e.Action {
	case "auth.login_failed":
		return !e.Success && (e.Reason == "invalid_credentials" || e.Reason == "invalid_code")
	case "auth.session_refresh_reuse":
		return !e.Success && e.ActorID > 0
	case "auth.access_denied":
		return !e.Success && e.Reason == "permission_denied"
	case "auth.recovery_requested":
		return e.Success
	}
	if !e.Success {
		return false
	}
	var facts Facts
	_ = common.UnmarshalJsonStr(e.Facts, &facts)
	return e.Action == "login" || e.Action == "relay.ip_observed" && e.ActorID > 0 || successfulSensitiveOperation(e, facts) || highRisk(e, facts) > 0
}

func enrichSecurityEvents(ctx context.Context, db *gorm.DB, events []model.SecurityEvent, cfg operation_setting.AuditSecuritySetting, remaining *int) error {
	if !cfg.IPIntelligenceEnabled {
		return nil
	}
	ids := make([]string, 0, len(events))
	positions := make([]int, 0, len(events))
	var failedPositions []int
	for i, event := range events {
		if ipIntelligenceCandidate(event) && (event.Action != "relay.ip_observed" || cfg.RelayIPEnabled) {
			ids = append(ids, event.EventID)
			if event.ActorID > 0 && (event.Success || event.Action == "auth.session_refresh_reuse") {
				positions = append(positions, i)
			} else {
				failedPositions = append(failedPositions, i)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var receipts []model.SecurityEvent
	if err := db.WithContext(ctx).Select("event_id", "ip_check_state", "ip_next_attempt_at").Where("event_id IN ?", ids).Find(&receipts).Error; err != nil {
		return err
	}
	processed := make(map[string]model.SecurityEvent, len(receipts))
	for _, receipt := range receipts {
		processed[receipt.EventID] = receipt
	}
	now := time.Now().Unix()
	// Failed attempts share the existing budget, after authenticated activity in
	// this batch. Keep the source order intact for the scanner checkpoint.
	positions = append(positions, failedPositions...)
	for _, i := range positions {
		event := &events[i]
		if receipt, exists := processed[event.EventID]; exists {
			// Empty state is a pre-upgrade receipt: do not implicitly rescan history.
			if receipt.IPCheckState != "pending" {
				continue
			}
			event.IPCheckState, event.IPNextAttemptAt = receipt.IPCheckState, receipt.IPNextAttemptAt
			if event.IPNextAttemptAt > now {
				continue
			}
		}
		if event.At < now-86400 {
			event.IPCheckState, event.IPNextAttemptAt = "skipped", 0
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		event.IPCheckState, event.IPNextAttemptAt = "pending", now+60
		snapshot, calls := globalIPIntelligence.lookup(ctx, db, event.IP, cfg, *remaining)
		*remaining -= calls
		if len(snapshot.Providers) == 0 {
			continue
		}
		var facts Facts
		_ = common.UnmarshalJsonStr(event.Facts, &facts)
		facts.IPIntelligence = &snapshot
		raw, err := common.Marshal(facts)
		if err != nil {
			return err
		}
		event.Facts = string(raw)
		event.IPCheckState, event.IPNextAttemptAt = "done", 0
	}
	return nil
}

func (w *worker) retryIPIntelligence(ctx context.Context, lease model.SecurityState, cfg operation_setting.AuditSecuritySetting, remaining *int) error {
	if !cfg.IPIntelligenceEnabled {
		return nil
	}
	var events []model.SecurityEvent
	query := w.db.WithContext(ctx).Select(model.SecurityEventColumns).Where("ip_check_state = ? AND ip_next_attempt_at <= ?", "pending", time.Now().Unix())
	if !cfg.RelayIPEnabled {
		query = query.Where("action <> ?", "relay.ip_observed")
	}
	if err := query.Order("ip_next_attempt_at ASC, event_id ASC").Limit(20).Find(&events).Error; err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	if err := enrichSecurityEvents(ctx, w.db, events, cfg, remaining); err != nil {
		return err
	}
	lease.Name = "ip_retry"
	return Apply(ctx, w.db, lease, events, lease, cfg)
}

func evaluateIPIntelligenceFindings(tx *gorm.DB, event model.SecurityEvent, cfg operation_setting.AuditSecuritySetting) ([]model.SecurityFinding, error) {
	var facts Facts
	_ = common.UnmarshalJsonStr(event.Facts, &facts)
	intel := facts.IPIntelligence
	if intel == nil {
		return nil, nil
	}
	findings := make([]model.SecurityFinding, 0, 3)
	// Ordinary VPN/proxy use is expected here. Keep those facts for investigation,
	// but only Tor warrants a standalone anonymous-network finding.
	if intel.IsTor != nil && *intel.IsTor {
		findings = append(findings, model.SecurityFinding{Rule: "anonymous_network", SubjectRef: event.SubjectRef, Severity: 1, Count: 1, Threshold: 1, Dimensions: model.SecurityDimensions{"detected": 1, "tor": 1}})
	}
	if intel.IsAbuser != nil && *intel.IsAbuser {
		findings = append(findings, model.SecurityFinding{Rule: "ip_abuse", SubjectRef: event.SubjectRef, Severity: 2, Count: 1, Threshold: 1, Dimensions: model.SecurityDimensions{"detected": 1}})
	}
	if event.Action != "login" || event.ActorID <= 0 || !reliableGeoForTravel(*intel) {
		return findings, nil
	}
	var baseline model.SecurityGeoBaseline
	err := tx.Where("account_id = ?", event.ActorID).Take(&baseline).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if baseline.AccountID != 0 && event.At > baseline.LastAt {
		distance := geoDistanceKM(baseline.Latitude, baseline.Longitude, *intel.Latitude, *intel.Longitude)
		elapsed := event.At - baseline.LastAt
		if elapsed > 0 {
			speed := distance * 3600 / float64(elapsed)
			if distance >= float64(cfg.IPIntelligenceMinDistanceKM) && speed >= float64(cfg.IPIntelligenceImpossibleTravelKMH) {
				findings = append(findings, model.SecurityFinding{
					Rule: "impossible_travel", SubjectRef: event.SubjectRef, Severity: 1, Count: int(math.Round(speed)),
					Threshold: cfg.IPIntelligenceImpossibleTravelKMH, WindowSeconds: int(elapsed),
					Dimensions: model.SecurityDimensions{"distance_km": int(math.Round(distance)), "speed_kmh": int(math.Round(speed)), "min_distance_km": cfg.IPIntelligenceMinDistanceKM},
				})
			}
		}
	}
	if baseline.AccountID == 0 || event.At >= baseline.LastAt {
		baseline = model.SecurityGeoBaseline{
			AccountID: event.ActorID, CountryCode: intel.CountryCode, Country: intel.Country,
			Latitude: math.Round(*intel.Latitude*100) / 100, Longitude: math.Round(*intel.Longitude*100) / 100,
			LastAt: event.At, LastEventID: event.EventID,
		}
		if err := tx.Save(&baseline).Error; err != nil {
			return nil, err
		}
	}
	return findings, nil
}

func geoDistanceKM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKM = 6371
	toRadians := func(value float64) float64 { return value * math.Pi / 180 }
	latDelta := toRadians(lat2 - lat1)
	lonDelta := toRadians(lon2 - lon1)
	a := math.Sin(latDelta/2)*math.Sin(latDelta/2) + math.Cos(toRadians(lat1))*math.Cos(toRadians(lat2))*math.Sin(lonDelta/2)*math.Sin(lonDelta/2)
	return 2 * earthRadiusKM * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
