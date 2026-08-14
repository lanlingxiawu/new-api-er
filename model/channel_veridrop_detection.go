package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type ChannelVeridropDetectionStatus string

const (
	ChannelVeridropDetectionQueued    ChannelVeridropDetectionStatus = "queued"
	ChannelVeridropDetectionRunning   ChannelVeridropDetectionStatus = "running"
	ChannelVeridropDetectionDone      ChannelVeridropDetectionStatus = "done"
	ChannelVeridropDetectionError     ChannelVeridropDetectionStatus = "error"
	ChannelVeridropDetectionTimeout   ChannelVeridropDetectionStatus = "timeout"
	ChannelVeridropDetectionCancelled ChannelVeridropDetectionStatus = "cancelled"
	ChannelVeridropDetectionSkipped   ChannelVeridropDetectionStatus = "skipped"
)

type ChannelVeridropDetection struct {
	ID            int64                          `json:"id" gorm:"primary_key"`
	ChannelID     int                            `json:"channel_id" gorm:"index:idx_veridrop_channel_id"`
	ChannelName   string                         `json:"channel_name" gorm:"type:varchar(255)"`
	ChannelType   int                            `json:"channel_type"`
	Protocol      string                         `json:"protocol" gorm:"type:varchar(32);index:idx_veridrop_protocol_id"`
	Model         string                         `json:"model" gorm:"type:varchar(200);index:idx_veridrop_model"`
	Mode          string                         `json:"mode" gorm:"type:varchar(32)"`
	Status        ChannelVeridropDetectionStatus `json:"status" gorm:"type:varchar(32);index:idx_veridrop_status_id"`
	VeridropJobID string                         `json:"veridrop_job_id" gorm:"type:varchar(128);index"`
	Score         float64                        `json:"score" gorm:"index:idx_veridrop_score"`
	Verdict       string                         `json:"verdict" gorm:"type:varchar(32)"`
	Summary       string                         `json:"summary" gorm:"type:text"`
	RunError      string                         `json:"run_error" gorm:"type:text"`
	Error         string                         `json:"error" gorm:"type:text"`
	ResultJSON    string                         `json:"result_json,omitempty" gorm:"type:text"`
	CreatedAt     int64                          `json:"created_at" gorm:"bigint;index"`
	StartedAt     int64                          `json:"started_at" gorm:"bigint"`
	FinishedAt    int64                          `json:"finished_at" gorm:"bigint;index:idx_veridrop_finished_id"`
	UpdatedAt     int64                          `json:"updated_at" gorm:"bigint;index:idx_veridrop_updated_id"`
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

type ChannelVeridropDetectionListOptions struct {
	ChannelID    int
	ChannelName  string
	Model        string
	Mode         string
	Verdict      string
	Keyword      string
	Status       string
	Protocol     string
	MinScore     *float64
	MaxScore     *float64
	UpdatedAfter int64
	ErrorOnly    bool
	BeforeID     int64
	Limit        int
}

func ListChannelVeridropDetections(options ChannelVeridropDetectionListOptions) ([]*ChannelVeridropDetection, error) {
	if options.Limit <= 0 {
		options.Limit = 20
	}
	if options.Limit > 100 {
		options.Limit = 100
	}

	query := DB.Model(&ChannelVeridropDetection{})
	if options.ChannelID > 0 {
		query = query.Where("channel_id = ?", options.ChannelID)
	}
	if options.ChannelName != "" {
		query = query.Where("channel_name LIKE ?", "%"+options.ChannelName+"%")
	}
	if options.Model != "" {
		query = query.Where("model LIKE ?", "%"+options.Model+"%")
	}
	if options.Mode != "" {
		query = query.Where("mode = ?", options.Mode)
	}
	if options.Verdict != "" {
		query = query.Where("verdict LIKE ?", "%"+options.Verdict+"%")
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
		like := "%" + options.Keyword + "%"
		query = query.Where("(verdict LIKE ? OR summary LIKE ? OR error LIKE ? OR run_error LIKE ?)", like, like, like, like)
	}
	if options.BeforeID > 0 {
		query = query.Where("id < ?", options.BeforeID)
	}

	var detections []*ChannelVeridropDetection
	err := query.Order("id desc").Limit(options.Limit).Find(&detections).Error
	return detections, err
}

func FindEnabledChannelsForVeridropAfterID(lastID int, batchSize int, channelIDs []int) ([]*Channel, error) {
	if batchSize <= 0 {
		batchSize = 50
	}
	query := DB.
		Where("status = ?", common.ChannelStatusEnabled).
		Order("id asc").
		Limit(batchSize)
	if lastID > 0 {
		query = query.Where("id > ?", lastID)
	}
	if len(channelIDs) > 0 {
		query = query.Where("id IN ?", channelIDs)
	}

	var channels []*Channel
	return channels, query.Find(&channels).Error
}
