package model

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

type auditExportPools struct{ main, logs *gorm.DB }

var auditExportReaders atomic.Pointer[auditExportPools]

// Export pools are node-wide and read-only; queries and pool waits use task contexts.
func InitAuditExportPools(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pools := &auditExportPools{}
	ready := false
	defer func() {
		if !ready {
			for _, db := range []*gorm.DB{pools.main, pools.logs} {
				if db != nil {
					_ = closeDB(db)
				}
			}
		}
	}()
	for _, item := range []struct {
		log  bool
		dest **gorm.DB
	}{{false, &pools.main}, {true, &pools.logs}} {
		db, err := openAuditSecurityDB(ctx, item.log, 0)
		if err != nil {
			return err
		}
		*item.dest = db
		pool, err := db.DB()
		if err != nil {
			return err
		}
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
	}
	auditExportReaders.Store(pools)
	ready = true
	return nil
}

func CloseAuditExportPools() {
	if pools := auditExportReaders.Swap(nil); pools != nil {
		for _, db := range []*gorm.DB{pools.main, pools.logs} {
			if pool, err := db.DB(); err == nil {
				_ = pool.Close()
			}
		}
	}
}

func AuditExportDB(logStore bool) (*gorm.DB, error) {
	pools := auditExportReaders.Load()
	if pools == nil {
		return nil, errors.New("audit export pools unavailable")
	}
	if logStore {
		return pools.logs, nil
	}
	return pools.main, nil
}
