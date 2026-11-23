package controller

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func securityQueryContext(c *gin.Context) (context.Context, context.CancelFunc, *gorm.DB) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	db, err := model.AuditReadDB(false)
	if err != nil {
		logger.LogError(c, "audit security storage unavailable")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return ctx, cancel, nil
	}
	return ctx, cancel, db
}

func securityAlertFilter(c *gin.Context) (auditsecurity.AlertFilter, bool) {
	now := time.Now().Unix()
	view := c.DefaultQuery("view", "id")
	f := auditsecurity.AlertFilter{Start: now - 86400, End: now, Limit: 50, View: view, Status: c.Query("status"), Resource: c.Query("resource"), HistoryScope: c.Query("history_scope"), Rule: c.Query("rule"), Cursor: c.Query("cursor"), ViewerID: c.GetInt("id")}
	if view == "activity" {
		var valid bool
		f.Start, f.End, valid = auditCursorTimeRange(c, 86400)
		if !valid {
			return f, false
		}
	}
	if view == "unresolved" || view == "archived" {
		f.Start, f.End = 0, 0
	}
	if view != "id" && view != "handled" && view != "recent_hits" && view != "activity" && view != "unresolved" && view != "archived" {
		return f, false
	}
	startSet, endSet := c.Query("start") != "", c.Query("end") != ""
	beforeSet, limitSet := c.Query("before") != "", c.Query("limit") != ""
	for key, dest := range map[string]*int64{"start": &f.Start, "end": &f.End, "before": &f.Before} {
		if raw := c.Query(key); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return f, false
			}
			*dest = v
		}
	}
	for key, dest := range map[string]*int{"actor_id": &f.ActorID, "severity": &f.Severity} {
		if raw := c.Query(key); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return f, false
			}
			*dest = v
		}
	}
	if raw := c.Query("assignee_id"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return f, false
		}
		f.AssigneeID = &value
	}
	if raw := c.Query("archived"); raw != "" {
		if view != "id" && view != "handled" && view != "activity" || raw != "true" && raw != "false" {
			return f, false
		}
		value := raw == "true"
		f.Archived = &value
	}
	if raw := c.Query("ip"); raw != "" {
		ip, err := netip.ParseAddr(raw)
		if err != nil || len(raw) > 64 {
			return f, false
		}
		f.IP = ip.String()
	}
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return f, false
		}
		f.Limit = v
	}
	if startSet || endSet {
		if f.Start == 0 {
			f.Start = now - 86400
		}
		if f.End == 0 {
			f.End = now
		}
	}
	if f.Start < 0 || f.End < f.Start || f.End-f.Start > 31*86400 || f.Before < 0 || f.ActorID < 0 || f.Severity < 0 || f.Severity > 3 || f.Limit < 1 || f.Limit > 100 {
		return f, false
	}
	if view != "unresolved" && view != "archived" && (f.Start <= 0 || f.End <= 0) {
		return f, false
	}
	if view == "recent_hits" && (beforeSet || f.Cursor != "" || limitSet && f.Limit != 20) {
		return f, false
	}
	if view == "recent_hits" {
		f.Limit = 20
	}
	if view == "activity" && beforeSet || view != "activity" && f.Cursor != "" {
		return f, false
	}
	if f.Status != "" && f.Status != "open" && f.Status != "investigating" && f.Status != "resolved" && f.Status != "false_positive" {
		return f, false
	}
	if view == "unresolved" && f.Status != "" && f.Status != "open" && f.Status != "investigating" {
		return f, false
	}
	if (view == "archived" || view == "handled") && f.Status != "" && f.Status != "resolved" && f.Status != "false_positive" {
		return f, false
	}
	if f.Resource != "" && f.Resource != "channel" && f.Resource != "user" && f.Resource != "account" {
		return f, false
	}
	if f.HistoryScope != "" && (f.HistoryScope != "legacy" || f.Resource != "") {
		return f, false
	}
	if f.Rule != "" && !auditsecurity.ValidSecurityRule(f.Rule) {
		return f, false
	}
	return f, true
}

func GetSecurityAlerts(c *gin.Context) {
	f, ok := securityAlertFilter(c)
	if !ok {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	page, err := auditsecurity.ListAlerts(ctx, db, c.GetInt("role"), f)
	if errors.Is(err, auditsecurity.ErrActivityCursor) {
		common.ApiErrorI18n(c, "audit_security.activity_changed")
		return
	}
	if err != nil {
		logger.LogError(c, "audit security list failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	common.ApiSuccess(c, page)
}

func GetSecuritySummary(c *gin.Context) {
	f, ok := securityAlertFilter(c)
	if !ok || f.View != "id" && f.View != "handled" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	summary, err := auditsecurity.SummarizeAlerts(ctx, db, c.GetInt("role"), f)
	if err != nil {
		logger.LogError(c, "audit security summary failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	common.ApiSuccess(c, summary)
}

func GetSecurityPendingCount(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	count, err := auditsecurity.CountPendingAlerts(ctx, db, c.GetInt("role"))
	if err != nil {
		logger.LogError(c, common.LogText("audit pending count failed: %v", err))
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	common.ApiSuccess(c, struct {
		Count int `json:"count"`
	}{count})
}

func GetSecurityAlert(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	detail, err := auditsecurity.GetAlert(ctx, db, c.GetInt("role"), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorI18n(c, "audit_security.not_found")
		return
	}
	if err != nil {
		logger.LogError(c, "audit security detail failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	common.ApiSuccess(c, detail)
}

func ActOnSecurityAlert(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	var request struct {
		Version             int64   `json:"version"`
		Status              string  `json:"status"`
		Note                string  `json:"note"`
		AssigneeID          *int    `json:"assignee_id"`
		AssigneeIDs         *[]int  `json:"assignee_ids"`
		Conclusion          *string `json:"conclusion"`
		ObservedOccurrences *int64  `json:"observed_occurrences"`
	}
	if common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 8192), &request) != nil || request.Version < 1 || utf8.RuneCountInString(request.Note) > 1000 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if request.ObservedOccurrences != nil && (*request.ObservedOccurrences <= 0 || *request.ObservedOccurrences > request.Version || request.Status != "resolved" || request.Note != "" || request.AssigneeID != nil || request.AssigneeIDs != nil || request.Conclusion != nil) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if request.Status != "" && request.Status != "open" && request.Status != "investigating" && request.Status != "resolved" && request.Status != "false_positive" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if request.Conclusion != nil {
		switch *request.Conclusion {
		case "", "authorized", "expected", "malicious", "false_positive", "other":
		default:
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
	}
	if request.Status == "" && request.Note == "" && request.AssigneeID == nil && request.AssigneeIDs == nil && request.Conclusion == nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if request.AssigneeID != nil && (*request.AssigneeID < 0 || request.AssigneeIDs != nil) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if request.AssigneeIDs != nil {
		seen := map[int]bool{}
		for _, id := range *request.AssigneeIDs {
			if id <= 0 || seen[id] || len(*request.AssigneeIDs) > 10 {
				common.ApiErrorI18n(c, i18n.MsgInvalidParams)
				return
			}
			seen[id] = true
		}
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	alert, err := auditsecurity.UpdateAlertInvestigation(ctx, db, c.GetInt("role"), c.GetInt("id"), auditsecurity.AlertInvestigation{Version: request.Version, Status: request.Status, Note: request.Note, AssigneeID: request.AssigneeID, AssigneeIDs: request.AssigneeIDs, Conclusion: request.Conclusion, ObservedOccurrences: request.ObservedOccurrences}, id)
	if errors.Is(err, auditsecurity.ErrInvalidAlertAssignee) {
		common.ApiErrorI18n(c, "audit_security.invalid_assignee")
		return
	}
	if errors.Is(err, model.ErrSecurityConflict) {
		common.ApiErrorI18n(c, "audit_security.conflict")
		return
	}
	if errors.Is(err, auditsecurity.ErrNoAlertChanges) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err != nil {
		logger.LogError(c, "audit security action failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	recordManageAudit(c, "audit_security.status", map[string]any{"alert_id": id, "status": request.Status})
	common.ApiSuccess(c, alert)
}

func GetSecurityHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	common.ApiSuccess(c, auditsecurity.DetailedHealth(ctx))
}
