package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmployeeExportTemplateValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*EmployeeExportTemplate)
		valid bool
	}{
		{"valid", func(t *EmployeeExportTemplate) {}, true},
		{"customer identity", func(t *EmployeeExportTemplate) { t.Columns = []string{"username", "cost_usd"} }, true},
		{"empty name", func(t *EmployeeExportTemplate) { t.Name = " " }, false},
		{"64 characters", func(t *EmployeeExportTemplate) { t.Name = strings.Repeat("名", 64) }, true},
		{"65 characters", func(t *EmployeeExportTemplate) { t.Name = strings.Repeat("名", 65) }, false},
		{"empty columns", func(t *EmployeeExportTemplate) { t.Columns = nil }, false},
		{"internal columns", func(t *EmployeeExportTemplate) { t.Columns = []string{"ip", "quota", "content", "use_time"} }, true},
		{"admin-only column", func(t *EmployeeExportTemplate) { t.Columns = []string{"channel_name"} }, false},
		{"admin-only channel id", func(t *EmployeeExportTemplate) { t.Columns = []string{"created_at", "channel_id"} }, false},
		{"unknown column", func(t *EmployeeExportTemplate) { t.Columns = []string{"missing"} }, false},
		{"duplicate column", func(t *EmployeeExportTemplate) { t.Columns = []string{"created_at", "created_at"} }, false},
		{"invalid format", func(t *EmployeeExportTemplate) { t.Format = "pdf" }, false},
		{"invalid mode", func(t *EmployeeExportTemplate) { t.Mode = "unknown" }, false},
		{"summary empty", func(t *EmployeeExportTemplate) { t.Mode = "summary" }, false},
		{"summary valid", func(t *EmployeeExportTemplate) { t.Mode = "summary"; t.SummaryDims = []string{"date"} }, true},
		{"summary internal", func(t *EmployeeExportTemplate) { t.Mode = "both"; t.SummaryDims = []string{"channel"} }, false},
		{"invalid filter", func(t *EmployeeExportTemplate) { t.AllowedFilters = []string{"channel"} }, false},
		{"duplicate filter", func(t *EmployeeExportTemplate) { t.AllowedFilters = []string{"model_name", "model_name"} }, false},
		{"invalid timezone", func(t *EmployeeExportTemplate) { t.Options.Timezone = "invalid/zone" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tpl := EmployeeExportTemplate{Name: "Invoice", Columns: []string{"created_at", "cost_usd"}, Format: "csv_gz", Mode: "detail"}
			tc.edit(&tpl)
			if tc.valid {
				require.NoError(t, tpl.Validate())
			} else {
				require.Error(t, tpl.Validate())
			}
		})
	}
}
