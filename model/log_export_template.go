package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// LogExportTemplate 用户自定义导出模板。
//
// 存主库（非 LOG_DB），仅管理页面读写，relay 链路完全不访问。表规模最多几千行。
// Columns/Options 用 TEXT 存 JSON 字符串（Rule 2：不用 JSONB，三库通用）。
type LogExportTemplate struct {
	Id     int    `json:"id" gorm:"primaryKey"`
	UserId int    `json:"user_id" gorm:"index:idx_let_user_name,priority:1"`
	Name   string `json:"name" gorm:"type:varchar(64);index:idx_let_user_name,priority:2"`
	// LogCategory 预留：common / drawing / task。
	LogCategory string `json:"log_category" gorm:"type:varchar(16);default:'common'"`
	Columns     string `json:"-" gorm:"type:text"`
	Format      string `json:"format" gorm:"type:varchar(16);default:'csv_gz'"`
	Options     string `json:"-" gorm:"type:text"`
	// IsShared 管理员发布的共享模板，对所有人只读可见。
	IsShared  bool  `json:"is_shared" gorm:"index;default:false"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`

	// 以下为出参字段，不落库。
	ColumnKeys []string         `json:"columns" gorm:"-"`
	Opts       LogExportOptions `json:"options" gorm:"-"`
}

const logExportTemplateNameMaxLen = 64

var (
	ErrLogExportTemplateNameEmpty = errors.New("template name is empty")
	ErrLogExportTemplateNameLong  = errors.New("template name too long")
	ErrLogExportTemplateExists    = errors.New("template name already exists")
	ErrLogExportTemplateLimit     = errors.New("template limit reached")
	ErrLogExportTemplateNotFound  = errors.New("template not found")
)

// hydrate 把存储的 JSON 字符串展开成出参字段。
func (t *LogExportTemplate) hydrate() {
	t.ColumnKeys = nil
	if t.Columns != "" {
		_ = common.UnmarshalJsonStr(t.Columns, &t.ColumnKeys)
	}
	t.Opts = LogExportOptions{CSVBOM: true, Header: true}
	if t.Options != "" {
		_ = common.UnmarshalJsonStr(t.Options, &t.Opts)
	}
}

// dehydrate 把出参字段序列化回存储字段，并做校验。
func (t *LogExportTemplate) dehydrate() error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return ErrLogExportTemplateNameEmpty
	}
	if len([]rune(t.Name)) > logExportTemplateNameMaxLen {
		return ErrLogExportTemplateNameLong
	}
	// 列集合按注册表校验：未知 key 直接报错，避免坏模板被静默存下来。
	if _, err := ResolveLogExportColumns(t.ColumnKeys, true); err != nil {
		return err
	}
	if !ValidLogExportFormat(t.Format) {
		t.Format = LogExportFormatCSVGz
	}
	if t.LogCategory == "" {
		t.LogCategory = "common"
	}
	columns, err := common.Marshal(t.ColumnKeys)
	if err != nil {
		return err
	}
	t.Columns = string(columns)
	options, err := common.Marshal(t.Opts)
	if err != nil {
		return err
	}
	t.Options = string(options)
	return nil
}

// ListLogExportTemplates 返回用户可见的模板：自己的 + 共享的。
func ListLogExportTemplates(userId int) ([]*LogExportTemplate, error) {
	var templates []*LogExportTemplate
	err := DB.Where("user_id = ? OR is_shared = ?", userId, true).
		Order("id desc").Find(&templates).Error
	if err != nil {
		return nil, err
	}
	for _, tpl := range templates {
		tpl.hydrate()
	}
	return templates, nil
}

// GetLogExportTemplate 按 ID 取模板。
func GetLogExportTemplate(id int) (*LogExportTemplate, error) {
	var tpl LogExportTemplate
	err := DB.First(&tpl, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	tpl.hydrate()
	return &tpl, nil
}

// CanReadLogExportTemplate 报告用户是否可读该模板。
func CanReadLogExportTemplate(tpl *LogExportTemplate, userId int) bool {
	return tpl.UserId == userId || tpl.IsShared
}

// CanWriteLogExportTemplate 报告用户是否可改该模板。共享模板对非属主只读。
func CanWriteLogExportTemplate(tpl *LogExportTemplate, userId int, isRoot bool) bool {
	return tpl.UserId == userId || isRoot
}

// CreateLogExportTemplate 新建模板。
func CreateLogExportTemplate(tpl *LogExportTemplate) error {
	if err := tpl.dehydrate(); err != nil {
		return err
	}
	var count int64
	if err := DB.Model(&LogExportTemplate{}).Where("user_id = ?", tpl.UserId).Count(&count).Error; err != nil {
		return err
	}
	if count >= int64(operation_setting.GetLogExportSetting().GetMaxTemplatesPerUser()) {
		return ErrLogExportTemplateLimit
	}
	var existing int64
	if err := DB.Model(&LogExportTemplate{}).
		Where("user_id = ? AND name = ?", tpl.UserId, tpl.Name).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return ErrLogExportTemplateExists
	}
	now := time.Now().Unix()
	tpl.CreatedAt = now
	tpl.UpdatedAt = now
	if err := DB.Create(tpl).Error; err != nil {
		return err
	}
	tpl.hydrate()
	return nil
}

// UpdateLogExportTemplate 更新模板的名称、列、格式与选项。
func UpdateLogExportTemplate(tpl *LogExportTemplate) error {
	if err := tpl.dehydrate(); err != nil {
		return err
	}
	var conflict int64
	if err := DB.Model(&LogExportTemplate{}).
		Where("user_id = ? AND name = ? AND id <> ?", tpl.UserId, tpl.Name, tpl.Id).
		Count(&conflict).Error; err != nil {
		return err
	}
	if conflict > 0 {
		return ErrLogExportTemplateExists
	}
	tpl.UpdatedAt = time.Now().Unix()
	err := DB.Model(&LogExportTemplate{}).Where("id = ?", tpl.Id).Updates(map[string]any{
		"name":       tpl.Name,
		"columns":    tpl.Columns,
		"format":     tpl.Format,
		"options":    tpl.Options,
		"is_shared":  tpl.IsShared,
		"updated_at": tpl.UpdatedAt,
	}).Error
	if err != nil {
		return err
	}
	tpl.hydrate()
	return nil
}

// DeleteLogExportTemplate 删除模板。
func DeleteLogExportTemplate(id int) error {
	return DB.Delete(&LogExportTemplate{}, "id = ?", id).Error
}
