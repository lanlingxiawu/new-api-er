package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	AuditCategoryLogin       = "login"
	AuditCategorySecurity    = "security"
	AuditCategoryOperation   = "operation"
	AuditCategoryAccessToken = "access_token"
)

// AuditLog is retained independently of usage logs and their cleanup/TTL policy.
// TokenRef identifies a PAT generation without storing its bearer credential.
type AuditLog struct {
	Id              int        `json:"id"`
	EventId         string     `json:"event_id" gorm:"type:varchar(64);uniqueIndex;index:idx_audit_time_event,priority:2;index:idx_audit_ip_time,priority:3;index:idx_audit_actor_time,priority:3;index:idx_audit_employee_time,priority:4;index:idx_audit_auth_time,priority:3;index:idx_audit_route_time,priority:3;index:idx_audit_customer_cursor,priority:3"`
	UserId          int        `json:"user_id" gorm:"index:idx_audit_user_time,priority:1;index:idx_audit_user_route,priority:1;index:idx_audit_customer_cursor,priority:1"`
	Username        string     `json:"username" gorm:"type:varchar(64);index"`
	ActorRole       int        `json:"actor_role" gorm:"index:idx_audit_actor_time,priority:1;index:idx_audit_employee_time,priority:1;index:idx_audit_route_time,priority:4;index:idx_audit_user_route,priority:4"`
	ActorIsEmployee *bool      `json:"actor_is_employee" gorm:"index:idx_audit_employee_time,priority:2"`
	CreatedAt       int64      `json:"created_at" gorm:"type:bigint;index:idx_audit_user_time,priority:2;index:idx_audit_token_time,priority:2;index;index:idx_audit_time_event,priority:1;index:idx_audit_ip_time,priority:2;index:idx_audit_actor_time,priority:2;index:idx_audit_employee_time,priority:3;index:idx_audit_auth_time,priority:2;index:idx_audit_route_time,priority:2;index:idx_audit_user_route,priority:3;index:idx_audit_customer_cursor,priority:2"`
	Category        string     `json:"category" gorm:"type:varchar(24);index"`
	Action          string     `json:"action" gorm:"type:varchar(128)"`
	TokenRef        string     `json:"token_ref" gorm:"type:varchar(64);index:idx_audit_token_time,priority:1"`
	AuthMethod      string     `json:"auth_method" gorm:"type:varchar(24);index:idx_audit_auth_time,priority:1"`
	Ip              string     `json:"ip" gorm:"type:varchar(64);index:idx_audit_ip_time,priority:1"`
	UserAgent       string     `json:"user_agent" gorm:"type:varchar(512)"`
	Method          string     `json:"method" gorm:"type:varchar(16)"`
	Route           string     `json:"route" gorm:"type:varchar(255);index:idx_audit_route_time,priority:1;index:idx_audit_user_route,priority:2"`
	Status          int        `json:"status"`
	Success         bool       `json:"success"`
	RequestId       string     `json:"request_id" gorm:"type:varchar(64);index"`
	Content         string     `json:"content" gorm:"type:text"`
	Other           AuditOther `json:"other" gorm:"type:json"`
}

type AuditLogFilter struct {
	IP              string `json:"ip,omitempty"`
	ActorType       string `json:"actor_type,omitempty"`
	AuthMethod      string `json:"auth_method,omitempty"`
	Route           string `json:"route,omitempty"`
	RouteMissing    bool   `json:"route_missing,omitempty"`
	SelfView        bool   `json:"self_view,omitempty"` // Server-selected metadata projection, independent of the viewer's actual role.
	UserId          int    `json:"user_id,omitempty"`
	Username        string `json:"username,omitempty"`
	Category        string `json:"category,omitempty"`
	TokenRef        string `json:"token_ref,omitempty"`
	ExcludeTokenRef string `json:"exclude_token_ref,omitempty"`
	RequestId       string `json:"request_id,omitempty"`
	StartTimestamp  int64  `json:"start_timestamp,omitempty"`
	EndTimestamp    int64  `json:"end_timestamp,omitempty"`
	Success         *bool  `json:"success,omitempty"`
}

func AccessTokenFingerprint(token string) string {
	// PostgreSQL returns CHAR(32) tokens padded with spaces. Normalize that
	// storage padding so persisted tokens and incoming credentials share a ref.
	token = strings.TrimRight(token, " ")
	if token == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", digest)
}

// RecordAuditLog captures safe request metadata only; raw URLs, query strings,
// credentials and response/request bodies must never enter this table.
func RecordAuditLog(c *gin.Context, entry AuditLog) {
	captureAuditIdentity(c, &entry)
	if entry.Category == AuditCategoryOperation || entry.Category == AuditCategorySecurity {
		if entry.Other.Changes == nil {
			entry.Other.Changes = requestAuditChanges(c)
		}
		if entry.Other.Changes != nil && !entry.Success && len(entry.Other.Changes.Targets) > 0 {
			entry.Other.Changes.Outcome = "partial"
		}
		if entry.Other.Changes != nil && !entry.Success && len(entry.Other.Changes.Targets) == 0 && entry.Other.Changes.Outcome != "unavailable" {
			entry.Other.Changes.Outcome = "not_committed"
		}
	}
	if c != nil && entry.ActorRole == common.RoleCommonUser && entry.ActorIsEmployee == nil {
		actorID := entry.UserId
		if entry.Other.AdminInfo != nil && entry.Other.AdminInfo.AdminID > 0 {
			actorID = entry.Other.AdminInfo.AdminID
		}
		entry.ActorIsEmployee = AuditEmployeeSnapshot(c, actorID)
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
		entry.RequestId = c.GetString(common.RequestIdKey)
		entry.Ip = c.ClientIP()
		entry.UserAgent = c.Request.UserAgent()
		entry.Method = c.Request.Method
		entry.Route = c.FullPath()
		if entry.Status == 0 {
			entry.Status = c.Writer.Status()
		}
		if entry.AuthMethod == "" {
			entry.AuthMethod = "session"
			if c.GetBool("use_access_token") {
				entry.AuthMethod = "access_token"
			}
		}
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = common.GetTimestamp()
	}
	if entry.RequestId == "" {
		entry.RequestId = common.NewRequestId()
	}
	if entry.EventId == "" {
		entry.EventId = common.NewRequestId()
	}
	switch entry.ActorRole {
	case common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser:
	default:
		logger.LogError(ctx, common.LogText("audit actor role unavailable (request_id=%s, actor_role=%d)", entry.RequestId, entry.ActorRole))
		entry.ActorRole = 0 // Unknown actors remain visible to root only.
	}
	if entry.Username == "" {
		entry.Username, _ = GetUsernameById(entry.UserId, false)
	}
	ua := []rune(entry.UserAgent)
	if len(ua) > 512 {
		entry.UserAgent = string(ua[:512])
	}
	if LOG_DB == nil {
		logger.LogError(ctx, common.LogText("audit log write failed (request_id=%s): log database unavailable", entry.RequestId))
		return
	}
	row, err := AuditLogStorageRow(LOG_DB, entry)
	if err != nil {
		logger.LogError(ctx, common.LogText("audit log write failed (request_id=%s): %v", entry.RequestId, err))
		return
	}
	if err := LOG_DB.Table("audit_logs").Create(row).Error; err != nil {
		logger.LogError(ctx, common.LogText("audit log write failed (request_id=%s): %v", entry.RequestId, err))
	}
}

// AuditLogStorageRow adapts both audit writers to the native ClickHouse insert
// callback. A concrete bool or nil works with both Nullable(Bool) and the old
// Nullable(UInt8); *bool does not. Keep the domain/API's three-state pointer.
func AuditLogStorageRow(db *gorm.DB, entry AuditLog) (any, error) {
	if db.Dialector.Name() != "clickhouse" {
		return &entry, nil
	}
	encoded, err := common.Marshal(entry.Other)
	if err != nil {
		return nil, err
	}
	var employee any
	if entry.ActorIsEmployee != nil {
		employee = *entry.ActorIsEmployee
	}
	return &struct {
		AuditLog         `gorm:"embedded"`
		EncodedOther     string `gorm:"column:other;type:json"`
		EmployeeSnapshot any    `gorm:"column:actor_is_employee;type:Nullable(Bool)"`
	}{AuditLog: entry, EncodedOther: string(encoded), EmployeeSnapshot: employee}, nil
}

func GetAuditLogs(filter AuditLogFilter, start, limit, viewerRole int) ([]*AuditLog, int64, error) {
	return QueryAuditLogs(LOG_DB, filter, start, limit, viewerRole)
}

func AuditLogQuery(db *gorm.DB, filter AuditLogFilter, viewerRole int) *gorm.DB {
	query := db.Model(&AuditLog{})
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// Decode native JSON through database/sql as text for AuditOther.Scan.
		// Preserve numeric metadata instead of returning quoted Int64 values.
		query = query.WithContext(clickhouse.Context(query.Statement.Context, clickhouse.WithSettings(clickhouse.Settings{
			"output_format_native_write_json_as_string": 1,
			"output_format_json_quote_64bit_integers":   0,
		})))
	}
	if viewerRole < common.RoleRootUser {
		query = query.Where("actor_role IN ? AND actor_role <= ?", []int{common.RoleCommonUser, common.RoleAdminUser}, viewerRole)
	}
	if !CanFilterAuditActor(filter.ActorType, viewerRole) {
		return query.Where("1 = 0")
	}
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Username != "" {
		query = query.Where("username = ?", filter.Username)
	}
	if filter.Category != "" {
		query = query.Where("category = ?", filter.Category)
	}
	if filter.TokenRef != "" {
		query = query.Where("token_ref = ?", filter.TokenRef)
	}
	if filter.ExcludeTokenRef != "" {
		query = query.Where("token_ref <> ?", filter.ExcludeTokenRef)
	}
	if filter.RequestId != "" {
		query = query.Where("request_id = ?", filter.RequestId)
	}
	if filter.StartTimestamp > 0 {
		query = query.Where("created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp > 0 {
		query = query.Where("created_at <= ?", filter.EndTimestamp)
	}
	if filter.Success != nil {
		query = query.Where("success = ?", *filter.Success)
	}
	if filter.IP != "" {
		query = query.Where("ip = ?", filter.IP)
	}
	if filter.Route != "" {
		query = query.Where("route = ?", filter.Route)
	}
	if filter.RouteMissing {
		query = query.Where("route = ?", "")
	}
	if filter.AuthMethod == "unknown" {
		query = query.Where("auth_method IS NULL OR auth_method NOT IN ?", []string{"session", "access_token"})
	} else if filter.AuthMethod != "" {
		query = query.Where("auth_method = ?", filter.AuthMethod)
	}
	switch filter.ActorType {
	case "root":
		query = query.Where("actor_role = ?", common.RoleRootUser)
	case "admin":
		query = query.Where("actor_role = ?", common.RoleAdminUser)
	case "employee", "user":
		query = query.Where("actor_role = ? AND actor_is_employee = ?", common.RoleCommonUser, filter.ActorType == "employee")
	case "unclassified_user":
		query = query.Where("actor_role = ? AND actor_is_employee IS NULL", common.RoleCommonUser)
	case "unknown":
		query = query.Where("actor_role NOT IN ?", []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser})
	}
	return query
}

func QueryAuditLogs(db *gorm.DB, filter AuditLogFilter, start, limit, viewerRole int) ([]*AuditLog, int64, error) {
	query := AuditLogQuery(db, filter, viewerRole)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	logs := make([]*AuditLog, 0)
	if err := query.Select(AuditLogColumns).Order("created_at DESC").Order("event_id DESC").Offset(start).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	ProjectAuditLogs(logs, filter.SelfView, viewerRole)
	return logs, total, nil
}

func ProjectAuditLogs(logs []*AuditLog, selfView bool, viewerRole int) {
	visibility := logOtherVisibilityUser
	if !selfView && viewerRole >= common.RoleRootUser {
		visibility = logOtherVisibilityRoot
	} else if !selfView && viewerRole >= common.RoleAdminUser {
		visibility = logOtherVisibilityAdmin
	}
	for _, entry := range logs {
		if entry.Other.Op != nil {
			op := *entry.Other.Op
			op.Identity = nil
			op.LoginTarget = nil
			entry.Other.Op = &op
		}
		entry.Other.Changes = entry.Other.Changes.project(selfView, viewerRole, entry.UserId)
		if visibility != logOtherVisibilityRoot {
			entry.Other.RootInfo = nil
		}
		if visibility == logOtherVisibilityUser {
			entry.Other.AdminInfo = nil
			entry.Other.AuditInfo = nil
		}
	}
}

// UserAccessTokenStatus describes the legacy users.access_token credential.
//
// Deprecated: 旧版面板访问令牌，仅在升级后的过渡期内使用；删除 users.access_token 列时一并移除。
type UserAccessTokenStatus struct {
	Exists     bool   `json:"exists"`
	TokenHint  string `json:"token_hint"`
	TokenRef   string `json:"token_ref"`
	CreatedAt  *int64 `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	LastUsedIp string `json:"last_used_ip"`
}

// GetUserAccessTokenStatus reads last use of the legacy token from the access audit.
//
// Deprecated: 旧版面板访问令牌，仅在升级后的过渡期内使用；删除 users.access_token 列时一并移除。
func GetUserAccessTokenStatus(userId int) (*UserAccessTokenStatus, error) {
	var user User
	if err := DB.Select("id", "role", "access_token", "access_token_created_at").First(&user, userId).Error; err != nil {
		return nil, err
	}
	status := &UserAccessTokenStatus{Exists: user.GetAccessToken() != ""}
	if !status.Exists {
		return status, nil
	}
	status.TokenRef = AccessTokenFingerprint(user.GetAccessToken())
	status.TokenHint = AccessTokenHint(user.GetAccessToken())
	status.CreatedAt = user.AccessTokenCreatedAt
	var latest AuditLog
	query := LOG_DB.Select("created_at", "ip").Where("user_id = ? AND token_ref = ? AND category = ?", userId, status.TokenRef, AuditCategoryAccessToken)
	if user.Role < common.RoleRootUser {
		query = query.Where("actor_role IN ? AND actor_role <= ?", []int{common.RoleCommonUser, common.RoleAdminUser}, user.Role)
	}
	err := query.Order("created_at DESC").Order("event_id DESC").Take(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	status.LastUsedAt = &latest.CreatedAt
	status.LastUsedIp = latest.Ip
	return status, nil
}

// MigrateAuditLogs also supports independently configured ClickHouse log stores.
// All stores use the configured log cleanup task. A native fixed TTL cannot
// honor permanent or extended retention and must be removed from older tables.
// Deployment risk DB-R01/DB-R07: adding/replacing indexes scans existing data,
// and this runs even when security monitoring is disabled. Confirm production
// schema, table size, lock budget and TTL before rollout; see
// docs/design/database-performance-review-20261115.md.
func MigrateAuditLogs() error {
	if !common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		if err := LOG_DB.AutoMigrate(&AuditLog{}); err != nil {
			return err
		}
		// AutoMigrate only checks index names. Repair only the known incomplete
		// definitions from the initial filter release, never custom indexes.
		indexes, err := LOG_DB.Migrator().GetIndexes(&AuditLog{})
		if err != nil {
			return err
		}
		for _, index := range indexes {
			var legacy []string
			switch index.Name() {
			case "idx_audit_actor_time", "idx_audit_route_time", "idx_audit_user_route":
				legacy = []string{"actor_role"}
			case "idx_audit_employee_time":
				legacy = []string{"actor_role", "actor_is_employee"}
			default:
				continue
			}
			columns := slices.Clone(index.Columns())
			// PostgreSQL's GetIndexes does not guarantee key order. These legacy
			// shapes are identifiable by their column set and count alone.
			slices.Sort(columns)
			slices.Sort(legacy)
			unique, known := index.Unique()
			if !known || unique || !slices.Equal(columns, legacy) {
				continue
			}
			if err := LOG_DB.Migrator().DropIndex(&AuditLog{}, index.Name()); err != nil {
				return err
			}
			if err := LOG_DB.Migrator().CreateIndex(&AuditLog{}, index.Name()); err != nil {
				return err
			}
		}
		return nil
	}
	var definition string
	if err := LOG_DB.Raw("SELECT create_table_query FROM system.tables WHERE database = currentDatabase() AND name = ?", "audit_logs").Scan(&definition).Error; err != nil {
		return err
	}
	if definition != "" {
		if strings.Contains(definition, "TTL ") {
			if err := LOG_DB.Exec("ALTER TABLE audit_logs REMOVE TTL").Error; err != nil {
				return err
			}
		}
		if !strings.Contains(definition, "actor_is_employee") {
			return LOG_DB.Exec("ALTER TABLE audit_logs ADD COLUMN actor_is_employee Nullable(Bool)").Error
		}
		return nil
	}
	return LOG_DB.Exec(`CREATE TABLE IF NOT EXISTS audit_logs (
		id Int64 DEFAULT 0, event_id String, user_id Int64, username String, actor_role Int32, actor_is_employee Nullable(Bool),
		created_at Int64, category String, action String, token_ref String,
		auth_method String, ip String, user_agent String, method String, route String,
		status Int32, success UInt8, request_id String, content String, other JSON
	) ENGINE = MergeTree()
	PARTITION BY toYYYYMM(toDateTime(created_at))
	ORDER BY (created_at, event_id)`).Error
}

// AuditLogCleanupTargetTimestamp 返回审计日志清理的时间上界（早于它的行可删）。
// 保留天数配置为 0 时返回 0，表示不清理。
func AuditLogCleanupTargetTimestamp() int64 {
	days := operation_setting.GetAuditLogSetting().GetRetentionDays()
	if days <= 0 {
		return 0
	}
	return common.GetTimestamp() - int64(days)*24*60*60
}

// CountOldAuditLog 统计早于 targetTimestamp 的审计日志行数，走 created_at 索引。
func CountOldAuditLog(ctx context.Context, db *gorm.DB, targetTimestamp int64) (int64, error) {
	var total int64
	if err := db.WithContext(ctx).Model(&AuditLog{}).Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// DeleteOldAuditLogBatch 分批删除早于 targetTimestamp 的审计日志。
// 批量删除而非一条大 DELETE：这张表与消费日志异步管线共用 LOG_DB 连接池，
// 长事务会把连接和行锁一起占住。
func DeleteOldAuditLogBatch(ctx context.Context, db *gorm.DB, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	if db.Dialector.Name() == "clickhouse" {
		// ClickHouse DELETE 是重写数据分区的 mutation，按批下发会病态地慢。
		// 与 logs 清理一致：一次同步 mutation 删完，返回行数让调用方的进度循环一轮结束。
		total, err := CountOldAuditLog(ctx, db, targetTimestamp)
		if err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, nil
		}
		if err := db.WithContext(ctx).Exec(
			"ALTER TABLE audit_logs DELETE WHERE created_at < ? SETTINGS mutations_sync = 1",
			targetTimestamp,
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	// 先取一批主键再按主键删：`DELETE ... LIMIT` 只有 MySQL 支持，
	// PostgreSQL 下 GORM 会静默丢掉 LIMIT，一条语句删空整段区间并长时间
	// 持有事务与连接——那正是这里要避免的（Rule 0 / Rule 2）。
	var ids []int
	if err := db.WithContext(ctx).Model(&AuditLog{}).
		Where("created_at < ?", targetTimestamp).
		Order("created_at, event_id").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := db.WithContext(ctx).Where("id IN ?", ids).Delete(&AuditLog{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func ValidAuditCategory(category string) bool {
	return category == "" || category == AuditCategoryLogin || category == AuditCategorySecurity || category == AuditCategoryOperation || category == AuditCategoryAccessToken
}

func ValidTokenFingerprint(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}
