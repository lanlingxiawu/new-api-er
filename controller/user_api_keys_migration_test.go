package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type userAPIKeysMigrationLog struct {
	logger.Interface
	ddl []string
}

func (l *userAPIKeysMigrationLog) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "ALTER ") || strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "DROP ") {
		l.ddl = append(l.ddl, sql)
	}
}

func TestUserAPIKeysMigrationAndQueryPlans(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade=%t", upgrade), func(t *testing.T) {
			// Each fixture owns an isolated database; no shared tables are dropped.
			setupSecurityEnrollmentTest(t)
			db := model.DB
			require.NoError(t, db.Migrator().CreateTable(&model.Token{}))
			if upgrade {
				// The only schema difference from the previous Token is this index.
				require.NoError(t, db.Migrator().DropIndex(&model.Token{}, "idx_tokens_user_deleted_id"))
				assert.False(t, db.Migrator().HasIndex(&model.Token{}, "idx_tokens_user_deleted_id"))
				require.NoError(t, db.AutoMigrate(&model.Token{}))
			}
			assert.True(t, db.Migrator().HasIndex(&model.Token{}, "idx_tokens_user_deleted_id"))
			log := &userAPIKeysMigrationLog{Interface: logger.Default}
			for range 2 {
				require.NoError(t, db.Session(&gorm.Session{Logger: log}).AutoMigrate(&model.Token{}))
			}
			assert.Empty(t, log.ddl, "repeated startup must not alter the tokens schema")
			if !upgrade {
				return
			}
			tokens := make([]model.Token, 10000)
			for i := range tokens {
				tokens[i] = model.Token{UserId: i%100 + 1, Key: fmt.Sprintf("index-fixture-%d", i)}
				// A growing token table retains deleted history. Exercise the
				// composite index's live-row filtering, not just an empty table.
				if i < 9000 {
					tokens[i].DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
				}
			}
			require.NoError(t, db.CreateInBatches(&tokens, 250).Error)
			if db.Dialector.Name() == "mysql" {
				require.NoError(t, db.Exec("ANALYZE TABLE tokens").Error)
			} else {
				require.NoError(t, db.Exec("ANALYZE tokens").Error)
			}
			queries := []string{
				db.ToSQL(func(tx *gorm.DB) *gorm.DB {
					return tx.Select("id").Where("user_id = ?", 2).Order("deleted_at DESC, id DESC").Limit(1).Find(&model.Token{})
				}),
				db.ToSQL(func(tx *gorm.DB) *gorm.DB {
					return tx.Select("id").Where("user_id = ?", 2).Order("deleted_at, id").Limit(100).Find(&[]model.Token{})
				}),
			}
			for _, query := range queries {
				prefix := "EXPLAIN "
				if db.Dialector.Name() == "sqlite" {
					prefix = "EXPLAIN QUERY PLAN "
				}
				rows, err := db.Raw(prefix + query).Rows()
				require.NoError(t, err)
				columns, err := rows.Columns()
				require.NoError(t, err)
				var plan strings.Builder
				for rows.Next() {
					values := make([]any, len(columns))
					pointers := make([]any, len(columns))
					for i := range values {
						pointers[i] = &values[i]
					}
					require.NoError(t, rows.Scan(pointers...))
					for i, value := range values {
						if bytes, ok := value.([]byte); ok {
							value = string(bytes)
						}
						fmt.Fprintf(&plan, "%s=%v ", columns[i], value)
					}
				}
				require.NoError(t, rows.Err())
				require.NoError(t, rows.Close())
				t.Logf("%s\n%s", query, plan.String())
				assert.Contains(t, plan.String(), "idx_tokens_user_deleted_id")
			}
		})
	}
}
