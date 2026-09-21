package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

// 任务状态。
const (
	LogExportStatusPending  = "pending"
	LogExportStatusRunning  = "running"
	LogExportStatusReady    = "ready"
	LogExportStatusFailed   = "failed"
	LogExportStatusCanceled = "canceled"
)

// Redis 键名空间。与 relay 及台账导出（ledger:export:*）均不冲突。
const (
	logExportJobKeyPfx      = "logexport:job:"
	logExportUserJobsKeyPfx = "logexport:user:jobs:"
	logExportAllJobsKey     = "logexport:jobs"
	logExportSlotsKey       = "logexport:slots"
	logExportCooldownKeyPfx = "logexport:cooldown:"
	logExportCancelKeyPfx   = "logexport:cancel:"
	logExportDLTokenKeyPfx  = "logexport:dl-token:"
)

const logExportRedisOpTimeout = 3 * time.Second

// logExportWindowShrinkBatches 一个扫描窗口用掉这么多批次就把下个窗口宽度减半。
// 取 4 而不是 2：偶尔用满两三批属于正常波动，减半太敏感会让宽度在两个值之间反复横跳。
const logExportWindowShrinkBatches = 4

// LogExportPart 一个分片文件。
type LogExportPart struct {
	Index int `json:"index"`
	// Kind "detail"（默认，向后兼容空值）或 "summary"。
	// 「明细 + 汇总」模式下一次任务同时产出两种分片，下载时要能分得清哪个是哪个。
	Kind string `json:"kind,omitempty"`
	// Path 服务端绝对路径。必须参与序列化——任务状态存在 Redis 里，重新载入后
	// 还要靠它定位文件；下发前端前用 PublicView 抹掉。
	Path      string `json:"path,omitempty"`
	Rows      int64  `json:"rows"`
	Bytes     int64  `json:"bytes"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

// 分片种类。空值按 detail 处理，保证升级前创建的任务行为不变。
const (
	LogExportPartKindDetail  = "detail"
	LogExportPartKindSummary = "summary"
)

// LogExportOptions 导出的呈现选项。
type LogExportOptions struct {
	CSVBOM   bool   `json:"csv_bom"`
	Timezone string `json:"timezone"`
	Header   bool   `json:"header"`
}

// LogExportJob 一次导出任务的完整状态，序列化后存 Redis。
type LogExportJob struct {
	JobID    string `json:"job_id"`
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Status   string `json:"status"`
	// Progress 0-100，按时间轴推进，不做 COUNT。
	Progress int `json:"progress"`
	// RowCount 实际写进文件的行数。带行级筛选时它远小于扫描量。
	RowCount int64 `json:"row_count"`
	// ScannedRows 扫描过的行数。没有它，管理员看到「跑了 8 分钟只出 340 行」
	// 会以为出了故障——而那正是异常筛选该有的样子。
	ScannedRows int64  `json:"scanned_rows,omitempty"`
	BytesOut    int64  `json:"bytes_out"`
	Format      string `json:"format"`
	// FormatDowngraded 结果超出 xlsx 行数上限而自动降级为 csv.gz。
	FormatDowngraded bool            `json:"format_downgraded"`
	Columns          []string        `json:"columns"`
	Filters          LogExportFilter `json:"filters"`
	// Mode "detail"（默认，向后兼容空值）或 "summary"。
	// summary 模式忽略 Columns，改按 SummaryDims 聚合。
	Mode string `json:"mode,omitempty"`
	// SummaryDims 聚合维度，仅 summary 模式有效。
	SummaryDims []string         `json:"summary_dims,omitempty"`
	Options     LogExportOptions `json:"options"`
	Lang        string           `json:"lang"`
	// NodeName 执行该任务的节点（common.NodeName）。任务状态存共享 Redis、
	// 分片文件却写在执行节点本地，重启回收必须只认自己的任务，否则一个节点重启
	// 会把另一个节点正在跑的导出标记为失败并删掉分片。
	// 升级前创建的任务该字段为空，按旧行为回收。
	NodeName string          `json:"node_name,omitempty"`
	Parts    []LogExportPart `json:"parts"`
	// ThrottledMs 因资源闸门累计让出的毫秒数，供运维观察。
	ThrottledMs int64  `json:"throttled_ms"`
	Error       string `json:"error,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	// CreatedAtMs 仅用作任务索引的排序分值。用秒会让同一秒内创建的任务在
	// ZSET 里分值相同，ZRevRange 退化成按成员名（UUID）排序，列表顺序随机。
	CreatedAtMs int64 `json:"created_at_ms,omitempty"`
	UpdatedAt   int64 `json:"updated_at"`
	FinishedAt  int64 `json:"finished_at,omitempty"`
}

// indexScore 返回任务在索引 ZSET 中的分值（毫秒）。
func (j *LogExportJob) indexScore() float64 {
	if j.CreatedAtMs > 0 {
		return float64(j.CreatedAtMs)
	}
	return float64(j.CreatedAt) * 1000
}

// PublicView 返回可下发前端的副本：抹掉服务端文件路径，其余信息保留。
func (j *LogExportJob) PublicView() *LogExportJob {
	if j == nil {
		return nil
	}
	view := *j
	view.Parts = make([]LogExportPart, len(j.Parts))
	copy(view.Parts, j.Parts)
	for i := range view.Parts {
		view.Parts[i].Path = ""
	}
	return &view
}

// IsTerminal 报告任务是否已进入终态。
func (j *LogExportJob) IsTerminal() bool {
	return j.Status == LogExportStatusReady ||
		j.Status == LogExportStatusFailed ||
		j.Status == LogExportStatusCanceled
}

func logExportJobKey(jobID string) string { return logExportJobKeyPfx + jobID }

func logExportUserJobsKey(userID int) string {
	return logExportUserJobsKeyPfx + strconv.Itoa(userID)
}

func logExportRedisCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), logExportRedisOpTimeout)
}

// NewLogExportJobID 生成任务 ID。
func NewLogExportJobID() string { return uuid.New().String() }

// LogExportAvailable 报告后台导出是否可用（依赖 Redis 存放任务状态）。
func LogExportAvailable() bool {
	return common.RedisEnabled && common.RDB != nil
}

func saveLogExportJob(job *LogExportJob) error {
	job.UpdatedAt = time.Now().Unix()
	data, err := common.Marshal(job)
	if err != nil {
		return err
	}
	ttl := operation_setting.GetLogExportSetting().GetJobTTL()
	if err := common.RedisSet(logExportJobKey(job.JobID), string(data), ttl); err != nil {
		return err
	}
	ctx, cancel := logExportRedisCtx()
	defer cancel()
	member := &redis.Z{Score: job.indexScore(), Member: job.JobID}
	if err := common.RDB.ZAdd(ctx, logExportUserJobsKey(job.UserID), member).Err(); err != nil {
		return err
	}
	common.RDB.Expire(ctx, logExportUserJobsKey(job.UserID), ttl)
	if err := common.RDB.ZAdd(ctx, logExportAllJobsKey, member).Err(); err != nil {
		return err
	}
	common.RDB.Expire(ctx, logExportAllJobsKey, ttl)
	return nil
}

// CreateLogExportJob 落盘一个 pending 任务。
func CreateLogExportJob(job *LogExportJob) error {
	job.Status = LogExportStatusPending
	job.NodeName = common.NodeName
	now := time.Now()
	job.CreatedAt = now.Unix()
	job.CreatedAtMs = now.UnixMilli()
	return saveLogExportJob(job)
}

// UpdateLogExportJob 持久化任务状态，失败只记日志不影响导出继续。
func UpdateLogExportJob(job *LogExportJob) {
	if err := saveLogExportJob(job); err != nil {
		common.SysError("log export: update job failed: " + err.Error())
	}
}

// GetLogExportJob 读取任务；不存在时返回 (nil, nil)。
func GetLogExportJob(jobID string) (*LogExportJob, error) {
	raw, err := common.RedisGet(logExportJobKey(jobID))
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var job LogExportJob
	if err := common.UnmarshalJsonStr(raw, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// DeleteLogExportJob 删除任务状态与索引（文件由调用方删除）。
func DeleteLogExportJob(job *LogExportJob) {
	_ = common.RedisDel(logExportJobKey(job.JobID))
	ctx, cancel := logExportRedisCtx()
	defer cancel()
	common.RDB.ZRem(ctx, logExportUserJobsKey(job.UserID), job.JobID)
	common.RDB.ZRem(ctx, logExportAllJobsKey, job.JobID)
	_ = common.RedisDel(logExportCancelKeyPfx + job.JobID)
}

// RemoveLogExportJobFiles 删除任务产生的分片文件。
func RemoveLogExportJobFiles(job *LogExportJob) {
	for _, part := range job.Parts {
		if part.Path != "" {
			_ = os.Remove(part.Path)
		}
	}
}

// ListLogExportJobs 列出任务，按创建时间倒序。all=true 时列出所有人的任务（Root）。
func ListLogExportJobs(userID int, all bool, limit int) ([]*LogExportJob, error) {
	if limit <= 0 {
		limit = 20
	}
	key := logExportUserJobsKey(userID)
	if all {
		key = logExportAllJobsKey
	}
	ctx, cancel := logExportRedisCtx()
	defer cancel()
	ids, err := common.RDB.ZRevRange(ctx, key, 0, int64(limit-1)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	jobs := make([]*LogExportJob, 0, len(ids))
	for _, id := range ids {
		job, err := GetLogExportJob(id)
		if err != nil || job == nil {
			// 任务已过期：顺手清掉索引里的残留成员。
			common.RDB.ZRem(ctx, key, id)
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// CountActiveLogExportJobs 统计用户未过期的任务数。
func CountActiveLogExportJobs(userID int) int {
	jobs, err := ListLogExportJobs(userID, false, 100)
	if err != nil {
		return 0
	}
	count := 0
	for _, job := range jobs {
		if !job.IsTerminal() {
			count++
		}
	}
	return count
}

// ── 并发槽（计数信号量）─────────────────────────────────────────

// logExportSlotAcquireScript 先按过期时间清理僵尸槽位，再判断是否还有空位。
// 进程崩溃留下的槽位会随 score 过期被自动回收，无需人工干预。
const logExportSlotAcquireScript = `
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[2]) then
  return 0
end
redis.call('ZADD', KEYS[1], ARGV[3], ARGV[4])
redis.call('EXPIRE', KEYS[1], ARGV[5])
return 1
`

// AcquireLogExportSlot 抢占一个运行槽位。maxJobs 为 0 时直接拒绝（运营用它停止接受新任务）。
func AcquireLogExportSlot(jobID string, maxJobs int, ttl time.Duration) bool {
	if maxJobs <= 0 {
		return false
	}
	now := time.Now().Unix()
	expireAt := now + int64(ttl.Seconds())
	ctx, cancel := logExportRedisCtx()
	defer cancel()
	res, err := common.RDB.Eval(ctx, logExportSlotAcquireScript,
		[]string{logExportSlotsKey},
		now, maxJobs, expireAt, jobID, int64(ttl.Seconds())*2,
	).Int64()
	if err != nil {
		common.SysError("log export: acquire slot failed: " + err.Error())
		return false
	}
	return res == 1
}

// ReleaseLogExportSlot 释放运行槽位。
func ReleaseLogExportSlot(jobID string) {
	ctx, cancel := logExportRedisCtx()
	defer cancel()
	if err := common.RDB.ZRem(ctx, logExportSlotsKey, jobID).Err(); err != nil {
		common.SysError("log export: release slot failed: " + err.Error())
	}
}

// CheckAndSetLogExportCooldown 返回 true 表示用户仍在冷却中。
func CheckAndSetLogExportCooldown(userID int) bool {
	ctx, cancel := logExportRedisCtx()
	defer cancel()
	ttl := time.Duration(operation_setting.GetLogExportSetting().GetUserCooldownSec()) * time.Second
	ok, err := common.RDB.SetNX(ctx, logExportCooldownKeyPfx+strconv.Itoa(userID), "1", ttl).Result()
	if err != nil {
		common.SysError("log export: cooldown check failed: " + err.Error())
		return false
	}
	return !ok // SetNX 成功表示此前不在冷却中
}

// ClearLogExportCooldown 在任务创建失败时回收冷却，避免白白占掉用户的一次机会。
func ClearLogExportCooldown(userID int) {
	_ = common.RedisDel(logExportCooldownKeyPfx + strconv.Itoa(userID))
}

// CancelLogExportJob 置取消标志，运行中的任务在下一批检测到后退出。
func CancelLogExportJob(jobID string) {
	if err := common.RedisSet(logExportCancelKeyPfx+jobID, "1", time.Hour); err != nil {
		common.SysError("log export: set cancel flag failed: " + err.Error())
	}
}

func isLogExportJobCanceled(jobID string) bool {
	raw, err := common.RedisGet(logExportCancelKeyPfx + jobID)
	if err != nil {
		return false
	}
	return raw != ""
}

// ── 下载令牌 ─────────────────────────────────────────────────────

// logExportDownloadTokenScript 首次使用把令牌转为「已绑定」并续期到续传窗口，
// 之后的 Range 请求可在窗口内重复使用同一令牌（断点续传会发多次请求，
// 用完即失效会让续传直接失败）。窗口是固定的，不随每次请求滚动延长。
const logExportDownloadTokenScript = `
local v = redis.call('GET', KEYS[1])
if not v then return nil end
if string.sub(v, 1, 2) ~= 'b:' then
  redis.call('SET', KEYS[1], 'b:' .. v, 'EX', ARGV[1])
  return v
end
return string.sub(v, 3)
`

// CreateLogExportDownloadToken 签发一次性下载令牌。part=0 表示打包下载全部分片。
func CreateLogExportDownloadToken(jobID string, userID, part int) (string, error) {
	token := uuid.New().String()
	value := jobID + ":" + strconv.Itoa(userID) + ":" + strconv.Itoa(part)
	ttl := operation_setting.GetLogExportSetting().GetDownloadTokenTTL()
	if err := common.RedisSet(logExportDLTokenKeyPfx+token, value, ttl); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeLogExportDownloadToken 校验并绑定下载令牌。
// 令牌不存在或已过期时返回 ("", 0, 0, nil)。
func ConsumeLogExportDownloadToken(token string) (jobID string, userID, part int, err error) {
	sessionTTL := int64(operation_setting.GetLogExportSetting().GetDownloadSessionTTL().Seconds())
	ctx, cancel := logExportRedisCtx()
	defer cancel()
	raw, err := common.RDB.Eval(ctx, logExportDownloadTokenScript,
		[]string{logExportDLTokenKeyPfx + token}, sessionTTL).Text()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", 0, 0, nil
		}
		return "", 0, 0, err
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return "", 0, 0, fmt.Errorf("invalid download token payload")
	}
	userID, err = strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid user id in download token")
	}
	part, err = strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid part in download token")
	}
	return parts[0], userID, part, nil
}

// ── 文件清理与启动恢复 ───────────────────────────────────────────

// CleanupOrphanLogExportFiles 删除 Redis 中已无对应任务的分片文件。
// 在新建任务时异步触发，并在进程启动时执行一次。
func CleanupOrphanLogExportFiles() {
	if !LogExportAvailable() {
		return
	}
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), logExportFilePrefix+"*"))
	if err != nil || len(matches) == 0 {
		return
	}
	for _, file := range matches {
		jobID := jobIDFromExportFileName(filepath.Base(file))
		if jobID == "" {
			continue
		}
		ctx, cancel := logExportRedisCtx()
		n, err := common.RDB.Exists(ctx, logExportJobKey(jobID)).Result()
		cancel()
		if err != nil || n > 0 {
			continue
		}
		_ = os.Remove(file)
	}
}

// jobIDFromExportFileName 从 log-export-<jobID>-part-0001.csv.gz 还原 jobID。
func jobIDFromExportFileName(base string) string {
	rest := strings.TrimPrefix(base, logExportFilePrefix)
	if rest == base {
		return ""
	}
	if idx := strings.Index(rest, "-part-"); idx > 0 {
		return rest[:idx]
	}
	return ""
}

// isOwnLogExportJob 报告任务是否由本节点创建。
// NodeName 为空的是升级前创建的存量任务，按旧行为（本节点回收）处理，
// 避免升级瞬间遗留的任务永远卡在 running。
func isOwnLogExportJob(job *LogExportJob) bool {
	return job.NodeName == "" || job.NodeName == common.NodeName
}

// RecoverStaleLogExportJobs 启动时把本节点上次进程遗留的 running 任务置为失败。
// 半成品文件的续写正确性代价高于让管理员重试，因此不做自动续跑。
//
// 只回收本节点的任务：任务状态存共享 Redis，多实例部署下这里能看到所有节点的任务，
// 而分片文件在执行节点本地。不加节点过滤的话，本节点启动会把另一个节点正在跑的
// 导出标记为失败并删掉它的分片文件。
func RecoverStaleLogExportJobs() {
	if !LogExportAvailable() {
		return
	}
	jobs, err := ListLogExportJobs(0, true, 200)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.IsTerminal() {
			continue
		}
		if !isOwnLogExportJob(job) {
			continue
		}
		job.Status = LogExportStatusFailed
		job.Error = "interrupted by server restart"
		job.FinishedAt = time.Now().Unix()
		UpdateLogExportJob(job)
		RemoveLogExportJobFiles(job)
		// 必须连槽位一起释放。槽位由执行 goroutine 的 defer 释放，而进程被杀时
		// 那个 defer 根本没机会跑，槽位会一直占到 TTL（默认等于 timeout_sec，2 小时）。
		// max_concurrent_jobs 默认是 1 —— 少了这一行，一次重启就能让导出功能
		// 瘫痪两个小时，且日志里看不出任何原因，只会回「已有导出正在运行」。
		ReleaseLogExportSlot(job.JobID)
	}
	CleanupOrphanLogExportFiles()
}

// ── 导出执行 ─────────────────────────────────────────────────────

// errXlsxRowLimit 触发 xlsx → csv.gz 自动降级。
var errXlsxRowLimit = errors.New("xlsx row limit exceeded")

// ErrLogExportDiskFull 磁盘可用空间不足。
var ErrLogExportDiskFull = errors.New("insufficient disk space")

// ErrLogExportTooManyParts 分片数超过上限，说明选定范围的数据量过大。
var ErrLogExportTooManyParts = errors.New("too many export parts")

// CheckLogExportDiskSpace 校验磁盘可用空间。
func CheckLogExportDiskSpace() error {
	minFreeMB := operation_setting.GetLogExportSetting().GetMinFreeDiskMB()
	info := common.GetDiskSpaceInfo()
	if info.Total == 0 {
		// 取不到磁盘信息时不阻断导出，写入失败会在后续被捕获。
		return nil
	}
	if info.Free < uint64(minFreeMB)*1024*1024 {
		return ErrLogExportDiskFull
	}
	return nil
}

// StartLogExport 启动导出 goroutine。调用前必须已抢到运行槽位。
func StartLogExport(job *LogExportJob) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("log export: panic recovered: %v", r))
				job.Status = LogExportStatusFailed
				job.Error = "internal error"
				job.FinishedAt = time.Now().Unix()
				UpdateLogExportJob(job)
				RemoveLogExportJobFiles(job)
			}
			ReleaseLogExportSlot(job.JobID)
		}()
		runLogExport(job)
	}()
}

func runLogExport(job *LogExportJob) {
	job.Status = LogExportStatusRunning
	UpdateLogExportJob(job)

	// 用可取消的 ctx 而不是带 deadline 的：超时按「实际工作时间」判定，见
	// writeLogExport 里的预算检查。闸门让出的时间（CPU 高位暂停、低峰等待、限速休眠）
	// 不该计入任务超时——否则开启低峰模式后，白天创建的任务会先干等几小时再超时失败。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// xlsx 超出行数上限时降级重跑一次。xlsx 本身被限制在 20 万行以内，
	// 重跑浪费的工作量有界且很小。
	for attempt := 0; attempt < 2; attempt++ {
		err := writeLogExport(ctx, job)
		if err == nil {
			job.Status = LogExportStatusReady
			job.Progress = 100
			job.FinishedAt = time.Now().Unix()
			UpdateLogExportJob(job)
			return
		}
		RemoveLogExportJobFiles(job)
		job.Parts = nil
		job.RowCount = 0
		job.BytesOut = 0

		if errors.Is(err, errXlsxRowLimit) && job.Format == LogExportFormatXlsx {
			job.Format = LogExportFormatCSVGz
			job.FormatDowngraded = true
			UpdateLogExportJob(job)
			continue
		}
		if errors.Is(err, context.Canceled) || isLogExportJobCanceled(job.JobID) {
			job.Status = LogExportStatusCanceled
		} else {
			job.Status = LogExportStatusFailed
			job.Error = logExportErrorCode(err)
		}
		job.FinishedAt = time.Now().Unix()
		UpdateLogExportJob(job)
		return
	}
}

// logExportErrorCode 把内部错误映射成稳定的错误码，前端据此渲染用户可读文案。
// 原始错误只进系统日志，不下发给客户端。
func logExportErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrLogExportDiskFull):
		return "disk_full"
	case errors.Is(err, ErrLogExportTooManyParts):
		return "too_many_parts"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrLogSummaryTooManyGroups):
		return "too_many_groups"
	default:
		common.SysError("log export: job failed: " + err.Error())
		return "internal"
	}
}

// 导出模式。
const (
	LogExportModeDetail  = "detail"
	LogExportModeSummary = "summary"
	// LogExportModeBoth 一次扫描同时产出明细与汇总。
	// 分两次导出会把同一段日志扫两遍——对大表来说这是最贵的一步。
	LogExportModeBoth = "both"
)

// IsSummary 报告任务是否**只**产出聚合汇总。空值按 detail 处理，
// 保证升级前创建的任务行为不变。
func (j *LogExportJob) IsSummary() bool { return j.Mode == LogExportModeSummary }

// NeedsSummary 是否需要聚合累加（纯汇总或明细+汇总）。
func (j *LogExportJob) NeedsSummary() bool {
	return j.Mode == LogExportModeSummary || j.Mode == LogExportModeBoth
}

// writeLogExport 执行一次完整导出：按时间窗口由早到晚扫描，逐批写入分片文件。
func writeLogExport(ctx context.Context, job *LogExportJob) (retErr error) {
	setting := operation_setting.GetLogExportSetting()

	// 聚合导出复用同一套扫描循环（限速、CPU 闸门、超时、取消全部原样生效），
	// 只把「逐行写文件」换成「逐行累加，扫完一次性写出」。
	if job.IsSummary() {
		return writeLogExportSummary(ctx, job)
	}

	columnSet, err := ResolveLogExportColumns(job.Columns, true)
	if err != nil {
		return err
	}
	loc := time.Local
	if job.Options.Timezone != "" {
		if parsed, err := time.LoadLocation(job.Options.Timezone); err == nil {
			loc = parsed
		}
	}
	lang := job.Lang
	rctx := &rowCtx{
		loc:          loc,
		channelNames: make(map[int]string, 16),
		translate:    func(key string) string { return i18n.Translate(lang, key) },
	}

	var header []string
	if job.Options.Header {
		header = make([]string, 0, len(columnSet.Columns))
		for _, key := range columnSet.HeaderI18nKeys() {
			header = append(header, i18n.Translate(lang, key))
		}
	}
	writerOpts := LogExportWriterOptions{
		CSVBOM: job.Options.CSVBOM,
		Header: header,
	}

	fields := columnSet.SelectFields()
	// 行级条件要读 other，即使勾选的列一个都不依赖它。other 是行宽的大头，
	// 这会明显抬高单批的传输与解析开销——代价在新建导出时已向管理员说明。
	rowFilter := job.Filters.HasRowFilter()
	if rowFilter && !slices.Contains(fields, "other") {
		fields = append(fields, "other")
		sort.Strings(fields)
	}
	exportStart := job.Filters.StartTimestamp

	// 第一个分片。
	writerOpts.GzipLevel = setting.GetGzipLevel()
	partPath := LogExportPartFilePath(job.JobID, 1, job.Format)
	writer, err := newLogExportPartWriter(partPath, job.Format, writerOpts)
	if err != nil {
		return err
	}
	// 正序扫描：分片内第一行时间最小（StartTime），最后一行时间最大（EndTime）。
	// 一行都没写到时退化成所选区间的起点，下载列表不至于显示 1970。
	currentPart := LogExportPart{Index: 1, Path: partPath, StartTime: exportStart, EndTime: exportStart}
	var partRows int64
	defer func() {
		if writer == nil {
			return
		}
		_, _ = writer.Close()
		writer = nil
		if retErr != nil {
			// 未收尾的分片没进 job.Parts，调用方的 RemoveLogExportJobFiles 清不到它，
			// 只能在这里删掉，否则半成品会一直占着磁盘直到任务过期。
			_ = os.Remove(partPath)
		}
	}()

	// closePart 收尾当前分片并登记到任务。
	closePart := func() error {
		bytes, err := writer.Close()
		writer = nil
		if err != nil {
			return err
		}
		currentPart.Rows = partRows
		currentPart.Bytes = bytes
		job.BytesOut += bytes
		job.Parts = append(job.Parts, currentPart)
		return nil
	}

	// acc 非 nil 表示本次还要顺带产出汇总分片（mode=both）。
	var acc *logSummaryAccumulator
	if job.NeedsSummary() {
		var accErr error
		acc, accErr = newLogSummaryAccumulator(job.SummaryDims, loc, GetLogSummaryMaxGroups())
		if accErr != nil {
			return accErr
		}
	}
	rowBuf := make([]string, len(columnSet.Columns))
	xlsxMaxRows := int64(setting.GetXlsxMaxRows())

	scanner := newLogExportScanner(job, fields, columnSet.NeedChannelName, rctx)
	err = scanner.run(ctx, func(logs []*Log) error {
		for _, l := range logs {
			// 行级条件在这里判定。other 的解析缓存按行归属失效（见 rowCtx.otherMap），
			// 所以先判定、后渲染只会解析一次。
			if rowFilter && !job.Filters.matchesRow(l, rctx) {
				continue
			}
			if job.Format == LogExportFormatXlsx && job.RowCount >= xlsxMaxRows {
				return errXlsxRowLimit
			}
			rowsPerFile := int64(operation_setting.GetLogExportSetting().GetRowsPerFile())
			if partRows >= rowsPerFile {
				if err := closePart(); err != nil {
					return err
				}
				if len(job.Parts) >= operation_setting.GetLogExportSetting().GetMaxParts() {
					return ErrLogExportTooManyParts
				}
				// 每个分片写完复检磁盘，避免把磁盘写满拖垮整个服务。
				if err := CheckLogExportDiskSpace(); err != nil {
					return err
				}
				nextIndex := len(job.Parts) + 1
				partPath = LogExportPartFilePath(job.JobID, nextIndex, job.Format)
				// 压缩等级在此刻现取：改动只对新分片生效，不影响已打开的文件。
				writerOpts.GzipLevel = operation_setting.GetLogExportSetting().GetGzipLevel()
				var writerErr error
				writer, writerErr = newLogExportPartWriter(partPath, job.Format, writerOpts)
				if writerErr != nil {
					return writerErr
				}
				currentPart = LogExportPart{Index: nextIndex, Path: partPath}
				partRows = 0
			}
			if partRows == 0 {
				currentPart.StartTime = l.CreatedAt
			}
			currentPart.EndTime = l.CreatedAt

			rowBuf = columnSet.Render(l, rctx, rowBuf)
			if err := writer.WriteRow(rowBuf); err != nil {
				return err
			}
			partRows++
			job.RowCount++
			// 「明细 + 汇总」：同一行既写进明细，也累加进汇总。
			// 这样两份产出只扫一遍库——分两次导出会把同一段日志扫两遍，
			// 而扫描正是整条链路上最贵的一步。
			if acc != nil {
				if err := acc.Add(l); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := closePart(); err != nil {
		return err
	}
	if acc != nil {
		return appendLogExportSummaryPart(job, acc, rctx.translate)
	}
	return nil
}

// nextLogExportWindowSec 根据刚扫完的窗口的产出，决定下一个窗口的宽度。
//
//   - 整个窗口连一批都没读满 → 窗口太窄，翻倍。稀疏时间段里这能把「31 天 744 次
//     空转往返」迅速收敛到几十次。
//   - 窗口用掉了很多批 → 数据密集，减半收回，重新把单次索引区间限死。
//
// 下限是配置的 window_sec（配置调大时立刻跟上），上限沿用配置层的硬上限，
// 不另设一套阈值。
func nextLogExportWindowSec(current, cfgWindowSec int64, windowRows, windowBatches, batchSize int) int64 {
	if batchSize <= 0 {
		return current
	}
	switch {
	case windowRows < batchSize:
		return min(current*2, operation_setting.MaxLogExportWindowSec)
	case windowBatches >= logExportWindowShrinkBatches:
		return max(current/2, cfgWindowSec)
	default:
		return current
	}
}

// logExportProgress 按时间轴推进计算进度，避免对大表做 COUNT。
// 扫描由早到晚，position 越接近 end 进度越高。
func logExportProgress(start, end, position int64) int {
	if end <= start {
		return 99
	}
	elapsed := position - start
	if elapsed <= 0 {
		return 1
	}
	progress := int(elapsed * 99 / (end - start))
	if progress < 1 {
		return 1
	}
	if progress > 99 {
		return 99
	}
	return progress
}
