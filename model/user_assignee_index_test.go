package model

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSecurityAssigneeUserIndexMigration(t *testing.T) {
	for _, mode := range []string{"fresh", "previous_release"} {
		t.Run(mode, func(t *testing.T) {
			dsn := os.Getenv("TEST_ASSIGNEE_MIGRATION_DSN")
			if dsn == "" {
				previous := common.SQLitePath
				common.SQLitePath = filepath.Join(t.TempDir(), "assignees.db")
				t.Cleanup(func() { common.SQLitePath = previous })
				dsn = "local"
			} else {
				require.Contains(t, dsn, "/audit_test")
				t.Setenv("ASSIGNEE_INDEX_DB", dsn)
				admin, _, err := chooseDB("ASSIGNEE_INDEX_DB", false)
				require.NoError(t, err)
				pool, err := admin.DB()
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, pool.Close()) })
				name := fmt.Sprintf("audit_test_assignee_index_%d", time.Now().UnixNano())
				require.NoError(t, admin.Exec("CREATE DATABASE "+name).Error)
				t.Cleanup(func() { require.NoError(t, admin.Exec("DROP DATABASE "+name).Error) })
				if strings.HasPrefix(dsn, "postgres") {
					u, err := url.Parse(dsn)
					require.NoError(t, err)
					u.Path = "/" + name
					dsn = u.String()
				} else {
					cfg, err := drivermysql.ParseDSN(dsn)
					require.NoError(t, err)
					cfg.DBName = name
					dsn = cfg.FormatDSN()
				}
			}
			t.Setenv("ASSIGNEE_INDEX_DB", dsn)
			db, _, err := chooseDB("ASSIGNEE_INDEX_DB", false)
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			recorder := &migrationSQLRecorder{}
			db = db.Session(&gorm.Session{Logger: recorder})
			if mode == "previous_release" {
				require.NoError(t, db.Table("users").AutoMigrate(&assigneePreviousReleaseUser{}))
				require.False(t, db.Migrator().HasIndex(&User{}, "idx_users_security_assignees"))
				require.False(t, db.Migrator().HasIndex(&User{}, "idx_users_customer_audit"))
				require.NoError(t, db.Table("users").Create(&assigneePreviousReleaseUser{Id: 1, Username: "retained-admin", Password: "test-hash", Role: 10, Status: 1, Quota: 1000, AffCode: "fixture-invite"}).Error)
			}
			require.NoError(t, db.AutoMigrate(&User{}))
			require.True(t, db.Migrator().HasIndex(&User{}, "idx_users_security_assignees"))
			require.True(t, db.Migrator().HasIndex(&User{}, "idx_users_customer_audit"))
			recorder.reset()
			require.NoError(t, db.AutoMigrate(&User{}))
			require.Empty(t, recorder.schemaMutations(), "second startup must emit no DDL with production dialectors")
			if mode == "previous_release" {
				var user User
				require.NoError(t, db.Take(&user, 1).Error)
				require.Equal(t, "retained-admin", user.Username)
				require.Equal(t, 10, user.Role)
				require.Equal(t, 1000, user.Quota)
			}
		})
	}
}

// Persisted User fields from production reference 127c82331f979f2937cd1035149820eb79e6e1b6.
// Non-persistent fields and methods are omitted; database tags are unchanged.
type assigneePreviousReleaseUser struct {
	Id                        int            `json:"id"`
	Username                  string         `gorm:"unique;index"`
	Password                  string         `gorm:"not null;"`
	DisplayName               string         `gorm:"index"`
	Role                      int            `gorm:"type:int;default:1"`
	Status                    int            `gorm:"type:int;default:1"`
	Email                     string         `gorm:"index"`
	GitHubId                  string         `gorm:"column:github_id;index"`
	DiscordId                 string         `gorm:"column:discord_id;index"`
	OidcId                    string         `gorm:"column:oidc_id;index"`
	WeChatId                  string         `gorm:"column:wechat_id;index"`
	TelegramId                string         `gorm:"column:telegram_id;index"`
	AccessToken               *string        `gorm:"type:char(32);column:access_token;uniqueIndex"`
	AccessTokenCreatedAt      *int64         `gorm:"type:bigint;column:access_token_created_at"`
	Quota                     int            `gorm:"type:int;default:0"`
	UsedQuota                 int            `gorm:"type:int;default:0;column:used_quota"`
	RequestCount              int            `gorm:"type:int;default:0;"`
	Group                     string         `gorm:"type:varchar(64);default:'default'"`
	GroupRatios               string         `gorm:"type:text"`
	ExcludedChannelIDs        string         `gorm:"type:text;column:excluded_channel_ids"`
	AffCode                   string         `gorm:"type:varchar(32);column:aff_code;uniqueIndex"`
	AffCount                  int            `gorm:"type:int;default:0;column:aff_count"`
	AffQuota                  int            `gorm:"type:int;default:0;column:aff_quota"`
	AffHistoryQuota           int            `gorm:"type:int;default:0;column:aff_history"`
	InviterId                 int            `gorm:"type:int;column:inviter_id;index"`
	DeletedAt                 gorm.DeletedAt `gorm:"index"`
	LinuxDOId                 string         `gorm:"column:linux_do_id;index"`
	Setting                   string         `gorm:"type:text;column:setting"`
	Remark                    string         `gorm:"type:varchar(255)"`
	StripeCustomer            string         `gorm:"type:varchar(64);column:stripe_customer;index"`
	CreatedAt                 int64          `gorm:"autoCreateTime;column:created_at"`
	LastLoginAt               int64          `gorm:"default:0;column:last_login_at"`
	AuthVersion               int64          `gorm:"type:bigint;not null;default:1;column:auth_version"`
	StreamResponseTimeout     int            `gorm:"type:int;not null;default:0;column:stream_response_timeout"`
	StreamResponseTimeoutMode string         `gorm:"type:varchar(32);not null;default:'first_output';column:stream_response_timeout_mode"`
	StreamTotalTimeout        int            `gorm:"type:int;not null;default:0;column:stream_total_timeout"`
	NonStreamResponseTimeout  int            `gorm:"type:int;not null;default:0;column:non_stream_response_timeout"`
	NonStreamTotalTimeout     int            `gorm:"type:int;not null;default:0;column:non_stream_total_timeout"`
	NonStreamTimeoutBilling   string         `gorm:"type:varchar(16);not null;default:'refund';column:non_stream_timeout_billing"`
	RetryTimes                int            `gorm:"type:int;not null;default:0;column:retry_times"`
	ProfileVersion            int64          `gorm:"type:bigint;not null;default:0;column:profile_version"`
}
