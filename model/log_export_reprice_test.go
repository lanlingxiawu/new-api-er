package model

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestLogExportGroupRatioOverrideValidation(t *testing.T) {
	for _, value := range []string{"0", "0.000001", "1.5", "10000", "10000.000000"} {
		require.NoError(t, ValidateLogExportGroupRatioOverride(value), value)
	}
	for _, value := range []string{"", "-1", "NaN", "Infinity", "1e3", "10000.000001", "0.0000001", "1.2.3", strings.Repeat("0", 100)} {
		require.Error(t, ValidateLogExportGroupRatioOverride(value), value)
	}
}

func TestLogExportRepriceRows(t *testing.T) {
	for _, tc := range []struct {
		name, ratio, other string
		quota, want        int
		logType            int
		changed, invalid   bool
	}{
		{name: "replace", ratio: "1.5", other: `{"group_ratio":2,"user_group_ratio":2}`, quota: 1000, want: 750, changed: true},
		{name: "round once", ratio: "1", other: `{"group_ratio":6}`, quota: 3, want: 1, changed: true},
		{name: "zero new", ratio: "0", other: `{"group_ratio":2}`, quota: 1000, changed: true},
		{name: "zero old", ratio: "1.5", other: `{"group_ratio":0}`, quota: 1000, want: 1000},
		{name: "missing old", ratio: "1.5", other: `{}`, quota: 1000, want: 1000},
		{name: "null old", ratio: "1.5", other: `{"group_ratio":null}`, quota: 1000, want: 1000},
		{name: "zero both", ratio: "0", other: `{"group_ratio":0}`, quota: 1000, want: 1000},
		{name: "same", ratio: "2", other: `{"group_ratio":2}`, quota: 3, want: 3, changed: true},
		{name: "zero quota", ratio: "1.5", other: `{"group_ratio":2}`, changed: true},
		{name: "non consume", ratio: "1.5", other: `{"group_ratio":2}`, quota: 1000, want: 1000, logType: LogTypeRefund},
		{name: "negative old", ratio: "1.5", other: `{"group_ratio":-1}`, quota: 1000, invalid: true},
		{name: "invalid json", ratio: "1.5", other: `{"group_ratio":2`, quota: 1000, invalid: true},
		{name: "invalid type", ratio: "1.5", other: `{"group_ratio":"bad"}`, quota: 1000, invalid: true},
		{name: "negative quota", ratio: "1.5", other: `{"group_ratio":2}`, quota: -1, invalid: true},
		{name: "overflow", ratio: "10000", other: `{"group_ratio":0.000001}`, quota: common.MaxQuota, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := &LogExportJob{CreatorRole: common.RoleAdminUser, GroupRatioOverride: &tc.ratio}
			calculator, err := newLogExportRepricer(job)
			require.NoError(t, err)
			ctx := &rowCtx{repricer: calculator}
			logType := tc.logType
			if logType == 0 {
				logType = LogTypeConsume
			}
			l := &Log{Type: logType, Quota: tc.quota, Other: tc.other}
			err = ctx.prepareReprice(l)
			if tc.invalid {
				require.ErrorIs(t, err, ErrLogExportReprice)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, ctx.exportQuota(l))
			require.Equal(t, tc.changed, ctx.repriced)
			require.Equal(t, tc.quota, l.Quota)
			require.Equal(t, tc.other, l.Other)
		})
	}
}

func TestLogExportRepriceColumnsAndRowReset(t *testing.T) {
	ratio := "1.5"
	calculator, err := newLogExportRepricer(&LogExportJob{CreatorRole: common.RoleAdminUser, GroupRatioOverride: &ratio})
	require.NoError(t, err)
	ctx := &rowCtx{repricer: calculator}
	set, err := ResolveLogExportColumnsForRole([]string{"quota", "cost_usd", "group_ratio", "user_group_ratio"}, common.RoleAdminUser)
	require.NoError(t, err)
	row := &Log{Type: LogTypeConsume, Quota: 1000, Other: `{"group_ratio":2,"user_group_ratio":2}`}
	require.NoError(t, ctx.prepareReprice(row))
	require.Equal(t, []string{"750", "0.0015", "1.5", "1.5"}, set.Render(row, ctx, nil))
	row = &Log{Type: LogTypeConsume, Quota: 1000, Other: `{"group_ratio":0}`}
	require.NoError(t, ctx.prepareReprice(row))
	require.Equal(t, []string{"1000", "0.002", "0", ""}, set.Render(row, ctx, nil))
	row = &Log{Type: LogTypeConsume, Quota: 1000, Other: `{"group_ratio":2,"user_group_ratio":-1}`}
	require.NoError(t, ctx.prepareReprice(row))
	require.Equal(t, []string{"750", "0.0015", "1.5", ""}, set.Render(row, ctx, nil))
}

func TestLogExportRepriceWorkerAuthorization(t *testing.T) {
	ratio := "1"
	for _, job := range []*LogExportJob{
		{GroupRatioOverride: &ratio},
		{GroupRatioOverride: &ratio, CreatorRole: common.RoleCommonUser},
		{GroupRatioOverride: &ratio, CreatorRole: common.RoleAdminUser, SelfExport: true},
		{GroupRatioOverride: &ratio, CreatorRole: common.RoleRootUser, EmployeeScope: &EmployeeExportScope{}},
	} {
		_, err := newLogExportRepricer(job)
		require.Error(t, err)
	}
	calculator, err := newLogExportRepricer(&LogExportJob{})
	require.NoError(t, err)
	require.Nil(t, calculator)
}

func TestLogExportRepriceSnapshotAndDisabledPath(t *testing.T) {
	ratio := "1.5"
	job := &LogExportJob{CreatorRole: common.RoleAdminUser, GroupRatioOverride: &ratio, RepriceQuotaPerUnit: 1000}
	calculator, err := newLogExportRepricer(job)
	require.NoError(t, err)
	ctx := &rowCtx{repricer: calculator}
	set, err := ResolveLogExportColumnsForRole([]string{"quota", "cost_usd", "group_ratio"}, common.RoleAdminUser)
	require.NoError(t, err)
	for _, tc := range []struct{ other, amount, cost string }{
		{`{"group_ratio":2}`, "750", "0.75"},
		{`{"group_ratio":0}`, "1000", "1"},
		{`{}`, "1000", "1"},
	} {
		l := &Log{Type: LogTypeConsume, Quota: 1000, Other: tc.other}
		require.NoError(t, ctx.prepareReprice(l))
		row := set.Render(l, ctx, nil)
		require.Equal(t, tc.amount, row[0])
		require.Equal(t, tc.cost, row[1])
	}
	disabled := &rowCtx{}
	l := &Log{Type: LogTypeConsume, Quota: 1000, Other: `not json`}
	require.NoError(t, disabled.prepareReprice(l))
	require.Equal(t, 1000, disabled.exportQuota(l))
	fields := []string{"id", "created_at"}
	var off *logExportRepricer
	require.Equal(t, fields, off.selectFields(fields))
	for _, required := range []string{"type", "quota", "other"} {
		require.Contains(t, calculator.selectFields(fields), required)
	}
	job.RepriceQuotaPerUnit = -1
	_, err = newLogExportRepricer(job)
	require.ErrorIs(t, err, ErrLogExportReprice)
	badRatio := "-1"
	job.GroupRatioOverride = &badRatio
	_, err = newLogExportRepricer(job)
	require.ErrorIs(t, err, ErrLogExportInvalidGroupRatio)
}

func TestLogExportRepriceJobRoundTrip(t *testing.T) {
	useUserCacheMiniRedis(t)
	ratio := "0"
	job := mkExportJob(t, nextTestID(), func(job *LogExportJob) {
		job.CreatorRole = common.RoleAdminUser
		job.GroupRatioOverride = &ratio
		job.RepriceQuotaPerUnit = 1000
	})
	loaded, err := GetLogExportJob(job.JobID)
	require.NoError(t, err)
	require.NotNil(t, loaded.GroupRatioOverride)
	require.Equal(t, "0", *loaded.GroupRatioOverride)
	require.EqualValues(t, 1000, loaded.RepriceQuotaPerUnit)
	calculator, err := newLogExportRepricer(loaded)
	require.NoError(t, err)
	require.True(t, calculator.ratio.IsZero())
}

// Use a configured project log DB when available; otherwise a dedicated matrix
// database or a real SQLite engine. Only test-owned rows are removed.
func repriceTestLogDB(t *testing.T) {
	t.Helper()
	if LOG_DB != nil {
		return
	}
	var driver gorm.Dialector
	kind := common.DatabaseTypeSQLite
	dsn := os.Getenv("LOG_EXPORT_TEST_DSN")
	switch {
	case strings.HasPrefix(dsn, "postgres"):
		driver = postgres.Open(dsn)
		kind = common.DatabaseTypePostgreSQL
	case dsn != "":
		driver = mysql.Open(dsn)
		kind = common.DatabaseTypeMySQL
	default:
		driver = sqlite.Open(":memory:")
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	previousDB, previousKind := LOG_DB, common.LogDatabaseType()
	LOG_DB = db
	common.SetLogDatabaseType(kind)
	require.NoError(t, db.AutoMigrate(&Log{}))
	versionQuery := "SELECT version()"
	if kind == common.DatabaseTypeSQLite {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("export test database: %s %s", db.Dialector.Name(), version)
	t.Cleanup(func() { LOG_DB = previousDB; common.SetLogDatabaseType(previousKind); require.NoError(t, conn.Close()) })
}

func TestWriteLogExportGroupRatioOverride(t *testing.T) {
	repriceTestLogDB(t)
	useUserCacheMiniRedis(t)
	fastExportSettings(t, nil)
	username := uniq("reprice")
	base := time.Now().Unix() - 5000
	for i, other := range []string{`{"group_ratio":2}`, `{"group_ratio":4}`, `{"group_ratio":0}`, `{}`} {
		mkExportLog(t, func(l *Log) {
			l.Username = username
			l.CreatedAt = base + int64(i)
			l.Quota = 1000
			l.ModelName = "m"
			l.Other = other
		})
	}
	ratio := "1.5"
	for _, mode := range []string{LogExportModeDetail, LogExportModeSummary, LogExportModeBoth} {
		t.Run(mode, func(t *testing.T) {
			job := newExportJobFor(username, base-1, base+10, []string{"quota", "cost_usd", "group_ratio", "other_raw"})
			job.CreatorRole = common.RoleRootUser
			job.GroupRatioOverride = &ratio
			job.Mode = mode
			job.SummaryDims = []string{LogSummaryDimModel}
			require.NoError(t, runExportJob(t, job))
			if mode != LogExportModeSummary {
				rows, _ := readCSVGz(t, job.Parts[0].Path)
				require.Equal(t, []string{"Quota", "Cost (USD)", "Group Ratio"}, rows[0])
				require.Equal(t, []string{"750", "0.0015", "1.5"}, rows[1])
				require.Equal(t, []string{"375", "0.00075", "1.5"}, rows[2])
				require.Equal(t, []string{"1000", "0.002", "0"}, rows[3])
				require.Equal(t, []string{"1000", "0.002", ""}, rows[4])
			}
			if mode != LogExportModeDetail {
				rows, _ := readCSVGz(t, job.Parts[len(job.Parts)-1].Path)
				require.Equal(t, "3125", rows[1][5])
				require.Equal(t, "0.006250", rows[1][6])
			}
		})
	}
	// SQL filters continue to apply to the original amount, even though output
	// becomes cheaper. A narrow projection must still select calculation inputs.
	job := newExportJobFor(username, base-1, base+10, []string{"id"})
	job.CreatorRole = common.RoleAdminUser
	job.GroupRatioOverride = &ratio
	minQuota := 1000
	job.Filters.QuotaMin = &minQuota
	require.NoError(t, runExportJob(t, job))
	require.EqualValues(t, 4, job.RowCount)
	var original Log
	require.NoError(t, LOG_DB.Where("username = ?", username).Order("created_at").First(&original).Error)
	require.Equal(t, 1000, original.Quota)
	require.Equal(t, `{"group_ratio":2}`, original.Other)
}

func BenchmarkLogExportReprice(b *testing.B) {
	for _, tc := range []struct{ name, ratio, other string }{
		{"convert", "1.5", `{"group_ratio":2,"user_group_ratio":2}`},
		{"same_ratio", "2", `{"group_ratio":2,"user_group_ratio":2}`},
		{"missing_ratio", "1.5", `{}`},
		{"wide_other", "1.5", `{"group_ratio":2,"content":"` + strings.Repeat("x", 4096) + `"}`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			calculator, err := newLogExportRepricer(&LogExportJob{CreatorRole: common.RoleAdminUser, GroupRatioOverride: &tc.ratio})
			require.NoError(b, err)
			ctx := &rowCtx{repricer: calculator}
			l := &Log{Type: LogTypeConsume, Quota: 1000, Other: tc.other}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := ctx.prepareReprice(l); err != nil {
					require.NoError(b, err)
				}
			}
		})
	}
}
