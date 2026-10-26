package model

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrLogExportListQuery = errors.New("invalid export list query")

type LogExportListQuery struct {
	Scope          string `form:"scope"`
	CreatorID      int    `form:"creator_id"`
	CreatorKeyword string `form:"creator_keyword"`
	CustomerID     int    `form:"customer_id"`
	Source         string `form:"source"`
	Mode           string `form:"mode"`
	Status         string `form:"status"`
	CreatedAfter   int64  `form:"created_after"`
	CreatedBefore  int64  `form:"created_before"`
	JobID          string `form:"job_id"`
	Limit          int    `form:"limit"`
	Cursor         string `form:"cursor"`
}

func (q *LogExportListQuery) Validate() error {
	q.CreatorKeyword = strings.TrimSpace(q.CreatorKeyword)
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 100 || q.CreatorID < 0 || q.CustomerID < 0 ||
		utf8.RuneCountInString(q.CreatorKeyword) > 64 || len(q.Cursor) > 2048 ||
		q.CreatedAfter < 0 || q.CreatedBefore < 0 || q.CreatedAfter > 253402300799 || q.CreatedBefore > 253402300799 ||
		(q.CreatedAfter > 0 && q.CreatedBefore > 0 && q.CreatedAfter > q.CreatedBefore) ||
		!slices.Contains([]string{"", "all", "mine"}, q.Scope) ||
		!slices.Contains([]string{"", "admin", "employee", "self"}, q.Source) ||
		!slices.Contains([]string{"", "detail", "summary", "both"}, q.Mode) ||
		!slices.Contains([]string{"", LogExportStatusPending, LogExportStatusRunning, LogExportStatusReady, LogExportStatusFailed, LogExportStatusCanceled}, q.Status) {
		return ErrLogExportListQuery
	}
	if q.JobID != "" {
		if _, err := uuid.Parse(q.JobID); err != nil {
			return ErrLogExportListQuery
		}
	}
	return nil
}

func (q LogExportListQuery) Matches(job *LogExportJob) bool {
	if (q.CreatorID > 0 && job.UserID != q.CreatorID) || (q.JobID != "" && job.JobID != q.JobID) ||
		(q.Status != "" && job.Status != q.Status) ||
		(q.CreatedAfter > 0 && job.CreatedAt < q.CreatedAfter) || (q.CreatedBefore > 0 && job.CreatedAt > q.CreatedBefore) ||
		(q.CreatorKeyword != "" && !strings.Contains(strings.ToLower(job.Username), strings.ToLower(q.CreatorKeyword))) {
		return false
	}
	if q.Source != "" && job.ExportSource() != q.Source {
		return false
	}
	mode := job.Mode
	if mode == "" {
		mode = LogExportModeDetail
	}
	if q.Mode != "" && mode != q.Mode {
		return false
	}
	if q.CustomerID > 0 && !job.IsSelfExportFor(q.CustomerID) &&
		(job.EmployeeScope == nil || !slices.Contains(job.EmployeeScope.CustomerIDs, q.CustomerID)) {
		return false
	}
	return true
}

func (job *LogExportJob) ExportSource() string {
	if job.EmployeeScope != nil {
		return "employee"
	}
	if job.SelfExport {
		return "self"
	}
	return "admin"
}

// LogExportAccess is request-local. Customers must be loaded from the current DB,
// never supplied by the client or retained across requests.
type LogExportAccess struct {
	UserID    int
	Role      int
	Scope     string
	Customers map[int]bool
}

func (a *LogExportAccess) LoadCustomers(ctx context.Context, jobs []*LogExportJob, query LogExportListQuery) error {
	if a.Scope != "employee" {
		return nil
	}
	if err := AuthorizeEmployeeExport(ctx, a.UserID); err != nil {
		return err
	}
	ids := make([]int, 0, len(jobs)+2)
	for _, job := range jobs {
		if job.IsSelfExportFor(job.UserID) {
			ids = append(ids, job.UserID)
		}
	}
	if query.CustomerID > 0 {
		ids = append(ids, query.CustomerID)
	}
	if query.CreatorID > 0 && query.CreatorID != a.UserID {
		ids = append(ids, query.CreatorID)
	}
	a.Customers = make(map[int]bool)
	if len(ids) == 0 {
		return nil
	}
	var customers []int
	if err := DB.WithContext(ctx).Model(&User{}).Where("id IN ? AND inviter_id = ? AND role = ?", ids, a.UserID, common.RoleCommonUser).Pluck("id", &customers).Error; err != nil {
		return err
	}
	for _, id := range customers {
		a.Customers[id] = true
	}
	if (query.CustomerID > 0 && !a.Customers[query.CustomerID]) ||
		(query.CreatorID > 0 && query.CreatorID != a.UserID && !a.Customers[query.CreatorID]) {
		return ErrEmployeeExportAccess
	}
	return nil
}

func (a LogExportAccess) Permissions(job *LogExportJob) (view, operate bool) {
	if a.UserID <= 0 {
		return false, false
	}
	switch a.Scope {
	case "admin":
		return a.Role >= common.RoleAdminUser, a.Role >= common.RoleAdminUser
	case "self":
		return job.IsSelfExportFor(a.UserID), job.IsSelfExportFor(a.UserID)
	case "employee":
		if job.UserID == a.UserID && job.EmployeeScope != nil {
			return true, true
		}
		if job.IsSelfExportFor(job.UserID) && a.Customers[job.UserID] {
			return true, operation_setting.GetLogExportSetting().EmployeeCustomerManageEnabled
		}
	}
	return false, false
}

type ManagedLogExportJob struct {
	*LogExportJob
	Source      string `json:"source"`
	CanDownload bool   `json:"can_download"`
	CanCancel   bool   `json:"can_cancel"`
	CanDelete   bool   `json:"can_delete"`
}

func (a LogExportAccess) PublicJob(job *LogExportJob) *ManagedLogExportJob {
	view, operate := a.Permissions(job)
	if !view {
		return nil
	}
	public := job.PublicView()
	public.NodeName = ""
	if job.UserID != a.UserID {
		public.Filters = LogExportFilter{StartTimestamp: job.Filters.StartTimestamp, EndTimestamp: job.Filters.EndTimestamp}
		public.Columns = nil
		public.SummaryDims = nil
		public.EmployeeScope = nil
		public.Options = LogExportOptions{}
		public.Lang = ""
		public.CreatorRole = 0
	}
	// Old restart errors may contain implementation details. Only stable codes leave the server.
	if public.Error != "" && !slices.Contains([]string{"disk_full", "too_many_parts", "timeout", "too_many_groups", "internal"}, public.Error) {
		public.Error = "internal"
	}
	return &ManagedLogExportJob{LogExportJob: public, Source: job.ExportSource(),
		CanDownload: operate && job.Status == LogExportStatusReady && len(job.Parts) > 0,
		CanCancel:   operate && !job.IsTerminal(), CanDelete: operate && job.IsTerminal()}
}

type LogExportJobPage struct {
	Jobs       []*ManagedLogExportJob `json:"jobs"`
	NextCursor string                 `json:"next_cursor"`
	HasMore    bool                   `json:"has_more"`
}

// CancelOrDeleteManagedLogExportJob reports storage failures to the management UI.
// Active jobs are canceled through the existing signal; only terminal files are removed.
func CancelOrDeleteManagedLogExportJob(ctx context.Context, job *LogExportJob) (bool, error) {
	if !job.IsTerminal() {
		return true, common.RDB.Set(ctx, logExportCancelKeyPfx+job.JobID, "1", time.Hour).Err()
	}
	for _, part := range job.Parts {
		if part.Path == "" {
			continue
		}
		if err := os.Remove(part.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	pipe := common.RDB.TxPipeline()
	pipe.Del(ctx, logExportJobKey(job.JobID), logExportCancelKeyPfx+job.JobID)
	pipe.ZRem(ctx, logExportUserJobsKey(job.UserID), job.JobID)
	pipe.ZRem(ctx, logExportAllJobsKey, job.JobID)
	_, err := pipe.Exec(ctx)
	return false, err
}

type logExportListCursor struct {
	Score  float64 `json:"score"`
	ID     string  `json:"id"`
	Filter string  `json:"filter"`
}

// Binary search in the existing sorted set supports equal scores and deleted cursor
// anchors without OFFSET scans or unbounded reads. At most 201 IDs leave Redis.
const logExportPageScript = `
local lo = 0
local hi = redis.call('ZCARD', KEYS[1])
local score = tonumber(ARGV[1])
local member = ARGV[2]
while lo < hi do
  local mid = math.floor((lo + hi) / 2)
  local item = redis.call('ZREVRANGE', KEYS[1], mid, mid, 'WITHSCORES')
  local value = tonumber(item[2])
  if value > score or (value == score and member ~= '' and item[1] >= member) then
    lo = mid + 1
  else
    hi = mid
  end
end
return redis.call('ZREVRANGE', KEYS[1], lo, lo + 200, 'WITHSCORES')
`

func ListManagedLogExportJobs(ctx context.Context, access LogExportAccess, query LogExportListQuery) (*LogExportJobPage, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if access.Scope == "self" && query.CreatorID > 0 && query.CreatorID != access.UserID {
		return nil, ErrEmployeeExportAccess
	}
	page := &LogExportJobPage{Jobs: make([]*ManagedLogExportJob, 0)}
	key := logExportAllJobsKey
	if access.Scope == "self" || query.Scope == "mine" {
		key = logExportUserJobsKey(access.UserID)
	} else if query.CreatorID > 0 {
		key = logExportUserJobsKey(query.CreatorID)
	}
	fingerprintQuery := query
	fingerprintQuery.Cursor = ""
	encoded, err := common.Marshal([]any{access.UserID, access.Scope, fingerprintQuery})
	if err != nil {
		return nil, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(encoded))
	// Cursors can point at tasks hidden from this viewer. Encrypt their IDs and
	// authenticate the filter/actor binding rather than exposing base64 metadata.
	cursorKey := sha256.Sum256([]byte("logexport:cursor:v1:" + common.CryptoSecret))
	block, err := aes.NewCipher(cursorKey[:])
	if err != nil {
		return nil, err
	}
	codec, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	cursor := logExportListCursor{Score: 253402300800000, Filter: fingerprint}
	if query.CreatedBefore > 0 {
		cursor.Score = float64(query.CreatedBefore*1000 + 999)
	}
	if query.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		if err != nil {
			return nil, ErrLogExportListQuery
		}
		data, err = codec.Open(nil, nil, data, []byte(fingerprint))
		if err != nil || common.Unmarshal(data, &cursor) != nil || cursor.Filter != fingerprint || cursor.ID == "" || cursor.Score < 0 || cursor.Score > 253402300800000 {
			return nil, ErrLogExportListQuery
		}
	}
	var candidates []*LogExportJob
	var positions []logExportListCursor
	more := false
	if query.JobID != "" {
		job, err := GetLogExportJob(query.JobID)
		if err != nil {
			return nil, err
		}
		if job != nil {
			candidates = append(candidates, job)
		}
	} else {
		items, err := common.RDB.Eval(ctx, logExportPageScript, []string{key}, cursor.Score, cursor.ID).StringSlice()
		if err != nil {
			return nil, err
		}
		more = len(items) > 400
		if more {
			items = items[:400]
		}
		keys := make([]string, 0, len(items)/2)
		for i := 0; i < len(items); i += 2 {
			score, err := strconv.ParseFloat(items[i+1], 64)
			if err != nil {
				return nil, err
			}
			if query.CreatedAfter > 0 && score < float64(query.CreatedAfter*1000) {
				more = false
				break
			}
			keys = append(keys, logExportJobKey(items[i]))
			positions = append(positions, logExportListCursor{Score: score, ID: items[i], Filter: fingerprint})
		}
		if len(keys) > 0 {
			values, err := common.RDB.MGet(ctx, keys...).Result()
			if err != nil {
				return nil, err
			}
			var expired []any
			for i, value := range values {
				if value == nil {
					candidates = append(candidates, nil)
					expired = append(expired, positions[i].ID)
					continue
				}
				raw, ok := value.(string)
				if !ok {
					return nil, errors.New("invalid export task data")
				}
				var job LogExportJob
				if err := common.UnmarshalJsonStr(raw, &job); err != nil {
					return nil, err
				}
				candidates = append(candidates, &job)
			}
			if len(expired) > 0 {
				if err := common.RDB.ZRem(ctx, key, expired...).Err(); err != nil {
					return nil, err
				}
			}
		}
	}
	valid := make([]*LogExportJob, 0, len(candidates))
	for _, job := range candidates {
		if job != nil {
			valid = append(valid, job)
		}
	}
	if err := access.LoadCustomers(ctx, valid, query); err != nil {
		return nil, err
	}
	last := -1
	for i, job := range candidates {
		last = i
		if job == nil || !query.Matches(job) || (query.Scope == "mine" && job.UserID != access.UserID) {
			continue
		}
		public := access.PublicJob(job)
		if public == nil {
			continue
		}
		page.Jobs = append(page.Jobs, public)
		if len(page.Jobs) == query.Limit {
			more = more || i+1 < len(candidates)
			break
		}
	}
	if more && last >= 0 && len(positions) > last {
		data, err := common.Marshal(positions[last])
		if err != nil {
			return nil, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(codec.Seal(nil, nil, data, []byte(fingerprint)))
		page.HasMore = true
	}
	return page, nil
}

// CurrentLogExportAccess is used for bearer download links, which have no session
// middleware. Re-read the account so a role change invalidates existing links.
func CurrentLogExportAccess(ctx context.Context, userID int, scope string) (LogExportAccess, error) {
	var user User
	err := DB.WithContext(ctx).Select("id", "role", "status").Where("id = ?", userID).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return LogExportAccess{}, ErrEmployeeExportAccess
	}
	if err != nil {
		return LogExportAccess{}, err
	}
	if user.Status != common.UserStatusEnabled {
		return LogExportAccess{}, ErrEmployeeExportAccess
	}
	return LogExportAccess{UserID: user.Id, Role: user.Role, Scope: scope}, nil
}

// Export download grants are stored server-side; the browser only receives a random token.
type LogExportDownloadGrant struct {
	Version int    `json:"version"`
	JobID   string `json:"job_id"`
	UserID  int    `json:"user_id"`
	Part    int    `json:"part"`
	Scope   string `json:"scope"`
}

func CreateManagedLogExportDownloadToken(jobID string, userID, part int, scope string) (string, error) {
	data, err := common.Marshal(LogExportDownloadGrant{Version: 2, JobID: jobID, UserID: userID, Part: part, Scope: scope})
	if err != nil {
		return "", err
	}
	token := uuid.NewString()
	return token, common.RedisSet(logExportDLTokenKeyPfx+token, string(data), operation_setting.GetLogExportSetting().GetDownloadTokenTTL())
}

func ConsumeManagedLogExportDownloadToken(token string) (*LogExportDownloadGrant, error) {
	ctx, cancel := logExportRedisCtx()
	defer cancel()
	raw, err := common.RDB.Eval(ctx, logExportDownloadTokenScript, []string{logExportDLTokenKeyPfx + token}, int64(operation_setting.GetLogExportSetting().GetDownloadSessionTTL().Seconds())).Text()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	grant := &LogExportDownloadGrant{}
	if strings.HasPrefix(raw, "{") {
		if err := common.UnmarshalJsonStr(raw, grant); err != nil {
			return nil, err
		}
		if grant.Version != 2 || grant.UserID <= 0 || grant.Part < 0 || !slices.Contains([]string{"admin", "self", "employee"}, grant.Scope) {
			return nil, ErrEmployeeExportAccess
		}
		return grant, nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return nil, ErrEmployeeExportAccess
	}
	grant.JobID = parts[0]
	grant.UserID, err = strconv.Atoi(parts[1])
	if err != nil {
		return nil, err
	}
	grant.Part, err = strconv.Atoi(parts[2])
	if err != nil || grant.UserID <= 0 || grant.Part < 0 {
		return nil, ErrEmployeeExportAccess
	}
	return grant, nil
}
