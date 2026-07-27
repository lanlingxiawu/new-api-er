package model

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// log.go writes/reads the `logs` table on LOG_DB (a separate PostgreSQL in this
// environment). deleteByID targets the MAIN DB, so log rows are cleaned up with
// LOG_DB-scoped helpers below. Tests scope every aggregation/count to a unique
// username / user id / channel / group so they never read sibling rows.
//
// Cross-dialect note (Rule 2): group filters use logGroupCol which is "group"
// on PostgreSQL and `group` on MySQL; these tests run against LOG_DB=PostgreSQL
// so the double-quoted reserved word is exercised. ClickHouse-only branches
// (clickHouseLogOrder in queries, sanitizeClickHouseLikePattern inside filters,
// ALTER TABLE ... DELETE) cannot run here and are noted where relevant.
// ---------------------------------------------------------------------------

func init() { gin.SetMode(gin.TestMode) }

// logCleanupUser hard-deletes every logs row for a user id on LOG_DB.
func logCleanupUser(t *testing.T, userID int) {
	t.Helper()
	t.Cleanup(func() {
		if LOG_DB != nil {
			LOG_DB.Where("user_id = ?", userID).Delete(&Log{})
		}
	})
}

// mkLogRow inserts a logs row on LOG_DB (via createLog) and cleans it up by id.
func mkLogRow(t *testing.T, mut func(l *Log)) *Log {
	t.Helper()
	requireLogDB(t)
	l := &Log{
		UserId:    nextTestID(),
		Username:  uniq("lu"),
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeConsume,
	}
	if mut != nil {
		mut(l)
	}
	require.NoError(t, createLog(l))
	id := l.Id
	t.Cleanup(func() {
		if LOG_DB != nil {
			LOG_DB.Where("id = ?", id).Delete(&Log{})
		}
	})
	return l
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

func TestClickHouseLogOrder(t *testing.T) {
	assert.Equal(t, "created_at desc, request_id desc", clickHouseLogOrder(""))
	assert.Equal(t, "logs.created_at desc, logs.request_id desc", clickHouseLogOrder("logs."))
}

func TestAssignDisplayLogIds(t *testing.T) {
	logs := []*Log{{}, {}, {}}
	assignDisplayLogIds(logs, 10)
	assert.Equal(t, 11, logs[0].Id)
	assert.Equal(t, 12, logs[1].Id)
	assert.Equal(t, 13, logs[2].Id)
}

func TestBuildOpField(t *testing.T) {
	// action only
	op := buildOpField("delete_user", nil)
	assert.Equal(t, "delete_user", op["action"])
	_, hasParams := op["params"]
	assert.False(t, hasParams)
	// action + params
	op = buildOpField("ban", map[string]interface{}{"id": 5})
	assert.Equal(t, "ban", op["action"])
	assert.NotNil(t, op["params"])
}

func TestEnsureLogRequestId(t *testing.T) {
	ensureLogRequestId(nil) // nil-safe, must not panic
	l := &Log{}
	ensureLogRequestId(l)
	assert.NotEmpty(t, l.RequestId)
	l2 := &Log{RequestId: "keep"}
	ensureLogRequestId(l2)
	assert.Equal(t, "keep", l2.RequestId)
}

func TestFormatUserLogs(t *testing.T) {
	other := common.MapToJsonStr(map[string]interface{}{
		"admin_info":    map[string]interface{}{"x": 1},
		"audit_info":    map[string]interface{}{"y": 2},
		"stream_status": "done",
		"keep":          "visible",
	})
	logs := []*Log{{ChannelName: "should-clear", Other: other}}
	formatUserLogs(logs, 5)

	assert.Equal(t, "", logs[0].ChannelName)
	assert.Equal(t, 6, logs[0].Id) // startIdx(5)+0+1
	m, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, m, "admin_info")
	assert.NotContains(t, m, "audit_info")
	assert.NotContains(t, m, "stream_status")
	assert.Equal(t, "visible", m["keep"])
}

func TestSanitizeClickHouseLikePattern(t *testing.T) {
	// underscore is escaped with backslash; no % -> valid
	out, err := sanitizeClickHouseLikePattern("a_b")
	require.NoError(t, err)
	assert.Equal(t, `a\_b`, out)
	// backslash doubled
	out, err = sanitizeClickHouseLikePattern(`a\b`)
	require.NoError(t, err)
	assert.Equal(t, `a\\b`, out)
	// invalid: consecutive %
	_, err = sanitizeClickHouseLikePattern("a%%b")
	assert.Error(t, err)
}

func TestBuildLogLikeCondition_NonClickHouse(t *testing.T) {
	// LOG_DB is PostgreSQL here -> the ESCAPE '!' form
	cond, pattern, err := buildLogLikeCondition("logs.model_name", "gpt%")
	require.NoError(t, err)
	assert.Equal(t, "logs.model_name LIKE ? ESCAPE '!'", cond)
	assert.Equal(t, "gpt%", pattern)
	// invalid pattern surfaces error
	_, _, err = buildLogLikeCondition("logs.model_name", "a%%b")
	assert.Error(t, err)
}

func TestApplyExplicitLogTextFilter(t *testing.T) {
	requireLogDB(t)
	base := LOG_DB.Model(&Log{})
	// empty value -> unchanged tx, no error
	tx, err := applyExplicitLogTextFilter(base, "model_name", "")
	require.NoError(t, err)
	assert.NotNil(t, tx)
	// exact value (no %) -> equality branch, no error
	tx, err = applyExplicitLogTextFilter(base, "model_name", "gpt-4o")
	require.NoError(t, err)
	assert.NotNil(t, tx)
	// fuzzy value -> LIKE branch, no error
	tx, err = applyExplicitLogTextFilter(base, "model_name", "gpt%")
	require.NoError(t, err)
	assert.NotNil(t, tx)
	// invalid fuzzy -> error, nil tx
	tx, err = applyExplicitLogTextFilter(base, "model_name", "a%%b")
	assert.Error(t, err)
	assert.Nil(t, tx)
}

// ---------------------------------------------------------------------------
// RecordLog family (writes to LOG_DB)
// ---------------------------------------------------------------------------

// countUserLogs is a small assertion helper scoped to a user id.
func countUserLogs(t *testing.T, userID int) int64 {
	t.Helper()
	var n int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("user_id = ?", userID).Count(&n).Error)
	return n
}

func firstUserLog(t *testing.T, userID int) *Log {
	t.Helper()
	var l Log
	require.NoError(t, LOG_DB.Where("user_id = ?", userID).Order("id desc").First(&l).Error)
	return &l
}

func TestRecordLog_ConsumeDisabledSkips(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)

	prev := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = prev })

	RecordLog(u.Id, LogTypeConsume, "should be skipped")
	assert.EqualValues(t, 0, countUserLogs(t, u.Id))
}

func TestRecordLog_Basic(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)

	RecordLog(u.Id, LogTypeManage, "hello")
	require.EqualValues(t, 1, countUserLogs(t, u.Id))
	l := firstUserLog(t, u.Id)
	assert.Equal(t, LogTypeManage, l.Type)
	assert.Equal(t, "hello", l.Content)
	assert.Equal(t, u.Username, l.Username) // resolved from main DB
	assert.NotEmpty(t, l.RequestId)         // auto-filled
}

func TestRecordLogWithAdminInfo(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)

	RecordLogWithAdminInfo(u.Id, LogTypeManage, "admin op", map[string]interface{}{"operator": "root"})
	l := firstUserLog(t, u.Id)
	m, err := common.StrToMap(l.Other)
	require.NoError(t, err)
	assert.Contains(t, m, "admin_info")

	// consume + disabled skips
	prev := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = prev })
	before := countUserLogs(t, u.Id)
	RecordLogWithAdminInfo(u.Id, LogTypeConsume, "skip", nil)
	assert.Equal(t, before, countUserLogs(t, u.Id))
}

func TestRecordLoginLog(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)

	RecordLoginLog(u.Id, u.Username, "login ok", "10.0.0.1",
		"login", map[string]interface{}{"method": "password"},
		map[string]interface{}{"user_agent": "go-test"})

	l := firstUserLog(t, u.Id)
	assert.Equal(t, LogTypeLogin, l.Type)
	assert.Equal(t, "10.0.0.1", l.Ip)
	m, err := common.StrToMap(l.Other)
	require.NoError(t, err)
	assert.Equal(t, "go-test", m["user_agent"]) // extra merged
	op, ok := m["op"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "login", op["action"])
}

func TestRecordOperationAuditLog(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)

	RecordOperationAuditLog(u.Id, "did thing", "10.0.0.2", "ban_user",
		map[string]interface{}{"target": 9},
		map[string]interface{}{"operator": "admin"},
		map[string]interface{}{"route": "/api/x"})

	l := firstUserLog(t, u.Id)
	assert.Equal(t, LogTypeManage, l.Type)
	m, err := common.StrToMap(l.Other)
	require.NoError(t, err)
	assert.Contains(t, m, "op")
	assert.Contains(t, m, "admin_info")
	assert.Contains(t, m, "audit_info")
}

func TestRecordTopupLog(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)

	RecordTopupLog(u.Id, "topup 100", "1.2.3.4", "stripe", "card")
	l := firstUserLog(t, u.Id)
	assert.Equal(t, LogTypeTopup, l.Type)
	assert.Equal(t, "1.2.3.4", l.Ip)
	m, err := common.StrToMap(l.Other)
	require.NoError(t, err)
	admin, ok := m["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "stripe", admin["payment_method"])
	assert.Equal(t, "card", admin["callback_payment_method"])
}

func TestRecordErrorLog(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat", nil)
	c.Set("username", u.Username)

	// default user setting -> RecordIpLog false -> ip stays empty
	RecordErrorLog(c, u.Id, 42, "gpt-4o", "tok-1", "boom", 7, 3, true, "grpA",
		map[string]interface{}{"detail": "x"})

	l := firstUserLog(t, u.Id)
	assert.Equal(t, LogTypeError, l.Type)
	assert.Equal(t, "boom", l.Content)
	assert.Equal(t, 42, l.ChannelId)
	assert.Equal(t, "gpt-4o", l.ModelName)
	assert.Equal(t, "grpA", l.Group)
	assert.True(t, l.IsStream)
	assert.Equal(t, "", l.Ip)
}

func TestRecordErrorLog_RecordsIpWhenEnabled(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, func(usr *User) {
		usr.SetSetting(dto.UserSetting{RecordIpLog: true})
	})
	logCleanupUser(t, u.Id)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat", nil)
	c.Request.RemoteAddr = "203.0.113.7:5555"
	c.Set("username", u.Username)

	RecordErrorLog(c, u.Id, 1, "m", "tk", "err", 0, 0, false, "g", nil)
	l := firstUserLog(t, u.Id)
	assert.NotEmpty(t, l.Ip) // ClientIP recorded because record_ip_log=true
}

func TestRecordConsumeLog(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat", nil)
	c.Set("username", u.Username)

	// disabled -> returns 0, writes nothing
	prev := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	assert.Equal(t, 0, RecordConsumeLog(c, u.Id, RecordConsumeLogParams{Quota: 5}))
	assert.EqualValues(t, 0, countUserLogs(t, u.Id))
	common.LogConsumeEnabled = prev

	// enabled -> returns positive id, exact quota/token values
	id := RecordConsumeLog(c, u.Id, RecordConsumeLogParams{
		ChannelId: 3, PromptTokens: 11, CompletionTokens: 22, ModelName: "gpt-4o",
		TokenName: "tk", Quota: 1234, TokenId: 9, UseTimeSeconds: 4, IsStream: true, Group: "g",
	})
	assert.Positive(t, id)
	l := firstUserLog(t, u.Id)
	assert.Equal(t, LogTypeConsume, l.Type)
	assert.Equal(t, 1234, l.Quota)
	assert.Equal(t, 11, l.PromptTokens)
	assert.Equal(t, 22, l.CompletionTokens)
}

func TestRecordConsumeLog_DataExportLogsQuota(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	logCleanupUser(t, u.Id)
	usedataResetCache(t)

	prevExport := common.DataExportEnabled
	common.DataExportEnabled = true
	t.Cleanup(func() { common.DataExportEnabled = prevExport })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	c.Set("username", u.Username)

	model := uniq("cem")
	id := RecordConsumeLog(c, u.Id, RecordConsumeLogParams{
		PromptTokens: 5, CompletionTokens: 5, ModelName: model, Quota: 100, Group: "g",
	})
	assert.Positive(t, id)

	// LogQuotaData appended one entry to the in-memory cache for this user
	CacheQuotaDataLock.Lock()
	var found *QuotaData
	for _, v := range CacheQuotaData {
		if v.UserID == u.Id && v.ModelName == model {
			found = v
		}
	}
	CacheQuotaDataLock.Unlock()
	require.NotNil(t, found)
	assert.Equal(t, 100, found.Quota)
	assert.Equal(t, 10, found.TokenUsed) // prompt+completion
}

func TestRecordTaskBillingLog(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	tk := mkToken(t, u.Id, nil)
	logCleanupUser(t, u.Id)

	// consume + disabled -> 0
	prev := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	assert.Equal(t, 0, RecordTaskBillingLog(RecordTaskBillingLogParams{UserId: u.Id, LogType: LogTypeConsume}))
	common.LogConsumeEnabled = prev

	// normal consume with token id -> token name resolved
	id := RecordTaskBillingLog(RecordTaskBillingLogParams{
		UserId: u.Id, LogType: LogTypeConsume, Content: "task", ChannelId: 2,
		ModelName: "mj", Quota: 50, TokenId: tk.Id, Group: "g",
	})
	assert.Positive(t, id)
	l := firstUserLog(t, u.Id)
	assert.Equal(t, tk.Name, l.TokenName)
	assert.Equal(t, 50, l.Quota)
}

// ---------------------------------------------------------------------------
// Channel-name snapshots from logs
// ---------------------------------------------------------------------------

func TestGetChannelNameSnapshotsFromLogs(t *testing.T) {
	requireLogDB(t)
	chID := nextTestID()
	uid := nextTestID()
	logCleanupUser(t, uid)

	// older snapshot then newer snapshot; newest (by created_at desc) wins
	mkLogRow(t, func(l *Log) {
		l.UserId, l.ChannelId, l.CreatedAt = uid, chID, common.GetTimestamp() - 100
		l.Other = common.MapToJsonStr(map[string]interface{}{"channel_name": "OldName"})
	})
	mkLogRow(t, func(l *Log) {
		l.UserId, l.ChannelId, l.CreatedAt = uid, chID, common.GetTimestamp()
		l.Other = common.MapToJsonStr(map[string]interface{}{"channel_name": "NewName"})
	})

	m := GetChannelNameSnapshotsFromLogs([]int{chID})
	assert.Equal(t, "NewName", m[chID])

	// empty / zero / duplicate ids handled
	assert.Empty(t, GetChannelNameSnapshotsFromLogs(nil))
	assert.Empty(t, GetChannelNameSnapshotsFromLogs([]int{0, 0}))

	// context variant
	m = GetChannelNameSnapshotsFromLogsWithContext(context.Background(), []int{chID, chID})
	assert.Equal(t, "NewName", m[chID])
}

// ---------------------------------------------------------------------------
// GetAllLogs — filters, pagination, channel-name fill
// ---------------------------------------------------------------------------

func TestGetAllLogs(t *testing.T) {
	requireLogDB(t)
	uname := uniq("gal")
	uid := nextTestID()
	logCleanupUser(t, uid)
	ch := mkChannel(t, nil)
	now := common.GetTimestamp()
	grp := uniq("galg")

	// three consume logs sharing username, increasing created_at
	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.ModelName, l.TokenName, l.Group = uid, uname, "gpt-4o", "tkA", grp
		l.ChannelId, l.Quota, l.CreatedAt, l.RequestId = ch.Id, 10, now, "rid-1"
	})
	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.ModelName, l.TokenName, l.Group = uid, uname, "claude-3", "tkB", grp
		l.ChannelId, l.Quota, l.CreatedAt, l.UpstreamRequestId = ch.Id, 20, now + 1, "urid-9"
	})
	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.Type, l.CreatedAt = uid, uname, LogTypeManage, now + 2
		l.Content = "mgmt"
	})

	t.Run("all types via username filter", func(t *testing.T) {
		logs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", uname, "", 0, 50, 0, 0, "", "", "")
		require.NoError(t, err)
		assert.EqualValues(t, 3, total)
		assert.Len(t, logs, 3)
		// ordered created_at desc -> the manage log (now+2) first
		assert.Equal(t, LogTypeManage, logs[0].Type)
	})

	t.Run("type filter", func(t *testing.T) {
		_, total, err := GetAllLogs(LogTypeConsume, 0, 0, "", uname, "", 0, 50, 0, 0, "", "", "")
		require.NoError(t, err)
		assert.EqualValues(t, 2, total)
	})

	t.Run("model exact and fuzzy", func(t *testing.T) {
		_, total, err := GetAllLogs(LogTypeConsume, 0, 0, "gpt-4o", uname, "", 0, 50, 0, 0, "", "", "")
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		_, total, err = GetAllLogs(LogTypeConsume, 0, 0, "claude%", uname, "", 0, 50, 0, 0, "", "", "")
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
	})

	t.Run("invalid model pattern errors", func(t *testing.T) {
		_, _, err := GetAllLogs(LogTypeConsume, 0, 0, "a%%b", uname, "", 0, 50, 0, 0, "", "", "")
		assert.Error(t, err)
	})

	t.Run("token name filter", func(t *testing.T) {
		_, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", uname, "tkA", 0, 50, 0, 0, "", "", "")
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
	})

	t.Run("channel + group filters and channel-name fill", func(t *testing.T) {
		logs, total, err := GetAllLogs(LogTypeConsume, 0, 0, "", uname, "", 0, 50, ch.Id, 0, grp, "", "")
		require.NoError(t, err)
		assert.EqualValues(t, 2, total)
		require.NotEmpty(t, logs)
		assert.Equal(t, ch.Name, logs[0].ChannelName) // resolved from channels table
	})

	t.Run("time range", func(t *testing.T) {
		_, total, err := GetAllLogs(LogTypeUnknown, now+1, now+2, "", uname, "", 0, 50, 0, 0, "", "", "")
		require.NoError(t, err)
		assert.EqualValues(t, 2, total)
	})

	t.Run("request id filters", func(t *testing.T) {
		_, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", uname, "", 0, 50, 0, 0, "", "rid-1", "")
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		_, total, err = GetAllLogs(LogTypeUnknown, 0, 0, "", uname, "", 0, 50, 0, 0, "", "", "urid-9")
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
	})

	t.Run("pagination offset/limit", func(t *testing.T) {
		page1, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", uname, "", 0, 2, 0, 0, "", "", "")
		require.NoError(t, err)
		assert.EqualValues(t, 3, total)
		assert.Len(t, page1, 2)
		page2, _, err := GetAllLogs(LogTypeUnknown, 0, 0, "", uname, "", 2, 2, 0, 0, "", "", "")
		require.NoError(t, err)
		assert.Len(t, page2, 1)
	})
}

// ---------------------------------------------------------------------------
// ExportLogs — batched streaming
// ---------------------------------------------------------------------------

func TestExportLogs(t *testing.T) {
	requireLogDB(t)
	uname := uniq("exp")
	uid := nextTestID()
	logCleanupUser(t, uid)
	now := common.GetTimestamp()
	for i := 0; i < 3; i++ {
		mkLogRow(t, func(l *Log) {
			l.UserId, l.Username, l.CreatedAt, l.ModelName = uid, uname, now + int64(i), "gpt-4o"
		})
	}

	var collected int
	err := ExportLogs(LogTypeConsume, 0, 0, "", uname, "", 0, "", uid, func(batch []*Log) error {
		collected += len(batch)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, collected)

	// model fuzzy filter narrows the export
	collected = 0
	err = ExportLogs(LogTypeConsume, 0, 0, "gpt%", uname, "", 0, "", uid, func(batch []*Log) error {
		collected += len(batch)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, collected)

	// fn error is propagated
	sentinel := context.Canceled
	err = ExportLogs(LogTypeConsume, 0, 0, "", uname, "", 0, "", uid, func(batch []*Log) error {
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)

	// invalid pattern surfaces before iterating
	err = ExportLogs(LogTypeConsume, 0, 0, "a%%b", uname, "", 0, "", uid, func(batch []*Log) error { return nil })
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetUserLogs
// ---------------------------------------------------------------------------

func TestGetUserLogs(t *testing.T) {
	requireLogDB(t)
	uid := nextTestID()
	uname := uniq("gul")
	logCleanupUser(t, uid)
	now := common.GetTimestamp()
	grp := uniq("gulg")

	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.ModelName, l.Group, l.CreatedAt, l.RequestId = uid, uname, "gpt-4o", grp, now, "urq"
		// Other carries admin_info to prove formatUserLogs strips it
		l.Other = common.MapToJsonStr(map[string]interface{}{"admin_info": map[string]interface{}{"a": 1}, "keep": "y"})
	})
	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.Type, l.CreatedAt = uid, uname, LogTypeManage, now + 1
	})

	// all types
	logs, total, err := GetUserLogs(uid, LogTypeUnknown, 0, 0, "", "", 0, 50, 0, "", "", "")
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.NotEmpty(t, logs)
	// admin_info stripped by formatUserLogs
	for _, l := range logs {
		m, _ := common.StrToMap(l.Other)
		assert.NotContains(t, m, "admin_info")
	}

	// type filter
	_, total, err = GetUserLogs(uid, LogTypeConsume, 0, 0, "", "", 0, 50, 0, "", "", "")
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// group filter (logGroupCol reserved word on PG)
	_, total, err = GetUserLogs(uid, LogTypeUnknown, 0, 0, "", "", 0, 50, 0, grp, "", "")
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// request id filter
	_, total, err = GetUserLogs(uid, LogTypeUnknown, 0, 0, "", "", 0, 50, 0, "", "urq", "")
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// time-range filter
	_, total, err = GetUserLogs(uid, LogTypeUnknown, now+1, 0, "", "", 0, 50, 0, "", "", "")
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

func TestGetLogByTokenId(t *testing.T) {
	requireLogDB(t)
	uid := nextTestID()
	tokenID := nextTestID()
	logCleanupUser(t, uid)
	mkLogRow(t, func(l *Log) {
		l.UserId, l.TokenId, l.Other = uid, tokenID, common.MapToJsonStr(map[string]interface{}{"admin_info": 1, "keep": 2})
	})

	logs, err := GetLogByTokenId(tokenID)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	m, _ := common.StrToMap(logs[0].Other)
	assert.NotContains(t, m, "admin_info") // formatUserLogs applied
}

// ---------------------------------------------------------------------------
// Aggregations: GetConsumptionByChannelGroup / SumUsedQuota / SumUsedToken
// ---------------------------------------------------------------------------

func TestGetConsumptionByChannelGroup(t *testing.T) {
	requireLogDB(t)
	uid := nextTestID()
	logCleanupUser(t, uid)
	chID := nextTestID()
	grp := uniq("ccg")
	now := common.GetTimestamp()

	mkLogRow(t, func(l *Log) { l.UserId, l.ChannelId, l.Group, l.Quota, l.CreatedAt = uid, chID, grp, 30, now })
	mkLogRow(t, func(l *Log) { l.UserId, l.ChannelId, l.Group, l.Quota, l.CreatedAt = uid, chID, grp, 70, now })

	rows, err := GetConsumptionByChannelGroup(now-10, now+10)
	require.NoError(t, err)
	var mine *ChannelGroupConsumption
	for i := range rows {
		if rows[i].ChannelId == chID && rows[i].GroupName == grp {
			mine = &rows[i]
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, 100, mine.Quota) // 30 + 70
}

func TestSumUsedQuota(t *testing.T) {
	requireLogDB(t)
	uname := uniq("suq")
	uid := nextTestID()
	logCleanupUser(t, uid)
	now := common.GetTimestamp()
	grp := uniq("suqg")
	chID := nextTestID()

	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.Group, l.ChannelId = uid, uname, grp, chID
		l.Quota, l.PromptTokens, l.CompletionTokens, l.CreatedAt = 100, 10, 5, now
	})
	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.Group, l.ChannelId = uid, uname, grp, chID
		l.Quota, l.PromptTokens, l.CompletionTokens, l.CreatedAt = 200, 20, 5, now
	})

	stat, err := SumUsedQuota(LogTypeConsume, now-10, now+10, "", uname, "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 300, stat.Quota) // 100+200
	// rpm/tpm only count the last 60s; our rows are "now"
	assert.Equal(t, 2, stat.Rpm)
	assert.Equal(t, 40, stat.Tpm) // (10+5)+(20+5)

	// channel + group filter branch
	stat, err = SumUsedQuota(LogTypeConsume, 0, 0, "", uname, "", chID, grp)
	require.NoError(t, err)
	assert.Equal(t, 300, stat.Quota)

	// invalid model pattern -> error
	_, err = SumUsedQuota(LogTypeConsume, 0, 0, "a%%b", uname, "", 0, "")
	assert.Error(t, err)
}

func TestSumUsedToken(t *testing.T) {
	requireLogDB(t)
	uname := uniq("sut")
	uid := nextTestID()
	logCleanupUser(t, uid)
	now := common.GetTimestamp()
	model := uniq("sutm")

	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.ModelName, l.PromptTokens, l.CompletionTokens, l.CreatedAt = uid, uname, model, 7, 3, now
	})
	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.ModelName, l.PromptTokens, l.CompletionTokens, l.CreatedAt = uid, uname, model, 5, 5, now
	})

	token := SumUsedToken(LogTypeConsume, now-10, now+10, model, uname, "")
	assert.Equal(t, 20, token) // (7+3)+(5+5)
}

// ---------------------------------------------------------------------------
// Employee/customer log scoping (split-DB path: LOG_DB != DB here)
// ---------------------------------------------------------------------------

func TestGetEmployeeCustomerLogs(t *testing.T) {
	requireLogDB(t)
	require.NotEqual(t, LOG_DB, DB, "this test targets the split-DB user-id scoping path")

	employee := mkUser(t, nil)
	cust1 := mkUser(t, func(u *User) { u.InviterId = employee.Id })
	cust2 := mkUser(t, func(u *User) { u.InviterId = employee.Id })
	// a customer excluded because it has an enabled EmployeeProfile
	excluded := mkUser(t, func(u *User) { u.InviterId = employee.Id })
	ep := &EmployeeProfile{UserId: excluded.Id, Status: CustomerStatusEnabled}
	require.NoError(t, DB.Create(ep).Error)
	deleteByID(t, &EmployeeProfile{}, ep.Id)

	ch := mkChannel(t, nil)
	logCleanupUser(t, employee.Id)
	logCleanupUser(t, cust1.Id)
	logCleanupUser(t, cust2.Id)
	logCleanupUser(t, excluded.Id)
	now := common.GetTimestamp()

	mkLogRow(t, func(l *Log) {
		l.UserId, l.Username, l.Quota, l.CreatedAt, l.PromptTokens = cust1.Id, cust1.Username, 10, now, 3
		l.ChannelId = ch.Id // exercises fillLogChannelNames DB path
	})
	mkLogRow(t, func(l *Log) { l.UserId, l.Username, l.Quota, l.CreatedAt, l.CompletionTokens = cust2.Id, cust2.Username, 20, now, 4 })
	mkLogRow(t, func(l *Log) { l.UserId, l.Username, l.Quota, l.CreatedAt = excluded.Id, excluded.Username, 999, now })
	mkLogRow(t, func(l *Log) { l.UserId, l.Username, l.Quota, l.CreatedAt = employee.Id, employee.Username, 5, now })

	// employee scope (no specific customer) -> employee's own + cust1 + cust2, NOT excluded
	logs, total, err := GetEmployeeCustomerLogs(EmployeeCustomerLogFilter{
		EmployeeUserId: employee.Id, PageSize: 50,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	got := map[int]bool{}
	var cust1Log *Log
	for _, l := range logs {
		got[l.UserId] = true
		if l.UserId == cust1.Id {
			cust1Log = l
		}
	}
	require.NotNil(t, cust1Log)
	assert.Equal(t, ch.Name, cust1Log.ChannelName) // fillLogChannelNames resolved it
	assert.True(t, got[cust1.Id])
	assert.True(t, got[cust2.Id])
	assert.True(t, got[employee.Id])
	assert.False(t, got[excluded.Id])

	// scoped to a single customer
	_, total, err = GetEmployeeCustomerLogs(EmployeeCustomerLogFilter{
		EmployeeUserId: employee.Id, CustomerUserId: cust1.Id, PageSize: 50,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// employeeUserId <= 0 -> empty result set (no scope resolvable)
	logs, total, err = GetEmployeeCustomerLogs(EmployeeCustomerLogFilter{EmployeeUserId: 0, PageSize: 50})
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
	assert.Empty(t, logs)

	// SumEmployeeCustomerUsedQuota over the same scope
	stat, err := SumEmployeeCustomerUsedQuota(EmployeeCustomerLogFilter{EmployeeUserId: employee.Id})
	require.NoError(t, err)
	assert.Equal(t, 35, stat.Quota) // 10 + 20 + 5 (excluded's 999 not counted)

	// empty scope (employeeUserId <= 0) -> zero-value stat, no error
	stat, err = SumEmployeeCustomerUsedQuota(EmployeeCustomerLogFilter{EmployeeUserId: 0})
	require.NoError(t, err)
	assert.Equal(t, 0, stat.Quota)
}

// ---------------------------------------------------------------------------
// Deletion by time: CountOldLog / DeleteOldLogBatch / DeleteOldLog
// ---------------------------------------------------------------------------

func TestDeleteOldLog(t *testing.T) {
	requireLogDB(t)
	uid := nextTestID()
	logCleanupUser(t, uid)
	// pin an old, distinct timestamp far in the past & unique to this user
	oldTs := int64(1_000_000) + int64(nextTestID())
	for i := 0; i < 3; i++ {
		mkLogRow(t, func(l *Log) { l.UserId, l.CreatedAt = uid, oldTs })
	}

	ctx := context.Background()

	// count scoped to our rows only: use a where on user id via CountOldLog is
	// global, so verify our rows are counted within the threshold instead.
	n, err := countScopedOld(uid, oldTs+1)
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)

	// CountOldLog counts ALL rows older than threshold; assert it sees >= ours
	total, err := CountOldLog(ctx, oldTs+1)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(3))

	// Delete in a loop over DeleteOldLogBatch and confirm our rows are gone.
	var deleted int64
	for {
		n, err := DeleteOldLogBatch(ctx, oldTs+1, 2)
		require.NoError(t, err)
		deleted += n
		if n < 2 {
			break
		}
	}
	assert.GreaterOrEqual(t, deleted, int64(3))
	remaining, err := countScopedOld(uid, oldTs+1)
	require.NoError(t, err)
	assert.EqualValues(t, 0, remaining)

	// context already cancelled -> DeleteOldLogBatch returns ctx error
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = DeleteOldLogBatch(cancelledCtx, oldTs, 10)
	assert.Error(t, err)
}

func countScopedOld(userID int, ts int64) (int64, error) {
	var n int64
	err := LOG_DB.Model(&Log{}).Where("user_id = ? AND created_at < ?", userID, ts).Count(&n).Error
	return n, err
}
