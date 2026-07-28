package model

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkExportJob 创建一个任务并注册清理。
func mkExportJob(t *testing.T, userID int, mut func(job *LogExportJob)) *LogExportJob {
	t.Helper()
	job := &LogExportJob{
		JobID:   NewLogExportJobID(),
		UserID:  userID,
		Format:  LogExportFormatCSVGz,
		Columns: DefaultLogExportColumns(),
		Filters: LogExportFilter{StartTimestamp: 1, EndTimestamp: 2},
	}
	if mut != nil {
		mut(job)
	}
	require.NoError(t, CreateLogExportJob(job))
	t.Cleanup(func() { DeleteLogExportJob(job) })
	return job
}

func TestLogExportJob_CreateGetRoundTrip(t *testing.T) {
	enableRedis(t)
	user := nextTestID()
	job := mkExportJob(t, user, func(job *LogExportJob) {
		job.Username = "admin"
		job.Options = LogExportOptions{CSVBOM: true, Header: true, Timezone: "UTC"}
	})

	assert.Equal(t, LogExportStatusPending, job.Status)
	assert.NotZero(t, job.CreatedAt)

	loaded, err := GetLogExportJob(job.JobID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, job.JobID, loaded.JobID)
	assert.Equal(t, user, loaded.UserID)
	assert.Equal(t, "admin", loaded.Username)
	assert.Equal(t, DefaultLogExportColumns(), loaded.Columns)
	assert.Equal(t, "UTC", loaded.Options.Timezone)
}

// 分片路径必须随任务状态一起存进 Redis：重新载入后还要靠它定位文件去下载和清理。
// （曾经把 Path 标成 json:"-"，导致重载后的任务既下载不了也删不掉文件。）
func TestLogExportJob_PartPathSurvivesRedisRoundTrip(t *testing.T) {
	enableRedis(t)
	job := mkExportJob(t, nextTestID(), nil)
	job.Status = LogExportStatusReady
	job.Parts = []LogExportPart{
		{Index: 1, Path: "/tmp/log-export-x-part-0001.csv.gz", Rows: 10, Bytes: 99, StartTime: 1, EndTime: 2},
	}
	UpdateLogExportJob(job)

	loaded, err := GetLogExportJob(job.JobID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Parts, 1)
	assert.Equal(t, "/tmp/log-export-x-part-0001.csv.gz", loaded.Parts[0].Path)
	assert.Equal(t, int64(10), loaded.Parts[0].Rows)
	assert.Equal(t, int64(99), loaded.Parts[0].Bytes)
}

// 下发前端时必须抹掉服务端路径。
func TestLogExportJob_PublicViewStripsPaths(t *testing.T) {
	job := &LogExportJob{
		JobID: "j1",
		Parts: []LogExportPart{
			{Index: 1, Path: "/tmp/secret-part-0001.csv.gz", Rows: 5},
			{Index: 2, Path: "/tmp/secret-part-0002.csv.gz", Rows: 6},
		},
	}
	view := job.PublicView()
	require.Len(t, view.Parts, 2)
	for _, part := range view.Parts {
		assert.Empty(t, part.Path)
	}
	assert.Equal(t, int64(5), view.Parts[0].Rows, "everything else must be preserved")
	// 原对象不受影响，后续仍能用路径删除文件。
	assert.Equal(t, "/tmp/secret-part-0001.csv.gz", job.Parts[0].Path)

	assert.Nil(t, (*LogExportJob)(nil).PublicView())
}

func TestLogExportJob_GetMissingReturnsNil(t *testing.T) {
	enableRedis(t)
	job, err := GetLogExportJob("no-such-job")
	require.NoError(t, err)
	assert.Nil(t, job)
}

func TestLogExportJob_IsTerminal(t *testing.T) {
	cases := map[string]bool{
		LogExportStatusPending:  false,
		LogExportStatusRunning:  false,
		LogExportStatusReady:    true,
		LogExportStatusFailed:   true,
		LogExportStatusCanceled: true,
	}
	for status, want := range cases {
		assert.Equal(t, want, (&LogExportJob{Status: status}).IsTerminal(), status)
	}
}

func TestLogExportJob_ListAndActiveCount(t *testing.T) {
	enableRedis(t)
	user := nextTestID()

	running := mkExportJob(t, user, nil)
	done := mkExportJob(t, user, nil)
	done.Status = LogExportStatusReady
	// 显式拉开创建时刻：Windows 的 time.Now() 粒度可达十几毫秒，连续两次创建
	// 可能落在同一毫秒，用例不该依赖墙钟去区分先后。
	done.CreatedAtMs = running.CreatedAtMs + 5000
	UpdateLogExportJob(done)

	jobs, err := ListLogExportJobs(user, false, 20)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	// 倒序：后创建的排前面。索引分值是毫秒，同一秒内创建的任务顺序也必须稳定。
	assert.Equal(t, done.JobID, jobs[0].JobID)
	assert.Equal(t, running.JobID, jobs[1].JobID)
	assert.Greater(t, done.CreatedAtMs, int64(0), "index score needs millisecond resolution")

	assert.Equal(t, 1, CountActiveLogExportJobs(user), "only non-terminal jobs count as active")

	// 其他用户看不到这些任务。
	otherJobs, err := ListLogExportJobs(nextTestID(), false, 20)
	require.NoError(t, err)
	assert.Empty(t, otherJobs)

	// all=true 走全局索引，能看到本用户的任务。
	allJobs, err := ListLogExportJobs(0, true, 100)
	require.NoError(t, err)
	found := false
	for _, j := range allJobs {
		if j.JobID == running.JobID {
			found = true
		}
	}
	assert.True(t, found, "global listing must include other users' jobs")
}

func TestLogExportJob_DeleteRemovesStateAndIndex(t *testing.T) {
	enableRedis(t)
	user := nextTestID()
	job := mkExportJob(t, user, nil)

	DeleteLogExportJob(job)

	loaded, err := GetLogExportJob(job.JobID)
	require.NoError(t, err)
	assert.Nil(t, loaded)

	jobs, err := ListLogExportJobs(user, false, 20)
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

func TestLogExportSlot_SemaphoreLimitsConcurrency(t *testing.T) {
	enableRedis(t)
	a, b, c := NewLogExportJobID(), NewLogExportJobID(), NewLogExportJobID()
	t.Cleanup(func() {
		ReleaseLogExportSlot(a)
		ReleaseLogExportSlot(b)
		ReleaseLogExportSlot(c)
	})

	assert.True(t, AcquireLogExportSlot(a, 2, time.Minute))
	assert.True(t, AcquireLogExportSlot(b, 2, time.Minute))
	assert.False(t, AcquireLogExportSlot(c, 2, time.Minute), "third job must wait when limit is 2")

	ReleaseLogExportSlot(a)
	assert.True(t, AcquireLogExportSlot(c, 2, time.Minute), "slot must be reusable after release")
}

// max_concurrent_jobs=0 用于停止接受新任务。
func TestLogExportSlot_ZeroLimitRejects(t *testing.T) {
	enableRedis(t)
	assert.False(t, AcquireLogExportSlot(NewLogExportJobID(), 0, time.Minute))
}

// 进程崩溃遗留的槽位必须随过期时间自动回收，不能永久占位。
func TestLogExportSlot_ExpiredSlotsSelfHeal(t *testing.T) {
	enableRedis(t)
	zombie := NewLogExportJobID()
	fresh := NewLogExportJobID()
	t.Cleanup(func() {
		ReleaseLogExportSlot(zombie)
		ReleaseLogExportSlot(fresh)
	})

	// 负 TTL 模拟一个早已过期的槽位。
	require.True(t, AcquireLogExportSlot(zombie, 1, -time.Minute))
	assert.True(t, AcquireLogExportSlot(fresh, 1, time.Minute), "expired slot must be purged before the capacity check")
}

func TestLogExportCooldown(t *testing.T) {
	enableRedis(t)
	user := nextTestID()
	t.Cleanup(func() { ClearLogExportCooldown(user) })

	assert.False(t, CheckAndSetLogExportCooldown(user), "first request must not be on cooldown")
	assert.True(t, CheckAndSetLogExportCooldown(user), "second request within the window is on cooldown")

	// 创建失败时回收冷却，不白占用户的一次机会。
	ClearLogExportCooldown(user)
	assert.False(t, CheckAndSetLogExportCooldown(user))
}

// removeCancelFlagForTest 清掉取消标志，供用例之间互不干扰。
func removeCancelFlagForTest(jobID string) error {
	return common.RedisDel(logExportCancelKeyPfx + jobID)
}

func TestLogExportCancelFlag(t *testing.T) {
	enableRedis(t)
	jobID := NewLogExportJobID()
	assert.False(t, isLogExportJobCanceled(jobID))

	CancelLogExportJob(jobID)
	assert.True(t, isLogExportJobCanceled(jobID))
	t.Cleanup(func() { _ = common.RedisDel(logExportCancelKeyPfx + jobID) })
}

// 下载令牌首次使用后转为「已绑定」并续期，让 Range 续传的后续请求仍可用同一令牌。
func TestLogExportDownloadToken_BindsForResumableRange(t *testing.T) {
	enableRedis(t)
	jobID := NewLogExportJobID()
	token, err := CreateLogExportDownloadToken(jobID, 77, 3)
	require.NoError(t, err)
	t.Cleanup(func() { _ = common.RedisDel(logExportDLTokenKeyPfx + token) })

	gotJob, gotUser, gotPart, err := ConsumeLogExportDownloadToken(token)
	require.NoError(t, err)
	assert.Equal(t, jobID, gotJob)
	assert.Equal(t, 77, gotUser)
	assert.Equal(t, 3, gotPart)

	// 续传窗口内可重复使用。
	gotJob2, gotUser2, gotPart2, err := ConsumeLogExportDownloadToken(token)
	require.NoError(t, err)
	assert.Equal(t, jobID, gotJob2)
	assert.Equal(t, 77, gotUser2)
	assert.Equal(t, 3, gotPart2)
}

func TestLogExportDownloadToken_MissingReturnsEmpty(t *testing.T) {
	enableRedis(t)
	jobID, userID, part, err := ConsumeLogExportDownloadToken("not-a-token")
	require.NoError(t, err)
	assert.Equal(t, "", jobID)
	assert.Zero(t, userID)
	assert.Zero(t, part)
}

func TestLogExportDownloadToken_PartZeroMeansWholeArchive(t *testing.T) {
	enableRedis(t)
	token, err := CreateLogExportDownloadToken("job-x", 5, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = common.RedisDel(logExportDLTokenKeyPfx + token) })

	_, _, part, err := ConsumeLogExportDownloadToken(token)
	require.NoError(t, err)
	assert.Zero(t, part)
}

func TestCleanupOrphanLogExportFiles(t *testing.T) {
	enableRedis(t)
	// 有任务的文件保留，无任务的文件删除。
	live := mkExportJob(t, nextTestID(), nil)
	livePath := LogExportPartFilePath(live.JobID, 1, LogExportFormatCSVGz)
	require.NoError(t, os.WriteFile(livePath, []byte("x"), 0o600))
	t.Cleanup(func() { _ = os.Remove(livePath) })

	orphanPath := LogExportPartFilePath(NewLogExportJobID(), 1, LogExportFormatCSVGz)
	require.NoError(t, os.WriteFile(orphanPath, []byte("x"), 0o600))
	t.Cleanup(func() { _ = os.Remove(orphanPath) })

	CleanupOrphanLogExportFiles()

	_, err := os.Stat(livePath)
	assert.NoError(t, err, "file of a live job must be kept")
	_, err = os.Stat(orphanPath)
	assert.True(t, os.IsNotExist(err), "orphan file must be removed")
}

func TestRemoveLogExportJobFiles(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.csv.gz")
	p2 := filepath.Join(dir, "b.csv.gz")
	require.NoError(t, os.WriteFile(p1, []byte("a"), 0o600))
	require.NoError(t, os.WriteFile(p2, []byte("b"), 0o600))

	RemoveLogExportJobFiles(&LogExportJob{Parts: []LogExportPart{
		{Index: 1, Path: p1}, {Index: 2, Path: p2}, {Index: 3, Path: ""},
	}})

	_, err := os.Stat(p1)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(p2)
	assert.True(t, os.IsNotExist(err))
}

// 进程重启后遗留的 running 任务没有 goroutine 继续推进，必须置为失败。
func TestRecoverStaleLogExportJobs(t *testing.T) {
	enableRedis(t)
	user := nextTestID()
	stale := mkExportJob(t, user, nil)
	stale.Status = LogExportStatusRunning
	UpdateLogExportJob(stale)

	finished := mkExportJob(t, user, nil)
	finished.Status = LogExportStatusReady
	UpdateLogExportJob(finished)

	RecoverStaleLogExportJobs()

	reloaded, err := GetLogExportJob(stale.JobID)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, LogExportStatusFailed, reloaded.Status)
	assert.NotEmpty(t, reloaded.Error)

	untouched, err := GetLogExportJob(finished.JobID)
	require.NoError(t, err)
	require.NotNil(t, untouched)
	assert.Equal(t, LogExportStatusReady, untouched.Status)
}

// 多实例部署下任务状态存共享 Redis，分片文件却在执行节点本地。
// 回收必须只认本节点的任务，否则一个节点重启会把另一个节点正在跑的导出
// 标记为失败并删掉它的分片文件。
func TestRecoverStaleLogExportJobs_SkipsOtherNodes(t *testing.T) {
	enableRedis(t)
	user := nextTestID()

	// 另一节点正在跑的任务：必须原样保留，分片文件也不能被删。
	foreign := mkExportJob(t, user, nil)
	foreign.Status = LogExportStatusRunning
	foreign.NodeName = common.NodeName + "-other"
	foreignPart := filepath.Join(t.TempDir(), "foreign-part-0001.csv.gz")
	require.NoError(t, os.WriteFile(foreignPart, []byte("keep me"), 0o600))
	foreign.Parts = []LogExportPart{{Index: 1, Path: foreignPart}}
	UpdateLogExportJob(foreign)

	// 本节点遗留的任务：必须置为失败。
	own := mkExportJob(t, user, nil)
	own.Status = LogExportStatusRunning
	UpdateLogExportJob(own)

	// 升级前创建的存量任务（NodeName 为空）：按旧行为回收，不能永远卡在 running。
	legacy := mkExportJob(t, user, nil)
	legacy.Status = LogExportStatusRunning
	legacy.NodeName = ""
	UpdateLogExportJob(legacy)

	RecoverStaleLogExportJobs()

	reloadedForeign, err := GetLogExportJob(foreign.JobID)
	require.NoError(t, err)
	require.NotNil(t, reloadedForeign)
	assert.Equal(t, LogExportStatusRunning, reloadedForeign.Status,
		"另一节点正在跑的任务不能被标记为失败")
	assert.Empty(t, reloadedForeign.Error)
	assert.FileExists(t, foreignPart, "另一节点的分片文件不能被删除")

	reloadedOwn, err := GetLogExportJob(own.JobID)
	require.NoError(t, err)
	require.NotNil(t, reloadedOwn)
	assert.Equal(t, LogExportStatusFailed, reloadedOwn.Status)

	reloadedLegacy, err := GetLogExportJob(legacy.JobID)
	require.NoError(t, err)
	require.NotNil(t, reloadedLegacy)
	assert.Equal(t, LogExportStatusFailed, reloadedLegacy.Status)
}

// CreateLogExportJob 必须打上本节点标记，否则回收过滤无从判断归属。
func TestCreateLogExportJob_StampsNodeName(t *testing.T) {
	enableRedis(t)
	job := mkExportJob(t, nextTestID(), nil)

	assert.Equal(t, common.NodeName, job.NodeName)

	reloaded, err := GetLogExportJob(job.JobID)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, common.NodeName, reloaded.NodeName, "节点标记必须能从 Redis 往返")
}

func TestCheckLogExportDiskSpace(t *testing.T) {
	s := operation_setting.GetLogExportSetting()
	prev := s.MinFreeDiskMB
	t.Cleanup(func() { s.MinFreeDiskMB = prev })

	// 极小阈值：正常机器都应通过。
	s.MinFreeDiskMB = 1
	assert.NoError(t, CheckLogExportDiskSpace())

	// 不可能满足的阈值：必须拒绝，避免把磁盘写满拖垮整个服务。
	info := common.GetDiskSpaceInfo()
	if info.Total == 0 {
		t.Skip("disk info unavailable on this platform")
	}
	s.MinFreeDiskMB = int(info.Free/1024/1024) + 1024*1024
	assert.ErrorIs(t, CheckLogExportDiskSpace(), ErrLogExportDiskFull)
}

func TestLogExportAvailable_RequiresRedis(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	assert.False(t, LogExportAvailable())
	common.RedisEnabled = prev
}
