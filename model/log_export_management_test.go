package model

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestExportManagementPermissions(t *testing.T) {
	setting := operation_setting.GetLogExportSetting()
	previous := setting.EmployeeCustomerManageEnabled
	t.Cleanup(func() { setting.EmployeeCustomerManageEnabled = previous })
	customer := &LogExportJob{UserID: 42, SelfExport: true, Filters: LogExportFilter{UserId: 42}, Status: LogExportStatusReady}
	privileged := &LogExportJob{UserID: 42, Status: LogExportStatusReady, CreatorRole: common.RoleRootUser}
	for _, manage := range []bool{false, true} {
		setting.EmployeeCustomerManageEnabled = manage
		access := LogExportAccess{UserID: 7, Role: common.RoleCommonUser, Scope: "employee", Customers: map[int]bool{42: true}}
		view, operate := access.Permissions(customer)
		require.True(t, view)
		require.Equal(t, manage, operate)
		view, operate = access.Permissions(privileged)
		require.False(t, view)
		require.False(t, operate)
		delete(access.Customers, 42)
		view, operate = access.Permissions(customer)
		require.False(t, view)
		require.False(t, operate)
	}
	admin := LogExportAccess{UserID: 7, Role: common.RoleAdminUser, Scope: "admin"}
	view, operate := admin.Permissions(privileged)
	require.True(t, view)
	require.True(t, operate)
	admin.Scope = "self"
	view, operate = admin.Permissions(customer)
	require.False(t, view)
	require.False(t, operate)
}

func TestExportListReportsActualDownloadExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	previous, enabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = previous, enabled })
	ctx := context.Background()
	job := &LogExportJob{JobID: NewLogExportJobID(), UserID: 42, Status: LogExportStatusReady}
	require.NoError(t, saveLogExportJob(job))
	// A legacy task has no stored expiry; use its actual remaining Redis lifetime.
	require.NoError(t, common.RDB.Expire(ctx, logExportJobKey(job.JobID), 2*time.Hour).Err())
	server.FastForward(30 * time.Minute)
	access := LogExportAccess{UserID: 7, Role: common.RoleAdminUser, Scope: "admin"}
	page, err := ListManagedLogExportJobs(ctx, access, LogExportListQuery{})
	require.NoError(t, err)
	require.Len(t, page.Jobs, 1)
	require.InDelta(t, time.Now().Add(90*time.Minute).Unix(), page.Jobs[0].ExpiresAt, 2)
	setting := operation_setting.GetLogExportSetting()
	previousTTL := setting.JobTTLHours
	t.Cleanup(func() { setting.JobTTLHours = previousTTL })
	setting.JobTTLHours = 48
	page, err = ListManagedLogExportJobs(ctx, access, LogExportListQuery{})
	require.NoError(t, err)
	require.InDelta(t, time.Now().Add(90*time.Minute).Unix(), page.Jobs[0].ExpiresAt, 2)
	require.NoError(t, common.RDB.Persist(ctx, logExportJobKey(job.JobID)).Err())
	page, err = ListManagedLogExportJobs(ctx, access, LogExportListQuery{})
	require.NoError(t, err)
	require.Zero(t, page.Jobs[0].ExpiresAt)
	require.NoError(t, common.RDB.Expire(ctx, logExportJobKey(job.JobID), time.Second).Err())
	server.FastForward(2 * time.Second)
	page, err = ListManagedLogExportJobs(ctx, access, LogExportListQuery{})
	require.NoError(t, err)
	require.Empty(t, page.Jobs)
}

func TestExportManagementFiltersAndPagination(t *testing.T) {
	server := miniredis.RunT(t)
	previous, enabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = previous, enabled })
	access := LogExportAccess{UserID: 7, Role: common.RoleAdminUser, Scope: "admin"}
	// Identical scores must not lose tasks, even if the preceding anchor is deleted.
	for i := range 205 {
		job := &LogExportJob{JobID: NewLogExportJobID(), UserID: 42, Username: "Customer Alpha", SelfExport: true,
			Filters: LogExportFilter{UserId: 42}, Status: LogExportStatusReady, CreatedAt: 100, CreatedAtMs: 100999}
		if i == 0 {
			job.Status = LogExportStatusFailed
		}
		require.NoError(t, saveLogExportJob(job))
	}
	query := LogExportListQuery{Limit: 20, CreatedAfter: 100, CreatedBefore: 100, CreatorKeyword: "alpha", Source: "self", Mode: "detail", CustomerID: 42}
	seen := map[string]bool{}
	for {
		page, err := ListManagedLogExportJobs(context.Background(), access, query)
		require.NoError(t, err)
		for _, job := range page.Jobs {
			require.False(t, seen[job.JobID])
			seen[job.JobID] = true
		}
		if page.NextCursor == "" {
			break
		}
		require.NotEqual(t, query.Cursor, page.NextCursor)
		cursorBytes, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
		require.NoError(t, err)
		if len(page.Jobs) > 0 {
			require.NotContains(t, string(cursorBytes), page.Jobs[len(page.Jobs)-1].JobID)
		}
		query.Cursor = page.NextCursor
		if len(page.Jobs) > 0 {
			DeleteLogExportJob(page.Jobs[len(page.Jobs)-1].LogExportJob)
		}
	}
	require.Len(t, seen, 205)
	query.Cursor = "invalid"
	_, err := ListManagedLogExportJobs(context.Background(), access, query)
	require.ErrorIs(t, err, ErrLogExportListQuery)
	query.Cursor = ""
	query.Status = LogExportStatusPending
	page, err := ListManagedLogExportJobs(context.Background(), access, query)
	require.NoError(t, err)
	require.Empty(t, page.Jobs)
	server.FastForward(31 * 24 * time.Hour)
	page, err = ListManagedLogExportJobs(context.Background(), access, query)
	require.NoError(t, err)
	require.Empty(t, page.Jobs)
	require.Empty(t, page.NextCursor)
}

func TestExportManagementDownloadGrantExpiryAndLegacy(t *testing.T) {
	server := miniredis.RunT(t)
	previous, enabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = previous, enabled })
	for _, legacy := range []bool{false, true} {
		jobID := NewLogExportJobID()
		token, err := CreateManagedLogExportDownloadToken(jobID, 42, 1, "employee")
		if legacy {
			token, err = CreateLogExportDownloadToken(jobID, 42, 1)
		}
		require.NoError(t, err)
		grant, err := ConsumeManagedLogExportDownloadToken(token)
		require.NoError(t, err)
		require.NotNil(t, grant)
		require.Equal(t, jobID, grant.JobID)
		require.Equal(t, 42, grant.UserID)
		require.Equal(t, 1, grant.Part)
		if legacy {
			require.Equal(t, 0, grant.Version)
		} else {
			require.Equal(t, "employee", grant.Scope)
		}
		server.FastForward(operation_setting.GetLogExportSetting().GetDownloadSessionTTL() / 2)
		reused, err := ConsumeManagedLogExportDownloadToken(token)
		require.NoError(t, err)
		require.Equal(t, grant, reused)
		server.FastForward(operation_setting.GetLogExportSetting().GetDownloadSessionTTL()/2 + time.Second)
		expired, err := ConsumeManagedLogExportDownloadToken(token)
		require.NoError(t, err)
		require.Nil(t, expired)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CancelOrDeleteManagedLogExportJob(ctx, &LogExportJob{JobID: NewLogExportJobID(), Status: LogExportStatusRunning})
	require.Error(t, err, "a failed cancellation must not report success")
}

func TestExportManagementSparsePages(t *testing.T) {
	enableRedis(t)
	owner := nextTestID()
	access := LogExportAccess{UserID: owner, Role: common.RoleAdminUser, Scope: "admin"}
	for i := range 205 {
		job := &LogExportJob{JobID: NewLogExportJobID(), UserID: owner, CreatedAt: int64(1000 + i), Status: LogExportStatusRunning}
		if i == 0 {
			job.Status = LogExportStatusReady
		}
		require.NoError(t, saveLogExportJob(job))
		t.Cleanup(func() { DeleteLogExportJob(job) })
	}
	query := LogExportListQuery{CreatorID: owner, Status: LogExportStatusReady}
	page, err := ListManagedLogExportJobs(context.Background(), access, query)
	require.NoError(t, err)
	require.Empty(t, page.Jobs)
	require.True(t, page.HasMore)
	query.Cursor = page.NextCursor
	query.Status = LogExportStatusFailed
	_, err = ListManagedLogExportJobs(context.Background(), access, query)
	require.ErrorIs(t, err, ErrLogExportListQuery)
	query.Status = LogExportStatusReady
	page, err = ListManagedLogExportJobs(context.Background(), access, query)
	require.NoError(t, err)
	require.Len(t, page.Jobs, 1)
	require.Equal(t, int64(1000), page.Jobs[0].CreatedAt)
	require.False(t, page.HasMore)
}

func TestExportManagementQueryValidation(t *testing.T) {
	for _, query := range []LogExportListQuery{
		{Limit: -1}, {Limit: 101}, {CreatorID: -1}, {CustomerID: -1},
		{CreatorKeyword: strings.Repeat("x", 65)}, {Scope: "global"}, {Source: "other"}, {Mode: "all"}, {Status: "expired"},
		{CreatedAfter: -1}, {CreatedBefore: -1}, {CreatedAfter: 2, CreatedBefore: 1}, {CreatedBefore: 253402300800}, {JobID: "invalid"},
	} {
		require.ErrorIs(t, query.Validate(), ErrLogExportListQuery, "%+v", query)
	}
	for _, query := range []LogExportListQuery{{}, {Limit: 1}, {Limit: 100}, {CreatedAfter: 1, CreatedBefore: 1}, {CreatorKeyword: strings.Repeat("字", 64)}} {
		require.NoError(t, query.Validate())
	}
	job := &LogExportJob{JobID: NewLogExportJobID(), UserID: 42, Username: "Alice", Status: LogExportStatusReady, CreatedAt: 100,
		EmployeeScope: &EmployeeExportScope{CustomerIDs: []int{7, 8}}}
	for _, query := range []LogExportListQuery{{CreatorID: 43}, {CreatorKeyword: "bob"}, {CustomerID: 9}, {Status: "failed"}, {CreatedAfter: 101}, {CreatedBefore: 99}, {Source: "admin"}, {Mode: "summary"}, {JobID: NewLogExportJobID()}} {
		require.False(t, query.Matches(job), "%+v", query)
	}
	query := LogExportListQuery{CreatorID: 42, CreatorKeyword: "ali", CustomerID: 8, Status: "ready", CreatedAfter: 100, CreatedBefore: 100, Source: "employee", Mode: "detail", JobID: job.JobID}
	require.True(t, query.Matches(job))
}
