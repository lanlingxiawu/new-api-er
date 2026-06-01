package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UserExtension 是所有用户的扩展统计表，当前主要用于员工提成汇总。
// 通过 user_id 与 users 表 1:1 关联，按需懒创建。
type UserExtension struct {
	Id     int `json:"id"`
	UserId int `json:"user_id" gorm:"uniqueIndex;not null"`

	// 提成汇总（quota 单位，与系统其他字段一致）
	CommissionTotalQuota   int64 `json:"commission_total_quota" gorm:"default:0"`
	CommissionPendingQuota int64 `json:"commission_pending_quota" gorm:"default:0"`
	CommissionSettledQuota int64 `json:"commission_settled_quota" gorm:"default:0"`

	// 业绩汇总
	RevenueCustomerCount int   `json:"revenue_customer_count" gorm:"default:0"`
	RevenueTotalQuota    int64 `json:"revenue_total_quota" gorm:"default:0"`

	// JSON 预留扩展（不破坏表结构）
	Extra string `json:"extra,omitempty" gorm:"type:text;default:''"`

	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

// EnsureUserExtension 确保指定用户的扩展记录存在，不存在则插入默认行。
// 使用 ON CONFLICT DO NOTHING 语义，安全并发调用。
func EnsureUserExtension(userId int) error {
	ext := &UserExtension{UserId: userId}
	result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(ext)
	return result.Error
}

// GetUserExtension 获取用户扩展信息，不存在时返回零值结构体（不报错）。
func GetUserExtension(userId int) (*UserExtension, error) {
	var ext UserExtension
	err := DB.Where("user_id = ?", userId).First(&ext).Error
	if err == gorm.ErrRecordNotFound {
		return &UserExtension{UserId: userId}, nil
	}
	return &ext, err
}

// AddCommissionQuota 原子累加提成额度到 user_extensions。
// delta 可为负值（退款冲销）。
func AddCommissionQuota(userId int, delta int64) error {
	if delta == 0 {
		return nil
	}
	// 先确保记录存在
	if err := EnsureUserExtension(userId); err != nil {
		return err
	}
	return DB.Model(&UserExtension{}).
		Where("user_id = ?", userId).
		Updates(map[string]interface{}{
			"commission_total_quota":   gorm.Expr("commission_total_quota + ?", delta),
			"commission_pending_quota": gorm.Expr("commission_pending_quota + ?", delta),
		}).Error
}

// AddRevenueStats 原子累加业绩数据（新客户数 + 消费额度）。
func AddRevenueStats(userId int, revenueQuota int64, newCustomer bool) error {
	if err := EnsureUserExtension(userId); err != nil {
		return err
	}
	updates := map[string]interface{}{
		"revenue_total_quota": gorm.Expr("revenue_total_quota + ?", revenueQuota),
	}
	if newCustomer {
		updates["revenue_customer_count"] = gorm.Expr("revenue_customer_count + 1")
	}
	return DB.Model(&UserExtension{}).
		Where("user_id = ?", userId).
		Updates(updates).Error
}

