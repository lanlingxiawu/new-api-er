package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const AuditLogColumns = "id,event_id,user_id,username,actor_role,actor_is_employee,created_at,category,action,token_ref,auth_method,ip,user_agent,method,route,status,success,request_id,content,other"

type auditReadPools struct{ main, logs, employees *gorm.DB }

var auditReaders atomic.Pointer[auditReadPools]

// Dedicated pools never borrow relay or security-worker connections. Called once at startup.
func InitAuditReadPools(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pools := &auditReadPools{}
	ready := false
	defer func() {
		if !ready {
			for _, db := range []*gorm.DB{pools.main, pools.logs, pools.employees} {
				if db != nil {
					_ = closeDB(db)
				}
			}
		}
	}()
	for _, item := range []struct {
		log  bool
		dest **gorm.DB
	}{{false, &pools.main}, {true, &pools.logs}, {false, &pools.employees}} {
		db, err := OpenAuditSecurityDB(ctx, item.log)
		if err != nil {
			return err
		}
		*item.dest = db
		pool, err := db.DB()
		if err != nil {
			return err
		}
		pool.SetMaxOpenConns(2)
		pool.SetMaxIdleConns(2)
	}
	auditReaders.Store(pools)
	ready = true
	return nil
}

func CloseAuditReadPools() {
	if pools := auditReaders.Swap(nil); pools != nil {
		for _, db := range []*gorm.DB{pools.main, pools.logs, pools.employees} {
			if pool, err := db.DB(); err == nil {
				_ = pool.Close()
			}
		}
	}
}

func AuditReadDB(logStore bool) (*gorm.DB, error) {
	pools := auditReaders.Load()
	if pools == nil {
		return nil, errors.New("audit read pools unavailable")
	}
	if logStore {
		return pools.logs, nil
	}
	return pools.main, nil
}

func AuditEmployeeSnapshot(c *gin.Context, actorID int) *bool {
	if actorID <= 0 || c == nil || c.Request == nil || !strings.HasPrefix(c.Request.URL.Path, "/api/") {
		return nil
	}
	key := "audit_employee_snapshot:" + strconv.Itoa(actorID)
	if saved, ok := c.Get(key); ok {
		return saved.(*bool)
	}
	var result *bool
	defer func() { c.Set(key, result) }()
	pools := auditReaders.Load()
	if pools == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
	defer cancel()
	var profile EmployeeProfile
	err := pools.employees.WithContext(ctx).Select("status").Where("user_id = ?", actorID).Take(&profile).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.LogError(c, "audit employee snapshot unavailable")
		return nil
	}
	enabled := err == nil && profile.Status == 1
	result = &enabled
	return result
}

type AuditRoutePage struct {
	Items      []string `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func QueryAuditRoutes(db *gorm.DB, filter AuditLogFilter, role int, prefix, after string, limit int) (AuditRoutePage, error) {
	page := AuditRoutePage{Items: []string{}}
	query := AuditLogQuery(db, filter, role).Where("route <> ?", "")
	// A literal prefix avoids LIKE wildcard escaping differences between SQL dialects.
	if prefix != "" {
		query = query.Where("route >= ? AND SUBSTR(route, 1, ?) = ?", prefix, len([]rune(prefix)), prefix)
	}
	if after != "" {
		query = query.Where("route > ?", after)
	}
	err := query.Distinct("route").Order("route ASC").Limit(limit+1).Pluck("route", &page.Items).Error
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = page.Items[limit-1]
	}
	return page, err
}

func QueryAuditEvents(db *gorm.DB, filter AuditLogFilter, role int, ids []string) ([]*AuditLog, error) {
	rows := make([]*AuditLog, 0)
	if len(ids) == 0 {
		return rows, nil
	}
	if len(ids) > 100 {
		return nil, errors.New("too many audit events")
	}
	err := AuditLogQuery(db, filter, role).Select(AuditLogColumns).Where("event_id IN ?", ids).Order("created_at DESC").Order("event_id DESC").Limit(100).Find(&rows).Error
	ProjectAuditLogs(rows, filter.SelfView, role)
	return rows, err
}

func ValidAuditActor(value string) bool {
	switch value {
	case "", "root", "admin", "employee", "user", "unclassified_user", "unknown":
		return true
	default:
		return false
	}
}

// CanFilterAuditActor applies the viewer's current role to identity choices.
// Unknown historical roles remain root-only because their privilege is unknown.
func CanFilterAuditActor(value string, viewerRole int) bool {
	switch value {
	case "":
		return true
	case "root", "unknown":
		return viewerRole >= common.RoleRootUser
	case "admin":
		return viewerRole >= common.RoleAdminUser
	case "employee", "user", "unclassified_user":
		return viewerRole >= common.RoleCommonUser
	default:
		return false
	}
}
