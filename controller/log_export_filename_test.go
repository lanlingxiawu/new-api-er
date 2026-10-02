package controller

import (
	"mime"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fileNameJob(lang string) *model.LogExportJob {
	shanghai := time.FixedZone("CST", 8*3600)
	return &model.LogExportJob{
		Lang:      lang,
		Format:    model.LogExportFormatCSVGz,
		CreatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, shanghai).Unix(),
		Options:   model.LogExportOptions{Timezone: "Asia/Shanghai"},
		Filters: model.LogExportFilter{
			StartTimestamp: time.Date(2026, 9, 1, 0, 0, 0, 0, shanghai).Unix(),
			EndTimestamp:   time.Date(2026, 9, 30, 23, 59, 59, 0, shanghai).Unix(),
		},
		Parts: []model.LogExportPart{{Index: 1}},
	}
}

// 文件名必须说明「哪段数据、什么条件」：时间取数据范围（不是任务创建时间），按导出时区格式化。
func TestLogExportFileBase_AdminRangeAndFilters(t *testing.T) {
	job := fileNameJob("zh-CN")
	assert.Equal(t, "使用日志_2026-09-01至2026-09-30", logExportFileBase(job), "a whole-day range shows dates only")

	job.Filters.Username = "alice"
	job.Filters.ModelName = "gpt-4o"
	job.Filters.TokenName = "prod key"
	job.Filters.Group = "vip"
	job.Filters.ChannelId = 3
	assert.Equal(t, "使用日志_2026-09-01至2026-09-30_用户alice_模型gpt-4o_令牌prod key_分组vip_渠道3", logExportFileBase(job))

	job = fileNameJob("zh-CN")
	job.Filters.UserId = 42
	assert.Equal(t, "使用日志_2026-09-01至2026-09-30_用户ID42", logExportFileBase(job))

	// 写不进文件名的条件也要提示「数据经过筛选」，否则会被当成全量。
	job = fileNameJob("zh-CN")
	quota := 0
	job.Filters.QuotaMax = &quota
	assert.Equal(t, "使用日志_2026-09-01至2026-09-30_含其他筛选", logExportFileBase(job))
	job = fileNameJob("zh-CN")
	job.Filters.AnomalyOnly = true
	assert.Contains(t, logExportFileBase(job), "含其他筛选")

	job = fileNameJob("en")
	job.Filters.ModelName = "gpt-4o"
	assert.Equal(t, "Usage Logs_2026-09-01 to 2026-09-30_model gpt-4o", logExportFileBase(job))
}

func TestLogExportFileDateRange(t *testing.T) {
	shanghai := time.FixedZone("CST", 8*3600)
	at := func(day, hour, minute, second int) int64 {
		return time.Date(2026, 9, day, hour, minute, second, 0, shanghai).Unix()
	}
	for _, tc := range []struct {
		name       string
		start, end int64
		want       string
	}{
		{"whole days", at(1, 0, 0, 0), at(30, 23, 59, 59), "2026-09-01至2026-09-30"},
		{"single day", at(5, 0, 0, 0), at(5, 23, 59, 59), "2026-09-05"},
		{"end at next midnight", at(1, 0, 0, 0), at(3, 0, 0, 0), "2026-09-01至2026-09-02"},
		{"end at 23:59:00", at(1, 0, 0, 0), at(2, 23, 59, 0), "2026-09-01至2026-09-02"},
		{"partial day", at(1, 8, 30, 0), at(1, 18, 0, 0), "2026-09-01 08.30至2026-09-01 18.00"},
		{"start not midnight", at(1, 0, 0, 1), at(2, 23, 59, 59), "2026-09-01 00.00至2026-09-02 23.59"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := fileNameJob("zh-CN")
			job.Filters.StartTimestamp, job.Filters.EndTimestamp = tc.start, tc.end
			assert.Equal(t, "使用日志_"+tc.want, logExportFileBase(job))
		})
	}
	// 按导出时区格式化：同一时刻换到 UTC 就不再是整天。
	job := fileNameJob("zh-CN")
	job.Options.Timezone = "UTC"
	assert.Equal(t, "使用日志_2026-08-31 16.00至2026-09-30 15.59", logExportFileBase(job))
}

// 员工文件名把客户放最前：先看到是谁的，再看到什么表、哪段时间。
func TestLogExportFileBase_EmployeeScope(t *testing.T) {
	job := fileNameJob("zh-CN")
	job.EmployeeScope = &model.EmployeeExportScope{TemplateKey: model.LogExportTemplateCustomerInvoice, TemplateName: "Customer Invoice", CustomerIDs: []int{7}, CustomerName: "alice"}
	job.Filters.ModelName = "gpt-4o"
	assert.Equal(t, "alice_客户对账单_2026-09-01至2026-09-30_模型gpt-4o", logExportFileBase(job), "builtin titles are localized")

	job.Filters.ModelName = ""
	job.EmployeeScope.CustomerName = ""
	job.EmployeeScope.CustomerIDs = []int{7, 8, 9}
	assert.Equal(t, "3个客户_客户对账单_2026-09-01至2026-09-30", logExportFileBase(job))

	job.EmployeeScope.AllCustomers = true
	assert.Equal(t, "全部客户_客户对账单_2026-09-01至2026-09-30", logExportFileBase(job))

	// 员工任务的客户由范围决定，扫描时写入的 UserId 不能混进文件名。
	job.Filters.UserId = 7
	assert.Equal(t, "全部客户_客户对账单_2026-09-01至2026-09-30", logExportFileBase(job))

	job.EmployeeScope = &model.EmployeeExportScope{TemplateKey: "12", TemplateName: "月度/对账", CustomerIDs: []int{7}, CustomerName: "bob"}
	job.Filters.UserId = 0
	assert.Equal(t, "bob_月度-对账_2026-09-01至2026-09-30", logExportFileBase(job), "custom names are used as typed, minus unsafe characters")

	job.Lang = "en"
	job.EmployeeScope = &model.EmployeeExportScope{TemplateKey: model.LogExportTemplateCustomerInvoice, TemplateName: "Customer Invoice", CustomerIDs: []int{7, 8}}
	assert.Equal(t, "2 customers_Customer Invoice_2026-09-01 to 2026-09-30", logExportFileBase(job))
}

func TestSanitizeLogExportFileName(t *testing.T) {
	assert.Equal(t, "a-b-c d", sanitizeLogExportFileName(`a\/b:*?"<>|c d`))
	assert.Equal(t, "x y", sanitizeLogExportFileName("x\t\n  y"))
	assert.Equal(t, "log-export", sanitizeLogExportFileName(`/\:`))
	long := sanitizeLogExportFileName(strings.Repeat("名", 300))
	assert.Equal(t, logExportFileNameMaxRunes, utf8.RuneCountInString(long))
	assert.True(t, utf8.ValidString(long), "truncation must not split a multi-byte character")
}

// 只有一个分片时文件名就是主体名；多个分片打包时明细带序号、汇总带「汇总」，互不重名。
func TestLogExportPartFileName_DistinguishesParts(t *testing.T) {
	job := fileNameJob("zh-CN")
	assert.Equal(t, "使用日志_2026-09-01至2026-09-30.csv.gz", logExportPartFileName(job, &job.Parts[0]))

	job.Parts = []model.LogExportPart{{Index: 1}, {Index: 2}, {Index: 3, Kind: model.LogExportPartKindSummary}}
	names := map[string]bool{}
	for i := range job.Parts {
		names[logExportPartFileName(job, &job.Parts[i])] = true
	}
	assert.Equal(t, map[string]bool{
		"使用日志_2026-09-01至2026-09-30_第1部分.csv.gz": true,
		"使用日志_2026-09-01至2026-09-30_第2部分.csv.gz": true,
		"使用日志_2026-09-01至2026-09-30_汇总.csv.gz":   true,
	}, names)

	job.Format = model.LogExportFormatXlsx
	assert.True(t, strings.HasSuffix(logExportPartFileName(job, &job.Parts[2]), "_汇总.xlsx"))
}

func TestLogExportContentDisposition_EncodesNonASCII(t *testing.T) {
	header := logExportContentDisposition("alice_客户对账单_2026-09-01至2026-09-30.csv.gz")
	disposition, params, err := mime.ParseMediaType(header)
	require.NoError(t, err)
	assert.Equal(t, "attachment", disposition)
	assert.Equal(t, "alice_客户对账单_2026-09-01至2026-09-30.csv.gz", params["filename"])
}

// 不指定分片下载时：单个分片直接给文件，多个分片才打 zip。管理员导出中心指定分片、
// 员工导出不指定，两条入口必须拿到同样的文件。
func TestDownloadLogExport_SinglePartIsServedDirectly(t *testing.T) {
	enableRedis(t)
	requireDB(t)

	admin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	job := mkReadyExportJob(t, admin.Id, "only-part")
	token, err := model.CreateLogExportDownloadToken(job.JobID, admin.Id, 0)
	require.NoError(t, err)

	ctx, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	ctx.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/gzip", rec.Header().Get("Content-Type"))
	assert.Equal(t, "only-part", rec.Body.String())
	_, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(params["filename"], ".csv.gz"), params["filename"])
}

func TestDownloadLogExport_ZipAllParts(t *testing.T) {
	enableRedis(t)
	requireDB(t)

	admin := mkUser(t, func(u *model.User) { u.Role = common.RoleAdminUser })
	job := mkReadyExportJob(t, admin.Id, "part-one")
	second := model.LogExportPartFilePath(job.JobID, 2, model.LogExportFormatCSVGz)
	require.NoError(t, os.WriteFile(second, []byte("part-two"), 0o600))
	t.Cleanup(func() { _ = os.Remove(second) })
	job.Parts = append(job.Parts, model.LogExportPart{Index: 2, Path: second, Rows: 1, Bytes: 8})
	model.UpdateLogExportJob(job)

	token, err := model.CreateLogExportDownloadToken(job.JobID, admin.Id, 0)
	require.NoError(t, err)
	ctx, rec := newCtx(t, http.MethodGet, "/dl/log-export/"+token, nil)
	ctx.Params = gin.Params{{Key: "token", Value: token}}
	DownloadLogExport(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/zip", rec.Header().Get("Content-Type"))
	_, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(params["filename"], ".zip"), params["filename"])
	assert.Greater(t, rec.Body.Len(), 0)
}
