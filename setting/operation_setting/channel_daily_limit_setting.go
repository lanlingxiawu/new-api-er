package operation_setting

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// 渠道每日金额上限的全局配置。设计见 docs/design/channel-daily-quota-limit.md §3.2。
const (
	channelDailyLimitConfigName = "channel_daily_limit_setting"

	DefaultChannelDailyLimitTimezone = "Asia/Shanghai"

	MinChannelDailyLimitRetentionDays = 7
	MaxChannelDailyLimitRetentionDays = 3650

	// ChannelDailyLimitNearThreshold 「接近上限」的阈值，筛选与前端配色共用同一个常量，
	// 避免两边各写一份而漂移。
	ChannelDailyLimitNearThreshold = 0.8
)

type ChannelDailyLimitSetting struct {
	// Enabled 功能总开关。关闭后不累计、不禁用、不恢复，可即时关停（Rule 0）。
	Enabled bool `json:"enabled"`
	// Timezone 日切时区，决定每日 00:00 的边界。
	Timezone string `json:"timezone"`
	// RetentionDays 用量历史的保留天数。
	RetentionDays int `json:"retention_days"`
}

var channelDailyLimitSetting = ChannelDailyLimitSetting{
	Enabled:       true,
	Timezone:      DefaultChannelDailyLimitTimezone,
	RetentionDays: 90,
}

var channelDailyLimitSnapshot config.Snapshot[ChannelDailyLimitSetting]

func init() {
	config.GlobalConfig.RegisterSnapshot(channelDailyLimitConfigName, &channelDailyLimitSetting, publishChannelDailyLimitSetting)
}

// publishChannelDailyLimitSetting 在配置草稿锁持有期间执行。
func publishChannelDailyLimitSetting() {
	channelDailyLimitSnapshot.Publish(channelDailyLimitSetting)
}

// GetChannelDailyLimitSnapshot 无锁读取当前配置快照。累计观察者在 relay 结算路径上
// 调用，必须走这个无锁读取，不能走带锁的 GetChannelDailyLimitSetting。
func GetChannelDailyLimitSnapshot() *ChannelDailyLimitSetting {
	s := channelDailyLimitSnapshot.Load()
	if s == nil {
		// RegisterSnapshot 在 init 时已发布，正常运行不会走到这里；兜底也只在草稿锁内复制。
		fallback := GetChannelDailyLimitSetting()
		return &fallback
	}
	return s
}

func GetChannelDailyLimitSetting() ChannelDailyLimitSetting {
	var out ChannelDailyLimitSetting
	config.WithConfigDraft(func() { out = channelDailyLimitSetting })
	return out
}

// ReplaceChannelDailyLimitSetting 整体替换配置并立即发布快照（供测试使用）。
func ReplaceChannelDailyLimitSetting(s ChannelDailyLimitSetting) {
	config.WithConfigDraft(func() {
		channelDailyLimitSetting = s
		publishChannelDailyLimitSetting()
	})
}

// ValidateChannelDailyLimitSetting 校验整份配置。注意：单键 PUT /api/option/ 不会自动
// 调用本函数，controller/option.go 中另有逐键校验，二者必须保持一致。
func ValidateChannelDailyLimitSetting(s ChannelDailyLimitSetting) error {
	if err := ValidateChannelDailyLimitTimezone(s.Timezone); err != nil {
		return err
	}
	if s.RetentionDays < MinChannelDailyLimitRetentionDays || s.RetentionDays > MaxChannelDailyLimitRetentionDays {
		return fmt.Errorf("retention_days must be between %d and %d", MinChannelDailyLimitRetentionDays, MaxChannelDailyLimitRetentionDays)
	}
	return nil
}

// ValidateChannelDailyLimitTimezone 保存时校验时区可解析，非法直接拒绝保存。
func ValidateChannelDailyLimitTimezone(timezone string) error {
	if timezone == "" || timezone == "Local" {
		return nil
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("invalid timezone: %s", timezone)
	}
	return nil
}

var (
	channelDailyLimitLocMu    sync.RWMutex
	channelDailyLimitLocCache = map[string]*time.Location{}
)

// ResolveChannelDailyLimitLocation 解析日切时区，结果按时区串缓存，避免每次
// time.LoadLocation。运行期解析失败时回退 time.Local（保存时已校验过，这里是兜底）。
func ResolveChannelDailyLimitLocation(timezone string) (*time.Location, string) {
	if timezone == "Local" {
		return time.Local, "Local"
	}
	if timezone == "" {
		timezone = DefaultChannelDailyLimitTimezone
	}
	channelDailyLimitLocMu.RLock()
	loc, ok := channelDailyLimitLocCache[timezone]
	channelDailyLimitLocMu.RUnlock()
	if ok {
		return loc, timezone
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Local, "Local"
	}
	channelDailyLimitLocMu.Lock()
	channelDailyLimitLocCache[timezone] = loc
	channelDailyLimitLocMu.Unlock()
	return loc, timezone
}

// ChannelDailyLimitStatDate 返回 ts 所属自然日在配置时区下 00:00 的 unix 秒。
// 夏令时由 time.Date 天然处理；默认时区 Asia/Shanghai 无夏令时。
func ChannelDailyLimitStatDate(ts int64, timezone string) int64 {
	loc, _ := ResolveChannelDailyLimitLocation(timezone)
	t := time.Unix(ts, 0).In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Unix()
}

// ChannelDailyLimitDayRange 返回 ts 所属自然日的 [起, 止) unix 秒区间。
// 观察者用它做原子缓存，跨界才重新计算，避免每次记录都做时区换算。
func ChannelDailyLimitDayRange(ts int64, timezone string) (int64, int64) {
	loc, _ := ResolveChannelDailyLimitLocation(timezone)
	t := time.Unix(ts, 0).In(loc)
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	return start.Unix(), start.AddDate(0, 0, 1).Unix()
}
