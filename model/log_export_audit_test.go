package model

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestAuditExportAccessIsolation(t *testing.T) {
	job := &LogExportJob{DataSource: "audit", UserID: 42, CreatorRole: common.RoleAdminUser, AuditFilters: &AuditExportFilter{}}
	for _, scope := range []string{"admin", "self", "employee", "usage_admin", "usage_self"} {
		access := LogExportAccess{UserID: 42, Role: common.RoleRootUser, Scope: scope, Customers: map[int]bool{42: true}, AuditRead: true, AuditSecurityExport: true}
		view, operate := access.Permissions(job)
		require.False(t, view, scope)
		require.False(t, operate, scope)
	}
	access := LogExportAccess{UserID: 7, Role: common.RoleAdminUser, Scope: "audit_admin", AuditRead: true}
	view, _ := access.Permissions(job)
	require.False(t, view, "read access does not grant bulk export")
	access.AuditExport = true
	view, _ = access.Permissions(job)
	require.True(t, view)
	job.AuditFilters.IPRisk = "high"
	view, _ = access.Permissions(job)
	require.False(t, view)
	access.AuditSecurityExport = true
	view, _ = access.Permissions(job)
	require.True(t, view)
	job.CreatorRole = common.RoleRootUser
	view, _ = access.Permissions(job)
	require.False(t, view)
	access.Role = common.RoleRootUser
	view, _ = access.Permissions(job)
	require.True(t, view)
	job.SelfExport = true
	job.Filters.UserId = 42
	job.AuditFilters = &AuditExportFilter{AuditLogFilter: AuditLogFilter{SelfView: true, UserId: 42}}
	view, _ = access.Permissions(job)
	require.False(t, view)
	access.Scope, access.UserID = "audit_self", 42
	view, _ = access.Permissions(job)
	require.True(t, view)
	job.AuditFilters.UserId = 43
	view, _ = access.Permissions(job)
	require.False(t, view)
}

func TestAuditExportRoleDowngrade(t *testing.T) {
	for _, roles := range [][2]int{{100, 10}, {100, 1}, {10, 1}} {
		job := &LogExportJob{DataSource: LogExportDataAudit, UserID: 42, CreatorRole: roles[0], SelfExport: true,
			AuditProjectionVersion: AuditActivityProjectionVersion, Status: LogExportStatusReady, Parts: []LogExportPart{{Index: 1}},
			Filters: LogExportFilter{UserId: 42}, AuditFilters: &AuditExportFilter{AuditLogFilter: AuditLogFilter{SelfView: true, UserId: 42}}}
		access := LogExportAccess{UserID: 42, Role: roles[0], Scope: "audit_self"}
		require.True(t, access.PublicJob(job).CanDownload)
		access.Role = roles[1]
		require.False(t, access.CanReadData(job))
		public := access.PublicJob(job)
		require.NotNil(t, public, "a demoted user can still manage their task")
		require.False(t, public.CanDownload)
		require.True(t, public.CanDelete)
		job.Status = LogExportStatusRunning
		require.True(t, access.PublicJob(job).CanCancel)
		job.CreatorRole = roles[1]
		require.True(t, access.CanReadData(job), "a new export uses current role")
	}
}

type sparseAuditReader struct {
	calls   int
	windows [][2]int64
}

func (r *sparseAuditReader) Header() []string                     { return []string{"event"} }
func (r *sparseAuditReader) BatchSize(n int) int                  { return min(n, 100) }
func (r *sparseAuditReader) Validate(context.Context, bool) error { return nil }
func (r *sparseAuditReader) Reverse() bool                        { return true }
func (r *sparseAuditReader) Read(_ context.Context, start, end int64, cursor any, limit int) (LogExportBatch, error) {
	r.calls++
	r.windows = append(r.windows, [2]int64{start, end})
	if r.calls == 1 {
		return LogExportBatch{Scanned: 100, Cursor: "continue", Position: end}, nil
	}
	if r.calls == 2 {
		if cursor != "continue" {
			return LogExportBatch{}, ErrLogExportDataSource
		}
		return LogExportBatch{Scanned: 1, Done: true, Position: start, Rows: []LogExportRow{{At: start, Values: []string{"found"}}}}, nil
	}
	return LogExportBatch{Done: true}, nil
}

func TestAuditExportSparseDescendingWindows(t *testing.T) {
	server := miniredis.RunT(t)
	old, enabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = redis.NewClient(&redis.Options{Addr: server.Addr()}), true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = old, enabled })
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.WindowSec = 60
		s.BatchSize = 100
		s.MaxRowsPerSec = 100
	})
	job := &LogExportJob{JobID: NewLogExportJobID(), Filters: LogExportFilter{StartTimestamp: 1, EndTimestamp: 121}}
	reader := &sparseAuditReader{}
	scanner := newLogExportScanner(job, nil, false, nil)
	scanner.reader = reader
	scanner.gate.cpuFn = func() (float64, bool) { return 1, true }
	var charged time.Duration
	scanner.gate.sleepFn = func(_ context.Context, d time.Duration) { charged += d }
	var rows int
	lastProgress := 0
	require.NoError(t, scanner.runBatches(context.Background(), func(batch LogExportBatch) error {
		require.GreaterOrEqual(t, job.Progress, lastProgress)
		lastProgress = job.Progress
		rows += len(batch.Rows)
		return nil
	}))
	require.Equal(t, 1, rows)
	require.EqualValues(t, 101, job.ScannedRows)
	require.Equal(t, [][2]int64{{62, 121}, {62, 121}, {2, 61}, {1, 1}}, reader.windows)
	require.GreaterOrEqual(t, charged, time.Second, "empty output still charges candidate scan rate")
	require.Equal(t, 99, job.Progress)
}

func TestExportUnknownSourceFailsBeforeUsageProcessing(t *testing.T) {
	job := &LogExportJob{DataSource: "unrecognized"}
	require.ErrorIs(t, writeLogExport(context.Background(), job), ErrLogExportDataSource)
}

func TestAuditExportLegacyGrantCannotAccessAudit(t *testing.T) {
	job := &LogExportJob{DataSource: "audit"}
	for _, grant := range []LogExportDownloadGrant{
		{Version: 0}, {Version: 2, Scope: "admin"},
		{Version: 3, DataSource: "usage", Scope: "audit_admin"},
	} {
		require.False(t, grant.MatchesSource(job))
	}
	require.True(t, (&LogExportDownloadGrant{Version: 3, DataSource: "audit", Scope: "audit_admin"}).MatchesSource(job))
}

func TestAuditExportMixedListAndCursorIsolation(t *testing.T) {
	server := miniredis.RunT(t)
	previous, enabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = redis.NewClient(&redis.Options{Addr: server.Addr()}), true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = previous, enabled })
	jobs := []*LogExportJob{
		{}, // Legacy usage metadata.
		{DataSource: "audit", AuditFilters: &AuditExportFilter{}},
		{DataSource: "audit", AuditFilters: &AuditExportFilter{}},
		{DataSource: "audit", AuditFilters: &AuditExportFilter{IPRisk: "high"}},
		{DataSource: "audit", CreatorRole: common.RoleRootUser, AuditFilters: &AuditExportFilter{}},
		{DataSource: "audit", SelfExport: true, Filters: LogExportFilter{UserId: 7}, AuditFilters: &AuditExportFilter{AuditLogFilter: AuditLogFilter{SelfView: true, UserId: 7}}},
	}
	for i, job := range jobs {
		job.JobID, job.UserID, job.Status = NewLogExportJobID(), 7, LogExportStatusPending
		job.CreatedAtMs = int64(100000 + i)
		if job.CreatorRole == 0 {
			job.CreatorRole = common.RoleAdminUser
		}
		require.NoError(t, saveLogExportJob(job))
	}
	require.Equal(t, len(jobs), CountActiveLogExportJobs(7), "both sources consume the same active-task quota")
	access := LogExportAccess{UserID: 7, Role: common.RoleAdminUser, Scope: "audit_admin", AuditRead: true, AuditExport: true}
	page, err := ListManagedLogExportJobs(context.Background(), access, LogExportListQuery{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Jobs, 1)
	require.Equal(t, jobs[2].JobID, page.Jobs[0].JobID)
	require.NotEmpty(t, page.NextCursor)
	for _, scope := range []string{"usage_admin", "audit_self"} {
		access.Scope = scope
		_, err = ListManagedLogExportJobs(context.Background(), access, LogExportListQuery{Limit: 1, Cursor: page.NextCursor})
		require.ErrorIs(t, err, ErrLogExportListQuery)
	}
	access.Scope = "usage_admin"
	page, err = ListManagedLogExportJobs(context.Background(), access, LogExportListQuery{})
	require.NoError(t, err)
	require.Len(t, page.Jobs, 1)
	require.Equal(t, jobs[0].JobID, page.Jobs[0].JobID)
}

func TestAuditExportOutputRemovesIncompleteParts(t *testing.T) {
	for _, mode := range []string{"cancel", "panic", "part_limit"} {
		t.Run(mode, func(t *testing.T) {
			for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
				t.Setenv(key, t.TempDir())
			}
			fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
				s.RowsPerFile, s.MaxParts, s.MinFreeDiskMB = 1, 2, 1
			})
			job := &LogExportJob{JobID: NewLogExportJobID(), Format: LogExportFormatCSVGz}
			output, err := newLogExportOutput(job, []string{"event"})
			require.NoError(t, err)
			defer output.Abort()
			require.NoError(t, output.Write([]string{"newer"}, 20))
			require.NoError(t, output.Write([]string{"older"}, 10))
			require.Len(t, job.Parts, 1)
			unfinished := output.part.Path
			finished := job.Parts[0].Path
			switch mode {
			case "panic":
				require.Panics(t, func() { defer output.Abort(); panic("renderer failed") })
			case "part_limit":
				require.ErrorIs(t, output.Write([]string{"overflow"}, 5), ErrLogExportTooManyParts)
				output.Abort()
			default:
				output.Abort()
			}
			_, err = os.Stat(unfinished)
			require.ErrorIs(t, err, os.ErrNotExist)
			_, err = os.Stat(finished)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}
