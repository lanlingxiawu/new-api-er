package controller

import (
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Cursor-backed audit APIs pin their first query window. Only omitted bounds
// are restored here; each reader still validates the full cursor binding,
// identity, revision and position before reading any records.
func auditCursorTimeRange(c *gin.Context, defaultSeconds int64) (int64, int64, bool) {
	end := time.Now().Unix()
	start := end - defaultSeconds
	if cursor := c.Query("cursor"); cursor != "" {
		if len(cursor) > 3000 {
			return 0, 0, false
		}
		var window struct{ Start, End int64 }
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || common.Unmarshal(raw, &window) != nil {
			return 0, 0, false
		}
		if window.Start > 0 && window.End >= window.Start {
			start, end = window.Start, window.End
		}
	}
	for key, target := range map[string]*int64{"start": &start, "end": &end} {
		if raw := c.Query(key); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			*target = value
		}
	}
	return start, end, start > 0 && end >= start && end-start <= 31*86400
}

func parseAuditFilter(c *gin.Context, bounded bool) (model.AuditLogFilter, bool) {
	f := model.AuditLogFilter{Username: c.Query("username"), Category: c.Query("category"), TokenRef: c.Query("token_ref"), ExcludeTokenRef: c.Query("exclude_token_ref"), RequestId: c.Query("request_id"), IP: strings.TrimSpace(c.Query("ip")), ActorType: c.Query("actor_type"), AuthMethod: c.Query("auth_method"), Route: c.Query("route")}
	if strings.HasPrefix(c.FullPath(), "/api/audit/self") {
		f.UserId = c.GetInt("id")
		f.Username = ""
		f.SelfView = true
	} else if mine := c.Query("mine"); mine != "" {
		// Management projection limited to the signed-in account. Callers cannot
		// pair it with a username, and /api/audit/self keeps the public activity DTO.
		if mine != "true" || f.Username != "" || c.GetInt("id") <= 0 {
			return f, false
		}
		f.UserId = c.GetInt("id")
	}
	if !validAuditFilterFields(f) || !model.CanFilterAuditActor(f.ActorType, c.GetInt("role")) {
		return f, false
	}
	if raw := c.Query("route_missing"); raw != "" {
		if raw != "true" || f.Route != "" {
			return f, false
		}
		f.RouteMissing = true
	}
	if f.SelfView && !validAuditActivityFilter(f) {
		return f, false
	}
	for name, target := range map[string]*int64{"start_timestamp": &f.StartTimestamp, "end_timestamp": &f.EndTimestamp} {
		if raw := c.Query(name); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v < 0 {
				return f, false
			}
			*target = v
		}
	}
	if f.EndTimestamp > 0 && f.EndTimestamp < f.StartTimestamp {
		return f, false
	}
	if bounded && (f.StartTimestamp <= 0 || f.EndTimestamp < f.StartTimestamp || f.EndTimestamp-f.StartTimestamp > 31*86400) {
		return f, false
	}
	if raw := c.Query("success"); raw != "" {
		if raw != "true" && raw != "false" {
			return f, false
		}
		v := raw == "true"
		f.Success = &v
	}
	return f, true
}

var auditReadDatabase = model.AuditReadDB

func auditQueryDB(c *gin.Context, logStore bool) (*gorm.DB, bool) {
	db, err := auditReadDatabase(logStore)
	if err != nil {
		logger.LogError(c, "audit read pool unavailable")
		common.ApiErrorI18n(c, i18n.MsgAuditFilterUnavailable)
		return nil, false
	}
	return db, true
}

func GetAuditRoutes(c *gin.Context) {
	f, ok := parseAuditFilter(c, true)
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		ok = ok && err == nil
	}
	prefix, after := c.Query("q"), c.Query("after_route")
	if !ok || limit < 1 || limit > 100 || utf8.RuneCountInString(prefix) > 255 || utf8.RuneCountInString(after) > 255 {
		common.ApiErrorI18n(c, i18n.MsgAuditFilterInvalid)
		return
	}
	// Options depend only on scope and submitted time, never on the current page.
	if f.SelfView {
		common.ApiSuccess(c, model.AuditRoutePage{Items: []string{}})
		return
	}
	f = model.AuditLogFilter{SelfView: f.SelfView, UserId: f.UserId, StartTimestamp: f.StartTimestamp, EndTimestamp: f.EndTimestamp}
	db, ok := auditQueryDB(c, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	result, err := model.QueryAuditRoutes(db.WithContext(ctx), f, c.GetInt("role"), prefix, after, limit)
	if err != nil {
		logger.LogError(c, common.LogText("audit route query failed: %v", err))
		common.ApiErrorI18n(c, i18n.MsgAuditFilterUnavailable)
		return
	}
	common.ApiSuccess(c, result)
}

func auditBatchRecords(c *gin.Context, ctx context.Context) ([]*model.AuditLog, bool) {
	f, ok := parseAuditFilter(c, true)
	ids := strings.Split(c.Query("event_ids"), ",")
	if !ok || len(ids) > 100 || len(ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgAuditFilterInvalid)
		return nil, false
	}
	for _, id := range ids {
		if id == "" || len(id) > 64 {
			common.ApiErrorI18n(c, i18n.MsgAuditFilterInvalid)
			return nil, false
		}
	}
	db, ok := auditQueryDB(c, true)
	if !ok {
		return nil, false
	}
	rows, err := model.QueryAuditEvents(db.WithContext(ctx), f, c.GetInt("role"), ids)
	if err != nil {
		logger.LogError(c, common.LogText("audit event lookup failed: %v", err))
		common.ApiErrorI18n(c, i18n.MsgAuditFilterUnavailable)
		return nil, false
	}
	return rows, true
}

func GetAuditIPMarkers(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	rows, ok := auditBatchRecords(c, ctx)
	if !ok {
		return
	}
	db, ok := auditQueryDB(c, false)
	if !ok {
		return
	}
	markers, err := auditsecurity.RiskMarkers(ctx, db, rows, c.GetInt("role"))
	if err != nil {
		logger.LogError(c, common.LogText("audit IP marker lookup failed: %v", err))
		common.ApiErrorI18n(c, i18n.MsgAuditFilterUnavailable)
		return
	}
	common.ApiSuccess(c, gin.H{"items": markers, "coverage": auditsecurity.CurrentRiskCoverage()})
}

func GetAuditIPRecords(c *gin.Context) {
	f, ok := parseAuditFilter(c, true)
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		ok = ok && err == nil
	}
	risk := c.Query("ip_risk")
	if !ok || limit < 1 || limit > 100 || (risk != "abnormal" && risk != "attention" && risk != "high") || len(c.Query("cursor")) > 3000 {
		common.ApiErrorI18n(c, i18n.MsgAuditFilterInvalid)
		return
	}
	db, ok := auditQueryDB(c, false)
	if !ok {
		return
	}
	logs, ok := auditQueryDB(c, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	page, err := auditsecurity.QueryRiskRecords(ctx, db, logs, f, c.GetInt("role"), c.GetInt("id"), risk, c.Query("cursor"), limit)
	if errors.Is(err, auditsecurity.ErrRiskCursor) {
		common.ApiErrorI18n(c, i18n.MsgAuditFilterChanged)
		return
	}
	if err != nil {
		logger.LogError(c, common.LogText("audit risk query failed: %v", err))
		common.ApiErrorI18n(c, i18n.MsgAuditFilterUnavailable)
		return
	}
	common.ApiSuccess(c, page)
}

func validAuditFilterFields(f model.AuditLogFilter) bool {
	if !model.ValidAuditCategory(f.Category) || !model.ValidTokenFingerprint(f.TokenRef) || !model.ValidTokenFingerprint(f.ExcludeTokenRef) || !model.ValidAuditActor(f.ActorType) {
		return false
	}
	if len(f.IP) > 64 {
		return false
	}
	if f.IP != "" {
		addr, err := netip.ParseAddr(f.IP)
		if err != nil || addr.Zone() != "" {
			return false
		}
	}
	if f.AuthMethod != "" && f.AuthMethod != "session" && f.AuthMethod != "access_token" && f.AuthMethod != "unknown" {
		return false
	}
	if utf8.RuneCountInString(f.Route) > 255 || strings.ContainsAny(f.Route, "?\r\n") || f.Route != "" && !strings.HasPrefix(f.Route, "/") {
		return false
	}
	return !(f.RouteMissing && f.Route != "") && len(f.Username) <= 256 && len(f.RequestId) <= 256
}

// Current/historical PAT selection remains available on the access-token page.
// Actor and route filters belong to the management audit contract only.
func validAuditActivityFilter(f model.AuditLogFilter) bool {
	return f.ActorType == "" && f.Route == "" && !f.RouteMissing &&
		(f.TokenRef == "" && f.ExcludeTokenRef == "" || f.Category == model.AuditCategoryAccessToken) &&
		(f.TokenRef == "" || f.ExcludeTokenRef == "")
}
