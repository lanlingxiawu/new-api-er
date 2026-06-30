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

	// 业绩汇总（业绩 = 利润，即客户消耗扣除渠道成本后的净收益）
	RevenueCustomerCount int   `json:"revenue_customer_count" gorm:"default:0"`
	ProfitTotalQuota     int64 `json:"profit_total_quota" gorm:"column:profit_total_quota;default:0"`

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

// GetUserExtensionsByUserIds 批量获取用户扩展信息，返回 userId -> UserExtension map。
// 不存在记录的 userId 不会出现在返回的 map 中（调用方应按零值处理）。
func GetUserExtensionsByUserIds(userIds []int) (map[int]*UserExtension, error) {
	if len(userIds) == 0 {
		return map[int]*UserExtension{}, nil
	}
	var exts []*UserExtension
	if err := DB.Where("user_id IN ?", userIds).Find(&exts).Error; err != nil {
		return nil, err
	}
	result := make(map[int]*UserExtension, len(exts))
	for _, e := range exts {
		result[e.UserId] = e
	}
	return result, nil
}
