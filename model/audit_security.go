package model

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var ErrSecurityConflict = errors.New("audit security state changed")

// SecurityEvent contains allowlisted, non-credential facts only. Raw audit
// metadata is deliberately not copied into the monitoring database.
type SecurityEvent struct {
	TargetRole      int    `json:"target_role,omitempty" gorm:"-:all"`
	TargetUsername  string `json:"target_username,omitempty" gorm:"-:all"`
	ActorUsername   string `json:"actor_username,omitempty" gorm:"-:all"`
	ResponseGroupID *int64 `json:"-"`
	EventID         string `json:"event_id" gorm:"primaryKey;type:varchar(64);index:idx_sec_ip_pending,priority:3"`
	IPCheckState    string `json:"-" gorm:"type:varchar(16);index:idx_sec_ip_pending,priority:1"`
	IPNextAttemptAt int64  `json:"-" gorm:"index:idx_sec_ip_pending,priority:2"`
	At              int64  `json:"at" gorm:"index:idx_sec_event_time,priority:1;index:idx_sec_actor_time,priority:2;index:idx_sec_subject_time,priority:2;index:idx_sec_ip_time,priority:2"`
	ProcessedAt     int64  `json:"-" gorm:"index"`
	ActorID         int    `json:"actor_id" gorm:"index:idx_sec_actor_time,priority:1"`
	ActorRole       int    `json:"actor_role"`
	TargetID        int    `json:"target_id"`
	Visibility      int    `json:"-"`
	ResourceType    string `json:"resource_type" gorm:"type:varchar(16)"`
	SubjectRef      string `json:"subject_ref" gorm:"type:varchar(96);index:idx_sec_subject_time,priority:1"`
	RequestID       string `json:"request_id" gorm:"type:varchar(64)"`
	Action          string `json:"action" gorm:"type:varchar(128)"`
	IP              string `json:"ip" gorm:"type:varchar(64);index:idx_sec_ip_time,priority:1"`
	TokenRef        string `json:"-" gorm:"type:varchar(64)"`
	SessionRef      string `json:"-" gorm:"type:varchar(64)"`
	SourceRef       string `json:"-" gorm:"type:varchar(64)"`
	Success         bool   `json:"success"`
	Reason          string `json:"reason" gorm:"type:varchar(64)"`
	Count           int    `json:"affected_count"`
	Facts           string `json:"facts" gorm:"type:text"`
}

func (SecurityEvent) TableName() string { return "audit_security_events" }

const SecurityEventColumns = "event_id,at,processed_at,actor_id,actor_role,target_id,visibility,resource_type,subject_ref,request_id,action,ip,token_ref,session_ref,source_ref,success,reason,count,facts,ip_check_state,ip_next_attempt_at,response_group_id"

type SecurityAlert struct {
	TargetUserID         *int                      `json:"target_user_id,omitempty" gorm:"index:idx_sec_target_time,priority:1;index:idx_sec_target_pending,priority:1"`
	TargetRole           *int                      `json:"target_role,omitempty"`
	TargetUsername       string                    `json:"target_username,omitempty" gorm:"-:all"`
	RepeatKey            *string                   `json:"-" gorm:"type:varchar(64);index:idx_sec_repeat_episode,priority:1"`
	RepeatUntil          *int64                    `json:"repeat_until,omitempty" gorm:"index:idx_sec_repeat_episode,priority:2"`
	ConfirmedOccurrences *int64                    `json:"confirmed_occurrences,omitempty"`
	AttemptedAccount     string                    `json:"attempted_account,omitempty" gorm:"type:varchar(128)"`
	ActorUsername        string                    `json:"actor_username,omitempty" gorm:"-:all"`
	ManualAssigneeIDs    []int                     `json:"manual_assignee_ids" gorm:"-:all"`
	Assignees            []SecurityAlertAssignee   `json:"assignees" gorm:"-:all"`
	ResponseGroupID      *int64                    `json:"response_group_id,omitempty" gorm:"-:all"`
	CanStop              bool                      `json:"can_stop" gorm:"-:all"`
	Responses            []SecurityAccountResponse `json:"responses,omitempty" gorm:"-:all"`
	Response             *SecurityAccountResponse  `json:"response,omitempty" gorm:"-:all"`
	ID                   int64                     `json:"id" gorm:"index:idx_sec_target_time,priority:3;index:idx_sec_alert_time_id,priority:3;index:idx_sec_alert_root_time_id,priority:2;index:idx_sec_alert_activity_id,priority:3;index:idx_sec_alert_root_activity_id,priority:2;index:idx_sec_cleanup_closed,priority:3;index:idx_sec_purge_id,priority:2;index:idx_sec_customer_time,priority:3;index:idx_sec_repeat_episode,priority:5;index:idx_sec_customer_pending,priority:3;index:idx_sec_target_pending,priority:3;index:idx_sec_alert_pending,priority:5"`
	Rule                 string                    `json:"rule" gorm:"type:varchar(64);index:idx_sec_episode,priority:1;index:idx_sec_episode_v2,priority:1"`
	SubjectRef           string                    `json:"subject_ref" gorm:"type:varchar(96);index:idx_sec_episode,priority:2;index:idx_sec_alert_subject,priority:2;index:idx_sec_episode_v2,priority:2"`
	Visibility           int                       `json:"-" gorm:"index:idx_sec_episode,priority:3;index:idx_sec_alert_time,priority:1;index:idx_sec_alert_status,priority:1;index:idx_sec_alert_resource,priority:1;index:idx_sec_alert_actor,priority:1;index:idx_sec_alert_subject,priority:1;index:idx_sec_episode_v2,priority:3;index:idx_sec_alert_time_id,priority:1;index:idx_sec_alert_activity_id,priority:1;index:idx_sec_alert_assignee,priority:1;index:idx_sec_cleanup_closed,priority:4;index:idx_sec_alert_pending,priority:2"`
	ActorID              int                       `json:"actor_id" gorm:"index:idx_sec_alert_actor,priority:2;index:idx_sec_customer_time,priority:1;index:idx_sec_customer_pending,priority:1"`
	ActorRole            int                       `json:"actor_role"`
	ResourceType         string                    `json:"resource_type" gorm:"type:varchar(16);index:idx_sec_alert_resource,priority:2;index:idx_sec_episode_v2,priority:4"`
	Severity             int                       `json:"severity"`
	Status               string                    `json:"status" gorm:"type:varchar(16);index:idx_sec_alert_status,priority:2;index:idx_sec_cleanup_closed,priority:1;index:idx_sec_customer_pending,priority:2;index:idx_sec_target_pending,priority:2;index:idx_sec_alert_pending,priority:1"`
	FirstSeen            int64                     `json:"first_seen"`
	LastSeen             int64                     `json:"last_seen" gorm:"index:idx_sec_target_time,priority:2;index:idx_sec_episode,priority:4;index:idx_sec_alert_time,priority:2;index:idx_sec_alert_status,priority:3;index:idx_sec_alert_resource,priority:3;index:idx_sec_alert_actor,priority:3;index:idx_sec_alert_subject,priority:3;index;index:idx_sec_episode_v2,priority:6;index:idx_sec_alert_time_id,priority:2;index:idx_sec_alert_root_time_id,priority:1;index:idx_sec_customer_time,priority:2"`
	Occurrences          int64                     `json:"occurrences"`
	MatchedCount         int                       `json:"matched_count"`
	Version              int64                     `json:"version"`
	Evidence             string                    `json:"evidence" gorm:"type:text"`
	NotifiedAt           int64                     `json:"notified_at"`
	RuleVersion          int                       `json:"rule_version" gorm:"index:idx_sec_episode_v2,priority:5"`
	UpdatedAt            int64                     `json:"updated_at" gorm:"index:idx_sec_alert_activity_id,priority:2;index:idx_sec_alert_root_activity_id,priority:1"`
	ClosedAt             *int64                    `json:"closed_at" gorm:"index:idx_sec_cleanup_closed,priority:2"`
	AssigneeID           int                       `json:"assignee_id" gorm:"index:idx_sec_alert_assignee,priority:2"`
	Conclusion           string                    `json:"conclusion" gorm:"type:varchar(32)"`
	ConclusionNote       string                    `json:"conclusion_note" gorm:"type:text"`
	ArchivedAt           *int64                    `json:"archived_at" gorm:"index;index:idx_sec_repeat_episode,priority:4;index:idx_sec_customer_pending,priority:5;index:idx_sec_target_pending,priority:5;index:idx_sec_alert_pending,priority:4"`
	PurgeState           string                    `json:"purge_state" gorm:"index:idx_sec_target_time,priority:4;type:varchar(16);index;index:idx_sec_purge_id,priority:1;index:idx_sec_repeat_episode,priority:3;index:idx_sec_customer_pending,priority:4;index:idx_sec_target_pending,priority:4;index:idx_sec_alert_pending,priority:3"`
	LatestFindingID      int64                     `json:"latest_finding_id"`
}

type SecurityAccountResponse struct {
	ResponseGroupID *int64 `json:"response_group_id,omitempty"`
	Action          string `json:"action"`
	AffectedCount   *int64 `json:"affected_count,omitempty"`
	ID              int64  `json:"id"`
	Source          string `json:"source"`
	Outcome         string `json:"outcome"`
	ReasonCode      string `json:"reason_code"`
	TargetUserID    int    `json:"target_user_id"`
	CreatedAt       int64  `json:"created_at"`
}

func (SecurityAlert) TableName() string { return "audit_security_alerts" }

// Only manual assignments are persisted. The current customer owner is derived
// at read time and never grants access to a management endpoint.
type SecurityAssignee struct {
	AlertID int64 `gorm:"primaryKey;autoIncrement:false;index:idx_sec_assignee_user,priority:2"`
	UserID  int   `gorm:"primaryKey;autoIncrement:false;index:idx_sec_assignee_user,priority:1"`
}

func (SecurityAssignee) TableName() string { return "audit_security_assignees" }

type SecurityAlertAssignee struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     int    `json:"role"`
	Source   string `json:"source"`
}

type SecurityAction struct {
	ActorUsername        string                  `json:"actor_username,omitempty" gorm:"-:all"`
	AccountNotifyPending bool                    `json:"-" gorm:"index:idx_sec_account_notify,priority:1"`
	Assignees            []SecurityAlertAssignee `json:"assignees,omitempty" gorm:"-:all"`
	ResponseGroupID      *int64                  `json:"response_group_id,omitempty" gorm:"index:idx_sec_action_group,priority:1"`
	AffectedCount        *int64                  `json:"affected_count,omitempty" gorm:"-:all"`
	ExecutionKey         *string                 `json:"-" gorm:"type:varchar(96);uniqueIndex:idx_sec_response_execution"`
	Outcome              string                  `json:"outcome,omitempty" gorm:"type:varchar(24);index:idx_sec_response_due,priority:1;index:idx_sec_response_target,priority:2"`
	TargetUserID         int                     `json:"target_user_id,omitempty" gorm:"index:idx_sec_response_target,priority:1"`
	ReasonCode           string                  `json:"reason_code,omitempty" gorm:"type:varchar(48)"`
	NextAttemptAt        int64                   `json:"-" gorm:"index:idx_sec_response_due,priority:2"`
	Attempts             int                     `json:"-"`
	Result               string                  `json:"-" gorm:"type:text"`
	ID                   int64                   `json:"id" gorm:"index:idx_sec_action_cursor,priority:3;index:idx_sec_response_due,priority:3;index:idx_sec_response_alert,priority:3;index:idx_sec_action_group,priority:3;index:idx_sec_response_target,priority:3;index:idx_sec_account_notify,priority:2"`
	AlertID              int64                   `json:"alert_id" gorm:"index:idx_sec_action_alert,priority:1;index:idx_sec_action_cursor,priority:1;index:idx_sec_response_alert,priority:1"`
	ActorID              int                     `json:"actor_id"`
	ActorRole            int                     `json:"actor_role"`
	Action               string                  `json:"action" gorm:"type:varchar(32);index:idx_sec_response_alert,priority:2;index:idx_sec_action_group,priority:2"`
	Note                 string                  `json:"note" gorm:"type:text"`
	CreatedAt            int64                   `json:"created_at" gorm:"index:idx_sec_action_alert,priority:2;index;index:idx_sec_action_cursor,priority:2"`
	Source               string                  `json:"source" gorm:"type:varchar(16)"`
	FromStatus           string                  `json:"from_status" gorm:"type:varchar(16)"`
	ToStatus             string                  `json:"to_status" gorm:"type:varchar(16)"`
	EventID              string                  `json:"event_id" gorm:"type:varchar(64)"`
	EventAt              int64                   `json:"event_at"`
	DetectedAt           int64                   `json:"detected_at"`
	AssigneeID           int                     `json:"assignee_id"`
	Conclusion           string                  `json:"conclusion" gorm:"type:varchar(32)"`
}

func (SecurityAction) TableName() string { return "audit_security_actions" }

type SecurityDelivery struct {
	RecipientUserID int     `json:"-" gorm:"index:idx_sec_account_cooldown,priority:2;index:idx_sec_account_sent,priority:2"`
	DeliveryKey     *string `json:"-" gorm:"type:varchar(96);uniqueIndex:idx_sec_account_delivery"`
	ID              int64   `json:"id"`
	AlertID         int64   `json:"alert_id" gorm:"index;index:idx_sec_account_cooldown,priority:1;index:idx_sec_account_sent,priority:1"`
	FindingID       int64   `json:"finding_id" gorm:"index"`
	Version         int64   `json:"version"`
	Destination     string  `json:"-" gorm:"type:text"`
	Kind            string  `json:"kind" gorm:"type:varchar(16)"`
	Status          string  `json:"status" gorm:"type:varchar(16);index:idx_sec_delivery_due,priority:1"`
	Attempts        int     `json:"attempts"`
	NextAttemptAt   int64   `json:"next_attempt_at" gorm:"index:idx_sec_delivery_due,priority:2"`
	CreatedAt       int64   `json:"created_at" gorm:"index;index:idx_sec_account_cooldown,priority:3"`
	UpdatedAt       int64   `json:"updated_at" gorm:"index"`
	SentAt          int64   `json:"sent_at" gorm:"index;index:idx_sec_account_sent,priority:3"`
	ErrorCode       string  `json:"error_code" gorm:"type:varchar(48)"`
	WaitReason      string  `json:"wait_reason" gorm:"type:varchar(48)"`
}

func (SecurityDelivery) TableName() string { return "audit_security_deliveries" }

type SecurityState struct {
	Name        string `gorm:"primaryKey;type:varchar(48)"`
	Owner       string `gorm:"type:varchar(64)"`
	Fence       int64
	ExpiresAt   int64
	CursorAt    int64
	CursorEvent string `gorm:"type:varchar(64)"`
	WindowEnd   int64
	LastScan    int64
	LastRepair  int64 // Retained schema only; historical source repair was removed.
	CommitSeq   int64
}

func (SecurityState) TableName() string { return "audit_security_states" }

type SecurityBaseline struct {
	ID        int64
	AccountID int    `gorm:"uniqueIndex:idx_sec_baseline,priority:1;index:idx_sec_baseline_maturity,priority:1"`
	Kind      string `gorm:"type:varchar(16);index:idx_sec_baseline_maturity,priority:2"`
	SourceRef string `gorm:"type:varchar(64);uniqueIndex:idx_sec_baseline,priority:2"`
	FirstSeen int64  `gorm:"index:idx_sec_baseline_maturity,priority:3"`
	LastSeen  int64  `gorm:"index"`
}

func (SecurityBaseline) TableName() string { return "audit_security_baselines" }

// OpenAuditSecurityDB opens a separate pool without modifying DB/LOG_DB flags,
// migrations, shared limits, or authentication caches.
func OpenAuditSecurityDB(ctx context.Context, logStore bool) (*gorm.DB, error) {
	return openAuditSecurityDB(ctx, logStore, 5*time.Second)
}

func openAuditSecurityDB(ctx context.Context, logStore bool, readTimeout time.Duration) (*gorm.DB, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var pool *sql.DB
	ready := false
	defer func() {
		if !ready && pool != nil {
			_ = pool.Close()
		}
	}()
	env := "SQL_DSN"
	if logStore && os.Getenv("LOG_SQL_DSN") != "" {
		env = "LOG_SQL_DSN"
	}
	dsn := os.Getenv(env)
	var dialect gorm.Dialector
	cfg := newGormConfig(false)
	cfg.Logger = logger.Default.LogMode(logger.Silent)
	cfg.DisableAutomaticPing = true
	switch {
	case isClickHouseDSN(dsn):
		if !logStore {
			return nil, errors.New("unsupported monitoring primary database")
		}
		u, err := url.Parse(normalizeClickHouseDSN(dsn))
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("dial_timeout", "3s")
		q.Set("read_timeout", readTimeout.String())
		u.RawQuery = q.Encode()
		pool, err = sql.Open("clickhouse", u.String())
		if err != nil {
			return nil, err
		}
		dialect = clickhouse.New(clickhouse.Config{DSN: u.String(), Conn: auditInitPool{pool, ctx}})
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("connect_timeout", "3")
		u.RawQuery = q.Encode()
		dialect = postgresMigrationDialector{postgres.Dialector{Config: &postgres.Config{DSN: u.String(), PreferSimpleProtocol: true}}}
	case dsn == "", strings.HasPrefix(dsn, "local"):
		var err error
		pool, err = sql.Open("sqlite", common.SQLitePath)
		if err != nil {
			return nil, err
		}
		dialect = sqlite.Dialector{Conn: auditInitPool{pool, ctx}}
	default:
		parsed, err := drivermysql.ParseDSN(dsn)
		if err != nil {
			return nil, err
		}
		parsed.ParseTime = true
		parsed.Timeout = 3 * time.Second
		parsed.ReadTimeout = readTimeout
		parsed.WriteTimeout = 5 * time.Second
		pool, err = sql.Open("mysql", parsed.FormatDSN())
		if err != nil {
			return nil, err
		}
		dialect = mysqlMigrationDialector{mysql.Dialector{Config: &mysql.Config{DSN: parsed.FormatDSN(), Conn: auditInitPool{pool, ctx}}}}
	}
	db, err := gorm.Open(dialect, cfg)
	if err != nil {
		return nil, err
	}
	// Drivers probe versions with Background contexts. The temporary wrapper
	// bounds that probe; runtime queries must use their own request/task contexts.
	if pool != nil {
		db.ConnPool = pool
		db.Statement.ConnPool = pool
	} else {
		pool, err = db.DB()
		if err != nil {
			return nil, err
		}
	}
	db.Logger = logger.Default.LogMode(logger.Silent) // Never print bound destination/credential values.
	max := 2
	if logStore {
		max = 1
	}
	pool.SetMaxOpenConns(max)
	pool.SetMaxIdleConns(max)
	pool.SetConnMaxLifetime(5 * time.Minute)
	if err := pool.PingContext(ctx); err != nil {
		return nil, err
	}
	ready = true
	return db, nil
}

type auditInitPool struct {
	*sql.DB
	ctx context.Context
}

func (p auditInitPool) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	return p.DB.QueryRowContext(p.ctx, query, args...)
}

func MigrateAuditSecurity(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).AutoMigrate(
		&SecurityEvent{}, &SecurityAlert{}, &SecurityAssignee{}, &SecurityAction{}, &SecurityResponseGroup{}, &SecurityResponseLink{}, &SecurityDelivery{}, &SecurityState{}, &SecurityBaseline{},
		&SecurityAlertEvent{}, &SecurityRiskState{}, &SecurityFindingSnapshot{}, &SecurityActivityState{},
		&SecurityCleanupState{}, &SecurityGeoBaseline{}, &SecurityIPProviderState{},
	); err != nil {
		return err
	}
	if db.Dialector.Name() == "mysql" {
		// Anonymous input can contain any Unicode account name, even when the
		// schema default only supports a narrower set of registered usernames.
		// Restrict charset changes to these new, non-indexed evidence columns.
		for _, table := range []string{"audit_security_alerts", "audit_security_findings"} {
			var charset string
			if err := db.WithContext(ctx).Table("information_schema.COLUMNS").Select("CHARACTER_SET_NAME").
				Where("TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?", table, "attempted_account").Scan(&charset).Error; err != nil {
				return err
			}
			if charset == "utf8mb4" {
				continue
			}
			if err := db.WithContext(ctx).Exec("ALTER TABLE `" + table + "` MODIFY COLUMN `attempted_account` VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL").Error; err != nil {
				return err
			}
		}
	}
	if err := db.WithContext(ctx).Where("name = ?", "risk").FirstOrCreate(&SecurityRiskState{Name: "risk"}).Error; err != nil {
		return err
	}
	if err := db.WithContext(ctx).Where("name = ?", "activity").FirstOrCreate(&SecurityActivityState{Name: "activity"}).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Where("name = ?", "alerts").FirstOrCreate(&SecurityCleanupState{Name: "alerts"}).Error
}

// ClaimAuditSecurity serializes only the monitoring singleton. The fencing
// increment is checked again in the same transaction that commits each batch.
func ClaimAuditSecurity(ctx context.Context, db *gorm.DB, owner string) (SecurityState, error) {
	var state SecurityState
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var now int64
		if err := tx.Raw(securityDatabaseTimeSQL(tx)).Scan(&now).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&SecurityState{Name: "scanner"}).Error; err != nil {
			return err
		}
		r := tx.Model(&SecurityState{}).Where("name = ? AND (owner = ? OR expires_at < ?)", "scanner", owner, now).Updates(map[string]any{"owner": owner, "expires_at": now + 30, "fence": gorm.Expr("fence + 1")})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrSecurityConflict
		}
		return tx.Where("name = ?", "scanner").Take(&state).Error
	})
	return state, err
}

func securityDatabaseTimeSQL(db *gorm.DB) string {
	switch db.Dialector.Name() {
	case "postgres":
		return "SELECT CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)"
	case "mysql":
		return "SELECT UNIX_TIMESTAMP()"
	default:
		return "SELECT CAST(strftime('%s','now') AS INTEGER)"
	}
}

func TouchAuditSecurityLease(tx *gorm.DB, state SecurityState) error {
	var now int64
	if err := tx.Raw(securityDatabaseTimeSQL(tx)).Scan(&now).Error; err != nil {
		return err
	}
	r := tx.Model(&SecurityState{}).Where("name = ? AND owner = ? AND fence = ? AND expires_at >= ?", "scanner", state.Owner, state.Fence, now).Updates(map[string]any{"last_scan": now, "commit_seq": gorm.Expr("commit_seq + 1")})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrSecurityConflict
	}
	return nil
}

type SecurityFinding struct {
	Rule, SubjectRef string
	Severity         int
	Count            int
	Threshold        int
	WindowSeconds    int
	Dimensions       SecurityDimensions
	Truncated        bool
	// Correlated findings belong to the account whose own authenticated event
	// supplies the evidence, not necessarily the last actor in a shared source.
	TargetEvent *SecurityEvent
	Evidence    []SecurityEvent
}

// SecurityVisibleQuery is shared by list, detail, history and status mutation.
func SecurityVisibleQuery(db *gorm.DB, role int) *gorm.DB {
	if role < common.RoleRootUser {
		return db.Where("visibility = ?", common.RoleAdminUser)
	}
	return db
}
