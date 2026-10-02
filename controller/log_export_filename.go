package controller

import (
	"mime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
)

// logExportFileNameMaxRunes 文件名主体（不含扩展名）的上限。Windows 整条路径默认 260 字符，
// 超长的筛选值（长令牌名、多个条件）在这里截断，免得解压或另存时报错。
const logExportFileNameMaxRunes = 120

// logExportBuiltinFileTitles 内置模板在文件名里的本地化标题。员工端只开放客户对账单。
var logExportBuiltinFileTitles = map[string]string{
	model.LogExportTemplateCustomerInvoice: i18n.MsgLogExportFileTemplateCustomerInvoice,
}

// logExportFileBase 生成下载文件名的主体（不含扩展名）。
//
//	员工：客户_模板名_日期范围_筛选条件，如 alice_客户对账单_2026-09-01至2026-09-30_模型gpt-4o
//	管理员：使用日志_日期范围_筛选条件，如 使用日志_2026-09-01至2026-09-30_用户alice_分组vip
//
// 客户放最前，拿到文件的人先看到「是谁的」，再看到「什么表、哪段时间」。时间取导出的数据范围
// 而不是任务创建时间，并按导出时区格式化；整天的范围只写日期，否则精确到分钟。
func logExportFileBase(job *model.LogExportJob) string {
	tr := func(key string, args ...map[string]any) string { return i18n.Translate(job.Lang, key, args...) }
	value := func(key, v string) string { return tr(key, map[string]any{"Value": v}) }

	parts := []string{}
	if scope := job.EmployeeScope; scope != nil {
		switch {
		case scope.AllCustomers:
			parts = append(parts, tr(i18n.MsgLogExportFileAllCustomers))
		case scope.CustomerName != "":
			parts = append(parts, scope.CustomerName)
		default:
			parts = append(parts, tr(i18n.MsgLogExportFileCustomerCount, map[string]any{"Count": len(scope.CustomerIDs)}))
		}
		title := scope.TemplateName
		if key, ok := logExportBuiltinFileTitles[scope.TemplateKey]; ok {
			title = tr(key)
		}
		parts = append(parts, title)
	} else {
		parts = append(parts, tr(i18n.MsgLogExportFileUsageLogs))
	}
	parts = append(parts, logExportFileDateRange(job, tr))

	filter := job.Filters
	if job.EmployeeScope == nil {
		if filter.Username != "" {
			parts = append(parts, value(i18n.MsgLogExportFileFilterUser, filter.Username))
		} else if filter.UserId > 0 {
			parts = append(parts, value(i18n.MsgLogExportFileFilterUserId, strconv.Itoa(filter.UserId)))
		}
	}
	if filter.ModelName != "" {
		parts = append(parts, value(i18n.MsgLogExportFileFilterModel, filter.ModelName))
	}
	if filter.TokenName != "" {
		parts = append(parts, value(i18n.MsgLogExportFileFilterToken, filter.TokenName))
	}
	if filter.Group != "" {
		parts = append(parts, value(i18n.MsgLogExportFileFilterGroup, filter.Group))
	}
	if filter.ChannelId != 0 {
		parts = append(parts, value(i18n.MsgLogExportFileFilterChannel, strconv.Itoa(filter.ChannelId)))
	}
	// 数值、异常等条件写不进文件名，但文件名必须提示「数据经过了筛选」，否则会被当成全量。
	if logExportHasMoreFilters(filter) {
		parts = append(parts, tr(i18n.MsgLogExportFileMoreFilters))
	}
	return sanitizeLogExportFileName(strings.Join(parts, "_"))
}

// logExportFileDateRange 按导出时区格式化数据范围。起点是 00:00、终点是 23:59（或次日 00:00）时
// 视为整天，只写日期；起止同一天只写一个日期。其余情况精确到分钟，冒号在 Windows 文件名里非法，用点代替。
func logExportFileDateRange(job *model.LogExportJob, tr func(string, ...map[string]any) string) string {
	loc := time.Local
	if job.Options.Timezone != "" {
		if parsed, err := time.LoadLocation(job.Options.Timezone); err == nil {
			loc = parsed
		}
	}
	start := time.Unix(job.Filters.StartTimestamp, 0).In(loc)
	end := time.Unix(job.Filters.EndTimestamp, 0).In(loc)
	startsAtMidnight := start.Hour() == 0 && start.Minute() == 0 && start.Second() == 0
	endDay := end
	endsAtDayEnd := end.Hour() == 23 && end.Minute() == 59
	if end.Hour() == 0 && end.Minute() == 0 && end.Second() == 0 && end.After(start) {
		endDay, endsAtDayEnd = end.AddDate(0, 0, -1), true
	}
	if startsAtMidnight && endsAtDayEnd {
		from, to := start.Format("2006-01-02"), endDay.Format("2006-01-02")
		if from == to {
			return from
		}
		return tr(i18n.MsgLogExportFileDateRange, map[string]any{"Start": from, "End": to})
	}
	return tr(i18n.MsgLogExportFileDateRange, map[string]any{
		"Start": start.Format("2006-01-02 15.04"), "End": end.Format("2006-01-02 15.04"),
	})
}

func logExportHasMoreFilters(f model.LogExportFilter) bool {
	return f.Charged != nil || f.QuotaMin != nil || f.QuotaMax != nil ||
		f.PromptTokensMin != nil || f.PromptTokensMax != nil ||
		f.CompletionTokensMin != nil || f.CompletionTokensMax != nil ||
		f.UseTimeMin != nil || f.UseTimeMax != nil || f.IsStream != nil ||
		f.MinRetryCount != nil || f.HasRowFilter()
}

// sanitizeLogExportFileName 把任一平台文件名不允许的字符换成连字符，空白统一为单个空格，
// 并按 rune 截断（不能把多字节字符切成半个）。
func sanitizeLogExportFileName(name string) string {
	var b strings.Builder
	last := rune(0)
	for _, r := range name {
		switch {
		case unicode.IsSpace(r):
			r = ' '
		case r == utf8.RuneError || unicode.IsControl(r) || strings.ContainsRune(`\/:*?"<>|`, r):
			r = '-'
		}
		if (r == ' ' || r == '-') && r == last {
			continue
		}
		b.WriteRune(r)
		last = r
	}
	out := strings.Trim(b.String(), "-_. ")
	if runes := []rune(out); len(runes) > logExportFileNameMaxRunes {
		out = strings.TrimRight(string(runes[:logExportFileNameMaxRunes]), "-_. ")
	}
	if out == "" {
		return "log-export"
	}
	return out
}

// logExportPartFileName 生成单个分片的下载文件名。
//
// 只有一个分片时直接用主体名；多个分片打进同一个压缩包时，明细加序号、汇总加「汇总」，
// 拿到包的人不用逐个解开就能分清哪个是明细、哪个是汇总。
func logExportPartFileName(job *model.LogExportJob, part *model.LogExportPart) string {
	ext := ".csv.gz"
	if job.Format == model.LogExportFormatXlsx {
		ext = ".xlsx"
	}
	base := logExportFileBase(job)
	if len(job.Parts) <= 1 {
		return base + ext
	}
	if part.Kind == model.LogExportPartKindSummary {
		return base + "_" + i18n.Translate(job.Lang, i18n.MsgLogExportFileSummary) + ext
	}
	return base + "_" + i18n.Translate(job.Lang, i18n.MsgLogExportFilePart, map[string]any{"Index": part.Index}) + ext
}

// logExportContentDisposition 生成附件头。文件名含中文等非 ASCII 字符时，
// mime.FormatMediaType 按 RFC 2231 输出 filename*=utf-8”...，各主流浏览器都能正确还原。
func logExportContentDisposition(filename string) string {
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); value != "" {
		return value
	}
	return `attachment; filename="log-export"`
}
