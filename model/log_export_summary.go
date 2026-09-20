package model

import (
	"context"
	"errors"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// 聚合维度。时间粒度只做「天」：小时把维度组合数直接乘 24，
// `hour × username × model` 在几千活跃用户下轻易撞上组合数上限而失败，
// 为此要在界面上加一层勉强的劝阻——不如不给这个选项。
// 真要看峰谷分布，那是监控页面该解决的问题，不是导出。
const (
	LogSummaryDimDate      = "date"
	LogSummaryDimUsername  = "username"
	LogSummaryDimGroup     = "group"
	LogSummaryDimModel     = "model_name"
	LogSummaryDimTokenName = "token_name"
	LogSummaryDimChannel   = "channel" // AdminOnly
)

var logSummaryDimensions = []string{
	LogSummaryDimDate,
	LogSummaryDimUsername,
	LogSummaryDimGroup,
	LogSummaryDimModel,
	LogSummaryDimTokenName,
	LogSummaryDimChannel,
}

// LogSummaryDimensions 返回全部可选维度（供接口下发）。
func LogSummaryDimensions() []string {
	out := make([]string, len(logSummaryDimensions))
	copy(out, logSummaryDimensions)
	return out
}

// 维度表头的 i18n key，与列表头同一套命名规则。
func logSummaryDimI18nKey(dim string) string { return "log_export.summary.dim." + dim }

// 指标表头的 i18n key。
func logSummaryMetricI18nKey(metric string) string { return "log_export.summary.metric." + metric }

// logSummaryMetrics 固定全给——聚合行本来就少，按需挑选指标只会多一个要维护的
// 配置项，换不来任何实际收益。
//
// 刻意不含缓存 token：它住在 other 里，为它逐行解析 JSON 会把聚合导出拖慢约 45 倍
// （实测 10.3µs/行 vs 0.23µs/行）；不解析又只能恒为 0，而一列恒为 0 比没有这列更
// 误导人。需要缓存明细请走明细导出。
var logSummaryMetrics = []string{
	"calls", "prompt_tokens", "completion_tokens", "total_tokens",
	"quota", "cost_usd",
}

var (
	ErrLogSummaryNoDimensions     = errors.New("no summary dimensions selected")
	ErrLogSummaryTooManyGroups    = errors.New("too many summary groups")
	ErrLogSummaryUnknownDimension = errors.New("unknown summary dimension")
)

// logSummaryKey 是一行聚合结果的分组键。
// 用可比较的结构体而不是拼接字符串：拼接要处理分隔符转义，
// 而模型名、令牌名都可能含任意字符。
type logSummaryKey struct {
	Date      string
	Username  string
	Group     string
	Model     string
	TokenName string
	ChannelId int
}

// logSummaryRow 一行聚合结果。
//
// 全部用 int64：月度求和远超 int32，prompt_tokens 在 30k RPM 下一天就能
// 越过 20 亿。这类溢出不会报错，只会算出一个负数总量。
type logSummaryRow struct {
	Calls            int64
	PromptTokens     int64
	CompletionTokens int64
	Quota            int64
}

// logSummaryAccumulator 在导出扫描管线里做流式聚合。
//
// 刻意**不用 SQL GROUP BY**：
//  1. 31 天的 logs 表做 hash aggregate，中间结果全落在数据库进程内存里，
//     而这个库同时在承接关系链路的日志写入——把内存压力推给数据库，
//     等于把导出的代价转嫁到关系链路上。
//  2. GROUP BY 是一条长事务查询，绕过了现有的全部资源治理：限速令牌桶、
//     CPU 水位闸门、批间休眠对它统统无效，跑起来就只能等它结束。
//
// 复用既有的 keyset 扫描循环后，上述治理手段原样生效。
type logSummaryAccumulator struct {
	dims      []string
	rows      map[logSummaryKey]*logSummaryRow
	maxGroups int
	loc       *time.Location
}

func newLogSummaryAccumulator(dims []string, loc *time.Location, maxGroups int) (*logSummaryAccumulator, error) {
	if len(dims) == 0 {
		return nil, ErrLogSummaryNoDimensions
	}
	for _, d := range dims {
		if !containsDim(d) {
			return nil, ErrLogSummaryUnknownDimension
		}
	}
	return &logSummaryAccumulator{
		dims:      dims,
		rows:      make(map[logSummaryKey]*logSummaryRow, 1024),
		maxGroups: maxGroups,
		loc:       loc,
	}, nil
}

func containsDim(dim string) bool {
	for _, d := range logSummaryDimensions {
		if d == dim {
			return true
		}
	}
	return false
}

// Add 累加一行日志。
//
// 超过组合数上限时返回错误：宁可让任务带着「请减少维度」的提示失败，
// 也不能把进程 OOM 掉。
func (a *logSummaryAccumulator) Add(l *Log) error {
	key := logSummaryKey{}
	for _, dim := range a.dims {
		switch dim {
		case LogSummaryDimDate:
			// 按导出时区切天：同一批数据在不同时区下的「天」边界不同，
			// 用服务器时区会让跨时区对账对不上。
			key.Date = time.Unix(l.CreatedAt, 0).In(a.loc).Format("2006-01-02")
		case LogSummaryDimUsername:
			key.Username = l.Username
		case LogSummaryDimGroup:
			key.Group = l.Group
		case LogSummaryDimModel:
			key.Model = l.ModelName
		case LogSummaryDimTokenName:
			key.TokenName = l.TokenName
		case LogSummaryDimChannel:
			key.ChannelId = l.ChannelId
		}
	}
	row, ok := a.rows[key]
	if !ok {
		if len(a.rows) >= a.maxGroups {
			return ErrLogSummaryTooManyGroups
		}
		row = &logSummaryRow{}
		a.rows[key] = row
	}
	row.Calls++
	row.PromptTokens += int64(l.PromptTokens)
	row.CompletionTokens += int64(l.CompletionTokens)
	row.Quota += int64(l.Quota)
	return nil
}

// Groups 返回当前累计的分组数。
func (a *logSummaryAccumulator) Groups() int { return len(a.rows) }

// Header 返回聚合文件的表头（维度列 + 指标列）。
func (a *logSummaryAccumulator) Header(translate func(string) string) []string {
	out := make([]string, 0, len(a.dims)+len(logSummaryMetrics))
	for _, dim := range a.dims {
		out = append(out, translate(logSummaryDimI18nKey(dim)))
	}
	for _, metric := range logSummaryMetrics {
		out = append(out, translate(logSummaryMetricI18nKey(metric)))
	}
	return out
}

// Rows 把聚合结果整理成可写出的行，按维度值字典序排序，保证同样的数据
// 每次导出得到同样的文件。
func (a *logSummaryAccumulator) Rows() [][]string {
	keys := make([]logSummaryKey, 0, len(a.rows))
	for key := range a.rows {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return summaryKeyLess(keys[i], keys[j])
	})

	out := make([][]string, 0, len(keys))
	for _, key := range keys {
		row := a.rows[key]
		cells := make([]string, 0, len(a.dims)+len(logSummaryMetrics))
		for _, dim := range a.dims {
			cells = append(cells, summaryDimValue(key, dim))
		}
		total := row.PromptTokens + row.CompletionTokens
		usd := common.QuotaToUSD(row.Quota)
		cells = append(cells,
			strconv.FormatInt(row.Calls, 10),
			strconv.FormatInt(row.PromptTokens, 10),
			strconv.FormatInt(row.CompletionTokens, 10),
			strconv.FormatInt(total, 10),
			strconv.FormatInt(row.Quota, 10),
			strconv.FormatFloat(usd, 'f', 6, 64),
		)
		out = append(out, cells)
	}
	return out
}

func summaryDimValue(key logSummaryKey, dim string) string {
	switch dim {
	case LogSummaryDimDate:
		return key.Date
	case LogSummaryDimUsername:
		return key.Username
	case LogSummaryDimGroup:
		return key.Group
	case LogSummaryDimModel:
		return key.Model
	case LogSummaryDimTokenName:
		return key.TokenName
	case LogSummaryDimChannel:
		if key.ChannelId == 0 {
			return ""
		}
		return strconv.Itoa(key.ChannelId)
	}
	return ""
}

func summaryKeyLess(a, b logSummaryKey) bool {
	if a.Date != b.Date {
		return a.Date < b.Date
	}
	if a.Username != b.Username {
		return a.Username < b.Username
	}
	if a.Group != b.Group {
		return a.Group < b.Group
	}
	if a.Model != b.Model {
		return a.Model < b.Model
	}
	if a.TokenName != b.TokenName {
		return a.TokenName < b.TokenName
	}
	return a.ChannelId < b.ChannelId
}

// NormalizeLogSummaryDimensions 去重并按固定顺序整理维度，丢弃未知取值。
// 渠道维度对非管理员不可用。
func NormalizeLogSummaryDimensions(dims []string, isAdmin bool) []string {
	seen := make(map[string]bool, len(dims))
	for _, d := range dims {
		d = strings.TrimSpace(d)
		if d == "" || !containsDim(d) {
			continue
		}
		if d == LogSummaryDimChannel && !isAdmin {
			continue
		}
		seen[d] = true
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for _, d := range logSummaryDimensions {
		if seen[d] {
			out = append(out, d)
		}
	}
	return out
}

// GetLogSummaryMaxGroups 组合数上限，超限时任务失败而不是把进程 OOM 掉。
func GetLogSummaryMaxGroups() int {
	return operation_setting.GetLogExportSetting().GetSummaryMaxGroups()
}

// summaryScanFields 聚合导出实际需要的数据库列。
//
// 只取维度与指标用到的列，**不取 other**——聚合导出因此完全不碰 JSON 解析，
// 这是它比明细导出快一个数量级的全部原因。
func summaryScanFields(dims []string) []string {
	fields := map[string]bool{
		"id": true, "created_at": true,
		"prompt_tokens": true, "completion_tokens": true, "quota": true,
	}
	for _, dim := range dims {
		switch dim {
		case LogSummaryDimUsername:
			fields["username"] = true
		case LogSummaryDimGroup:
			fields[logGroupCol] = true
		case LogSummaryDimModel:
			fields["model_name"] = true
		case LogSummaryDimTokenName:
			fields["token_name"] = true
		case LogSummaryDimChannel:
			fields["channel_id"] = true
		}
	}
	out := make([]string, 0, len(fields))
	for f := range fields {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// writeLogExportSummary 执行一次聚合汇总导出。
//
// 复用与明细导出完全相同的扫描器（限速闸门、CPU 水位、超时预算、取消、
// 窗口自适应），只把「逐行写文件」换成「逐行累加、扫完一次性写出」。
func writeLogExportSummary(ctx context.Context, job *LogExportJob) (retErr error) {
	loc := time.Local
	if job.Options.Timezone != "" {
		if parsed, err := time.LoadLocation(job.Options.Timezone); err == nil {
			loc = parsed
		}
	}
	acc, err := newLogSummaryAccumulator(job.SummaryDims, loc, GetLogSummaryMaxGroups())
	if err != nil {
		return err
	}

	lang := job.Lang
	translate := func(key string) string { return i18n.Translate(lang, key) }

	rctx := &rowCtx{loc: loc, channelNames: make(map[int]string, 16), translate: translate}
	fields := summaryScanFields(job.SummaryDims)
	rowFilter := job.Filters.HasRowFilter()
	if rowFilter && !slices.Contains(fields, "other") {
		fields = append(fields, "other")
		sort.Strings(fields)
	}

	scanner := newLogExportScanner(job, fields, false, rctx)
	if err := scanner.run(ctx, func(logs []*Log) error {
		for _, l := range logs {
			if rowFilter && !job.Filters.matchesRow(l, rctx) {
				continue
			}
			if err := acc.Add(l); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// 分片写出与登记同 mode=both 共用一份实现。
	if err := appendLogExportSummaryPart(job, acc, translate); err != nil {
		return err
	}
	job.RowCount = job.Parts[len(job.Parts)-1].Rows
	return nil
}

// appendLogExportSummaryPart 把聚合结果作为一个额外分片追加到任务里，
// 返回写出的汇总行数。
//
// 供「明细 + 汇总」模式复用：明细分片写完之后再追加一个汇总分片，
// 两者同在一个 zip 里，靠文件名区分。
func appendLogExportSummaryPart(job *LogExportJob, acc *logSummaryAccumulator, translate func(string) string) error {
	var header []string
	if job.Options.Header {
		header = acc.Header(translate)
	}
	writerOpts := LogExportWriterOptions{
		CSVBOM:    job.Options.CSVBOM,
		Header:    header,
		GzipLevel: operation_setting.GetLogExportSetting().GetGzipLevel(),
	}
	index := len(job.Parts) + 1
	path := LogExportSummaryPartFilePath(job.JobID, index, job.Format)
	writer, err := newLogExportPartWriter(path, job.Format, writerOpts)
	if err != nil {
		return err
	}
	rows := acc.Rows()
	for _, row := range rows {
		if err := writer.WriteRow(row); err != nil {
			_, _ = writer.Close()
			_ = os.Remove(path)
			return err
		}
	}
	bytes, err := writer.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	job.BytesOut += bytes
	job.Parts = append(job.Parts, LogExportPart{
		Index:     index,
		Kind:      LogExportPartKindSummary,
		Path:      path,
		Rows:      int64(len(rows)),
		Bytes:     bytes,
		StartTime: job.Filters.StartTimestamp,
		EndTime:   job.Filters.EndTimestamp,
	})
	return nil
}
