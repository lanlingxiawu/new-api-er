package model

import (
	"context"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Prefer the harness's real DB. SQLite is only a no-env fallback; no dialect-specific SQL is used.
func employeeExportTestDB(t *testing.T) {
	t.Helper()
	if DB == nil {
		require.Empty(t, os.Getenv("SQL_DSN"), "a configured project database must not silently fall back")
		oldDB, oldLogDB := DB, LOG_DB
		oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		DB, LOG_DB = db, db
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
		common.SetLogDatabaseType(common.DatabaseTypeSQLite)
		initCol()
		require.NoError(t, DB.AutoMigrate(&User{}, &EmployeeProfile{}, &Log{}))
		t.Cleanup(func() {
			DB, LOG_DB = oldDB, oldLogDB
			common.SetMainDatabaseType(oldMain)
			common.SetLogDatabaseType(oldLog)
			initCol()
			_ = sqlDB.Close()
		})
	}
	require.NoError(t, DB.AutoMigrate(&EmployeeExportTemplate{}))
}

func mkExportEmployee(t *testing.T) *User {
	t.Helper()
	employee := mkUser(t, nil)
	profile := &EmployeeProfile{UserId: employee.Id, Status: 1}
	require.NoError(t, DB.Create(profile).Error)
	deleteByID(t, &EmployeeProfile{}, profile.Id)
	return employee
}

func employeeExportFixture(t *testing.T) (*User, *User, *EmployeeExportTemplate) {
	t.Helper()
	employeeExportTestDB(t)
	employee := mkExportEmployee(t)
	customer := mkUser(t, func(u *User) { u.InviterId = employee.Id })
	tpl := &EmployeeExportTemplate{Name: uniq("employee-export"), Enabled: true, Columns: []string{"created_at", "cost_usd"}, Format: "csv_gz", Mode: "detail", Options: LogExportOptions{Header: true, Timezone: "UTC"}}
	require.NoError(t, SaveEmployeeExportTemplate(context.Background(), tpl, employee.Id))
	t.Cleanup(func() { DB.Delete(&EmployeeExportTemplate{}, tpl.Id) })
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) { s.EmployeeExportEnabled = true })
	return employee, customer, tpl
}

func TestEmployeeExportAccessAndCustomerScope(t *testing.T) {
	employee, customer, tpl := employeeExportFixture(t)
	ctx := context.Background()
	other := mkUser(t, nil)
	// There is no per-employee grant: the global switch alone opens export to every active employee.
	require.NoError(t, AuthorizeEmployeeExport(ctx, mkExportEmployee(t).Id))
	require.ErrorIs(t, AuthorizeEmployeeExport(ctx, other.Id), ErrEmployeeExportAccess, "non-employees stay denied")
	require.Equal(t, strconv.Itoa(tpl.Id), tpl.Key)
	job := &LogExportJob{UserID: employee.Id, EmployeeScope: &EmployeeExportScope{
		TemplateKey: tpl.Key, CustomerIDs: []int{customer.Id},
	}}
	require.NoError(t, ValidateEmployeeExportJob(ctx, job))
	for _, tc := range []struct {
		ids []int
		all bool
	}{
		{nil, false}, {[]int{0}, false}, {[]int{customer.Id, customer.Id}, false},
		{[]int{other.Id}, false}, {[]int{customer.Id, other.Id}, false}, {[]int{customer.Id}, true},
	} {
		_, err := ResolveEmployeeExportCustomers(ctx, employee.Id, tc.ids, tc.all)
		require.Error(t, err)
	}
	ids, err := ResolveEmployeeExportCustomers(ctx, employee.Id, nil, true)
	require.NoError(t, err)
	require.Equal(t, []int{customer.Id}, ids)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", customer.Id).Update("inviter_id", other.Id).Error)
	require.ErrorIs(t, ValidateEmployeeExportJob(ctx, job), ErrEmployeeExportAccess)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", customer.Id).Update("inviter_id", employee.Id).Error)
	require.NoError(t, DB.Model(&EmployeeExportTemplate{}).Where("id = ?", tpl.Id).Update("enabled", false).Error)
	require.ErrorIs(t, ValidateEmployeeExportJob(ctx, job), ErrEmployeeExportAccess)
	require.NoError(t, DB.Model(&EmployeeExportTemplate{}).Where("id = ?", tpl.Id).Update("enabled", true).Error)
	require.NoError(t, ValidateEmployeeExportJob(ctx, job))

	builtinJob := &LogExportJob{UserID: employee.Id, EmployeeScope: &EmployeeExportScope{
		TemplateKey: LogExportTemplateCustomerInvoice, CustomerIDs: []int{customer.Id},
	}}
	require.NoError(t, ValidateEmployeeExportJob(ctx, builtinJob))
	// Only the three reconciliation builtins are shared; other admin builtins and malformed keys are refused.
	for _, key := range []string{"", "0", "-1", "abc", "builtin:", LogExportTemplateBilling, LogExportTemplateLegacy, LogExportTemplateAudit, LogExportTemplateFull, strconv.Itoa(tpl.Id + 100000000)} {
		_, err := LookupEmployeeExportTemplate(ctx, key)
		require.ErrorIs(t, err, ErrEmployeeExportAccess, key)
	}

	require.NoError(t, DB.Model(&EmployeeProfile{}).Where("user_id = ?", employee.Id).Update("status", 2).Error)
	require.ErrorIs(t, ValidateEmployeeExportJob(ctx, job), ErrEmployeeExportAccess)
	require.ErrorIs(t, ValidateEmployeeExportJob(ctx, builtinJob), ErrEmployeeExportAccess)
	require.NoError(t, DB.Model(&EmployeeProfile{}).Where("user_id = ?", employee.Id).Update("status", 1).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", employee.Id).Update("status", common.UserStatusDisabled).Error)
	require.ErrorIs(t, ValidateEmployeeExportJob(ctx, job), ErrEmployeeExportAccess)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", employee.Id).Update("status", common.UserStatusEnabled).Error)
	require.NoError(t, DB.Delete(&EmployeeExportTemplate{}, tpl.Id).Error)
	require.ErrorIs(t, ValidateEmployeeExportJob(ctx, job), ErrEmployeeExportAccess, "deleted templates revoke running jobs")
	require.NoError(t, ValidateEmployeeExportJob(ctx, builtinJob))
}

func TestEmployeeBuiltinExportTemplates(t *testing.T) {
	templates := EmployeeBuiltinExportTemplates()
	keys := []string{}
	for _, tpl := range templates {
		keys = append(keys, tpl.Key)
		require.True(t, tpl.Builtin)
		require.True(t, tpl.Enabled)
		require.Equal(t, "detail", tpl.Mode)
		require.Equal(t, LogExportFormatCSVGz, tpl.Format, "builtin templates export a compressed package")
		require.Equal(t, employeeExportFilters, tpl.AllowedFilters)
		require.Contains(t, tpl.Columns, "username", "multi-customer files need the customer name")
		set, err := ResolveLogExportColumns(tpl.Columns, false)
		require.NoError(t, err)
		require.Empty(t, set.Dropped, "the preview must match the file: no admin-only column may remain")
		admin, ok := LookupBuiltinLogExportTemplate(tpl.Key)
		require.True(t, ok)
		require.Equal(t, LogExportPurposeReconciliation, admin.Purpose)
		for _, key := range admin.Columns {
			require.Equal(t, !logExportColumnMap[key].AdminOnly, slices.Contains(tpl.Columns, key), key)
		}
	}
	require.Equal(t, []string{LogExportTemplateCustomerInvoice}, keys, "only the customer invoice is built in")
	require.Equal(t, "username", templates[0].Columns[0], "the customer invoice gains the customer name as its first column")
	templates[0].Columns[0] = "mutated"
	templates[0].AllowedFilters[0] = "mutated"
	fresh := EmployeeBuiltinExportTemplates()[0]
	require.Equal(t, "username", fresh.Columns[0], "callers must not share the builtin definition")
	require.Equal(t, employeeExportFilters[0], fresh.AllowedFilters[0])
	require.Equal(t, "model_name", employeeExportFilters[0])
}

func TestEmployeeExportCRUDAndLimits(t *testing.T) {
	employee, customer, tpl := employeeExportFixture(t)
	ctx := context.Background()
	var loaded EmployeeExportTemplate
	require.NoError(t, DB.First(&loaded, tpl.Id).Error)
	require.Equal(t, tpl.Columns, loaded.Columns)
	require.Equal(t, tpl.Options, loaded.Options)
	loaded.Name += "-updated"
	require.NoError(t, SaveEmployeeExportTemplate(ctx, &loaded, employee.Id))
	require.Equal(t, 2, loaded.Version)
	require.ErrorIs(t, SaveEmployeeExportTemplate(ctx, tpl, employee.Id), ErrEmployeeExportConflict)
	listed, err := ListEmployeeExportTemplates(ctx)
	require.NoError(t, err)
	require.Equal(t, LogExportTemplateCustomerInvoice, listed[0].Key, "builtin templates come first")
	require.True(t, slices.ContainsFunc(listed, func(item EmployeeExportTemplate) bool { return item.Key == tpl.Key }))
	require.NoError(t, DB.Model(&EmployeeExportTemplate{}).Where("id = ?", tpl.Id).Update("enabled", false).Error)
	listed, err = ListEmployeeExportTemplates(ctx)
	require.NoError(t, err)
	require.False(t, slices.ContainsFunc(listed, func(item EmployeeExportTemplate) bool { return item.Key == tpl.Key }), "disabled templates are hidden from employees")
	second := mkUser(t, func(u *User) { u.InviterId = employee.Id })
	operation_setting.GetLogExportSetting().EmployeeMaxCustomersPerJob = 1
	_, err = ResolveEmployeeExportCustomers(ctx, employee.Id, []int{customer.Id}, false)
	require.NoError(t, err)
	_, err = ResolveEmployeeExportCustomers(ctx, employee.Id, []int{customer.Id, second.Id}, false)
	require.ErrorIs(t, err, ErrEmployeeExportTooManyCustomers)
	_, err = ResolveEmployeeExportCustomers(ctx, employee.Id, nil, true)
	require.ErrorIs(t, err, ErrEmployeeExportTooManyCustomers, "all customers over the limit is a count problem, not an access problem")
	operation_setting.GetLogExportSetting().EmployeeMaxCustomersPerJob = 2
	_, err = ResolveEmployeeExportCustomers(ctx, employee.Id, nil, true)
	require.NoError(t, err)
	_, err = ResolveEmployeeExportCustomers(ctx, mkExportEmployee(t).Id, nil, true)
	require.ErrorIs(t, err, ErrEmployeeExportNoCustomers)
	operation_setting.GetLogExportSetting().EmployeeExportEnabled = false
	require.ErrorIs(t, AuthorizeEmployeeExport(ctx, employee.Id), ErrEmployeeExportAccess)
}

func TestEmployeeExportQueryPlan(t *testing.T) {
	_, customer, _ := employeeExportFixture(t)
	prefix := "EXPLAIN "
	if LOG_DB.Dialector.Name() == "sqlite" {
		prefix = "EXPLAIN QUERY PLAN "
	}
	var plan []map[string]any
	require.NoError(t, LOG_DB.Raw(prefix+"SELECT id, created_at FROM logs WHERE user_id = ? AND created_at >= ? AND created_at <= ? ORDER BY created_at, id LIMIT 3000", customer.Id, time.Now().Unix()-3600, time.Now().Unix()).Scan(&plan).Error)
	require.NotEmpty(t, plan)
	for _, row := range plan {
		for key, value := range row {
			if bytes, ok := value.([]byte); ok {
				row[key] = string(bytes)
			}
		}
		t.Logf("employee export query plan: %v", row)
	}
}

func TestEmployeeExportWorkerScopeAndSummary(t *testing.T) {
	employee, customer, tpl := employeeExportFixture(t)
	enableRedis(t)
	second := mkUser(t, func(u *User) { u.InviterId = employee.Id })
	outsider := mkUser(t, nil)
	base := time.Now().Unix() - 600
	for _, u := range []*User{customer, second, outsider} {
		mkExportLog(t, func(l *Log) { l.UserId = u.Id; l.Username = u.Username; l.CreatedAt = base; l.Quota = 100 })
	}
	job := &LogExportJob{
		JobID: NewLogExportJobID(), UserID: employee.Id, Format: "csv_gz", Columns: tpl.Columns,
		Mode: "both", SummaryDims: []string{"username"}, Options: tpl.Options, Lang: "en",
		Filters:       LogExportFilter{StartTimestamp: base, EndTimestamp: base},
		EmployeeScope: &EmployeeExportScope{TemplateKey: tpl.Key, CustomerIDs: []int{customer.Id, second.Id}},
	}
	job.Columns = []string{"username", "cost_usd"}
	require.NoError(t, runExportJob(t, job))
	require.Equal(t, int64(2), job.RowCount)
	require.Len(t, job.Parts, 2)
	detailRows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Equal(t, []string{"User", "Cost (USD)"}, detailRows[0])
	require.ElementsMatch(t, []string{customer.Username, second.Username}, []string{detailRows[1][0], detailRows[2][0]})
	rows, _ := readCSVGz(t, job.Parts[1].Path)
	require.Len(t, rows, 3)
	require.Len(t, rows[0], 6, "without a quota column the summary omits quota")
	require.Equal(t, []string{"User", "Calls", "Input Tokens", "Output Tokens", "Total Tokens", "Cost (USD)"}, rows[0])
	for _, row := range rows[1:] {
		require.Len(t, row, 6, "rows and header drop the same column")
	}
	require.NotEqual(t, outsider.Username, rows[1][0])
	require.NotEqual(t, outsider.Username, rows[2][0])
	require.NotEqual(t, rows[1][0], rows[2][0], "both-mode scans must select summary dimensions")
	// A template that selects quota gets it in the summary as well.
	withQuota := *job
	withQuota.JobID = NewLogExportJobID()
	withQuota.Parts, withQuota.RowCount = nil, 0
	withQuota.Columns = []string{"username", "quota", "cost_usd"}
	require.NoError(t, runExportJob(t, &withQuota))
	quotaRows, _ := readCSVGz(t, withQuota.Parts[1].Path)
	require.Equal(t, []string{"User", "Calls", "Input Tokens", "Output Tokens", "Total Tokens", "Quota", "Cost (USD)"}, quotaRows[0])
	require.Equal(t, "100", quotaRows[1][5])
	// Empty scopes must fail before any unscoped scan.
	job.EmployeeScope.CustomerIDs = nil
	require.Error(t, runExportJob(t, job))
}

// A snapshot that still carries an admin-only column must not leak it into an employee file.
func TestEmployeeExportWorkerDropsAdminOnlyColumns(t *testing.T) {
	employee, customer, _ := employeeExportFixture(t)
	enableRedis(t)
	base := time.Now().Unix() - 700
	mkExportLog(t, func(l *Log) {
		l.UserId = customer.Id
		l.Username = customer.Username
		l.CreatedAt = base
		l.ChannelId = 42
	})
	job := &LogExportJob{
		JobID: NewLogExportJobID(), UserID: employee.Id, Format: "csv_gz", Mode: "detail", Lang: "en",
		Columns: []string{"username", "channel_id", "retry_chain", "cost_usd"}, Options: LogExportOptions{Header: true, Timezone: "UTC"},
		Filters:       LogExportFilter{StartTimestamp: base, EndTimestamp: base},
		EmployeeScope: &EmployeeExportScope{TemplateKey: LogExportTemplateCustomerInvoice, CustomerIDs: []int{customer.Id}},
	}
	require.NoError(t, runExportJob(t, job))
	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Equal(t, []string{"User", "Cost (USD)"}, rows[0])
	require.Equal(t, customer.Username, rows[1][0])
}

func TestEmployeeExportOptionsStayScoped(t *testing.T) {
	_, customer, _ := employeeExportFixture(t)
	base := time.Now().Unix() - 800
	mkExportLog(t, func(l *Log) { l.UserId = customer.Id; l.CreatedAt = base; l.ModelName = "allowed" })
	mkExportLog(t, func(l *Log) { l.UserId = customer.Id + 1; l.CreatedAt = base; l.ModelName = "hidden" })
	filter := LogExportFilter{StartTimestamp: base, EndTimestamp: base}
	items, next, err := LogExportFilterOptions(context.Background(), filter, "model_name", "", "", []int{customer.Id})
	require.NoError(t, err)
	require.Empty(t, next)
	require.Equal(t, []LogExportFilterOption{{Value: "allowed", Label: "allowed"}}, items)
	items, _, err = LogExportFilterOptions(context.Background(), filter, "model_name", "", "", []int{})
	require.NoError(t, err)
	require.Empty(t, items)
	_, _, err = LogExportFilterOptions(context.Background(), filter, "other", "", "", nil)
	require.Error(t, err)
	_, _, err = LogExportFilterOptions(context.Background(), filter, "model_name", "", "invalid", nil)
	require.Error(t, err)
}

func TestEmployeeExportWorkerStopsAtRevokedBatchBoundary(t *testing.T) {
	employee, customer, tpl := employeeExportFixture(t)
	enableRedis(t)
	operation_setting.GetLogExportSetting().BatchSize = 1
	base := time.Now().Unix() - 900
	for i := 0; i < 2; i++ {
		mkExportLog(t, func(l *Log) { l.UserId = customer.Id; l.CreatedAt = base })
	}
	scan := func(interval time.Duration) (int, *LogExportJob, error) {
		previous := employeeExportRevalidateInterval
		employeeExportRevalidateInterval = interval
		defer func() { employeeExportRevalidateInterval = previous }()
		operation_setting.GetLogExportSetting().EmployeeExportEnabled = true
		job := &LogExportJob{JobID: NewLogExportJobID(), UserID: employee.Id,
			Filters:       LogExportFilter{StartTimestamp: base, EndTimestamp: base},
			EmployeeScope: &EmployeeExportScope{TemplateKey: tpl.Key, CustomerIDs: []int{customer.Id}},
		}
		scanner := newLogExportScanner(job, []string{"id", "created_at"}, false, &rowCtx{})
		rows := 0
		err := scanner.run(context.Background(), func(logs []*Log) error {
			rows += len(logs)
			if len(logs) > 0 {
				operation_setting.GetLogExportSetting().EmployeeExportEnabled = false
			}
			return nil
		})
		return rows, job, err
	}
	rows, job, err := scan(0)
	require.ErrorIs(t, err, ErrEmployeeExportAccess)
	require.Equal(t, 1, rows, "revocation stops the scan at the next batch")
	require.Zero(t, job.Filters.UserId, "scanning must restore the original filter")
	// Within the revalidation interval the scan does not re-query access on every batch.
	rows, _, err = scan(time.Hour)
	require.NoError(t, err)
	require.Equal(t, 2, rows)
}

func TestEmployeeExportTemplateNamesAreUnique(t *testing.T) {
	employee, _, tpl := employeeExportFixture(t)
	ctx := context.Background()
	for _, name := range []string{"customer invoice", "CUSTOMER INVOICE", tpl.Name} {
		dup := &EmployeeExportTemplate{Name: name, Enabled: true, Columns: []string{"created_at"}, Format: "csv_gz", Mode: "detail"}
		require.ErrorIs(t, SaveEmployeeExportTemplate(ctx, dup, employee.Id), ErrEmployeeExportNameTaken, name)
	}
	var loaded EmployeeExportTemplate
	require.NoError(t, DB.First(&loaded, tpl.Id).Error)
	loaded.Enabled = false
	require.NoError(t, SaveEmployeeExportTemplate(ctx, &loaded, employee.Id), "keeping its own name is not a conflict")
}

func TestEmployeeExportColumnCatalogAndEmptyArrays(t *testing.T) {
	_, _, tpl := employeeExportFixture(t)
	tpl.Columns = []string{"username", "cost_usd"}
	tpl.SummaryDims = nil
	tpl.AllowedFilters = nil
	require.NoError(t, SaveEmployeeExportTemplate(context.Background(), tpl, 1))
	var loaded EmployeeExportTemplate
	require.NoError(t, DB.First(&loaded, tpl.Id).Error)
	require.NotNil(t, loaded.SummaryDims)
	require.NotNil(t, loaded.AllowedFilters)
	require.Equal(t, []string{"username", "cost_usd"}, loaded.Columns)
	catalog := map[string]LogExportColumn{}
	for _, column := range EmployeeExportColumns() {
		require.False(t, column.AdminOnly, column.Key)
		catalog[column.Key] = column
	}
	require.Equal(t, LogExportAudienceCustomer, catalog["username"].Audience, "the customer name identifies rows")
	require.Equal(t, LogExportAudienceInternal, catalog["quota"].Audience, "internal columns stay badged")
	require.Equal(t, LogExportAudienceInternal, catalog["ip"].Audience)
	require.Equal(t, LogExportAudienceCustomer, catalog["cost_usd"].Audience)
	for _, key := range []string{"channel_id", "channel_name", "retry_chain"} {
		require.NotContains(t, catalog, key, "admin-only columns are never offered to employees")
	}
	adminOnly := 0
	for _, column := range LogExportColumns() {
		if column.AdminOnly {
			adminOnly++
		}
	}
	require.Len(t, catalog, len(LogExportColumns())-adminOnly)
	require.Equal(t, LogExportAudienceInternal, logExportColumnMap["username"].Audience, "the employee catalog must not change the global customer invoice audience")
}
