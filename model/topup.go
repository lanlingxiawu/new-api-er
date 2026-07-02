package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type TopUp struct {
	Id int `json:"id"`
	// 复合索引 idx_topups_user_create(user_id, create_time) 服务「用户维度 + 时间范围/排序」查询；
	// 保留原单列 index 以兼容历史部署。
	UserId          int     `json:"user_id" gorm:"index;index:idx_topups_user_create,priority:1"`
	Amount          int64   `json:"amount"`
	Money           float64 `json:"money"`
	TradeNo         string  `json:"trade_no" gorm:"unique;type:varchar(255);index"`
	PaymentMethod   string  `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider string  `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	// ProviderOrderId 保存上游支付平台的平台订单号（如 Infini order_id），用于 webhook 双向绑定校验
	ProviderOrderId string `json:"provider_order_id" gorm:"type:varchar(255);default:''"`
	// PaymentCurrency 保存下单时使用的结算币种，供 webhook 校验时与通知币种比对，防止配置变更导致误拒或误充
	PaymentCurrency string `json:"payment_currency" gorm:"type:varchar(10);default:''"`
	// CreateTime 同时参与复合索引（用户维度）与单列索引（管理员全表时间范围扫描）。
	CreateTime   int64  `json:"create_time" gorm:"index:idx_topups_user_create,priority:2;index:idx_topups_create_time"`
	CompleteTime int64  `json:"complete_time"`
	Status       string `json:"status"`
}

const (
	PaymentMethodStripe       = "stripe"
	PaymentMethodCreem        = "creem"
	PaymentMethodWaffo        = "waffo"
	PaymentMethodWaffoPancake = "waffo_pancake"
	PaymentMethodAlipay       = "alipay_official"
	PaymentMethodWechat       = "wechat_official"
	PaymentMethodBalance      = "balance"
	PaymentMethodInfini       = "infini"
)

const (
	PaymentProviderEpay         = "epay"
	PaymentProviderStripe       = "stripe"
	PaymentProviderCreem        = "creem"
	PaymentProviderWaffo        = "waffo"
	PaymentProviderWaffoPancake = "waffo_pancake"
	PaymentProviderAlipay       = "alipay_official"
	PaymentProviderWechat       = "wechat_official"
	PaymentProviderBalance      = "balance"
	PaymentProviderInfini       = "infini"
)

var (
	ErrPaymentMethodMismatch = errors.New("payment method mismatch")
	ErrTopUpNotFound         = errors.New("topup not found")
	ErrTopUpStatusInvalid    = errors.New("topup status invalid")
)

func (topUp *TopUp) Insert() error {
	var err error
	err = DB.Create(topUp).Error
	return err
}

func (topUp *TopUp) Update() error {
	var err error
	err = DB.Save(topUp).Error
	return err
}

func GetTopUpById(id int) *TopUp {
	var topUp *TopUp
	var err error
	err = DB.Where("id = ?", id).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
}

func GetTopUpByTradeNo(tradeNo string) *TopUp {
	var topUp *TopUp
	var err error
	err = DB.Where("trade_no = ?", tradeNo).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
}

func UpdatePendingTopUpStatus(tradeNo string, expectedPaymentProvider string, targetStatus string) error {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		topUp := &TopUp{}
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error; err != nil {
			return ErrTopUpNotFound
		}
		if expectedPaymentProvider != "" && topUp.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}

		topUp.Status = targetStatus
		return tx.Save(topUp).Error
	})
}

// Recharge 处理 Stripe 充值到账。
//
// notifiedAmountCents / notifiedCurrency 为 Stripe webhook 通知的实付金额（美分）与币种：
// 币种必须与下单币种一致；实付金额必须落在 (0, 下单快照金额] 区间内（允许 Stripe 促销码带来的更低实付），
// 校验通过后按下单时锁定的额度快照（topUp.Amount）发放。
func Recharge(referenceId string, customerId string, callerIp string, notifiedAmountCents int64, notifiedCurrency string) (err error) {
	if referenceId == "" {
		return errors.New("未提供支付单号")
	}

	var quota float64
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", referenceId).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderStripe {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		// 到账校验币种与实付金额，按下单时锁定的额度快照发放
		if !strings.EqualFold(notifiedCurrency, topUp.PaymentCurrency) {
			return fmt.Errorf("充值币种不匹配 notified=%s expected=%s", notifiedCurrency, topUp.PaymentCurrency)
		}
		expectedCents := decimal.NewFromFloat(topUp.Money).Mul(decimal.NewFromInt(100)).Round(0).IntPart()
		if notifiedAmountCents <= 0 || notifiedAmountCents > expectedCents {
			return fmt.Errorf("充值金额不匹配 notified_cents=%d expected_cents<=%d", notifiedAmountCents, expectedCents)
		}
		quota = float64(topUp.Amount)
		if quota <= 0 {
			return errors.New("无效的充值额度")
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		err = tx.Save(topUp).Error
		if err != nil {
			return err
		}

		err = tx.Model(&User{}).Where("id = ?", topUp.UserId).Updates(map[string]interface{}{"stripe_customer": customerId, "quota": gorm.Expr("quota + ?", quota)}).Error
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用在线充值成功，充值额度: %v，支付金额：%.2f", logger.FormatQuota(int(quota)), topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodStripe)

	return nil
}

// topUpQueryWindowSeconds 限制充值记录查询的时间窗口（秒）。
const topUpQueryWindowSeconds int64 = 30 * 24 * 60 * 60

// topUpQueryCutoff 返回允许查询的最早 create_time（秒级 Unix 时间戳）。
func topUpQueryCutoff() int64 {
	return common.GetTimestamp() - topUpQueryWindowSeconds
}

// searchTopUpCountHardLimit 列表 COUNT 的安全上限，
// 防止对超大表执行无界 COUNT 触发 DoS。
const searchTopUpCountHardLimit = 10000

// TopUpListFilter 充值记录筛选条件，列表查询与导出共用。
// 所有字段均为可选；零值表示不限制该维度。调用方负责保证 Status/PaymentMethod 已通过白名单校验。
type TopUpListFilter struct {
	UserId        int    // >0 限定单个用户；0 表示全平台（管理员）
	Keyword       string // 按订单号 LIKE 搜索
	StartTime     int64  // 创建时间下限（Unix 秒，含）；0 不限
	EndTime       int64  // 创建时间上限（Unix 秒，含）；0 不限
	Status        string // 支付状态精确匹配；空不限
	PaymentMethod string // 支付方式精确匹配；空不限
	BeforeID      int64  // 可选主键游标，仅返回 id < before_id 的记录
	EnforceWindow bool   // true 时强制 create_time >= 30 天窗口（普通用户）
}

// buildTopUpQuery 按筛选条件构造查询（不含排序/分页/游标）。
// 所有条件均使用 GORM 参数化占位符，keyword 额外经 sanitizeLikePattern 转义，杜绝 SQL 注入。
// 列名（user_id/create_time/status/payment_method/trade_no/id）均非保留字，无需跨库引号处理。
func buildTopUpQuery(tx *gorm.DB, f TopUpListFilter) (*gorm.DB, error) {
	query := tx.Model(&TopUp{})

	if f.UserId > 0 {
		query = query.Where("user_id = ?", f.UserId)
	}

	// 普通用户强制 30 天窗口；与显式 start_time 取较晚者，防止越窗查询。
	minCreate := int64(0)
	if f.EnforceWindow {
		minCreate = topUpQueryCutoff()
	}
	if f.StartTime > minCreate {
		minCreate = f.StartTime
	}
	if minCreate > 0 {
		query = query.Where("create_time >= ?", minCreate)
	}
	if f.EndTime > 0 {
		query = query.Where("create_time <= ?", f.EndTime)
	}

	if f.Status != "" {
		query = query.Where("status = ?", f.Status)
	}
	if f.PaymentMethod != "" {
		query = query.Where("payment_method = ?", f.PaymentMethod)
	}

	if f.Keyword != "" {
		pattern, perr := sanitizeLikePattern(f.Keyword)
		if perr != nil {
			return nil, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}
	if f.BeforeID > 0 {
		query = query.Where("id < ?", f.BeforeID)
	}

	return query, nil
}

func buildTopUpListQuery(tx *gorm.DB, f TopUpListFilter, pageInfo *common.PageInfo) *gorm.DB {
	query := tx.Order("id desc").Limit(pageInfo.GetPageSize())
	if f.BeforeID > 0 {
		return query
	}
	return query.Offset(pageInfo.GetStartIdx())
}

// ListTopUps 按筛选条件分页查询充值记录（列表接口使用）。
func ListTopUps(f TopUpListFilter, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	countFilter := f
	countFilter.BeforeID = 0
	countQuery, berr := buildTopUpQuery(DB, countFilter)
	if berr != nil {
		return nil, 0, berr
	}
	countSubQuery := countQuery.Select("id").Order("id desc").Limit(searchTopUpCountHardLimit)
	if err = DB.Table("(?) as bounded_topups", countSubQuery).Count(&total).Error; err != nil {
		common.SysError("failed to count topups: " + err.Error())
		return nil, 0, errors.New("查询充值记录失败")
	}

	listQuery, berr := buildTopUpQuery(DB, f)
	if berr != nil {
		return nil, 0, berr
	}
	if err = buildTopUpListQuery(listQuery, f, pageInfo).Find(&topups).Error; err != nil {
		common.SysError("failed to list topups: " + err.Error())
		return nil, 0, errors.New("查询充值记录失败")
	}
	return topups, total, nil
}

// FetchTopUpExportBatch 以主键游标（keyset）取一批用于导出的记录：
// 仅返回 id < beforeId 的最多 limit 条，按 id 降序。每批为命中索引的有界查询，
// 不使用 OFFSET 深翻页、不开长事务，批与批之间归还连接，对 DB 友好（Rule 8.3）。
// 首批传 beforeId = math.MaxInt64。
func FetchTopUpExportBatch(f TopUpListFilter, beforeId int64, limit int) ([]*TopUp, error) {
	if limit <= 0 {
		return nil, nil
	}
	query, berr := buildTopUpQuery(DB, f)
	if berr != nil {
		return nil, berr
	}
	var batch []*TopUp
	if err := query.Where("id < ?", beforeId).Order("id desc").Limit(limit).Find(&batch).Error; err != nil {
		common.SysError("failed to fetch topup export batch: " + err.Error())
		return nil, errors.New("导出查询失败")
	}
	return batch, nil
}

// ManualCompleteTopUp 管理员手动完成订单并给用户充值
func ManualCompleteTopUp(tradeNo string, callerIp string) error {
	if tradeNo == "" {
		return errors.New("未提供订单号")
	}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	var userId int
	var quotaToAdd int
	var payMoney float64
	var paymentMethod string

	err := DB.Transaction(func(tx *gorm.DB) error {
		topUp := &TopUp{}
		// 行级锁，避免并发补单
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error; err != nil {
			return errors.New("充值订单不存在")
		}

		// 幂等处理：已成功直接返回
		if topUp.Status == common.TopUpStatusSuccess {
			return nil
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("订单状态不是待支付，无法补单")
		}

		// 计算应充值额度：
		// - Stripe / Infini 动态汇率订单：Amount 为下单时锁定的额度快照，直接使用
		// - 其他订单（如易支付）：Amount 为美元数量，* QuotaPerUnit
		if topUp.PaymentProvider == PaymentProviderStripe || topUp.PaymentProvider == PaymentProviderInfini {
			quotaToAdd = int(topUp.Amount)
		} else {
			dAmount := decimal.NewFromInt(topUp.Amount)
			dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
			quotaToAdd = int(dAmount.Mul(dQuotaPerUnit).IntPart())
		}
		if quotaToAdd <= 0 {
			return errors.New("无效的充值额度")
		}

		// 标记完成
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		// 增加用户额度（立即写库，保持一致性）
		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", gorm.Expr("quota + ?", quotaToAdd)).Error; err != nil {
			return err
		}

		userId = topUp.UserId
		payMoney = topUp.Money
		paymentMethod = topUp.PaymentMethod
		return nil
	})

	if err != nil {
		return err
	}

	// 事务外记录日志，避免阻塞
	RecordTopupLog(userId, fmt.Sprintf("管理员补单成功，充值金额: %v，支付金额：%f", logger.FormatQuota(quotaToAdd), payMoney), callerIp, paymentMethod, "admin")
	return nil
}
func RechargeCreem(referenceId string, customerEmail string, customerName string, callerIp string) (err error) {
	if referenceId == "" {
		return errors.New("未提供支付单号")
	}

	var quota int64
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", referenceId).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderCreem {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		err = tx.Save(topUp).Error
		if err != nil {
			return err
		}

		// Creem 直接使用 Amount 作为充值额度（整数）
		quota = topUp.Amount

		// 构建更新字段，优先使用邮箱，如果邮箱为空则使用用户名
		updateFields := map[string]interface{}{
			"quota": gorm.Expr("quota + ?", quota),
		}

		// 如果有客户邮箱，尝试更新用户邮箱（仅当用户邮箱为空时）
		if customerEmail != "" {
			// 先检查用户当前邮箱是否为空
			var user User
			err = tx.Where("id = ?", topUp.UserId).First(&user).Error
			if err != nil {
				return err
			}

			// 如果用户邮箱为空，则更新为支付时使用的邮箱
			if user.Email == "" {
				updateFields["email"] = customerEmail
			}
		}

		err = tx.Model(&User{}).Where("id = ?", topUp.UserId).Updates(updateFields).Error
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("creem topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用Creem充值成功，充值额度: %v，支付金额：%.2f", quota, topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodCreem)

	return nil
}

func RechargeWaffo(tradeNo string, callerIp string) (err error) {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderWaffo {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess {
			return nil // 幂等：已成功直接返回
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		dAmount := decimal.NewFromInt(topUp.Amount)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quotaToAdd = int(dAmount.Mul(dQuotaPerUnit).IntPart())
		if quotaToAdd <= 0 {
			return errors.New("无效的充值额度")
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", gorm.Expr("quota + ?", quotaToAdd)).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("waffo topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if quotaToAdd > 0 {
		RecordTopupLog(topUp.UserId, fmt.Sprintf("Waffo充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodWaffo)
	}

	return nil
}

func RechargeWaffoPancake(tradeNo string) (err error) {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderWaffoPancake {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess {
			return nil
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		quotaToAdd = int(decimal.NewFromInt(topUp.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
		if quotaToAdd <= 0 {
			return errors.New("无效的充值额度")
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", gorm.Expr("quota + ?", quotaToAdd)).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("waffo pancake topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if quotaToAdd > 0 {
		RecordLog(topUp.UserId, LogTypeTopup, fmt.Sprintf("Waffo Pancake充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money))
	}

	return nil
}

func RechargeAlipay(tradeNo string, callerIp string) (err error) {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderAlipay {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess {
			return nil // 幂等：已成功直接返回
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		dAmount := decimal.NewFromInt(topUp.Amount)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quotaToAdd = int(dAmount.Mul(dQuotaPerUnit).IntPart())
		if quotaToAdd <= 0 {
			return errors.New("无效的充值额度")
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", gorm.Expr("quota + ?", quotaToAdd)).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("alipay topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if quotaToAdd > 0 {
		RecordTopupLog(topUp.UserId, fmt.Sprintf("支付宝充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodAlipay)
	}

	return nil
}

func RechargeWechat(tradeNo string, callerIp string) (err error) {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderWechat {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess {
			return nil // 幂等：已成功直接返回
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		dAmount := decimal.NewFromInt(topUp.Amount)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quotaToAdd = int(dAmount.Mul(dQuotaPerUnit).IntPart())
		if quotaToAdd <= 0 {
			return errors.New("无效的充值额度")
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", gorm.Expr("quota + ?", quotaToAdd)).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("wechat topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if quotaToAdd > 0 {
		RecordTopupLog(topUp.UserId, fmt.Sprintf("微信支付充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodWechat)
	}

	return nil
}

func RechargeInfini(tradeNo string, callerIp string) (err error) {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderInfini {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess {
			return nil // 幂等：已成功直接返回
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		quotaToAdd = int(topUp.Amount)
		if quotaToAdd <= 0 {
			return errors.New("无效的充值额度")
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", gorm.Expr("quota + ?", quotaToAdd)).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		common.SysError("infini topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if quotaToAdd > 0 {
		RecordTopupLog(topUp.UserId, fmt.Sprintf("Infini充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodInfini)
	}

	return nil
}
