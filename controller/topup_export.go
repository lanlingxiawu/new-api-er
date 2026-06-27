package controller

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// ─── 筛选条件解析（列表 + 导出共用）────────────────────────────────────────────

// validTopUpStatuses 支付状态白名单。非白名单值一律拒绝，绝不拼入 SQL。
var validTopUpStatuses = map[string]bool{
	common.TopUpStatusPending: true,
	common.TopUpStatusSuccess: true,
	common.TopUpStatusFailed:  true,
	common.TopUpStatusExpired: true,
}

// paymentMethodPattern 限定支付方式仅含安全字符（GORM 已参数化，此处为输入正确性校验）。
var paymentMethodPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{1,50}$`)

// parseTopUpListFilter 从请求解析筛选条件。
// isAdmin=false 时强制 UserId=当前登录用户并启用 30 天窗口，忽略客户端传入的 user_id（防越权）。
func parseTopUpListFilter(c *gin.Context, isAdmin bool) (model.TopUpListFilter, error) {
	var f model.TopUpListFilter

	f.Keyword = strings.TrimSpace(c.Query("keyword"))

	tr, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		return f, err
	}
	f.StartTime = tr.StartTime
	f.EndTime = tr.EndTime

	if status := strings.TrimSpace(c.Query("status")); status != "" {
		if !validTopUpStatuses[status] {
			return f, errors.New("invalid status")
		}
		f.Status = status
	}

	if pm := strings.TrimSpace(c.Query("payment_method")); pm != "" {
		if !paymentMethodPattern.MatchString(pm) {
			return f, errors.New("invalid payment_method")
		}
		f.PaymentMethod = pm
	}
	beforeID, err := parseOptionalInt64Query(c, "before_id")
	if err != nil {
		return f, err
	}
	f.BeforeID = beforeID

	if isAdmin {
		// 仅管理员可按任意用户ID筛选；0/空表示全部
		uid, err := parseOptionalIntQuery(c, "user_id")
		if err != nil {
			return f, err
		}
		f.UserId = uid
	} else {
		// 普通用户：强制本人 + 时间窗口，杜绝越权读取他人账单
		f.UserId = c.GetInt("id")
		f.EnforceWindow = true
		if f.UserId <= 0 {
			return f, errors.New("unauthorized")
		}
	}

	return f, nil
}

// ─── CSV 导出 ──────────────────────────────────────────────────────────────────

// topUpExportBatchSize 导出每批拉取行数（命中索引的有界查询）。
const topUpExportBatchSize = 1000

// topUpExportMemLimiter 是 Redis 不可用时的进程内存兜底限流器（与项目现有用户级限流一致，
// 避免「没开 Redis / Redis 故障」时导出彻底失去频控、退化成无限制大查询入口）。
var topUpExportMemLimiter common.InMemoryRateLimiter

// topUpExportCooldownSeconds 单用户导出冷却窗口（秒），从管理后台「系统调优-导出设置」动态读取。
func topUpExportCooldownSeconds() int64 {
	return int64(operation_setting.GetExportSetting().GetRateLimitCooldownSec())
}

// acquireTopUpExportSlot 对单个用户做导出频控（每冷却窗口最多 1 次）：
//   - Redis 可用：用过期锁 SET topup_export:cooldown:user:{id} 1 NX EX ttl，
//     键新建即放行；键已存在=仍在冷却期=拒绝。锁到期自动释放。
//   - Redis 不可用或异常：退回进程内存限流（非降级放行），保证无 Redis 部署/故障期间
//     仍有频控，与 middleware 中用户级限流的兜底策略一致。
//
// 通过 TOPUP_EXPORT_RATE_LIMIT_ENABLE 可整体关闭。
func acquireTopUpExportSlot(c *gin.Context, userID int) bool {
	if !common.GetEnvOrDefaultBool("TOPUP_EXPORT_RATE_LIMIT_ENABLE", true) {
		return true
	}
	if userID <= 0 {
		return false
	}

	ttlSeconds := topUpExportCooldownSeconds()
	key := fmt.Sprintf("topup_export:cooldown:user:%d", userID)

	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		ok, err := common.RDB.SetNX(ctx, key, "1", time.Duration(ttlSeconds)*time.Second).Result()
		if err == nil {
			return ok // true=键新建=允许；false=仍在冷却期
		}
		// Redis 异常：不放行，转用内存限流兜底。
		common.SysError("acquireTopUpExportSlot redis error, fallback to memory limiter: " + err.Error())
	}

	// 内存兜底：每 ttl 窗口允许 1 次。
	topUpExportMemLimiter.Init(common.RateLimitKeyExpirationDuration)
	return topUpExportMemLimiter.Request(key, 1, ttlSeconds)
}

// ExportUserTopUps 用户导出本人充值记录（受 TopUpExportRateLimit 频控）。
func ExportUserTopUps(c *gin.Context) {
	if !operation_setting.GetExportSetting().GetUserExportEnabled() {
		common.ApiErrorI18n(c, i18n.MsgUserBillingExportDisabled)
		return
	}
	filter, err := parseTopUpListFilter(c, false)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	hardCeiling := operation_setting.GetExportSetting().GetHardCeilingRows()
	maxRows := operation_setting.GetPaymentSetting().GetUserExportMaxRows()
	if maxRows <= 0 || maxRows > hardCeiling {
		maxRows = hardCeiling
	}
	exportTopUps(c, filter, maxRows, false)
}

// ExportAllTopUps 管理员导出全平台充值记录（受 TopUpExportRateLimit 频控）。
// 不施加用户行数上限，仅受管理后台「系统调优-导出设置」中的导出硬上限保护。
func ExportAllTopUps(c *gin.Context) {
	filter, err := parseTopUpListFilter(c, true)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	exportTopUps(c, filter, operation_setting.GetExportSetting().GetHardCeilingRows(), true)
}

// exportTopUps 先把整份 CSV 原子化生成到临时文件（恒定内存、keyset 分批、命中索引），
// 全部成功后才设置下载头并把【完整文件】流式回客户端；生成阶段任一批次失败都返回
// JSON 错误（此时尚未写任何下载头），杜绝「看似成功的残缺 CSV」——对账场景下不会把
// 不完整账单误当完整账单。参考台账明细导出的「完整文件或报错」语义。
func exportTopUps(c *gin.Context, filter model.TopUpListFilter, maxRows int, includeUserId bool) {
	// Redis 过期锁频控：拒绝在写任何下载头之前以 429 返回，避免重复触发大查询打满 DB/带宽。
	if !acquireTopUpExportSlot(c, c.GetInt("id")) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgTopupExportRateLimited),
		})
		return
	}

	path, truncated, err := generateTopUpExportFile(c, filter, maxRows, includeUserId)
	if err != nil {
		logger.LogError(c.Request.Context(), "topup export generation failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgTopupExportFailed)
		return
	}
	defer func() { _ = os.Remove(path) }()

	streamTopUpExportFile(c, path, truncated, maxRows)
}

// generateTopUpExportFile 将筛选结果以 keyset 分批写入临时 CSV 文件。
// 成功返回临时文件路径与是否截断；失败时删除临时文件并返回错误（不留半成品）。
func generateTopUpExportFile(c *gin.Context, filter model.TopUpListFilter, maxRows int, includeUserId bool) (path string, truncated bool, err error) {
	if maxRows <= 0 {
		maxRows = operation_setting.GetExportSetting().GetHardCeilingRows()
	}

	f, err := os.CreateTemp("", "topup-export-*.csv")
	if err != nil {
		return "", false, err
	}
	path = f.Name()
	success := false
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()

	// UTF-8 BOM，便于 Excel/WPS 正确识别中文。
	if _, err = f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return "", false, err
	}
	cw := csv.NewWriter(f)
	if err = cw.Write(topUpExportColumnHeaders(c, includeUserId)); err != nil {
		return "", false, err
	}

	written := 0
	lastId := int64(math.MaxInt64)
	for written < maxRows {
		limit := topUpExportBatchSize
		if remain := maxRows - written; remain < limit {
			limit = remain
		}
		batch, ferr := model.FetchTopUpExportBatch(filter, lastId, limit)
		if ferr != nil {
			return "", false, ferr
		}
		if len(batch) == 0 {
			break
		}
		for _, rec := range batch {
			if werr := cw.Write(topUpExportRow(rec, includeUserId)); werr != nil {
				return "", false, werr
			}
			lastId = int64(rec.Id)
			written++
		}
		if len(batch) < limit {
			break // 数据已取尽
		}
	}

	cw.Flush()
	if err = cw.Error(); err != nil {
		return "", false, err
	}

	// 仅在因达到行数上限而停止时判断是否还有更多记录（截断）。
	if written >= maxRows {
		probe, ferr := model.FetchTopUpExportBatch(filter, lastId, 1)
		if ferr != nil {
			return "", false, ferr
		}
		truncated = len(probe) > 0
	}

	if err = f.Sync(); err != nil {
		return "", false, err
	}
	success = true
	return path, truncated, nil
}

// streamTopUpExportFile 设置下载头并把【完整】临时文件流式回客户端。
// 文件已完整生成且带 Content-Length，客户端拿到的要么是完整文件，要么是明显失败的下载。
func streamTopUpExportFile(c *gin.Context, path string, truncated bool, maxRows int) {
	f, err := os.Open(path)
	if err != nil {
		common.ApiErrorMsg(c, "导出失败，请稍后重试")
		return
	}
	defer f.Close()

	filename := fmt.Sprintf("topup-export-%s.csv", time.Now().Format("20060102-150405"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-store")
	// 允许前端读取截断标记头（同源默认可读，跨域时需显式暴露）。
	c.Header("Access-Control-Expose-Headers", "X-Export-Truncated, X-Export-Max-Rows")
	if truncated {
		// 截断时显式告知（含管理员命中内部安全上限的情形），避免误当完整账单。
		c.Header("X-Export-Truncated", "true")
		c.Header("X-Export-Max-Rows", strconv.Itoa(maxRows))
	}
	if info, serr := f.Stat(); serr == nil {
		c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	if _, cerr := io.Copy(c.Writer, f); cerr != nil {
		logger.LogError(c.Request.Context(), "topup export stream copy failed: "+cerr.Error())
	}
}

// topUpExportColumnHeaders 返回本地化表头；includeUserId=true 时含「用户ID」列（管理员导出）。
func topUpExportColumnHeaders(c *gin.Context, includeUserId bool) []string {
	headers := []string{i18n.T(c, i18n.MsgTopUpExportColTradeNo)}
	if includeUserId {
		headers = append(headers, i18n.T(c, i18n.MsgTopUpExportColUserId))
	}
	headers = append(headers,
		i18n.T(c, i18n.MsgTopUpExportColPaymentMethod),
		i18n.T(c, i18n.MsgTopUpExportColAmount),
		i18n.T(c, i18n.MsgTopUpExportColMoney),
		i18n.T(c, i18n.MsgTopUpExportColCurrency),
		i18n.T(c, i18n.MsgTopUpExportColStatus),
		i18n.T(c, i18n.MsgTopUpExportColCreateTime),
		i18n.T(c, i18n.MsgTopUpExportColCompleteTime),
	)
	return headers
}

// topUpExportRow 构造一行 CSV。文本字段经 csvSafeField 转义，防 CSV 公式注入。
func topUpExportRow(r *model.TopUp, includeUserId bool) []string {
	row := []string{csvSafeField(r.TradeNo)}
	if includeUserId {
		row = append(row, strconv.Itoa(r.UserId))
	}
	row = append(row,
		csvSafeField(r.PaymentMethod),
		strconv.FormatInt(r.Amount, 10),
		strconv.FormatFloat(r.Money, 'f', 2, 64),
		csvSafeField(r.PaymentCurrency),
		csvSafeField(r.Status),
		formatExportTime(r.CreateTime),
		formatExportTime(r.CompleteTime),
	)
	return row
}

// formatExportTime 将 Unix 秒格式化为可读时间；0 视为未发生，返回空。
func formatExportTime(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

// csvSafeField 防 CSV/Excel 公式注入：以 = + - @ 或控制字符开头时前置单引号转义。
func csvSafeField(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}
