package model

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func quotaExportJob(user *User, start, end int64, filter *QuotaExportFilter) *LogExportJob {
	if filter == nil {
		filter = &QuotaExportFilter{}
	}
	return &LogExportJob{
		DataSource:   LogExportDataQuota,
		JobID:        NewLogExportJobID(),
		UserID:       user.Id,
		Username:     user.Username,
		CreatorRole:  user.Role,
		Status:       LogExportStatusRunning,
		Format:       LogExportFormatCSVGz,
		Mode:         LogExportModeDetail,
		Columns:      QuotaExportColumns(user.Role),
		Lang:         "zh-CN",
		Options:      LogExportOptions{Header: true, CSVBOM: false, Timezone: "UTC"},
		Filters:      LogExportFilter{StartTimestamp: start, EndTimestamp: end},
		QuotaFilters: filter,
	}
}

func quotaColumn(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, column := range header {
		if column == name {
			return i
		}
	}
	t.Fatalf("column %s not in %v", name, header)
	return -1
}

func TestQuotaExportColumnsFollowRole(t *testing.T) {
	admin := QuotaExportColumns(common.RoleAdminUser)
	require.NotContains(t, admin, "token_id")
	require.NotContains(t, admin, "node_name")
	root := QuotaExportColumns(common.RoleRootUser)
	require.Equal(t, append(append([]string{}, admin...), "token_id", "node_name"), root)
}

func TestWriteQuotaExportRejectsForeignShape(t *testing.T) {
	columns := QuotaExportColumns(common.RoleAdminUser)
	base := &LogExportJob{
		DataSource:   LogExportDataQuota,
		CreatorRole:  common.RoleAdminUser,
		Columns:      columns,
		QuotaFilters: &QuotaExportFilter{},
		Filters:      LogExportFilter{StartTimestamp: 1, EndTimestamp: 2},
	}
	require.ErrorIs(t, writeQuotaDataExport(context.Background(), &LogExportJob{DataSource: LogExportDataQuota, Columns: columns}), ErrLogExportDataSource)

	self := *base
	self.SelfExport = true
	require.ErrorIs(t, writeQuotaDataExport(context.Background(), &self), ErrLogExportDataSource)

	summary := *base
	summary.Mode = LogExportModeSummary
	require.ErrorIs(t, writeQuotaDataExport(context.Background(), &summary), ErrLogExportDataSource)

	override := "1"
	repriced := *base
	repriced.GroupRatioOverride = &override
	require.ErrorIs(t, writeQuotaDataExport(context.Background(), &repriced), ErrLogExportDataSource)

	unknown := *base
	unknown.Columns = []string{"quota", "not_a_column"}
	require.ErrorIs(t, writeQuotaDataExport(context.Background(), &unknown), ErrLogExportDataSource)

	tokenID := 4
	filtered := *base
	filtered.QuotaFilters = &QuotaExportFilter{TokenID: &tokenID}
	require.ErrorIs(t, writeQuotaDataExport(context.Background(), &filtered), ErrLogExportAccessChanged)

	tampered := *base
	tampered.Columns = QuotaExportColumns(common.RoleRootUser)
	require.ErrorIs(t, writeQuotaDataExport(context.Background(), &tampered), ErrLogExportAccessChanged)
	require.Empty(t, tampered.Parts)
}

func TestQuotaExportZeroRateFailsBeforeOutput(t *testing.T) {
	previous := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	common.QuotaPerUnit = 0
	job := &LogExportJob{
		DataSource:   LogExportDataQuota,
		CreatorRole:  common.RoleAdminUser,
		Columns:      QuotaExportColumns(common.RoleAdminUser),
		QuotaFilters: &QuotaExportFilter{},
		Filters:      LogExportFilter{StartTimestamp: 1, EndTimestamp: 2},
		Options:      LogExportOptions{Header: true},
	}
	err := writeQuotaDataExport(context.Background(), job)
	require.ErrorIs(t, err, errQuotaExportRate)
	require.Empty(t, job.Parts)
	require.Equal(t, "internal", logExportErrorCode(err))
}

func TestQuotaExportUSDSnapshotAndChineseHeader(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)
	previous := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	common.QuotaPerUnit = 500000

	user := mkUser(t, func(u *User) { u.Role = common.RoleAdminUser })
	username := uniq("qusd")
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC).Unix()
	whole := mkQuotaData(t, func(q *QuotaData) {
		q.UserID = user.Id
		q.Username = username
		q.ModelName = "gpt-usd"
		q.CreatedAt = at
		q.UseGroup = "vip"
		q.ChannelID = 0
		q.Count = 2
		q.Quota = 500000
		q.TokenUsed = 9
		q.CacheReadTokens = 3
		q.CacheCreationTokens = 4
	})
	fraction := mkQuotaData(t, func(q *QuotaData) {
		q.UserID = user.Id
		q.Username = username
		q.ModelName = "gpt-usd"
		q.CreatedAt = at
		q.Quota = 1
		q.TokenUsed = 1
	})
	job := quotaExportJob(user, at, at, &QuotaExportFilter{Username: username})
	reader, err := newQuotaExportReader(job)
	require.NoError(t, err)
	common.QuotaPerUnit = 1000000
	batch, err := reader.Read(context.Background(), at, at, nil, 10)
	require.NoError(t, err)
	require.Len(t, batch.Rows, 2)
	quotaAt := quotaColumn(t, reader.Header(), "消耗额度（美元）")
	require.Equal(t, "1", batch.Rows[0].Values[quotaAt])
	require.Equal(t, "0.000002", batch.Rows[1].Values[quotaAt])
	common.QuotaPerUnit = 500000

	require.NoError(t, runExportJob(t, job))
	require.EqualValues(t, 2, job.RowCount)
	require.EqualValues(t, 2, job.ScannedRows)
	rows, bom := readCSVGz(t, job.Parts[0].Path)
	require.False(t, bom)
	require.Equal(t, reader.Header(), rows[0])
	require.NotContains(t, rows[0], "令牌 ID")
	require.NotContains(t, rows[0], "节点")
	require.Len(t, rows, 3)
	byID := map[string][]string{}
	idAt := quotaColumn(t, rows[0], "ID")
	for _, row := range rows[1:] {
		byID[row[idAt]] = row
	}
	require.Equal(t, []string{"1", "9"}, []string{byID[strconv.Itoa(whole.Id)][quotaAt], byID[strconv.Itoa(whole.Id)][quotaColumn(t, rows[0], "消耗 Token")]})
	require.Equal(t, "0.000002", byID[strconv.Itoa(fraction.Id)][quotaAt])
	require.Equal(t, "2026-01-02 03:00", byID[strconv.Itoa(whole.Id)][quotaColumn(t, rows[0], "小时")])
	require.Equal(t, "0", byID[strconv.Itoa(whole.Id)][quotaColumn(t, rows[0], "渠道 ID")])
}

func TestQuotaExportKeysetDoesNotSkipOrRepeat(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) {
		s.BatchSize = 1
		s.WindowSec = 60
	})

	user := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	username := uniq("qkey")
	base := usedataUniqTime()
	inserted := []*QuotaData{
		mkQuotaData(t, func(q *QuotaData) {
			q.Username = username
			q.CreatedAt = base + 1
			q.ChannelID = 7
			q.TokenID = 8
			q.NodeName = "node-a"
			q.Quota = 3
		}),
		mkQuotaData(t, func(q *QuotaData) {
			q.Username = username
			q.CreatedAt = base
			q.ChannelID = 0
			q.Quota = 1
		}),
		mkQuotaData(t, func(q *QuotaData) {
			q.Username = username
			q.CreatedAt = base
			q.ChannelID = 5
			q.Quota = 2
		}),
		mkQuotaData(t, func(q *QuotaData) {
			q.Username = username
			q.CreatedAt = base
			q.ChannelID = 9
			q.Quota = 4
		}),
	}
	sort.Slice(inserted, func(i, j int) bool {
		if inserted[i].CreatedAt == inserted[j].CreatedAt {
			return inserted[i].Id < inserted[j].Id
		}
		return inserted[i].CreatedAt < inserted[j].CreatedAt
	})

	job := quotaExportJob(user, base, base+1, &QuotaExportFilter{Username: username})
	reader, err := newQuotaExportReader(job)
	require.NoError(t, err)
	var cursor any
	var got []int
	for {
		batch, err := reader.Read(context.Background(), base, base+1, cursor, 1)
		require.NoError(t, err)
		require.LessOrEqual(t, len(batch.Rows), 1)
		for _, row := range batch.Rows {
			id, err := strconv.Atoi(row.Values[0])
			require.NoError(t, err)
			got = append(got, id)
		}
		if batch.Done {
			break
		}
		cursor = batch.Cursor
	}
	want := make([]int, len(inserted))
	for i, row := range inserted {
		want[i] = row.Id
	}
	require.Equal(t, want, got)

	zero := 0
	zeroJob := quotaExportJob(user, base, base+1, &QuotaExportFilter{Username: username, ChannelID: &zero})
	zeroReader, err := newQuotaExportReader(zeroJob)
	require.NoError(t, err)
	channelBatch, err := zeroReader.Read(context.Background(), base, base+1, nil, 10)
	require.NoError(t, err)
	require.Len(t, channelBatch.Rows, 1)
	require.Equal(t, "0", channelBatch.Rows[0].Values[quotaColumn(t, zeroReader.Header(), "渠道 ID")])

	left, err := reader.Read(context.Background(), base, base, nil, 10)
	require.NoError(t, err)
	right, err := reader.Read(context.Background(), base+1, base+1, nil, 10)
	require.NoError(t, err)
	require.Len(t, left.Rows, 3)
	require.Len(t, right.Rows, 1)
	for _, row := range left.Rows {
		require.Equal(t, strconv.FormatInt(base, 10), row.Values[quotaColumn(t, reader.Header(), "统计时间")])
	}
	require.Equal(t, strconv.FormatInt(base+1, 10), right.Rows[0].Values[quotaColumn(t, reader.Header(), "统计时间")])

	require.NoError(t, runExportJob(t, job))
	require.EqualValues(t, len(inserted), job.RowCount)
	require.EqualValues(t, len(inserted), job.ScannedRows)
	file, _ := readCSVGz(t, job.Parts[0].Path)
	require.Contains(t, file[0], "令牌 ID")
	require.Contains(t, file[0], "节点")
	var fileIDs []int
	for _, row := range file[1:] {
		id, err := strconv.Atoi(row[0])
		require.NoError(t, err)
		fileIDs = append(fileIDs, id)
	}
	require.Equal(t, want, fileIDs)
	tokenAt := quotaColumn(t, file[0], "令牌 ID")
	nodeAt := quotaColumn(t, file[0], "节点")
	require.Equal(t, "8", file[len(file)-1][tokenAt])
	require.Equal(t, "node-a", file[len(file)-1][nodeAt])
}

func TestQuotaExportWindowBoundaryIsClosed(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	fastExportSettings(t, func(s *operation_setting.LogExportSetting) { s.WindowSec = 60 })
	user := mkUser(t, func(u *User) { u.Role = common.RoleAdminUser })
	username := uniq("qwin")
	start := usedataUniqTime()
	var want []int
	for _, at := range []int64{start, start + 59, start + 60} {
		row := mkQuotaData(t, func(q *QuotaData) {
			q.Username = username
			q.CreatedAt = at
		})
		want = append(want, row.Id)
	}
	job := quotaExportJob(user, start, start+60, &QuotaExportFilter{Username: username})
	require.NoError(t, runExportJob(t, job))
	require.EqualValues(t, 3, job.RowCount)
	require.EqualValues(t, 3, job.ScannedRows)
	rows, _ := readCSVGz(t, job.Parts[0].Path)
	var got []int
	for _, row := range rows[1:] {
		id, err := strconv.Atoi(row[0])
		require.NoError(t, err)
		got = append(got, id)
	}
	require.Equal(t, want, got)
}

func TestQuotaExportEmptyRangeStillWritesChineseHeader(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)
	user := mkUser(t, func(u *User) { u.Role = common.RoleAdminUser })
	start := usedataUniqTime()
	job := quotaExportJob(user, start, start+5, &QuotaExportFilter{Username: uniq("qempty")})
	require.NoError(t, runExportJob(t, job))
	require.Zero(t, job.RowCount)
	require.Len(t, job.Parts, 1)
	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, 1)
	require.Contains(t, rows[0], "消耗额度（美元）")
}

func TestQuotaExportAccessRecheck(t *testing.T) {
	requireDB(t)
	enableRedis(t)
	fastExportSettings(t, nil)
	root := mkUser(t, func(u *User) { u.Role = common.RoleRootUser })
	job := quotaExportJob(root, 1, 2, &QuotaExportFilter{})
	reader, err := newQuotaExportReader(job)
	require.NoError(t, err)
	require.NoError(t, reader.Validate(context.Background(), true))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", root.Id).Update("role", common.RoleAdminUser).Error)
	err = reader.Validate(context.Background(), true)
	require.ErrorIs(t, err, ErrLogExportAccessChanged)
	require.Equal(t, "access_changed", logExportErrorCode(err))
	err = runExportJob(t, job)
	require.ErrorIs(t, err, ErrLogExportAccessChanged)
	require.Empty(t, job.Parts)

	admin := mkUser(t, func(u *User) { u.Role = common.RoleAdminUser })
	plain := quotaExportJob(admin, 1, 2, &QuotaExportFilter{})
	plainReader, err := newQuotaExportReader(plain)
	require.NoError(t, err)
	require.NoError(t, plainReader.Validate(context.Background(), true))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", admin.Id).Update("status", common.UserStatusDisabled).Error)
	require.ErrorIs(t, plainReader.Validate(context.Background(), true), ErrLogExportAccessChanged)
}

func TestQuotaExportListIsolationAndGrants(t *testing.T) {
	enableRedis(t)
	owner := nextTestID()
	usage := &LogExportJob{
		JobID:    NewLogExportJobID(),
		UserID:   owner,
		Username: "usage-owner",
		Filters:  LogExportFilter{StartTimestamp: 10, EndTimestamp: 20},
		Mode:     LogExportModeDetail,
	}
	channel := 0
	quota := &LogExportJob{
		DataSource:   LogExportDataQuota,
		JobID:        NewLogExportJobID(),
		UserID:       owner,
		Username:     "quota-owner",
		CreatorRole:  common.RoleAdminUser,
		Columns:      QuotaExportColumns(common.RoleAdminUser),
		QuotaFilters: &QuotaExportFilter{Username: "alice", ChannelID: &channel},
		Filters:      LogExportFilter{StartTimestamp: 10, EndTimestamp: 20},
		Mode:         LogExportModeDetail,
	}
	require.NoError(t, CreateLogExportJob(usage))
	require.NoError(t, CreateLogExportJob(quota))
	t.Cleanup(func() {
		DeleteLogExportJob(usage)
		DeleteLogExportJob(quota)
	})

	ctx := context.Background()
	query := LogExportListQuery{CreatorID: owner, Limit: 20}
	usagePage, err := ListManagedLogExportJobs(ctx, LogExportAccess{UserID: owner, Role: common.RoleAdminUser, Scope: "usage_admin"}, query)
	require.NoError(t, err)
	quotaPage, err := ListManagedLogExportJobs(ctx, LogExportAccess{UserID: owner, Role: common.RoleAdminUser, Scope: "quota_admin"}, query)
	require.NoError(t, err)
	require.Equal(t, []string{usage.JobID}, managedIDs(usagePage))
	require.Equal(t, []string{quota.JobID}, managedIDs(quotaPage))

	other := LogExportAccess{UserID: owner + 1, Role: common.RoleAdminUser, Scope: "quota_admin"}
	hidden := other.PublicJob(quota)
	require.NotNil(t, hidden)
	require.Nil(t, hidden.QuotaFilters)
	require.Equal(t, int64(10), hidden.Filters.StartTimestamp)
	require.Equal(t, "alice", quota.QuotaFilters.Username)
	visible := LogExportAccess{UserID: owner, Role: common.RoleAdminUser, Scope: "quota_admin"}.PublicJob(quota)
	require.Equal(t, "alice", visible.QuotaFilters.Username)
	require.Equal(t, 0, *visible.QuotaFilters.ChannelID)

	rootJob := *quota
	rootJob.CreatorRole = common.RoleRootUser
	rootJob.Columns = QuotaExportColumns(common.RoleRootUser)
	view, _ := other.Permissions(&rootJob)
	require.False(t, view)
	rootView, rootOperate := (LogExportAccess{UserID: owner, Role: common.RoleRootUser, Scope: "quota_admin"}).Permissions(&rootJob)
	require.True(t, rootView)
	require.True(t, rootOperate)
	_, selfOperate := (LogExportAccess{UserID: owner, Role: common.RoleAdminUser, Scope: "usage_self"}).Permissions(quota)
	require.False(t, selfOperate)

	usageJob := &LogExportJob{}
	quotaJob := &LogExportJob{DataSource: LogExportDataQuota}
	require.False(t, (&LogExportDownloadGrant{Version: 2, Scope: "usage_admin"}).MatchesSource(quotaJob))
	require.True(t, (&LogExportDownloadGrant{Version: 2, Scope: "usage_admin"}).MatchesSource(usageJob))
	require.False(t, (&LogExportDownloadGrant{Version: 3, DataSource: LogExportDataAudit, Scope: "audit_admin"}).MatchesSource(quotaJob))
	grant := &LogExportDownloadGrant{Version: 4, DataSource: LogExportDataQuota, Scope: "quota_admin"}
	require.True(t, grant.MatchesSource(quotaJob))
	require.False(t, grant.MatchesSource(usageJob))
	quotaJob.SelfExport = true
	require.False(t, grant.MatchesSource(quotaJob))

	token, err := CreateManagedLogExportDownloadToken(quota.JobID, owner, 1, "quota_admin")
	require.NoError(t, err)
	t.Cleanup(func() { _ = common.RedisDel(logExportDLTokenKeyPfx + token) })
	raw, err := common.RedisGet(logExportDLTokenKeyPfx + token)
	require.NoError(t, err)
	stored := &LogExportDownloadGrant{}
	require.NoError(t, common.UnmarshalJsonStr(raw, stored))
	require.Equal(t, 4, stored.Version)
	require.Equal(t, LogExportDataQuota, stored.DataSource)
	require.Equal(t, "quota_admin", stored.Scope)
	require.True(t, stored.MatchesSource(&LogExportJob{DataSource: LogExportDataQuota}))
}

func TestQuotaExportCreatedAtPlan(t *testing.T) {
	requireDB(t)
	if !common.UsingMainDatabase(common.DatabaseTypeMySQL) && !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("created_at plan is checked on MySQL and PostgreSQL")
	}
	statement := DB.Session(&gorm.Session{DryRun: true}).Table("quota_data").
		Select("id").
		Where("created_at >= ? AND created_at <= ?", int64(1), int64(2)).
		Order("created_at").Order("id").Limit(10).
		Find(&[]QuotaData{}).Statement
	explained := DB.Dialector.Explain(statement.SQL.String(), statement.Vars...)
	prefix := "EXPLAIN "
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		prefix = "EXPLAIN FORMAT=TRADITIONAL "
	}
	var plan []map[string]any
	require.NoError(t, DB.Raw(prefix+explained).Scan(&plan).Error)
	for _, row := range plan {
		for key, value := range row {
			if raw, ok := value.([]byte); ok {
				row[key] = string(raw)
			}
		}
	}
	encoded, err := common.Marshal(plan)
	require.NoError(t, err)
	t.Logf("quota_data plan: %s -- %s", explained, encoded)
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		require.NotEmpty(t, plan)
		require.NotEqual(t, "ALL", fmt.Sprint(plan[0]["type"]), "created_at range must not full-scan quota_data")
	} else {
		require.NotContains(t, string(encoded), "Seq Scan")
	}
}

func managedIDs(page *LogExportJobPage) []string {
	ids := make([]string, 0, len(page.Jobs))
	for _, job := range page.Jobs {
		ids = append(ids, job.JobID)
	}
	sort.Strings(ids)
	return ids
}
