package model

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

var ErrEmployeeExportAccess = errors.New("employee export access denied")
var ErrEmployeeExportInvalid = errors.New("invalid employee export configuration")
var ErrEmployeeExportConflict = errors.New("employee export version conflict")
var ErrEmployeeExportNoCustomers = errors.New("employee has no customers to export")
var ErrEmployeeExportTooManyCustomers = errors.New("too many customers in one employee export")
var ErrEmployeeExportNameTaken = errors.New("employee export template name taken")

// These tables are only used by dashboard/export workers, never by relay.
type EmployeeExportTemplate struct {
	Id             int              `json:"id" gorm:"primaryKey"`
	Name           string           `json:"name" gorm:"size:64;uniqueIndex"`
	Enabled        bool             `json:"enabled"`
	Version        int              `json:"version"`
	Definition     string           `json:"-" gorm:"type:text"`
	CreatedBy      int              `json:"created_by"`
	UpdatedBy      int              `json:"updated_by"`
	CreatedAt      int64            `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      int64            `json:"updated_at" gorm:"autoUpdateTime"`
	Columns        []string         `json:"columns" gorm:"-"`
	Format         string           `json:"format" gorm:"-"`
	Mode           string           `json:"mode" gorm:"-"`
	SummaryDims    []string         `json:"summary_dims" gorm:"-"`
	Options        LogExportOptions `json:"options" gorm:"-"`
	AllowedFilters []string         `json:"allowed_filters" gorm:"-"`
	// Key is what employees submit: a builtin ID ("builtin:...") or the decimal row ID.
	Key     string `json:"key" gorm:"-"`
	Builtin bool   `json:"builtin" gorm:"-"`
}

type EmployeeExportScope struct {
	TemplateKey     string `json:"template_key"`
	TemplateName    string `json:"template_name"`
	TemplateVersion int    `json:"template_version"`
	CustomerIDs     []int  `json:"customer_ids"`
	// AllCustomers and CustomerName only label the download file; access is always checked against CustomerIDs.
	AllCustomers bool   `json:"all_customers,omitempty"`
	CustomerName string `json:"customer_name,omitempty"`
}

// employeeBuiltinTemplateIDs are the administrator templates shared read-only with every employee.
var employeeBuiltinTemplateIDs = []string{
	LogExportTemplateCustomerInvoice,
}

var employeeExportFilters = []string{"model_name", "token_name", "group"}

// EmployeeBuiltinExportTemplates reuses the administrator column sets. Admin-only columns are
// removed here so the preview matches the file, and the customer name is prepended where missing
// so a multi-customer file stays identifiable.
func EmployeeBuiltinExportTemplates() []EmployeeExportTemplate {
	out := make([]EmployeeExportTemplate, 0, len(employeeBuiltinTemplateIDs))
	for _, id := range employeeBuiltinTemplateIDs {
		builtin, ok := LookupBuiltinLogExportTemplate(id)
		if !ok {
			continue
		}
		columns := []string{}
		if !slices.Contains(builtin.Columns, "username") {
			columns = append(columns, "username")
		}
		for _, key := range builtin.Columns {
			if col := logExportColumnMap[key]; col != nil && !col.AdminOnly {
				columns = append(columns, key)
			}
		}
		out = append(out, EmployeeExportTemplate{
			Key: id, Builtin: true, Name: builtin.Name, Enabled: true, Version: 1,
			// Compressed CSV; the BOM lets Excel open non-ASCII text directly.
			Columns: columns, Format: LogExportFormatCSVGz, Mode: "detail", SummaryDims: []string{},
			Options: LogExportOptions{Header: true, CSVBOM: true}, AllowedFilters: slices.Clone(employeeExportFilters),
		})
	}
	return out
}

// LookupEmployeeExportTemplate resolves a builtin key or an enabled custom template ID.
func LookupEmployeeExportTemplate(ctx context.Context, key string) (*EmployeeExportTemplate, error) {
	if strings.HasPrefix(key, "builtin:") {
		for _, tpl := range EmployeeBuiltinExportTemplates() {
			if tpl.Key == key {
				return &tpl, nil
			}
		}
		return nil, ErrEmployeeExportAccess
	}
	id, err := strconv.Atoi(key)
	if err != nil || id <= 0 {
		return nil, ErrEmployeeExportAccess
	}
	var tpl EmployeeExportTemplate
	err = DB.WithContext(ctx).First(&tpl, "id = ? AND enabled = ?", id, true).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrEmployeeExportAccess
	}
	if err != nil {
		return nil, err
	}
	return &tpl, nil
}

// ListEmployeeExportTemplates returns builtin templates first, then enabled custom templates.
func ListEmployeeExportTemplates(ctx context.Context) ([]EmployeeExportTemplate, error) {
	custom := []EmployeeExportTemplate{}
	if err := DB.WithContext(ctx).Where("enabled = ?", true).Order("id").Limit(200).Find(&custom).Error; err != nil {
		return nil, err
	}
	return append(EmployeeBuiltinExportTemplates(), custom...), nil
}

// EmployeeExportColumns is every column except admin-only ones (channel and upstream routing).
// Internal columns keep their audience so the editor can badge them; the customer name is shown as
// customer-facing because it identifies rows in a multi-customer file. The returned copies never
// change the audience used by administrator or customer-self exports.
func EmployeeExportColumns() []LogExportColumn {
	columns := []LogExportColumn{}
	for _, column := range LogExportColumns() {
		if column.AdminOnly {
			continue
		}
		if column.Key == "username" {
			column.Audience = LogExportAudienceCustomer
		}
		columns = append(columns, column)
	}
	return columns
}

func (t *EmployeeExportTemplate) Validate() error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len([]rune(t.Name)) > 64 || len(t.Columns) == 0 || len(t.Columns) > LogExportMaxColumns {
		return ErrEmployeeExportInvalid
	}
	seen := map[string]bool{}
	for _, key := range t.Columns {
		col := logExportColumnMap[key]
		if col == nil || col.AdminOnly || seen[key] {
			return ErrEmployeeExportInvalid
		}
		seen[key] = true
	}
	if !ValidLogExportFormat(t.Format) || !slices.Contains([]string{"detail", "summary", "both"}, t.Mode) {
		return ErrEmployeeExportInvalid
	}
	if t.Mode != "detail" && len(t.SummaryDims) == 0 {
		return ErrEmployeeExportInvalid
	}
	seen = map[string]bool{}
	for _, dim := range t.SummaryDims {
		if !slices.Contains([]string{"date", "username", "group", "model_name", "token_name"}, dim) || seen[dim] {
			return ErrEmployeeExportInvalid
		}
		seen[dim] = true
	}
	seen = map[string]bool{}
	for _, filter := range t.AllowedFilters {
		if !slices.Contains(employeeExportFilters, filter) || seen[filter] {
			return ErrEmployeeExportInvalid
		}
		seen[filter] = true
	}
	if t.Options.Timezone != "" {
		if _, err := time.LoadLocation(t.Options.Timezone); err != nil {
			return ErrEmployeeExportInvalid
		}
	}
	return nil
}

func (t *EmployeeExportTemplate) AfterFind(tx *gorm.DB) error {
	var definition EmployeeExportTemplate
	if err := common.UnmarshalJsonStr(t.Definition, &definition); err != nil {
		return err
	}
	t.Columns, t.Format, t.Mode = definition.Columns, definition.Format, definition.Mode
	t.SummaryDims, t.Options, t.AllowedFilters = definition.SummaryDims, definition.Options, definition.AllowedFilters
	if t.SummaryDims == nil {
		t.SummaryDims = []string{}
	}
	if t.AllowedFilters == nil {
		t.AllowedFilters = []string{}
	}
	t.Key, t.Builtin = strconv.Itoa(t.Id), false
	return nil
}

func SaveEmployeeExportTemplate(ctx context.Context, tpl *EmployeeExportTemplate, operator int) error {
	if err := tpl.Validate(); err != nil {
		return err
	}
	data, err := common.Marshal(tpl)
	if err != nil {
		return err
	}
	tpl.Definition = string(data)
	tpl.UpdatedBy = operator
	for _, builtin := range EmployeeBuiltinExportTemplates() {
		if strings.EqualFold(builtin.Name, tpl.Name) {
			return ErrEmployeeExportNameTaken
		}
	}
	var taken int64
	if err := DB.WithContext(ctx).Model(&EmployeeExportTemplate{}).Where("name = ? AND id <> ?", tpl.Name, tpl.Id).Count(&taken).Error; err != nil {
		return err
	}
	if taken > 0 {
		return ErrEmployeeExportNameTaken
	}
	if tpl.Id == 0 {
		tpl.Version, tpl.CreatedBy = 1, operator
		if err := DB.WithContext(ctx).Create(tpl).Error; err != nil {
			return err
		}
		tpl.Key = strconv.Itoa(tpl.Id)
		return nil
	}
	result := DB.WithContext(ctx).Model(&EmployeeExportTemplate{}).
		Where("id = ? AND version = ?", tpl.Id, tpl.Version).
		Updates(map[string]any{"name": tpl.Name, "enabled": tpl.Enabled, "definition": tpl.Definition,
			"version": tpl.Version + 1, "updated_by": operator, "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEmployeeExportConflict
	}
	tpl.Version++
	return nil
}

// AuthorizeEmployeeExport allows every active employee once the global switch is on.
// It always reads the current account state, with a bounded context.
func AuthorizeEmployeeExport(ctx context.Context, userID int) error {
	if !operation_setting.GetLogExportSetting().EmployeeExportEnabled {
		return ErrEmployeeExportAccess
	}
	var count int64
	err := DB.WithContext(ctx).Model(&User{}).Where("id = ? AND status = ?", userID, common.UserStatusEnabled).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrEmployeeExportAccess
	}
	err = DB.WithContext(ctx).Model(&EmployeeProfile{}).Where("user_id = ? AND status = ?", userID, 1).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrEmployeeExportAccess
	}
	return nil
}

func ResolveEmployeeExportCustomers(ctx context.Context, userID int, ids []int, all bool) ([]int, error) {
	limit := operation_setting.GetLogExportSetting().GetEmployeeMaxCustomersPerJob()
	if (!all && len(ids) == 0) || (all && len(ids) != 0) {
		return nil, ErrEmployeeExportInvalid
	}
	if len(ids) > limit {
		return nil, ErrEmployeeExportTooManyCustomers
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, ErrEmployeeExportInvalid
		}
		seen[id] = true
	}
	query := DB.WithContext(ctx).Model(&User{}).Where("inviter_id = ? AND role = ?", userID, common.RoleCommonUser)
	if !all {
		query = query.Where("id IN ?", ids)
	}
	var customers []int
	if err := query.Order("id").Limit(limit+1).Pluck("id", &customers).Error; err != nil {
		return nil, err
	}
	// Ownership is checked before the count so a transferred customer is reported as an access change.
	if !all && len(customers) != len(ids) {
		return nil, ErrEmployeeExportAccess
	}
	if len(customers) == 0 {
		return nil, ErrEmployeeExportNoCustomers
	}
	if len(customers) > limit {
		return nil, ErrEmployeeExportTooManyCustomers
	}
	return customers, nil
}

func ValidateEmployeeExportJob(ctx context.Context, job *LogExportJob) error {
	if job.EmployeeScope == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := AuthorizeEmployeeExport(ctx, job.UserID); err != nil {
		return err
	}
	// A disabled or deleted custom template stops running jobs and old download links.
	if _, err := LookupEmployeeExportTemplate(ctx, job.EmployeeScope.TemplateKey); err != nil {
		return err
	}
	_, err := ResolveEmployeeExportCustomers(ctx, job.UserID, job.EmployeeScope.CustomerIDs, false)
	return err
}
