package model

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Run before AutoMigrate: an existing users table must never be rebuilt by an
// implicit ADD COLUMN fallback during application startup. Fresh databases get
// the column in their normal CREATE TABLE, without a separate ALTER.
func migrateUserChannelExclusionColumn(db *gorm.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	bounded := db.WithContext(ctx)
	if !bounded.Migrator().HasTable(&User{}) {
		return ctx.Err()
	}
	columns, err := bounded.Migrator().ColumnTypes(&User{})
	if err != nil {
		return fmt.Errorf("check user channel exclusion schema: %w", err)
	}
	for _, column := range columns {
		if column.Name() != "excluded_channel_ids" {
			continue
		}
		if !strings.EqualFold(column.DatabaseTypeName(), "text") {
			return errors.New("users.excluded_channel_ids must be TEXT; complete the pre-deployment migration before startup")
		}
		if nullable, known := column.Nullable(); known && !nullable {
			return errors.New("users.excluded_channel_ids must be nullable; complete the pre-deployment migration before startup")
		}
		return nil
	}

	switch db.Dialector.Name() {
	case "mysql":
		pool, poolErr := db.DB()
		if poolErr != nil {
			return poolErr
		}
		conn, connErr := pool.Conn(ctx)
		if connErr != nil {
			return connErr
		}
		defer conn.Close()
		var previous int64
		if err = conn.QueryRowContext(ctx, "SELECT @@SESSION.lock_wait_timeout").Scan(&previous); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "SET SESSION lock_wait_timeout = 2"); err != nil {
			return err
		}
		// INSTANT is deliberate: unsupported versions/table layouts must fail,
		// never silently copy or rebuild a production users table.
		_, err = conn.ExecContext(ctx, "ALTER TABLE `users` ADD COLUMN `excluded_channel_ids` TEXT NULL, ALGORITHM=INSTANT")
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), time.Second)
		_, restoreErr := conn.ExecContext(restoreCtx, fmt.Sprintf("SET SESSION lock_wait_timeout = %d", previous))
		restoreCancel()
		if restoreErr != nil {
			// Do not return a connection with changed session settings to relay.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			err = errors.Join(err, fmt.Errorf("restore migration connection settings: %w", restoreErr))
		}
	case "postgres":
		err = bounded.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL lock_timeout = '2s'").Error; err != nil {
				return err
			}
			if err := tx.Exec("SET LOCAL statement_timeout = '3s'").Error; err != nil {
				return err
			}
			return tx.Exec(`ALTER TABLE "users" ADD COLUMN "excluded_channel_ids" TEXT NULL`).Error
		})
	case "sqlite":
		err = bounded.Exec("ALTER TABLE `users` ADD COLUMN `excluded_channel_ids` TEXT NULL").Error
	default:
		return fmt.Errorf("unsupported user channel exclusion schema dialect: %s", db.Dialector.Name())
	}
	if err != nil {
		return fmt.Errorf("user channel exclusion startup migration stopped; complete the pre-deployment users.excluded_channel_ids migration or release the blocking transaction before retrying: %w", err)
	}
	return nil
}
