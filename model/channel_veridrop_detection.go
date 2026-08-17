package model

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type ChannelVeridropDetectionStatus string

type ChannelVeridropDetectionOutcome string

const (
	ChannelVeridropDetectionQueued    ChannelVeridropDetectionStatus = "queued"
	ChannelVeridropDetectionRunning   ChannelVeridropDetectionStatus = "running"
	ChannelVeridropDetectionDone      ChannelVeridropDetectionStatus = "done"
	ChannelVeridropDetectionError     ChannelVeridropDetectionStatus = "error"
	ChannelVeridropDetectionTimeout   ChannelVeridropDetectionStatus = "timeout"
	ChannelVeridropDetectionCancelled ChannelVeridropDetectionStatus = "cancelled"
	ChannelVeridropDetectionSkipped   ChannelVeridropDetectionStatus = "skipped"

	ChannelVeridropOutcomeInProgress ChannelVeridropDetectionOutcome = "in_progress"
	ChannelVeridropOutcomePassed     ChannelVeridropDetectionOutcome = "passed"
	ChannelVeridropOutcomeCompleted  ChannelVeridropDetectionOutcome = "completed"
	ChannelVeridropOutcomeLowScore   ChannelVeridropDetectionOutcome = "low_score"
	ChannelVeridropOutcomeFailed     ChannelVeridropDetectionOutcome = "failed"
	ChannelVeridropOutcomeSkipped    ChannelVeridropDetectionOutcome = "skipped"
	ChannelVeridropOutcomeCancelled  ChannelVeridropDetectionOutcome = "cancelled"

	ChannelVeridropLowScoreThreshold = 50.0
)

var ErrInvalidChannelVeridropBatchCursor = errors.New("invalid channel veridrop batch cursor")

type ChannelVeridropDetection struct {
	ID            int64                           `json:"id" gorm:"primary_key;index:idx_veridrop_status_score_id,priority:3;index:idx_veridrop_score_id,priority:2;index:idx_veridrop_channel_name_id,priority:2;index:idx_veridrop_model_id,priority:2;index:idx_veridrop_updated_id_v2,priority:2;index:idx_veridrop_cleanup,priority:3;index:idx_veridrop_batch_id,priority:2"`
	ChannelID     int                             `json:"channel_id" gorm:"index:idx_veridrop_channel_id"`
	ChannelName   string                          `json:"channel_name" gorm:"type:varchar(255);index:idx_veridrop_channel_name_id,priority:1"`
	ChannelType   int                             `json:"channel_type"`
	Protocol      string                          `json:"protocol" gorm:"type:varchar(32);index:idx_veridrop_protocol_id"`
	Model         string                          `json:"model" gorm:"type:varchar(200);index:idx_veridrop_model;index:idx_veridrop_model_id,priority:1"`
	Mode          string                          `json:"mode" gorm:"type:varchar(32)"`
	Status        ChannelVeridropDetectionStatus  `json:"status" gorm:"type:varchar(32);index:idx_veridrop_status_id;index:idx_veridrop_status_score_id,priority:1;index:idx_veridrop_cleanup,priority:1"`
	BatchTaskID   string                          `json:"batch_task_id" gorm:"type:varchar(64);index:idx_veridrop_batch_id,priority:1"`
	VeridropJobID string                          `json:"veridrop_job_id" gorm:"type:varchar(128);index"`
	Score         float64                         `json:"score" gorm:"index:idx_veridrop_score;index:idx_veridrop_status_score_id,priority:2;index:idx_veridrop_score_id,priority:1"`
	Verdict       string                          `json:"verdict" gorm:"type:varchar(32)"`
	Summary       string                          `json:"summary" gorm:"type:text"`
	RunError      string                          `json:"run_error" gorm:"type:text"`
	Error         string                          `json:"error" gorm:"type:text"`
	ResultJSON    string                          `json:"result_json,omitempty" gorm:"type:text"`
	CreatedAt     int64                           `json:"created_at" gorm:"bigint;index"`
	StartedAt     int64                           `json:"started_at" gorm:"bigint"`
	FinishedAt    int64                           `json:"finished_at" gorm:"bigint;index:idx_veridrop_finished_id"`
	UpdatedAt     int64                           `json:"updated_at" gorm:"bigint;index:idx_veridrop_updated_id_v2,priority:1;index:idx_veridrop_cleanup,priority:2"`
	Outcome       ChannelVeridropDetectionOutcome `json:"outcome" gorm:"-"`
}

func (d *ChannelVeridropDetection) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if d.CreatedAt == 0 {
		d.CreatedAt = now
	}
	if d.UpdatedAt == 0 {
		d.UpdatedAt = now
	}
	return nil
}

func (d *ChannelVeridropDetection) AfterCreate(_ *gorm.DB) error {
	d.Outcome = ClassifyChannelVeridropDetectionOutcome(d)
	return nil
}

func (d *ChannelVeridropDetection) AfterFind(_ *gorm.DB) error {
	d.Outcome = ClassifyChannelVeridropDetectionOutcome(d)
	return nil
}

func ClassifyChannelVeridropDetectionOutcome(detection *ChannelVeridropDetection) ChannelVeridropDetectionOutcome {
	if detection == nil {
		return ChannelVeridropOutcomeCompleted
	}
	switch detection.Status {
	case ChannelVeridropDetectionQueued, ChannelVeridropDetectionRunning:
		return ChannelVeridropOutcomeInProgress
	case ChannelVeridropDetectionError, ChannelVeridropDetectionTimeout:
		return ChannelVeridropOutcomeFailed
	case ChannelVeridropDetectionSkipped:
		return ChannelVeridropOutcomeSkipped
	case ChannelVeridropDetectionCancelled:
		return ChannelVeridropOutcomeCancelled
	}
	verdict := strings.ToLower(strings.TrimSpace(detection.Verdict))
	switch verdict {
	case "passed", "pass", "success":
		return ChannelVeridropOutcomePassed
	case "failed", "fail", "error":
		return ChannelVeridropOutcomeFailed
	case "marginal":
		if detection.Score < ChannelVeridropLowScoreThreshold {
			return ChannelVeridropOutcomeLowScore
		}
	}
	return ChannelVeridropOutcomeCompleted
}

func CreateChannelVeridropDetection(detection *ChannelVeridropDetection) error {
	return DB.Create(detection).Error
}

func GetChannelVeridropDetectionByID(id int64) (*ChannelVeridropDetection, error) {
	var detection ChannelVeridropDetection
	if err := DB.Where("id = ?", id).First(&detection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &detection, nil
}

func UpdateChannelVeridropDetection(id int64, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = common.GetTimestamp()
	return DB.Model(&ChannelVeridropDetection{}).Where("id = ?", id).Updates(updates).Error
}

func GetLatestChannelVeridropDetectionBatchTaskID() (string, error) {
	var batchTaskID string
	err := DB.Model(&ChannelVeridropDetection{}).
		Where("batch_task_id <> ''").
		Order("id DESC").
		Limit(1).
		Pluck("batch_task_id", &batchTaskID).Error
	return batchTaskID, err
}

func GetChannelVeridropDetectionBatchTaskIDByID(id int64) (string, error) {
	if id <= 0 {
		return "", ErrInvalidChannelVeridropBatchCursor
	}
	var batchTaskID string
	err := DB.Model(&ChannelVeridropDetection{}).Where("id = ?", id).Pluck("batch_task_id", &batchTaskID).Error
	if err != nil {
		return "", err
	}
	if batchTaskID == "" {
		return "", ErrInvalidChannelVeridropBatchCursor
	}
	return batchTaskID, nil
}

func CountChannelVeridropDetectionsForCleanup(ctx context.Context, targetTimestamp int64, excludedBatchTaskIDs []string) (int64, error) {
	query := DB.WithContext(ctx).Model(&ChannelVeridropDetection{}).
		Where("status IN ?", []ChannelVeridropDetectionStatus{
			ChannelVeridropDetectionDone,
			ChannelVeridropDetectionError,
			ChannelVeridropDetectionTimeout,
			ChannelVeridropDetectionCancelled,
			ChannelVeridropDetectionSkipped,
		})
	if targetTimestamp > 0 {
		query = query.Where("updated_at < ?", targetTimestamp)
	}
	if len(excludedBatchTaskIDs) > 0 {
		query = query.Where("batch_task_id = '' OR batch_task_id NOT IN ?", excludedBatchTaskIDs)
	}
	var count int64
	err := query.Count(&count).Error
	return count, err
}

func CleanupChannelVeridropDetectionsBatch(ctx context.Context, targetTimestamp int64, batchSize int, excludedBatchTaskIDs []string) (int64, error) {
	return cleanupChannelVeridropDetectionsBatch(ctx, DB, targetTimestamp, batchSize, excludedBatchTaskIDs)
}

func cleanupChannelVeridropDetectionsBatch(ctx context.Context, db *gorm.DB, targetTimestamp int64, batchSize int, excludedBatchTaskIDs []string) (int64, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	if batchSize > 1000 {
		batchSize = 1000
	}

	query := db.WithContext(ctx).Model(&ChannelVeridropDetection{}).
		Where("status IN ?", []ChannelVeridropDetectionStatus{
			ChannelVeridropDetectionDone,
			ChannelVeridropDetectionError,
			ChannelVeridropDetectionTimeout,
			ChannelVeridropDetectionCancelled,
			ChannelVeridropDetectionSkipped,
		})
	if targetTimestamp > 0 {
		query = query.Where("updated_at < ?", targetTimestamp)
	}
	if len(excludedBatchTaskIDs) > 0 {
		query = query.Where("batch_task_id = '' OR batch_task_id NOT IN ?", excludedBatchTaskIDs)
	}

	var ids []int64
	if err := query.Order("id DESC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	result := db.WithContext(ctx).Where("id IN ?", ids).Delete(&ChannelVeridropDetection{})
	return result.RowsAffected, result.Error
}

type ChannelVeridropDetectionListOptions struct {
	BatchTaskID  string
	ChannelID    int
	ChannelName  string
	Model        string
	Mode         string
	Verdict      string
	Outcome      string
	Outcomes     []string
	Keyword      string
	Status       string
	Protocol     string
	MinScore     *float64
	MaxScore     *float64
	UpdatedAfter int64
	ErrorOnly    bool
	BeforeID     int64
	Limit        int
	SortBy       string
	SortOrder    string
}

type ChannelVeridropDetectionSummary struct {
	Total      int64 `json:"total"`
	InProgress int64 `json:"in_progress"`
	Passed     int64 `json:"passed"`
	Completed  int64 `json:"completed"`
	LowScore   int64 `json:"low_score"`
	Failed     int64 `json:"failed"`
	Skipped    int64 `json:"skipped"`
	Cancelled  int64 `json:"cancelled"`
}

func ListChannelVeridropDetections(options ChannelVeridropDetectionListOptions) ([]*ChannelVeridropDetection, error) {
	if options.Limit <= 0 {
		options.Limit = 20
	}
	if options.Limit > 100 {
		options.Limit = 100
	}

	query := applyChannelVeridropDetectionBaseFilters(DB.Model(&ChannelVeridropDetection{}), options)
	query = applyChannelVeridropDetectionOutcomeFilter(query, channelVeridropDetectionOutcomes(options))
	if options.BeforeID > 0 {
		query = query.Where("id < ?", options.BeforeID)
	}

	if options.BeforeID > 0 {
		query = query.Order("id DESC")
	} else {
		sortBy := strings.ToLower(strings.TrimSpace(options.SortBy))
		sortOrder := strings.ToLower(strings.TrimSpace(options.SortOrder))
		validSort := sortOrder == "asc" || sortOrder == "desc"
		sortColumn := "updated_at"
		switch sortBy {
		case "score":
			sortColumn = "score"
		case "channel_name":
			sortColumn = "channel_name"
		case "model":
			sortColumn = "model"
		case "updated_at":
		default:
			validSort = false
		}
		if !validSort {
			sortColumn = "updated_at"
			sortOrder = "desc"
		}
		query = query.Order(sortColumn + " " + strings.ToUpper(sortOrder)).Order("id DESC")
	}

	var detections []*ChannelVeridropDetection
	err := query.Limit(options.Limit).Find(&detections).Error
	return detections, err
}

func applyChannelVeridropDetectionBaseFilters(query *gorm.DB, options ChannelVeridropDetectionListOptions) *gorm.DB {
	if options.BatchTaskID != "" {
		query = query.Where("batch_task_id = ?", options.BatchTaskID)
	}
	if options.ChannelID > 0 {
		query = query.Where("channel_id = ?", options.ChannelID)
	}
	if options.ChannelName != "" {
		query = query.Where("channel_name LIKE ? ESCAPE '!'", "%"+escapeVeridropLikePattern(options.ChannelName)+"%")
	}
	if options.Model != "" {
		query = query.Where("model LIKE ? ESCAPE '!'", "%"+escapeVeridropLikePattern(options.Model)+"%")
	}
	if options.Mode != "" {
		query = query.Where("mode = ?", options.Mode)
	}
	if options.Verdict != "" {
		query = query.Where("verdict LIKE ? ESCAPE '!'", "%"+escapeVeridropLikePattern(options.Verdict)+"%")
	}
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Protocol != "" {
		query = query.Where("protocol = ?", options.Protocol)
	}
	if options.MinScore != nil {
		query = query.Where("score >= ?", *options.MinScore)
	}
	if options.MaxScore != nil {
		query = query.Where("score <= ?", *options.MaxScore)
	}
	if options.UpdatedAfter > 0 {
		query = query.Where("updated_at >= ?", options.UpdatedAfter)
	}
	if options.ErrorOnly {
		query = query.Where("(error <> '' OR run_error <> '')")
	}
	if options.Keyword != "" {
		like := "%" + escapeVeridropLikePattern(options.Keyword) + "%"
		query = query.Where(
			"(channel_name LIKE ? ESCAPE '!' OR model LIKE ? ESCAPE '!' OR verdict LIKE ? ESCAPE '!' OR summary LIKE ? ESCAPE '!' OR error LIKE ? ESCAPE '!' OR run_error LIKE ? ESCAPE '!')",
			like, like, like, like, like, like,
		)
	}
	return query
}

func channelVeridropDetectionOutcomes(options ChannelVeridropDetectionListOptions) []string {
	values := options.Outcomes
	if len(values) == 0 && options.Outcome != "" {
		values = []string{options.Outcome}
	}
	seen := make(map[string]struct{}, len(values))
	outcomes := make([]string, 0, len(values))
	for _, outcome := range values {
		if _, exists := seen[outcome]; exists {
			continue
		}
		switch outcome {
		case string(ChannelVeridropOutcomeInProgress),
			string(ChannelVeridropOutcomePassed),
			string(ChannelVeridropOutcomeCompleted),
			string(ChannelVeridropOutcomeLowScore),
			string(ChannelVeridropOutcomeFailed),
			string(ChannelVeridropOutcomeSkipped),
			string(ChannelVeridropOutcomeCancelled):
			seen[outcome] = struct{}{}
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes
}

func applyChannelVeridropDetectionOutcomeFilter(query *gorm.DB, outcomes []string) *gorm.DB {
	conditions := make([]string, 0, len(outcomes))
	args := make([]any, 0, len(outcomes)*3)
	for _, outcome := range outcomes {
		condition, conditionArgs := channelVeridropDetectionOutcomeCondition(outcome)
		if condition == "" {
			continue
		}
		conditions = append(conditions, condition)
		args = append(args, conditionArgs...)
	}
	if len(conditions) == 0 {
		return query
	}
	return query.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

func channelVeridropDetectionOutcomeCondition(outcome string) (string, []any) {
	switch outcome {
	case string(ChannelVeridropOutcomeInProgress):
		return "status IN ?", []any{[]ChannelVeridropDetectionStatus{
			ChannelVeridropDetectionQueued,
			ChannelVeridropDetectionRunning,
		}}
	case string(ChannelVeridropOutcomePassed):
		return "status = ? AND LOWER(verdict) IN ?", []any{ChannelVeridropDetectionDone, []string{"passed", "pass", "success"}}
	case string(ChannelVeridropOutcomeCompleted):
		return "status = ? AND LOWER(COALESCE(verdict, '')) NOT IN ? AND (LOWER(COALESCE(verdict, '')) <> ? OR score >= ?)", []any{
			ChannelVeridropDetectionDone,
			[]string{"passed", "pass", "success", "failed", "fail", "error"},
			"marginal",
			ChannelVeridropLowScoreThreshold,
		}
	case string(ChannelVeridropOutcomeFailed):
		return "(status IN ? OR (status = ? AND LOWER(verdict) IN ?))", []any{
			[]ChannelVeridropDetectionStatus{ChannelVeridropDetectionError, ChannelVeridropDetectionTimeout},
			ChannelVeridropDetectionDone,
			[]string{"failed", "fail", "error"},
		}
	case string(ChannelVeridropOutcomeLowScore):
		return "status = ? AND score < ? AND LOWER(verdict) = ?", []any{
			ChannelVeridropDetectionDone,
			ChannelVeridropLowScoreThreshold,
			"marginal",
		}
	case string(ChannelVeridropOutcomeSkipped):
		return "status = ?", []any{ChannelVeridropDetectionSkipped}
	case string(ChannelVeridropOutcomeCancelled):
		return "status = ?", []any{ChannelVeridropDetectionCancelled}
	}
	return "", nil
}

func SummarizeChannelVeridropDetections(options ChannelVeridropDetectionListOptions) (ChannelVeridropDetectionSummary, int64, error) {
	var summary ChannelVeridropDetectionSummary
	query := applyChannelVeridropDetectionBaseFilters(DB.Model(&ChannelVeridropDetection{}), options)
	err := query.Select(`
		COUNT(*) AS total,
		COALESCE(SUM(CASE WHEN status IN (?, ?) THEN 1 ELSE 0 END), 0) AS in_progress,
		COALESCE(SUM(CASE WHEN status = ? AND LOWER(COALESCE(verdict, '')) IN (?, ?, ?) THEN 1 ELSE 0 END), 0) AS passed,
		COALESCE(SUM(CASE WHEN status = ? AND LOWER(COALESCE(verdict, '')) NOT IN (?, ?, ?, ?, ?, ?) AND (LOWER(COALESCE(verdict, '')) <> ? OR score >= ?) THEN 1 ELSE 0 END), 0) AS completed,
		COALESCE(SUM(CASE WHEN status = ? AND LOWER(COALESCE(verdict, '')) = ? AND score < ? THEN 1 ELSE 0 END), 0) AS low_score,
		COALESCE(SUM(CASE WHEN status IN (?, ?) OR (status = ? AND LOWER(COALESCE(verdict, '')) IN (?, ?, ?)) THEN 1 ELSE 0 END), 0) AS failed,
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS skipped,
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS cancelled`,
		ChannelVeridropDetectionQueued, ChannelVeridropDetectionRunning,
		ChannelVeridropDetectionDone, "passed", "pass", "success",
		ChannelVeridropDetectionDone, "passed", "pass", "success", "failed", "fail", "error", "marginal", ChannelVeridropLowScoreThreshold,
		ChannelVeridropDetectionDone, "marginal", ChannelVeridropLowScoreThreshold,
		ChannelVeridropDetectionError, ChannelVeridropDetectionTimeout, ChannelVeridropDetectionDone, "failed", "fail", "error",
		ChannelVeridropDetectionSkipped,
		ChannelVeridropDetectionCancelled,
	).Scan(&summary).Error
	if err != nil {
		return ChannelVeridropDetectionSummary{}, 0, err
	}

	outcomes := channelVeridropDetectionOutcomes(options)
	if len(outcomes) == 0 {
		return summary, summary.Total, nil
	}
	var matchingCount int64
	for _, outcome := range outcomes {
		switch outcome {
		case string(ChannelVeridropOutcomeInProgress):
			matchingCount += summary.InProgress
		case string(ChannelVeridropOutcomePassed):
			matchingCount += summary.Passed
		case string(ChannelVeridropOutcomeCompleted):
			matchingCount += summary.Completed
		case string(ChannelVeridropOutcomeLowScore):
			matchingCount += summary.LowScore
		case string(ChannelVeridropOutcomeFailed):
			matchingCount += summary.Failed
		case string(ChannelVeridropOutcomeSkipped):
			matchingCount += summary.Skipped
		case string(ChannelVeridropOutcomeCancelled):
			matchingCount += summary.Cancelled
		}
	}
	return summary, matchingCount, nil
}

func escapeVeridropLikePattern(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}

func migrateChannelVeridropUpdatedIndex(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasIndex(&ChannelVeridropDetection{}, "idx_veridrop_updated_id_v2") {
		return errors.New("missing idx_veridrop_updated_id_v2 after migration")
	}
	if migrator.HasIndex(&ChannelVeridropDetection{}, "idx_veridrop_updated_id") {
		return migrator.DropIndex(&ChannelVeridropDetection{}, "idx_veridrop_updated_id")
	}
	return nil
}

func FindEnabledChannelsForVeridropAfterID(lastID int, batchSize int, channelIDs []int) ([]*Channel, error) {
	return FindChannelsForVeridropAfterID(lastID, batchSize, channelIDs, false)
}

func FindChannelsForVeridropAfterID(lastID int, batchSize int, channelIDs []int, includeDisabled bool) ([]*Channel, error) {
	if batchSize <= 0 {
		batchSize = 50
	}
	query := DB.
		Order("id asc").
		Limit(batchSize)
	if !includeDisabled {
		query = query.Where("status = ?", common.ChannelStatusEnabled)
	}
	if lastID > 0 {
		query = query.Where("id > ?", lastID)
	}
	if len(channelIDs) > 0 {
		query = query.Where("id IN ?", channelIDs)
	}

	var channels []*Channel
	return channels, query.Find(&channels).Error
}
