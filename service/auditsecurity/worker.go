package auditsecurity

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Health struct {
	Enabled               bool                     `json:"enabled"`
	CaptureEnabled        bool                     `json:"capture_enabled"`
	Suspended             bool                     `json:"suspended"`
	AutoBanEnabled        bool                     `json:"auto_ban_enabled"`
	Ready                 bool                     `json:"ready"`
	StorageReady          bool                     `json:"storage_ready"`
	LastScan              int64                    `json:"last_scan"`
	CursorAt              int64                    `json:"cursor_at"`
	ErrorCode             string                   `json:"error_code"`
	Dropped               uint64                   `json:"dropped"`
	HistoryTruncated      bool                     `json:"history_truncated"`
	ActivityRevision      int64                    `json:"activity_revision"`
	Capacity              SecurityCapacity         `json:"capacity"`
	IPIntelligenceEnabled bool                     `json:"ip_intelligence_enabled"`
	Providers             []SecurityProviderHealth `json:"providers"`
	RelayIPEnabled        bool                     `json:"relay_ip_enabled"`
	RelayIPQueued         int                      `json:"relay_ip_queued"`
	RelayIPDropped        uint64                   `json:"relay_ip_dropped"`
	RelayIPMissing        uint64                   `json:"relay_ip_missing"`
	RelayIPLastObserved   int64                    `json:"relay_ip_last_observed"`
}

var primary atomic.Pointer[gorm.DB]
var healthMu sync.RWMutex
var health Health

func Database() *gorm.DB { return primary.Load() }
func Status() Health {
	healthMu.RLock()
	h := health
	healthMu.RUnlock()
	c := operation_setting.GetAuditSecuritySetting()
	h.Enabled = c.Enabled
	h.CaptureEnabled = c.CaptureEnabled
	h.Suspended = c.Suspended
	h.AutoBanEnabled = c.AutoBanEnabled
	h.Dropped = model.SecurityCaptureDropped.Load()
	h.RelayIPEnabled = c.Enabled && c.IPIntelligenceEnabled && c.RelayIPEnabled && !c.Suspended
	h.RelayIPQueued = model.SecurityRelayIPQueued()
	h.RelayIPDropped = model.SecurityRelayIPDropped.Load()
	h.RelayIPMissing = model.SecurityRelayIPMissing.Load()
	h.RelayIPLastObserved = model.SecurityRelayIPLastObserved.Load()
	return h
}

type worker struct {
	db, source, writer *gorm.DB
	owner              string
	lastCleanup        int64
	lastScanAttempt    time.Time
	sourceStartAt      int64
	credentialCache    *redis.Client
	relayIPSeen        map[string]int64
}

// Start owns a fixed deletion loop, a supervisor and at most one detector worker. Configuration changes
// cancel in-flight work within a second, including notification HTTP/SMTP I/O.
func Start() func() {
	ctx, stop := context.WithCancel(context.Background())
	deletionDone := make(chan struct{})
	go runAlertDeletion(ctx, deletionDone)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w := &worker{owner: common.NewRequestId()}
		defer w.close()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		var running chan struct{}
		var cancel context.CancelFunc
		var lastRun time.Time
		for {
			select {
			case <-ctx.Done():
				if cancel != nil {
					cancel()
				}
				if running != nil {
					<-running
				}
				return
			case <-running:
				running = nil
				cancel = nil
			case <-timer.C:
				cfg := operation_setting.GetAuditSecuritySetting()
				inactive := cfg.Suspended || (!cfg.Enabled && !cfg.CaptureEnabled)
				if inactive && running != nil {
					if cancel != nil {
						cancel()
					}
					continue
				}
				interval := time.Duration(cfg.ScanIntervalSeconds) * time.Second
				if cfg.CaptureEnabled || cfg.Enabled && cfg.IPIntelligenceEnabled && cfg.RelayIPEnabled {
					interval = time.Second
				}
				if inactive {
					interval = 30 * time.Second
				}
				if running != nil || time.Since(lastRun) < interval {
					continue
				}
				runCtx, runCancel := context.WithTimeout(ctx, 25*time.Second)
				cancel = runCancel
				running = make(chan struct{})
				lastRun = time.Now()
				go w.cycle(runCtx, runCancel, running, cfg)
			}
		}
	}()
	return func() {
		stop()
		<-deletionDone
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (w *worker) close() {
	primary.Store(nil)
	if w.credentialCache != nil {
		_ = w.credentialCache.Close()
	}
	for _, db := range []*gorm.DB{w.db, w.source, w.writer} {
		if db != nil {
			if pool, err := db.DB(); err == nil {
				_ = pool.Close()
			}
		}
	}
}

func (w *worker) cycle(ctx context.Context, cancel context.CancelFunc, done chan struct{}, cfg operation_setting.AuditSecuritySetting) {
	defer close(done)
	defer cancel()
	defer func() {
		if recover() != nil {
			w.report("worker_failure")
		}
	}()
	// Committed authentication updates must finish even after monitoring is
	// disabled. Only the isolated primary pool is needed for that recovery.
	if w.db == nil {
		var err error
		w.db, err = model.OpenAuditSecurityDB(ctx, false)
		if err != nil {
			w.report("storage_unavailable")
			return
		}
	}
	primary.Store(w.db)
	if err := w.processAccountResponses(ctx); err != nil && ctx.Err() == nil {
		common.SysError("audit account response queue unavailable")
	}
	if cfg.Suspended || (!cfg.Enabled && !cfg.CaptureEnabled) {
		_ = w.captureRelayIPs(ctx, cfg)
		return
	}
	if err := w.initialize(ctx); err != nil {
		w.report("storage_unavailable")
		return
	}
	if cfg.Enabled && w.sourceStartAt == 0 {
		if err := w.initializeSourceBoundary(ctx); err != nil {
			w.report("storage_unavailable")
			return
		}
	}
	if cfg.CaptureEnabled {
		if err := w.capture(ctx); err != nil {
			w.report("capture_failed")
			return
		}
	}
	if !cfg.Enabled {
		return
	}
	if err := w.captureRelayIPs(ctx, cfg); err != nil {
		w.report("capture_failed")
		return
	}
	if time.Since(w.lastScanAttempt) < time.Duration(cfg.ScanIntervalSeconds)*time.Second {
		return
	}
	w.lastScanAttempt = time.Now()
	state, err := model.ClaimAuditSecurity(ctx, w.db, w.owner)
	if errors.Is(err, model.ErrSecurityConflict) {
		return
	}
	if err != nil {
		w.report("lease_unavailable")
		return
	}
	if err = w.scan(ctx, state, cfg); err != nil {
		w.report("scan_failed")
		return
	}
	if err = w.deliver(ctx, cfg); err != nil {
		w.report("notification_failed")
		return
	}
	if err = w.cleanup(ctx, cfg); err != nil {
		w.report("cleanup_failed")
		return
	}
}

func (w *worker) initialize(ctx context.Context) error {
	var err error
	if w.db == nil {
		w.db, err = model.OpenAuditSecurityDB(ctx, false)
		if err != nil {
			return err
		}
	}
	if primary.Load() == nil {
		primary.Store(w.db)
	}
	if w.source == nil {
		w.source, err = model.OpenAuditSecurityDB(ctx, true)
		if err != nil {
			return err
		}
	}
	if w.writer == nil {
		w.writer, err = model.OpenAuditSecurityDB(ctx, true)
		if err != nil {
			return err
		}
	}
	healthMu.Lock()
	health.Ready = true
	healthMu.Unlock()
	return nil
}

func (w *worker) report(code string) {
	healthMu.Lock()
	changed := health.ErrorCode != code
	health.ErrorCode = code
	healthMu.Unlock()
	if changed && code != "" {
		common.SysError("audit security monitor: " + code)
	}
}

func (w *worker) capture(ctx context.Context) error {
	for range 200 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		e, ok := model.TakeSecurityCapture()
		if !ok {
			return nil
		}
		row, err := model.AuditLogStorageRow(w.writer, e)
		if err != nil {
			model.SecurityCaptureDropped.Add(1)
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = w.writer.WithContext(writeCtx).Table("audit_logs").Create(row).Error
		cancel()
		if err != nil {
			model.SecurityCaptureDropped.Add(1)
			return err
		}
	}
	return nil
}

func (w *worker) initializeSourceBoundary(ctx context.Context) error {
	start := model.SecurityState{Name: "source_start", CursorAt: time.Now().Unix()}
	if err := w.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&start).Error; err != nil {
		return err
	}
	if err := w.db.WithContext(ctx).Where("name = ?", start.Name).Take(&start).Error; err != nil {
		return err
	}
	if start.CursorAt <= 0 {
		return errors.New("audit monitoring start time unavailable")
	}
	w.sourceStartAt = start.CursorAt
	return nil
}

func (w *worker) scan(ctx context.Context, state model.SecurityState, cfg operation_setting.AuditSecuritySetting) error {
	now := time.Now().Unix()
	if w.sourceStartAt == 0 {
		if err := w.initializeSourceBoundary(ctx); err != nil {
			return err
		}
	}
	// The overlap handles recently delayed writes, never pre-enable history.
	// Persist the floor once so worker/application restarts retain this boundary.
	if state.CursorAt < w.sourceStartAt {
		state.CursorAt, state.CursorEvent = w.sourceStartAt, ""
	}
	if state.WindowEnd == 0 || state.WindowEnd < state.CursorAt {
		state.WindowEnd = now
	}
	remaining := cfg.IPIntelligenceMaxLookupsPerCycle
	next, err := w.scanPage(ctx, state, cfg, &remaining)
	if err != nil {
		return err
	}
	healthMu.Lock()
	health.LastScan = now
	health.CursorAt = next.CursorAt
	healthMu.Unlock()
	if err := w.retryIPIntelligence(ctx, state, cfg, &remaining); err != nil {
		return err
	}
	code := ""
	if cfg.IPIntelligenceEnabled {
		providers, err := operation_setting.ParseIPIntelligenceProviders(cfg.IPIntelligenceProvidersJSON)
		if err != nil || operation_setting.ActiveIPIntelligenceProvider(providers) == nil {
			code = "invalid_provider_config"
		}
	}
	w.report(code)
	return nil
}

// scanPage commits a bounded page and its own cursor under the scanner lease.
func (w *worker) scanPage(ctx context.Context, state model.SecurityState, cfg operation_setting.AuditSecuritySetting, remaining *int) (model.SecurityState, error) {
	if ctx.Err() != nil {
		return state, ctx.Err()
	}
	query := w.source.WithContext(ctx)
	if w.source.Dialector.Name() == "clickhouse" {
		query = query.WithContext(clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{"output_format_native_write_json_as_string": 1, "output_format_json_quote_64bit_integers": 0})))
	}
	var rows []model.AuditLog
	// Display-only changes can be much larger than the security facts. Exclude
	// them in the source query so they cannot exhaust the evidence budget or
	// enter the worker's memory. Normalize only reads op and admin_info.
	metadata := "json_object('op', json_extract(other, '$.op'), 'admin_info', json_extract(other, '$.admin_info'))"
	switch w.source.Dialector.Name() {
	case "postgres":
		metadata = "CAST(json_build_object('op', other->'op', 'admin_info', other->'admin_info') AS TEXT)"
	case "clickhouse":
		// Literal JSON braces trigger clickhouse-go's named-parameter parser.
		metadata = `concat(char(123), '"op":', coalesce(nullIf(JSONExtractRaw(toJSONString(other), 'op'), ''), 'null'), ',"admin_info":', coalesce(nullIf(JSONExtractRaw(toJSONString(other), 'admin_info'), ''), 'null'), char(125))`
	}
	metadataSize := "length(" + metadata + ")"
	switch w.source.Dialector.Name() {
	case "postgres":
		metadataSize = "octet_length(" + metadata + ")"
	case "sqlite":
		metadataSize = "length(CAST(" + metadata + " AS BLOB))"
	}
	projection := "event_id,user_id,actor_role,created_at,category,action,status,token_ref,auth_method,ip,user_agent,request_id,success,CASE WHEN " + metadataSize + " <= 16384 THEN " + metadata + " ELSE ? END AS other"
	err := query.Select(projection, `{"op":{"params":{"evidence_incomplete":true}}}`).Where("created_at >= ? AND created_at <= ?", state.CursorAt, state.WindowEnd).Where("created_at > ? OR event_id > ?", state.CursorAt, state.CursorEvent).Order("created_at ASC").Order("event_id ASC").Limit(500).Find(&rows).Error
	if err != nil {
		return state, err
	}
	events := make([]model.SecurityEvent, 0, len(rows))
	for _, row := range rows {
		if row.EventId == "" {
			return state, errors.New("audit source missing event identity")
		}
		event := Normalize(row)
		if strings.Contains(event.Facts, `"evidence_incomplete":true`) {
			healthMu.Lock()
			health.HistoryTruncated = true
			healthMu.Unlock()
		}
		events = append(events, event)
	}
	if err := enrichSecurityEvents(ctx, w.db, events, cfg, remaining); err != nil {
		return state, err
	}
	next := state
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next.CursorAt = last.CreatedAt
		next.CursorEvent = last.EventId
	}
	complete := len(rows) < 500
	if complete {
		next.CursorAt = max(state.WindowEnd-120, w.sourceStartAt)
		next.CursorEvent = ""
		next.WindowEnd = 0
	}
	if err = Apply(ctx, w.db, state, events, next, cfg); err != nil {
		return state, err
	}
	return next, nil
}

// Deletes are bounded and only touch this feature's tables. Audit source logs,
// users, channels, billing records and shared caches are never mutated here.
func (w *worker) cleanup(ctx context.Context, cfg operation_setting.AuditSecuritySetting) error {
	now := time.Now().Unix()
	var baselineIDs []int64
	if err := w.db.WithContext(ctx).Model(&model.SecurityBaseline{}).Where("last_seen < ?", now-30*86400).Limit(500).Pluck("id", &baselineIDs).Error; err != nil {
		return err
	}
	if len(baselineIDs) > 0 {
		if err := w.db.WithContext(ctx).Where("id IN ?", baselineIDs).Delete(&model.SecurityBaseline{}).Error; err != nil {
			return err
		}
	}
	var eventIDs []string
	if err := w.db.WithContext(ctx).Model(&model.SecurityEvent{}).Where("processed_at < ?", now-172800).Limit(500).Pluck("event_id", &eventIDs).Error; err != nil {
		return err
	}
	if len(eventIDs) > 0 {
		if err := w.db.WithContext(ctx).Where("event_id IN ?", eventIDs).Delete(&model.SecurityEvent{}).Error; err != nil {
			return err
		}
	}
	if err := model.CleanupSecurityResponseGroups(ctx, w.db); err != nil {
		return err
	}
	if now-w.lastCleanup < 3600 {
		return nil
	}
	drained, err := model.CleanupAuditSecurityAlerts(ctx, w.db, now-int64(cfg.AlertRetentionDays)*86400, now-int64(cfg.ArchiveRetentionDays)*86400)
	if err == nil && drained {
		w.lastCleanup = now
	}
	// Keep draining on later cycles; only an empty candidate query above starts
	// the hourly idle period. No unbounded catch-up loop runs in this cycle.
	return err
}
